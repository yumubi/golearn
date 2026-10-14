# 07 并发二：context、pipeline、worker pool、errgroup 与优雅关停

> 详细程度：深入 · 约 35 分钟
> 材料：TB33、TB35、`C:dachang/concurrence`、`C:go_basic/concurrence` · 旧长版：`notes/p14-pipelines.md`、`notes/p3-concurrency.md`
> 前置：06 并发一（channel 四态、select、sync 选型、泄漏）

## 这一节要建立的认知

上一节给了零件，这一节给组装图。工程里的并发只有三个问题反复出现：

1. **怎么让一堆 goroutine 一起停**：靠 `context`，把"退出条件"做成统一的、可传播的机制。
2. **怎么控制规模**：靠 worker pool、`errgroup.SetLimit`、带缓冲 channel 的**背压**，让系统在过载时变慢，而不是变死。
3. **怎么收摊**：关停时先停止接收新活，再等在途的做完，最后设一个超时兜底。

所有套路都服务这三件事。判断一段并发代码好不好，先问三句：它怎么停？上游快下游慢时会发生什么？每个 goroutine 因什么退出？

## 和你已知语言的差异

| 问题 | Java | Rust（tokio） | Go |
|---|---|---|---|
| 取消与传播 | 中断位、`Future.cancel`，传播靠手工 | drop、`CancellationToken` 的子 token | `context.Context` 显式传参，`WithCancel`/`WithTimeout` 派生，父取消则子全部取消 |
| 线程池 | `ExecutorService` 内建 | 运行时自带 | 没有标准池，用 channel 加 goroutine 自己搭 |
| 优雅关停 | `shutdown hook`、`Executor.shutdown` | 信号加 `select!` | `signal.NotifyContext` 加 `Server.Shutdown` |

结构化并发（一个失败则取消兄弟）在 Go 里对应 `errgroup`，第四节细讲。

## 一、context：取消的统一机制

`context.Context` 同时承担两个职责：取消信号和请求范围内的值。取消是它的主业，值是副业。〔C:go_basic/concurrence/context.go:76-127〕

三条必须记住的规则，已在 go1.26.4 上验证：

1. **子不会比父活得久**：父剩 50ms，你给子设 1s，子仍然在 50ms 到期。
2. **`cancel` 必须调用**：`defer cancel()`，否则定时器和子节点一直挂着。
3. **取消原因可以带出来**：`WithCancelCause` 加 `context.Cause`，`Err()` 只会告诉你 `context canceled`，`Cause` 才能告诉你为什么。

```go
parent, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
defer cancel()
child, cancel2 := context.WithTimeout(parent, time.Second) // 想要 1s
defer cancel2()
<-child.Done()
fmt.Println(child.Err()) // context deadline exceeded，约 50ms 后

c, cancelC := context.WithCancelCause(context.Background())
cancelC(errors.New("upstream gone"))
fmt.Println(c.Err(), "|", context.Cause(c)) // context canceled | upstream gone
```

超时的写法有四种，只有一种是标准答案：用 `context.WithTimeout`，耗时函数放进独立 goroutine，`select` 同时等结果和 `ctx.Done()`；`time.After` 本身没问题（原材料说它的定时器不回收，那是 Go 1.23 之前的行为，1.23 起未被引用的定时器可被垃圾回收，〔1.26 修正〕见 `go doc time.After`），但它只管超时、不能向下传播取消；手动 `WithCancel` 加 `Sleep` 容易出错。〔C:dachang/concurrence/timeout.go:21-105〕

用法约定：`ctx` 放在函数第一个参数，不放进结构体字段；`WithValue` 的 key 用自定义类型避免碰撞，只用于跨 API 边界携带请求范围数据（追踪 ID 之类），不当可选参数用。〔C:go_basic/concurrence/context.go:76-127〕服务端还有一个常被忽略的点：客户端超时撤销请求，服务端 `r.Context()` 会收到 `Done`，应该及时放弃处理。〔C:dachang/concurrence/timeout/server/main.go:1-35〕

## 二、pipeline：每一级都要听得见取消

pipeline 是"生成器、处理器、汇聚"串起来的 channel 链。写法的核心规则只有一条：**每一级的每一次发送都要和 `ctx.Done()` 一起放进 `select`**，否则下游走了，上游就卡死在发送上。

```go
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
```

扇出是同一个输入 channel 交给多个 worker，扇入是把多个 channel 合并成一个，合并时要用 `WaitGroup` 等所有输入读完再关闭输出。

〔实测〕`gen` 之后接两路 `square` 再 `merge`，消费 5 个结果后 `cancel()`，等待片刻，goroutine 数量回到起点（残留 0）。把 `select` 里的 `ctx.Done()` 分支去掉，就会残留一批卡在发送上的 goroutine。这就是 06 讲的泄漏，在 pipeline 里的真实形态。

扇出会打乱顺序。需要保序有两个办法：按下标写入结果切片，或者让任务带序号再排序。按下标写入最简单，各 goroutine 写各自的槽位，没有竞争。

## 三、worker pool：把规模钉死

每请求一个 goroutine 在中小规模下完全合适；规模上去、或者下游资源有限（数据库连接、文件句柄）时，要限制同时在跑的数量。〔TB35 p2〕最小的 pool 只需要一个任务 channel 和 N 个 worker：

```go
func pool(n int, tasks []Task) []int {
	res := make([]int, len(tasks))
	jobs := make(chan int) // 无缓冲：worker 全忙时，提交方会阻塞
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				res[i] = tasks[i]()
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
```

〔实测〕3 个 worker 处理 6 个耗时递减的任务，结果仍是 `[0 1 4 9 16 25]`，用 `go run -race .` 无报警。

生产级的 pool 要补三块：容量校验（容量小于等于 0 回退默认值）、worker 内的 `recover`（一个任务 panic 不能带走进程，同时要释放计数）、`Free` 时通过 `quit` channel 通知所有 worker 退出。〔TB35 p5-9〕可选行为（预创建 worker、池满时阻塞还是返回错误）用 functional options 配置，和 05 讲的 Option 模式一致。〔TB35 p12-15〕

另一种更轻的"池"：用容量为 N 的 channel 当计数信号量，每个任务自己起 goroutine，但先拿令牌。06 讲过，适合任务数不大、不想维护 worker 的场景。

## 四、errgroup：带错误、带取消、带上限的 WaitGroup

`WaitGroup` 只管等。工程里它缺三件事：传递错误、一个失败后其他的还在白跑、不限并发。`golang.org/x/sync/errgroup` 补齐：

- `errgroup.WithContext(ctx)`：任何一个 `g.Go` 返回非 nil 错误，派生出的 `ctx` 被取消，`Wait()` 返回**第一个**错误。
- `g.SetLimit(n)`：同时运行的 goroutine 不超过 n。**闸门满时 `g.Go` 会阻塞调用方**，这是一个非显然语义。`TryGo` 不阻塞，返回是否提交成功。

```go
g, ctx := errgroup.WithContext(context.Background())
for i := 1; i <= 4; i++ {
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

〔实测〕用 `go run .` 验证：`Wait` 返回 `task 2 failed`，耗时不到 100ms；`SetLimit(2)` 连续 `Go` 四次，调用方被阻塞了至少 50ms。

只返回第一个错误是设计如此。要收集全部错误，用 `errors.Join` 或自己 `append`（05 讲过）。对照：Kotlin 的 `coroutineScope`、Java 21 的 `StructuredTaskScope` 是同一个模型；JS 的 `Promise.all` 遇到第一个失败就返回，但**不会取消其余任务**。

## 五、背压：慢要有路径，不是消失

上游快、下游慢时，数据必须去某个地方。三种选择：

1. **阻塞上游**：有界 channel 满了，发送方自然阻塞，压力一路传回源头。这是默认首选，系统变慢但不丢数据。无缓冲 channel 是容量为 0 的极端。
2. **丢弃**：`select { case q <- x: default: dropped++ }`。适合指标、日志这种"丢一点无所谓"的数据。〔实测〕容量 2 的队列连发 5 个，入队 2 个，丢弃 3 个。
3. **拒绝**：对外返回 429、`ErrNoIdleWorker` 之类的错误，让调用方重试或降级。适合有明确失败语义的请求。

唯独不应该做的是**无界排队**：用无界切片或 channel 加一个生产者，内存悄悄涨到被系统杀掉，而且排在队尾的请求早已超时，处理它们是纯浪费。限流、熔断、降级会在稳定性那一节展开。

## 六、优雅关停：停止接收、等在途、设兜底

关停的顺序固定：先让监听器不再接新请求，再等已经接收的请求做完，最后设一个总超时，到点强退。`Shutdown` 会立即关闭所有 listener 和空闲连接，并等待活动连接处理完。〔TB09 p9〕

```go
srv := &http.Server{Addr: ":8080"}
ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
defer stop()

errc := make(chan error, 1)
go func() { errc <- srv.ListenAndServe() }()

<-ctx.Done() // 1 收到信号
sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()
err := srv.Shutdown(sctx)  // 2 停止接收，等在途做完，超时则返回 ctx 错误
fmt.Println(err, <-errc)   // 3 ListenAndServe 随后返回 http.ErrServerClosed
```

〔实测〕自己给自己发 `SIGTERM`，依次输出：`收到信号: context canceled`、`Shutdown: <nil>`、`ListenAndServe 返回: http: Server closed`。

关停时的几个易错点：

- 多个发送方时，**不要由某个接收者直接 `close(stopCh)`**，可能重复关闭而 panic。让一个"主持人" goroutine 统一关闭，接收者只发"我要停"的信号，发送方的 `select` 同时监听 `stopCh` 和数据通道。〔C:dachang/concurrence/main/grace_close_channel.go:16-93〕
- 后台 goroutine 也要纳入：用 `WaitGroup` 或 `errgroup` 等待它们，不要只等 HTTP 请求。
- 超时兜底必须有，否则一个卡住的请求会让进程永远关不掉。

## 七、并发安全的集合与单例

- **map 并发写会 `fatal error`，不能 recover**；数组、切片、结构体并发改只是脏写。要共享 map，用 `sync.RWMutex` 包一层，或者读多写少的缓存用 `sync.Map`。读写都很重时，分段锁（按 key 哈希分到多个 map，每个一把锁）降低冲突。〔C:go_basic/concurrence/collection_safety.go:6-11〕〔C:dachang/concurrence/concurrent_hash_map.go:10-106〕
- **单例与懒初始化**：`sync.Once`，或者带返回值的 `sync.OnceValue`（1.21 起）。〔实测〕三个 goroutine 同时调用，初始化函数只执行一次，输出一行 `load once`。
- `sync.Once` 的热路径靠原子读避免加锁，手写双检锁容易在内存序上出错，直接用标准库。〔C:dachang/concurrence/once.go:7-31〕

## 常见坑

1. pipeline 某一级的发送没有 `ctx.Done()` 分支，下游提前退出后上游永久卡住。
2. 超时返回后，子 goroutine 仍往无缓冲结果 channel 发送，永久阻塞（泄漏的成因与检测见 06 节第六节）。〔实测〕用 `runtime.NumGoroutine()` 前后相减，50 次请求全部超时：无缓冲泄漏 50 个 goroutine，给 1 个缓冲后泄漏 0。
3. 忘记 `defer cancel()`，定时器泄漏。
4. `errgroup` 里的任务不监听 `ctx`，一个失败后兄弟还在白跑。
5. 关停时只关监听器，不等在途请求；或者等待没有总超时。
6. 无界队列：压力没有出口，内存先爆。
7. 在 `main` 里直接 `context.Background()` 传给长运行服务，没有接信号，进程收到 SIGTERM 直接被杀，没有机会收摊。

## 判断题（先答再看）

1. `WithTimeout(parent, 1*time.Second)`，而 `parent` 还剩 50ms，子 context 什么时候到期？
2. 为什么超时处理里，结果 channel 给 1 个缓冲就能避免泄漏？
3. `errgroup.SetLimit(2)` 之后，连续调用 5 次 `g.Go`，调用方会发生什么？
4. 三种背压策略各自适合什么数据？
5. 优雅关停为什么要先停监听器再等在途请求，顺序反过来会怎样？

练习包 `exercises/p14pipe/`：`TestPipeline`、`TestFanoutResultOrder`、`TestFanoutLimit`、`TestFanoutFirstError`、`TestFanoutGoroutineLeak`、`TestGenCancel`、`TestFanin`；`exercises/p31sched/` 的 `TestWorkerPool`、`TestChildCtxInheritsDeadline`。

## 在项目里

- **背压**：`dnsproxi-go/main.go` 的 `Run` 在 `WorkerPool` 形态下，读循环把包写入带缓冲的 `qch`，满了读循环阻塞，压力回到内核的接收缓冲区，这是"阻塞上游"的真实例子。读错误时先 `close(qch)` 让 worker 排空，再 `wg.Wait()`，与关停顺序一致。
- **取消感知**：`Run(ctx)` 在 `ctx` 取消时关闭监听口，读循环因此返回，并按"取消就返回 nil、否则包装错误"区分优雅关停与异常退出。
- **真实的缺口**：`dnsproxi-go/main.go` 的 `main` 现在传的是 `context.Background()`，没有接入信号，所以收到 SIGTERM 时进程直接被杀，`Run` 里写好的优雅关停分支实际用不上。把它改成 `signal.NotifyContext` 加超时兜底，是一道现成的改造题，对照上面第六节。
- **接上信号之后会撞上的第二个缺口**：`Run` 在 `DNS_CACHE_ACTIVE_TTL=1` 时会让后台回收 goroutine 加入同一个 `WaitGroup`，而它要等 `done` 通道关闭才退出；`done` 却是在 `Run` 返回时才关闭的，读循环退出后 `Run` 先 `wg.Wait()`，于是互相等待。我在 HEAD 版本上实测：取消 ctx 后，关闭回收的配置 2 秒内返回，打开回收的配置 2 秒仍没有返回。修法是在 `wg.Wait()` 之前先关闭 `done`。这是 `WaitGroup` 与通道互等的典型形态，也是做第一道改造题时自然会撞上的。
- **后台任务的退出**：`dnsproxi-go/cache/expire.go` 的 `RunExpirer` 接受 `done` 通道，主动老化 goroutine 因此可被停止；调用处用 `sync.Once` 保证只关闭一次。

## 想深入去哪查

- 原材料：TB35（goroutine 池的完整实现：创建、worker 管理、提交调度、功能选项）；`C:dachang/concurrence`（超时四法、优雅退出、泄漏现场、手写 Once 与分段锁）；`C:go_basic/concurrence`（context、协程池与限流、海量文件三级流水线）。
- 旧长版：`notes/p14-pipelines.md`（errgroup、SetLimit、生成器、扇出保序、并发上限消融实验）、`notes/p3-concurrency.md`。
- 不在这一节：自旋锁与 CAS 的内存序细节〔C:dachang/concurrence/spinllock.go:15-50〕、文件并发写的性能对比〔C:dachang/concurrence/file_test.go:11-44〕，遇到再查。
