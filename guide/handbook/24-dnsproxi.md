# 24 项目线一：dnsproxi-go 从零读懂

> 详细程度：项目 · 约 30 分钟
> 材料：`dnsproxi-go/` 全部源码、`dnsproxi-go/LESSON.md`、旧章 `notes/p6-capstone-dnsproxi.md`、`notes/p17-dnsproxi-metrics.md`、`notes/p18-dnsproxi-slog.md`、`notes/p34-tcp-server-dns-over-tcp.md`
> 前置：04 到 08 节读过一遍；这一节反过来用，是把前面的范式在一个真实项目里找出来
> 说明：本节的请求路径、缓存行为都用 HEAD 版本的代码在本机跑过；压测数据引自 `LESSON.md`，没有复测

## 这一节要建立的认知

dnsproxi-go 是一个只有约 5300 行（含测试）的 DNS 代理：收下客户端的 UDP 或 TCP 查询，必要时命中缓存，否则转发给上游，再把应答带回去。它小到能一次读完，又包含了后端服务的大部分常规问题：并发模型、缓存与逐出、准入、过期、背压、可观测、协议成帧。**读它的正确方式不是逐文件，而是顺着一个请求走一遍，再看每个模块为什么要存在。**

## 一、请求怎么流动

```text
UDP 包 ─→ Proxy.Run 读循环 ─┬─ 每包一个 goroutine
                            └─ 或固定 worker 池（DNS_WORKER_POOL）
TCP 连接 ─→ tcp.Serve ─→ 每连接一个 goroutine ─→ 成帧读 ─→ streamConn
                                  │
                                  ▼
                           Proxy.handleQuery
        短包丢弃 → 限流(令牌不够直接丢) → sinkhole 命中则本地 NXDOMAIN → 缓存查找
        命中: 改写 ID 与 TTL 后直接回
        未命中: 熔断门(开放态直接丢) → 临时 UDP socket 转发上游(2s deadline)
                → 按成败回填熔断器 → 判断能否入缓存 → 回客户端
```

限流和熔断来自 `guard/` 包，由 `DNS_RATE_LIMIT`（如 `100,200`）和 `DNS_BREAKER_THRESHOLD`（失败率百分比）开启，默认全关，所以上面的缓存实验不受影响。被拒的查询都不回包，只在 `dnsproxi_queries_total` 里记成 `outcome="rate_limited"` 或 `"breaker_open"`，由客户端超时重试。限流在缓存之前，所以命中缓存的查询也要花令牌；熔断在缓存之后，缓存命中的查询不受上游故障影响。我在本机用同一份 HEAD 二进制各试了一遍：限流设 `2,2`，一批并发查询里放行 2 个、其余记 `rate_limited`；上游端口关闭、阈值 50 时，约 20 个失败后熔断打开，后续查询记 `breaker_open`。

关键事实：**UDP 与 TCP 共用同一个 `handleQuery`**。TCP 侧只是把一条连接包装成 `net.PacketConn`（`tcp.streamConn`），`WriteTo` 时加两字节长度前缀。业务逻辑一份，传输方式两种，这就是 04 节讲的"接口参数是关节"。

我用 HEAD 版本编译了二进制，写了一个 20 行的假上游（对任何查询回一条 A 记录，并计数），起了代理（缓存开、UDP 与 TCP 同端口），用 `dig +noedns +noadflag` 发请求，结果：

```text
UDP 第 1 次   → 1.2.3.4   （缓存未命中，上游答了 1 次）
UDP 第 2 次   → 1.2.3.4   （命中，上游没再被问）
TCP           → 1.2.3.4   （同一份缓存，命中）
dig 默认(带 AD 标志) → 1.2.3.4  （转发，没有命中）
dnsproxi_queries_total{outcome="hit"}       2
dnsproxi_queries_total{outcome="miss"}      1
dnsproxi_queries_total{outcome="forwarded"} 2
dnsproxi_cache_entries                      1
```

最后一行是个值得记住的现象：**`dig` 默认发出的查询带 AD 标志位，而缓存早期只接受"RD=1，其余标志全 0，没有 EDNS，单问题，没有附加记录"的纯净查询**（`cache.Request` 的判据）。所以真实的 stub resolver 发来的包大多不会命中这个缓存。这已经修了：判据放宽为 `flags2&^0x20`（只豁免 AD 位，RA/rcode 仍拒），`dig` 默认查询现在能命中（见下文"动手"）。

## 二、模块分工

| 位置 | 做什么 | 手册对应 |
|---|---|---|
| `wire/` | DNS 报文头与域名的手写解析（含压缩指针、循环检测） | 03 类型与数据 |
| `cache/cache.go`、`reply.go` | 键为（小写名字, 类型）的缓存；命中时改写 ID 并按已过去的秒数原地递减各条 TTL | 06、13 |
| `cache/evict.go` | 容量上限与采样近似 LRU 逐出 | 13 |
| `cache/sketch.go` | TinyLFU 准入的频率草图 | 13、06 |
| `cache/expire.go` | 后台主动清理过期条目 | 13、07 |
| `tcp/` | 两字节成帧、`streamConn` 适配、截断位 TC | 04 |
| `logger.go` | 带请求 ID 的结构化日志 | 15 |
| `metrics.go` | RED 指标与缓存指标 | 15 |
| `dnsq/` | 自写压测器 | 无 |

## 三、几个值得讲清楚的设计取舍

### 1. 命中为什么要写锁

`Cache.Get` 用 `mu.Lock()` 而不是 `RLock()`：命中时要更新"最后访问时间"（近似 LRU 的排序依据），读变成了写，读多写少的优势打了折，代码注释直说了这个代价。严格 LRU 本来就要求每次读都改顺序，没有免费午餐。

### 2. 逐出：几张索引表必须一起动

缓存用 `keys` 切片加三张 map（`slot` 反查下标、`m` 存数据、`access` 记最后访问时间）支持 O(1) 的"随机抽样加交换删除"：`removeSlot` 把最后一个元素搬进被删的洞，再修正被搬动者的 `slot`。代码里记录了第一版的失误：漏删了 `m`，`len(keys)=3` 而 `len(m)=4`，容量上限静默失效，被测试抓住。**多张索引表要同步，是缓存类代码最常见的缺陷。**

采样近似 LRU 是随机抽 K 个、淘汰其中最冷的（Floyd 算法保证不重复）。注释提醒：`sampleIndices` 只能调一次，调两次样本不同，下标全乱。

### 3. 准入：缓存满了，新来的凭什么挤掉在位者

TinyLFU 的思路：满仓时，比较"新来者的访问频率"和"将被淘汰者的频率"，新来者不够热就不让进。频率不能精确记，用 count-min 草图：4 行，每行很多个 4 bit 计数器，16 个打包进一个 `uint64`；估计值取各行最小值，会高估，但不会低估。计数饱和于 15，累计到阈值后整体右移一位减半，这就是"老化"，否则历史热点会永远占着位置。（依据：`cache/sketch.go` 开头注释）

判决函数很短：

```go
func Admit(candidate, victim uint32, roll uint64) bool {
	if candidate > victim {
		return true
	}
	if candidate >= AdmitHashdosThld { // 平局或略输，但自身够热
		return roll&127 == 0 // 1/128 概率放行
	}
	return false
}
```

那 1/128 的随机放行是防哈希碰撞攻击：否则攻击者把在位者的估计值抬高，整个缓存就再也进不去新条目了。另有一条规则：被淘汰者如果已经过期（"尸体"），不比频率直接让位，不能让死人靠生前的热度守门。

### 4. 主动过期：照搬 Redis

读路径遇到过期条目当未命中，但条目还占着位置（`Ghosts()` 统计这类"尸体"）。`ExpireCycle` 照搬 Redis：每秒醒 10 次，每轮随机抽 20 个，当场判断当场删，样本里过期比例达到 10% 就再来一轮，直到耗尽 1 毫秒预算。两个要点：

- **锁的边界等于轮的边界**：每轮结束就放写锁，让排队的查询插进来。让出的是锁不是时间片，这是延迟设计。
- **一次只抽一个，不能先攒一批下标再逐个删**：交换删除会让记下的下标全指向别人，不会 panic，只是静默删掉活条目，命中率莫名下降。

### 5. 并发模型与背压

默认是"每包一个 goroutine"。`DNS_WORKER_POOL=N` 切换成固定 N 个 worker 从带缓冲的 channel 取包：缓冲满了，读循环在 `qch <- packet` 处阻塞，这就是背压，在飞查询数 `dnsproxi_in_flight_queries` 会顶到 N。每个 goroutine 入口都有 `recover`，因为一个 goroutine 的 panic 会让整个进程退出。UDP 读循环里每个查询要拷贝一份 `buf`（`append([]byte{}, buf[:n]...)`），因为读缓冲会被下一个包复用，不拷就是数据竞争。

### 6. TC 回退

UDP 应答超过上限时，`tcp.TruncConn` 只保留头与问题段、把应答计数清零、置 TC 位；客户端见到 TC 会改用 TCP 重发。这条链路与 `streamConn` 配合，细节见旧章 `notes/p34-tcp-server-dns-over-tcp.md`。

## 四、范式落地速查

| 手册范式 | 在 dnsproxi-go 的位置（均为真实文件与函数） |
|---|---|
| 隐式接口、适配器 | `tcp.streamConn` 实现 `net.PacketConn`；`tcp.Handler` 与 `handleQuery` 签名一致，直接传方法值 |
| 包装器 | `logger.go` 的 `ctxHandler` 包装 `slog.Handler`，`Handle` 前补请求 ID |
| 错误分类 | `metrics.go` 的 `upstreamErrReason` 把上游错误归为 timeout、network、refused 三类低基数标签 |
| 低基数标签纪律 | 查询指标的标签只有 `qtype` 与 `outcome`（上游错误指标只有 `reason`）；名字和客户端地址是高基数，只进日志 |
| 并发安全 | 缓存 `sync.RWMutex`；草图 `atomic.Uint64` 加 CAS；`BufPool`（`sync.Pool`）复用应答缓冲 |
| 优雅关停 | `Proxy.Run(ctx)` 的关停路径，以及 `main` 没接信号的缺口：见 22 节「在项目里」 |
| 测试 | `cache/*_test.go` 覆盖逐出一致性、并发逐出、草图并发；`tcp` 包有 `FuzzDecodeFrame` |
| 压测方法 | `dnsq/` 自写压测器；`LESSON.md` 的结论是先修压测器自己的 bug，再读数据 |

## 常见坑

1. 多张索引表只改一张，缓存默默漂移。
2. 删除时先收集下标再批量删，交换删除让下标全部失效。
3. 以为缓存命中是纯读，结果命中也要改状态，读锁不够。
4. UDP 读缓冲不拷贝就交给 goroutine，被下一个包覆盖。
5. 缓存只认纯净查询，客户端多带一个 AD 或 EDNS 就不命中。
6. 高基数值（域名、IP）放进指标标签。

## 判断题

1. 为什么 `Cache.Get` 要用写锁？有没有办法既保留读锁又记录访问？（提示：想想 Rust 版用了什么。）
2. TinyLFU 的草图可能高估频率但不会低估，为什么这个方向对准入是安全的？
3. 主动过期为什么每轮结束就要放锁？
4. 把 `DNS_WORKER_POOL` 设成 1 会发生什么？

## 动手

```sh
cd dnsproxi-go && go vet ./... && go test -race -count=1 ./...
```

我在本机用 HEAD 版本跑过：`dnsproxi-go`、`cache`、`tcp`、`wire` 各包通过。当初建议的三个改造，前两个已经落地（提交 `ff5f72b`、`68aedcd`）：

1. 缓存现在接受带 AD 标志的查询：`cache.Request` 的判据从"flags2 全 0"放宽为 `flags2&^0x20`，只豁免 AD 位，RA/rcode 仍拒；应答里的 AD 位仍由上游决定，不从查询带。
2. `main` 已接上 `signal.NotifyContext`（SIGTERM/SIGINT 转 ctx 取消）。顺带修了 ActiveTTL 关停死锁：expirer 在 `wg` 里等 `done`，而 `close(done)` 原挂在 `Run` 的 defer 上（要等 `wg.Wait()` 之后的 return 才跑），两者互等；现在读循环退出路径先 `close(done)` 再 `wg.Wait()`。

剩下的改造题：把命中路径的应答拷贝去掉，对照 `labs/experiments/hitpath-ab` 的基准。

## 想深入去哪查

- 旧长版：`notes/p6-capstone-dnsproxi.md`（G1 到 G6 的完整复盘日志）、`notes/p17-dnsproxi-metrics.md`、`notes/p18-dnsproxi-slog.md`、`notes/p34-tcp-server-dns-over-tcp.md`。
- `dnsproxi-go/LESSON.md`：同机压测与读数；`labs/experiments/`：命中路径、GC 压力、worker pool 的消融实验。
- 原材料：这个项目的主题来自作者自己的 Rust 版课程线，资料中没有对应单元；TCP 成帧部分对应 TB36 到 TB38。
