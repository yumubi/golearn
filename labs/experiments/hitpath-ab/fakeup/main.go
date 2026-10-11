// fakeup.go — fake upstream DNS 回包器（labs/experiments/hitpath-ab 复现用）。
// 从 LESSON.md 复现协议重建：热循环收 UDP 查询，回固定 A 记录应答。
// 监听 127.0.0.1:15300（PROXY 上游约定口）。
//
// 用法: go run fakeup.go [-port 15300]
package main

import (
	"encoding/binary"
	"flag"
	"log"
	"net"
)

// buildResp 从查询构造最小 A 应答：echo header(ID/flags 改 QR+RA)+question + 1 条 A 记录。
func buildResp(q []byte) []byte {
	if len(q) < 12 {
		return nil
	}
	qd := binary.BigEndian.Uint16(q[4:6])
	if qd != 1 {
		return nil
	}
	// 找 question 结尾：跳 QNAME（label 序）+ QTYPE + QCLASS
	i := 12
	for {
		if i >= len(q) {
			return nil
		}
		l := int(q[i])
		i++
		if l == 0 {
			break
		}
		i += l
	}
	i += 4 // QTYPE+QCLASS
	if i > len(q) {
		return nil
	}

	resp := make([]byte, 0, i+16)
	resp = append(resp, q[:i]...) // header+question 原样
	resp[2] = 0x81                // QR=1, RD=1
	resp[3] = 0x80                // RA=1
	resp[6], resp[7] = 0, 1       // ANCOUNT=1
	// answer: name ptr 0xc00c, type A, class IN, ttl 300, rdlen 4, 1.2.3.4
	var rr [16]byte
	binary.BigEndian.PutUint16(rr[0:], 0xc00c)
	binary.BigEndian.PutUint16(rr[2:], 1)
	binary.BigEndian.PutUint16(rr[4:], 1)
	binary.BigEndian.PutUint32(rr[6:], 300)
	binary.BigEndian.PutUint16(rr[10:], 4)
	rr[12], rr[13], rr[14], rr[15] = 1, 2, 3, 4
	resp = append(resp, rr[:]...)
	return resp
}

func main() {
	port := flag.Int("port", 15300, "listen port")
	flag.Parse()
	pc, err := net.ListenPacket("udp", "127.0.0.1:"+itoa(*port))
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("fakeup on 127.0.0.1:%d", *port)
	buf := make([]byte, 4096)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			continue
		}
		if resp := buildResp(buf[:n]); resp != nil {
			if _, err := pc.WriteTo(resp, addr); err != nil {
				continue
			}
		}
	}
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}
