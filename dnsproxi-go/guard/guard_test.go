package guard

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// fakeClock 手动推进的时钟，测试用。
type fakeClock struct {
	mu   sync.Mutex
	now  time.Time
	done chan struct{}
}

func newFakeClock(start time.Time) *fakeClock {
	return &fakeClock{now: start, done: make(chan struct{})}
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// advance 推进时钟；被 WaitN 的定时等待阻塞时发一次信号唤醒。
func (f *fakeClock) advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	f.mu.Unlock()
	select {
	case <-f.done:
	default:
	}
}

// guardedWaitN 阻塞中让 advance 立即返回（测试钩子）。
func (f *fakeClock) signal() chan struct{} { return f.done }

// ---- TokenBucket 测试 ----

func (s *guardSuite) testBucketAllowReject() {
	c := newFakeClock(s.base)
	b := NewTokenBucket(10, 5, c) // 10/s，容量 5
	for i := range 5 {
		if !b.Allow(1) {
			s.failf("第 %d 个应放行", i+1)
		}
	}
	if b.Allow(1) {
		s.failf("第 6 个应拒绝：容量 5 已耗尽")
	}
	// 0.2s 补 2 枚：再放 2 个，第 3 个拒
	c.advance(200 * time.Millisecond)
	if !b.Allow(2) {
		s.failf("补 2 枚后应放行 2")
	}
	if b.Allow(1) {
		s.failf("应再次拒绝")
	}
}

func (s *guardSuite) testBucketRefillCap() {
	c := newFakeClock(s.base)
	b := NewTokenBucket(10, 5, c)
	c.advance(time.Hour) // 补远超 cap：封顶 5
	for range 5 {
		if !b.Allow(1) {
			s.failf("封顶后应有 5 枚")
		}
	}
	if b.Allow(1) {
		s.failf("封顶 5 不应多出")
	}
}

func (s *guardSuite) testBucketWaitN() {
	// WaitN 的等待用真实 timer（教学实现不做 fake-timer 框架），
	// 所以本用例走真实时钟：rate=1000/s、桶 1、要 10 枚 ≈ 9ms。
	b := NewTokenBucket(1000, 1, RealClock)
	if !b.Allow(1) {
		s.failf("首枚应放行")
	}
	start := time.Now()
	if err := b.WaitN(s.ctx, 10); err != nil {
		s.failf("WaitN 不应报错: %v", err)
	}
	if d := time.Since(start); d < 8*time.Millisecond {
		s.failf("欠 9 枚至少应等 9ms，实际 %v", d)
	}
}

func (s *guardSuite) testBucketWaitNCancel() {
	b := NewTokenBucket(1, 1, RealClock)
	ctx, cancel := context.WithCancel(s.ctx)
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	if err := b.WaitN(ctx, 100); err == nil {
		s.failf("取消后 WaitN 应报错")
	}
}

func (s *guardSuite) testBucketNilOnBad() {
	if NewTokenBucket(0, 10, RealClock) != nil || NewTokenBucket(10, 0, RealClock) != nil {
		s.failf("非法参数应返回 nil")
	}
}

// ---- Breaker 测试 ----

func (s *guardSuite) testBreakerTrip() {
	b := NewBreaker(BreakerConfig{MinSamples: 4, FailThreshold: 0.5, Window: 10, CoolOff: time.Second}, newFakeClock(s.base))
	// 2 成功 2 失败：50% 达阈值，样本够 → open
	b.MarkSuccess()
	b.MarkSuccess()
	b.MarkFailure()
	b.MarkFailure()
	if b.State() != "open" {
		s.failf("失败率 50%% 达阈值应 open，得 %s", b.State())
	}
	if err := b.Allow(); err != ErrOpen {
		s.failf("open 应拒 ErrOpen，得 %v", err)
	}
}

func (s *guardSuite) testBreakerMinSamples() {
	b := NewBreaker(BreakerConfig{MinSamples: 10, FailThreshold: 0.5}, newFakeClock(s.base))
	for range 3 {
		b.MarkFailure()
	}
	if b.State() != "closed" {
		s.failf("样本不足不应 open，得 %s", b.State())
	}
}

func (s *guardSuite) testBreakerHalfOpenRecover() {
	c := newFakeClock(s.base)
	b := NewBreaker(BreakerConfig{MinSamples: 2, FailThreshold: 0.5, CoolOff: time.Second}, c)
	b.MarkFailure()
	b.MarkFailure()
	if b.State() != "open" {
		s.failf("应 open")
	}
	c.advance(2 * time.Second) // 冷却期过
	if err := b.Allow(); err != nil {
		s.failf("冷却后应放试探: %v", err)
	}
	if b.State() != "half-open" {
		s.failf("应 half-open，得 %s", b.State())
	}
	// 试探期间第二个请求应被拒
	if err := b.Allow(); err != ErrOpen {
		s.failf("试探期间应拒，得 %v", err)
	}
	b.MarkSuccess()
	if b.State() != "closed" {
		s.failf("试探成功应 closed，得 %s", b.State())
	}
}

func (s *guardSuite) testBreakerHalfOpenRetrip() {
	c := newFakeClock(s.base)
	b := NewBreaker(BreakerConfig{MinSamples: 2, FailThreshold: 0.5, CoolOff: time.Second}, c)
	b.MarkFailure()
	b.MarkFailure()
	c.advance(2 * time.Second)
	if err := b.Allow(); err != nil {
		s.failf("应放试探: %v", err)
	}
	b.MarkFailure()
	if b.State() != "open" {
		s.failf("试探失败应重新 open，得 %s", b.State())
	}
	// 重新计时：未到冷却期继续拒
	if err := b.Allow(); err != ErrOpen {
		s.failf("冷却期内应拒: %v", err)
	}
}

// ---- 滑动窗口语义 ----

func (s *guardSuite) testBreakerWindowSlide() {
	b := NewBreaker(BreakerConfig{MinSamples: 4, FailThreshold: 0.5, Window: 4, CoolOff: time.Second}, newFakeClock(s.base))
	// 前 4 次全失败 → open
	for range 4 {
		b.MarkFailure()
	}
	if b.State() != "open" {
		s.failf("全败应 open")
	}
}

// ---- 并发安全（-race 下跑）----

func (s *guardSuite) testGuardConcurrent() {
	b := NewBreaker(BreakerConfig{MinSamples: 50, Window: 64}, newFakeClock(s.base))
	tb := NewTokenBucket(1000, 1000, newFakeClock(s.base))
	var allowed, rejected atomic.Int64
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 200 {
				if tb.Allow(1) {
					allowed.Add(1)
				} else {
					rejected.Add(1)
				}
				if b.Allow() == nil {
					if i%3 == 0 {
						b.MarkFailure()
					} else {
						b.MarkSuccess()
					}
				}
			}
		}()
	}
	wg.Wait()
	if allowed.Load()+rejected.Load() != 1600 {
		s.failf("令牌桶判定总数应 1600，得 %d", allowed.Load()+rejected.Load())
	}
	_ = b.State()
}
