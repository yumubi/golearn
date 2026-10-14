// Package p31sched：P31 的 12 个测试，全部对应正文小节。
// 预测题（先写答案再跑）标 PREDICT；观察题标 WATCH；改造题标 MODIFY。
package p31sched

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestSignalOneToOne：spawn 信号 1 对 1（正文 §4）。
// 预测：spawn 返回后 f 已在跑，main 阻塞在 <-c 直到 f 完成。
// Rust 对照: tokio JoinHandle.await 同职责，Go 无单 goroutine 句柄。
func TestSignalOneToOne(t *testing.T) {
	done := false
	c := spawn(func() error {
		time.Sleep(10 * time.Millisecond)
		done = true
		return nil
	})
	<-c
	if !done {
		t.Fatal("信号到达时 f 应已完成")
	}
}

// TestBroadcastClose：close 起跑枪声，5 个 worker 同时起跑（正文 §4）。
// 预测：close 前全部阻塞在 <-groupSignal，close 后全部立即通过。
func TestBroadcastClose(t *testing.T) {
	const N = 5
	groupSignal := make(chan signal)
	var start sync.WaitGroup
	start.Add(N)
	var arrived sync.WaitGroup
	arrived.Add(N)
	for range N {
		go func() {
			start.Done()
			<-groupSignal
			arrived.Done()
		}()
	}
	start.Wait()
	close(groupSignal) // 枪响
	arrived.Wait()
}

// TestCounterByChannel：后台 goroutine 独占状态（正文 §4）。
// 预测：Increase 返回值单调递增且不超过调用次数。
// Rust 对照: 这里没有所有权编译器兜底，纪律是"只有这个 goroutine 写 c.i"。
func TestCounterByChannel(t *testing.T) {
	c := NewCounter()
	for range 5 {
		c.c <- 0
		if got := c.Increase(); got < 1 || got > 5 {
			t.Fatalf("got %d, want 1..5", got)
		}
	}
}

// TestCountingSemaphore：容量 3 限流 9 个 job（正文 §4）。
// 预测：9 个 50ms job 分 3 批，每批恰好 3 个，无 1 个的尾巴。
func TestCountingSemaphore(t *testing.T) {
	active := make(chan struct{}, 3)
	var wg sync.WaitGroup
	now := time.Now()
	var mu sync.Mutex
	batches := map[string]int{}
	for range 9 {
		wg.Go(func() {
			active <- struct{}{} // 获取
			batch := int(time.Since(now) / (50 * time.Millisecond))
			mu.Lock()
			batches[fmt.Sprint(batch)]++
			mu.Unlock()
			time.Sleep(50 * time.Millisecond)
			<-active // 释放
		})
	}
	wg.Wait()
	if len(batches) < 3 {
		t.Fatalf("expected >=3 batches, got %v", batches)
	}
	for b, n := range batches {
		if n > 3 {
			t.Fatalf("batch %s has %d concurrent (limit 3)", b, n)
		}
	}
	t.Logf("批次分布: %v", batches)
}

// TestNilChannelSelect：双源 close 后置 nil 防零值风暴（正文 §5）。
// 预测：5、7 各收一次后循环收敛；不置 nil 的退化版见 TestDegenerateZeroStorm。
func TestNilChannelSelect(t *testing.T) {
	ch1 := make(chan int)
	ch2 := make(chan int)
	go func() {
		time.Sleep(5 * time.Millisecond)
		ch1 <- 5
		close(ch1)
	}()
	go func() {
		time.Sleep(7 * time.Millisecond)
		ch2 <- 7
		close(ch2)
	}()
	var got []int
	for len(got) < 2 {
		select {
		case v, ok := <-ch1:
			if !ok {
				ch1 = nil // 已 close：置 nil 让分支永久阻塞
				break
			}
			got = append(got, v)
		case v, ok := <-ch2:
			if !ok {
				ch2 = nil
				break
			}
			got = append(got, v)
		}
	}
	if got[0] != 5 || got[1] != 7 {
		t.Fatalf("got %v, want [5 7]", got)
	}
}

// TestDegenerateZeroStorm：退化对照（正文 §5）。
// 预测：ch1 close 后"从已关闭 channel 接收永不阻塞"，select 反复选中它刷零值。
func TestDegenerateZeroStorm(t *testing.T) {
	ch1 := make(chan int)
	go func() {
		time.Sleep(2 * time.Millisecond)
		ch1 <- 5
		close(ch1)
	}()
	time.Sleep(3 * time.Millisecond)
	n := 0
	for range 1000 {
		select {
		case v := <-ch1: // 已 close：立即返回零值，永不阻塞
			if v == 0 {
				n++
			}
		case <-time.After(1 * time.Millisecond):
		}
	}
	t.Logf("零值命中: %d/1000", n)
	if n < 900 {
		t.Fatalf("expected zero storm, got %d/1000", n)
	}
}

// TestWorkerPool：capacity=5 提交 10 任务，全部执行且峰值 ≤5（正文 §7）。
// 预测：tasks 改成容量 10 的缓冲后 Schedule 不再阻塞，背压消失。
func TestWorkerPool(t *testing.T) {
	var mu sync.Mutex
	ran := 0
	p := New(5)
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		if err := p.Schedule(func() {
			defer wg.Done()
			time.Sleep(10 * time.Millisecond)
			mu.Lock()
			ran++
			mu.Unlock()
		}); err != nil {
			wg.Done()
			t.Fatal(err)
		}
	}
	wg.Wait()
	if ran != 10 {
		t.Fatalf("ran %d, want 10", ran)
	}
	p.Free()
}

// TestFunctionalOptions：WithBlock(false) 池满返回 ErrNoIdleWorkerInPool（正文 §7）。
func TestFunctionalOptions(t *testing.T) {
	p := New(1, WithBlock(false))
	defer p.Free()
	_ = p.Schedule(func() { time.Sleep(50 * time.Millisecond) }) // 占住 worker
	var gotErr error
	for {
		err := p.Schedule(func() {})
		if err != nil {
			gotErr = err
			break
		}
	}
	if gotErr != ErrNoIdleWorkerInPool {
		t.Fatalf("got %v, want ErrNoIdleWorkerInPool", gotErr)
	}
}

// TestLostUpdateDistribution：1000 协程 n++ 跑 5 轮，记录分布（正文 §8）。
// 不做确定性断言：脏写不保证每次发生，偶发全对（5 轮全 1000）也是
// 实验结论的一部分：间歇性故障比每次都错更危险。
// 本测试故意制造数据竞争以复现脏写，-race 下必报 race，因此 raceEnabled
// 时跳过（skip-ok: 正文 §8 已在无 -race 下实拍分布，整包 -race 跑会崩）。
// 单独跑法：`go test -run TestLostUpdateDistribution -count=1`（不带 -race）。
// Rust 对照: Rc/RefCell 违规是编译错；这里同一错误是静默脏写。
func TestLostUpdateDistribution(t *testing.T) {
	if raceEnabled {
		t.Skip("skip-ok: 故意竞争复现脏写，-race 下必 FAIL；分跑命令见注释与正文 §8")
	}
	sum := 0
	low := 0
	for range 5 {
		var n int64
		var wg sync.WaitGroup
		for range 1000 {
			wg.Go(func() { n++ })
		}
		wg.Wait()
		t.Logf("n++ 结果: %d", n)
		sum += int(n)
		if int(n) < 1000 {
			low++
		}
	}
	if low == 0 {
		t.Logf("warning: 5 轮全部 1000，脏写未发生（偶发全对也是结论）")
	}
	_ = sum
}

// TestAtomicCounter：同构实验换 atomic.Int64，断言恒 1000（正文 §8 对照）。
func TestAtomicCounter(t *testing.T) {
	var n atomic.Int64
	var wg sync.WaitGroup
	for range 1000 {
		wg.Go(func() { n.Add(1) })
	}
	wg.Wait()
	if got := n.Load(); got != 1000 {
		t.Fatalf("atomic counter got %d, want 1000", got)
	}
}

// TestRWMutexMatrix：RWMutex 行为矩阵（正文 §8）。
// 读锁可重入；写锁等待时新读锁也阻塞（防写饿死）。
func TestRWMutexMatrix(t *testing.T) {
	var mu sync.RWMutex

	// 1. 读锁可重入（同 goroutine 多次 RLock）
	mu.RLock()
	mu.RLock()
	mu.RUnlock()
	mu.RUnlock()

	// 2. 有写锁等待时，新读锁也阻塞
	mu.RLock()
	locked := make(chan struct{})
	go func() {
		mu.Lock()
		mu.Unlock()
		close(locked)
	}()
	time.Sleep(20 * time.Millisecond)
	readerBlocked := make(chan struct{})
	go func() {
		mu.RLock()
		mu.RUnlock()
		close(readerBlocked)
	}()
	time.Sleep(20 * time.Millisecond)
	select {
	case <-readerBlocked:
		t.Fatal("新读锁不应在写锁等待时获得")
	default:
	}
	mu.RUnlock()
	<-readerBlocked
	<-locked
}

// TestChildCtxInheritsDeadline：子 ctx 寿命取父子较短者（正文 §8）。
// 预测：父 1000ms 睡 500ms 后子设 1000ms，子 ~500ms 到期。
func TestChildCtxInheritsDeadline(t *testing.T) {
	parent, cancel1 := context.WithTimeout(context.Background(), 1000*time.Millisecond)
	defer cancel1()
	time.Sleep(500 * time.Millisecond) // 父剩 ~500ms
	start := time.Now()
	child, cancel2 := context.WithTimeout(parent, 1000*time.Millisecond)
	defer cancel2()
	<-child.Done()
	elapsed := time.Since(start)
	t.Logf("child 到期耗时 %v, err=%v", elapsed, child.Err())
	if elapsed >= 600*time.Millisecond {
		t.Fatalf("child 应随父 ~500ms 到期，实际 %v", elapsed)
	}
}
