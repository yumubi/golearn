package p14pipe

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// TestPipeline：三板斧串起来，结果保序。
func TestPipeline(t *testing.T) {
	got, err := Pipeline(context.Background(), 1, 2, 3, 4)
	if err != nil {
		t.Fatal(err)
	}
	want := []int{1, 4, 9, 16}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// TestFanoutConcurrent：4 worker 处理 10 个任务，结果全部归位（下标写回保序）。
func TestFanoutConcurrent(t *testing.T) {
	results, err := Fanout(context.Background(), 4, func(_ context.Context, n int) (string, error) {
		return fmtInt(n * 10), nil
	}, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	if err != nil {
		t.Fatal(err)
	}
	for i, n := range []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10} {
		if results[i] != fmtInt(n*10) {
			t.Fatalf("results[%d] = %q, want %q", i, results[i], fmtInt(n*10))
		}
	}
}

// TestFanoutResultOrder：真实 process，验证下标写回后结果与输入一一对应。
func TestFanoutResultOrder(t *testing.T) {
	in := []int{5, 3, 8, 1}
	results, err := Fanout(context.Background(), 3, func(_ context.Context, n int) (string, error) {
		return fmtInt(n * 10), nil
	}, in...)
	if err != nil {
		t.Fatal(err)
	}
	for i, n := range in {
		if results[i] != fmtInt(n*10) {
			t.Fatalf("results[%d] = %q, want %q", i, results[i], fmtInt(n*10))
		}
	}
}

func fmtInt(n int) string {
	return []string{"0", "10", "20", "30", "40", "50", "60", "70", "80", "90", "100"}[n/10]
}

// TestFanoutLimit：SetLimit(2) 时同时活的 worker 不超过 2（消融实验的计数器版）。
func TestFanoutLimit(t *testing.T) {
	var cur, max atomic.Int64
	_, err := Fanout(context.Background(), 2, func(_ context.Context, n int) (string, error) {
		c := cur.Add(1)
		for {
			m := max.Load()
			if c <= m || max.CompareAndSwap(m, c) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond) // 制造重叠窗口
		cur.Add(-1)
		return "ok", nil
	}, 1, 2, 3, 4, 5, 6, 7, 8)
	if err != nil {
		t.Fatal(err)
	}
	if m := max.Load(); m > 2 {
		t.Fatalf("max concurrent workers = %d, want <= 2", m)
	}
}

// TestFanoutFirstError：一个 worker 失败，g.Wait 返回那个错误，其余任务被取消。
func TestFanoutFirstError(t *testing.T) {
	wantErr := errors.New("boom on 3")
	var ran atomic.Int64
	_, err := Fanout(context.Background(), 2, func(ctx context.Context, n int) (string, error) {
		if n == 3 {
			return "", wantErr
		}
		// 失败后 ctx 被取消：后续任务要么没跑，要么立刻退出。
		select {
		case <-time.After(20 * time.Millisecond):
			ran.Add(1)
			return "ok", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}, 1, 2, 3, 4, 5)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

// TestFanoutGoroutineLeak：跑完一轮，goroutine 数回落（g.Wait 等全部收尾，无泄漏）。
func TestFanoutGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	_, err := Fanout(context.Background(), 4, func(_ context.Context, n int) (string, error) {
		return "ok", nil
	}, 1, 2, 3, 4, 5, 6, 7, 8)
	if err != nil {
		t.Fatal(err)
	}
	// 给 runtime 一点时间回收
	for range 50 {
		if runtime.NumGoroutine() <= before+1 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("goroutines before=%d after=%d: leak", before, runtime.NumGoroutine())
}

// TestGenCancel：下游提前退出，ctx 取消后 gen 的 goroutine 不泄漏。
func TestGenCancel(t *testing.T) {
	before := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	ch := gen(ctx, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	<-ch // 只收一个就撤
	cancel()
	for range 50 {
		if runtime.NumGoroutine() <= before {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("goroutine leak after cancel: before=%d after=%d", before, runtime.NumGoroutine())
}

// TestFanin：三路汇一路，总数对得上（顺序不保证，并发语义）。
func TestFanin(t *testing.T) {
	ctx := context.Background()
	a := square(ctx, gen(ctx, 1, 2, 3))
	b := square(ctx, gen(ctx, 4, 5))
	c := square(ctx, gen(ctx, 6))

	seen := map[int]bool{}
	for v := range Fanin(ctx, a, b, c) {
		seen[v] = true
	}
	for _, want := range []int{1, 4, 9, 16, 25, 36} {
		if !seen[want] {
			t.Fatalf("missing %d in %v", want, seen)
		}
	}
}
