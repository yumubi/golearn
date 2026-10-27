---
title: P31 · 并发调度：GMP 与 channel/select
---

# P31 · 并发调度：GMP 与 channel/select


> 这章学完你能干什么：讲清一个 goroutine 怎么被摆上 CPU（GMP 演进与 sysmon 抢占链路），选对 channel 惯用法（信号、广播、替代锁、计数信号量），用 select 三式写出不卡死的多路监听，并用本机基准数据在 Mutex、RWMutex、atomic、chan 之间做选型。前置：P3（并发基础）。P3 已讲语法与最小实测，这章讲运行时行为与证据。

## 体系背景：并发是结构问题，并行是执行问题

并发（concurrency）是把程序分成多个可独立执行部分的结构化设计方法；并行（parallelism）是多核同一时刻执行多个任务。Rob Pike 那句"并发不是并行"说的就是这个区分：并发设计是结构，并行执行是结果，单核上并发设计的程序不会并行。〔TB31 p4-5〕

传统线程模型的两个缺口让"结构"做不大：一是复杂，创建容易退出难，共享内存加锁易死锁；二是难规模化，线程栈与切换代价摆在那里，网络服务只能少量线程加 I/O 多路复用，回调把逻辑撕碎。〔TB31 p5-6〕

Go 的答案是 goroutine 加 channel：初始栈 2 KB、用户态调度、`go` 关键字创建、语言内建通信原语。〔TB31 p6〕 goroutine 有多便宜 P3 已实测（10 万个只占 200 MB 栈、11 条线程），这章不重复。

模型选择上 CSP（Communicating Sequential Processes）是主流推荐：并发程序是一组用输入输出原语连接起来的 Process 集合，Go 里 Process 对应 goroutine，channel 是连接件。〔TB31 p9-10〕 局部性能敏感区退回 sync 低级原语，这也是"sync 家族与基准"一节的主线。spawn 模式先用起来（goroutine 的返回值会被忽略，退出状态只能靠 channel 传回）：`func spawn(f func() error) <-chan error { c := make(chan error, 1); go func() { c <- f() }(); return c }`。

## GMP 演进：从全局锁到信号抢占

G-M 模型（Go 1.0）只有两个角色：G 是 goroutine，M 是 OS 线程。全局只有一个运行队列，配一把全局锁 Sched.Lock。Dmitry Vyukov 在《Scalable Go Scheduler Design》里数了四大问题：所有 G 操作都要抢全局锁；G 在 M 间传递放大调度延迟；每个 M 都带内存缓存，占用高且局部性差；syscall 频繁阻塞解除阻塞线程。〔TB32 p3〕

Go 1.1 加了间接层 P：M 持有 P 才能执行普通 Go 代码；可运行的 G 可以位于 P 的本地队列或全局队列。调度器从队列取任务，也会从其他 P 窃取工作。〔TB32 p4〕

抢占是这条演进线的关键剧情。协作式时代（Go 1.2 起）靠编译器在函数入口埋点，纯算法循环没有函数调用就抢不动，GOMAXPROCS=1 时整个程序饿死。Go 1.14 引入基于信号的异步抢占（Austin Clements 提案），sysmon 直接向线程发信号。〔TB32 p5-6〕

实测这组行为（dl_demo.go，完整程序随 /tmp/p31probe，写文件避开管道 SIGPIPE）：

```go
// dl_demo.go 节选：GOMAXPROCS=1，纯循环 goroutine 对打 main
go func() {
	for {
		for i := 0; i < 1e9; i++ { // 纯循环，无函数调用无让出点
		}
	}
}()
for {
	time.Sleep(100 * time.Millisecond)
	fmt.Fprintln(f, "main alive") // 写文件，避开管道 SIGPIPE
}
```

〔实测〕 `timeout 3 go run dl_demo.go; wc -l < dl_out.txt` 三轮都是 25 行；`timeout 3 env GODEBUG=asyncpreemptoff=1 go run dl_demo.go` 三轮都是 0 行（本机 go1.26.4，Apple M4）。关掉异步抢占，协作式时代"纯循环饿死一切"的行为原样复现。tokio 对照：task 在 await 点让出，纯循环同样饿死 worker，要 `yield_now()` 补救；Go 的信号抢占连纯循环都能夺回，这是两者调度器最深的一条差异。

## G/P/M 结构与 sysmon 抢占链路

三个结构体在 `runtime2.go`，先看字段再看职责。〔TB32 p6-8〕

G（goroutine）存执行所需的一切：栈（stack/stackguard0）、状态（atomicstatus）、任务现场（sched gobuf）、身份（goid/gopc/startpc）。M 不保留 G 的任何状态，所以 G 能跨 M 迁移。

```go
// $GOROOT/src/runtime/runtime2.go（go1.26.4）
type g struct {
	// stackguard0 平时是栈边界；被置成 stackPreempt 后，
	// 常规的栈溢出检查就变成抢占触发器（见下文 preemptone）。
	stack        stack // offset known to runtime/cgo
	stackguard0  uintptr // offset known to liblink
	...
	atomicstatus atomic.Uint32
	goid         uint64
	...
	preempt      bool // preemption signal, duplicates stackguard0 = stackpreempt
}
// p 的核心资产（GOMAXPROCS 个，决定最大并行 G 数）：
	// Queue of runnable goroutines. Accessed without lock.
	runqhead uint32
	runqtail uint32
	runq     [256]guintptr
	// runnext 非 nil 时是"刚 ready 的 G 下一个就跑"的优化槽
	runnext guintptr
```

runq 256 槽无锁访问。P 还挂着 mcache（内存分配缓存，P32 讲）。M 是真实执行资源，有 g0 调度栈和 curg 指针，绑定 P 后进入"取 G、切栈执行、goexit 清理"的循环。

### 抢占链路：10ms 一刀

sysmon 是启动时 `newm(sysmon, nil, -1)` 创建的监控线程，不绑 P、以 g0 形式运行，20us 到 10ms 巡检一次。〔TB32 p8-9〕 与抢占相关的是 retake：

```go
// $GOROOT/src/runtime/proc.go（go1.26.4）
const forcePreemptNS = 10 * 1000 * 1000 // 10ms
		schedt := int64(pp.schedtick)
		if int64(pd.schedtick) != schedt {
			pd.schedtick = uint32(schedt)
			pd.schedwhen = now
		} else if pd.schedwhen+forcePreemptNS <= now {
			preemptone(pp)
```

同一个 P 的 schedtick 超过 10ms 没变（没有新 G 上过调度），preemptone 出手：

```go
// $GOROOT/src/runtime/proc.go（go1.26.4）
	gp.preempt = true
	// Setting gp->stackguard0 to StackPreempt folds
	// preemption into the normal stack overflow check.
	gp.stackguard0 = stackPreempt
	// Request an async preemption of this P.
	if preemptMSupported && debug.asyncpreemptoff == 0 {
		pp.preempt = true
		preemptM(mp)
	}
```

在所示的 Unix 实现中，preemptM 向目标线程发送 `sigPreempt = _SIGURG`，信号处理器尝试在异步安全点注入 asyncPreempt。10ms 是 sysmon 发起抢占的目标阈值，不是每个 goroutine 必定在 10ms 内被抢占的实时保证；平台和安全点也影响实际时机。

### 两种阻塞，两种待遇

channel 阻塞和网络 I/O：G 进等待队列，M 立刻找 P 的下一个 G，P 无 G 可跑则 M 解绑挂起；I/O 完成后 G 标记 runnable 进某 P 的队列。阻塞的只是 G。〔TB32 p10〕
系统调用：G 和 M 一起挂起，M 先解绑 P；空闲 M 接手 P，没有就创建新 M。syscall 返回后 G 尝试拿回 P，拿不到就标记 runnable 排队，原 M 继续闲置。〔TB32 p11〕 这就是慢系统调用涨线程的根源。

〔实测〕 8 个 goroutine 各做 5000 次 `os.Stat` 系统调用（GOMAXPROCS=4），`GODEBUG=schedtrace=250 go run syscallsched.go`：

```text
SCHED 0ms: gomaxprocs=4 idleprocs=1 threads=5 spinningthreads=1 needspinning=0 idlethreads=0 runqueue=0 [ 0 0 0 0 ] schedticks=[ 1 0 0 2 ]
SCHED 254ms: gomaxprocs=4 idleprocs=4 threads=9 spinningthreads=0 needspinning=0 idlethreads=3 runqueue=0 [ 0 0 0 0 ] schedticks=[ 59 65 77 53 ]
done
```

threads 从 5 涨到 9：syscall 阻塞了 M，运行时新建线程接手 P。schedtrace 字段解读见 notes/p22 §GODEBUG（P22 有 10 P 的实拍行，这里补 threads 增长的现场）。

## channel 惯用法群

channel 语法 P3 已讲（无缓冲同步点、close 与 range、发送方负责关闭）。这章讲四个惯用法，都是"发送端负责关闭"这条铁律的应用。〔TB33 p6-7〕

### 信号传递：1 对 1

空结构体信号，spawn 完成后发一声，main 阻塞读：`func spawn(f func() error) <-chan signal { c := make(chan signal); go func() { _ = f(); c <- signal(struct{}{}) }(); return c }`。〔TB33 p8-9〕

〔实测〕 `go test ./exercises/p31sched/ -run TestSignalOneToOne -count=1` PASS（练习包落库后复跑）。Rust 对照：tokio 的 `JoinHandle.await` 返回 Result，同职责；Go 没有单 goroutine 句柄，信号就是最轻的 join。

### 广播：1 对 n

close 无缓冲 channel 是广播：所有阻塞接收同时返回。起跑枪声模式：

```go
groupSignal := make(chan signal)
for range 5 {
	go func() {
		<-groupSignal // 全部阻塞在起跑线上
		// 干活
	}()
}
close(groupSignal) // 枪响，5 个同时起跑
```

〔实测〕 `go test ./exercises/p31sched/ -run TestBroadcastClose -count=1` PASS。close 能一次唤醒所有等待接收者；也可以向同一个 channel 分别发送 n 次，但那不是一次性广播。

### 替代锁：后台 goroutine 独占状态

传统计数器是 Mutex 包住 i。channel 方案让一个后台 goroutine 独占自增逻辑：

```go
type cter struct {
	c chan int
	i int
}

// NewCounter 的后台 goroutine 持续自增，再通过 c.c 发送新值。
// Increase 调用方只接收；状态只由后台 goroutine 修改。
func (c *cter) Increase() int { return <-c.c }
```

〔实测〕 `go test ./exercises/p31sched/ -run TestCounterByChannel -count=1` PASS。"通过通信共享内存"落地成：状态只有持有它的 goroutine 能碰，别的方法都是收发消息。〔TB33 p11-13〕 Rust 读者注意：这里没有所有权编译器兜底，c.i 谁都能摸，模式成立全靠"只有这个 goroutine 写它"的纪律。

### 计数信号量：容量即并发上限

带缓冲 channel 的两个数字天然是一对信号量：当前元素个数 = 活动 goroutine 数，容量 = 最大并发数。发送 = 获取，接收 = 释放：

```go
active := make(chan struct{}, 3)
for j := range 9 {
	go func() {
		active <- struct{}{} // 获取
		defer func() { <-active }() // 释放；至多 3 个在跑
	}()
}
```

〔实测〕 `go test ./exercises/p31sched/ -run TestCountingSemaphore -count=1 -v` 输出 `批次分布: map[0:3 1:3 2:3]`（9 个 50ms 的 job 分 3 批跑完，每批恰好 3 个）。〔TB33 p16-17〕 tokio 的 `Semaphore::acquire` 同构。

### channel 当消息队列快不快

TB33 的基准（Go 1.17/i5）给过数字，量级结论"带缓冲占优"仍成立，数字不可照抄。本机重测（M4/go1.26.4，每轮各起一个 goroutine 收发）：

〔实测〕 `go test -bench "Msg(Unbuf|Buf100)$" -benchtime=3s .`：

```text
BenchmarkMsgUnbuf-10          	14766714	       270.5 ns/op
BenchmarkMsgBuf100-10         	13762598	       246.2 ns/op
```

10 发 10 收的对照：`BenchmarkMsg10x10Unbuf-10 889251 4464 ns/op`、`BenchmarkMsg10x10Buf100-10 1000000 3266 ns/op`。1 对 1 时缓冲几乎不影响（瓶颈是 goroutine 唤醒），参与者变多后缓冲拉开差距。要优先级、权重、持久化，换专业 MQ，channel 没有这些。〔TB33 p13-16〕

## select 三式与 nil channel

select 语法 P3 已讲（三规则：随机、只收发、nil 永久阻塞）。这章讲三个惯用法和一个经典 bug 的修法。

### default 防阻塞

select 加 default 就是 trySend/tryRecv，判定与收发合并成一个"事务"，空/满走 default 不阻塞：`select { case c <- i: return true; default: return false }`。标准库同款：`time/sleep.go` 的 sendTime 就是无阻塞发送。〔TB33 p26〕 注意代价：这个操作改变了 channel 状态（收走或写入一个元素），无副作用的无阻塞侦测没有通用方法。

`len(ch)` 只是一瞬间的快照，不会为接下来的收发预留位置。即便只有单个发送者或接收者，也要考虑关闭、其他操作以及观察与执行之间的变化；需要非阻塞收发时优先使用 select/default。

### 超时与心跳

超时用 `case <-time.After(30 * time.Second)`，心跳用 `time.NewTicker` 的 `.C` 字段。纪律：长驻服务里 timer/ticker 要 Stop 回收。time.After 在 Go 1.23 起未被引用的 timer 可被 GC 回收，P3 §select 已讲版本史。dachang 课件把超时控制归纳成四档：双 channel 截止、time.After、WithCancel、WithTimeout；其中 WithTimeout 加 defer cancel 是标准答案。〔TB33 p27-28〕
展开见 notes/p3 §select 与 MATERIALS 索引 C:dachang/concurrence#K5。

### nil channel：摘掉 select 的坏分支

对 nil channel 收发皆阻塞，这个"缺陷"是 select 的控制流工具。经典 bug：双源 select，ch1 先发完并 close。ch1 close 之后"从已关闭 channel 接收永不阻塞"，select 从此反复选中它，狂刷零值：

```go
select {
case v, ok := <-ch1:
	if !ok { ch1 = nil } else { out = append(out, v) }
case v, ok := <-ch2:
	if !ok { ch2 = nil } else { out = append(out, v) }
}
```

上面是 select 片段：外层应在 `ch1 == nil && ch2 == nil` 时结束，否则两个分支都禁用后会永久阻塞。〔实测〕 `go test ./exercises/p31sched/ -run TestNilChannelSelect -count=1` PASS；退化版反复读取关闭的通道，可能产生零值风暴。〔TB33 p22-25〕

## sync 家族与基准：谁快谁慢

Rob Pike 的口号是"通过通信共享内存"，但 Go 从未放弃共享内存模型：sync 包提供 Mutex/RWMutex/Cond，atomic 包提供原子操作。场景就两个：高性能临界区、不转移所有权的状态同步。〔TB34 p1-4〕

### 禁止复制

sync 包注释写明：包含 sync 类型值的结构不得复制；Mutex/RWMutex/Cond 的文档都对 first use 之后的复制行为给了同一条警告。Mutex 只有两个字段（state 锁状态 + sema 信号量），按值传参等于复制这两块整型，原变量与副本从此各自为政。

```go
// 按值传递：g1 持有副本的锁，锁不住 g0 用的原 mu
func bad(mu sync.Mutex) {
	mu.Lock() // 锁的是副本
	mu.Unlock()
}
```

修复是传 `*sync.Mutex` 或闭包捕获。go vet 的 copylocks 检查会拦这类代码。〔TB34 p4-7〕 Rust 对照：Mutex 不可 Copy 且跨线程要 Arc，编译器直接杜绝；Go 值语义让这个坑独有，vet 是补偿。

### 本机基准全套

TB34 的数字是 Go 1.17 darwin/amd64，全量重测（exercises/p31sched，M4/go1.26.4）。临界区（`i++` 一行）两种实现的对照，`go test -bench "Critical" -benchtime=3s .`：

```text
BenchmarkMutexCritical-10    	1000000000	         2.281 ns/op	       0 B/op	       0 allocs/op
BenchmarkChanCritical-10     	189625609	        21.01 ns/op	       0 B/op	       0 allocs/op
```

Mutex 临界区比 chan 信号量快约 9 倍，"Mutex 快 3 倍以上"的量级结论在本机成立且更悬殊。读写锁与原子组：

〔实测〕 `go test -bench "RWMutexRead$|RWMutexWrite$|AtomicAdd|AtomicLoad" -benchtime=3s .`，并行形态（`b.RunParallel`）一组 `go test -bench "Parallel" -benchtime=2s .`：

```text
BenchmarkRWMutexRead-10     	856018142	         4.147 ns/op
BenchmarkRWMutexWrite-10    	669963080	         5.832 ns/op
BenchmarkAtomicAdd-10       	1000000000	         1.920 ns/op
BenchmarkAtomicLoad-10      	1000000000	         0.4024 ns/op
BenchmarkMutexParallel-10           	34791222	        67.62 ns/op
BenchmarkRWMutexReadParallel-10     	149970933	        27.51 ns/op
BenchmarkRWMutexWriteParallel-10    	51479498	        52.16 ns/op
```

这些数字只反映各自基准的工作量和机器：单线程无争用的 Mutex 在此比读锁便宜，并行读基准中 RWMutex 优于 Mutex。不能据此断言 RWMutex 写锁在所有高并发负载下最差；实际选型应测量读写比例和临界区。

原子读 0.40 ns 是读锁 4.15 ns 的 10 分之 1。"原子读约为读锁 200 倍"在 1.17 时代成立，本机倍数小得多，方向一致。〔TB34 p16-18〕 atomic 的本质是硬件级不可中断指令，x86_64 的 XCHGQ 就是 SwapInt64 的底。它比 OS/运行时同步原语更原始，是 Mutex 与 channel 的实现基础。〔TB34 p14-16〕
手写 Once 双检查锁的本机复测是 1.150 对 std 0.2800 ns/op。std 热路径用 atomic.Load 免锁，快 3 倍。bench 对照法见 MATERIALS 索引 C:dachang/concurrence#K1。

### Cond 与选型口诀

sync.Cond 是等待条件成立的 goroutine 容器。调用 Wait 前必须 Lock：Wait 内部先释放锁再挂起，唤醒后重新加锁；唤醒后要重判条件（for !ready 循环），Broadcast 唤醒全部。适用面比 Mutex 窄得多，channel 加 sync 已覆盖 99.9% 场景。〔TB34 p10-14〕 channel 等价物：`ch<-struct{}{}` 对应 Signal，`close(ch)` 对应 Broadcast，但 close 只能一次，反复广播必须 Cond（P3 §锁与原子操作讲过选型，这里补原理）。
选型口诀照录：读多写少用 RWMutex、等待条件成立用 Cond、不确定就用 Mutex、极致性能单变量用 atomic。〔TB34 p18-19〕

## workerpool 与功能选项

goroutine 不是免费的：初始栈随运行时和平台变化，并可按需增长或收缩。大量任务可交给有限数量的 worker 重用，以限制并发并施加背压。下面的最小版用 channel+select 展示协作。〔TB35 p2-4〕

### 三部分结构

Pool 三字段加一组生命周期：capacity 上限、active 带缓冲 channel（计数信号量，容量 = capacity）、tasks 无缓冲 channel（任务传递）、wg、quit。

- run 管理循环：for+select 监视 quit 与 active，active 可写（有空位）就 newWorker；
- newWorker：wg.Add(1)，worker 里 for+select 监视 quit 与 tasks，defer 里 recover 捕获 task panic 并释放 active 计数；
- Schedule：select 检视 quit（返回哨兵错误 ErrWorkerPoolFreed），否则投递 tasks。tasks 无缓冲，worker 全忙时调用方阻塞，这是背压。〔TB35 p5-9〕

完整实现 90 行，在 exercises/p31sched/pool.go。最关键的 run 一行讲完：`for { select { case <-p.quit: return; case p.active <- struct{}{}: p.wg.Add(1); go p.newWorker() } }`，active 有空位就补一个 worker，quit 关闭就退。

〔实测〕 `go test ./exercises/p31sched/ -run TestWorkerPool -count=1` PASS：capacity=5 提交 10 个 sleep 任务，全部执行，Free 后 wg 收尾归零。〔TB35 p9-11〕

### 功能选项

New 的参数要加选项又不破坏旧调用，用 Option 模式：`type Option func(*Pool)`，选项是改 Pool 字段的闭包，New 签名变 `New(capacity int, opts ...Option)`。WithBlock(false) 让池满时 Schedule 返回 ErrNoIdleWorkerInPool 而不是阻塞。Rob Pike 2014 年定型的社区最流行方案。〔TB35 p11-12〕

〔实测〕 `go test ./exercises/p31sched/ -run TestFunctionalOptions -count=1` PASS：容量 1 占住 worker 后非阻塞 Schedule 返回 ErrNoIdleWorkerInPool。Java 的 ThreadPoolExecutor+RejectedExecutionHandler 是同构物；Rust 走 builder 模式，两大惯用法各自成派。

一个边界要说清：课程版 run 在 preAlloc=false 时不主动建 worker，先阻塞在 tasks 读取上，首个 task 才触发创建（作者留言确认）。教学实现，不能上生产；生产用 ants 这类库。〔TB35 p13-15〕 C:dachang/concurrence#K4 的双 goroutine 池对照（close 即停版与 CAS 状态机版）进 MATERIALS 索引。

## 实测五点：脏写、锁矩阵、map 崩溃、context

最后四个现场，来自课件包 go_basic 并发部分，全部本机复跑。〔C:go_basic/concurrence/atomic.go:11-19〕〔C:go_basic/concurrence/rwlock.go:33-67〕

### n++ 为什么不安全

n++ 是取、加、写三步，中断可发生在任意两步之间：

```go
// atomic.go Inc 函数（节选）〔C:go_basic/concurrence/atomic.go:11-19〕
func Inc() {
	n++ // 取 n，加 1，存回 n，三步非原子
}
// inc2 用 atomic.AddInt32(&n, 1) 修复
```

〔实测〕 1000 个 goroutine 各 `n++` 跑 5 轮，`go test ./exercises/p31sched/ -run TestLostUpdateDistribution -count=1 -v`：

```text
n++ 结果: 940
n++ 结果: 933
n++ 结果: 899
n++ 结果: 878
n++ 结果: 943
```

五轮全丢更新，且没有规律。P3 §数据竞争实测的 946 是单次抽样，这里补分布面：脏写不保证每次发生，偶发一次 1000 全对反而更危险，因为它是间歇性故障。同构实验换 atomic.Int64 五轮恒 1000（TestAtomicCounter）。〔C:go_basic/concurrence/atomic.go:11-45〕

### RWMutex 行为矩阵

源码锚点 rwlock.go 33-67 行〔C:go_basic/concurrence/rwlock.go:33-67〕：没有等待写锁时连续 RLock 可能成功；但 RWMutex 不支持递归读锁，因为一旦写锁等待，后续 RLock 会阻塞，持有读锁者再次 RLock 就可能死锁。写锁同样不可重入。

〔实测〕 `go test ./exercises/p31sched/ -run TestRWMutexMatrix -race -count=1` PASS：先 RLock 两次再 RUnlock 两次正常；RLock 持有期间起 goroutine Lock，20ms 后再起 goroutine RLock，新读锁拿不到（select default 探测确认阻塞），原 RUnlock 后两条才依次通过。〔C:go_basic/concurrence/rwlock.go:33-67〕

### map 并发写是 fatal 不是 panic

数组、slice、struct 的无同步并发访问也可能发生数据竞争，行为不可靠；map 并发写还可能被运行时检测并报告 fatal：

```text
fatal error: concurrent map writes

goroutine 18 [running]:
internal/runtime/maps.fatal({0x10095c6b2?, 0x0?})
	/Users/akka/Toolchain/go/current/src/runtime/panic.go:1181 +0x20
```

〔实测〕 两个 goroutine 各写 1 万个 key，`go run main.go` 退出码 1，先打印 fatal error: concurrent map writes。套一层 `defer recover()` 再跑，同样 fatal。recover 只捕 panic；fatal 是运行时直接 abort。修复用 sync.Map 或 RWMutex 包 map。〔C:go_basic/concurrence/collection_safety.go:6-11〕
Rust 对照：Rc/RefCell 违规是编译错，Go 把这类错误留到运行时且给的是最重的一档。

### 子 ctx 的寿命不超父

context 纪律 P3 §context 已讲三原则。这里补一个继承实测：

〔实测〕 父 WithTimeout 1000ms，睡 500ms 后子也 WithTimeout 1000ms，`go test ./exercises/p31sched/ -run TestChildCtxInheritsDeadline -count=1 -v`：

```text
child 到期耗时 500.581625ms, err=context deadline exceeded
```

子设 1000ms 但父只剩 500ms，子的实际 deadline 是两者的较短者。〔C:go_basic/concurrence/context.go:53-74〕

WithValue 的自定义 key 纪律（StringKey 类型避免碰撞、只用于跨 API 边界携带数据）P3 §context 已讲，源码锚点 〔C:go_basic/concurrence/context.go:76-127〕；〔C:go_basic/concurrence/context.go:13-33〕 的 select 写法是上面实测的模板（done 收业务结果，ctx.Done() 收超时，Err() 区分 canceled 与 deadline exceeded）。

### goroutine 泄露的现场

超时 ctx 等 5ms 的 rpc、1ms 就返回，rpc goroutine 仍阻塞在无缓冲 channel 上，每请求泄一个；NumGoroutine 打印与 pprof goroutine profile 抓现行。这是"channel 惯用法群"一节信号惯用法用错方向的现场，完整链路见 MATERIALS 索引 C:dachang/concurrence#K7，P36 泄露专题会展开。

## 小结

带走这六条：

1. 并发是结构（把程序分成独立执行模块），并行是执行（多核同时跑）；goroutine 加 channel 是 CSP 的 Go 落地，性能敏感区退回 sync。
2. GMP 拆分全局争用；异步抢占（Go 1.14）允许在安全点抢占纯循环，但 10ms 不是实时保证。
3. channel 阻塞只挂 G；syscall 阻塞 M 并解绑 P，慢系统调用涨线程有 schedtrace 实证。
4. 四个 channel 惯用法：信号（1 对 1）、广播（close 起跑枪声）、替代锁（后台 goroutine 独占）、计数信号量（容量限流）。
5. select 三式（default 事务、超时、心跳）加 nil channel 摘坏分支；sync 家族禁止复制，选型照口诀，本机基准 Mutex 2.28 对 chan 21.0 ns/op。
6. map 并发写是 fatal 不可 recover；子 ctx 寿命取父子较短者。

## 在 dnsproxi 里看这一章

dnsproxi-go/main.go 是本章三个惯用法的生产形态（行号 go1.26.4 工作副本实读）：

- `Proxy.Run`（main.go:127）：per-packet 形态每查询 `wg.Add(1); go …` spawn 一个 goroutine（main.go:230），命中"goroutine 不是免费的"边界后，`WorkerPool>0` 分支换成固定 worker 吃 `qch` 缓冲 channel（main.go:188-193），`qch <- packet` 满时读循环阻塞，这就是"计数信号量：容量即并发上限"的背压版（main.go:216）。
- ctx 取消关监听是"信号传递 1 对 1"的极简款：一个只等 `<-ctx.Done()` 就 `pc.Close()` 的 goroutine（main.go:179-181），让阻塞在 `ReadFromUDP` 的读循环立刻醒来收尾。
- 老化循环 `for st := range p.cache.RunExpirer(done, ecfg)`（main.go:166）配 cache/expire.go:110-118 的双 select：`done` 收停机信号、`t.C` 收心跳，stats 发送套 `default` 防消费方拖慢，正是"select 三式"里 default 防阻塞与超时两式合用。

## Take-away

- 调度器的演进史就是争用史：一把全局锁拆成每 P 一个无锁队列，代价是 P 的间接层与 work stealing。
- 〔实测〕 `GODEBUG=schedtrace=250` 与 `asyncpreemptoff=1` 是调度器的两个取证旋钮，前者看线程与队列，后者复现协作式时代。
- channel 惯用法背后都是同一条铁律（发送方负责 close），nil channel 是 select 的控制流工具。
- sync 的禁止复制是 Go 值语义独有的坑；基准数据要用本机重测的，量级结论跨版本成立，数字不成立。
- fatal 与 panic 两档兜底：map 并发写无 recover 可言，这类错误 Rust 在编译期拦下。

## 章末参考

### Rust/tokio 对照

| Go | Rust/tokio | 关键差异 |
|---|---|---|
| goroutine + GMP | task + 多线程 work-stealing runtime | 同构；Go 多一个信号抢占（tokio 靠 await 让出） |
| `spawn(f) <-chan signal` | `JoinHandle.await` | Go 无单 goroutine 句柄，信号即 join |
| 计数信号量 `chan struct{}` / `close` 广播 | `tokio::sync::Semaphore` / `tokio::sync::broadcast` | 同构；Go 的广播是一声 close |
| `select` 随机分支 | `tokio::select!` 随机（`biased;` 可改） | Go 无 biased 选项 |
| 置 nil 摘 select 分支 | 分支不能动态摘除（Option\<Receiver\> 分层） | Go 独有技巧 |
| `sync.Mutex`（不可复制） | `std::sync::Mutex`（Arc 包装） | Rust 编译器杜绝复制坑 |
| `atomic.Int64` | `AtomicI64` | Go 1.19 起类型化封装 |

### 速查与深读

- 调度器参数：GOMAXPROCS=P 数；forcePreemptNS=10ms；runq 256 槽；线程暴涨根源是 syscall 挂 M 解绑 P，空闲 M 不够就 newm。
- 惯用法口诀：信号可用无缓冲、广播用 close、限流用缓冲容量；len(ch) 只是快照，非阻塞收发用 select/default。
- map 并发写 fatal；recover 拦不住；用 sync.Map 或 RWMutex。
- 原材料单元：TB31（并发定义与 CSP）、TB32（GMP 演进与抢占）、TB33（channel 惯用法）、TB34（sync 与 atomic 基准）、TB35（workerpool）、C:go_basic/concurrence（行为矩阵实测源码）、C:dachang/concurrence（手写合集，多数进索引）。
- 旧章交叉：p3 §goroutine 有多便宜（2KB/11 线程实测）、§select（三规则与 time.After 版本史）、§数据竞争（946 与 -race 输出）、§锁与原子操作（选型）、§context（三纪律）、§panic（goroutine panic 杀进程）。
- 源码：runtime2.go 的 g/p/m 三结构（本机 go1.26.4 行号 g:473/m:618/p:772）、proc.go 的 retake(L6630)/preemptone(L6866)/schedule(L4135)/findRunnable(L3389)、signal_unix.go 的 preemptM(L369)。
- 提案：Austin Clements《Asynchronous preemption for Go》（Go 1.14 异步抢占）。

## 练习：p31sched

先跑全绿（TestLostUpdateDistribution 故意制造竞争复现脏写，-race 下自动 Skip（skip-ok 注释在测试里），必须按第一条命令在无 -race 下单独跑）：

```sh
$ go test -count=1 -run "TestLostUpdateDistribution" ./exercises/p31sched/
ok  	golearn/exercises/p31sched	0.3s
$ go test -race -count=1 -run "Test[^L]" ./exercises/p31sched/
ok  	golearn/exercises/p31sched	2.5s
```

12 个测试各考一件事：

- `TestSignalOneToOne`/`TestBroadcastClose`/`TestCounterByChannel`/`TestCountingSemaphore`：channel 惯用法群一节的四惯用法，spawn 1 对 1、close 起跑枪声、后台 goroutine 独占、容量 3 限 9 job 批次分布。
- `TestNilChannelSelect`/`TestDegenerateZeroStorm`：select 三式与 nil channel 一节的置 nil 防零值风暴与退化对照。
- `TestWorkerPool`/`TestFunctionalOptions`：workerpool 与功能选项一节的池（active 信号量+for-select+哨兵错误）与 WithBlock(false) 池满返回 ErrNoIdleWorkerInPool。
- `TestLostUpdateDistribution`/`TestAtomicCounter`/`TestRWMutexMatrix`/`TestChildCtxInheritsDeadline`：实测五点一节的脏写分布对 atomic 恒 1000、锁矩阵、子 ctx 寿命。

**预测**（先写答案再跑，猜错的地方才是认知边界；四个问题都对应一个可跑的预测）：
1. TestCountingSemaphore：9 个 50ms job 配容量 3 的信号量，批次数是 2、3 还是 4？批次里会不会出现 1 个的尾巴？
2. TestNilChannelSelect：不置 nil 的退化版，1000 次 select 里有几次命中已关闭的 ch1？
3. TestWorkerPool：把 tasks 从无缓冲改成容量 10 的缓冲，Schedule 还阻塞吗？背压去哪了？
4. TestChildCtxInheritsDeadline：父 1000ms 睡 500ms 后子设 1000ms，子几毫秒到期？

**观察**（跑之前写下你预期的数字，跑完对照）：
5. `go test -bench "Msg" -benchtime=3s ./exercises/p31sched/`：1 对 1 与 10 对 10 里缓冲 channel 的差距各多大？batch 分布（TestCountingSemaphore 的输出）里边界抖动把两批并成一批了吗？
6. `GODEBUG=schedtrace=250 go run` 练习包外的 syscallsched：threads 什么时候涨、什么时候回落？观察点：TestCountingSemaphore 的 batch 用 50ms 整除切窗，job 时长恰好 50ms 时调度延迟会把两窗并成一窗，分布就是调度延迟的直读。

**改造**（三题各练一个惯用法的边界）：

7. 改造 Pool：加 WithMaxIdleTime 选项，空闲超时的 worker 自己退出并释放 active（先想清楚计数由谁释放，答案在 newWorker 的 defer 链里）。
8. 把 TestCountingSemaphore 的信号量换成 `golang.org/x/sync/semaphore.Weighted`，对比 API 与行为。
9. 把 TestCounterByChannel 的 counter 泛型化（`chan func() int` 返回新值），对比锁版与 channel 版的调用方手感。

配套基准：BenchmarkMutexCritical、BenchmarkChanCritical、BenchmarkRWMutexRead/Write（串行+Parallel 两组）、BenchmarkAtomicAdd、BenchmarkAtomicLoad、BenchmarkMsgUnbuf、BenchmarkMsgBuf100、BenchmarkMsg10x10Unbuf、BenchmarkMsg10x10Buf100；配套说明性测试 TestMapFatal（`t.Skip("skip-ok: 正文已实拍，真跑会崩测试进程")`，真跑会 fatal 崩掉测试进程）。
