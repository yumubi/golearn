---
title: P3 · 并发：goroutine vs tokio task
---

# P3 · 并发：goroutine vs tokio task


> 你的 Rust 学习线（_rust_learning L1/L3/L4）在这里全额兑现：Go 的并发
> 模型和 tokio 同构，但**内建在语言里**，没有 runtime crate 可选。
> 读者：写过多线程 Rust/tokio，第一次写 Go 并发。前置：无。
> 后续：P14 把本章原语组装成生产流水线（errgroup/pipeline）。

## 这章学完你能干什么

写出一轮带超时和取消的并发任务：`go` 起 goroutine、channel 收结果、
`select` 挂超时分支、ctx 贯穿取消，并用 `go test -race` 验证没有
数据竞争。四个问题依次是：

1. goroutine 和 tokio task 到底差在哪（急切、无让出点标记、panic 全进程死）？
2. channel + select 怎么用才不踩坑（close 语义、nil channel、随机分支）？
3. 取消为什么用 context 而不是 drop（ctx 纪律）？
4. 没有 borrow checker，共享状态怎么保证安全（race detector + 决策树）？

练习在 `exercises/p3conc/`（6 个测试），读完正文你应该能预测
`TestPool` 的 worker 怎么退出、`TestPanicKillsProcess` 会发生什么。

## 问题 1：goroutine 和 tokio task 到底差在哪？

先看对比的核心四条，完整的 Rust/tokio 对照大表在章末参考：

| 差异 | Go | Rust/tokio |
|---|---|---|
| 启动 | `go func() { ... }()`，**急切**，立刻调度 | `tokio::spawn(async {...})` 也是急切，但 future 本身惰性，`poll` 驱动 |
| 让出点 | **编译器+runtime 在函数调用点插入，源码不可见** | `.await` 显式标记 |
| panic | **任何 goroutine panic 杀整个进程** | task panic 只杀该 task（catch_unwind 边界） |
| 栈 | 初始 2KB，按需增长 | 状态机，无栈 |

goroutine 直接跑，长得像同步代码，`.await` 没有对应物，runtime 偷偷
切走。好处是代码好读；代价是让出点没有标记，你在源码里找不到
"这里可能切换"。调度器是 GMP（G=goroutine, M=OS 线程, P=processor）。
P 是 Go 特有的概念：tokio 每个 worker 也有本地 run queue + LIFO slot
（2019 调度器重构后就有），但 P 还管理着本地缓存的 mcache 和
netpoller 集成，粒度比 tokio 的 worker 更重。

panic 这条最痛。你的 netty 经验：EventLoop 里抛异常是同样的事故。
必须在每个 goroutine 入口自己 recover。练习包里 `TestPanicKillsProcess`
是个"给读不给跑"的测试：删掉 `t.Skip` 运行，整个测试进程死掉，
体会后恢复。

## 问题 2：channel + select 怎么用才不踩坑？

channel 是 Go 并发的第一等公民：可传参、可 close、可 range。

```go
ch := make(chan int)        // 无缓冲：发送阻塞到有人收（≈ rendezvous）
ch := make(chan int, 10)    // 有缓冲：满了才阻塞
ch <- v                     // 发送
v := <-ch                   // 接收
v, ok := <-ch               // ok=false = channel 已关闭且空（≈ tokio RecvError）
close(ch)                   // 只有发送方 close（≈ tokio drop Sender）
for v := range ch { }       // 迭代到关闭
```

Go 的 channel 是 **tokio mpsc + oneshot + broadcast 的合体**：

- 双向 channel 可以转单向：`func produce(ch chan<- int)`（只发）、
  `<-chan int`（只收）
- `close` 语义 ≈ Rust drop Sender：接收方收到零值+false
- **nil channel 永久阻塞**。这不是坑，是惯用法：select 里把某个
  分支的 channel 置 nil，就禁用了该分支

select 是语言关键字（`tokio::select!` 的对应物）：

```go
select {
case v := <-in:
    process(v)
case <-ctx.Done():        // ≈ tokio::select! { _ = ctx.cancelled() => {} }
    return ctx.Err()
case <-time.After(time.Second):   // ≈ tokio::time::timeout
}
```

三个差异要背下来：

1. Go select **随机**挑就绪分支（防饿死），tokio::select! 默认随机、
   可 `biased;`。
2. case 只能是 channel 操作，没有模式匹配。
3. `time.After` 在 Go 1.23 前会在循环里泄漏（timer 到期前不被 GC
   回收）；1.23 起 GC 能回收无引用未停止的 timer，官方明确
   "no reason to prefer NewTimer when After will do"（time/sleep.go
   的 After 注释）。老代码里的 `NewTimer`+`Reset` 循环仍是对的，
   新代码可以直接 `time.After`。练习 `TestFirstOrTimeout` 的超时分支
   就踩在这个知识点上。

## 问题 3：取消为什么用 context，而不是 drop？

Rust 取消是「drop Future」：作用域结束，future 没人 poll 就死了。
Go 的 goroutine 没有 drop，**取消必须是显式协议**，这就是
`context.Context`，Go 生态的取消统一载体：

```go
ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
defer cancel()   // 惯例：谁创建谁 cancel（≈ Rust drop guard）

func fetch(ctx context.Context, url string) error {
    req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
    // 所有阻塞调用都收 ctx：数据库、HTTP、RPC……
}
```

规矩（deepsearch/ejdb 都遵守）：

- ctx 永远是**第一个参数**，命名恒为 `ctx`
- ctx 只传取消信号/截止时间/请求级 kv，**不传业务参数**
  （≈ 不拿 CancellationToken 当 HashMap 用）
- `ctx.Err()` 返回 nil / Canceled / DeadlineExceeded
- 你 L9 的"过载降级"在 Go 的形态：每个 handler 第一件事就是看 ctx

对比一句话：Rust 的取消在**类型系统里**（drop guard 自动触发），
Go 的取消在**参数列表里**（你忘了传、忘了检查，它就静默失效）。
所以 ctx 纪律是纪律，不是编译器保证。

## 问题 4：没有 borrow checker，共享状态怎么保证安全？

这行代码完全合法，且是数据竞争：

```go
c := 0
for i := 0; i < 10; i++ { go func() { c++ }() }  // race！
```

编译器一声不吭。两条防线：

1. `go test -race` / `go run -race`：动态检测（TSan），**CI 必开**。
2. 惯例："Don't communicate by sharing memory, share memory by
   communicating"，能用 channel 传所有权就传（Go 版的 move 语义，
   纯靠纪律）。

真要共享状态，从 Rust 决策树翻译：

| 你的 Rust 决策树 | Go 对应 |
|---|---|
| 单计数 → AtomicU64 | `atomic.Int64`（1.19+ 类型化封装） |
| 复合临界区 → Mutex | `sync.Mutex`（**零值可用**，不需 new） |
| 读多写少 → RwLock | `sync.RWMutex` |
| 一次性初始化 → OnceLock | `sync.Once` |
| ArcSwap | `atomic.Pointer[T]`（1.19+） |
| Map 并发 → dashmap | `sync.Map`（专用场景）或分片锁 |

练习 `TestCounters` 把 mutex 和 atomic 两种姿势各写一遍，写之前
用 race detector 抓一次裸竞争（测试注释里写了怎么触发）。

## 动手：练习 p3conc（30-45 分钟）

```bash
go test -race ./exercises/p3conc/...          # P3 必须 -race（问题 4 说过为什么）
go test -race -run TestPool -v ./exercises/p3conc/
```

**预测题**（先写答案再跑）：

1. `TestPool` 里 worker 从 `jobs` 收任务，`close(jobs)` 之后 worker
   怎么知道该退出了？如果没有 close，会卡在哪一行？（§问题 2）
2. `TestMerge` 未实现时死锁，"all goroutines are asleep" 卡住的
   goroutine 在等什么？（§问题 2）
3. `TestPanicKillsProcess` 删掉 `t.Skip` 运行，go test 退出码是多少？
   tokio 里同样的 panic 杀什么？（§问题 1）
4. `TestLimit` 里 ctx 已经 cancel，为什么 `Limit` 必须在起任务**前**
   检查 ctx？（§问题 3）

**观察验证**：

```bash
go test -race ./exercises/p3conc/ -run TestCounters -v   # 看 race detector 绿灯
go run -race -run TestPanicKillsProcess                   # 体会进程死亡（记得恢复 Skip）
```

| 练习 | 考什么 | 在哪 |
|---|---|---|
| TestPool | goroutine 池 + channel 收敛，close 生命周期 | p3conc |
| TestMerge | fan-in：多路合一 + close 总闸 | p3conc |
| TestLimit | SetLimit 限并发 + ctx 取消语义 | p3conc |
| TestFirstOrTimeout | select 三分支：结果/超时/取消 | p3conc |
| TestCounters | atomic vs mutex，-race 抓裸竞争 | p3conc |
| TestPanicKillsProcess | goroutine panic = 进程死（读，别跑） | p3conc |

**改造题**：

- errgroup 限并发（`TestLimit` 的官方版）：用 `errgroup.SetLimit` +
  首错返回重写 `Limit`（对照 tokio try_join）。
- 无 race 的计数器：`TestCounters` 的 mutex vs channel 两种姿势都写。
- （§问题 1 的实测作业）写 10 万 goroutine ping-pong benchmark，
  对比内存和每跳延迟，数据贴回 notes，把 §实测 的 [INFERENCE] 标记消掉。

## Take-away

1. goroutine ≈ tokio task：都便宜、都百万级、都不是线程。差异四条：
   急切、让出点不可见、panic 杀全进程、2KB 栈。
2. channel + select 内建：只有发送方 close，nil channel 永久阻塞
   （select 禁用分支的惯用法），select 随机挑就绪分支。
3. context 是取消的统一协议，全生态贯穿：ctx 第一参数、只传信号
   不传业务、谁创建谁 cancel。Rust 的 drop 换成了显式纪律。
4. 没有 Send/Sync：race detector（`-race`，CI 必开）是编译器之外
   的唯一防线；能用 channel 传所有权就传。
5. goroutine panic = 进程死，每个 goroutine 入口想清楚 recover；
   批量任务的生命周期交给 errgroup（P14 展开）。

---

## 章末参考

### §实测：goroutine 切换成本 vs tokio task

[INFERENCE，待你实测] 同机对照实验设计：10万 goroutine ping-pong vs
10万 tokio task ping-pong，测每跳延迟和内存。预期：Go 切换略慢
（栈拷贝 vs 状态机 resume），内存初始 2KB vs tokio 的更小。
这个实验留给你 p3 练习做完后自己跑：方法沿用 _rust_learning L1。

### 模型对照大表（对标你的 L1 到 L4）

| Rust/tokio（你学过的） | Go | 关键差异 |
|---|---|---|
| `tokio::spawn(async { ... })` | `go func() { ... }()` | 语法内建；**没有 Future/async 语法**，函数本身就是可暂停的 |
| task（单次堆分配，元数据 64B + future 状态机本体，无栈） | goroutine（**初始 2KB 栈，可增长**，runtime/proc.go:5322 `malg(stackMin)`） | 都是绿色线程，不是 OS 线程；tokio task 不是"小栈"而是无栈+元数据 |
| runtime：多线程 work-stealing（每 worker 本地 run queue + LIFO slot） | runtime：GMP 调度器（G=goroutine, M=OS线程, P=processor） | 两边都有 work-stealing 和本地队列；P 是 Go 特有（还管 mcache/netpoller） |
| `Future` 是状态机，惰性，poll 驱动 | goroutine 直接跑，**编译器+runtime 在函数调用点插入让出点** | Rust 惰性、组合子丰富；Go 急切、只能靠 channel/锁组合 |
| `.await` | 没有对应物，阻塞式写法，runtime 偷偷切走 | **Go 代码长得像同步代码**；代价是看不到让出点 |
| `tokio::select!` | `select { case v := <-ch: ... }` | Go select 是语言关键字；没有偏置（随机选择就绪分支） |
| `tokio::sync::mpsc` | `chan T`（内建） | channel 是一等公民：可传参、可 close、可 range |
| `Arc<Mutex<T>>` | `sync.Mutex` + 裸指针共享 | **没有所有权系统**，共享内存随便传，正确性靠纪律+race detector |
| `Send`/`Sync` 编译期检查 | 无 | 编译器不拦数据竞争，靠 `go test -race` |
| `tokio::sync::Semaphore` | `chan struct{}` 容量 或 `golang.org/x/sync/semaphore` | 你 L9c 的 shed 模式在 Go 里用 buffered channel 实现最常见 |
| `CancellationToken` | `context.Context` | Go 专属设计，全生态贯穿 |
| `JoinHandle` | `sync.WaitGroup` / errgroup | 没有 join 单个 goroutine 的句柄；集合 join 靠 WG |
| `tokio::spawn_blocking` | 不需要 | goroutine 本来就能跑阻塞代码（栈自动增长，调度器扛得住） |

**一句话**：tokio 是"你自己选择把 async 编译进哪些依赖"，Go 是"整
个语言、整个标准库、整个生态都已经是 async 的，只是长得像同步"。

### 共享状态决策树的补注

- `sync.Mutex`/`RWMutex`/`sync.Once`/`sync.Map` 全部零值可用，
  `var mu sync.Mutex` 直接 Lock，这是 P2 问题 4 的零值哲学在 sync
  包的落地。
- "Don't communicate by sharing memory, share memory by
  communicating"（Effective Go）：channel 传数据 ≈ Rust 的 move
  语义，但没有编译器盯着，纯靠纪律 + `-race` 兜底。

时间相关的并发测试（Sleep/计时器怎么测）见 P21（testing/synctest：
假时钟零注入）。
