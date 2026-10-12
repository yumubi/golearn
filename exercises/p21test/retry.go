// Package p21test：P21 的练习包。两个并发原语（Retry、Debounce）+
// 一个可跑工具（cmd/retryrun：失败自动重试地执行命令）。
// 计时全部走标准库 time：测试用 testing/synctest 的 bubble 时，
// time.NewTimer/time.AfterFunc 自动被假时钟驱动，不需要任何注入。
package p21test

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ===== Retry：指数退避重试 =====

// Backoff 计算第 attempt 次重试（从 0 数）前的等待时长：
// base * 2^attempt，封顶 cap。纯函数，可独立测试。
func Backoff(base, cap time.Duration, attempt int) time.Duration {
	d := base
	for range attempt {
		d *= 2
		if d >= cap {
			return cap
		}
	}
	if d > cap {
		return cap
	}
	return d
}

// RetryOptions 控制重试行为。
type RetryOptions struct {
	Max  int           // 最大尝试次数（含首次），<1 视为 1
	Base time.Duration // 首次重试前的等待
	Cap  time.Duration // 单次等待上限
	// bubble 内 time.NewTimer 自动走假时钟，无需注入。
	OnRetry func(attempt int, next time.Duration, err error)
}

// Retry 以指数退避重试 fn：成功返回 nil、次数用尽返回最后一次错误、
// ctx 取消立刻返回 ctx.Err()（不打完剩余退避，等待期间也响应取消）。
func Retry(ctx context.Context, opts RetryOptions, fn func() error) error {
	if opts.Max < 1 {
		opts.Max = 1
	}
	// 睡 wait 但响应 ctx 取消：timer 与 ctx.Done() 竞速。
	// bubble 内 NewTimer 走假时钟（等待零真实耗时）。
	sleepCtx := func(ctx context.Context, d time.Duration) error {
		if d <= 0 {
			return ctx.Err()
		}
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}
	var lastErr error
	for attempt := range opts.Max {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := fn()
		if err == nil {
			return nil
		}
		lastErr = err
		if attempt == opts.Max-1 {
			break
		}
		wait := Backoff(opts.Base, opts.Cap, attempt)
		if opts.OnRetry != nil {
			opts.OnRetry(attempt+1, wait, err)
		}
		if err := sleepCtx(ctx, wait); err != nil {
			return err
		}
	}
	return lastErr
}

// ===== Debounce：防抖 =====

// Debounce 把连续调用合并成"最后一次之后 wait 才触发一次"。
type Debounce struct {
	wait  time.Duration
	mu    chan struct{} // 容量 1 的 channel 当锁
	timer *time.Timer
	ver   int           // 计时器代数：Call 一次 +1，触发回调核对代数
	quit  chan struct{} // 关闭 = 作废 pending 触发
}

// NewDebounce 创建 wait 窗口的防抖器。
func NewDebounce(wait time.Duration) *Debounce {
	return &Debounce{wait: wait, mu: make(chan struct{}, 1), quit: make(chan struct{})}
}

// Call 记录一次调用：wait 内没有新调用时 fn 恰好触发一次。
// 返回本次是否重置了计时器（首次 false，重置 true）。
func (d *Debounce) Call(fn func()) bool {
	d.mu <- struct{}{}
	defer func() { <-d.mu }()
	reset := d.timer != nil
	if d.timer != nil {
		d.timer.Stop()
		close(d.quit)
		d.quit = make(chan struct{})
	}
	ver := d.ver + 1
	d.ver = ver
	q := d.quit
	d.timer = time.AfterFunc(d.wait, func() {
		select {
		case <-q:
			return // 已被新 Call 作废
		default:
		}
		fn()
		_ = ver
	})
	return reset
}

// Stop 取消 pending 触发（幂等）。
func (d *Debounce) Stop() {
	d.mu <- struct{}{}
	defer func() { <-d.mu }()
	if d.timer != nil {
		d.timer.Stop()
	}
}

// ===== 工具共享逻辑：cmd/retryrun 的核心 =====

// RetryRunPlan 把 retryrun 的 flag 组合成 RetryOptions（main 与测试共用）。
func RetryRunPlan(max int, base, capD time.Duration) RetryOptions {
	return RetryOptions{Max: max, Base: base, Cap: capD}
}

// FormatAttempts 渲染重试日志行（工具输出，测试断言用）。
func FormatAttempts(attempt int, wait time.Duration, err error) string {
	return fmt.Sprintf("attempt %d failed: %v, retrying in %v", attempt, err, wait)
}

// ClampWait 把负等待钳到 0（防御 flag 解析出的怪值）。
func ClampWait(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d
}

// JoinFlags 简单的 flag 串归一化（给 fuzz 目标当第二个输入维度）。
func JoinFlags(parts []string) string {
	return strings.Join(parts, ",")
}
