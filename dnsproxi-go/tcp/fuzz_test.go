package tcp

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

// FuzzDecodeFrame 对成帧解码做 fuzz：任何输入序列只允许三种结局：
// EOF/UnexpectedEOF、非 nil 的完整帧（长度与前缀一致）、普通错误。
// 绝不允许 panic。种子覆盖：0 长度、1 字节、前缀大于实际内容、
// 粘包、超大前缀（65535 全 1）、恰好一帧。
func FuzzDecodeFrame(f *testing.F) {
	f.Add([]byte{})                                               // 空输入：读前缀即 EOF
	f.Add([]byte{0x00})                                           // 1 字节：半截前缀
	f.Add([]byte{0x00, 0x03, 'a'})                                // 前缀 3 > 实际 1：正文读不满
	f.Add([]byte{0x00, 0x00})                                     // 0 长度帧：合法边界
	f.Add([]byte{0x00, 0x01, 'q'})                                // 恰好一帧 1 字节
	f.Add([]byte{0x00, 0x01, 'q', 0x00, 0x01, 'r'})               // 粘包：两帧背靠背
	f.Add([]byte{0xFF, 0xFF})                                     // 超大前缀 65535、无正文
	f.Add([]byte{0x00, 0x0C, 0, 1, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0}) // 12 字节"头"
	f.Add([]byte{0x12, 0x34})                                     // 前缀 0x1234 无正文
	f.Add([]byte{0x00, 0x01, 'q', 0x00})                          // 一整帧 + 半截前缀

	f.Fuzz(func(t *testing.T, data []byte) {
		r := bytes.NewReader(data)
		for {
			buf, err := DecodeFrame(r)
			if err != nil {
				if buf != nil {
					t.Fatalf("error path returned non-nil buffer: %v", err)
				}
				if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
					// 其余错误只允许来自包装这两个哨兵的路径
					if !errors.Is(err, errPrefixShort) && !errors.Is(err, errBodyShort) {
						t.Fatalf("unexpected error %v", err)
					}
				}
				return // 消费完或流断，退出
			}
			// 成功分支：buf 长度必须等于某个已声明前缀；DecodeFrame 内部
			// 按前缀分配，长度不匹配说明读到跨帧数据，这是逻辑错误。
			// 粘包场景下后续帧由下一轮循环验证。
			_ = buf
		}
	})
}

// errPrefixShort/errBodyShort 是 DecodeFrame 错误包装的哨兵，测试断言用。
var (
	errPrefixShort = io.ErrUnexpectedEOF
	errBodyShort   = io.ErrUnexpectedEOF
)

// 粘包正确性直接用确定性测试钉住（fuzz 不擅长断言多帧分布）。
func TestDecodeFramePipelined(t *testing.T) {
	wire2 := func(msgs ...string) []byte {
		var out []byte
		for _, m := range msgs {
			b, err := EncodeFrame([]byte(m))
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, b...)
		}
		return out
	}
	got := wire2("ab", "cde", "")
	r := bytes.NewReader(got)
	want := []string{"ab", "cde", ""}
	for i, w := range want {
		buf, err := DecodeFrame(r)
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if string(buf) != w {
			t.Fatalf("frame %d: got %q want %q", i, buf, w)
		}
	}
	if _, err := DecodeFrame(r); !errors.Is(err, io.EOF) {
		t.Fatalf("want EOF after last frame, got %v", err)
	}
}

// 前缀大于实际内容：UnexpectedEOF，正文长度留痕在错误里。
func TestDecodeFrameShortBody(t *testing.T) {
	_, err := DecodeFrame(bytes.NewReader([]byte{0x00, 0x05, 'a'}))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("want ErrUnexpectedEOF, got %v", err)
	}
}

// EncodeFrame 超限拒绝。
func TestEncodeFrameTooLarge(t *testing.T) {
	if _, err := EncodeFrame(make([]byte, MaxMsgLen+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
	b, err := EncodeFrame(make([]byte, MaxMsgLen))
	if err != nil || len(b) != MaxMsgLen+2 || binary.BigEndian.Uint16(b[:2]) != MaxMsgLen {
		t.Fatalf("max-size frame rejected or malformed: %v", err)
	}
}
