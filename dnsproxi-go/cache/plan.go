package cache

// Plan 报文解析：question 提取、RR 遍历（收集 TTL 偏移）、可缓存性判定。
// 对照你 Rust 版 question()/records()/cache_request()/cache_plan()。

import (
	"encoding/binary"
	"fmt"
	"strings"

	"golearn/dnsproxi-go/wire"
)

// Question 解析出的查询问题。对照 Rust question() 返回的 (name, qtype, qend)。
type Question struct {
	Name string // 小写
	Type uint16
	End  int // question 段结束偏移（含 type/class）
}

// ParseQuestion 解析单问题段的 (qname, qtype) 与结束偏移。
// 对照 Rust question()：QDCount 必须 1、class 必须 IN、qtype != 0。
func ParseQuestion(buf []byte) (*Question, error) {
	if len(buf) < 12 {
		return nil, fmt.Errorf("short")
	}
	if binary.BigEndian.Uint16(buf[4:6]) != 1 {
		return nil, fmt.Errorf("QDCount != 1")
	}
	name, next, err := wire.ParseName(buf, 12)
	if err != nil {
		return nil, err
	}
	if next+4 > len(buf) {
		return nil, fmt.Errorf("truncated question")
	}
	qtype := binary.BigEndian.Uint16(buf[next : next+2])
	qclass := binary.BigEndian.Uint16(buf[next+2 : next+4])
	if qclass != 1 {
		return nil, fmt.Errorf("class != IN")
	}
	if qtype == 0 {
		return nil, fmt.Errorf("qtype 0 reserved")
	}
	return &Question{Name: strings.ToLower(name), Type: qtype, End: next + 4}, nil
}

// RR 一条资源记录的元数据。对照 Rust struct Rr。
type RR struct {
	Section int // 0=answer 1=authority 2=additional
	Type    uint16
	Class   uint16
	TTLOff  int
	TTL     uint32
	End     int // 该 RR 的结束偏移
}

// Records 走完所有 RR，收集元数据。截断报文必须整体报错，不能拿半截当可缓存应答。
// 对照 Rust records()。
func Records(buf []byte) ([]RR, error) {
	if len(buf) < 12 {
		return nil, fmt.Errorf("short")
	}
	pos := 12
	qd := int(binary.BigEndian.Uint16(buf[4:6]))
	for range qd {
		_, next, err := wire.ParseName(buf, pos)
		if err != nil {
			return nil, err
		}
		pos = next + 4
		if pos > len(buf) {
			return nil, fmt.Errorf("truncated question")
		}
	}

	var out []RR
	for section := range 3 {
		count := int(binary.BigEndian.Uint16(buf[6+section*2 : 8+section*2]))
		for range count {
			_, next, err := wire.ParseName(buf, pos)
			if err != nil {
				return nil, err
			}
			pos = next
			if pos+10 > len(buf) {
				return nil, fmt.Errorf("truncated rr")
			}
			ttlOff := pos + 4 // type(2)+class(2) 之后
			rr := RR{
				Section: section,
				Type:    binary.BigEndian.Uint16(buf[pos : pos+2]),
				Class:   binary.BigEndian.Uint16(buf[pos+2 : pos+4]),
				TTLOff:  ttlOff,
				TTL:     binary.BigEndian.Uint32(buf[ttlOff : ttlOff+4]),
				End:     pos + 10 + int(binary.BigEndian.Uint16(buf[pos+8:pos+10])),
			}
			if rr.End > len(buf) {
				return nil, fmt.Errorf("rdata overruns")
			}
			out = append(out, rr)
			pos = rr.End
		}
	}
	if pos != len(buf) {
		return nil, fmt.Errorf("trailing %d bytes", len(buf)-pos)
	}
	return out, nil
}

// Request 判定该查询是否可缓存；可缓存时返回提取的问题。
// 对照 Rust cache_request()：RD=1、OPCODE=0、无 EDNS、单问题、
// question 之后没有多余字节（no additional records）的查询。
// AD 位（0x0020）豁免：stub resolver（dig 默认、systemd-resolved）普遍带
// AD 查询，它只表达"希望上游校验"，不改问题的身份（手册 24 节改造题 1）。
// 应答侧不受影响：缓存里的应答 AD 位来自上游（PlanResponse 原样入存），
// Reply 拷贝应答原字节，AD 的真伪由上游负责，不从查询里带。
func Request(buf []byte) (*Question, error) {
	q, err := ParseQuestion(buf)
	if err != nil {
		return nil, err
	}
	if len(buf) < 12 {
		return nil, fmt.Errorf("short")
	}
	flags1, flags2 := buf[2], buf[3]
	// flags1 低字节=0x01（RD=1，opcode/AA/TC=0）；flags2 允许 AD 位（0x20），
	// 其余（RA/zero 位/rcode）必须全 0。
	if flags1 != 1 || flags2&^0x20 != 0 {
		return nil, fmt.Errorf("not plain query")
	}
	// ANCOUNT/NSCOUNT/ARCOUNT 全 0。
	if buf[6] != 0 || buf[7] != 0 || buf[8] != 0 || buf[9] != 0 || buf[10] != 0 || buf[11] != 0 {
		return nil, fmt.Errorf("extra records")
	}
	if q.End != len(buf) {
		return nil, fmt.Errorf("trailing bytes")
	}
	return q, nil
}

// Plan 可缓存应答的规划结果。对照 Rust CachePlan。
type Plan struct {
	Key      Key
	TTLs     []TTLField
	Lifetime uint32 // min TTL，整条过期窗口
}

// PlanResponse 判定上游应答是否可入缓存。
// D2 档只收正常答案（rcode=0 且 answers>0 且 answer 段有匹配 qtype 的 IN 记录）。
// 负缓存（NXDOMAIN/NODATA）是 D7，这里不做。
// 对照 Rust cache_plan() 的 positive 分支。
func PlanResponse(resp, query []byte) (*Plan, error) {
	q, err := Request(query)
	if err != nil {
		return nil, fmt.Errorf("query not cacheable: %w", err)
	}
	if len(resp) < 12 {
		return nil, fmt.Errorf("short resp")
	}
	// QR=1、OPCODE=0、TC=0（resp[2]&0xfa == 0x80），ID 匹配。
	if resp[2]&0xfa != 0x80 || resp[0] != query[0] || resp[1] != query[1] {
		return nil, fmt.Errorf("not a matching response")
	}
	rq, err := ParseQuestion(resp)
	if err != nil {
		return nil, fmt.Errorf("resp question: %w", err)
	}
	if rq.Name != q.Name || rq.Type != q.Type {
		return nil, fmt.Errorf("question mismatch")
	}
	rrs, err := Records(resp)
	if err != nil {
		return nil, fmt.Errorf("resp records: %w", err)
	}
	// OPT（type 41）TTL 高位非 0 = EDNS 扩展 flags，不入缓存（对照 Rust 同款）。
	for _, r := range rrs {
		if r.Type == 41 && r.TTL>>24 != 0 {
			return nil, fmt.Errorf("edns ext flags")
		}
	}
	rcode := resp[3] & 0x0f
	answers := binary.BigEndian.Uint16(resp[6:8])
	if rcode != 0 || answers == 0 {
		return nil, fmt.Errorf("rcode=%d answers=%d (negative is D7)", rcode, answers)
	}
	hasAnswer := false
	var ttls []TTLField
	for _, r := range rrs {
		if r.Type != 41 {
			ttls = append(ttls, TTLField{Off: r.TTLOff, TTL: r.TTL})
		}
		if r.Section == 0 && r.Type == q.Type && r.Class == 1 {
			hasAnswer = true
		}
	}
	if !hasAnswer || len(ttls) == 0 {
		return nil, fmt.Errorf("no matching answer rr")
	}
	min := ttls[0].TTL
	for _, t := range ttls {
		if t.TTL < min {
			min = t.TTL
		}
	}
	return &Plan{Key: Key{Name: q.Name, Type: q.Type}, TTLs: ttls, Lifetime: min}, nil
}
