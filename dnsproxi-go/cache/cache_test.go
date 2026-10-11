package cache

import (
	"bytes"
	"encoding/binary"
	"sync"
	"testing"
	"time"

	"golearn/dnsproxi-go/wire"
)

// ---- 测试工具：手搓 query / response ----

func mkQuery(id uint16, name string, qtype uint16, edns bool) []byte {
	h := wire.Header{ID: id, RD: true, QDCount: 1}
	q := wire.EncodeHeader(h)
	q = append(q, wire.EncodeName(name)...)
	q = append(q, byte(qtype>>8), byte(qtype), 0, 1) // type, IN
	if edns {
		h.ARCount = 1
		q = wire.EncodeHeader(h)
		// OPT record：root name + type 41 + class 4096(udp size) + ttl 0 + rdlen 0
		q = append(q, 0, 0, 41, 0x10, 0, 0, 0, 0, 0, 0, 0)
	}
	return q
}

func mkResp(id uint16, name string, qtype uint16, ttl uint32, edns bool) []byte {
	h := wire.Header{ID: id, QR: true, RD: true, RA: true, QDCount: 1, ANCount: 1}
	r := wire.EncodeHeader(h)
	r = append(r, wire.EncodeName(name)...)
	r = append(r, byte(qtype>>8), byte(qtype), 0, 1)
	// answer：name 指针→12 + type + class + ttl + rdlen 4 + 10.0.0.1
	r = append(r, 0xc0, 0x0c, byte(qtype>>8), byte(qtype), 0, 1)
	var tb [4]byte
	binary.BigEndian.PutUint32(tb[:], ttl)
	r = append(r, tb[:]...)
	r = append(r, 0, 4, 10, 0, 0, 1)
	if edns {
		h.ARCount = 1
		r = wire.EncodeHeader(h)
		r = append(r, 0, 0, 41, 0x10, 0, 0, 0, 0, 0, 0, 0)
	}
	return r
}

// TestParseQuestionAndRecords 基础解析。
func TestParseQuestionAndRecords(t *testing.T) {
	q := mkQuery(1, "www.Example.COM", 1, false)
	pq, err := ParseQuestion(q)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if pq.Name != "www.example.com" || pq.Type != 1 {
		t.Fatalf("wrong: %+v", pq)
	}

	resp := mkResp(1, "www.example.com", 1, 300, false)
	rrs, err := Records(resp)
	if err != nil {
		t.Fatalf("records: %v", err)
	}
	if len(rrs) != 1 || rrs[0].TTL != 300 || rrs[0].Section != 0 {
		t.Fatalf("wrong rrs: %+v", rrs)
	}
}

// TestRequestCacheability 只缓存普通查询：EDNS / 多记录 / 带尾巴都拒。
func TestRequestCacheability(t *testing.T) {
	if _, err := Request(mkQuery(1, "a.com", 1, false)); err != nil {
		t.Fatalf("plain query should be cacheable: %v", err)
	}
	if _, err := Request(mkQuery(1, "a.com", 1, true)); err == nil {
		t.Fatal("EDNS query must not be cacheable (D2 档)")
	}
	q := mkQuery(1, "a.com", 1, false)
	q = append(q, 0, 0, 41, 0) // 尾巴
	if _, err := Request(q); err == nil {
		t.Fatal("trailing bytes must not be cacheable")
	}
}

// TestPlanResponse 正常应答可入缓存；负应答/TC/问题不匹配都拒。
func TestPlanResponse(t *testing.T) {
	query := mkQuery(7, "a.com", 1, false)
	resp := mkResp(7, "a.com", 1, 120, false)
	plan, err := PlanResponse(resp, query)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.Key.Name != "a.com" || plan.Key.Type != 1 || plan.Lifetime != 120 {
		t.Fatalf("wrong plan: %+v", plan)
	}
	if len(plan.TTLs) != 1 || plan.TTLs[0].TTL != 120 {
		t.Fatalf("wrong ttls: %+v", plan.TTLs)
	}

	// RCODE=3（负应答）D2 不缓存。
	neg := mkResp(7, "a.com", 1, 120, false)
	neg[3] = (neg[3] & 0xf0) | 3
	if _, err := PlanResponse(neg, query); err == nil {
		t.Fatal("NXDOMAIN must not be cached in D2")
	}

	// ID 不匹配拒。
	if _, err := PlanResponse(resp, mkQuery(8, "a.com", 1, false)); err == nil {
		t.Fatal("id mismatch must reject")
	}
}

// TestReplyCountdown 缓存回包：ID 换、TTL 倒计时、指针偏移保持。
func TestReplyCountdown(t *testing.T) {
	resp := mkResp(7, "a.com", 1, 300, false)
	plan, err := PlanResponse(resp, mkQuery(7, "a.com", 1, false))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	e := &Entry{Resp: resp, At: time.Now(), TTLs: plan.TTLs, MinTTL: plan.Lifetime}

	// 新查询用不同 ID（客户端每查询新 ID）。
	q2 := mkQuery(99, "a.com", 1, false)
	out := e.Reply(q2)
	if out == nil {
		t.Fatal("want hit")
	}
	h, err := wire.ParseHeader(out)
	if err != nil {
		t.Fatalf("parse reply: %v", err)
	}
	if h.ID != 99 {
		t.Fatalf("want id 99, got %d", h.ID)
	}
	// TTL 应该还是 300（刚缓存）。
	got := binary.BigEndian.Uint32(out[plan.TTLs[0].Off : plan.TTLs[0].Off+4])
	if got > 300 || got < 295 { // 允许解析耗时几秒
		t.Fatalf("ttl should be ~300, got %d", got)
	}

	// 睡 1.2s 再取：TTL 应该倒计时到 ~298。
	time.Sleep(1200 * time.Millisecond)
	out = e.Reply(q2)
	if out == nil {
		t.Fatal("still within ttl")
	}
	got = binary.BigEndian.Uint32(out[plan.TTLs[0].Off : plan.TTLs[0].Off+4])
	if got != 300-e.AgeSecs() {
		t.Fatalf("ttl countdown wrong: got %d want %d", got, 300-e.AgeSecs())
	}
}

// TestReplyExpiry 过期后 Reply 返回 nil，Get 也查不到。
func TestReplyExpiry(t *testing.T) {
	resp := mkResp(1, "a.com", 1, 60, false)
	plan, _ := PlanResponse(resp, mkQuery(1, "a.com", 1, false))
	e := &Entry{Resp: resp, At: time.Now().Add(-61 * time.Second), TTLs: plan.TTLs, MinTTL: plan.Lifetime}
	if !e.Expired() {
		t.Fatal("want expired")
	}
	if out := e.Reply(mkQuery(2, "a.com", 1, false)); out != nil {
		t.Fatal("expired entry must not reply")
	}

	c := New()
	c.Put(Key{Name: "a.com", Type: 1}, e)
	if _, ok := c.Get(Key{Name: "a.com", Type: 1}); ok {
		t.Fatal("expired entry must be invisible via Get")
	}
	if c.Len() != 1 {
		t.Fatal("惰性过期：尸体还在表里（D6 才管）")
	}
}

// TestConcurrentHitMiss 并发读写不 race（-race 下跑）。
func TestConcurrentHitMiss(t *testing.T) {
	c := New()
	query := mkQuery(1, "x.com", 1, false)
	resp := mkResp(1, "x.com", 1, 60, false)
	plan, _ := PlanResponse(resp, query)
	e := &Entry{Resp: resp, At: time.Now(), TTLs: plan.TTLs, MinTTL: plan.Lifetime}
	c.Put(Key{Name: "x.com", Type: 1}, e)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				if entry, ok := c.Get(Key{Name: "x.com", Type: 1}); ok {
					_ = entry.Reply(query)
				}
				c.Put(Key{Name: "y.com", Type: 1}, e)
			}
		}()
	}
	wg.Wait()
}

// TestBytesRoundTrip 守护：resp 字节在缓存后不被改写。
func TestBytesRoundTrip(t *testing.T) {
	resp := mkResp(5, "z.com", 1, 30, false)
	plan, _ := PlanResponse(resp, mkQuery(5, "z.com", 1, false))
	e := &Entry{Resp: resp, At: time.Now(), TTLs: plan.TTLs, MinTTL: plan.Lifetime}
	_ = e.Reply(mkQuery(6, "z.com", 1, false))
	if !bytes.Equal(e.Resp, resp) {
		t.Fatal("cached bytes must not be mutated in place")
	}
}
