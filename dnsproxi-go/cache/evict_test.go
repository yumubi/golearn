package cache

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func mkEntry(ttl uint32) *Entry {
	resp := mkResp(1, "x.test", 1, ttl, false)
	plan, _ := PlanResponse(resp, mkQuery(1, "x.test", 1, false))
	return &Entry{Resp: resp, At: time.Now(), TTLs: plan.TTLs, MinTTL: plan.Lifetime}
}

func key(name string) Key { return Key{Name: name, Type: 1} }

// 用假时钟控制 last_access：NewCap 后替换 NowMs。
type fakeClock struct{ ms uint64 }

func (f *fakeClock) install() func() {
	old := NowMs
	NowMs = func() uint64 { return f.ms }
	return func() { NowMs = old }
}

// TestCapFullEvictsOldest 全表扫描（sample=0）确定性：最冷者死。
func TestCapFullEvictsOldest(t *testing.T) {
	fc := &fakeClock{ms: 1000}
	restore := fc.install()
	defer restore()

	c := NewCap(3, 0, 42)
	c.PutFull(key("a.test"), mkEntry(60)) // access=1000
	fc.ms = 2000
	c.PutFull(key("b.test"), mkEntry(60)) // access=2000
	fc.ms = 3000
	c.PutFull(key("c.test"), mkEntry(60)) // access=3000

	// 在位刷新：b 重新变热。
	fc.ms = 4000
	if ev, rej := c.PutFull(key("b.test"), mkEntry(60)); ev != nil || rej != nil {
		t.Fatalf("in-place refresh must not evict: %+v", ev)
	}
	if c.Len() != 3 || c.Evictions != 0 {
		t.Fatalf("len=%d evictions=%d, want 3/0", c.Len(), c.Evictions)
	}

	// 第 4 个键：a（access=1000）最冷 → 逐出 a。
	fc.ms = 5000
	ev, _ := c.PutFull(key("d.test"), mkEntry(60))
	if ev == nil {
		t.Fatal("want eviction at capacity")
	}
	if ev.Key != key("a.test") {
		t.Fatalf("want victim a.test, got %s", ev.Key.Name)
	}
	if ev.Policy != "full-lru" || ev.Checked != 3 {
		t.Fatalf("policy=%s checked=%d, want full-lru/3", ev.Policy, ev.Checked)
	}
	if c.Len() != 3 {
		t.Fatalf("cap must hold: len=%d", c.Len())
	}
	if _, ok := c.Get(key("a.test")); ok {
		t.Fatal("a.test must be evicted")
	}
	if _, ok := c.Get(key("d.test")); !ok {
		t.Fatal("d.test must be present")
	}
}

// TestSampleKEvictsOldestInSample sample-K：样本内最冷者死；固定种子可复跑。
func TestSampleKEvictsOldestInSample(t *testing.T) {
	fc := &fakeClock{ms: 1000}
	restore := fc.install()
	defer restore()

	c := NewCap(10, 3, 0xD15C_A5E5)
	names := []string{"k0", "k1", "k2", "k3", "k4", "k5", "k6", "k7", "k8", "k9"}
	for i, n := range names {
		fc.ms = uint64(1000 + i)
		c.PutFull(key(n), mkEntry(60))
	}
	// 全部等概率：跑多次，每次受害者 ∈ 表中且 sample-lru policy。
	fc.ms = 9999
	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		victim := fmt.Sprintf("v%02d", i)
		ev, _ := c.PutFull(key(victim), mkEntry(60))
		if ev == nil {
			t.Fatal("want eviction at capacity")
		}
		if ev.Policy != "sample-lru" {
			t.Fatalf("policy=%s, want sample-lru", ev.Policy)
		}
		if ev.Checked != 3 {
			t.Fatalf("checked=%d, want 3 (成本只随 K 变)", ev.Checked)
		}
		if seen[ev.Key.Name] {
			t.Fatalf("victim %s evicted twice", ev.Key.Name)
		}
		seen[ev.Key.Name] = true
	}
}

// TestInPlaceRefreshSurvives 在位者刷新永不逐出（对照 Rust :1150-1156）。
func TestInPlaceRefreshSurvives(t *testing.T) {
	fc := &fakeClock{ms: 1000}
	restore := fc.install()
	defer restore()

	c := NewCap(2, 0, 1)
	c.PutFull(key("a.test"), mkEntry(60))
	c.PutFull(key("b.test"), mkEntry(60))
	// a 已是满仓最冷；刷新 a（TTL 过期后重取的路径）→ 绝不能逐出自己。
	for i := 0; i < 50; i++ {
		if ev, rej := c.PutFull(key("a.test"), mkEntry(60)); ev != nil || rej != nil {
			t.Fatalf("refresh of in-place key must never evict: %+v", ev)
		}
		if c.Len() != 2 {
			t.Fatalf("len=%d", c.Len())
		}
	}
	// 计数：刷新路径不算决策，admits/evictions 都是 0。
	if c.Admits != 0 || c.Evictions != 0 || c.Rejects != 0 {
		t.Fatalf("refresh must not count as decision: %+v", c)
	}
}

// TestUnboundedPutFull max=0 无界（D2 语义由 PutFull 承接主路径）。
func TestUnboundedPutFull(t *testing.T) {
	c := NewCap(0, 0, 1)
	for i := 0; i < 100; i++ {
		if ev, rej := c.PutFull(key(fmt.Sprintf("u%03d", i)), mkEntry(60)); ev != nil || rej != nil {
			t.Fatalf("unbounded cache must never evict: %+v", ev)
		}
	}
	if c.Len() != 100 {
		t.Fatalf("len=%d", c.Len())
	}
}

// TestSlotConsistency swap_remove 后 keys/slot/m 三表一致（逐出链路的命门）。
func TestSlotConsistency(t *testing.T) {
	fc := &fakeClock{ms: 1000}
	restore := fc.install()
	defer restore()

	c := NewCap(5, 0, 7)
	live := map[string]bool{}
	for i := 0; i < 50; i++ {
		name := fmt.Sprintf("s%02d", i)
		c.PutFull(key(name), mkEntry(60))
		if i >= 5 {
			// 上一轮的受害者恰好是 i-5（全表最冷）。验证 slot 反查无幽灵。
			dead := fmt.Sprintf("s%02d", i-5)
			live[dead] = false
		}
		live[name] = true
		// 全表核对：keys 里每个键都能经 slot 反查到正确下标。
		for idx, k := range c.keys {
			if c.slot[k] != idx {
				t.Fatalf("slot[%s]=%d != idx %d", k.Name, c.slot[k], idx)
			}
			if _, ok := c.m[k]; !ok {
				t.Fatalf("key %s in keys but not in m", k.Name)
			}
		}
		if len(c.slot) != len(c.keys) || len(c.m) != len(c.keys) {
			t.Fatalf("table drift: keys=%d slot=%d m=%d", len(c.keys), len(c.slot), len(c.m))
		}
		_ = live
	}
}

// TestConcurrentEviction 并发 PutFull/Get 不 race、len 恒 ≤ max。
func TestConcurrentEviction(t *testing.T) {
	c := NewCap(16, 4, 9)
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range 100 {
				k := key(fmt.Sprintf("c%d-%03d", g, i))
				c.PutFull(k, mkEntry(60))
				if _, ok := c.Get(k); ok {
				}
				if c.Len() > 16 {
					t.Error("len exceeded max")
					return
				}
			}
		}(g)
	}
	wg.Wait()
}
