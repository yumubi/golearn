---
title: 07 并发二：context、pipeline、worker pool、errgroup 与优雅关停
---

# 07 并发二：context、pipeline、worker pool、errgroup 与优雅关停


> 详细程度：深入 · 约 90 分钟
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

`done` 给 1 个缓冲，使一次结果发送在调用方超时后仍可完成。但它不能中断 `LongTimeWork`：如果工作永远不返回，goroutine 仍会滞留。实际任务应接受并检查 context，或使用支持取消的 I/O。

用法约定：`ctx` 放函数第一个参数，不放进结构体字段；`WithValue` 的 key 用自定义类型（不是裸 string/int），避免和别人的 key 碰撞；值只用于跨 API 边界携带请求范围数据（追踪 ID 之类），不当可选参数传。〔C:go_basic/concurrence/context.go:76-127〕

服务端还有个常被忽略的点：客户端超时撤销请求，服务端的 `r.Context()` 会收到 `Done`，应该及时放弃处理，别继续给一个已经走了的客户端算结果。〔C:dachang/concurrence/timeout/server/main.go:1-35〕

## 二、pipeline：每一级都要听得见取消

pipeline 是“生成器、处理器、汇聚”串起来的 channel 链。每一级不仅发送时要监听取消，等待上游接收时也要监听取消；否则上游不关闭时，下游仍可能一直阻塞。

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
		for {
			select {
			case <-ctx.Done():
				return
			case x, ok := <-in:
				if !ok {
					return
				}
				select {
				case out <- x * x:
				case <-ctx.Done():
					return
				}
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

每级都由发送方 `defer close(out)`；发送和等待输入都通过 `select` 监听取消。因此即使上游不再发送也不关闭，取消仍能让 `square` 退出。取消与通信同时就绪时 select 可任选，所以这不是“取消后绝不再发送”的保证。

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
			for {
				select {
				case <-ctx.Done():
					return
				case x, ok := <-c:
					if !ok {
						return
					}
					select {
					case out <- x:
					case <-ctx.Done():
						return
					}
				}
			}
		}(c)
	}
	go func() { wg.Wait(); close(out) }() // 所有输入读完才关输出
	return out
}
```

扇入的每个转发 goroutine 都监听取消，汇聚 goroutine 等它们全部退出后关闭输出。上游也必须配合取消，否则即使扇入退出，上游仍可能卡在发送上。

扇出会打乱顺序。需要保序有两个办法：按下标把结果写进切片（各 goroutine 写自己的槽位，没有竞争），或让任务带序号、收齐再排序。按下标写最简单，第三节的 worker pool 就是这么做的。

## 三、worker pool：把规模钉死

每请求一个 goroutine 在中小规模完全合适；规模上去、或下游资源有限（数据库连接、文件句柄）时，就要限制同时在跑的数量。〔TB35 p2〕为什么"规模上去"要管住数量？goroutine 相对操作系统线程开销十分小：起始栈只有 2KB，创建、切换与销毁的代价都很低，可以创建成千上万甚至更多，所以 Go 应用惯例是为每个新建立的连接、甚至每个传入请求配一个 goroutine，好处是实现十分简单、写代码心智负担低。〔TB35 p2〕但"廉价"不是免费，最明显的就是规模化后非零成本成为瓶颈：按一个 goroutine 分配 2KB 执行栈计，100 万个 goroutine 就是 2GB 的内存消耗；其次，goroutine 越多，Go 运行时调度它们的处理器消耗越大，成为阻碍应用性能提升的重要因素。〔TB35 p2〕2KB 这个数至今成立：go1.26.4 运行时里 `stackMin = 2048` 字节，新建 goroutine 就按它分配初始栈（runtime/stack.go 77-78 行、proc.go 5319-5322 行）。最小的 pool 只需要一个任务 channel 和 N 个 worker：

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

### 计数信号量：先限并发，还不急着建池

如果只想限住"同时在跑"的数量，连池都可以不建。Go 并发设计的一个惯用法，是把带缓冲 channel 用作计数信号量：channel 中当前元素个数代表当前同时处于活动状态（正在处理业务）的 goroutine 数量，channel 容量代表允许同时活动的 goroutine 最大数量；发送操作表示获取一个信号量，接收操作表示释放一个信号量。〔TB33 p16〕原文的例子：`active` 容量 3、`jobs` 容量 10，一个 goroutine 向 `jobs` 投递 8 个 job 后 `close(jobs)`；main 用 `for range jobs` 为每个 job 起 goroutine 并 `wg.Add(1)`，goroutine 内先 `active <- struct{}{}` 获取、`log` 并 sleep 2s 模拟处理、再 `<-active` 释放，最后 `wg.Wait()`：〔TB33 p16-17〕

```go
var active = make(chan struct{}, 3)
var jobs = make(chan int, 10)

func main() {
	go func() {
		for i := 0; i < 8; i++ {
			jobs <- (i + 1)
		}
		close(jobs)
	}()

	var wg sync.WaitGroup

	for j := range jobs {
		wg.Add(1)
		go func(j int) {
			active <- struct{}{}          // 获取信号量
			log.Printf("handle job: %d\n", j)
			time.Sleep(2 * time.Second)
			<-active                      // 释放信号量
			wg.Done()
		}(j)
	}
	wg.Wait()
}
```

原文给出的运行输出如下：〔TB33 p17〕

```text
2022/01/02 10:08:55 handle job: 1
2022/01/02 10:08:55 handle job: 4
2022/01/02 10:08:55 handle job: 8
2022/01/02 10:08:57 handle job: 5
2022/01/02 10:08:57 handle job: 7
2022/01/02 10:08:57 handle job: 6
2022/01/02 10:08:59 handle job: 3
2022/01/02 10:08:59 handle job: 2
```

每个 job 约处理 2 秒、信号量容量 3：10:08:55 三个、10:08:57 三个、10:08:59 两个，8 个 job 按 3+3+2 三批完成。虽然创建了很多 goroutine，同一时刻处于活动状态处理 job 的最多 3 个。〔TB33 p17〕同批内先处理哪个 job 由调度决定，三批的节奏才是信号量决定的。〔自〕goroutine 一个都没少起（8 个 job 照样起了 8 个 goroutine）：信号量限制的是"进入业务处理段"的并发数，已有 3 个 goroutine 持有信号量时，第 4 个执行 `active <- struct{}{}` 会阻塞，直到某个活动 goroutine 处理完执行 `<-active` 腾出空位。〔TB33 p4-5〕〔TB33 p16-17〕它和 worker pool 怎么选：信号量版每个任务仍独享一个 goroutine，代码最少，适合任务数不大、不想维护 worker 的场景；要把 goroutine 本身的数量也钉死、让 N 个 goroutine 反复重用时，才需要下面的池。〔自〕

### workerpool：把 M 个任务调度到 N 个 goroutine 上

Goroutine 池的核心思想是对 goroutine 的重用：把 M 个计算任务调度到 N 个 goroutine 上，而不是为每个计算任务分配一个独享的 goroutine，从而提高计算资源利用率。〔TB35 p3〕workerpool 的工作逻辑通常很简单，即便是用于生产环境的实现，代码规模也都在千行左右。实现方式有很多种，原文为了更好地演示 Go 并发模型的应用模式以及并发原语间的协作，采用完全基于 channel+select 的实现方案：不使用其他数据结构，也不使用 sync 包提供的 Mutex、RWMutex、Cond 等同步结构：这是教学取向的取舍，生产实现不受此限；原文小结也再次申明这个实现只是演示项目，不能直接用于生产。〔TB35 p3〕〔TB35 p16-17〕

workerpool 的实现主要分三部分：pool 的创建与销毁；pool 中 worker（goroutine）的管理；task 的提交与调度，其中后两部分是"精髓"所在，原文用一张原理图表示这两部分。〔TB35 p3-4〕原理两句话：〔TB35 p4〕

- **worker 管理**：capacity 是 pool 的属性，代表整个池中 worker 的最大容量；用一个带缓冲 channel `active` 作 worker 的"计数器"，这正是 33 讲的计数信号量模式。运行规则按顺序三步：active 可写时，就创建一个新 worker，处理用户通过 Schedule 提交的请求；active 满了就停止创建 worker；直到某个 worker 因故退出、active 又空出一个位置，pool 才创建新 worker 填补那个空位。
- **task 提交与调度**：用户要提交的请求抽象为 Task（本质是 `type Task func()` 函数类型），通过 Schedule 提交到一个 task channel，已创建的 worker 从这个 task channel 中读取 task 并执行。〔TB35 p4〕〔TB35 p8〕

骨架如下（节选自原文 workerpool1，略去编号与日志，不能独立编译）：〔TB35 p5-8〕

```go
type Pool struct {
	capacity int           // workerpool 大小
	active   chan struct{} // 对应 active channel：计数信号量
	tasks    chan Task     // 对应 task channel
	wg       sync.WaitGroup // 用于在 pool 销毁时等待所有 worker 退出
	quit     chan struct{}  // 用于通知各个 worker 退出的信号 channel
}

func New(capacity int) *Pool {
	if capacity <= 0 {
		capacity = defaultCapacity // 防御性校验：回退默认值（具体数值原课未给出，在配套源码里）
	}
	if capacity > maxCapacity {
		capacity = maxCapacity // 超上限截断
	}
	p := &Pool{
		capacity: capacity,
		active:   make(chan struct{}, capacity), // 容量 = 池容量
		tasks:    make(chan Task),               // 无缓冲
		quit:     make(chan struct{}),
	}
	go p.run() // 管理机制随池一起运行
	return p
}

func (p *Pool) run() {
	for {
		select { // for+select 监视多个 channel 是 Go 惯用法
		case <-p.quit:
			return
		case p.active <- struct{}{}: // active 可写：腾出一个名额外
			p.newWorker()
		}
	}
}

func (p *Pool) newWorker() {
	p.wg.Add(1)
	go func() {
		defer func() {
			if err := recover(); err != nil { // 任务 panic：捕获后 worker 退出
				<-p.active // 归还计数
			}
			p.wg.Done()
		}()
		for {
			select {
			case <-p.quit: // 收到退出信号：归还计数后退出
				<-p.active
				return
			case t := <-p.tasks: // 领任务、执行
				t()
			}
		}
	}()
}

var ErrWorkerPoolFreed = errors.New("workerpool freed")

func (p *Pool) Schedule(t Task) error {
	select {
	case <-p.quit:
		return ErrWorkerPoolFreed // 池已销毁：哨兵错误而不是 panic
	case p.tasks <- t:
		return nil
	}
}
```

把骨架读成一条完整的流转线：〔TB35 p4-8〕

1. `New` 做防御性校验后 `go p.run()`，管理 goroutine 进入 for+select 循环，同时监视 quit 与 active；
2. 用户调 `Schedule(t)`，select 尝试把 t 发进无缓冲的 tasks，可写即提交成功；
3. `run` 发现 active 可写，创建一个新 worker；
4. worker 的 for-select 从 tasks 领到任务并执行 `t()`；
5. active 写满后 `run` 阻塞在 `p.active <- struct{}{}` 上，停止创建 worker；直到某个 worker 因 quit 或 panic 退出、`<-p.active` 归还计数腾出空位，才补建新 worker；
6. worker 全忙时，无缓冲的 tasks 让 `Schedule` 阻塞到有 worker 空闲，背压直接反馈给提交方。〔TB35 p8-9〕

两个容易忽略的细节，都有原文背书。其一，active 额度的归还有两处，且 worker 没有"正常退出"路径：收到 quit 退出时 `<-p.active` 归还一次，panic 被 defer+recover 捕获后同样归还再退出；作者在留言区明确，worker 一旦创建，除了 panic 和 quit 通知退出，不会退出，所以 defer 里不做无条件归还。〔TB35 p7-8〕〔TB35 p19〕其二，销毁后再提交不会 panic：Schedule 的 select 先检视 quit，有信号就返回哨兵错误 ErrWorkerPoolFreed，调用方拿到的是错误而不是崩掉。〔TB35 p8-9〕原文 demo1 验证：capacity 为 5 的池连投 10 个睡 3s 的任务再 Free，5 个 worker 各领 2 个任务，Free 后等所有 worker 执行完退出，最后输出 workerpool freed，与原理图一致。〔TB35 p9-11〕

这个最小实现"铁板一块"，原文接着用功能选项（functional option）补齐可定制行为：`WithPreAllocWorkers` 控制创建池时是否预建满 worker（默认 false；不预建时 run 先阻塞在 tasks 读取上，由首个提交的 task 触发按需创建，active 写满后回到第一版的调度逻辑），`WithBlock` 控制池满时 Schedule 是阻塞等待（默认 true）还是走 select 的 default 立即返回 ErrNoIdleWorkerInPool。〔TB35 p11-15〕选项机制即 05 讲的 Option 模式；"池满返回错误"正是第五节说的第三种背压策略。demo2 用 `WithBlock(false)` 提交 10 个任务：worker 尚未创建完毕，大量 task 立刻拿到 no idle worker in pool 错误而不是阻塞，具体哪些失败因调度不确定性每次都可能不同。〔TB35 p15-16〕

回看这不到 200 行，池里到处是 select 的固定搭配。33 讲总结的三种惯用法和 nil channel 的一招，值得在这里一次摆齐，以后读别人的并发代码，认的就是这些形状。

### select 工具箱：三式加一招

**case 的形态与阻塞规则。**select 的每个 case 是一次 channel 通信，支持三种形态加一个 default：

```go
select {
case x := <-ch1:     // 从channel ch1接收数据
	... ...
case y, ok := <-ch2: // 从channel ch2接收数据，并根据ok值判断ch2是否已经关闭
	... ...
case ch3 <- z:       // 将z值发送到channel ch3中:
	... ...
default:             // 当上面case中的channel通信均无法实施时，执行该默认分支
}
```

不写 default、且某一刻所有 case 上的 channel 通信都无法实施时，整个 select 阻塞，直到某个 case 的 channel 变为可发送或可接收才能继续；加上 default，同一时刻就执行 default。〔TB33 p7-8〕select 与 channel 两种原语操作都十分简单，共同遵循 Go"追求简单"的设计哲学，却为并发程序带来强大的表达能力。〔TB33 p8〕

**一式：default 防阻塞。**default 的语义是在其余非 default 分支因通信未就绪而无法被选择时执行，由此获得"避免阻塞"特性。〔TB33 p26〕最典型的是一对"事务式"函数，把"判定与收发"合并成一个不阻塞的整体：

```go
func tryRecv(c <-chan int) (int, bool) {
	select {
	case i := <-c:
		return i, true

	default: // channel为空
		return 0, false
	}
}

func trySend(c chan<- int, i int) bool {
	select {
	case c <- i:
		return true
	default: // channel满了
		return false
	}
}
```

无缓冲、带缓冲通吃：无缓冲 channel 没有接收方时发送无法实施，带缓冲 channel 空时接收、满时发送无法实施，统统走 default，两个函数都不会阻塞在空 channel 或元素个数已达容量的 channel 上。〔TB33 p26〕原文的 producer/consumer 演示里双方都不阻塞：channel 空时 consumer 打印 `[consumer]: try to recv from channel, but the channel is empty` 再试，channel 满时 producer 打印 `[producer]: try send [7], but channel is full` 再试。〔TB33 p20-21〕〔TB33 p26〕

代价要看清：这种侦测会改变 channel 的状态：tryRecv 成功就真收走了一个元素，trySend 成功就真写入了一个元素。想不改变 channel 状态、单纯侦测且不阻塞，目前没有适用于所有场合的方法；"多发送单接收""多接收单发送"两个特定场景可以退而用 len 判断（06 讲与 33 讲的 len 小节有展开）。〔TB33 p21-22〕

标准库同款：$GOROOT/src/time/sleep.go 的 sendTime 用 select+default 无阻塞地向 c 发送当前时间，定时器到期要通知使用者的 channel，但不保证有人在收，这次发送必须非阻塞：〔TB33 p26〕

```go
// $GOROOT/src/time/sleep.go（原文摘录，Go 1.17 时代签名）
func sendTime(c interface{}, seq uintptr) {
	// 无阻塞的向c发送当前时间
	select {
	case c.(chan Time) <- Now():
	default:
	}
}
```

Go 1.26.4 源码里 sendTime 仍是同样的非阻塞 select 发送，但签名已演化为 `func sendTime(c any, seq uintptr, delta int64)`，发送的是 `Now().Add(-delta)`，delta 补偿运行时延迟发送的偏差。（sleep.go 179-190 行，本机 go1.26.4）

**二式：超时。**带超时机制的 select 是 Go 中常见组合：通过超时事件既可避免长期陷入某种操作的等待，也可做异常处理：〔TB33 p27〕

```go
func worker() {
	select {
	case <-c:
		// ... do some stuff
	case <-time.After(30 *time.Second):
		return
	}
}
```

原文写作时的告诫：使用带超时的 select 要特别注意 timer 使用后的释放，尤其大量创建 timer 时；标准库 timer 由 Go 运行时自行维护、不是 OS 级定时器资源，使用代价比 OS 级低许多，但仍应尽量减少给运行时和 GC 的压力，及时调用 timer 的 Stop 方法回收资源。〔TB33 p27〕这条告诫有一个版本边界，按 go1.26.4 标准库修订：time.After 等价于 `NewTimer(d).C`；Go 1.23 之前，GC 不会回收尚未到期且未 Stop 的 timer，所以惯用法是 NewTimer 后紧跟 `defer t.Stop()`；从 Go 1.23 起，GC 能回收未被引用的 timer，即使它未到期、未 Stop，Stop 不再是为帮助 GC 所必需（仍可因"让计时提前停下来"等功能性原因调用），After 的文档也改写为"没有理由在 After 能胜任时偏好 NewTimer"。（按 go1.26.4 标准库 time/sleep.go 120-141、192-204 行核实）〔1.26 修正〕这条边界与 §一比较超时方案时给过的是同一条；Stop 的去留判断到这里就完整了：为了 GC 不必调，为了提前停表仍要调。兼容开关：GODEBUG=asynctimerchan=1 可恢复 Go 1.23 前的旧行为（未到期 timer 不被 GC 回收），该设置可能在 Go 1.27 或以后移除（sleep.go 137-141 行注释）。timer 确由运行时维护也有旁证：Timer 的 stopTimer/resetTimer 通过 `//go:linkname` 直连 runtime 实现（sleep.go 74-78 行声明）。

**三式：心跳。**结合 time 包的 Ticker，可以在监听 channel 的同时执行周期性任务：〔TB33 p27-28〕

```go
func worker() {
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-c:
			// ... do some stuff
		case <- heartbeat.C:
			//... do heartbeat stuff
		}
	}
}
```

NewTicker 创建的 Ticker 实例包含一个 channel 类型的字段 C，它按时间间隔持续产生事件，就像"心跳"一样：c 无数据可收时，for 循环每隔特定时间完成一次迭代再回到下一次 select。和 timer 一样，用完要调用 Stop，避免心跳事件在 ticker 的 channel（示例中的 heartbeat.C）中持续产生。〔TB33 p27-28〕两个补充边界：Stop 只停表、不关闭 .C，标准库 time/tick.go 的 Stop 注释明写 Stop does not close the channel，防止并发读取方看到错误的 tick（本机 go1.26.4 源码 49-52 行核实）；GC 边界与 timer 同款，Go 1.23 起未被引用的 Ticker 也能被 GC 回收，Stop 不再是为帮助 GC 所必需（同文件 28-35 行 NewTicker 注释）。

**nil channel：把枯竭的分支从 select 里摘掉。**channel 类型变量的值为 nil 时，对它的接收和发送都会阻塞：`var c chan int` 之后 `<-c` 或 `c <- 1` 都会让 main goroutine 永久阻塞。〔TB33 p22-23〕这个特性用来收拾一类 select 事故。原文的例子：ch1 在 5s 后发 5 并 close，ch2 在 7s 后发 7 并 close，主循环 for+select 监听两个 case，用 ok1/ok2 标志两者都收到后才 break，预期输出 5、7 后退出。实际时间线是：〔TB33 p23-24〕

1. 前 5s 两个 channel 都无数据，select 阻塞；
2. 第 5s ch1 发来 5 后被 close，select 选中 `case x := <-ch1` 输出 5，回到 for 重新 select；
3. 从已关闭的 channel 接收永远不会阻塞，新一轮 select 又选中这个分支，拿到元素类型的零值 0，循环输出 0（0 值风暴）；
4. 第 7s ch2 发来 7，某轮 select 选中该分支输出 7，退出条件满足，程序终止。

改进：case 改为 comma-ok 形式，当 ok=false（channel 已关闭）时显式将该 channel 置为 nil，对一个 nil channel 的获取操作会阻塞、永远不会就绪，于是这个 case 从此再也不会被 select 选中；退出条件相应改写为 `ch1 == nil && ch2 == nil`：〔TB33 p24-25〕

```go
// 翻车版：ch1 关闭后，这个分支永远就绪，反复读出零值
case x := <-ch1:
	ok1 = true
	fmt.Println(x)

// 改进版：comma-ok 判定关闭后置 nil
case x, ok := <-ch1:
	if !ok {
		ch1 = nil // 已关闭：置 nil，让这个分支从此不再被选中
	} else {
		fmt.Println(x)
	}
```

注意置 nil 不是关闭 channel：close 的语义、"谁发送谁关闭"的惯例都不变，这里只是利用"nil channel 收发皆阻塞"把 select 中已枯竭的分支永久摘除，避免已关闭 channel"永不阻塞接收零值"的特性劫持 select。〔TB33 p24-25〕改进后输出 5、7、program end，与预期相符。

至此本节的并发工具箱闭环：active 是计数信号量，run 的 for+select 是管理循环的惯用法，Schedule 的 select+quit 是非阻塞检视加哨兵错误，nil channel 一招在"多路汇聚、逐路枯竭"的场景里止住 0 值风暴。剩下两块工程化拼图在后面两节：errgroup 给 WaitGroup 补上错误传递、失败取消与并发上限（§四）；优雅关停的信号处理与超时兜底（§六）。

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

为什么能“远小于 300ms”？task 2 返回错误后取消派生 ctx，其余任务监听到取消并退出。前提是任务配合取消：如果任务不监听 ctx，它会继续执行，`Wait` 仍要等所有任务返回，不能提前返回。

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

关停时先停止接收新请求，再等待已接收的请求，并设置总超时。`Shutdown` 关闭 listener 和空闲连接，等待活动连接空闲；超时后返回 context 错误，不会自动强制关闭所有活动连接。需要强制终止时，调用方还要决定是否调用 `Close`。〔TB09 p9〕

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

- **map 的并发读写或写写需要同步**；运行时有时会以不可 recover 的 fatal error 终止，但不能依赖它检测所有竞争。数组、切片、结构体的竞争也不安全。要共享 map，可用 `sync.RWMutex` 包一层，或在适合的访问模式下使用 `sync.Map`；高竞争场景可考虑分段锁。〔C:go_basic/concurrence/collection_safety.go:6-11〕〔C:dachang/concurrence/concurrent_hash_map.go:10-106〕
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
2. 超时处理可以监听 `ctx.Done()`；如果通过独立 goroutine 返回一次结果，缓冲 channel 可避免结果发送卡住，但不能中断不配合取消的工作。
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
2. 一次结果发送可以放进缓冲，不依赖接收方继续接收；但工作本身必须能够结束或配合取消，否则仍可能泄漏。
3. 前两个 `Go` 立刻返回，第三个起会阻塞，直到有 worker 空出来（闸门满时 `Go` 阻塞调用方）。
4. 阻塞上游适合必须送达的数据；丢弃适合指标、日志；拒绝适合有明确失败语义的请求（调用方可重试）。
5. 先停监听器，新请求进不来，等在途的规模才不会再涨；反过来会边等边接新请求，可能永远等不完。

练习包 `exercises/p14pipe/`：`TestPipeline`、`TestFanoutResultOrder`、`TestFanoutLimit`、`TestFanoutFirstError`、`TestFanoutGoroutineLeak`、`TestGenCancel`、`TestFanin`；`exercises/p31sched/` 的 `TestWorkerPool`、`TestChildCtxInheritsDeadline`。

## 在项目里

- **背压（阻塞上游）**：`dnsproxi-go/main.go` 的 `Run` 在 worker pool 形态下，读循环把包写进带缓冲的 `qch`（容量等于 worker 数），满了读循环阻塞，压力回到内核的接收缓冲区。读错误时先 `close(qch)` 让 worker 排空，再 `wg.Wait()`，与关停顺序一致。
- **取消感知**：`Run(ctx)` 在 ctx 取消时先关监听口，读循环因此返回，再按"取消就返回 nil、否则包装错误"区分优雅关停与异常退出。
- **信号接入**：`main.go` 的 `main` 用 `signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)` 接住信号，`Run` 里那条优雅关停路径因此真正用得上。
- **后台任务的退出顺序**：`dnsproxi-go/cache/expire.go` 的 `RunExpirer(done, cfg)` 把主动老化 goroutine 收进同一个 `WaitGroup`，它要等 `done` 关闭才退出；`main.go` 把 `close(done)` 封装成 `stopExpirer`，在 `wg.Wait()` **之前**调用。顺序反了就互等死锁：`Run` 等 `wg`，expirer 等 `done`，而 `done` 若挂在 `Run` 返回时才关，谁也等不到谁。这是 `WaitGroup` 与通道互等的典型形态。

## 想深入：正文之外的干货

### TB35 goroutine 池的完整实现

- 池化的成本账要算全：百万 goroutine 光栈就是 2GB（每个 2KB），调度器的处理器消耗还随数量同步上涨。池化就是把 M 个任务调度到 N 个 goroutine 上重用，生产级的实现也就千行左右。〔TB35 p2-3〕
- 连续栈的隐藏代价：Go 1.4 起栈不够用就分配一块更大的连续内存，把旧栈整体拷过去，换来的是 1.3 分段栈 hot split 问题的消失；代价是 grow 之后即便不再需要也不收缩，大栈一直闲置到 goroutine 退出。06 节对"不收缩"有精确化：只在 GC 扫描且用量不足四分之一时才减半。〔TB35 p2〕
- 教学版池刻意只用 channel+select，不碰 Mutex、RWMutex、Cond：active 是带缓冲 channel，当 worker 计数器用（就是 06 讲的计数信号量），run 循环里 `p.active <- struct{}{}` 可写就创建 worker，写满就停，某个 worker 退出腾出空位再补。〔TB35 p3-6〕
- 对外只有三个 API：New 建池并启动管理 goroutine，Free 停掉所有 worker，Schedule 提交 `type Task func()`；run 的管理循环是 for+select 监视 quit 与 active，这是 Go 的惯用法。〔TB35 p5-8〕
- active 额度的归还有两处，且 worker 没有"正常退出"路径：收到 quit 退出时 `<-p.active` 归还一次，panic 被 defer+recover 捕获后同样归还再退出；除这两者外 worker 永不退出，所以 defer 里不做无条件归还。〔TB35 p7-8〕〔TB35 p19〕
- 池销毁后再提交：Schedule 的 select 先检视 quit，有信号就返回哨兵错误 ErrWorkerPoolFreed，调用方拿到的是错误而不是 panic。〔TB35 p8-9〕
- demo1 定义的预期行为：capacity=5 提交 10 个睡 3 秒的任务，5 个 worker 各领一个排队做完；Free 之后要等所有 worker 把手里的任务执行完，进程才退出。〔TB35 p9-11〕
- 功能选项的出处与默认值：Rob Pike 2014 年的《自引用函数与选项设计》，`type Option func(*Pool)`，WithBlock 与 WithPreAllocWorkers 都是返回闭包的函数；默认 block=true（池满时 Schedule 阻塞）、preAlloc=false（按需建 worker）。〔TB35 p11-12〕
- 按需创建的机关：preAlloc=false 时 run 阻塞在读 tasks 上，第一个到达的 task 只是触发信号，run 把它 returnTask 扔回 tasks 再去建 worker，active 写满后才进入常规调度。留言区有人以为 Sleep 2 秒能让 run 先把 worker 建好，作者的答复是不会：run 不处理 task。〔TB35 p13〕〔TB35 p20〕
- 池满的两个分支：Schedule 先非阻塞尝试往 tasks 写，落到 default 再按 block 选项决定阻塞提交还是返回 ErrNoIdleWorkerInPool；demo2（不预建、不阻塞）里大量任务直接报 no idle worker in pool，这是调度不确定性的正常表现。这套实现是演示项目，不能直接用于生产。〔TB35 p14-17〕

### TB09 Web 服务实战的启动与组装

- 启动探测：`ListenAndServe` 会阻塞，必须放进独立 goroutine，再用 errChan 把运行状态传回主 goroutine；启动函数 select errChan 与 `time.After(1s)`，一秒内没等到错误就当启动成功。这是"启动失败要快速反馈"与"不能无限阻塞等待"之间的权衡。〔TB09 p16-17〕
- 教程里的存储工厂就是第七节"RWMutex 包 map"的实战样本：factory 包用 map[string]store.Store 加 sync.RWMutex 管理实例类型，Register 注册（nil 或重名直接 panic），New 按名取实例。〔TB09 p10-11〕
- 注册靠 init 自注册：internal/store 在 init 里调 `factory.Register("mem", &MemStore{...})`，main 只需空导入 `_ "bookstore/internal/store"`。坑：忘了这个空导入，注册从未发生，`factory.New("mem")` 到运行时才 panic unknown provider。〔TB09 p12〕〔TB09 p22〕
- 惯用函数原型：接受接口参数、返回具体类型（`NewBookStoreServer(addr string, s store.Store) *BookStoreServer`），让返回的具体类型组合传入接口的能力。〔TB09 p13-14〕
- main 包是主要模块初始化与组装的场所：`factory.New("mem")` 建存储实例，`NewBookStoreServer(":8080", s)` 建 HTTP 服务实例并把存储实例注入。〔TB09 p9〕
- 这一讲的起点是最小 HTTP 服务：`http.HandleFunc` 注册处理函数、`http.ListenAndServe` 启动监听，处理函数原型固定为 `func(w http.ResponseWriter, r *http.Request)`。〔TB09 p3-4〕
- middleware 是"接受 http.Handler 返回 http.Handler"的通用处理函数（Logging 记请求概要、Validating 校验 Content-Type 为 application/json），嵌套包裹后串到业务 handler 之前，横切逻辑不用在每个 handler 里重复实现。〔TB09 p14-16〕

### C:go_basic/concurrence 与 C:dachang/concurrence 的补充点

- 双 goroutine 池对照：Simple 版是共享 tasks chan 加 N 个 worker for-range，close 即停、WaitGroup 收尾。RoutinePool 版给每个 worker 配容量 1 的 tasks chan，Submit 轮转提交，可能轮到空 worker。再用 CAS 状态机防重复 run 和 close。〔C:dachang/concurrence/routine_pool.go:16-154〕
- 泄漏抓现行：`runtime.NumGoroutine()` 看趋势，挂上 net/http/pprof 后访问 /debug/pprof/goroutine 能看到每个泄漏 goroutine 卡在哪一行；现场是 1ms 超时等 5ms 的 rpc，子 goroutine 卡在无缓冲发送上，每请求泄一个。〔C:dachang/concurrence/main/routine_leaky.go:19-59〕
- `net.Conn` 的接口契约允许多个 goroutine 同时调用方法；其他连接或客户端类型的并发安全性应分别查阅其文档，不能一概推断。〔C:dachang/concurrence/concurrent_connection/client/tcp/main.go:1-65〕
- 信号量的额度必须归还：QpsLimit 每次处理先取令牌，处理完要在 defer 里还（`<-qps`），取还不对称，额度越漏越少。〔C:go_basic/concurrence/qps_limit.go:10-19〕
- 三级流水线的实盘形状：单 goroutine walkDir 遍历文件名入 fileList，15 个读 goroutine 逐行进 lineBuffer，5 个处理 goroutine atomic 求和；上游 close 后下游 for-range 退出，逐级 Wait+close 收敛；另起 Ticker 定时打印两级缓冲的堆积量，用它指导并发度调整。〔C:go_basic/concurrence/deal_file.go:79-124〕

省略未吸收：TB09 的 ServeMux 与 gorilla/mux 已被 Go 1.22 标准库的方法加路径模式取代，归 11 节。TB09 的面向接口 Store 设计归 04 节。TB35 的 go.mod replace 本地引用归 02 节。C 单元的自旋锁与文件并发写维持"想深入去哪查"里声明的边界。Gosched 与 Goexit 归 08 节调度，recover 只捕本 goroutine 05 与 06 节已讲，Cond 与 RWMutex 矩阵归 06 节。

## 想深入去哪查

- 原材料：TB35（goroutine 池的完整实现：创建、worker 管理、提交调度、功能选项）；TB09（优雅退出的 signal.Notify + Shutdown）；`C:dachang/concurrence`（超时四法、优雅退出、泄漏现场、手写 Once 与分段锁）；`C:go_basic/concurrence`（context、goroutine 池与限流）。
- 旧长版：`notes/p14-pipelines.md`（errgroup、SetLimit、生成器、扇出保序、并发上限消融实验）、`notes/p3-concurrency.md`。
- 不在这一节：自旋锁与 CAS 的内存序细节〔C:dachang/concurrence/spinllock.go:15-50〕、文件并发写的性能对比〔C:dachang/concurrence/file_test.go:11-44〕，遇到再查。
