# 06 并发一：goroutine、channel、select 与 sync

> 详细程度：深入 · 约 45 分钟
> 材料：TB31 到 TB35、`C:go_basic/concurrence`、`C:dachang/concurrence` · 旧长版：`notes/p3-concurrency.md`、`notes/p31-concurrency-scheduler.md`
> 前置：04 接口与组合、05 错误与资源

## 先看一个反直觉的现象

很多人对 channel 的第一印象是：`close` 之后它就"死了"。下面这段代码，先自己猜一下三行输出，再往下读。

```go
package main

import "fmt"

func main() {
	ch := make(chan int, 1)
	ch <- 1
	close(ch)

	fmt.Println(<-ch) // 关闭后缓冲里的数据仍能读到
	v, ok := <-ch
	fmt.Println(v, ok) // 读完了，得到零值与 ok=false
}
```

```text
1
0 false
```

第一行 `1` 说明关闭后的 channel 不是死物，缓冲里剩的数据照样能读；第二行 `0 false` 说明读空之后它不报错，而是立刻返回零值和 `ok == false`。要真正讲清这个行为，得先弄明白 goroutine 是怎么被调度的、channel 为什么本质上是两个 goroutine 之间的同步点、以及 select 怎么在多个 channel 上选。这三样就是这一节的三根支柱。

## 和你已知语言的差异

| 问题 | Java | Rust（tokio） | Go |
|---|---|---|---|
| 执行单元 | OS 线程，约 1MB 栈；虚拟线程 | task，需要 async 运行时 | goroutine，初始栈 2KB，运行时调度 |
| 函数着色 | 无 | `async fn` 与同步函数不同色 | 无，普通函数直接 `go f()` |
| 通信 | `BlockingQueue`、共享内存加锁 | `mpsc`、`Arc<Mutex>` | channel，是语言内建的一等公民 |
| 取消 | 中断位、`Future.cancel` | drop、`CancellationToken` | `context.Context`（下一节） |
| 数据竞争 | 无内建检测，表现为偶发错误 | 编译期排除（`Send`/`Sync`） | 编译器不拦，靠 `-race` 抓 |
| 子任务 panic | 线程内抛出，不影响其他线程 | task 内被隔离，`JoinHandle` 返回错误 | 未恢复的 panic 终止整个进程 |

最后两行最值得记。Rust 的 `Send`/`Sync` 在编译期就把"这个类型能不能跨线程"检查完了，Go 没有这套，数据竞争只能在运行时靠 `-race` 抓，抓不到就表现为偶发错误。Java 的线程 panic 只挂掉那条线程，Go 的 goroutine 一旦 panic 且没人 recover，整个进程直接退出。

## 一、goroutine：便宜，但不是免费

`go f()` 创建一个 goroutine。为什么 Go 用 goroutine 而不是 OS 线程？看一个数字：OS 线程默认栈约 1MB，goroutine 初始栈只有 2KB，由 Go 运行时在用户态调度，切换不陷入内核。〔TB31 p6〕规模化的能力来自三点：栈小且可增长、用户态切换、`go` 关键字加 channel 内建。这三点凑在一起，让"每连接一个 goroutine"成为默认写法。

但规模化之后账就不一样了：2KB 乘一百万是 2GB；栈增长后不会立刻缩回，收缩只发生在 GC 扫描栈时，且仅当使用量不足四分之一才减半，闲置的大栈可能长期占着内存。原材料写的是"不会收缩，直到 goroutine 退出"，那是过度简化，`runtime/stack.go` 的 `shrinkstack` 可证。〔TB35 p2〕所以每连接一个 goroutine 在常规规模下完全合适，进入百万级才需要池化。

必须记住的三条生命周期规则：〔TB31 p7-8〕

- 创建者无法拿到 goroutine id，不要试图依赖它。
- 函数返回即退出，**返回值被丢弃**，要结果就用 channel 传回。
- `main` 退出，整个进程退出，不管还有多少 goroutine 在跑。

第二条最反直觉：`go` 后面跟的函数返回值直接没了。Java 里 `new Thread(...).start()` 之后还能拿到 `Future` 或者 join 结果，Go 没有这个通道，只能自己用 channel 补上：

```go
package main

import (
	"errors"
	"fmt"
	"time"
)

func spawn(f func() error) <-chan error {
	c := make(chan error)
	go func() { c <- f() }()
	return c
}

func main() {
	c := spawn(func() error {
		time.Sleep(50 * time.Millisecond)
		return errors.New("boom")
	})
	fmt.Println("子 goroutine 结果:", <-c)
}
```

```text
子 goroutine 结果: boom
```

`spawn` 把"goroutine 的退出状态"通过一个 `chan error` 传回来，调用方 `<-c` 拿结果。这就是 CSP 的基本形态：channel 转移数据，同时转移了数据的所有权。Go 的并发有两层，要分清各自的位置：

1. **默认用 CSP**：把程序拆成若干顺序执行的 goroutine，用 channel 把它们连接起来。channel 传递的是数据，同时转移了数据的所有权。"不要通过共享内存来通信，而要通过通信来共享内存。"〔TB31 p9〕〔TB34 p1-2〕
2. **局部用低级同步**：不转移所有权、只是想保护一个结构体的内部状态，或者性能敏感的临界区，用 `sync` 和 `atomic`。〔TB31 p10〕〔TB34 p4〕

另一个要内化的区分：**并发是结构，并行是执行**。把程序设计成可以独立执行的几部分，叫并发；这几部分同一时刻真的在多个核上跑，叫并行。并发设计的程序在单核上也成立，上多核自然获得并行。〔TB31 p4-5〕

Go 1.22 起，`for` 循环的变量是每轮独立的，`go func() { use(i) }()` 的老坑已不存在，这一点以本机 go1.26.4 的语言版本为准。

### 调度器：一张概念地图

只需要记住结构，不用背源码。〔TB32 p2-6〕〔TB32 p10-11〕

- **G**（goroutine）、**M**（OS 线程）、**P**（逻辑处理器）。G 必须先进入某个 P 的本地运行队列，P 与 M 绑定后才真正执行。P 的数量就是最大并行度，由 `GOMAXPROCS` 决定。队列空了会去别的 P 偷（work stealing）。
- 阻塞在 channel 或网络 I/O 上时，G 挂起、M 继续跑下一个 G；网络 I/O 由 netpoller 管理，只阻塞 G 不阻塞线程。
- 阻塞在慢系统调用上时，M 带着 G 一起阻塞，P 被交给别的 M；这就是慢系统调用会让线程数增加的原因。
- 抢占：Go 1.2 起靠函数入口检查的协作式抢占；纯计算死循环抢不动。Go 1.14 起，监控线程 sysmon 对运行过久的 G 发信号做异步抢占。
- 容器里的 `GOMAXPROCS`：语言版本 1.25 及以上，默认值会参考 Linux cgroup 的 CPU 配额，不再简单取机器核数。〔实测〕`go doc runtime.GOMAXPROCS` 的 Default 一节在 go1.26.4 上可见。

## 二、channel：状态机比语法重要

channel 的行为由它当前所处的状态决定，不是由"有没有被 close 过"决定。把下面这张表记住，就能预测几乎所有"为什么阻塞了、为什么 panic 了"。

| 操作 | nil | 空 | 有数据且未满 | 满 | 已关闭 |
|---|---|---|---|---|---|
| 发送 | 永久阻塞 | 成功（无缓冲则等接收方） | 成功 | 阻塞 | panic |
| 接收 | 永久阻塞 | 阻塞 | 成功 | 成功 | 先读完剩余数据，之后立即返回零值与 `ok == false` |
| `close` | panic | 成功 | 成功 | 成功 | panic |

〔实测〕每一项都用 `go run .` 在 go1.26.4 上逐项验证过，最紧凑的对照是这段片段：

```go
c := make(chan int, 1)
c <- 1
close(c)
v, ok := <-c   // 1 true：关闭后缓冲里剩下的数据仍能读到
v, ok = <-c    // 0 false
c <- 2         // panic: send on closed channel
close(c)       // panic: close of closed channel
var n chan int
close(n)       // panic: close of nil channel
```

表里最容易踩的两个坑，各自用一个能真正跑出错的程序钉死。第一个：同一个 goroutine 里读写同一个无缓冲 channel，收发双方必须同时存在，否则单方面操作让整个程序挂起：

```go fail
package main

func main() {
	ch := make(chan int)
	ch <- 1 // 没人接收，也没有别的 goroutine
	<-ch
}
```

```text
fatal error: all goroutines are asleep - deadlock!
```

第二个：向已关闭的 channel 发送。这解释了开头的现象为什么只限于"接收"：接收方从关闭的 channel 读是安全的，发送方往关闭的 channel 写必 panic：

```go fail
package main

func main() {
	ch := make(chan int)
	close(ch)
	ch <- 1
}
```

```text
panic: send on closed channel
```

由此推出的规则：〔TB33 p6-7〕

- **谁发送，谁关闭**。接收方无法安全判断是否已关闭，向已关闭的 channel 发送会 panic。
- 接收 `v, ok := <-ch` 判关闭，`for range ch` 在关闭后结束。
- 无缓冲 channel 是同步点，收发两端必须在不同 goroutine，否则当场死锁；带缓冲 channel 是异步队列。〔TB33 p3-5〕
- 用单向类型（`chan<- T`、`<-chan T`）在函数签名里限制权限，这是最便宜的文档。〔TB33 p5-6〕
- `len(ch)` 不能当同步依据：多个 goroutine 共同收发时，"判空之后、读取之前"数据已被别人取走。〔TB33 p19-22〕

## 三、channel 的四种惯用法

1. **信号传递，一对一**：`chan struct{}` 当作完成信号，空结构体不占空间。启动函数返回 `<-chan struct{}`，调用方 `<-done` 等待。〔TB33 p8-9〕
2. **信号传递，一对多（广播）**：多个 goroutine 都阻塞在同一个无缓冲 channel 上，`close` 它，所有人同时被唤醒。这是"起跑枪"，也是 `ctx.Done()` 的原理。〔TB33 p9-11〕
3. **替代锁**：让一个 goroutine 独占数据，别人通过 channel 请求它。契合"通过通信共享内存"，但开销比 `Mutex` 大，只在逻辑上更清晰时用。〔TB33 p11-13〕
4. **队列与计数信号量**：带缓冲 channel 天然是并发安全的 FIFO 队列；容量就是最大并发数，发送是获取、接收是释放，用来限制同时在跑的 goroutine 数。〔TB33 p13-16〕〔TB33 p16-17〕

广播为什么能同时唤醒？因为所有 goroutine 都阻塞在同一个 channel 的接收上，`close` 是一次性事件，会让所有等待者立刻醒来。下面用"编号之和"验证三个 goroutine 全部就绪，而不是只醒了一个：〔实测〕`go run .`

```go
package main

import (
	"fmt"
	"sync"
)

func main() {
	start := make(chan struct{})
	ready := make(chan int, 3)
	var wg sync.WaitGroup

	for i := 1; i <= 3; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start // 等起跑枪
			ready <- id
		}(i)
	}
	close(start) // 三个 goroutine 同时被唤醒
	wg.Wait()
	close(ready)

	sum := 0
	for id := range ready {
		sum += id
	}
	fmt.Println("全部就绪，编号之和:", sum)
}
```

```text
全部就绪，编号之和: 6
```

如果只有一部分 goroutine 醒来，编号之和就不是 6。计数信号量的用法是：`sem := make(chan struct{}, 2)`，发送是获取、接收是释放，容量 2 就意味着同一时刻最多 2 个任务在跑。下面实测了峰值并发数：

```go
package main

import (
	"fmt"
	"sync"
	"time"
)

func main() {
	sem := make(chan struct{}, 2)
	var wg sync.WaitGroup
	var mu sync.Mutex
	running, peak := 0, 0

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			sem <- struct{}{} // 获取：容量就是并发上限
			mu.Lock()
			running++
			if running > peak {
				peak = running
			}
			mu.Unlock()
			time.Sleep(20 * time.Millisecond)
			mu.Lock()
			running--
			mu.Unlock()
			<-sem // 释放
		}(i)
	}
	wg.Wait()
	fmt.Println("同时执行的峰值:", peak)
}
```

```text
同时执行的峰值: 2
```

## 四、select：多路监听的三式

`select` 同时等待多个 channel，哪个就绪走哪个，多个同时就绪则随机选一个。〔TB33 p7-8〕三种惯用写法：

1. **default 防阻塞**：所有 case 都未就绪时走 `default`，变成"尝试收发"。标准库 `time` 包就这样非阻塞地给 channel 送时间。注意它改变了 channel 状态：尝试成功就是真收走、真写入了一个元素。〔TB33 p25-26〕
2. **超时**：`case <-time.After(d)` 避免长等。定时器由运行时维护，代价小。原材料提醒要及时回收，那是 Go 1.23 之前的行为；1.23 起未被引用的定时器会被垃圾回收，〔1.26 修正〕见 `go doc time.After`。〔TB33 p27〕
3. **心跳**：`time.NewTicker(d)` 的 `.C` 周期性产出事件，和业务 channel 放在同一个 `select` 里，用完 `defer t.Stop()`。〔TB33 p27-28〕

超时是最常用的一式，先用一个能跑的程序看它长什么样：

```go
package main

import (
	"fmt"
	"time"
)

func main() {
	ch := make(chan int)
	select {
	case v := <-ch:
		fmt.Println("收到", v)
	case <-time.After(10 * time.Millisecond):
		fmt.Println("超时了")
	}
}
```

```text
超时了
```

### nil channel 的妙用

对 nil channel 收发都永久阻塞，所以在 `select` 里把某一路置为 nil，就等于把这个分支关掉。〔TB33 p22-25〕反过来，nil channel 的 case 永远不会就绪，`select` 会直接跳过它：

```go
package main

import "fmt"

func main() {
	var ch chan int // nil channel
	select {
	case v := <-ch:
		fmt.Println("收到", v)
	default:
		fmt.Println("nil channel 永远不会就绪，落到 default")
	}
}
```

```text
nil channel 永远不会就绪，落到 default
```

没有这一招，已关闭的 channel 会被一直选中，反复读出零值形成空转。下面这个"读完一路就置 nil"的合并模式，就是它的标准用法：

```go
for a != nil || b != nil {
	select {
	case v, ok := <-a:
		if !ok {
			a = nil
			continue // 这一路读完了，关掉它
		}
		fmt.Println("a", v)
	case v, ok := <-b:
		if !ok {
			b = nil
			continue
		}
		fmt.Println("b", v)
	}
}
```

〔实测〕`go run .`：a 路与 b 路的值各打出来，先后随机，循环正常结束，不会在已关闭的一路上空转。

## 五、sync 家族怎么选

口诀：读多写少用 `RWMutex`，等条件成立用 `Cond`，不确定就用 `Mutex`，极致性能加大并发加读多写少才用 `atomic`。〔TB34 p18-19〕下面是支撑口诀的几条事实。

**Mutex 与 channel 的取舍**。channel 建构在低级原语之上，保护同一个共享计数时开销更大。用一个整数自增做基准（`b.RunParallel`），〔实测〕`go test -bench`，Apple M4、go1.26.4 得到：

```text
BenchmarkMutex     71.86 ns/op
BenchmarkAtomic    16.07 ns/op
BenchmarkChanSem   94.78 ns/op   // 容量 1 的 channel 当锁
```

数字随机器浮动，量级是结论：`atomic` 最快，`Mutex` 其次，channel 最重。原材料在另一台机器上只比较 `Mutex` 与 channel 信号量，结论同样是 channel 慢三倍多。〔TB34 p2-4〕所以：数据对象要转移所有权，用 channel；只是保护内部状态，用 `Mutex`。〔TB34 p4〕用 `Mutex` 把前面的脏写改成安全写，结果是确定的 1000：

```go
package main

import (
	"fmt"
	"sync"
)

func main() {
	var mu sync.Mutex
	n := 0
	var wg sync.WaitGroup

	for i := 0; i < 1000; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mu.Lock()
			n++
			mu.Unlock()
		}()
	}
	wg.Wait()
	fmt.Println(n)
}
```

```text
1000
```

**RWMutex 只在擅长的场景才占便宜**：并发量不大时 `Mutex` 更快；写锁在大并发下最差；读多写少且有一定并发量时才有优势。〔TB34 p8-10〕行为矩阵：无写者排队时同一 goroutine 重复 `RLock` 能成功，但官方文档明确禁止递归读锁（见 `go doc sync.RWMutex`）；写锁不可重入（重入即死锁）；有写锁在排队时新的读锁也会阻塞，防止写饿死，所以递归读锁在写者出现时会死锁。〔C:go_basic/concurrence/rwlock.go:33-67〕

**Cond** 只用于"等待某个条件成立"，用法固定：加锁，`for !cond { c.Wait() }`，解锁。`Wait` 挂起前释放锁、返回前重新加锁。能用 channel 的 `close` 广播时不必用它。〔TB34 p10-14〕

**atomic** 的本质是不可中断的单条指令级"事务"，只能同步单个整型或自定义类型变量。读写都很便宜，复杂临界区仍首选 `sync`。〔TB34 p14-18〕

**Mutex 不能复制**。复制后得到两把独立的锁，同步失效；`go vet` 会抓：〔TB34 p4-7〕

```text
main.go:13:9: Inc passes lock by value: x.Counter contains sync.Mutex
```

方法接收者、函数参数里传带锁的结构体都要用指针。

## 六、数据竞争、泄漏：两类并发 bug 的现场

**数据竞争**。`n++` 是读、加、写三步，1000 个 goroutine 同时做，结果小于 1000。〔实测〕该程序在 go1.26.4 上用 `go run -race .` 会直接报 `WARNING: DATA RACE`，并给出读写双方的 goroutine 栈：

```go
var n int
var wg sync.WaitGroup
for i := 0; i < 1000; i++ {
	wg.Add(1)
	go func() {
		defer wg.Done()
		n++ // 读、加、写三步，中间可以被别的 goroutine 打断
	}()
}
wg.Wait()
fmt.Println(n) // 通常小于 1000，具体值每次都不一样
```

要点是：竞争不一定崩溃，没有 `-race` 时只是悄悄丢数据。CI 里跑 `go test -race` 应当是默认动作。数组、slice、结构体并发改只是脏写，而 map 并发写会触发 `fatal error`，**不能 recover**。〔C:go_basic/concurrence/atomic.go:11-19〕

**goroutine 泄漏**。goroutine 停在一个永远不会就绪的收发上，既不退出也不被回收，内存和调度成本随时间累积。〔实测〕用 `runtime.NumGoroutine()` 前后相减：

```go
package main

import (
	"fmt"
	"runtime"
	"time"
)

func main() {
	before := runtime.NumGoroutine()
	for i := 0; i < 100; i++ {
		ch := make(chan int)
		go func() { <-ch }() // 没人发送，也没人关闭，永久阻塞
	}
	time.Sleep(20 * time.Millisecond)
	fmt.Println("泄漏的 goroutine 数:", runtime.NumGoroutine()-before)
}
```

```text
泄漏的 goroutine 数: 100
```

常见成因：发送到没人接收的 channel、等待永不关闭的 channel、`select` 没有退出分支、请求超时后子 goroutine 还在往一个没人读的 channel 写。预防的两个习惯：每个 goroutine 都能说清"它因为什么而退出"；带结果的 channel 给出缓冲，使发送方不会因为接收方离开而卡死。检测手段是监控 `runtime.NumGoroutine()` 的趋势，或抓 pprof 的 goroutine profile。下一节用 `context` 把"退出条件"做成统一机制。

## 小结

1. 并发是结构、并行是执行；默认用 CSP（goroutine 加 channel），局部才用 `sync` 和 `atomic`。
2. goroutine 初始栈 2KB、用户态调度；返回值被丢弃，结果靠 channel 传回；`main` 退出进程就退出。
3. 调度器记住 G/M/P 三个角色和 work stealing，P 的数量由 `GOMAXPROCS` 决定。
4. channel 是状态机：对 nil 收发永久阻塞，对已关闭的接收返回零值、发送 panic；谁发送谁关闭。
5. 四种惯用法：一对一信号、close 广播、替代锁、队列与计数信号量。
6. `select` 三式：`default` 防阻塞、`time.After` 超时、`Ticker` 心跳；nil channel 用来关掉某个分支。
7. `sync` 选型：不确定用 `Mutex`，读多写少用 `RWMutex`，等条件用 `Cond`，单变量极致性能用 `atomic`；带锁结构体不要按值传。
8. 数据竞争靠 `-race` 抓、泄漏靠 `NumGoroutine` 趋势看，两者的退出条件都要在写的时候想清楚。

## 常见坑

1. 向已关闭的 channel 发送会 panic，接收方去关闭 channel 是典型误用。
2. 在同一个 goroutine 里读写同一个无缓冲 channel，`fatal error: all goroutines are asleep`。
3. `select` 里某一路关闭后没置 nil，被反复选中空转。
4. `Mutex`、`WaitGroup` 按值传递或复制，`go vet` 能抓，别忽略告警。
5. 子 goroutine 没有 `defer recover`，一个 panic 带走进程。
6. 靠 `len(ch)` 判断队列是否有数据。
7. 周期性事件用 `Ticker` 并 `Stop`，不要在热循环里靠反复 `time.After` 模拟。

## 判断题与答案

先自己答，再看后面。

1. 为什么无缓冲 channel 的收发必须在两个 goroutine 里？带缓冲时有什么变化？
2. 要等 10 个 goroutine 同时开跑，最简单的实现是什么？为什么它能同时唤醒？
3. `Mutex` 与容量为 1 的 channel 都能互斥，各自适合什么场景？
4. 对 nil channel 做 `select` 为什么是"有用"的？
5. 一个 goroutine 里 `panic`，`main` 里的 `defer recover()` 能挡住吗？

**答案**

1. 无缓冲 channel 的收发是同步点，单方面操作会让当前 goroutine 挂起，唯一能救它的是另一个 goroutine 的配对操作，没有配对就全体睡死。带缓冲 channel 在缓冲区未满/非空时收发不阻塞，同一 goroutine 里也能先写后读。
2. 建一个无缓冲 `chan struct{}`，10 个 goroutine 都 `<-start`，主线程 `close(start)`。`close` 是一次性事件，会唤醒所有阻塞在它上面的接收者，所以是广播而不是只放行一个。
3. 数据对象要转移所有权时用 channel，逻辑上"把数据交给谁"说得清；只是保护一块共享内部状态、不转移所有权时用 `Mutex`，因为它建构在更低级的原语上、开销更小。
4. nil channel 的收发永久阻塞，case 永远不会就绪，`select` 会跳过它；把读完的一路置 nil，就等于关掉这个分支，避免在已关闭的 channel 上空转。
5. 挡不住。`recover` 只能在 panic 所在的那条 goroutine 的 defer 里生效，`main` 的 defer 管不到别的 goroutine；子 goroutine 必须自己 `defer recover()`。

练习包 `exercises/p31sched/`：`TestSignalOneToOne`、`TestBroadcastClose`、`TestCountingSemaphore`、`TestNilChannelSelect`、`TestRWMutexMatrix`、`TestLostUpdateDistribution`、`TestAtomicCounter`；`exercises/p3conc/`：`TestPanicKillsProcess`、`TestCounters`。

## 在项目里

- **带缓冲 channel 做背压**：`dnsproxi-go/main.go` 的 `Run` 在 `WorkerPool` 形态下，读循环把包塞进带缓冲的 `qch`，worker 取出处理，队列满了读循环自然阻塞，这就是计数信号量与队列的组合。
- **select 加 default**：`dnsproxi-go/cache/expire.go` 的 `RunExpirer` 外层 `select` 同时等 `done` 与 `Ticker`，内层往统计 channel 发送时用 `default`，消费者不在就丢，让老化不被观测拖慢。
- **关闭广播**：同一个 `Run` 里 expirer 的 `done` 通道由 `stopExpirer` 闭包在读循环退出路径上 `close` 一次，老化 goroutine 因 `<-done` 退出；单次关闭靠的是调用顺序（先 close 再 `wg.Wait()`），不是 `sync.Once`。
- **atomic**：`dnsproxi-go/tcp/server.go` 的 `serveConn` 用 `atomic.Int64` 记录单连接在飞查询数，只同步一个整数，不需要锁。
- **sync.Once**：同文件的 `streamConn.Close` 用 `sync.Once` 保证底层连接只 `Close` 一次，让 `Close` 成为幂等操作。

## 想深入去哪查

- 原材料：TB31（CSP 与 goroutine 基础）、TB32（调度器演进与抢占，基于较老的 Go 版本，以结构为准）、TB33（channel 与 select 全部惯用法及基准）、TB34（sync 与 atomic 的选型基准）、TB35（goroutine 池，下一节会用到）。
- 代码：`C:go_basic/concurrence`（脏写复现、锁矩阵、sync.Cond 与 channel 等价、context）、`C:dachang/concurrence`（手写 Once、自旋锁、分段锁、协程池、泄漏现场）。
- 旧长版：`notes/p3-concurrency.md`、`notes/p31-concurrency-scheduler.md`（含调度器 G/P/M 结构体字段、sysmon 抢占链路、基准全表）。
- 不在这一节：`runtime` 里 `runq` 和 `sysmon` 的实现细节，遇到性能诊断再查 TB32 与旧长版。
