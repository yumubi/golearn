# 08 运行时直觉：调度、分配、逃逸与 GC

> 详细程度：标准（概念地图深度） · 约 25 分钟
> 材料：TB32、TBX10 · 旧长版：`notes/p31-concurrency-scheduler.md`、`notes/p32-memory-gc.md`
> 前置：03 类型与数据、06 并发一（goroutine 与 channel 的基本用法）

## 先看一个在 C 里必崩、在 Go 里没事的现象

C 语言里有条铁律：不能返回局部变量的地址。函数一返回，栈帧销毁，那个地址指向的内存就变成悬垂指针，轻则数据错乱，重则 segmentation fault。〔TBX10 p2〕下面这段 Go 代码做了同样的事，先猜猜会不会崩：

```go
package main

import "fmt"

type point struct{ x, y int }

//go:noinline
func newPoint() *point {
	p := point{1, 2}
	return &p // 返回局部变量的地址，C 里这是悬垂指针
}

func main() {
	q := newPoint()
	fmt.Println(q.x, q.y)
}
```

```text
1 2
```

程序不崩，还正确打印了 `1 2`。同样是"返回局部变量的地址"，为什么 Go 没事？因为 Go 的编译器做了逃逸分析：它发现 `p` 的地址会被函数外的人继续使用，于是**不放栈上，改放堆上**，由垃圾回收器（GC）决定它什么时候被回收。〔TBX10 p3-4〕

这一个小例子，背后是这一节要讲的三件事：**调度**（goroutine 怎么被摆到线程上跑）、**分配**（对象放栈还是堆、堆上怎么分）、**回收**（GC 怎么找出垃圾）。它们都由 Go 运行时（runtime）承担，而 runtime 和你的代码编译进同一个二进制，哪怕一个 hello world 也带着它。〔TB01 p12-13〕

这一节只讲"是什么、解决什么、怎么验证"，不讲实现细节。日常写代码用得上的结论只有一条：**性能的主要杠杆是减少分配次数**，因为分配的成本一部分在分配器，大部分在后面的 GC。

## 和你已知语言的差异

| 问题 | Java / Kotlin | Rust | TS | Go |
|---|---|---|---|---|
| 谁决定对象放栈还是堆 | 编译器与 JIT，程序员基本不直接控制 | 程序员：`Box` 进堆，其余按所有权 | V8 引擎决定 | 编译期逃逸分析决定 |
| 垃圾回收 | 分代 GC（G1/ZGC 等） | 无，所有权加借用检查 | 分代 GC（V8） | 并发三色标记，**无分代** |
| 悬垂指针 | 不可能（有 GC） | 编译期直接禁止 | 不可能（有 GC） | 逃逸分析加 GC 兜底 |
| 并发执行单元 | 线程、虚拟线程 | async task，需运行时 | event loop 加 worker | goroutine，G-P-M 调度 |
| 一个执行单元的成本 | 线程栈约 1MB | task 是无栈状态机，需要运行时 | 单线程 event loop | goroutine 初始栈 2KB |

表里最值得记的是最后两行：goroutine 初始栈只有 2KB、在用户态切换，所以能开成千上万个；但 Go 的 GC 没有分代，一个装满指针的大 map 会让每轮标记都变重。下面按"调度 → 分配 → 回收"的顺序展开。

## 一、调度：G、P、M

先想一个问题：Go 程序对操作系统来说只是普通用户进程，操作系统眼里只有线程，根本不知道 goroutine 存在。〔TB32 p2〕那成千上万个 goroutine 靠什么跑起来？答案是 Go 运行时自己的调度器，它把 goroutine 放到操作系统线程上执行，goroutine 要竞争的"CPU"资源就是这些线程。〔TB32 p2-3〕

调度器里有三个角色，各管一件事：〔TB32 p4〕〔TB32 p7〕

- **G**（goroutine）：存着执行栈、状态、要跑的函数。它是可重用的对象。〔TB32 p6-7〕
- **M**（machine）：操作系统线程，真正执行计算的资源。M 绑定 P 后进入调度循环：从队列取 G、切到 G 的栈上执行、G 跑完回到 M，如此反复。M 不保留 G 的状态，所以 G 能跨线程调度。〔TB32 p7〕
- **P**（logical processor）：逻辑处理器，持有本地运行队列。G 要先进入某个 P 的队列，P 与 M 绑定后才真正跑起来。对 G 来说，P 就是它的"CPU"；对调度器来说，真正的 CPU 是 M。〔TB32 p4〕

为什么要多出 P 这一层？最早 Go 1.0 只有 G 和 M，所有 goroutine 的创建、调度都要抢一把全局锁，伸缩性差。〔TB32 p3〕Go 1.1 加入 P 和 work stealing 算法：每个 P 有本地队列，M 优先从自己 P 的队列取 G，空了才去别的 P 偷。〔TB32 p4〕加一个间接层，把全局锁换成局部队列，这就是"计算机科学里任何问题都能通过加一个中间层解决"的又一个实例。〔TB32 p4〕

P 的数量由 `GOMAXPROCS` 决定，也就是最大并行度：同一时刻最多有这么多个 G 在真正并行执行。〔TB32 p3〕goroutine 便宜的原因也很直接：初始栈 2KB，上下文切换在用户态完成、不陷入内核。〔TB31 p6〕

### 抢占：死循环为什么饿不死别人

如果某个 G 跑起来就不停，其他 G 怎么办？这就是抢占要解决的问题。它的演进史最能说明"为什么不背旧结论"：〔TB32 p5-6〕

- Go 1.0：完全不支持抢占，一个死循环 G 会永远占住它的 P 和 M。
- Go 1.2：协作式抢占。编译器在每个函数入口埋一段检查代码，运行时有机会在那里决定要不要抢占。但它只在有函数调用的地方埋点，**纯计算死循环**里没有函数调用，照样抢不动。〔TB32 p5〕
- Go 1.14：基于信号的异步抢占。监控线程给运行过久的 G 发信号，不再依赖函数调用埋点。〔TB32 p5〕

下面的程序把 `GOMAXPROCS` 设成 1，起一个死循环 goroutine，再让 `main` 睡一会儿后打印：

```go
package main

import (
	"fmt"
	"runtime"
	"time"
)

func f() {}

func main() {
	runtime.GOMAXPROCS(1)
	go func() {
		for {
			f() // 1.14 之后，连这个函数调用都不需要了
		}
	}()
	time.Sleep(10 * time.Millisecond)
	fmt.Println("main still runs")
}
```

```text
main still runs
```

在 go1.26.4 上它能正常打印。〔实测〕`go run .`。反过来，关掉异步抢占再跑：`GODEBUG=asyncpreemptoff=1 go run .`，程序会卡死，因为死循环 G 拿走了唯一的 P 且抢不回来。所以老教程里"死循环饿死其他 goroutine"的现象已经过时。

### 阻塞时发生什么

goroutine 便宜，但它阻塞时分两种完全不同的情况，直接决定线程数会不会涨：〔TB32 p10-11〕

- **channel 或网络 I/O 阻塞**：只挂起这个 G，M 去跑 P 队列里的下一个 G，线程不阻塞。网络 I/O 由 netpoller 管理，所以大量网络连接不会造出大量线程。〔TB32 p5〕〔TB32 p10〕
- **系统调用阻塞**：G 和 M 一起挂起，P 被解绑、交给别的 M；没有空闲 M 就新建一个线程。所以慢系统调用多了，线程数会涨。〔TB32 p11〕

负责盯梢的是监控线程 `sysmon`，它不绑定 P，周期性做几件事：收回被系统调用卡住的 P、向运行超过 10ms 的 G 发抢占请求、超过 2 分钟没 GC 就强制一次。〔TB32 p8-9〕〔TB32 p9-10〕

## 二、分配：多级缓存与 size class

小对象高频创建销毁，是多线程程序锁竞争的重灾区。Go 的分配器基本是 tcmalloc 的思路：每个 P 有本地缓存，不够再向全局要，再不够向操作系统要，层级像 CPU 的多级缓存。〔TBX10 p7-8〕

对象按大小分三类：〔TBX10 p8〕

- **tiny**：小于 16 字节且不含指针。
- **small**：含指针，或者大小在 16 字节到 32KB 之间。
- **large**：大于 32KB，跳过缓存直接走页分配。

底层是 arena（向 OS 申请的大块，linux/amd64 上是 64MB，其他平台不同）切成 8KB 的 page，再按 size class 切成等大的块。size class 是预先定好的一组规格，本机 go1.26.4 的常量是 68（含第 0 档占位，实际可用 67 档规格）：〔实测〕`grep -n "NumSizeClasses" $(go env GOROOT)/src/internal/runtime/gc/sizeclasses.go` 输出 `NumSizeClasses = 68`。〔TBX10 p9-11〕

这一层不需要写代码，只要记住一个结论：**分配次数越少越快**。预分配容量、复用缓冲、少产生小对象，都是在回避这个成本。下一节"降低分配"会给出可量化的例子。

## 三、逃逸分析：栈还是堆由编译器决定

Go 没有 `new` 和 `malloc` 的区别，对象放栈还是堆，由编译期逃逸分析决定。栈上分配释放只是挪栈顶，堆上要走分配器并等 GC。〔TBX10 p4〕

用 `-m` 标志能直接看编译器做了哪些决定。下面三个函数，两个逃逸、一个不逃逸（行号来自把它们放进一个 `package main` 后 `go build -gcflags=-m .` 的实测输出）：

```go
type point struct{ x, y int }

//go:noinline
func onHeap() *point {
	p := point{1, 2}
	return &p // 返回地址：p 逃逸
}

//go:noinline
func staysOnStack() point {
	return point{3, 4} // 只返回值：不逃逸
}

//go:noinline
func big() int {
	s := make([]int, 10240)
	return len(s) // 太大：也逃逸
}
```

```text
./main.go:7:2: moved to heap: p
./main.go:18:11: make([]int, 10240) escapes to heap
```

两条输出对应两条规律：〔TBX10 p3-4〕

1. 返回局部变量地址，它就会"逃"到堆上，由 GC 接管。所以开头那个 C 里的悬垂问题在 Go 里根本不存在。
2. 较大的对象即使没返回指针也上堆。编译器对「隐式分配在栈上的单个对象」设了上限（`MaxImplicitStackVarSize`，本机源码里是 64KB），上面 `make([]int, 10240)` 是 80KB，超限所以上堆。

第三条规律是把值赋给 `interface` 可能触发装箱和逃逸（见 04）。逃逸分析加 GC 极大解放了心智，写代码时不必再手动操心分配释放。但"一切抽象皆有成本"，Go 选择把成本放在运行期，极端场景下 GC 本身会变成问题。〔TBX10 p4〕

## 四、GC：并发三色标记

Go 的 GC 选了**并发标记清扫**，而且**没有分代**。〔TBX10 p11〕〔TBX10 p32〕理解它，先认三个参与者：〔TBX10 p5-6〕

- **mutator**：你的应用，不停修改堆对象之间的引用关系。
- **allocator**：内存分配器，应用要内存就向它申请。
- **collector**：回收器，找出还活着的对象，其余的是垃圾。

**三色抽象**让 GC 能和应用同时跑：白色是还没扫到，灰色是已发现但子节点没扫完，黑色是自己和子节点都扫完。标记从全局数据和 goroutine 栈这些根出发，广度优先遍历整个对象图。〔TBX10 p15-17〕

并发带来的问题是**漏标**：GC 扫过 A 之后，应用把 A 指向新对象 B，GC 不知道，B 会被误回收。要堵住它，必须保证两条不变性之一：强三色（禁止黑指向白）或弱三色（黑可以指向白，但那个白必须能从灰到达）。〔TBX10 p21-24〕这靠**写屏障**实现，它是编译器在每次指针修改前插入的一小段代码。〔TBX10 p24-25〕

栈上对象操作极其频繁，给栈开屏障成本太高，所以 Go 不给栈开屏障；只对堆开屏障又需要 STW 重扫栈。于是现行 Go 用**混合写屏障**，把 Dijkstra 插入屏障和 Yuasa 删除屏障合起来：指针断开时，旧对象标灰，新对象在特定条件下也标灰（具体条件涉及栈的颜色，这里不展开）。〔TBX10 p26-30〕

几个影响写代码的结论：

1. **协助标记**：分配得太快、后台 mark worker 来不及时，分配内存的 goroutine 会被拉去帮忙标记，直接拉长请求延迟。优化方向只有一个：降低分配量。〔TBX10 p20-21〕
2. **STW 很短但不为零**：并发标记开始前和结束时各有一次短暂的 stop-the-world，官方宣称亚毫秒，但高压系统仍可见毫秒级，值得监控 `runtime.MemStats.PauseNs`（原材料写作 pprof 的 `pauseNs`，实际出处是 `MemStats`）。〔TBX10 p14-15〕
3. **无分代**：上千万个 kv 的大 map 会让每轮标记都很重，这类场景要主动降低指针数量或避开 GC（如 `sync.Pool`、堆外）。〔TBX10 p32〕
4. **1.26 修正**：Green Tea GC 在本机 go1.26.4 默认开启。〔1.26 修正〕`grep -n GreenTeaGC $(go env GOROOT)/src/internal/buildcfg/exp.go` 输出 `GreenTeaGC: true,`。上面的三色与写屏障是通用概念模型，具体标记实现的细节不要当成稳定接口。

## 五、两种垃圾

垃圾回收器不是万能的，垃圾分两种：〔TBX10 p11-14〕

- **语法垃圾**：语法上已不可达的对象，GC 的主要收集目标。
- **语义垃圾**：语法上仍可达、但语义上已经没用的对象，GC 无能为力，这就是内存泄露。

典型的语义垃圾是元素为指针的切片缩容。缩容只改了切片的长度，底层数组仍然持有后面那些指针，它们指向的对象不会被回收：

```go
package main

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

func main() {
	debug.SetGCPercent(-1) // 关掉自动 GC，让对象攒起来
	big := make([]*[10 * 1024]byte, 10000)
	for i := range big {
		big[i] = &[10 * 1024]byte{} // 每个指针指向 10KB 的对象
	}
	runtime.GC()
	var full runtime.MemStats
	runtime.ReadMemStats(&full)

	big = big[:1] // 缩容：底层数组仍持有另外 9999 个指针
	runtime.GC()
	var shrunk runtime.MemStats
	runtime.ReadMemStats(&shrunk)
	runtime.KeepAlive(big)

	fmt.Println("缩容后堆基本没变小:", shrunk.HeapAlloc > full.HeapAlloc/2)
}
```

```text
缩容后堆基本没变小: true
```

`big[:1]` 之后，那 9999 个指针还在底层数组里，各自指向的 10KB 依然可达，GC 不会收。修法是在缩容前先把不要的元素置 nil：`for i := 1; i < len(big); i++ { big[i] = nil }`。对照其他语言：Rust 的所有权让"不可达即析构"在编译期完成，不存在这类语法垃圾；Java 的静态集合缓存同样会产生语义垃圾，成因相同。〔TBX10 p12-13〕

## 六、观察三件套

调优不能靠猜，三个命令对应三件事。〔TBX10 p3〕

```sh
go build -gcflags=-m .              # 逃逸决策
GODEBUG=gctrace=1 ./prog            # 每次 GC 一行
go tool pprof -alloc_space ./prof   # 谁在分配
```

`gctrace` 每行格式长这样（本机 go1.26.4 实测，程序持有的存活堆一轮轮翻倍）：〔实测〕`GODEBUG=gctrace=1 go run .`

```text
gc 4 @0.000s 11%: 0.018+0.046+0.002 ms clock, 0.18+0.010/0.061/0.014+0.021 ms cpu, 36->36->36 MB, 36 MB goal, 0 MB stacks, 0 MB globals, 10 P
```

`36->36->36 MB` 依次是 GC 开始时堆、结束时堆、存活堆；`36 MB goal` 是本轮的目标堆，大致等于上一轮存活堆乘以 2：默认 `GOGC=100` 的含义是堆长到上次存活量的两倍左右再触发下一轮（1.18 起公式里还会加上栈和全局变量，所以只是近似），别拿它和本行的存活堆直接比。〔实测〕`go doc runtime` 的 `gctrace` 一节可查各字段含义。`10 P` 是 `GOMAXPROCS`。

"降低分配"在基准里长这样（本机 go1.26.4，`go test -bench . -benchmem ./exercises/p32gc/`，数字随机器浮动，看量级和 `allocs/op`）：

```text
BenchmarkAppendNoCap-10    	   53517	     22490 ns/op	  357571 B/op	      16 allocs/op
BenchmarkMakeCap-10        	  210051	      5911 ns/op	   81920 B/op	       1 allocs/op
BenchmarkPool-10           	  460200	      2439 ns/op	      24 B/op	       1 allocs/op
```

先预分配容量把 16 次分配降到 1 次，再用 `sync.Pool` 复用缓冲，耗时降一个数量级。这套"先量再改"的顺序，见旧长版 `notes/p32-memory-gc.md` 的 pprof 四级优化，实例在 `C:dachang/micro_service/pprof`。〔C:dachang/micro_service/pprof/object_pool.go:13-76〕

调高 `GOGC` 能换内存省 CPU；要给容器设内存上限，用 `debug.SetMemoryLimit` 设软上限。

## 小结

1. runtime 和你的代码编译进同一个二进制，负责调度、分配、回收三件事。〔TB01 p12-13〕
2. 调度是 G-P-M：G 先进 P 的本地队列，P 与 M 绑定才运行；P 的数量由 `GOMAXPROCS` 决定。〔TB32 p4〕
3. 抢占从协作式（1.2）演进到信号异步抢占（1.14），纯计算死循环也能被抢走。〔TB32 p5-6〕
4. channel 或网络阻塞只挂 G 不挂线程；慢系统调用会连 M 一起挂，导致线程数涨。〔TB32 p10-11〕
5. 分配走 tcmalloc 式的多级缓存，对象分 tiny/small/large 三类。〔TBX10 p7-8〕
6. 对象放栈还是堆由编译期逃逸分析决定，`go build -gcflags=-m` 可看。〔TBX10 p3-4〕
7. GC 是并发三色标记、无分代；漏标靠混合写屏障堵住。〔TBX10 p11〕〔TBX10 p26-30〕
8. 语义垃圾 GC 收不了，指针切片缩容要先置 nil。〔TBX10 p11-14〕

## 常见坑

1. 以为 `go func(){ for {} }()` 会饿死其他 goroutine：1.14 之后不会，老教程里的现象已过时。〔TB32 p5-6〕
2. 把 goroutine 数当成线程数：慢系统调用才会让线程数增加。〔TB32 p11〕
3. 缩容切片不置 nil，指针元素泄漏。〔TBX10 p11-14〕
4. 在热路径上无意装箱或返回大量小对象，分配次数飙升。
5. 以为 `pprof` 的 CPU 火焰图能看出 GC 压力：要看 `gctrace`、堆曲线和 `alloc_space`。

## 判断题与答案

先自己答，再看后面。

1. `GOMAXPROCS=1` 时，一个无函数调用的死循环 goroutine 会不会让 `main` 永远得不到调度？为什么 1.14 前后答案不同？
2. 一个函数返回局部变量的地址，会不会悬垂？编译器怎么处理？
3. `36->36->36 MB, 36 MB goal` 这行里，哪个数字是存活堆？`goal` 是怎么算出来的？
4. 为什么写屏障不对栈开启？

**答案**

1. 1.14 前会：协作式抢占只在函数入口埋点，死循环里没有函数调用就抢不动；1.14 起 sysmon 用信号做异步抢占，不再依赖埋点。
2. 不会。逃逸分析发现地址被外部引用，就把对象从栈挪到堆，交给 GC 管理。
3. 第三个 36 是存活堆。`goal` 大致是上一轮存活堆乘以 2（默认 `GOGC=100`，1.18 起还会算上栈和全局变量，所以只是近似）。
4. 栈上指针操作极其频繁，开屏障成本太高；只对堆开屏障又要 STW 重扫栈，所以用混合写屏障绕开。

## 在项目里

`dnsproxi-go/cache/reply.go` 的 `BufPool` 是 `sync.Pool`，在命中路径里复用 4KB 应答缓冲，回避每次命中分配；`dnsproxi-go/main.go` 的 `handleQuery` 命中分支 `Get`、写回、`Put`，同 goroutine 用完即还。这是第六节"降低分配"的真实落地，开关是配置里的 `HitPathPool`。

## 想深入去哪查

- 调度：TB32 全讲（含抢占与 sysmon）；GC 与分配：TBX10 全讲；goroutine 与 channel 的使用：TB31、TB33。
- 实验：`exercises/p32gc/`（逃逸矩阵、语义垃圾、三级分配基准）、`exercises/p31sched/`。
- 旧长版：`notes/p32-memory-gc.md`（含 `mallocgc` 源码锚点与 pprof 四级优化）、`notes/p31-concurrency-scheduler.md`。
- 不在这一节、遇到再查：`mcache/mcentral/mheap` 的字段、`itab`、`runtime2.go` 的 `g/m/p` 结构体。
