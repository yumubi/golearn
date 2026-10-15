# 07 并发二：context、pipeline、worker pool、errgroup 与优雅关停

> 详细程度：深入 · 约 35 分钟
> 材料：TB09、TB33、TB35、`C:dachang/concurrence`、`C:go_basic/concurrence` · 旧长版：`notes/p14-pipelines.md`、`notes/p3-concurrency.md`
> 前置：06 并发一（channel 四态、select、sync 选型、泄漏）

## 先看一个现象：1 秒的超时只活了 50 毫秒

给 `child` 设了 1 秒超时，它却不到 1 秒就到期了。先自己猜一下输出，再往下读。

```go
package main

import (
	"context"
	"fmt"
	"time"
)

func main() {
	parent, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	child, cancel2 := context.WithTimeout(parent, time.Second) // 想让子活 1 秒
	defer cancel2()

	start := time.Now()
	<-child.Done()
	fmt.Println("child:", child.Err())
	fmt.Println("远短于 1 秒:", time.Since(start) < time.Second)
}
```

```text
child: context deadline exceeded
远短于 1 秒: true
```

`child` 明明要 1 秒，却和 `parent` 一样在 50 毫秒就 `Done` 了，而且 `Err()` 给的是 `context deadline exceeded`（超时），不是 `context canceled`。这个现象背后就是 context 的第一条铁律：**子 context 的寿命不会超过父 context**。它也是这一节要回答的第一个问题的答案：怎么让一堆 goroutine 一起停。

工程里的并发绕来绕去只有三个问题：

1. **怎么一起停**：靠 `context`，把退出条件做成统一、可传播的机制。
2. **怎么控制规模**：靠 worker pool、`errgroup.SetLimit`、带缓冲 channel 的背压，让系统过载时变慢而不是变死。
3. **怎么收摊**：先停止接收新活，再等在途做完，最后设一个超时兜底。

判断一段并发代码好不好，先问三句：它怎么停？上游快下游慢时会发生什么？每个 goroutine 因什么退出？

## 和你已知语言的差异

| 问题 | Java | Rust（tokio） | Go |
|---|---|---|---|
| 取消与传播 | 中断位、`Future.cancel`，传播靠手工 | drop、`CancellationToken` 的子 token | `context.Context` 显式传参，`WithCancel`/`WithTimeout` 派生，父取消则子全部取消 |
| 线程池 | `ExecutorService` 内建 | 运行时自带 | 没有标准池，用 channel 加 goroutine 自己搭 |
| 优雅关停 | `shutdown hook`、`Executor.shutdown` | 信号加 `select!` | `signal.NotifyContext` 加 `Server.Shutdown` |

三行里最需要适应的是第一行：Go 没有全局的取消机制，`context` 是一个要你**亲手逐层传下去的普通参数**。不传，下游就不知道上游取消了。这个"显式"是 Go 并发风格的底色，和后面每一节都有关。

结构化并发（一个失败则取消兄弟）在 Go 里对应 `errgroup`，第四节细讲。

## 一、context：取消是主业，值是副业

`context.Context` 是个接口，同时承担两件事：取消信号，和请求范围内的键值。取消是主业，值只是顺带。〔C:go_basic/concurrence/context.go:76-127〕

先记住三条铁律，都在 go1.26.4 上验证过：

1. **子不长于父**。父剩 50ms，你给子设 1s，子仍在 50ms 到期。就是开头那个现象。
2. **`cancel` 必须调**。`WithTimeout`/`WithCancel` 返回的 `cancel` 要用 `defer cancel()` 挂上，否则定时器和子节点一直挂着不释放。
3. **取消原因能带出来**。`WithCancelCause` 配合 `context.Cause` 能记录"为什么取消"；`Err()` 只会说 `context canceled`，`Cause` 才说原因。

第二条和第三条用一段代码一起看：

```go
package main

import (
	"context"
	"errors"
	"fmt"
)

func main() {
	c, cancelC := context.WithCancelCause(context.Background())
	cancelC(errors.New("upstream gone"))
	fmt.Println("Err:", c.Err())
	fmt.Println("Cause:", context.Cause(c))
}
```

```text
Err: context canceled
Cause: upstream gone
```

`cancelC(errors.New("upstream gone"))` 把原因塞进去，之后任何拿到这个 ctx 的 goroutine 都能用 `context.Cause(c)` 问出"为什么停"。对比 Java：取消一个 `Future` 只有 `cancel(true)`，没有原因可查；Go 把这个原因做成了 context 的一部分。

超时是取消最常见的来源。材料里给了四种写法，最后一种最常用：〔C:dachang/concurrence/timeout.go:21-105〕

1. **手动 deadline channel**：另起 goroutine `sleep` 后 `close` 一个 channel，`select` 等它。能用，但每处都要手写一遍定时和关闭。
2. **`time.After`**：`select` 等 `time.After(d)`。它本身没问题（过去有人担心它的定时器不被回收，那是 Go 1.23 之前的行为；1.23 起未被引用的定时器能被 GC 回收，〔1.26 修正〕`go doc time.After` 是这么写的），但它只管超时，不能把取消往下游传播。
3. **手动 `WithCancel` + `sleep`**：另起 goroutine 睡到点再 `cancel`。能传播取消，但多一个 goroutine，还要保证 `cancel` 一定被调到，容易漏。
4. **`WithTimeout`**：耗时函数放进独立 goroutine，主 goroutine `select` 同时等结果和 `ctx.Done()`。超时能传播，取消也能传播，也不用自己多起 goroutine 计时。

第四种的样子：

```go
func handle() int {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan int, 1) // 给 1 个缓冲：超时后子 goroutine 还能把结果送进来再退出
	go func() { done <- LongTimeWork() }()
	select {
	case n := <-done:
		return n
	case <-ctx.Done():
		return 0
	}
}
```

`done` 给 1 个缓冲不是随手写的：如果无缓冲，超时后 `handle` 返回了，子 goroutine 还卡在 `done <- ...` 上没人接，就泄漏一个 goroutine（常见坑第 2 条）。

用法约定：`ctx` 放函数第一个参数，不放进结构体字段；`WithValue` 的 key 用自定义类型（不是裸 string/int），避免和别人的 key 碰撞；值只用于跨 API 边界携带请求范围数据（追踪 ID 之类），不当可选参数传。〔C:go_basic/concurrence/context.go:76-127〕

服务端还有个常被忽略的点：客户端超时撤销请求，服务端的 `r.Context()` 会收到 `Done`，应该及时放弃处理，别继续给一个已经走了的客户端算结果。〔C:dachang/concurrence/timeout/server/main.go:1-35〕

## 二、pipeline：每一级都要听得见取消

pipeline 是"生成器、处理器、汇聚"串起来的 channel 链。核心规则只有一条：**每一级的每一次发送，都要和 `ctx.Done()` 一起放进 `select`**。否则下游提前走了，上游就永久卡在发送上，这就是 06 讲的泄漏。

先看一个两级 pipeline（生成、平方）的完整样子：

```go
package main

import (
	"context"
	"fmt"
)

func gen(ctx context.Context, n int) <-chan int {
	out := make(chan int)
	go func() {
		defer close(out) // 发送方负责关闭
		for i := 1; i <= n; i++ {
			select {
			case out <- i:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

func square(ctx context.Context, in <-chan int) <-chan int {
	out := make(chan int)
	go func() {
		defer close(out)
		for x := range in {
			select {
			case out <- x * x:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for x := range square(ctx, gen(ctx, 5)) {
		fmt.Println(x)
	}
}
```

```text
1
4
9
16
25
```

每级的骨架一模一样：`go func` 里 `defer close(out)`，循环里 `select { case out <- ...: case <-ctx.Done(): return }`。`defer close(out)` 保证无论正常跑完还是被取消，接收方的 `range` 都能退出；`select` 保证被取消时发送方也能退出。两个出口缺一不可。

为什么"发送方负责关闭"？因为接收方不知道后面还有没有数据，它去 `close` 会让还没送完的发送方 panic（send on closed channel）。只有掌握全部发送权的一方，才敢说"我再也不发了"。

扇出是把同一个输入 channel 交给多个 worker，扇入是把多个 channel 合并成一个；扇入要用 `WaitGroup` 等所有输入读完再关输出。合并逻辑：

```go
func merge(ctx context.Context, cs ...<-chan int) <-chan int {
	var wg sync.WaitGroup
	out := make(chan int)
	for _, c := range cs {
		wg.Add(1)
		go func(c <-chan int) {
			defer wg.Done()
			for x := range c {
				select {
				case out <- x:
				case <-ctx.Done():
					return
				}
			}
		}(c)
	}
	go func() { wg.Wait(); close(out) }() // 所有输入读完才关输出
	return out
}
```

〔实测〕`gen` 接两路 `square` 再 `merge`，消费 5 个结果后 `cancel()`，等 50ms，`runtime.NumGoroutine()` 回到 1（只剩 main，残留 0）。把 `select` 里的 `ctx.Done()` 分支去掉，就会残留一批卡在发送上的 goroutine。

扇出会打乱顺序。需要保序有两个办法：按下标把结果写进切片（各 goroutine 写自己的槽位，没有竞争），或让任务带序号、收齐再排序。按下标写最简单，第三节的 worker pool 就是这么做的。

## 三、worker pool：把规模钉死

每请求一个 goroutine 在中小规模完全合适；规模上去、或下游资源有限（数据库连接、文件句柄）时，就要限制同时在跑的数量。〔TB35 p2〕最小的 pool 只需要一个任务 channel 和 N 个 worker：

```go
package main

import (
	"fmt"
	"sync"
)

type Task func() int

func pool(n int, tasks []Task) []int {
	res := make([]int, len(tasks))
	jobs := make(chan int) // 无缓冲：worker 全忙时，提交方阻塞
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				res[i] = tasks[i]() // 按下标写，天然保序
			}
		}()
	}
	for i := range tasks {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return res
}

func main() {
	tasks := make([]Task, 6)
	for i := range tasks {
		x := i
		tasks[i] = func() int { return x * x }
	}
	fmt.Println(pool(3, tasks))
}
```

```text
[0 1 4 9 16 25]
```

3 个 worker 吃 6 个任务，结果按下标写回 `res`，所以顺序不被打乱。〔实测〕这段用 `go run -race .` 跑无报警，因为每个下标只被一个 worker 写。

`jobs` 是无缓冲的，所以 worker 全忙时，提交任务的 `jobs <- i` 会阻塞。这正是背压的最朴素形态：任务太多就把生产方按在原地，而不是无限攒起来。

生产级的 pool 要再补三块：〔TB35 p5-9〕

1. **容量校验**：`New` 里对 `capacity <= 0` 回退默认值、超上限截断。
2. **worker 里的 `recover`**：一个任务 panic 不能带走整个进程，`recover` 后要释放 worker 计数再退出。
3. **`Free` 时通知退出**：通过一个 `quit` channel 广播，所有 worker 的 `select` 同时监听 `quit` 和任务 channel，收到 `quit` 就退出。

可选行为（预创建 worker、池满时阻塞还是返回错误）用 functional options 配置，和 05 讲的 Option 模式一致。〔TB35 p12-15〕"池满返回错误"正是第五节说的第三种背压策略。

另一种更轻的"池"：用容量为 N 的 channel 当计数信号量，每个任务自己起 goroutine，但先拿令牌。06 讲过，适合任务数不大、不想维护 worker 的场景。

## 四、errgroup：带错误、带取消、带上限的 WaitGroup

`WaitGroup` 只管"等"。工程里它缺三件事：传递错误、一个失败后让其他的停、限制并发。`golang.org/x/sync/errgroup` 补齐：

- `errgroup.WithContext(ctx)`：任何一个 `g.Go` 返回非 nil 错误，派生出的 `ctx` 被取消，`Wait()` 返回**第一个**错误。
- `g.SetLimit(n)`：同时运行的 goroutine 不超过 n。**闸门满时 `g.Go` 会阻塞调用方**，这是个非显然语义；`TryGo` 不阻塞，返回是否提交成功。

```go
g, ctx := errgroup.WithContext(context.Background())
for i := 1; i <= 4; i++ {
	i := i
	g.Go(func() error {
		if i == 2 {
			return errors.New("task 2 failed")
		}
		select {
		case <-time.After(300 * time.Millisecond):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
}
fmt.Println(g.Wait()) // task 2 failed，远小于 300ms 就返回
```

〔实测〕`go run .` 跑：`Wait` 返回 `task 2 failed`，耗时约 0.1ms；`SetLimit(2)` 后连 `Go` 四次，调用方阻塞约 50ms（每个任务睡 50ms，两批跑完）。

为什么能"远小于 300ms"？task 2 立刻返回错误，`WithContext` 把派生 ctx 取消，其余三个任务的 `select` 里 `ctx.Done()` 分支立刻命中，它们不再等 300ms。这里有个前提：**任务必须监听 ctx**。如果任务不 `select ctx.Done()`，一个失败后兄弟还在白跑，`Wait` 只是"先返回"，不是"先让兄弟停"。

只返回第一个错误是设计如此。要收集全部错误，用 `errors.Join` 或自己 `append`（05 讲过）。对照：Kotlin 的 `coroutineScope`、Java 的 `StructuredTaskScope`（21 起的预览 API）是同一个"一个失败取消兄弟"的模型；JS 的 `Promise.all` 遇到第一个失败就返回，但**不会取消其余任务**。

## 五、背压：慢要有路径，不是消失

上游快、下游慢时，数据必须去某个地方。三种选择：

1. **阻塞上游**：有界 channel 满了，发送方自然阻塞，压力一路传回源头。默认首选，系统变慢但不丢数据。无缓冲 channel 是容量 0 的极端。
2. **丢弃**：`select { case q <- x: default: dropped++ }`。适合指标、日志这种"丢一点无所谓"的数据。
3. **拒绝**：对外返回 429、`ErrNoIdleWorkerInPool` 之类的错误，让调用方重试或降级。适合有明确失败语义的请求。

丢弃那一类一眼看懂：

```go
package main

import "fmt"

func main() {
	q := make(chan int, 2)
	dropped := 0
	for i := 0; i < 5; i++ {
		select {
		case q <- i:
		default:
			dropped++
		}
	}
	fmt.Println("queued:", len(q), "dropped:", dropped)
}
```

```text
queued: 2 dropped: 3
```

唯独不应该做的是**无界排队**：用无界切片或 channel 加一个生产者，内存悄悄涨到被系统杀掉，而且排在队尾的请求早已超时，处理它们是纯浪费。限流、熔断、降级在稳定性那一节展开。

## 六、优雅关停：停止接收、等在途、设兜底

关停顺序固定：先让监听器不再接新请求，再等已经接收的做完，最后设一个总超时，到点强退。`Shutdown` 会立即关闭所有 listener 和空闲连接，并等待活动连接处理完。〔TB09 p9〕

```go
package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond) // 模拟在途请求
		fmt.Fprint(w, "ok")
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0") // 先同步绑好，再自己发信号
	if err != nil {
		fmt.Println("listen:", err)
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	syscall.Kill(os.Getpid(), syscall.SIGTERM) // 1 自己给自己发终止信号
	<-ctx.Done()
	fmt.Println("收到信号:", ctx.Err())

	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	fmt.Println("Shutdown:", srv.Shutdown(sctx)) // 2 停止接收，等在途做完
	fmt.Println("Serve 返回:", <-errc)            // 3 Serve 随后返回
}
```

```text
收到信号: context canceled
Shutdown: <nil>
Serve 返回: http: Server closed
```

三步和注释一一对应：收到信号（ctx 取消）到 `Shutdown` 停接收、等在途，最后 `Serve` 返回 `http.ErrServerClosed`。那句 `http: Server closed` 是正常退出的标志，不是错误。

关停时的三个易错点：

- 多个发送方时，**不要由某个接收者直接 `close(stopCh)`**，可能重复关闭而 panic。让一个"主持人" goroutine 统一关闭，接收者只发"我要停"的信号，发送方的 `select` 同时监听 `stopCh` 和数据通道。〔C:dachang/concurrence/main/grace_close_channel.go:16-93〕
- 后台 goroutine 也要纳入：用 `WaitGroup` 或 `errgroup` 等它们，不要只等 HTTP 请求。
- 超时兜底必须有，否则一个卡住的请求会让进程永远关不掉。

## 七、并发安全的集合与单例

- **map 并发写会 `fatal error`，不能 recover**；数组、切片、结构体并发改只是脏写。要共享 map，用 `sync.RWMutex` 包一层，或读多写少的缓存用 `sync.Map`。读写都很重时，分段锁（按 key 哈希分到多个 map，每个一把锁）降低冲突。〔C:go_basic/concurrence/collection_safety.go:6-11〕〔C:dachang/concurrence/concurrent_hash_map.go:10-106〕
- **单例与懒初始化**：`sync.Once`，或带返回值的 `sync.OnceValue`（1.21 起）。三个 goroutine 同时抢，初始化只跑一次：

```go
package main

import (
	"fmt"
	"sync"
)

func main() {
	var once sync.Once
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			once.Do(func() { fmt.Println("load once") })
		}()
	}
	wg.Wait()
}
```

```text
load once
```

`sync.Once` 的热路径靠原子读避免加锁，手写双检锁容易在内存序上出错，直接用标准库。〔C:dachang/concurrence/once.go:7-31〕

## 小结

1. context 的寿命规则：子不长于父；`cancel` 用 `defer` 挂上；取消原因用 `WithCancelCause` + `Cause`。
2. 超时标准写法：`WithTimeout` + 独立 goroutine + `select` 等结果和 `ctx.Done()`；结果 channel 给 1 个缓冲。
3. pipeline 每级都要 `select ctx.Done()`，发送方负责 `close`；扇入用 `WaitGroup` 再关输出。
4. worker pool 用任务 channel + N 个 worker 钉死并发；按下标写结果保序。
5. errgroup 补齐 WaitGroup 的三块：错误、取消、并发上限；任务必须监听 ctx。
6. 背压三选一：阻塞上游（首选）、丢弃、拒绝；不要无界排队。
7. 优雅关停三步：停接收、等在途、设兜底；map 并发写是 fatal error，单例用 `sync.Once`。

## 常见坑

1. pipeline 某一级的发送没有 `ctx.Done()` 分支，下游提前退出后上游永久卡住。
2. 超时返回后，子 goroutine 仍往无缓冲结果 channel 发送，永久阻塞。〔实测〕`runtime.NumGoroutine()` 前后相减，50 次请求全部超时：无缓冲泄漏 50 个，给 1 个缓冲后泄漏 0。
3. 忘记 `defer cancel()`，定时器泄漏。
4. `errgroup` 里的任务不监听 ctx，一个失败后兄弟还在白跑。
5. 关停时只关监听器，不等在途请求；或等待没有总超时。
6. 无界队列：压力没有出口，内存先爆。
7. 在 `main` 里直接 `context.Background()` 传给长运行服务，没接信号，进程收到 SIGTERM 直接被杀，没有机会收摊。

## 判断题与答案

先自己答，再看后面。

1. `WithTimeout(parent, 1*time.Second)`，而 `parent` 还剩 50ms，子 context 什么时候到期？
2. 为什么超时处理里，结果 channel 给 1 个缓冲就能避免泄漏？
3. `errgroup.SetLimit(2)` 之后，连续调用 5 次 `g.Go`，调用方会发生什么？
4. 三种背压策略各自适合什么数据？
5. 优雅关停为什么要先停监听器再等在途请求，顺序反过来会怎样？

**答案**

1. 约 50ms 后到期，且 `Err()` 是 `context deadline exceeded`。因为子 context 的 deadline 取父子中更早的那个，父到期就传播给子。
2. 缓冲让子 goroutine 在超时后仍能把结果送进 channel 再退出，不依赖接收方在场；无缓冲则发送方永远阻塞在那里。
3. 前两个 `Go` 立刻返回，第三个起会阻塞，直到有 worker 空出来（闸门满时 `Go` 阻塞调用方）。
4. 阻塞上游适合必须送达的数据；丢弃适合指标、日志；拒绝适合有明确失败语义的请求（调用方可重试）。
5. 先停监听器，新请求进不来，等在途的规模才不会再涨；反过来会边等边接新请求，可能永远等不完。

练习包 `exercises/p14pipe/`：`TestPipeline`、`TestFanoutResultOrder`、`TestFanoutLimit`、`TestFanoutFirstError`、`TestFanoutGoroutineLeak`、`TestGenCancel`、`TestFanin`；`exercises/p31sched/` 的 `TestWorkerPool`、`TestChildCtxInheritsDeadline`。

## 在项目里

- **背压（阻塞上游）**：`dnsproxi-go/main.go` 的 `Run` 在 worker pool 形态下，读循环把包写进带缓冲的 `qch`（容量等于 worker 数），满了读循环阻塞，压力回到内核的接收缓冲区。读错误时先 `close(qch)` 让 worker 排空，再 `wg.Wait()`，与关停顺序一致。
- **取消感知**：`Run(ctx)` 在 ctx 取消时先关监听口，读循环因此返回，再按"取消就返回 nil、否则包装错误"区分优雅关停与异常退出。
- **信号接入**：`main.go` 的 `main` 用 `signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)` 接住信号，`Run` 里那条优雅关停路径因此真正用得上。
- **后台任务的退出顺序**：`dnsproxi-go/cache/expire.go` 的 `RunExpirer(done, cfg)` 把主动老化 goroutine 收进同一个 `WaitGroup`，它要等 `done` 关闭才退出；`main.go` 把 `close(done)` 封装成 `stopExpirer`，在 `wg.Wait()` **之前**调用。顺序反了就互等死锁：`Run` 等 `wg`，expirer 等 `done`，而 `done` 若挂在 `Run` 返回时才关，谁也等不到谁。这是 `WaitGroup` 与通道互等的典型形态。

## 想深入去哪查

- 原材料：TB35（goroutine 池的完整实现：创建、worker 管理、提交调度、功能选项）；TB09（优雅退出的 signal.Notify + Shutdown）；`C:dachang/concurrence`（超时四法、优雅退出、泄漏现场、手写 Once 与分段锁）；`C:go_basic/concurrence`（context、协程池与限流）。
- 旧长版：`notes/p14-pipelines.md`（errgroup、SetLimit、生成器、扇出保序、并发上限消融实验）、`notes/p3-concurrency.md`。
- 不在这一节：自旋锁与 CAS 的内存序细节〔C:dachang/concurrence/spinllock.go:15-50〕、文件并发写的性能对比〔C:dachang/concurrence/file_test.go:11-44〕，遇到再查。
