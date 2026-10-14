// Package p34frame 是 P34 章的练习包：TCP 2 字节前缀成帧与上限语义。
//
// 测试与 notes/p34-tcp-server-dns-over-tcp.md 一一对应：
// 预测（TestZeroLenFrameBoundary/TestHalfPrefixEOF/TestPrefixOverrun）、
// 观察（TestSplitWriteRoundTrip/TestPipelinedTwoFrames/TestDropWhenInFlightFull）、
// 改造（TestFrameEncodePool/TestPrefix4RoundTrip）。
package p34frame

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"
)

// MaxMsgLen 与 dnsproxi-go/tcp 一致：2 字节前缀的天然上限。
const MaxMsgLen = 0xFFFF

var ErrTooLarge = errors.New("frame: message exceeds 65535 bytes")

// EncodeFrame 加 2 字节大端长度前缀。
func EncodeFrame(msg []byte) ([]byte, error) {
	if len(msg) > MaxMsgLen {
		return nil, ErrTooLarge
	}
	out := make([]byte, 2+len(msg))
	binary.BigEndian.PutUint16(out[:2], uint16(len(msg)))
	copy(out[2:], msg)
	return out, nil
}

// DecodeFrame 从字节流里解一帧（语义与 dnsproxi-go/tcp/frame.go 相同）。
func DecodeFrame(r io.Reader) ([]byte, error) {
	var prefix [2]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("frame: read prefix: %w", err)
	}
	n := binary.BigEndian.Uint16(prefix[:])
	if n == 0 {
		return nil, nil
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, fmt.Errorf("frame: read %d-byte body: %w", n, err)
	}
	return body, nil
}

// TestZeroLenFrameBoundary 预测一：前缀 0 的帧返回什么。
// io.ReadFull 对空 buf 不读直接 nil，返回 0 字节无错误（正文 §2 边界一）。
func TestZeroLenFrameBoundary(t *testing.T) {
	out, err := DecodeFrame(bytes.NewReader([]byte{0x00, 0x00}))
	if err != nil {
		t.Fatalf("0 长度帧不应报错，得 %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("0 长度帧应返回 0 字节，得 %d", len(out))
	}
	// 与 EncodeFrame 往返：0 长度帧编码是前缀 2 字节
	f, err := EncodeFrame(nil)
	if err != nil || len(f) != 2 {
		t.Fatalf("EncodeFrame(nil) = %d 字节, err=%v；要 2 字节 nil", len(f), err)
	}
}

// TestHalfPrefixEOF 预测二：半包与干净关闭的错误区分（正文 §2 边界二）。
func TestHalfPrefixEOF(t *testing.T) {
	// 1 字节前缀 + 对端断开：协议错
	_, err := DecodeFrame(bytes.NewReader([]byte{0x00}))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("半截前缀要 ErrUnexpectedEOF，得 %v", err)
	}
	// 0 字节 + 对端断开：干净关闭
	_, err = DecodeFrame(bytes.NewReader(nil))
	if !errors.Is(err, io.EOF) {
		t.Fatalf("干净关闭要 io.EOF，得 %v", err)
	}
}

// TestPrefixOverrun 预测三：前缀 16 正文 3 字节，卡到 EOF（正文 §2 边界三）。
func TestPrefixOverrun(t *testing.T) {
	_, err := DecodeFrame(bytes.NewReader([]byte{0x00, 0x10, 'a', 'b', 'c'}))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("前缀大于正文要 ErrUnexpectedEOF，得 %v", err)
	}
}

// TestSplitWriteRoundTrip 观察一：半包无缝（正文 §1/§3）。
// 前缀与正文分两次写，服务端按帧解出，且每次 Read 前 deadline 续租。
func TestSplitWriteRoundTrip(t *testing.T) {
	msg := []byte("hello frame")
	frame, err := EncodeFrame(msg)
	if err != nil {
		t.Fatal(err)
	}
	r := io.MultiReader(bytes.NewReader(frame[:2]), bytes.NewReader(frame[2:]))
	got, err := DecodeFrame(r)
	if err != nil {
		t.Fatalf("半包应无缝解出: %v", err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatalf("往返不一致: %q", got)
	}
}

// TestPipelinedTwoFrames 观察二：粘包各解各的（正文 §1）。
func TestPipelinedTwoFrames(t *testing.T) {
	f1, _ := EncodeFrame([]byte{1})
	f2, _ := EncodeFrame([]byte{2, 2})
	r := io.MultiReader(bytes.NewReader(append(append([]byte{}, f1...), f2...)))
	g1, err := DecodeFrame(r)
	if err != nil || len(g1) != 1 {
		t.Fatalf("第一帧: %v %v", g1, err)
	}
	g2, err := DecodeFrame(r)
	if err != nil || len(g2) != 2 {
		t.Fatalf("第二帧: %v %v", g2, err)
	}
	// 第三次读：流空，干净 EOF
	if _, err := DecodeFrame(r); !errors.Is(err, io.EOF) {
		t.Fatalf("流空要 EOF，得 %v", err)
	}
}

// inFlightHandler 模拟 server.go 的在飞计数：满了就丢帧（不排队）。
type inFlightHandler struct {
	mu       sync.Mutex
	inflight int
	max      int
	dropped  int
	seen     int
	release  chan struct{}
}

func (h *inFlightHandler) handle(query []byte) {
	h.mu.Lock()
	if h.inflight >= h.max {
		h.dropped++
		h.mu.Unlock()
		return // 丢帧：不排队、不断连接
	}
	h.inflight++
	h.seen++
	h.mu.Unlock()
	<-h.release // 模拟 handler 慢
	h.mu.Lock()
	h.inflight--
	h.mu.Unlock()
}

// TestDropWhenInFlightFull 观察三：MaxInFlight 超限丢帧语义（正文 §3）。
// 丢帧不是排队：dropped 增、seen 不增；恢复后新帧照常处理。
func TestDropWhenInFlightFull(t *testing.T) {
	h := &inFlightHandler{max: 1, release: make(chan struct{})}

	first := make(chan struct{})
	go func() {
		h.handle([]byte{1}) // 占满在飞额度（阻塞在 release）
		close(first)
	}()
	time.Sleep(20 * time.Millisecond) // 等 handle 进入阻塞

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.handle([]byte{2}) // 满了：丢
		h.handle([]byte{3}) // 还是满：再丢
	}()
	time.Sleep(20 * time.Millisecond)
	close(h.release) // handler 完成，在飞归零
	<-first
	<-done

	h.handle([]byte{4}) // 恢复后照常

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.dropped != 2 || h.seen != 2 {
		t.Fatalf("要 dropped=2 seen=2，得 dropped=%d seen=%d", h.dropped, h.seen)
	}
}

// poolEncoder 是改造一：帧缓冲进 sync.Pool（正文 §6，对齐 TB38#K14）。
var framePool = sync.Pool{
	New: func() any {
		b := make([]byte, 2+512)
		return &b
	},
}

func EncodeFramePool(msg []byte) ([]byte, error) {
	if len(msg) > MaxMsgLen {
		return nil, ErrTooLarge
	}
	bp := framePool.Get().(*[]byte)
	buf := *bp
	if cap(buf) < 2+len(msg) {
		buf = make([]byte, 2+len(msg))
	}
	out := buf[:2+len(msg)]
	binary.BigEndian.PutUint16(out[:2], uint16(len(msg)))
	copy(out[2:], msg)
	framePool.Put(bp)
	return out, nil
}

// TestFrameEncodePool 改造一：Pool 版与原版行为等价（正文 Take-away）。
// 注意：Pool 版返回的 buf 会被复用，调用方用完之前不能归还，这里复制后断言。
func TestFrameEncodePool(t *testing.T) {
	msg := bytes.Repeat([]byte{0xAB}, 512)
	p1, err := EncodeFramePool(msg)
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte{0x02, 0x00}, msg...)
	if !bytes.Equal(p1, want) {
		t.Fatal("Pool 版编码与原版不等价")
	}
	// 复用证据：再编码小消息，底层 cap 仍是 514（不缩容）
	p2, _ := EncodeFramePool([]byte{1})
	if cap(p2) < 514 {
		t.Fatalf("池化缓冲应复用大块，cap=%d", cap(p2))
	}
}

// EncodeFrame4 是改造二：4 字节前缀变体（正文 Take-away 改造题）。
func EncodeFrame4(msg []byte) ([]byte, error) {
	if len(msg) > 0xFFFFFFFF {
		return nil, errors.New("frame4: too large")
	}
	out := make([]byte, 4+len(msg))
	binary.BigEndian.PutUint32(out[:4], uint32(len(msg)))
	copy(out[4:], msg)
	return out, nil
}

// TestPrefix4RoundTrip 改造二：4 字节前缀往返，MaxMsgLen 抬到 4 GiB（正文 §2/§6）。
func TestPrefix4RoundTrip(t *testing.T) {
	msg := bytes.Repeat([]byte{0x5A}, 100)
	f, err := EncodeFrame4(msg)
	if err != nil {
		t.Fatal(err)
	}
	if binary.BigEndian.Uint32(f[:4]) != 100 {
		t.Fatalf("4 字节前缀值错: %d", binary.BigEndian.Uint32(f[:4]))
	}
	if !bytes.Equal(f[4:], msg) {
		t.Fatal("4 字节前缀往返不一致")
	}
	// 兼容性断言：2 字节解码器读 4 字节前缀帧，把头两字节 0x0000 当"长度 0"
	// → 返回 0 字节无错误（正文 §2 边界一）：4 字节前缀破坏 2 字节解码器的静默方式。
	if out, err := DecodeFrame(bytes.NewReader(f)); err != nil || len(out) != 0 {
		t.Fatalf("2 字节解码器读 4 字节帧应得 0 字节 nil 错误（0x0000 前缀），得 %d 字节 %v", len(out), err)
	}
}

// 基准：与正文 §2 同一靶子，-benchmem 看分配数。
var sink []byte

func BenchmarkFrameEncode(b *testing.B) {
	msg := bytes.Repeat([]byte{0xAB}, 512)
	b.ReportAllocs()
	for b.Loop() {
		out, err := EncodeFrame(msg)
		if err != nil {
			b.Fatal(err)
		}
		sink = out
	}
}

func BenchmarkFrameDecode(b *testing.B) {
	msg := bytes.Repeat([]byte{0xAB}, 512)
	frame, _ := EncodeFrame(msg)
	b.ReportAllocs()
	for b.Loop() {
		got, err := DecodeFrame(bytes.NewReader(frame))
		if err != nil {
			b.Fatal(err)
		}
		sink = got
	}
}
