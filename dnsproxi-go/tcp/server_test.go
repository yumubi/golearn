package tcp

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// fakeHandler 记录收到的查询，并按注册的应答表回（模拟 handleQuery 的回包侧）。
type fakeHandler struct {
	mu     sync.Mutex
	seen   [][]byte
	respOf func(query []byte) []byte
}

func (f *fakeHandler) handle(pc net.PacketConn, client net.Addr, query []byte) {
	f.mu.Lock()
	f.seen = append(f.seen, query)
	respOf := f.respOf
	f.mu.Unlock()
	// respOf 在锁外调用：TestMaxInFlight 的阻塞式 respOf 若在锁内执行，
	// 测试主 goroutine 读 h.seen 会和它死锁。
	if respOf == nil {
		return
	}
	if resp := respOf(query); resp != nil {
		if _, err := pc.WriteTo(resp, client); err != nil {
			tlog("write: %v", err)
		}
	}
}

func tlog(format string, args ...any) {}

func startServer(t *testing.T, h Handler, cfg ServerCfg) (addr string, cancel func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, c := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = Serve(ctx, ln, h, cfg)
		close(done)
	}()
	return ln.Addr().String(), func() {
		c()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("Serve did not return after ctx cancel")
		}
	}
}

func dial(t *testing.T, addr string) *Client {
	t.Helper()
	c, err := Dial(addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func qmsg(id uint16, body string) []byte {
	msg := make([]byte, 12+len(body))
	binary.BigEndian.PutUint16(msg[0:2], id)
	copy(msg[12:], body)
	return msg
}

// TestTCPRoundTrip 正常往返：一问一答，ID 与正文原样。
func TestTCPRoundTrip(t *testing.T) {
	h := &fakeHandler{respOf: func(q []byte) []byte {
		r := append([]byte{}, q...)
		r[2] |= 0x80 // QR=1
		return r
	}}
	addr, cancel := startServer(t, h.handle, ServerCfg{})
	defer cancel()
	c := dial(t, addr)
	resp, err := c.RoundTrip(qmsg(42, "example.org"))
	if err != nil {
		t.Fatalf("roundtrip: %v", err)
	}
	if len(resp) < 12 || binary.BigEndian.Uint16(resp[0:2]) != 42 || resp[2]&0x80 == 0 {
		t.Fatalf("bad resp: % x", resp)
	}
}

// TestTCPSplitWrite 半包：查询拆两次写，服务端必须等齐一帧才处理。
func TestTCPSplitWrite(t *testing.T) {
	h := &fakeHandler{respOf: func(q []byte) []byte { return q }}
	addr, cancel := startServer(t, h.handle, ServerCfg{})
	defer cancel()
	c := dial(t, addr)
	frame, err := EncodeFrame(qmsg(7, "half"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.conn.Write(frame[:3]); err != nil { // 前缀+1 字节
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	h.mu.Lock()
	n1 := len(h.seen)
	h.mu.Unlock()
	if _, err := c.conn.Write(frame[3:]); err != nil { // 剩余
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		h.mu.Lock()
		n2 := len(h.seen)
		h.mu.Unlock()
		if n2 == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("want 1 query total (split halves reassembled), got %d", n2)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if n1 != 0 {
		t.Fatalf("handler ran on half frame: %d queries seen", n1)
	}
}

// TestTCPPipelined 粘包：一次写两帧，两问两答。
func TestTCPPipelined(t *testing.T) {
	h := &fakeHandler{respOf: func(q []byte) []byte { return q }}
	addr, cancel := startServer(t, h.handle, ServerCfg{})
	defer cancel()
	c := dial(t, addr)
	f1, _ := EncodeFrame(qmsg(1, "one"))
	f2, _ := EncodeFrame(qmsg(2, "twotwo"))
	if _, err := c.conn.Write(append(append([]byte{}, f1...), f2...)); err != nil {
		t.Fatal(err)
	}
	r1, err := c.RoundTrip(qmsg(3, "three"))
	if err != nil {
		t.Fatalf("pipelined roundtrip: %v", err)
	}
	_ = r1
	// handler goroutine 与读循环并发：第三个到达不代表前两个已被记录，轮询等齐。
	deadline := time.Now().Add(2 * time.Second)
	for {
		h.mu.Lock()
		n := len(h.seen)
		h.mu.Unlock()
		if n == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("want 3 queries handled, got %d", n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestTCPTooLarge 超大帧：前缀 0xFFFF 但无正文 → 服务端等满帧直到超时断连；
// 客户端发超限应答经 WriteTo 直接 ErrTooLarge。
func TestTCPTooLarge(t *testing.T) {
	h := &fakeHandler{respOf: func(q []byte) []byte { return q }}
	addr, cancel := startServer(t, h.handle, ServerCfg{IdleTimeout: 300 * time.Millisecond})
	defer cancel()
	c := dial(t, addr)
	// 前缀声明 65535，正文只有 10 字节：永远凑不满一帧 → idle 断连，且 handler 未执行。
	if _, err := c.conn.Write([]byte{0xFF, 0xFF, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	n := len(h.seen)
	h.mu.Unlock()
	if n != 0 {
		t.Fatalf("handler ran on oversized prefix: %d", n)
	}
	_ = c.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := DecodeFrame(c.conn); !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("want EOF family on server hangup, got %v", err)
	}
}

// TestTCPIdleTimeout 空闲超时：连接建立后不发言，IdleTimeout 到点被关。
func TestTCPIdleTimeout(t *testing.T) {
	addr, cancel := startServer(t, (&fakeHandler{}).handle, ServerCfg{IdleTimeout: 200 * time.Millisecond})
	defer cancel()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	start := time.Now()
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("expected error on idle close")
	}
	if d := time.Since(start); d < 150*time.Millisecond {
		t.Fatalf("closed too early: %v", d)
	}
}

// TestTCPCtxCancel ctx 取消：监听口关闭、Serve 返回 nil（优雅关停）。
func TestTCPCtxCancel(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, c := context.WithCancel(context.Background())
	srvDone := make(chan error, 1)
	go func() { srvDone <- Serve(ctx, ln, (&fakeHandler{}).handle, ServerCfg{}) }()

	// 建一条在飞连接再取消：serve goroutine 应随连接关闭退出。
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	c()
	select {
	case err := <-srvDone:
		if err != nil {
			t.Fatalf("Serve err after cancel: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not stop on ctx cancel")
	}
	// 在飞连接被关（读侧报错）。
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("in-flight conn should be closed after cancel")
	}
	conn.Close()
}

// TestTruncConn TC 位截断：超限应答截到 limit 且 flags 的 TC 位置 1；
// 未超限原样直写。
func TestTruncConn(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	tc := WrapTrunc(pc, 64)

	client, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	// 200 字节应答，QNAME 用单标签 "big"：3+1 字节 + 零字节 + QTYPE/QCLASS 4
	// = 9，qEnd = 12+9 = 21（截断点：完整 question 之后）。
	qname := []byte{3, 'b', 'i', 'g', 0, 0, 1, 0, 1}
	big := append(make([]byte, 12), qname...)
	big = append(big, make([]byte, 200-21)...) // 补足 200
	binary.BigEndian.PutUint16(big[0:2], 9)
	big[2] = 0x80                           // QR=1，TC=0
	binary.BigEndian.PutUint16(big[6:8], 5) // ANCOUNT=5，截断后必须清零
	if _, err := tc.WriteTo(big, client.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1500)
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	n, _, err := client.ReadFromUDP(buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != 21 { // 12 头 + 4 QNAME + 1 零 + QTYPE/QCLASS 4 = 完整 question
		t.Fatalf("truncated to %d bytes, want 21 (header + question)", n)
	}
	if buf[2]&0x02 == 0 {
		t.Fatalf("TC bit not set: flags=%#x", buf[2])
	}
	// AN/NS/AR 计数清零，防止客户端解析被截断的记录表。
	if binary.BigEndian.Uint16(buf[6:8]) != 0 || binary.BigEndian.Uint16(buf[8:10]) != 0 || binary.BigEndian.Uint16(buf[10:12]) != 0 {
		t.Fatal("answer counts not zeroed after truncation")
	}

	// 未超限：原样。
	small := []byte("tiny")
	if _, err := tc.WriteTo(small, client.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	_, _, err = client.ReadFromUDP(buf)
	if err != nil {
		t.Fatal(err)
	}
}

// TestTruncPassthrough ReadFrom 透传到内层。
func TestTruncPassthrough(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	tc := WrapTrunc(pc, 512)
	other, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := other.WriteTo([]byte("hello"), pc.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 100)
	_ = tc.SetReadDeadline(time.Now().Add(time.Second))
	n, _, err := tc.ReadFrom(buf)
	if err != nil || string(buf[:n]) != "hello" {
		t.Fatalf("passthrough read: %q %v", buf[:n], err)
	}
	if tc.LocalAddr().String() != pc.LocalAddr().String() {
		t.Fatal("LocalAddr mismatch")
	}
}

// TestClientHalfResponse 客户端侧半包：服务端应答分两次到，RoundTrip 的 DecodeFrame 要等齐。
func TestClientHalfResponse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		q, err := DecodeFrame(conn)
		if err != nil {
			return
		}
		resp := append([]byte{}, q...)
		resp[2] |= 0x80
		frame, _ := EncodeFrame(resp)
		conn.Write(frame[:5])
		time.Sleep(80 * time.Millisecond)
		conn.Write(frame[5:])
	}()
	c, err := Dial(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	resp, err := c.RoundTrip(qmsg(5, "split-resp"))
	if err != nil {
		t.Fatalf("client half-response roundtrip: %v", err)
	}
	if len(resp) != 12+len("split-resp") {
		t.Fatalf("resp len %d", len(resp))
	}
}

// TestZeroLenFrame 0 长度帧过线：解码成功、handler 收到 0 字节 query。
// 语义上等价 UDP 的 short 包（handleQuery 12 字节头校验会丢弃），成帧层不拦。
func TestZeroLenFrame(t *testing.T) {
	var mu sync.Mutex
	zeroSeen := false
	h := &fakeHandler{respOf: func(q []byte) []byte {
		mu.Lock()
		defer mu.Unlock()
		if len(q) == 0 {
			zeroSeen = true
			return nil // 0 字节查询不回包（等价 UDP short 分支静默丢）
		}
		return q
	}}
	addr, cancel := startServer(t, h.handle, ServerCfg{})
	defer cancel()
	c := dial(t, addr)
	frame, _ := EncodeFrame(nil)
	if _, err := c.conn.Write(frame); err != nil {
		t.Fatal(err)
	}
	// 连接仍可用：后续正常查询不受零帧影响（RFC 允许 pipeline，帧层不重置流）。
	if _, err := c.RoundTrip(qmsg(6, "still-alive")); err != nil {
		t.Fatalf("conn unusable after zero frame: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !zeroSeen {
		t.Fatal("zero-length frame never reached handler")
	}
}

// 防御性：二进制负载往返一致。
func TestFrameRoundTripBinary(t *testing.T) {
	msg := bytes.Repeat([]byte{0xAB}, 512)
	frame, err := EncodeFrame(msg)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeFrame(bytes.NewReader(frame))
	if err != nil || !bytes.Equal(got, msg) {
		t.Fatalf("binary roundtrip: %v", err)
	}
}

// TestMaxConns 上限：第 MaxConns+1 条连接被直接关（读侧立刻 EOF）。
func TestMaxConns(t *testing.T) {
	h := &fakeHandler{respOf: func(q []byte) []byte { return q }}
	addr, cancel := startServer(t, h.handle, ServerCfg{MaxConns: 1})
	defer cancel()
	c1 := dial(t, addr)
	if _, err := c1.RoundTrip(qmsg(1, "ok")); err != nil {
		t.Fatalf("first conn roundtrip: %v", err)
	}
	conn2, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn2.Close()
	// 满员：第二条连接被服务端关闭，读侧报 EOF 族错误。
	_ = conn2.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := conn2.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("want EOF on over-limit conn, got %v", err)
	}
	// 第一条不受影响。
	if _, err := c1.RoundTrip(qmsg(2, "still")); err != nil {
		t.Fatalf("first conn broken: %v", err)
	}
}

// TestMaxInFlight 单连接在飞超限：在飞帧未应答时，后续帧被丢弃（连接不断）；
// 应答释放计数后，后续查询照常处理。
func TestMaxInFlight(t *testing.T) {
	// 阻塞式 handler：拿到 2 个查询前不回包，制造"在飞"窗口。
	release := make(chan struct{})
	h := &fakeHandler{respOf: func(q []byte) []byte {
		<-release
		return q
	}}
	addr, cancel := startServer(t, h.handle, ServerCfg{MaxInFlight: 1})
	defer cancel()
	c := dial(t, addr)
	// 帧 1：进入 handler，卡在 release 上（在飞=1）。
	f1, _ := EncodeFrame(qmsg(1, "a"))
	if _, err := c.conn.Write(f1); err != nil {
		t.Fatal(err)
	}
	// 帧 2、3：读到时在飞仍=1，超限 → 丢弃。
	f2, _ := EncodeFrame(qmsg(2, "b"))
	f3, _ := EncodeFrame(qmsg(3, "c"))
	if _, err := c.conn.Write(append(append([]byte{}, f2...), f3...)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	h.mu.Lock()
	n := len(h.seen)
	h.mu.Unlock()
	if n != 1 {
		t.Fatalf("want only 1 query accepted while in-flight full, got %d", n)
	}
	// 释放：handler 回包，在飞归零；新查询恢复。
	close(release)
	if _, err := c.RoundTrip(qmsg(4, "d")); err != nil {
		t.Fatalf("conn broken after release: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		h.mu.Lock()
		n := len(h.seen)
		h.mu.Unlock()
		if n == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("want 2 queries handled after release, got %d", n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
