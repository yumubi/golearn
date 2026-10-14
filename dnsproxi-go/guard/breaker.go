package guard

import (
	"errors"
	"sync"
	"time"
)

// ErrOpen 熔断开放时 Allow 返回的哨兵错误：调用方据此走降级/快速失败路径。
var ErrOpen = errors.New("guard: circuit open")

// 熔断三态（W14-1 p13）：闭合=全放行；开放=全拒绝；半开=试探。
// 转移规则：
//
//	closed → open   ：窗口内请求 >= MinSamples 且失败率 >= FailThreshold
//	open → halfOpen ：冷却期 CoolOff 过后，放一个试探请求
//	halfOpen → closed：试探成功
//	halfOpen → open  ：试探失败（回到开放，重新冷却）
type state int

const (
	stateClosed state = iota
	stateOpen
	stateHalfOpen
)

func (s state) String() string {
	switch s {
	case stateClosed:
		return "closed"
	case stateOpen:
		return "open"
	default:
		return "half-open"
	}
}

// BreakerConfig 熔断参数。零值字段取默认。
type BreakerConfig struct {
	FailThreshold float64       // 失败率阈值，默认 0.5
	MinSamples    int           // 判定所需最小样本，默认 20
	Window        int           // 滑动窗口计数（最近 N 次请求），默认 100
	CoolOff       time.Duration // 开放→半开的冷却期，默认 10s
}

func (c BreakerConfig) withDefaults() BreakerConfig {
	if c.FailThreshold <= 0 {
		c.FailThreshold = 0.5
	}
	if c.MinSamples <= 0 {
		c.MinSamples = 20
	}
	if c.Window <= 0 {
		c.Window = 100
	}
	if c.CoolOff <= 0 {
		c.CoolOff = 10 * time.Second
	}
	return c
}

// ring 是固定容量失败位环形缓冲：1=失败，0=成功。
// 滑动窗口语义：只看最近 Window 次请求。
type ring struct {
	bits []bool
	next int
	size int // 已写入数，cap 后恒为 len(bits)
}

func (r *ring) add(fail bool) {
	if r.size < len(r.bits) {
		r.size++
	}
	r.bits[r.next] = fail
	r.next = (r.next + 1) % len(r.bits)
}

func (r *ring) counts() (fails, total int) {
	for i := 0; i < r.size; i++ {
		if r.bits[i] {
			fails++
		}
	}
	return fails, r.size
}

func (r *ring) reset() {
	r.next, r.size = 0, 0
	for i := range r.bits {
		r.bits[i] = false
	}
}

// Breaker 三态熔断器。并发安全；时钟注入。
// 用法：Allow() 放行判定；每次上游调用的结局必须二选一回填：
// MarkSuccess() / MarkFailure()，否则窗口统计失真。
type Breaker struct {
	mu       sync.Mutex
	cfg      BreakerConfig
	st       state
	r        ring
	openedAt time.Time
	probing  bool // halfOpen 下试探请求是否已发出（一次只放一个）
	clock    Clock
}

// NewBreaker 构造熔断器，cfg 零值字段取默认。
func NewBreaker(cfg BreakerConfig, clock Clock) *Breaker {
	cfg = cfg.withDefaults()
	if clock == nil {
		clock = RealClock
	}
	return &Breaker{
		cfg:   cfg,
		st:    stateClosed,
		r:     ring{bits: make([]bool, cfg.Window)},
		clock: clock,
	}
}

// State 返回当前状态名（指标/日志用）。
func (b *Breaker) State() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.st.String()
}

// Allow 判定本次请求是否放行。
// closed：全放。open：冷却期未到全拒（ErrOpen）；到了转 halfOpen。
// halfOpen：只放一个试探请求，其余拒绝。
func (b *Breaker) Allow() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.st {
	case stateClosed:
		return nil
	case stateOpen:
		if b.clock.Now().Sub(b.openedAt) >= b.cfg.CoolOff {
			b.st = stateHalfOpen
			b.probing = false
		} else {
			return ErrOpen
		}
		fallthrough
	case stateHalfOpen:
		if b.probing {
			return ErrOpen
		}
		b.probing = true
		return nil
	}
	return nil
}

// MarkSuccess 回填一次成功。halfOpen 的试探成功 → closed（窗口清零重计）。
func (b *Breaker) MarkSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.st == stateHalfOpen {
		b.st = stateClosed
		b.probing = false
		b.r.reset()
		return
	}
	if b.st == stateClosed {
		b.r.add(false)
	}
}

// MarkFailure 回填一次失败。closed 下窗口统计够样本且失败率超阈值 → open；
// halfOpen 的试探失败 → open（重新冷却）。
func (b *Breaker) MarkFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.st {
	case stateHalfOpen:
		b.st = stateOpen
		b.probing = false
		b.openedAt = b.clock.Now()
	case stateClosed:
		b.r.add(true)
		fails, total := b.r.counts()
		if total >= b.cfg.MinSamples &&
			float64(fails)/float64(total) >= b.cfg.FailThreshold {
			b.st = stateOpen
			b.openedAt = b.clock.Now()
		}
	}
}
