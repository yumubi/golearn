// Package guard 提供进程内限流与熔断（P35）：令牌桶 + 三态熔断器 + 装饰器组合。
// 全部时钟经 Clock 注入，测试可以拨快时间而不 sleep。
package guard

import (
	"context"
	"sync"
	"time"
)

// Clock 抽象时间源：Now 供令牌桶补 token 与熔断状态机计时。
// 默认 realClock；测试用 fakeClock 手动推进。
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// RealClock 是生产用时钟。
var RealClock Clock = realClock{}

// TokenBucket 令牌桶限流器（教学实现，参照 C:dachang/algorithm/rate_limit.go
// 的浮点补 token 思路，并发安全化并注入时钟）。
// 语义：桶容量 cap，定速 rate 枚/秒补充；Allow 拿不到立即 false（拒绝式），
// WaitN 阻塞到攒够（可被 ctx 取消）。允许积压：突发流量先消耗存量令牌。
type TokenBucket struct {
	mu     sync.Mutex
	rate   float64   // 枚/秒
	cap    float64   // 桶容量
	tokens float64   // 当前令牌，可为负（WaitN 欠账）
	last   time.Time // 上次补 token 时刻
	clock  Clock
}

// NewTokenBucket 构造令牌桶。rate<=0 或 cap<=0 视为非法，返回 nil（调用方
// 据此跳过限流）；tokens 初始满桶（服务冷启动即放行 cap 个）。
func NewTokenBucket(rate int, cap int, clock Clock) *TokenBucket {
	if rate <= 0 || cap <= 0 {
		return nil
	}
	if clock == nil {
		clock = RealClock
	}
	return &TokenBucket{
		rate:   float64(rate),
		cap:    float64(cap),
		tokens: float64(cap),
		last:   clock.Now(),
		clock:  clock,
	}
}

// advance 把 last 推进到 now 并按流逝时间补 token，封顶 cap。
// 调用方必须持有 mu。
func (t *TokenBucket) advance(now time.Time) {
	delta := now.Sub(t.last).Seconds() * t.rate
	t.tokens += delta
	if t.tokens > t.cap {
		t.tokens = t.cap
	}
	t.last = now
}

// Allow 尝试取 n 枚令牌：够就扣掉返回 true，不够立即 false。
func (t *TokenBucket) Allow(n int) bool {
	if n <= 0 {
		n = 1
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.advance(t.clock.Now())
	if t.tokens >= float64(n) {
		t.tokens -= float64(n)
		return true
	}
	return false
}

// WaitN 欠账语义（对照 C:dachang/algorithm/rate_limit.go 的 tokens 可为负）：
// 先把 n 枚扣进 tokens（可为负），再按 rate 算"恢复到扣完时的水平"要多久，
// 睡满该时长即返回；ctx 取消/超时中途返回 ctx.Err()（欠账不回滚，已占配额）。
// 容量 cap 只在补充时封顶，不阻止欠账变负。
func (t *TokenBucket) WaitN(ctx context.Context, n int) error {
	if n <= 0 {
		n = 1
	}
	t.mu.Lock()
	t.advance(t.clock.Now())
	t.tokens -= float64(n) // 欠账：可为负
	// 负得越深，恢复越久：debt=-tokens，每秒回 rate 枚
	debt := -t.tokens
	if debt < 0 {
		debt = 0
	}
	wait := time.Duration(debt / t.rate * float64(time.Second))
	t.mu.Unlock()

	if wait <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(wait)
	select {
	case <-ctx.Done():
		timer.Stop()
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
