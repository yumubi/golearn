package cache

import (
	"sync"
	"testing"
	"time"
)

// TestSketchCountAndMin 计数单调；不同 key 不互相污染（取 min 的意义）。
func TestSketchCountAndMin(t *testing.T) {
	s := NewSketch(256, false, true, 8)
	k := key("hot.test")
	for i := 0; i < 100; i++ {
		s.Increment(k)
	}
	if got := s.Frequency(k); got != 15 {
		t.Fatalf("saturated frequency = %d, want 15", got)
	}
	// 没碰过的键：0（挡板关着，没白送的 +1）。
	if got := s.Frequency(key("cold.test")); got != 0 {
		t.Fatalf("untouched key frequency = %d, want 0", got)
	}
}

// TestDoorkeeper 挡板：首次 Increment 只留印子不计数；Frequency 的 +1 语义。
func TestDoorkeeper(t *testing.T) {
	s := NewSketch(256, true, true, 8)
	k := key("once.test")
	s.Increment(k)
	// 第一次见：挡板印子 = "见过一次"，所以估计值 1（0 格 + 挡板 1）。
	if got := s.Frequency(k); got != 1 {
		t.Fatalf("first-seen frequency = %d, want 1 (door bit)", got)
	}
	s.Increment(k)
	if got := s.Frequency(k); got != 2 {
		t.Fatalf("second-seen frequency = %d, want 2", got)
	}
}

// TestAgingHalving 老化：减半 + 挡板清零 + size 修正。
func TestAgingHalving(t *testing.T) {
	s := NewSketch(256, true, true, 8)
	k := key("hot.test")
	for i := 0; i < 20; i++ {
		s.Increment(k)
	}
	before := s.Frequency(k)
	s.Reset()
	after := s.Frequency(k)
	// 20 次 ≈ 15 饱和 → 减半到 7，挡板清零后无 +1。允许边界 ±1。
	if after > before/2+1 || after == 0 {
		t.Fatalf("after reset frequency = %d (before %d), want ~half", after, before)
	}
	if s.Resets() != 1 {
		t.Fatalf("resets = %d, want 1", s.Resets())
	}
}

// TestAgingUnlocksNewHotspot 老化的存在意义（demo② 正题）：
// 关老化 → 旧热点永久压死新热点；开老化 → reset 后新热点能翻身。
func TestAgingUnlocksNewHotspot(t *testing.T) {
	run := func(aging bool) uint32 {
		s := NewSketch(256, true, aging, 8)
		old := key("old-hot.test")
		for i := 0; i < 100; i++ { // 旧热点打满
			s.Increment(old)
		}
		fresh := key("fresh.test")
		for i := 0; i < 30; i++ { // 新热点来了
			s.Increment(fresh)
		}
		return s.Frequency(fresh)
	}
	// 关老化：fresh 的格子被 old 的计数污染不了（不同行同格才污染），
	// 这里断言的是纯行为：关老化时 fresh 至少 15（30 次 × 一半衰减没有）。
	// 开老化：sample=2560，30 次远不到 reset 阈值——所以这个测试真正验证的
	// 是"reset 时机没被误触发"。真正能看老化翻身的场景在 e2e（长流量）。
	if got := run(false); got < 15 {
		t.Fatalf("aging=off fresh frequency = %d, want >= 15", got)
	}
	if got := run(true); got < 15 {
		t.Fatalf("aging=on fresh frequency = %d, want >= 15", got)
	}
}

// TestAdmitDecision 判决函数：赢进/平输拒/阀门放行。
func TestAdmitDecision(t *testing.T) {
	if !Admit(5, 3, 0) {
		t.Fatal("candidate > victim must admit")
	}
	if Admit(2, 5, 0) {
		t.Fatal("cold candidate must be rejected")
	}
	if Admit(2, 5, 127) {
		t.Fatal("roll&127 != 0 must reject even hot candidate")
	}
	if !Admit(8, 100, 0) {
		t.Fatal("hot candidate (>=6) must get 1/128 pass with roll&127==0")
	}
}

// TestDeadVictimDoesNotGuard 受害者已过期 → 无条件让位，不比频率。
func TestDeadVictimDoesNotGuard(t *testing.T) {
	fc := &fakeClock{ms: 1000}
	restore := fc.install()
	defer restore()

	c := NewCap(2, 0, 42)
	c.PutFull(key("victim.test"), mkEntry(60))
	fc.ms = 2000
	c.PutFull(key("other.test"), mkEntry(60))

	// 把 victim 弄老（TTL 60 已过 → Expired()）。
	e := c.m[key("victim.test")]
	e.At = time.Now().Add(-61 * time.Second)
	if !e.Expired() {
		t.Fatal("setup: victim should be expired")
	}

	// 开准入，旧热点 victim 的频率极高（simulate：提前喂草图）。
	c.Sketch = NewSketch(256, false, true, 8)
	for i := 0; i < 100; i++ {
		c.Sketch.Increment(key("victim.test"))
	}
	// 冷候选人来了。
	c.Sketch.Increment(key("fresh.test"))

	fc.ms = 3000
	ev, rej := c.PutFull(key("fresh.test"), mkEntry(60))
	if rej != nil {
		t.Fatalf("expired victim must not guard the gate: %+v", rej)
	}
	if ev == nil || ev.Key != key("victim.test") {
		t.Fatalf("want victim.test evicted, got %+v", ev)
	}
	if ev.Policy != "expired" {
		t.Fatalf("policy = %s, want expired", ev.Policy)
	}
}

// TestAdmissionBlocksCold 频率门：冷候选人打不过热在位者 → 拒，缓存不动。
func TestAdmissionBlocksCold(t *testing.T) {
	fc := &fakeClock{ms: 1000}
	restore := fc.install()
	defer restore()

	c := NewCap(2, 0, 42)
	c.Sketch = NewSketch(256, false, true, 8)
	c.PutFull(key("hot.test"), mkEntry(60))
	fc.ms = 2000
	c.PutFull(key("warm.test"), mkEntry(60))
	// 喂热在位者。
	for i := 0; i < 50; i++ {
		c.Sketch.Increment(key("hot.test"))
		c.Sketch.Increment(key("warm.test"))
	}
	// 冷候选人（零频）撞上满仓。
	fc.ms = 3000
	ev, rej := c.PutFull(key("cold.test"), mkEntry(60))
	if ev != nil {
		t.Fatalf("cold candidate must not evict anyone: %+v", ev)
	}
	if rej == nil {
		t.Fatal("cold candidate must be rejected")
	}
	if c.Len() != 2 {
		t.Fatalf("rejected insert must not touch cache: len=%d", c.Len())
	}
	if c.Rejects != 1 {
		t.Fatalf("rejects = %d, want 1", c.Rejects)
	}
}

// TestAdmissionAdmitsHot 热候选人打赢 → 正常逐出插入。
func TestAdmissionAdmitsHot(t *testing.T) {
	fc := &fakeClock{ms: 1000}
	restore := fc.install()
	defer restore()

	c := NewCap(2, 0, 42)
	c.Sketch = NewSketch(256, false, true, 8)
	c.PutFull(key("cold1.test"), mkEntry(60))
	fc.ms = 2000
	c.PutFull(key("cold2.test"), mkEntry(60))
	// 冷候选人先在草图里养热（模拟它在上游流里复访过多次）。
	for i := 0; i < 50; i++ {
		c.Sketch.Increment(key("star.test"))
	}
	fc.ms = 3000
	ev, rej := c.PutFull(key("star.test"), mkEntry(60))
	if rej != nil {
		t.Fatalf("hot candidate must not be rejected: %+v", rej)
	}
	if ev == nil {
		t.Fatal("want eviction")
	}
	if c.Len() != 2 {
		t.Fatalf("len=%d", c.Len())
	}
	if _, ok := c.Get(key("star.test")); !ok {
		t.Fatal("star.test must be cached")
	}
}

// TestSketchConcurrent 并发 Increment 不 race、计数不丢（-race 下跑）。
func TestSketchConcurrent(t *testing.T) {
	s := NewSketch(256, false, true, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				s.Increment(key("shared.test"))
			}
		}()
	}
	wg.Wait()
	if got := s.Frequency(key("shared.test")); got != 15 { // 800 次，饱和
		t.Fatalf("saturated = %d, want 15", got)
	}
}

// TestNoSketchAdmissionOff sketch=nil → 满仓照常逐出（D3/D4 回归）。
func TestNoSketchAdmissionOff(t *testing.T) {
	fc := &fakeClock{ms: 1000}
	restore := fc.install()
	defer restore()
	c := NewCap(1, 0, 42)
	c.PutFull(key("a.test"), mkEntry(60))
	fc.ms = 2000
	ev, rej := c.PutFull(key("b.test"), mkEntry(60))
	if rej != nil {
		t.Fatal("admission off must never reject")
	}
	if ev == nil || ev.Key != key("a.test") {
		t.Fatalf("want a.test evicted, got %+v", ev)
	}
}
