// Package wire 练习手写 DNS 报文解析（对照你 Rust 版 dnsproxi 的 parse_header）。
package wire

import (
	"encoding/binary"
	"fmt"
)

// Header DNS 报文头 12 字节。
type Header struct {
	ID      uint16
	QR      bool // 0=query 1=response
	OpCode  uint8
	AA      bool
	TC      bool
	RD      bool
	RA      bool
	RCode   uint8
	QDCount uint16
	ANCount uint16
	NSCount uint16
	ARCount uint16
}

// ParseHeader 解析 12 字节头（对照你 Rust 版的同款函数）。
// 出参错误：长度不足 12。
func ParseHeader(b []byte) (Header, error) {
	if len(b) < 12 {
		return Header{}, fmt.Errorf("dns header: want 12 bytes, got %d", len(b))
	}
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
	}, nil
}

// EncodeHeader 序列化（测试用对照面）。
func EncodeHeader(h Header) []byte {
	var flags uint16
	if h.QR {
		flags |= 1 << 15
	}
	flags |= uint16(h.OpCode&0xF) << 11
	if h.AA {
		flags |= 1 << 10
	}
	if h.TC {
		flags |= 1 << 9
	}
	if h.RD {
		flags |= 1 << 8
	}
	if h.RA {
		flags |= 1 << 7
	}
	flags |= uint16(h.RCode) & 0xF

	b := make([]byte, 12)
	binary.BigEndian.PutUint16(b[0:2], h.ID)
	binary.BigEndian.PutUint16(b[2:4], flags)
	binary.BigEndian.PutUint16(b[4:6], h.QDCount)
	binary.BigEndian.PutUint16(b[6:8], h.ANCount)
	binary.BigEndian.PutUint16(b[8:10], h.NSCount)
	binary.BigEndian.PutUint16(b[10:12], h.ARCount)
	return b
}

// ParseName 解析 question/rr 里的域名，处理压缩指针（0xC0 开头）。
// 返回 "a.b.c" 形式 + 消耗的字节数（指针算 2 字节，指向目标不计入 next）。
//
// Rust 对照（你的 parse_name）：&b[off..] 切片有借用+边界检查；
// Go 这里每一步都要显式防御，否则 b[off] 越界直接 panic。
func ParseName(b []byte, off int) (name string, next int, err error) {
	if off < 0 || off >= len(b) {
		return "", 0, fmt.Errorf("dns name: offset %d out of range (len %d)", off, len(b))
	}

	var sb []byte
	pos := off
	jumped := false           // 跟随过指针后 next 固定在指针后 2 字节
	visited := map[int]bool{} // 指针环防御：同一目标不许来两次

	for range 128 { // 跳数上限 = 防环第二道保险
		if pos >= len(b) {
			return "", 0, fmt.Errorf("dns name: truncated at %d", pos)
		}
		l := int(b[pos])
		switch {
		case l == 0: // 根，结束
			if !jumped {
				next = pos + 1
			}
			return string(sb), next, nil

		case l&0xC0 == 0xC0: // 压缩指针：14 位偏移
			if pos+1 >= len(b) {
				return "", 0, fmt.Errorf("dns name: truncated pointer at %d", pos)
			}
			target := int(binary.BigEndian.Uint16(b[pos:pos+2]) & 0x3FFF)
			if !jumped {
				next = pos + 2
				jumped = true
			}
			if visited[target] {
				return "", 0, fmt.Errorf("dns name: pointer loop at %d", target)
			}
			visited[target] = true
			pos = target

		case l&0xC0 != 0: // 0x40/0x80 前缀保留，标准查询不该出现
			return "", 0, fmt.Errorf("dns name: bad label 0x%02x at %d", l, pos)

		default: // 普通 label
			if pos+1+l > len(b) {
				return "", 0, fmt.Errorf("dns name: label overruns at %d", pos)
			}
			if len(sb)+l+1 > 255 {
				return "", 0, fmt.Errorf("dns name: longer than 255")
			}
			if len(sb) > 0 {
				sb = append(sb, '.')
			}
			sb = append(sb, b[pos+1:pos+1+l]...)
			pos += 1 + l
		}
	}
	return "", 0, fmt.Errorf("dns name: too many jumps from %d", off)
}

// EncodeName 域名转 wire 格式："a.b" → [1]'a'[1]'b'[0]
func EncodeName(name string) []byte {
	out := make([]byte, 0, len(name)+2)
	start := 0
	for i := 0; i <= len(name); i++ {
		if i == len(name) || name[i] == '.' {
			if i > start {
				out = append(out, byte(i-start))
				out = append(out, name[start:i]...)
			}
			start = i + 1
		}
	}
	return append(out, 0)
}
