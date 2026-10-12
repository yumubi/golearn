package wire

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func refHeader(b []byte) Header {
	flags := binary.BigEndian.Uint16(b[2:4])
	return Header{
		ID:      binary.BigEndian.Uint16(b[0:2]),
		QR:      flags>>15&1 == 1,
		OpCode:  uint8(flags >> 11 & 0xF),
		AA:      flags>>10&1 == 1,
		TC:      flags>>9&1 == 1,
		RD:      flags>>8&1 == 1,
		RA:      flags>>7&1 == 1,
		RCode:   uint8(flags & 0xF),
		QDCount: binary.BigEndian.Uint16(b[4:6]),
		ANCount: binary.BigEndian.Uint16(b[6:8]),
		NSCount: binary.BigEndian.Uint16(b[8:10]),
		ARCount: binary.BigEndian.Uint16(b[10:12]),
	}
}

// TestHeaderRoundTrip 练习 1：ParseHeader 与手工位移互证。
func TestHeaderRoundTrip(t *testing.T) {
	in := Header{
		ID: 0x1234, QR: true, OpCode: 0, RD: true, RA: true, RCode: 3,
		QDCount: 1, ANCount: 2, NSCount: 0, ARCount: 1,
	}
	b := EncodeHeader(in)
	if len(b) != 12 {
		t.Fatalf("want 12 bytes, got %d", len(b))
	}

	// ParseHeader 已实装（参考实现）：手工位移 refHeader 与它互证。
	got, err := ParseHeader(b)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := refHeader(b)
	if got != want {
		t.Fatalf("parse mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestHeaderTooShort(t *testing.T) {
	if _, err := ParseHeader([]byte{1, 2, 3}); err == nil {
		t.Fatal("want error for short buffer")
	}
}

// TestNameBasic 练习 2：基础域名解析。
func TestNameBasic(t *testing.T) {
	// "www.example.com" 的 wire 格式：
	msg := append([]byte{}, 3, 'w', 'w', 'w', 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0)
	msg = append(msg, 0, 1, 0, 1) // qtype qclass 尾巴

	name, next, err := ParseName(msg, 0)
	if err != nil {
		t.Fatalf("parse name: %v", err)
	}
	if name != "www.example.com" {
		t.Fatalf("name = %q", name)
	}
	// 17 = (1+3)+(1+7)+(1+3)+1(根零字节)
	if next != 17 {
		t.Fatalf("next = %d, want 17", next)
	}
}

// TestNamePointer 练习 3：压缩指针（DNS 报文最妙的字节戏法）。
// 12 字节头之后："www" 在 offset 12，后续 question 用指针指回。
func TestNamePointer(t *testing.T) {
	msg := make([]byte, 12)
	msg = append(msg, 3, 'w', 'w', 'w', 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0) // offset 12
	ptrAt := len(msg)
	msg = append(msg, 0xC0, 12)   // 指针 → offset 12
	msg = append(msg, 0, 1, 0, 1) // qtype/class

	name, next, err := ParseName(msg, ptrAt)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if name != "www.example.com" {
		t.Fatalf("pointer name = %q", name)
	}
	// next 应该跳过指针（2 字节），不是跳到目标。
	if next != ptrAt+2 {
		t.Fatalf("next = %d, want %d", next, ptrAt+2)
	}
}

// TestNamePointerLoop 练习 4：恶意指针环不挂死。
// 0xC0 指向自己 → 必须报错而不是无限循环。
func TestNamePointerLoop(t *testing.T) {
	msg := make([]byte, 12)
	msg = append(msg, 0xC0, 12) // 自指

	if _, _, err := ParseName(msg, 12); err == nil {
		t.Fatal("want error for pointer loop")
	}
}

func TestEncodeName(t *testing.T) {
	got := EncodeName("www.example.com")
	want := []byte{3, 'w', 'w', 'w', 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0}
	if !bytes.Equal(got, want) {
		t.Fatalf("encode = % x, want % x", got, want)
	}
}
