package cache

// G5 主动老化测试。对照你 Rust 版 :1392-1476 的四个用例，一个不落：
//   1. 只收尸体不伤活人（换位陷阱的正向验证）
//   2. swap_remove 大批删除下活人零误伤（Rust 版同款 800 条压测）
//   3. 空表 noop；sample=0 = 抽表长次
//   4. Cycle 的 25%→10% 判据 + 预算止步

import (
	"sync"
	"testing"
	"time"
)

// TestExpireRoundReapsOnlyTheDead 到期的删、没到期的留。
func TestExpireRoundReapsOnlyTheDead(t *testing.T) {
	fc := &fakeClock{ms: 1000}
	restore := fc.install()
	defer restore()

	c := NewCap(0, 0, 42)
	c.PutFull(key("dead.test"), mkEntry(60))
	c.PutFull(key("alive.test"), mkEntry(600))
	// Expired 用的是墙钟（AgeSecs = time.Since(At)），fakeClock 只管 LRU。
	// 把 dead 弄老、alive 不动。
	c.m[key("dead.test")].At = time.Now().Add(-61 * time.Second)

	checked, removed := c.ExpireRound(200)
	if checked != 200 {
		t.Fatalf("checked=%d, want 200", checked)
	}
	if removed == 0 {
		t.Fatal("dead.test must be reaped")
	}
	if _, ok := c.Get(key("alive.test")); !ok {
		t.Fatal("alive.test must survive")
	}
	if c.ExpiredActive != uint64(removed) {
		t.Fatalf("expired_active=%d, want %d", c.ExpiredActive, removed)
	}
}

// TestExpireRoundNeverKillsLivingUnderSwapRemove 大批删除时的换位陷阱：
// 800 条里 600 条已死，反复轮询直到表空，活人必须一个不伤。
// （Rust 版 :1422 同款场景——这里也测了缓存一致性 keys/slot/m 三表同步。）
func TestExpireRoundNeverKillsLivingUnderSwapRemove(t *testing.T) {
	fc := &fakeClock{ms: 1000}
	restore := fc.install()
	defer restore()

	c := NewCap(0, 0, 42)
	for i := range 200 {
		c.PutFull(fmtKey("dead%03d", i), mkEntry(60))
	}
	for i := range 600 {
		c.PutFull(fmtKey("alive%03d", i), mkEntry(600))
	}
	for i := range 200 {
		c.m[fmtKey("dead%03d", i)].At = time.Now().Add(-61 * time.Second)
	}

	// 反复轮，直到把 200 具尸体全收干净（有放回抽样覆盖慢，多来几轮；
	// 20 抽/轮 × 500 轮 = 10000 抽，200/800 密度下漏网概率 (1-1/4)^10000 ≈ 0）。
	for range 500 {
		c.ExpireRound(20)
	}
	if n := c.Len(); n != 600 {
		t.Fatalf("len=%d, want 600 (all alive intact)", n)
	}
	for i := range 600 {
		if _, ok := c.Get(fmtKey("alive%03d", i)); !ok {
			t.Fatalf("alive%03d was killed by swap-remove shuffle", i)
		}
	}
}

// TestExpireRoundEmptyAndSampleZero 空表 noop；sample=0 = 抽表长次。
func TestExpireRoundEmptyAndSampleZero(t *testing.T) {
	fc := &fakeClock{ms: 1000}
	restore := fc.install()
	defer restore()

	c := NewCap(0, 0, 42)
	if checked, removed := c.ExpireRound(20); checked != 0 || removed != 0 {
		t.Fatalf("empty store: (%d,%d), want (0,0)", checked, removed)
	}

	c.PutFull(key("a.test"), mkEntry(60))
	// sample=0：抽"表长"次 = 1 次。
	if checked, _ := c.ExpireRound(0); checked != 1 {
		t.Fatalf("sample=0 on 1-entry store: checked=%d, want 1", checked)
	}
}

// TestExpireCycleAvalancheAndBudget 雪崩多轮 + 预算止步。
func TestExpireCycleAvalancheAndBudget(t *testing.T) {
	fc := &fakeClock{ms: 1000}
	restore := fc.install()
	defer restore()

	c := NewCap(0, 0, 42)
	// 300 条全死 → 第一轮 20 抽 20 删（100% > 10% 判据）→ 继续轮。
	for i := range 300 {
		c.PutFull(fmtKey("dead%03d", i), mkEntry(60))
		c.m[fmtKey("dead%03d", i)].At = time.Now().Add(-61 * time.Second)
	}

	rounds, checked, removed, took := c.ExpireCycle(DefaultExpireCfg())
	if removed != 300 {
		t.Fatalf("avalanche removed=%d, want 300", removed)
	}
	if rounds < 2 {
		t.Fatalf("rounds=%d, want >=2 (pct judgment must trigger extra rounds)", rounds)
	}
	if checked < removed {
		t.Fatalf("checked=%d < removed=%d, impossible", checked, removed)
	}
	if took <= 0 {
		t.Fatal("took must be positive")
	}
	if c.ExpireCycles != 1 || c.ExpireRounds != uint64(rounds) {
		t.Fatalf("cycle ledger: cycles=%d rounds=%d", c.ExpireCycles, c.ExpireRounds)
	}
}

// TestExpireCycleStopsOnLowPct 低过期率 → 一轮就停（自适应的核心）。
func TestExpireCycleStopsOnLowPct(t *testing.T) {
	fc := &fakeClock{ms: 1000}
	restore := fc.install()
	defer restore()

	c := NewCap(0, 0, 42)
	for i := range 100 {
		c.PutFull(fmtKey("alive%03d", i), mkEntry(600))
	}
	// 只有 1 条尸体：20 抽里大概率 ≤2 死（10% 判据不触发）。
	c.PutFull(key("dead.test"), mkEntry(60))
	c.m[key("dead.test")].At = time.Now().Add(-61 * time.Second)

	rounds, _, _, _ := c.ExpireCycle(DefaultExpireCfg())
	if rounds != 1 {
		t.Fatalf("low-stale cycle took %d rounds, want 1", rounds)
	}
}

// TestRunExpirerStopsOnDone 关停信号：channel 关闭、goroutine 退出。
func TestRunExpirerStopsOnDone(t *testing.T) {
	fc := &fakeClock{ms: 1000}
	restore := fc.install()
	defer restore()

	c := NewCap(0, 0, 42)
	done := make(chan struct{})
	stats := c.RunExpirer(done, ExpireCfg{HZ: 100, Sample: 5, Pct: 10, Budget: time.Millisecond})
	close(done)
	select {
	case _, ok := <-stats:
		if ok {
			t.Fatal("stats channel must be closed after done")
		}
	case <-time.After(time.Second):
		t.Fatal("expirer goroutine did not stop within 1s")
	}
}

// TestExpirerConcurrentWithQueries 老化和查询并发不 race（-race 下跑）。
func TestExpirerConcurrentWithQueries(t *testing.T) {
	fc := &fakeClock{ms: 1000}
	restore := fc.install()
	defer restore()

	c := NewCap(0, 0, 42)
	for i := range 50 {
		c.PutFull(fmtKey("k%02d", i), mkEntry(60))
	}
	done := make(chan struct{})
	stats := c.RunExpirer(done, ExpireCfg{HZ: 200, Sample: 10, Pct: 10, Budget: time.Millisecond})

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 500 {
				c.Get(fmtKey("k%02d", int(fc.ms)%50))
				c.PutFull(fmtKey("k%02d", int(fc.ms)%50), mkEntry(600))
			}
		}()
	}
	wg.Wait()
	close(done)
	<-stats // 等 expirer 确认退出
}

// fmtKey 避免 fmt 依赖进 cache 包（Key 构造够用了）。
func fmtKey(pattern string, i int) Key {
	name := pattern
	// 简易 %03d 替换：测试数据规模小，不值得引 fmt。
	digits := [3]byte{'0' + byte(i/100%10), '0' + byte(i/10%10), '0' + byte(i%10)}
	out := make([]byte, 0, len(name))
	for j := 0; j < len(name); j++ {
		if name[j] == '%' && j+3 < len(name) && name[j+1] == '0' && name[j+2] == '3' && name[j+3] == 'd' {
			out = append(out, digits[0], digits[1], digits[2])
			j += 3
			continue
		}
		out = append(out, name[j])
	}
	return Key{Name: string(out), Type: 1}
}
