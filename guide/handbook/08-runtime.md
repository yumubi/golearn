# 08 运行时直觉：调度、分配、逃逸与 GC

> 详细程度：标准（概念地图深度） · 约 25 分钟
> 材料：TB32、TBX10 · 旧长版：`notes/p31-concurrency-scheduler.md`、`notes/p32-memory-gc.md`
> 前置：03 类型与数据、06 并发一（goroutine 与 channel 的基本用法）

## 这一节要建立的认知

Go 的 runtime 和你的代码一起编译进同一个二进制，它负责三件事：**调度**（把 goroutine 摆到线程上）、**分配**（给对象找内存）、**回收**（找出垃圾）。〔TB01 p12-13〕

这一节只讲"是什么、解决什么、怎么验证"，不讲实现细节。日常写代码用得上的结论只有一条：**性能的主要杠杆是减少分配次数**，因为分配的成本一部分在分配器，大部分在后面的 GC。

| 问题 | 机制 | 怎么观察 |
|---|---|---|
| goroutine 怎么跑在线程上 | G-P-M 调度，抢占 | `GOMAXPROCS`、`go tool trace` |
| 对象放栈还是堆 | 编译期逃逸分析 | `go build -gcflags=-m` |
| 内存怎么分配 | 类似 tcmalloc 的多级缓存 | `pprof` 的 `alloc_space` |
| 垃圾怎么回收 | 并发三色标记，混合写屏障 | `GODEBUG=gctrace=1` |

## 一、调度：G、P、M

- **G** 是 goroutine，**M** 是操作系统线程，**P** 是逻辑处理器，持有本地运行队列。G 要先进入 P 的队列，P 与 M 绑定后才真正运行。〔TB32 p4〕〔TB32 p8〕
- 演进：最早只有 G 与 M，全局一把锁，伸缩性差（1.0）；加了 P 这个间接层并引入 work stealing（1.1）。〔TB32 p2-4〕现在只需要学 G-P-M。
- P 的数量由 `GOMAXPROCS` 决定，也就是最大并行度。
- 为什么便宜：初始栈 2KB，切换不陷入内核。〔TB31 p6〕

**抢占**是最容易讲错的一块。协作式抢占（1.2）靠函数入口埋点，纯计算循环里没有函数调用就抢占不了；1.14 起用信号做异步抢占。〔TB32 p5-6〕下面的程序在 `GOMAXPROCS(1)` 下起一个死循环，再让 `main` 睡 10 毫秒：

```go
runtime.GOMAXPROCS(1)
go func() { for { } }()
time.Sleep(10 * time.Millisecond)
fmt.Println("main still runs")
```

本机 go1.26.4 实测：正常运行，输出 `main still runs`；加上 `GODEBUG=asyncpreemptoff=1` 关掉异步抢占后，程序卡死，`timeout 5` 之后退出码是 124。

**阻塞时发生什么**，决定了你怎么理解"goroutine 很便宜"：

- channel 阻塞或网络 I/O：只挂起这个 G，M 去跑 P 的下一个 G，线程不阻塞（netpoller）。〔TB32 p10〕
- 系统调用阻塞：G 和 M 一起挂起，P 被交给别的 M；没有空闲 M 就新建线程。慢系统调用多了，线程数会涨。〔TB32 p11〕

监控线程 `sysmon` 不绑定 P，周期性做几件事：收回被系统调用卡住的 P、向运行超过 10ms 的 G 发抢占，超过 2 分钟没跑过 GC 就强制一次。〔TB32 p8-9〕〔TB32 p9-10〕

## 二、分配：多级缓存与 size class

小对象高频创建销毁，是多线程程序锁竞争的重灾区。Go 的分配器基本是 tcmalloc 的思路：每个 P 有本地缓存，不够再向全局要，再不够向操作系统要，层级像 CPU 缓存。〔TBX10 p7-9〕

- 对象按大小分三类：tiny（小于 16 字节且不含指针）、small（16 字节到 32KB）、large（大于 32KB，直接走页分配）。〔TBX10 p7-8〕
- 底层是 arena（向 OS 申请的大块，64MB 是 linux/amd64 的值，其他平台不同）切成 8KB 的 page，再按 size class 切成等大的块。size class 是预先定好的一组规格，本机 go1.26.4 共 68 档：`grep -n "NumSizeClasses" $(go env GOROOT)/src/internal/runtime/gc/sizeclasses.go` 得 `NumSizeClasses = 68`。〔TBX10 p9-11〕

这一层你不需要写代码，只需要知道一件事：**分配次数越少越快**。预分配容量、复用缓冲、少产生小对象，都是在回避这个成本。

## 三、逃逸分析：栈还是堆由编译器决定

Go 没有 `new` 和 `malloc` 的区别：对象放栈还是放堆，由编译期逃逸分析决定。栈上分配释放只是挪栈顶，堆上要走分配器并等 GC。〔TBX10 p3-4〕

```go
type point struct{ x, y int }

//go:noinline
func onHeap() *point { p := point{1, 2}; return &p }

//go:noinline
func big() int { s := make([]int, 10240); return len(s) }
```

```text
$ go build -gcflags=-m .
./main.go:15:2: moved to heap: p
./main.go:21:11: make([]int, 10240) escapes to heap
```

三条规律：返回局部变量地址，它会"逃"到堆；较大的对象即使没返回指针也上堆；赋给 `interface` 会装箱（见 04）。逃逸分析加 GC 极大解放了心智，但抽象不免费，Go 选择把成本放在运行期。〔TBX10 p3-4〕

## 四、GC：并发三色标记

Go 的 GC 选了**并发标记清扫**，**没有分代**。〔TBX10 p11〕〔TBX10 p32〕要理解它，先认三个参与者：mutator（你的应用，不停修改对象引用关系）、allocator、collector（负责找出还活着的对象）。〔TBX10 p5-6〕

**三色抽象**让 GC 能和应用同时跑：白色是还没扫到，灰色是已发现但子节点没扫完，黑色是自己和子节点都扫完。标记从全局数据和 goroutine 栈出发，广度优先遍历。〔TBX10 p15〕

并发带来的问题是**漏标**：GC 扫过 A 之后，应用把 A 指向一个新对象 B，GC 不知道，B 会被误回收。要堵住它，必须保证两条不变性之一：强三色（禁止黑指向白）或弱三色（黑可以指向白，但那个白必须能从灰到达）。〔TBX10 p21-24〕靠**写屏障**实现，它是编译器在每次指针修改前插入的一小段代码。〔TBX10 p24-26〕

栈上操作太频繁，不给栈开屏障；只对堆开屏障又要 STW 重扫栈。所以现行 Go 用**混合写屏障**，结合 Dijkstra 的插入屏障和 Yuasa 的删除屏障：指针断开时，旧对象和新对象都标灰。〔TBX10 p26-30〕

几个影响你写代码的结论：

1. **协助标记**：分配得太快、后台 worker 来不及时，分配内存的 goroutine 会被拉去帮忙标记，直接拉长请求延迟。优化方向只有一个：降低分配量。〔TBX10 p20-21〕
2. **STW 很短**：并发标记开始前和结束时各有一次，官方宣称亚毫秒，但高压系统仍可见毫秒级，值得监控 `runtime.MemStats.PauseNs`（原材料写作 pprof 的 `pauseNs`，实际出处是 `MemStats`）。〔TBX10 p14-15〕
3. **无分代**：上千万个 kv 的大 map 会让每轮标记都很重，这类场景要主动降低指针数量或避开 GC（如 `sync.Pool`、堆外）。〔TBX10 p32〕
4. **1.26 修正**：Green Tea GC 在本机 go1.26.4 默认开启，`grep -n GreenTeaGC $(go env GOROOT)/src/internal/buildcfg/exp.go` 输出 `GreenTeaGC: true,`。上面的三色与写屏障是通用概念模型，具体标记实现的细节不要当成稳定接口。

## 五、两种垃圾

**语法垃圾**是语法上已不可达的对象，GC 的主要收集目标。**语义垃圾**是语法上仍可达、但语义上已经没用的对象，GC 无能为力，这就是内存泄露。〔TBX10 p11-14〕典型场景是元素为指针的切片缩容：

```go
s = s[:1]   // 底层数组仍然持有后面元素的指针，它们不会被回收
// 正解：缩容前先置 nil
for i := 1; i < len(s); i++ { s[i] = nil }
s = s[:1]
```

Rust 的所有权让"不可达即析构"在编译期完成，不存在这类语法垃圾；Java 的静态集合缓存同样会产生语义垃圾，成因相同。

## 六、观察的三件套

```sh
go build -gcflags=-m .              # 逃逸决策
GODEBUG=gctrace=1 ./prog            # 每次 GC 一行
go tool pprof -alloc_space ./prof   # 谁在分配
```

`gctrace` 实测一行（持有约 70MB 存活堆时）：

```text
gc 5 @0.000s 7%: 0.006+0.062+0.001 ms clock, ..., 71->73->73 MB, 72 MB goal, ..., 10 P
```

`71->73->73 MB` 依次是 GC 开始时堆、结束时堆、存活堆；`72 MB goal` 是本轮的目标堆，由上一轮存活堆乘以 2 得来（默认 `GOGC=100` 的含义：堆长到上次存活量的两倍再触发下一轮），所以别拿它和本行的 73 直接比。`10 P` 是 `GOMAXPROCS`。（以上为实测与 `go doc runtime` 的 `GOGC` 说明，不出自原材料。）调高 `GOGC` 换内存省 CPU；要给容器设上限，用 `debug.SetMemoryLimit` 设软内存上限。

"降低分配"在基准里长这样（本机 go1.26.4，`go test -bench . -benchmem ./exercises/p32gc/`，数字随机器浮动，看量级和 `allocs/op`）：

```text
BenchmarkAppendNoCap  27663 ns/op  357574 B/op  16 allocs/op
BenchmarkMakeCap       6505 ns/op   81921 B/op   1 allocs/op
BenchmarkPool          2740 ns/op      24 B/op   1 allocs/op
```

先预分配容量把 16 次分配降到 1 次，再用 `sync.Pool` 复用缓冲，耗时降一个数量级。这套"先量再改"的顺序，见旧长版 `notes/p32-memory-gc.md` 的 pprof 四级优化，实例在 `C:dachang/micro_service/pprof`。〔C:dachang/micro_service/pprof/object_pool.go:13-76〕

## 常见坑

1. 以为 `go func(){ for {} }()` 会饿死其他 goroutine：1.14 之后不会；老教程里的现象已过时。〔TB32 p5-6〕
2. 把 goroutine 数当成线程数：慢系统调用才会让线程数增加。〔TB32 p11〕
3. 缩容切片不置 nil，指针元素泄漏。〔TBX10 p11-14〕
4. 在热路径上无意装箱或返回大量小对象，分配次数飙升。
5. 以为 `pprof` 的 CPU 火焰图能看出 GC 压力：要看 `gctrace`、堆曲线和 `alloc_space`。

## 判断题（先答再看）

1. `GOMAXPROCS=1` 时，一个无函数调用的死循环 goroutine 会不会让 `main` 永远得不到调度？为什么 1.14 前后答案不同？
2. 一个函数返回局部变量的地址，会不会悬垂？编译器怎么处理？
3. `71->73->73 MB, 72 MB goal` 这行里，哪个数字是存活堆？`goal` 是怎么算出来的，为什么它比存活堆还小？
4. 为什么写屏障不对栈开启？

## 在项目里

`dnsproxi-go/cache/reply.go` 的 `BufPool` 是 `sync.Pool`，在命中路径里复用 4KB 应答缓冲，回避每次命中分配；`dnsproxi-go/main.go` 的 `handleQuery` 命中分支 `Get`、写回、`Put`，同 goroutine 用完即还。这是第六节"降低分配"的真实落地，开关是配置里的 `HitPathPool`。

## 想深入去哪查

- 调度：TB32 全讲（含抢占与 sysmon）；GC 与分配：TBX10 全讲；goroutine 与 channel 的使用：TB31、TB33。
- 实验：`exercises/p32gc/`（逃逸矩阵、语义垃圾、三级分配基准）、`exercises/p31sched/`。
- 旧长版：`notes/p32-memory-gc.md`（含 `mallocgc` 源码锚点与 pprof 四级优化）、`notes/p31-concurrency-scheduler.md`。
- 不在这一节、遇到再查：`mcache/mcentral/mheap` 的字段、`itab`、`runtime2.go` 的 `g/m/p` 结构体。
