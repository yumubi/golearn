package p21test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// ===== 预测题 =====

// TestBackoffTable 退避序列：base*2^attempt 封顶 cap。
func TestBackoffTable(t *testing.T) {
	base, cap_ := 100*time.Millisecond, 1*time.Second
	want := []time.Duration{
		100 * time.Millisecond, // attempt 0
		200 * time.Millisecond, // 1
		400 * time.Millisecond, // 2
		800 * time.Millisecond, // 3
		1 * time.Second,        // 4（封顶）
		1 * time.Second,        // 5
	}
	for attempt, w := range want {
		if got := Backoff(base, cap_, attempt); got != w {
			t.Fatalf("Backoff(attempt=%d) = %v, want %v", attempt, got, w)
		}
	}
}

// TestRetryVirtualTime synctest 核心演示：3 次失败 + 2 次退避
// （100+200ms），虚拟时间恰好走 300ms。若退避倍数错了
// （变异 p21-backoff-double），虚拟账本立刻对不上。
func TestRetryVirtualTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		calls := 0
		err := Retry(context.Background(), RetryOptions{
			Max:  3,
			Base: 100 * time.Millisecond,
			Cap:  10 * time.Second,
		}, func() error {
			calls++
			return errors.New("boom")
		})
		if err == nil {
			t.Fatal("want error after exhausting retries")
		}
		if calls != 3 {
			t.Fatalf("calls = %d, want 3", calls)
		}
		elapsed := time.Since(start)
		if elapsed != 300*time.Millisecond {
			t.Fatalf("virtual elapsed = %v, want exactly 300ms (100+200 backoff)", elapsed)
		}
	})
}

// TestRetrySuccessSecond 第二次成功：退避只走了一段。
func TestRetrySuccessSecond(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		calls := 0
		err := Retry(context.Background(), RetryOptions{
			Max: 5, Base: 50 * time.Millisecond, Cap: time.Second,
		}, func() error {
			calls++
			if calls < 2 {
				return errors.New("flaky")
			}
			return nil
		})
		if err != nil {
			t.Fatalf("want success, got %v", err)
		}
		if got := time.Since(start); got != 50*time.Millisecond {
			t.Fatalf("virtual elapsed = %v, want exactly 50ms", got)
		}
	})
}

// TestRetryCtxCancelImmediate ctx 取消时重试立刻停，不打完退避。
func TestRetryCtxCancelImmediate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		cancel() // 进来就已取消
		err := Retry(ctx, RetryOptions{
			Max: 100, Base: time.Hour, Cap: time.Hour,
		}, func() error {
			calls++
			return errors.New("nope")
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("want context.Canceled, got %v", err)
		}
		if calls != 0 {
			t.Fatalf("calls = %d, want 0 (cancel must stop before first attempt)", calls)
		}
		if got := time.Since(start); got != 0 {
			t.Fatalf("virtual elapsed = %v, want 0", got)
		}
	})
}

// TestRetryCancelDuringBackoff 退避等待中被取消：立即返回，不等满。
func TestRetryCancelDuringBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(50 * time.Millisecond) // 此时正在第一次 100ms 退避里
			cancel()
		}()
		calls := 0
		err := Retry(ctx, RetryOptions{
			Max: 10, Base: 100 * time.Millisecond, Cap: time.Second,
		}, func() error {
			calls++
			return errors.New("fail")
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("want context.Canceled, got %v", err)
		}
		if calls != 1 {
			t.Fatalf("calls = %d, want 1", calls)
		}
		if got := time.Since(start); got != 50*time.Millisecond {
			t.Fatalf("virtual elapsed = %v, want exactly 50ms (cancel point)", got)
		}
	})
}

// ===== 观察题 =====

// TestRetryOnRetryLog OnRetry 回调收到 attempt/next/err 三元组。
func TestRetryOnRetryLog(t *testing.T) {
	var mu sync.Mutex
	var log []string
	synctest.Test(t, func(t *testing.T) {
		_ = Retry(context.Background(), RetryOptions{
			Max: 2, Base: 10 * time.Millisecond, Cap: time.Second,
			OnRetry: func(attempt int, next time.Duration, err error) {
				mu.Lock()
				log = append(log, FormatAttempts(attempt, next, err))
				mu.Unlock()
			},
		}, func() error { return errors.New("x") })
	})
	if len(log) != 1 {
		t.Fatalf("log = %v, want 1 entry", log)
	}
	want := "attempt 1 failed: x, retrying in 10ms"
	if log[0] != want {
		t.Fatalf("log[0] = %q, want %q", log[0], want)
	}
}

// TestDebounceSynctest 防抖：3 次快速 Call 只触发最后一次。
// synctest.Wait 后 fired 还是 0（计时器未到）；虚拟时间走满 wait 后恰 1 次。
func TestDebounceSynctest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := NewDebounce(200 * time.Millisecond)
		var fired atomic.Int32
		d.Call(func() { fired.Add(1) })
		d.Call(func() { fired.Add(1) }) // 重置第一次
		if reset := d.Call(func() { fired.Add(1) }); !reset {
			t.Fatal("third call must report reset=true")
		}
		synctest.Wait()
		if fired.Load() != 0 {
			t.Fatalf("fired before wait elapsed = %d, want 0", fired.Load())
		}
		time.Sleep(200 * time.Millisecond)
		synctest.Wait()
		if fired.Load() != 1 {
			t.Fatalf("fired = %d, want exactly 1 (debounced)", fired.Load())
		}
	})
}

// TestDebounceStop Stop 后不再触发。
func TestDebounceStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := NewDebounce(100 * time.Millisecond)
		var fired atomic.Int32
		d.Call(func() { fired.Add(1) })
		d.Stop()
		time.Sleep(500 * time.Millisecond)
		synctest.Wait()
		if fired.Load() != 0 {
			t.Fatalf("fired after Stop = %d, want 0", fired.Load())
		}
	})
}

// TestGoroutineLeakDetection 观察：bubble 泄漏一个 goroutine 时
// synctest.Wait 的行为。泄漏的 goroutine 卡在 bubble 内 channel 上
// （durable），Wait 能返回；但 Test 返回前 bubble 要求全部 goroutine
// 退出 → Test panic 指认泄漏者。用子进程隔离跑（leak 模式）。
func TestGoroutineLeakDetection(t *testing.T) {
	if testing.Short() {
		t.Skip("skip-ok: 子进程模式在 -short 下跳过")
	}
	out, err := runProbe(t, "leak")
	if err == nil {
		t.Fatalf("leak mode should fail, got success\n%s", out)
	}
	if !strings.Contains(string(out), "PANIC:") {
		t.Fatalf("leak mode want panic, got\n%s", out)
	}
	t.Logf("leak mode real output:\n%s", out)
}

// ===== bubble 挂死/泄漏演示（子进程隔离，不拖垮整包） =====

// deadlockProbe 源码：被子进程实验用 go build 编译执行。
// 模式 chan：bubble 内无人发的 channel 接收 → 所有 goroutine durable → panic。
// 模式 mutex：goroutine 卡互斥锁（非 durable）→ 主 goroutine synctest.Wait
// 永不返回 → 挂死（外部用 3s 墙钟判定后 kill）。
// 模式 leak：泄漏一个卡在 bubble channel 上的 goroutine → Test 收尾时
// panic 指认泄漏。
const deadlockProbe = `package main

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func main() {
	mode := os.Args[1]
	if mode == "leak" {
		defer func() {
			if r := recover(); r != nil {
				fmt.Println("PANIC:", r)
				os.Exit(7)
			}
		}()
		synctest.Test(&testing.T{}, func(t *testing.T) {
			ch := make(chan int) // bubble 内 channel
			go func() { <-ch }() // 泄漏：没人发、没人收尾
			time.Sleep(time.Millisecond)
		})
		fmt.Println("no panic?!")
		return
	}
	if mode == "chan" {
		defer func() {
			if r := recover(); r != nil {
				fmt.Println("PANIC:", r)
				os.Exit(7)
			}
		}()
		synctest.Test(&testing.T{}, func(t *testing.T) {
			ch := make(chan int)
			<-ch // 永远没人发
		})
		fmt.Println("no panic?!")
		return
	}
	// mode == "mutex"
	synctest.Test(&testing.T{}, func(t *testing.T) {
		var mu sync.Mutex
		mu.Lock()
		go func() {
			mu.Lock() // 非 durable：外层事件（主解锁）才能解开
		}()
		time.Sleep(1 * time.Millisecond)
		synctest.Wait() // 等其它全 durable：子卡锁永不 durable → 挂死
	})
	fmt.Println("wait returned?!")
}
`

// runProbe 编译并运行 deadlockProbe 的某个模式。
func runProbe(t *testing.T, mode string) ([]byte, error) {
	t.Helper()
	dir := t.TempDir()
	src := dir + "/main.go"
	if err := os.WriteFile(src, []byte(deadlockProbe), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := dir + "/probe"
	if out, err := exec.Command("go", "build", "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("build probe: %v\n%s", err, out)
	}
	return exec.Command(bin, mode).CombinedOutput()
}

// TestBubbleHangs 子进程跑三种模式，断言行为与真机 1.26.4 实测一致：
// 模式 chan（全 durable）→ synctest panic 文本 + 退出码 7；
// 模式 mutex（互斥锁，非 durable）→ runtime fatal "all goroutines are
// asleep - deadlock!"（不是 synctest 的 panic，也不是挂住：非 durable
// 阻塞让 runtime 认定无人可调度且没有计时器可推进，进程级 fatal）；
// 模式 leak → Test 收尾 panic 指认泄漏 goroutine。
func TestBubbleHangs(t *testing.T) {
	if testing.Short() {
		t.Skip("skip-ok: 子进程模式在 -short 下跳过")
	}

	// 模式 A：channel 死锁 → panic
	out, err := runProbe(t, "chan")
	if err == nil {
		t.Fatalf("chan mode should exit 7, got success\n%s", out)
	}
	if !strings.Contains(string(out), "PANIC:") {
		t.Fatalf("chan mode want panic text, got\n%s", out)
	}
	t.Logf("mode A (all durably blocked) real output:\n%s", out)

	// 模式 B：互斥锁（非 durable）→ runtime fatal deadlock
	out, err = runProbe(t, "mutex")
	if err == nil {
		t.Fatal("mutex mode should fail with runtime fatal, got success")
	}
	if !strings.Contains(string(out), "fatal error: all goroutines are asleep - deadlock!") {
		t.Fatalf("mutex mode want runtime fatal deadlock, got\n%s", out)
	}
	if strings.Contains(string(out), "wait returned") {
		t.Fatalf("mutex mode must not reach past synctest.Wait, got\n%s", out)
	}
	t.Logf("mode B (mutex, not durably blocked) real output head:\n%s", strings.SplitN(string(out), "\n", 2)[0])
}
