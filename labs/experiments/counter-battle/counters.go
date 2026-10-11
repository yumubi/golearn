// Package counterbattle 实验 2：共享计数器四方案同台。
//
// 复刻 rs/rust-for-frontend labs/experiments/concurrency/counters/ 的 Go 版：
// 8 goroutine × 1,000,000 次 +1，总和必须 == 8,000,000（正确性优先）。
// 两种竞争强度：hot（每次迭代打共享计数）/ batch（本地攒 1000 再打）。
//
// Rust 对照（原实验四方案）：
//
//	Arc<Mutex<u64>>       → sync.Mutex
//	Arc<AtomicU64>        → sync/atomic.Uint64
//	mpsc → 聚合线程        → chan int → 聚合 goroutine
//	thread_local + join    → 每 goroutine 本地计数 + WaitGroup 合并
//
// 原实验教训直接搬过来：单轮数字不可信（Apple Silicon P/E 核调度噪音），
// 必须 min-of-5 程序内 × 多轮程序外，报告区间；本地累加路径要防编译器折叠。
package counterbattle

import (
	"sync"
	"sync/atomic"
)

const (
	goroutines = 8
	perG       = 1_000_000
	batchSize  = 1000
)

// MutexCounter hot：每次迭代锁一次。
type MutexCounter struct {
	mu sync.Mutex
	n  uint64
}

func (c *MutexCounter) Run(hot bool) uint64 {
	// 每轮归零：复用实例跑多轮时，跨轮累积会把总和断言骗成 2x（实测踩过）。
	c.mu.Lock()
	c.n = 0
	c.mu.Unlock()
	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range perG {
				if hot {
					c.mu.Lock()
					c.n++
					c.mu.Unlock()
				} else {
					local := uint64(0)
					for range batchSize {
						local++
					}
					c.mu.Lock()
					c.n += local
					c.mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	return c.n
}

// AtomicCounter hot：每次迭代一次原子 RMW。
type AtomicCounter struct {
	n atomic.Uint64
}

func (c *AtomicCounter) Run(hot bool) uint64 {
	c.n.Store(0) // 同上：复用实例多轮必须先归零
	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range perG {
				if hot {
					c.n.Add(1)
				} else {
					local := uint64(0)
					for range batchSize {
						local++
					}
					c.n.Add(local)
				}
			}
		}()
	}
	wg.Wait()
	return c.n.Load()
}

// ChannelCounter：mpsc → 聚合 goroutine（Go 的 channel ≈ Rust mpsc::channel）。
func ChannelCounter(hot bool) uint64 {
	ch := make(chan uint64, goroutines)
	var agg atomic.Uint64
	done := make(chan struct{})
	go func() {
		for v := range ch {
			agg.Add(v)
		}
		close(done)
	}()
	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if hot {
				for range perG {
					ch <- 1 // hot 模式每次迭代发一条——channel 的最坏场景
				}
			} else {
				ch <- batchSize
			}
		}()
	}
	wg.Wait()
	close(ch)
	<-done
	return agg.Load()
}

// LocalJoin：每 goroutine 本地计数，join 后合并（≈ thread_local + join）。
func LocalJoin(hot bool) uint64 {
	var wg sync.WaitGroup
	var total atomic.Uint64
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := uint64(0)
			if hot {
				for range perG {
					local++ // 8 个独立局部变量，零竞争
				}
			} else {
				for range batchSize {
					local++
				}
			}
			total.Add(local)
		}()
	}
	wg.Wait()
	return total.Load()
}
