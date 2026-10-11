package cache

// cache_test.go 之外的场景测试：对齐你 Rust 版 D2 的验收口径
// （4 查同名上游只答 2 次、hit ttl 3→2 倒计、过期重取）。

import (
	"testing"
	"time"
)

// countingStore 包一层 Cache，数真实上游访问次数（=真实 Put 次数）。
func TestD2Scenario(t *testing.T) {
	c := New()
	key := Key{Name: "hit.test", Type: 1}

	// 模拟上游：TTL 3s 的应答。
	resp := mkResp(1, "hit.test", 1, 3, false)
	plan, err := PlanResponse(resp, mkQuery(1, "hit.test", 1, false))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	upstreamHits := 0
	doQuery := func(id uint16) []byte {
		if e, ok := c.Get(key); ok {
			return e.Reply(mkQuery(id, "hit.test", 1, false)) // 命中：不问上游
		}
		upstreamHits++ // miss：问上游并缓存
		e := &Entry{Resp: resp, At: time.Now(), TTLs: plan.TTLs, MinTTL: plan.Lifetime}
		c.Put(key, e)
		return e.Reply(mkQuery(id, "hit.test", 1, false))
	}

	// 4 连查：第 1 次 miss（问上游），2~4 命中。
	q1 := doQuery(10)
	if q1 == nil || upstreamHits != 1 {
		t.Fatalf("first query must miss: hits=%d", upstreamHits)
	}
	for _, id := range []uint16{11, 12, 13} {
		if r := doQuery(id); r == nil {
			t.Fatalf("query %d should hit", id)
		}
	}
	if upstreamHits != 1 {
		t.Fatalf("want upstream answered once, got %d", upstreamHits)
	}

	// hit ttl 倒计时：3 → 2。
	e, _ := c.Get(key)
	if left := e.MinTTL - e.AgeSecs(); left != 3 && left != 2 { // 解析/调度允许 1s 抖动
		t.Fatalf("ttl_left should be 3 or 2, got %d", left)
	}

	// 过期重取：把条目弄老（模拟 4s 过去）。
	e.At = time.Now().Add(-4 * time.Second)
	if _, ok := c.Get(key); ok {
		t.Fatal("expired entry must miss")
	}
	r := doQuery(14)
	if r == nil || upstreamHits != 2 {
		t.Fatalf("after expiry must re-query upstream: hits=%d", upstreamHits)
	}
}

// TestD2QuestionEcho 跨 ID 的 question 回显：缓存里的 question 段
// 与新查询一致（长度相同）时才可复用。
func TestD2QuestionEcho(t *testing.T) {
	resp := mkResp(1, "echo.test", 1, 30, false)
	plan, _ := PlanResponse(resp, mkQuery(1, "echo.test", 1, false))
	e := &Entry{Resp: resp, At: time.Now(), TTLs: plan.TTLs, MinTTL: plan.Lifetime}

	// 同名同 type、不同 ID：命中。
	if out := e.Reply(mkQuery(77, "echo.test", 1, false)); out == nil {
		t.Fatal("same name/type different id should hit")
	}
	// 不同大小写的名字：正常化后应命中（qname 小写进键）。
	if out := e.Reply(mkQuery(78, "ECHO.TEST", 1, false)); out == nil {
		t.Fatal("case-insensitive name should hit")
	}
	// 不同 type：键不同 → 这是新条目的事，Reply 层面 question 长度同但仍该命中
	// （键匹配在 proxy 层做，Reply 只看偏移）——文档化行为：
	if out := e.Reply(mkQuery(79, "echo.test", 28, false)); out == nil {
		t.Fatal("Reply only checks offsets; key match is caller's duty")
	}
}
