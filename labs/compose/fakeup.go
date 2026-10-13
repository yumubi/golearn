package main

import (
	"encoding/binary"
	"log"
	"net"
)

func main() {
	pc, err := net.ListenUDP("udp", &net.UDPAddr{Port: 15300})
	if err != nil {
		log.Fatal(err)
	}
	log.Println("fake upstream on :15300")
	buf := make([]byte, 4096)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			log.Fatal(err)
		}
		if n < 12 {
			continue
		}
		// qname 从 offset 12 读 len+label
		p, parts := 12, []byte{}
		for p < n && buf[p] != 0 {
			l := int(buf[p])
			parts = append(parts, buf[p+1:p+1+l]...)
			parts = append(parts, '.')
			p += 1 + l
		}
		if len(parts) > 0 {
			parts = parts[:len(parts)-1]
		}
		log.Printf("[upstream] %s type=%d", parts, binary.BigEndian.Uint16(buf[p+1:p+3]))
		// 响应：header + question + 压缩指针 A 记录。
		// 查询的 EDNS OPT 在 question 之后，只截取 header+question 段。
		resp := make([]byte, 0, n+16)
		resp = append(resp, buf[:p+5]...)          // header + qname + qtype/qclass
		resp[2] |= 0x80                            // QR=1
		binary.BigEndian.PutUint16(resp[6:8], 1)   // ANCOUNT=1
		binary.BigEndian.PutUint16(resp[10:12], 0) // ARCOUNT=0
		resp = append(resp, 0xc0, 0x0c, 0x00, 0x01, 0x00, 0x01)
		resp = binary.BigEndian.AppendUint32(resp, 60)
		resp = append(resp, 0x00, 0x04, 10, 0, 0, 1)
		pc.WriteTo(resp, addr)
	}
}
