package hitpathab

import (
	"encoding/binary"
	"testing"
)

// TTLField 与 cache.TTLField 同构（不 import dnsproxi-go——labs 独立可跑）。
type TTLField struct {
	Off int
	TTL uint32
}

// mkEntry 造一条 A 应答缓存条目：header + question + 1 answer（ttl 300）。
func mkEntry() (resp []byte, ttls []TTLField) {
	// header 12B: ID=0, flags=0x8180, QD=1 AN=1 NS=0 AR=0
	h := []byte{0, 0, 0x81, 0x80, 0, 1, 0, 1, 0, 0, 0, 0}
	qname := []byte{3, 'w', 'w', 'w', 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0}
	qtail := []byte{0, 1, 0, 1} // QTYPE=A QCLASS=IN
	q := append(append(append([]byte{}, h...), qname...), qtail...)

	resp = make([]byte, 0, len(q)+16)
	resp = append(resp, q[2:]...)        // 回包 flags 稍后改——q[0:2] 是 ID，entry 存不带 ID 的？不。
	resp = append([]byte{0, 0}, resp...) // ID 占位（命中时被 query 覆盖）
	resp[2], resp[3] = 0x81, 0x80        // QR=1 RA=1
	rr := make([]byte, 16)
	binary.BigEndian.PutUint16(rr[0:], 0xc00c)
	binary.BigEndian.PutUint16(rr[2:], 1)   // A
	binary.BigEndian.PutUint16(rr[4:], 1)   // IN
	binary.BigEndian.PutUint32(rr[6:], 300) // TTL=300
	binary.BigEndian.PutUint16(rr[10:], 4)
	rr[12], rr[13], rr[14], rr[15] = 1, 2, 3, 4
	resp = append(resp, rr...)
	return resp, []TTLField{{Off: len(q) + 6, TTL: 300}}
}

var sinkN int

// BenchmarkReplyA 基线形态：每次命中 make+copy（= cache.Entry.Reply 的独立复刻）。
func BenchmarkReplyA(b *testing.B) {
	resp, ttls := mkEntry()
	query := make([]byte, len(resp))
	copy(query, resp)
	query[0], query[1] = 0xAB, 0xCD
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		// A 形态：函数内自分配（照抄 cache/reply.go 的结构）
		out := make([]byte, len(resp))
		copy(out, resp)
		out[0], out[1] = query[0], query[1]
		for _, t := range ttls {
			binary.BigEndian.PutUint32(out[t.Off:t.Off+4], t.TTL)
		}
		sinkN += len(out)
	}
}

// BenchmarkReplyB pool 形态：Get/Put buffer，语义同 BuildReplyB。
func BenchmarkReplyB(b *testing.B) {
	resp, ttls := mkEntry()
	query := make([]byte, len(resp))
	copy(query, resp)
	query[0], query[1] = 0xAB, 0xCD
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		pb := BufPool.Get().(*[]byte)
		buf := *pb
		out := BuildReplyB(resp, 0, 300, ttls, query, len(resp)-16, buf)
		sinkN += len(out)
		BufPool.Put(pb)
	}
}
