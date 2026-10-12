# P3 · 并发：goroutine vs tokio task

> 这章学完你能干什么：写出一轮带超时和取消的并发任务。用 `go` 起 goroutine，用 channel 收结果，用 `select` 挂超时分支，用 ctx 贯穿取消，再用 `go test -race` 验证没有数据竞争。
> 读者：写过 Rust/tokio，第一次写 Go 并发。前置：P2。后续：P14 把本章原语组装成流水线。
>
> 动手方式：开一个空目录，`go mod init demo`，每个例子存成 `main.go`，用 `go run main.go` 运行。片段省略了 `package main` 和 `import`，用到的包名一眼就能认出来。

## 体系背景：线程为什么不够便宜

操作系统（OS）的线程是内核调度的。切换线程要陷入内核，保存一套寄存器，再换上另一套，这叫上下文切换。

每个线程还要一块栈。Linux 默认的栈上限由 `ulimit -s` 决定，本机的输出是：

```sh
$ ulimit -s
8176
```

单位是 KB。这是地址空间的预留，不是实际占用，但 10 万个线程按这个值算要预留约 780 GiB 地址空间。线程做不到"一个请求一个执行体"。

Java 21 的虚拟线程和 Kotlin 的协程解决的是同一个问题。JS 干脆只有一个线程，用事件循环加 `async/await` 交错执行，所以没有这里说的代价，也没有真正的并行。

Go 的办法是把执行体做成用户态的 goroutine，由运行时复用到少量线程上。调度器有三个角色：G 是 goroutine，M 是 OS 线程，P 是持有运行队列的调度上下文。

## goroutine

假设你想让一个函数在后台跑。在调用前面加 `go` 就行：

```go
func main() {
	go fmt.Println("来自 goroutine")
	fmt.Println("main 结束")
}
```

```sh
$ go run main.go
main 结束
```

goroutine 的输出没有出现。`main` 返回时进程直接退出，不会等任何 goroutine。

所以需要一个"等"的办法。`sync.WaitGroup` 是计数器，`wg.Go`（Go 1.25 起）启动一个 goroutine 并自动计数：

```go
var wg sync.WaitGroup
for i := range 3 {
	wg.Go(func() {
		fmt.Println("worker", i)
	})
}
wg.Wait()
fmt.Println("全部完成")
```

```sh
$ go run main.go
worker 2
worker 1
worker 0
全部完成
```

三个 worker 的顺序每次可能不同。调度器不保证先起的先跑。

对 Java 读者，`wg.Wait()` 像 `CountDownLatch`。对 Kotlin 读者，`go` 像 `launch`，但不需要 `CoroutineScope`，也没有结构化并发，goroutine 泄漏要自己防。对 JS 读者，`Promise.all` 是最接近的东西。

和 tokio 比，`go` 同样是急切的：语句执行完 goroutine 就已经可以被调度了。区别是 Go 没有 `.await`，让出点由编译器和运行时在函数调用处插入，源码里看不到。

## goroutine 有多便宜

回到体系背景里的问题。起 10 万个 goroutine 并让它们都停在原地，看栈占了多少：

```sh
$ go run main.go
goroutine 数: 100001
栈占用 KB: 200256
平均每个 goroutine 栈字节: 2050
```

多出来的 1 个是 `main` 自己。每个 goroutine 的栈约 2 KB，这是运行时初始栈大小（`runtime/proc.go:5322` 的 `malg(stackMin)`），不够会自动增长。

这 10 万个 goroutine 用了多少线程？

```sh
$ go run main.go
goroutine: 100001
GOMAXPROCS: 10
OS 线程累计创建: 11
```

本机 10 核，`GOMAXPROCS` 是 10，进程一共只创建了 11 个线程。这就是复用。

## channel

多个 goroutine 之间要传数据。直接共享变量会出问题（后面讲），Go 提供的第一选择是 channel。

```go
ch := make(chan int)
go func() {
	ch <- 42
}()
v := <-ch
fmt.Println("收到", v)
```

```sh
$ go run main.go
收到 42
```

无缓冲 channel 的发送会阻塞，直到有人接收。两边在这一刻"碰头"，所以收发本身就是同步点。

如果只发不收，会发生什么？在上面的程序末尾再加两行：

```go
dead := make(chan int)
dead <- 1
```

```sh
fatal error: all goroutines are asleep - deadlock!

goroutine 1 [chan send]:
main.main()
	.../main.go:14 +0xf0
exit status 2
```

运行时发现所有 goroutine 都在等，没人能唤醒它们，直接报死锁。`make(chan int, 10)` 创建有缓冲 channel，缓冲满了才阻塞。

## close 与 range

发送方写完以后，怎么告诉接收方"没有了"？答案是 `close`：

```go
ch := make(chan int, 3)
go func() {
	for i := 1; i <= 3; i++ {
		ch <- i * 10
	}
	close(ch)
}()
for v := range ch {
	fmt.Println("range 收到", v)
}
v, ok := <-ch
fmt.Println("关闭后接收:", v, ok)
```

```sh
$ go run main.go
range 收到 10
range 收到 20
range 收到 30
关闭后接收: 0 false
```

`range` 会一直收到 channel 关闭。关闭后再接收得到零值，`ok` 是 `false`。

无缓冲 channel 在 Java 里最像 `SynchronousQueue`，有缓冲的像 `ArrayBlockingQueue`，Kotlin 有同名的 `Channel`。JS 没有对应物。

只有发送方应该 `close`，对已关闭的 channel 再发送会 panic。这和 Rust 里 drop 掉 `Sender` 是同一个意思，不同的是 Go 要你手动写。练习 `TestPool` 的 worker 就靠 `close(jobs)` 知道该退出。

## select

等两个 channel，谁先到处理谁，用 `select`。给慢操作加超时是最常见的用法：

```go
select {
case r := <-slow():     // slow 要 200ms 才出结果
	fmt.Println("拿到", r)
case <-time.After(50 * time.Millisecond):
	fmt.Println("超时")
}
```

完整程序里还有第二个 `select`，超时放宽到 1 秒：

```sh
$ go run main.go
超时
拿到 结果
```

Kotlin 的 `select {}` 和 JS 的 `Promise.race` 做类似的事，Java 标准库里没有。

`select` 有三个要记的规则：

1. 多个分支同时就绪时，随机挑一个，防止某个分支饿死。
2. 分支只能是 channel 收发，没有模式匹配。
3. nil channel 永远阻塞，所以把变量置 nil 就能禁用那个分支。

第三条是惯用法。下面的程序把两个 channel 合并读完，每关一个就置 nil：

```sh
$ go run main.go
b 关闭，禁用该分支
a 关闭，禁用该分支
sum = 3
```

两行提示的先后每次可能不同，`sum` 恒为 3。

`time.After` 在 Go 1.23 前，循环里每次调用都会留下一个到期前不被回收的 timer。1.23 起 GC 能回收没有引用的 timer，官方注释明确说没有理由在 `After` 够用时偏向 `NewTimer`（`time/sleep.go`）。练习 `TestFirstOrTimeout` 的超时分支就用到这一点。

## context

`select` 能超时，但取消一整棵调用链就不够了。Rust 靠 drop future 取消，Go 的 goroutine 没有 drop，取消必须显式传递，载体是 `context.Context`：

```go
ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
defer cancel()
err := work(ctx, "任务A")     // work 里 select 等 ctx.Done()
fmt.Println(errors.Is(err, context.DeadlineExceeded))
```

```sh
$ go run main.go
任务A 被取消: context deadline exceeded
true
任务B 被取消: context canceled
true
```

任务 A 超时取消，任务 B 由另一个 goroutine 调 `cancel()` 取消。`ctx.Err()` 返回 `Canceled` 或 `DeadlineExceeded`，用 `errors.Is` 判断。

用法有三条纪律：

- ctx 永远是函数的第一个参数，名字就叫 `ctx`。
- 谁创建谁 `cancel`，通常是 `defer cancel()`。
- ctx 只传取消信号、截止时间和请求级数据，不传业务参数。

Kotlin 的 `Job.cancel()` 靠作用域传播，JS 用 `AbortController` 加 `AbortSignal`，Java 靠 `Thread.interrupt()` 加手动检查。Go 的 ctx 最像 `AbortSignal`：显式传、显式检查。

Rust 的取消在类型系统里，drop 自动触发。Go 的取消在参数列表里，你忘了传或忘了检查，取消就静默失效。所以 ctx 靠纪律，不靠编译器。

## 数据竞争

假设 1000 个 goroutine 同时给一个变量加一：

```go
c := 0
var wg sync.WaitGroup
for range 1000 {
	wg.Go(func() { c++ })
}
wg.Wait()
fmt.Println("c =", c)
```

```sh
$ go run main.go
c = 946
```

结果不是 1000，每次跑还不一样。JS 单线程，没有这类问题。Java 和 Kotlin 读者对 `synchronized`、`volatile` 和 `AtomicLong` 很熟，Go 的 `Mutex` 和 `atomic` 一一对应。

编译器一声不吭，因为 Go 没有 borrow checker，也没有 `Send`/`Sync`。

防线是 race detector，加 `-race` 运行：

```sh
$ go run -race main.go
==================
WARNING: DATA RACE
Read at 0x00c00019e048 by goroutine 10:
  main.main.func1()
      .../main.go:12 +0x2c

Previous write at 0x00c00019e048 by goroutine 7:
  main.main.func1()
      .../main.go:12 +0x3c
...
c = 935
Found 2 data race(s)
exit status 66
```

报告指出读写冲突的两行代码和各自的创建位置，退出码是 66。CI 里的测试必须开 `-race`。

## 锁与原子操作

确认有共享状态，就按场景选工具。单个计数用 `atomic`，多步的临界区用 `Mutex`：

```go
var n atomic.Int64
var mu sync.Mutex
m := map[string]int{}
var wg sync.WaitGroup
for range 1000 {
	wg.Go(func() {
		n.Add(1)
		mu.Lock()
		m["hits"]++
		mu.Unlock()
	})
}
wg.Wait()
fmt.Println("atomic:", n.Load(), "mutex:", m["hits"])
```

```sh
$ go run -race main.go
atomic: 1000 mutex: 1000
```

加了 `-race` 也没有报告。`sync.Mutex` 零值可用，不用 `new`。

读多写少换 `sync.RWMutex`，一次性初始化用 `sync.Once`，原子指针用 `atomic.Pointer[T]`。能用 channel 传所有权就传，这是 Go 版的 move，没有编译器盯着，纯靠纪律。

`wg.Go` 的可运行对照在 `exercises/p3conc/wggo.go`（TestWaitGroupGo）。它把 `go f`、`Add(1)` 和 `defer Done()` 合成一个调用，消灭了"`Add` 忘在 goroutine 里"的竞态样板。

## panic

最后一个和 tokio 不同的地方。在 goroutine 里 panic：

```go
go func() {
	panic("goroutine 里出事了")
}()
time.Sleep(100 * time.Millisecond)
fmt.Println("main 还活着吗？")
```

```sh
$ go build -o app main.go && ./app
panic: goroutine 里出事了

goroutine 7 [running]:
main.main.func1()
	.../main.go:10 +0x2c
created by main.main in goroutine 1
	.../main.go:9 +0x24
$ echo $?
2
```

`main` 的最后一行永远没机会打印。任何 goroutine panic 都会杀掉整个进程，退出码 2。tokio 里 task panic 只杀该 task。

Java 的线程里抛出未捕获异常，只终止该线程。Node 里未处理的异常默认也会终止整个进程，和 Go 更接近。

所以每个 goroutine 的入口都要想清楚要不要 `recover`。批量任务的生命周期交给 errgroup（P14 展开）。

## 小结

1. goroutine 便宜（约 2 KB 栈），运行时把它们复用到少量线程上。
2. channel 内建，只有发送方 `close`，nil channel 永久阻塞，`select` 随机挑就绪分支。
3. ctx 是取消的统一协议：第一参数、谁创建谁 cancel、只传信号。
4. 没有编译期检查，`-race` 是唯一防线，CI 必开。
5. goroutine panic 杀全进程，入口处想清楚 recover。

## 练习：p3conc

练习在 `exercises/p3conc/`，先跑一遍看全绿：

```sh
$ go test -race ./exercises/p3conc/
ok  	golearn/exercises/p3conc	2.334s
```

六个测试各考一件事：

- `TestPool`：goroutine 池加 channel 收敛，考 close 的生命周期。
- `TestMerge`：fan-in，多路合一，close 总闸。
- `TestLimit`：限并发加 ctx 取消语义。
- `TestFirstOrTimeout`：select 三个分支，结果、超时和取消。
- `TestCounters`：atomic 对 mutex，用 `-race` 抓裸竞争。
- `TestPanicKillsProcess`：goroutine panic 杀进程，读，别直接跑。

**预测**，先写答案再跑：

1. `TestPool` 里 `close(jobs)` 之后 worker 怎么知道该退出？不 close 会卡在哪一行？
2. `TestMerge` 未实现时死锁，"all goroutines are asleep" 说的 goroutine 在等什么？
3. `TestPanicKillsProcess` 删掉 `t.Skip` 并加上 `go func() { panic("dead") }(); time.Sleep(time.Second)`，`go test` 的退出码是多少？
4. `TestLimit` 里 ctx 已经取消，为什么 `Limit` 必须在起任务前检查 ctx？

第 3 题在临时副本上实测过，输出以 `panic: dead` 开头，最后是 `FAIL golearn/exercises/p3conc 0.269s`，`go test` 退出码 1。被测程序自己的退出码是 2。

**观察**：

```sh
go test -race -run TestCounters -v ./exercises/p3conc/    # race detector 绿灯
```

**改造**：

- 用 `errgroup.SetLimit` 加首错返回重写 `Limit`，对照 tokio 的 `try_join`。
- 把 `TestCounters` 的计数器再写一个 channel 版本，不要有 race。
- 写 10 万 goroutine ping-pong benchmark，把结果贴到下面的实测小节。

## 章末参考

### 实测：goroutine 切换成本 vs tokio task

[INFERENCE，待你实测] 同机对照实验设计：10 万 goroutine ping-pong 对 10 万 tokio task ping-pong，测每跳延迟和内存。预期 Go 切换略慢（栈拷贝对状态机 resume），初始内存 2 KB 对 tokio 更小。留给你做完练习后自己跑，方法沿用 `_rust_learning` L1。

### Rust/tokio 对照

| Rust/tokio | Go | 关键差异 |
|---|---|---|
| `tokio::spawn(async {...})` | `go func() {...}()` | 语法内建，没有 Future/async，函数本身可暂停 |
| task（堆分配，无栈状态机） | goroutine（2 KB 起可增长栈） | 都不是 OS 线程。tokio task 是无栈，不是小栈 |
| 多线程 work-stealing runtime | GMP 调度器 | 两边都有本地队列和偷任务，P 还管 mcache 和 netpoller |
| `.await` 显式让出 | 调用点隐式让出 | 代价是源码里看不到切换点 |
| `tokio::select!` | `select` 关键字 | Go 随机选就绪分支，没有 `biased;` |
| `mpsc` + `oneshot` + `broadcast` | `chan T` | 一个内建类型合体，可 close、可 range |
| `CancellationToken` / drop future | `context.Context` | Go 靠显式参数传递取消 |
| `Send`/`Sync` 编译期检查 | `go test -race` | 动态检测，不拦编译 |
| `Mutex` / `RwLock` / `OnceLock` | `sync.Mutex` / `RWMutex` / `Once` | 零值可用，不用 new |
| `AtomicU64` / `ArcSwap` | `atomic.Int64` / `atomic.Pointer[T]` | 1.19 起类型化封装 |
| `JoinHandle` / `JoinSet` | `WaitGroup` / errgroup | 没有单个 goroutine 的 join 句柄 |
| `tokio::spawn_blocking` | 不需要 | 阻塞调用不会饿死调度器 |
| task panic 只杀该 task | goroutine panic 杀全进程 | 每个入口自己 recover |

时间相关的并发测试（Sleep 和计时器怎么测）见 P21，`testing/synctest` 提供假时钟。

### Java/Kotlin 与 TS/JS 类比

| 概念 | Java / Kotlin | TS / JS |
|---|---|---|
| `go f()` | 虚拟线程 `Thread.ofVirtual().start`，Kotlin `launch` | 无，`async` 函数只是交错执行 |
| `WaitGroup` | `CountDownLatch`，Kotlin `coroutineScope` | `Promise.all` |
| `chan T` | `BlockingQueue`，Kotlin `Channel` | 无，近似异步迭代器 |
| `select` | Kotlin `select {}` | `Promise.race` |
| `context.Context` | Kotlin `Job` 取消，Java `Thread.interrupt` | `AbortSignal` |
| `Mutex` / `atomic` | `synchronized` / `AtomicLong` | 无，单线程 |
| goroutine panic | 只杀该线程 | Node 未处理异常默认杀进程 |
