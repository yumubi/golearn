# P32 · 内存与 GC

> 这章学完你能干什么：用 `-gcflags="-m"` 读逃逸分析输出，说清一个对象落在栈还是堆；用 tcmalloc 四级路径（mcache、mcentral、mheap、arena）解释一次分配走了哪几层缓存；讲清三色标记为什么能与应用并发、混合写屏障堵住了哪个漏标窗口；分清语义垃圾与语法垃圾并给出 slice 缩容置 nil 的正解；最后用 pprof 四级优化实例（72070 到 9625 ns/op）把"降分配"变成手上功夫。前置：P30（接口与装箱）。

## 为什么需要 GC：从悬垂指针说起

C 的经典规则"不能返回局部变量"：栈帧销毁后引用它的指针指向已失效内存，轻则数据错乱，重则 segmentation fault。堆上对象引用关系更复杂，正确 free 一个对象前得先画完整个对象图，确认再无人指向它。靠人处理这种复杂度不科学也不合理，C/C++ 程序员被折磨数十年后，后续语言选择把这件事交给运行时。〔TBX10 p1-3〕

GC 语言的办法是集成逃逸分析：编译期自动判断对象该分配在栈还是堆。栈上对象管理成本低（挪动栈顶指针即完成分配与释放），堆上对象要经历层层申请，且死后等 GC 收尸。较大对象（如 10240 个元素的切片）即使不返回指针也放堆。〔TBX10 p3-4〕 实测：

```sh
$ go build -gcflags="-m" escape.go
./escape.go:13:2: moved to heap: p
./escape.go:18:13: make([]int, 10240) escapes to heap
```

onStack 里的局部 `point` 没有出现在输出里（不逃逸）；onHeap 返回 `&p`，编译器报 `moved to heap: p`；`make([]int, 10240)` 的大对象直接 `escapes to heap`。程序照常输出 `3 &{1 2}`。逃逸分析加 GC 极大解放心智，但一切抽象皆有成本，GC 把成本选在运行期。〔TBX10 p3-4〕

GC 不是免费的。网友实测：内存缓存上亿 kv 时 GC 消耗的 CPU 可达 90%+，简单粗暴地缓存对象会让 GC 成为 CPU 杀手〔TBX10 p4-5〕；正确分析这类问题，得先理解 GC 的实现机制。本章顺序：分配器、三色标记与写屏障、垃圾的两副面孔、pprof 四级优化。

三个参与者协作完成内存管理：mutator 是应用本身，不停修改堆上对象图的指向；allocator 是分配器，维护分配数据结构、多线程场景下降锁冲突；collector 是回收器，扫描存活对象，没扫到的就是垃圾。编译器把 newobject 调用翻译给 allocator，allocator 先问本地缓存，不够再向 OS 要；collector 扫完后用 madvise 把闲置内存归还 OS。〔TBX10 p5-6〕

Rust 对照：所有权加 RAII 把"回收"搬到编译期，borrow checker 保证悬垂指针编译不过，Arc 循环引用要手工 Weak 破环。Java 是分代 GC 加读写屏障或染色指针。Go 选无分代加纯写屏障，参数面收敛到 GOGC 与 GOMEMLIMIT 两个。

预测：`escape.go` 里 onStack 的局部 `point` 为什么没出现在 `-gcflags="-m"` 输出里？把它改成返回 `&point{1, 2}` 后输出会多出哪一行？先猜再跑。

## 分配器：tcmalloc 的 1:1 搬运

小对象高频创建销毁是多线程程序的锁竞争重灾区，C/C++ 的解法是 Google 的 tcmalloc：线程本地缓存加多级回收。Go 分配器基本是 tcmalloc 的 1:1 搬运（同为 Google 项目）。按尺寸分类：tiny（小于 16B 且无指针）、small（16B 到 32KB）、large（大于 32KB）。〔TBX10 p7-8〕

分配路径像 CPU 缓存 hierarchy，四级由近到远：

1. `mcache.tiny`：每 P 一份的 tiny 对象缓存，无锁（L1）。
2. `mcache.alloc`：按 sizeclass 分桶的 mspan 数组（L2）。
3. `mheap.mcentral`：按 spanClass 全局汇总，带锁（L3）。
4. `mheap.arenas`：页分配器 pageAlloc 按页派发（L4），不够就向 OS 申请。

large 对象跳过前两级直接走页分配器。页分配器从 freelist 到 treap 再到 radix 树，复杂度从 O(N) 到 O(logn) 再到 O(1)，常数时间判定能否满足请求，不满足就切 arena 或向 OS 要。〔TBX10 p8-9〕

分配数据结构自底向上：arena 是向 OS 申请的最小单位（64MB，地址空间整体稀疏）；arena 切 8KB 的 page；一个或多个 page 拼成 mspan；mspan 按 sizeclass 划分成等大 element；mspan 分 scan/noscan 两类（内含指针与否）；mspan 上的 allocBits 是占用位图。本机 1.26 的 sizeclasses 表 68 个规格（`internal/runtime/gc/sizeclasses.go`），8B 到 32768B。〔TBX10 p9-11〕

分配入口都在 `runtime/malloc.go` 的 mallocgc（本机 go1.26.4 L1067），tiny 走 mallocgcTiny（L1202），small 无指针对象走 mallocgcSmallNoscan（L1358）。实测三级差距（M4，go1.26.4，`go test -bench . -benchmem ./exercises/p32gc/`）：

```text
BenchmarkAppendNoCap-10  42202 ns/op  357630 B/op  19 allocs/op
BenchmarkMakeCap-10       7024 ns/op   81921 B/op   1 allocs/op
BenchmarkPool-10          2748 ns/op      24 B/op   1 allocs/op
```

零容量 append 万次要 19 次分配（每次扩容翻倍复制），一次定容只要 1 次，sync.Pool 复用连这次分配也省了，只剩填数。这是"pprof 四级优化"一节的骨架。

Rust 对照：默认走系统 malloc，可插拔 jemalloc/mimalloc，多级缓存思想相同；Java 的 TLAB 是 tcmalloc 思想在 JVM 内的实现。

## 三色标记与写屏障

算法选型是并发标记清扫：标记阶段从根出发标出所有存活对象，清扫阶段回收没标的。它解决不了内存碎片，而 tcmalloc 恰好缓解碎片，两者相得益彰（但 tcmalloc 非零碎片，sizeclasses 表里每档都有 max waste 列）。〔TBX10 p11〕

GC 与应用并发的关键抽象是三色：黑，扫描完且子节点处理完（gcmarkbits=1 且不在队列）；灰，自己扫完子节点还没推完（gcmarkbits=1 且在队列）；白，还没扫到。标记从 `.bss`、`.data` 段与各 goroutine 栈出发做广度优先遍历，mark worker 从 gcw 队列弹对象、推子对象，队列满了向全局转移；交叉引用靠 isMarked 判断剪枝，并发安全用 atomic.Or8。〔TBX10 p15〕 三色抽象的价值是把扫描拆成多阶段与应用并发跑。

并发修改引用关系会漏标：GC 扫过 A 之后应用把 A 的指针改指向新对象 B，若 GC 感知不到，B 会被误回收。堵漏靠两条不变性：强三色，禁止黑色指向白色；弱三色，黑色可指向白色但该白必须能从灰可达。满足任一就不丢对象，实现靠写屏障。〔TBX10 p21-24〕

写屏障的本质是编译器在每次指针修改前插入的一小段代码，与并发编程的 memory barrier（内存序指令）无关；Go 只有写屏障没有读屏障。经典两种：Dijkstra 插入屏障（修改时把新指向的对象标灰）、Yuasa 删除屏障（修改前把旧指向的对象标灰）。〔TBX10 p24-26〕

栈上操作太频繁，不给栈开屏障；只对堆开屏障则单独任何一种都需要 stw 重扫全部栈（goroutine 多时 stw 极长）。所以现行 Go 用混合屏障 = Dijkstra + Yuasa：指针断开时把老对象、新对象都标灰，成对指针推入每 P 的 wbBuf，满了刷进 gcw。〔TBX10 p26-30〕

实测观测面：`GODEBUG=gctrace=1` 每行打印一次 GC 的时段、暂停、堆曲线。gogc.go 持有 100MB 存活堆时（`GOMAXPROCS=4 go run gogc.go`）：

```text
gc 5 @0.001s 7%: 0.015+0.059+0.008 ms clock, ..., 71->73->73 MB, 72 MB goal, ..., 10 P
HeapAlloc=100MB NumGC=5 PauseTotalNs=945081
```

`71->73->73 MB, 72 MB goal`：GC 结束时存活 73MB，goal 72MB 约是存活堆的两倍，这就是 GOGC=100 的语义（下次堆翻倍再触发）。GOGC=200 时 gctrace 可见 goal 从 8MB 到 79MB 的翻倍链（8→24→71→79）。官方宣称 stw 亚毫秒。〔TBX10 p14-15〕 实测：4 个 goroutine 疯狂分配 1MB 块压 2 秒后 `debug.ReadGCStats` 采 PauseQuantiles（两轮）：GC 12786 次，min 22us、q50 34us、max 156us；12616 次，21/37/150us。〔实测〕 复现：goroutine 循环 `_ = make([]byte, 1<<20)`，结束调 `debug.ReadGCStats`（PauseQuantiles 设 5 档）。亚毫秒在本机成立，但高压系统仍应以 pprof pauseNs 采进监控，别当恒真。

观察：同一程序加 `GOGC=200` 再跑，对比 goal 链从"约 2 倍存活堆"变成"约 3 倍"；gctrace 行末 `10 P` 是 GOMAXPROCS。

分配太快后台 worker 来不及标记时，应用 goroutine 会被拉去协助标记（mark assist），火焰图可见协助调用栈，直接吃掉响应延迟；内部是记账还账系统。〔TBX10 p20-21〕 优化方向只有降分配数量，这正是"pprof 四级优化"一节的主题。

Java 对照：G1 用 SATB（类 Yuasa 删除屏障），CMS 用增量更新（类 Dijkstra 插入屏障），ZGC 走读屏障加染色指针；Go 明确拒绝读屏障。

## 垃圾的两副面孔与回收流程

语法垃圾：语法上不可达的对象，GC 的主要收集目标。语义垃圾：语法上可达但语义上是垃圾，GC 无能为力，这就是内存泄露。〔TBX10 p11-14〕 典型：指针元素 slice 缩容，底层数组还攥着尾部元素。leak.go 双例（`GODEBUG=gctrace=1 go run leak.go` 实拍 12+ 轮 `3->3->1 MB, 4 MB goal`）：

```go
s = s[:1]      // 错：底层数组仍持有 &big{}，4KB 可达
t[1] = nil     // 正解：先断引用
t = t[:1]
```

`s 缩容后 len=1，但底层数组仍持有 &big{}（4KB 可达）；t 先置 nil 再缩容，尾部可回收`（leak.go 实跑输出）。同病不同方：Java 的静态集合缓存、Rust 的 Arc 环都是语义垃圾；语法垃圾只有 GC 语言有，Rust 的不可达即析构在编译期完成。〔C:go_basic/concurrence/collection_safety.go:6-11〕

改造：给你项目里一个 `xs = xs[:n]` 缩容点补上 `for i := n; i < len(xs); i++ { xs[i] = nil }`（或 `clear(xs[n:])`，1.21+），重跑 gctrace 看存活堆差值。

回收是两个专用 goroutine 的活：sweep.g 清扫死对象合并空闲页，scvg.g 向 OS 归还内存（dlv 里见 GC sweep wait / GC scavenge wait）。〔TBX10 p30-32〕
标记一结束 sweep.g 被唤醒，循环 sweepone：把 gcmarkbits 位图替换 allocBits 完成代际交替，按槽位去向归类 span；随后 scvg 线性执行 bgscavenge 到 madvise 归还 OS。源码锚点（go1.26.4）：mgcsweep.go bgsweep(L272)、sweepone(L359)；mgcscavenge.go bgscavenge(L649)。

全章结论四要点：Go GC 无分代、与应用并发、协助标记、并发时开写屏障。设计推论：无分代意味着上千万 kv 的特征系统要主动降扫描成本（sync.Pool 到 offheap）；GC CPU 超 25% 会触发大量协助伤延迟；简单场景 sync.Pool 就够，复杂场景才考虑 dgraph 这类欺骗 GC 的 offheap 方案。〔TBX10 p32〕 Go 参数极少（GOGC/GOMEMLIMIT）是在逼你降分配而不是调参。

## pprof 四级优化：从 72070 到 9625 ns/op

C:dachang/micro_service 的 Search 服务（召回加过滤）给了完整案例。在线 pprof 三步：`import _ "net/http/pprof"`、`go http.ListenAndServe("127.0.0.1:5678", nil)`、浏览器开 `/debug/pprof` 下载 profile，然后 `go tool pprof` 进 top/peek/list。离线：`go test -cpuprofile` 加 `-memprofile` 采文件。〔C:dachang/micro_service/pprof/object_pool.go:13-76〕

四级优化从零容量 append 开始逐级消除分配：

```text
切片初始容量为0        72070 ns/op  357875 B/op  24 allocs/op   (作者 Win11)
切片初始容量为10000    25587 ns/op   82168 B/op   6 allocs/op
使用全局容量10000切片  12454 ns/op     248 B/op   5 allocs/op
使用对象池              9625 ns/op     272 B/op   6 allocs/op
```

本机复测（M4，`go test -bench . -benchmem ./micro_service/pprof/`）：BenchmarkSearch 30170 ns/op 357765 B/op 18 allocs，与作者注释同量级（本机更快）。练习包 exercises/p32gc 用同构四级做了对照组：AppendNoCap 357573 B/op 16 allocs、MakeCap 81920 B/op 1 alloc、Pool 24 B/op 1 alloc。规律一致：定容砍掉 18 次扩容分配，池化把每次调用的新分配清零。

方法论对照：Java 的 JFR/async-profiler、Rust 的 cargo-flamegraph 与 pprof 同位；但 sync.Pool 这种"借还"池化是 Go 生态的惯用解，Java 常用对象池框架，Rust 更倾向零拷贝设计从根上避开。

这章学完你能干什么落地版：`-gcflags="-m"` 判断逃逸；gctrace 行读出 goal 与存活堆；语义垃圾找到就置 nil；基准里 allocs/op 高就先定容再池化。GC 的调优文化是"降分配"：Go 参数极少，Rust 无参可调，Java 才有分代参数体系。

## 小结

- 逃逸分析决定栈堆，`moved to heap` 与 `escapes to heap` 是两条实拍证据；大对象必上堆。〔TBX10 p3-4〕
- 分配器 tcmalloc 1:1：tiny/small/large 三类，mcache 到 mheap 四级路径，页分配器 radix O(1)。〔TBX10 p7-11〕
- 三色标记让 GC 与应用并发；漏标靠三色不变性堵，混合写屏障（Dijkstra+Yuasa）栈不开屏障。〔TBX10 p15〕〔TBX10 p21-30〕
- 语义垃圾 GC 无能为力：slice 缩容先置 nil；语法垃圾才是 GC 的活。〔TBX10 p11-14〕
- 四级优化：定容与池化把 allocs/op 从 24 砍到 6；本机 30170 ns/op 复测同量级。〔C:dachang/micro_service/pprof/object_pool.go:13-76〕

## 在 dnsproxi 里看这一章

dnsproxi 的命中路径就是本章 pool 一课的实战落点（行号工作副本实读）：

- cache/reply.go:49 `BufPool = sync.Pool{New: func() any { b := make([]byte, 4096); return &b }}`：4KB 应答缓冲池，命中路径零分配（labs 实验 4 的 B 形态）。
- main.go:283-293 `handleQuery` 命中分支：`BufPool.Get().(*[]byte)` 取缓冲、`e.ReplyPool(query, *bufp)` 写入、`pc.WriteTo` 发完才 `BufPool.Put(bufp)`。注释点明并发契约：同 goroutine 用完即还天然安全，跨 goroutine 复用会 TTL 撕裂，这正是 sync.Pool 的 per-P 本地缓存语义在应用层的镜像。
- 语义垃圾现成反面：cache.Cache 的 `m map[Key]*Entry`（cache/cache.go:54）配 `Expired()` 惰性判定，过期条目在下一次 ExpireRound 抽样才被摘；`Ghosts()`（cache/expire.go）数的就是"还占着格子的尸体"，对应"语法上可达但语义上是垃圾"。

## Take-away

预测：把 `make([]int, 10240)` 挪进函数返回值里，逃逸分析输出会怎么变？先猜再跑。观察：对你手头服务跑 `GODEBUG=gctrace=1`，找一条 GC 行读出存活堆与 goal。改造：找一个 allocs/op 大于 10 的基准，先定容后池化，重测三级数据。

## 章末参考

- 命令速查：`go build -gcflags="-m"`（逃逸）、`GODEBUG=gctrace=1`（GC 行）、`GOGC=200`（翻倍率）、`GOMEMLIMIT=64MiB`（软上限）、`go tool pprof`（top/peek/list）。
- 源码（本机 go1.26.4）：malloc.go mallocgc(L1067)/mallocgcTiny(L1202)/mallocgcSmallNoscan(L1358)；internal/runtime/gc/sizeclasses.go（68 档）；mgcmark.go gcAssistAlloc(L499)；mbarrier.go writeBarrier。
- sweep/scavenge：mgcsweep.go bgsweep(L272)/sweepone(L359)；mgcscavenge.go bgscavenge(L649)。
- 延伸：GC Handbook；memorymanagement.org；Green Tea GC（重排标记工作提升局部性）1.26 起默认开启〔1.26 修正〕：`grep -n GreenTeaGC $(go env GOROOT)/src/internal/buildcfg/exp.go` 输出 `88: GreenTeaGC: true,`。
- 旧章交叉：p30 §接口的运行时：eface、iface 与装箱（interface 装箱分配）；-benchmem 与 b.Loop 属 p8 §benchmark 与 pprof（p21 旧章无 bench 内容）。
- 原材料单元：TBX10（GC 全景 17 讲）、C:dachang/micro_service#K11（pprof 四级优化实例）。

## 练习：p32gc

先跑全绿：

```sh
$ go test -race -count=1 ./exercises/p32gc/
ok  	golearn/exercises/p32gc	1.3s
```

7 个条目各考一件事：

- `TestEscapeMatrix`：为什么需要 GC 一节的四例（栈、堆指针、大对象、接口装箱），断言行为，`go build -gcflags="-m"` 看编译器判定。
- `TestSemanticGarbage`：垃圾的两副面孔一节的置 nil 前后对照（错误写法与正解各一段）。
- `TestGCTriggerGoal`：三色标记与写屏障一节的 MemStats 一致性（HeapAlloc、NumGC 非零），gctrace 行见正文。
- `TestPPROFDemo`：pprof 四级优化一节的四级函数等价性（recall1 到 recall4 输出全等）。
- `BenchmarkAppendNoCap`/`BenchmarkMakeCap`/`BenchmarkPool`：三级分配对照，数字见文件头注释（本机 42202/7024/2748 ns/op 一轮实拍，另一轮 32410/9165/3030，波动正常看量级与 allocs/op）。
- pprof_demo.go 的 recall3 用全局切片复用：非并发安全，仅对照演示，并发版用 recall4 的 sync.Pool。
- 在线 pprof：`go run` 任一长驻程序加 `import _ "net/http/pprof"`，浏览器开 127.0.0.1:5678/debug/pprof（micro_service 四级优化实例同款）。〔C:dachang/micro_service/pprof/object_pool.go:13-76〕
