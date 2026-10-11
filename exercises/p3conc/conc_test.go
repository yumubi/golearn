package conc

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
)

// TestPool 练习 worker pool。
// 100 个 job、4 个 worker，全部平方后收集。关闭 jobs → 结果全收 → wait 返回。
//
// ⚠️ 练习者注意：这个测试**不关闭 results**——因为你实现的 RunPool 是
// results 的发送方之一，关闭权在发送方（Go 铁律）。验收靠两个信号：
//  1. wait() 返回（所有 worker 退出）
//  2. 收集侧统计正确 —— 所以收集 goroutine 用带缓冲计数，等 wait 后
//     再非阻塞读，不依赖 results 关闭。
//
// 如果你把 results 的 close 写在测试里而 worker 还在发 → panic send on closed，
// 这本身就是 lesson。
func TestPool(t *testing.T) {
	jobs := make(chan int, 100)
	results := make(chan int, 100) // 缓冲 = 全部结果，收集永不阻塞
	wait := RunPool(4, jobs, results)

	for i := range 100 {
		jobs <- i
	}
	close(jobs)

	wait() // 所有 worker 退出（练习者实现 range jobs + wg.Done 后返回）

	// worker 全退后不再有发送者 → results 里就是全部结果。
	got := map[int]bool{}
	for {
		select {
		case r := <-results:
			got[r] = true
			continue
		default:
		}
		break
	}
	if len(got) != 100 {
		t.Fatalf("want 100 unique results, got %d", len(got))
	}
	for i := range 100 {
		if !got[i*i] {
			t.Fatalf("missing %d", i*i)
		}
	}
}

// TestMerge 练习 fan-in。
// ⚠️ 未实现 Merge 时这个测试会**死锁**（out 永远不关）。
// 跑法带超时：go test -timeout 30s ./exercises/p3conc/
// 死锁是练习的一部分——先看到 all goroutines are asleep 才知道 select/channel 卡哪了。
func TestMerge(t *testing.T) {
	a := make(chan int, 3)
	b := make(chan int, 3)
	a <- 1
	a <- 2
	close(a)
	b <- 10
	close(b)

	seen := map[int]bool{}
	for v := range Merge(a, b) {
		seen[v] = true
	}
	if len(seen) != 3 || !seen[1] || !seen[2] || !seen[10] {
		t.Fatalf("merge wrong: %v", seen)
	}
}

// TestLimit 练习并发上限 + ctx 语义（对标 L9c shed）。
func TestLimit(t *testing.T) {
	var cur, max atomicInt64
	fn := func(i int) error {
		n := cur.add(1)
		for {
			old := max.load()
			if n <= old || max.cas(old, n) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		cur.add(-1)
		return nil
	}

	if err := Limit(context.Background(), 3, 12, fn); err != nil {
		t.Fatalf("limit run failed: %v", err)
	}
	if m := max.load(); m > 3 {
		t.Fatalf("concurrency exceeded limit: %d", m)
	}

	// ctx 已取消 → 应返回错误且不再执行。
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ran := false
	if err := Limit(ctx, 2, 5, func(i int) error { ran = true; return nil }); err == nil {
		t.Fatal("want ctx error for cancelled context")
	}
	if ran {
		t.Fatal("fn should not run after cancel")
	}
}

// TestFirstOrTimeout 练习 select。
func TestFirstOrTimeout(t *testing.T) {
	// 有结果：立刻返回结果。
	in := make(chan string, 1)
	in <- "fast"
	if got := FirstOrTimeout(in, make(chan struct{})); got != "fast" {
		t.Fatalf("want fast got %q", got)
	}

	// 无结果：超时。
	if got := FirstOrTimeout(make(chan string), make(chan struct{})); got != "timeout" {
		t.Fatalf("want timeout got %q", got)
	}

	// done 关闭：cancelled。
	done := make(chan struct{})
	close(done)
	if got := FirstOrTimeout(make(chan string), done); got != "cancelled" {
		t.Fatalf("want cancelled got %q", got)
	}
}

// TestCounters 练习 atomic vs mutex（对标 L3）。
// 跑法：go test -race ./exercises/p3conc/ -run TestCounters
// 修 CounterAtomic 之前这里大概率被 race detector 抓到。
func TestCounters(t *testing.T) {
	const n = 1000

	ca := &CounterAtomic{}
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() { defer wg.Done(); ca.Inc() }()
	}
	wg.Wait()
	if got := ca.Inc() - 1; got != n {
		t.Fatalf("atomic counter lost updates: got %d want %d", got, n)
	}

	cm := &CounterMu{}
	for range n {
		wg.Add(1)
		go func() { defer wg.Done(); cm.Inc() }()
	}
	wg.Wait()
	if got := cm.Inc() - 1; got != n {
		t.Fatalf("mutex counter lost updates: got %d want %d", got, n)
	}
}

// TestPanicKillsProcess 是给"读"的测试，别运行它。
// 把下面 t.Skip 删掉运行：整个测试进程会死——Go 的 goroutine panic
// 没有 tokio task 那层隔离。体会后恢复 t.Skip。
func TestPanicKillsProcess(t *testing.T) {
	t.Skip("取消注释下一行并运行，观察进程死亡；看完恢复")
	// go func() { panic("dead") }(); time.Sleep(time.Second)
}

// ---- 小工具：不用 atomic 包名的测试辅助 ----

type atomicInt64 struct {
	mu sync.Mutex
	v  int64
}

func (a *atomicInt64) add(d int64) int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.v += d
	return a.v
}

func (a *atomicInt64) load() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.v
}

func (a *atomicInt64) cas(old, new int64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.v == old {
		a.v = new
		return true
	}
	return false
}

// 编译期确保 errgroup 被引用（练习者实现 Limit 时用）。
var _ = errgroup.Group{}

var _ = fmt.Sprint
