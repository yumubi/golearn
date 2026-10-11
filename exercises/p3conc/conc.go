// Package conc 提供练习用的并发原语骨架。
package conc

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

// ============ 练习 1：worker pool（对标 L1 accept循环+spawn）============

// RunPool 起 workers 个 goroutine 消费 jobs，每个 job 的结果发到 results。
// 返回一个 blocking 函数：调用后等所有 worker 退出。
//
// Rust 对照（你 L1 学的）：
//
//	for _ in 0..workers { tokio::spawn(async move { while let Ok(job) = rx.recv() {...} }) }
//
// Go 惯例：WaitGroup 管 join（≈ tokio JoinSet），jobs chan 关闭=EOF 信号。
func RunPool(workers int, jobs <-chan int, results chan<- int) (wait func()) {
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// 参考实现：range jobs 到关闭（EOF 语义），平方后发 results。
			// 生产者 close(jobs) → range 自然退出，不需要 sentinel 值。
			// ≈ Rust: while let Ok(job) = rx.recv() { tx.send(job*job).await; }
			for j := range jobs {
				results <- j * j
			}
		}()
	}
	return wg.Wait
}

// ============ 练习 2：fan-in / fan-out ============

// Merge 把多个输入 channel 合并成一个输出（≈ tokio::stream::select_all）。
// 全部输入关闭后返回的 channel 应关闭。
func Merge(ins ...<-chan int) <-chan int {
	out := make(chan int)
	var wg sync.WaitGroup
	for _, c := range ins {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for v := range c {
				out <- v
			}
		}()
	}
	// 参考实现：closer goroutine——所有 fan-in 源头 drain 完才 close(out)。
	// 这是 fan-in 的标准收尾：不 close 的话消费方 range out 永远挂住（死锁），
	// 提前 close 的话慢源头的值会 send on closed channel panic。
	// wg.Wait 是"全部源头关闭"的同步点。
	go func() {
		wg.Wait()
		close(out)
	}()
	return out
}

// ============ 练习 3：Semaphore shed（对标 L9c）============

// Limit 并发上限执行：同时最多 limit 个 fn 在跑；超出者等待。
// ctx 取消时，还没开始的 fn 不再执行，返回 ctx.Err()。
//
// Rust 对照（你 L9c 做过的）：tokio::sync::Semaphore + Arc + acquire_owned
//
// Go 惯例三种实现：buffered chan / x/sync/semaphore / atomic counter。
// 这里用 x/sync/semaphore 练 ctx 语义。
func Limit(ctx context.Context, limit int, n int, fn func(i int) error) error {
	// 已实现（参考实现，下方）：semaphore.NewWeighted(limit) 控并发，
	// errgroup 管错误传播与取消。
	// errgroup ≈ 你 Rust 里手写的 try_join! + CancellationToken 合体：
	// 任一 fn 返回错误 → gctx 被 cancel → 后续 Acquire/执行看到取消。
	// 参考实现：semaphore 控并发上限 + errgroup 管错误传播与取消。
	// ≈ Rust L9c: Semaphore::new(limit) + try_join_all + CancellationToken。
	// 闭包捕获 i 的坑：Go 1.22 前 for 循环变量复用，g.Go(spawn) 读到的 i
	// 可能全都是 n-1；1.22+ 每轮新变量，但显式 shadow（i := i）是可读的保险。
	sem := semaphore.NewWeighted(int64(limit))
	g, gctx := errgroup.WithContext(ctx)
	for i := range n {
		i := i // 显式 shadow：别赌循环变量语义
		g.Go(func() error {
			// Acquire 带 ctx：取消时还没拿到名额的 fn 不再执行。
			if err := sem.Acquire(gctx, 1); err != nil {
				return err // 释放权重 nil-safe（没拿到就不用 Release）
			}
			defer sem.Release(1)
			return fn(i)
		})
	}
	return g.Wait()
}

// ============ 练习 4：select 超时 + 半关（对标 L2）============

// FirstOrTimeout 收第一个结果；1s 内没收到返回 "timeout"。
// done 关闭时返回 "cancelled"。
//
// Rust 对照（L2 你踩过 select! 半关 bug）：
//
//	tokio::select! { v = rx => v, _ = tokio::time::sleep(_) => "timeout" }
func FirstOrTimeout(in <-chan string, done <-chan struct{}) string {
	// 参考实现：select 三分支。case 顺序无关（Go select 是均匀随机调度，
	// 不像 tokio::select! 默认 random 之外还有 biased 模式可选）。
	// done 用 _, ok := <-done 检测"关闭"而非"值"——struct{} chan 只有关闭一个事件。
	select {
	case v := <-in:
		return v
	case <-time.After(1 * time.Second):
		return "timeout"
	case <-done:
		return "cancelled"
	}
}

// ============ 练习 5：atomic vs mutex（对标 L3 决策树）============

// CounterAtomic 原子计数。
// 参考实现：atomic.Int64（1.19+ 强类型原子）——8 字节对齐由语言保证，
// 不需要手动 padding（32 位机 int64 字段跨缓存行的老坑消亡史）。
type CounterAtomic struct {
	n atomic.Int64
}

// Inc 加一并返回新值。
func (c *CounterAtomic) Inc() int64 {
	return c.n.Add(1) // Add 返回新值 ≈ fetch_add(1, AcqRel)
}

// CounterMu 锁计数。
type CounterMu struct {
	mu sync.Mutex
	n  int64
}

// Inc 加一并返回新值。
func (c *CounterMu) Inc() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	return c.n
}
