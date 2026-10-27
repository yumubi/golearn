# 24 项目线一：dnsproxi-go 从零读懂

> 详细程度：项目 · 约 25 分钟
> 材料：`dnsproxi-go/` 全部源码、`dnsproxi-go/LESSON.md`、旧章 `notes/p6-capstone-dnsproxi.md`、`notes/p17-dnsproxi-metrics.md`、`notes/p18-dnsproxi-slog.md`、`notes/p34-tcp-server-dns-over-tcp.md`
> 前置：04 到 08 节读过一遍；这一节反过来用，是把前面的范式在一个真实项目里找出来
> 说明：本节的输出全部来自 2026-10-15 在本机跑 HEAD 版二进制的实测；压测数据引自 `dnsproxi-go/LESSON.md`，没有复测

## 先看一个现象：同一个查询发三遍

一个 DNS 代理听起来很平凡：收下查询，转发给上游，把应答带回来。我们把最小的一件事做三遍：同一个名字 `a` 的 AAAA 查询，发 UDP 两次、再走一次 TCP，观察代理干了什么。上游是一个假回包器，固定回答 `1.2.3.4`、TTL 60 秒；代理开着缓存、UDP 与 TCP 两个口。三次发送与代理的日志：

```text
UDP 第 1 次 → 1.2.3.4   日志：cache miss, forwarding  qname=a qtype=AAAA
UDP 第 2 次 → 1.2.3.4   日志：cache hit ttl_left=60
TCP 同名    → 1.2.3.4   日志：cache hit ttl_left=60（同一份缓存，上游没被再问）
```

```text
dnsproxi_queries_total{outcome="miss",qtype="AAAA"}      1
dnsproxi_queries_total{outcome="hit",qtype="AAAA"}       2
dnsproxi_queries_total{outcome="forwarded",qtype="AAAA"} 1
dnsproxi_cache_entries                                   1
```

三遍查询走的是两个完全不同的传输层，却共用同一个业务入口；缓存一次登记，两条路都能命中。这两个观察就是本节的骨架：**请求怎么流动**、**缓存怎么工作**。往下读之前，把 `dnsproxi-go/` 克隆到手边，对照源码。

再补一个实验让现象尖锐一点。缓存里的应答带 60 秒 TTL，等 3 秒后再查同一个名字，应答里的 TTL 字段必须变成 57，否则客户端会拿到越活越久的数据。实测（A 记录的 TTL 字段按大端读出）：

```text
第 1 次应答 35B，A 记录 TTL 字段 = 60 秒
（sleep 3 秒）
第 2 次应答 35B，A 记录 TTL 字段 = 57 秒
```

```text
{"msg":"cache miss, forwarding","qname":"a","qtype":"A","req_id":"q-1792049302236985000"}
{"msg":"cache hit","qname":"a","qtype":"A","ttl_left":57,"req_id":"q-1792049305237894000"}
```

命中不是"把存的东西还回去"这么简单：ID 要换成当前查询的，TTL 要按流逝的秒数原地递减。这是 `cache.Reply()` 干的活，13 节的"缓存的应答要改写"在这里落地。

## 一、请求路径：两种传输，一个入口

```text
UDP 包 ──→ Proxy.Run 读循环 ──┬── 每包一个 goroutine（默认）
                              └── 或固定 worker 池（DNS_WORKER_POOL=N）
TCP 连接 ──→ tcp.Serve ──→ 每连接一个 goroutine ──→ 2 字节成帧读 ──→ streamConn
                                    │
                                    ▼
                             Proxy.handleQuery
        短包丢弃 → 限流(令牌不够直接丢) → sinkhole 命中则本地 NXDOMAIN → 缓存
        命中：改写 ID 与 TTL 后直接回
        未命中：熔断门(开放态直接丢) → 临时 UDP socket 转发上游(2s deadline)
                → 按成败回填熔断器 → 判断能否入缓存 → 回客户端
```

TCP 侧没有一行"DNS 业务代码"。`tcp/streamConn` 把一条 TCP 连接包装成 `net.PacketConn`：`ReadFrom` 从成帧器里取一帧当作一个"包"，`WriteTo` 给应答加两字节长度前缀。于是 UDP 时代写好的 `Proxy.handleQuery` 原样接收 TCP 查询，它根本不知道对面是 UDP 还是 TCP。这就是 04 节"接口参数是关节"的实景：业务依赖的是 `net.PacketConn` 这个小接口，不是任何具体传输。

成帧本身是 4 字节的活：两字节长度前缀加 `io.ReadFull` 两次（先读长度，再读正文），正文长度在协议头里就有（见 03 节的二进制布局）。为什么 UDP 不需要这层？因为 UDP 自带边界，一个包就是一条报文；TCP 是无边界的字节流，"一条 DNS 报文"是应用层自己定的约定（对照 TB36 p6-7 的字节流无边界与定界模式）。半截帧、前缀比正文还长、零长度帧，这些畸形输入由 `FuzzDecodeFrame` 的 fuzz 测试兜底，见 14 节的性质测试。

`sinkhole` 值得一提：`DNS_SINKHOLE=ads.` 之后，`ads.example` 的查询在本地直接回答 NXDOMAIN，上游根本收不到包。实测：

```text
ads.example → 29B flags=0x81 rcode=3 ancount=0（本地 NXDOMAIN，上游没被问）
a.          → 35B rcode=0 ancount=1 answer=1.2.3.4（正常转发）
```

```text
dnsproxi_queries_total{outcome="sinkhole",qtype="A"} 1
dnsproxi_queries_total{outcome="forwarded",qtype="A"} 1
```

判断只是一个 `strings.HasPrefix`。它的意义不在功能，在位置：拦截发生在限流之后、缓存之前，被拦的查询连缓存都不污染。

## 二、缓存：命中一次写，逐出三张表

`cache/cache.go` 的核心结构在 13 节全讲过，这里只说源码里最值得抄走的三个决定。

**决定一：命中用写锁。** `Cache.Get()` 虽然叫 Get，却会更新 `access` 中的最后访问时间，供近似 LRU 排序使用。普通 Go map 的并发读写需要同步，因此这个实现用 `mu.Lock()` 保护整个更新，读多时会限制并发。它不是 Go 语言的唯一选择：也可以在适当的生命周期管理下，把访问时间放在条目中的原子变量，用原子操作更新；这会增加实现与维护成本。

**决定二：逐出时三张表一起动。** 缓存用 `keys` 切片加三张 map（`slot` 反查下标、`m` 存条目、`access` 记访问时间）支持 O(1) 的随机抽样加交换删除。`removeSlot` 把最后一个元素搬进被删的洞，再修正被搬动者的 `slot`。源码注释记录了第一版的失误：漏改了 `m`，`len(keys)=3` 而 `len(m)=4`，容量上限静默失效，被测试当场抓住。教训是 05 节讲过的：多份冗余状态要同步修改，漏一处不会有任何报错，只有命中率莫名下降。

**决定三：满仓准入不是比大小，是比频率。** 新条目要挤掉在位者时，比较两者的访问频率估计，新来的不够热就拒收（客户端照样拿到上游应答，只是不留档）。频率用 count-min 草图估计：4 行、每行 4 bit 计数器、16 格打包进一个 `uint64`，取各行最小值。哈希碰撞可能抬高估计值；计数饱和和定期老化又会使估计不再等于历史总访问次数。判决函数在 `cache/sketch.go`：

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

三个防堵死的细节都是防御真实攻击的：计数饱和于 15 加定期整体减半（老化），防止历史热点永远占着位置；被淘汰者如果已过期就"尸体让位"，不比频率直接放行，不让死人靠生前的热度守门；那 1/128 的随机放行是防哈希碰撞把在位者估计值抬满后新条目永远进不来。这套是 Caffeine 的 TinyLFU 准入缩水版，06 节讲过位打包的动机：`atomic.Uint64` 上的 CAS 才是无锁并发的最小单元。

## 三、guard：限流在缓存前，熔断在缓存后

`guard/` 包是 16 节的结论落地：令牌桶限流加三态熔断，默认全关。开启后行为用两个实验看清。

**限流**。`DNS_RATE_LIMIT=2,2`（每秒 2 个令牌、桶容量 2），10 个并发查询同时打进来。实测：

```text
10 个并发查询里拿到应答的：0 个（其余被限流丢弃，客户端只见到超时）
```

```text
dnsproxi_queries_total{outcome="rate_limited",qtype="OTHER"} 8
dnsproxi_queries_total{outcome="upstream_error",qtype="A"}   2
```

有意思的是前 2 个查询过了限流门、却撞上已经死掉的上游（`reason="refused"`）。限流在缓存之前：命中缓存的查询也要花令牌，因为限流保护的是代理自身，不是上游。被拒的查询不回包，UDP 的语义由客户端超时重试兜底，这是 16 节"限流是丢车保帅"的直译。

**熔断**。`DNS_BREAKER_THRESHOLD=50`（失败率 50%，最小样本 20），上游整个杀掉，连发 30 个查询。实测：

```text
前 22 个：upstream_error（refused）        → 窗口里 20 个样本、失败率 100%，熔断打开
后 8 个： breaker_open                    → 快速失败，不再碰上游
```

```text
dnsproxi_queries_total{outcome="upstream_error",qtype="A"} 22
dnsproxi_queries_total{outcome="breaker_open",qtype="A"}   8
```

熔断门的位置在转发之前、缓存之后：缓存命中的查询不经过它（上游病了不影响命中），未命中的查询先被门拦一次，免得每个查询都去上游撞 2 秒超时。回填纪律在 `Proxy.handleQuery` 里看得最清楚：`sent` 标志区分"已发往上游"与"门都没过"，前者成败都要 `markBreaker`，后者不计样本，否则窗口统计失真。10 秒冷却后转半开放一个试探，试通了才回 closed。这套状态机就是 16 节的图，不多一行不少一行。

## 四、TCP 成帧之外的截断：TC 位

UDP 应答超过上限时，正确行为不是硬塞，是按 RFC 1035 截断并置 TC 位，让客户端自己退回 TCP。`tcp/truncconn.go` 只包了出站的 `WriteTo`，收包路径零改动。用 `tcp.WrapTrunc` 直接演示（应答 37 字节、上限 30 字节）：

```text
超限应答 37B、上限 30B → 客户端收到 21B：flags=0x82 TC=true ancount=0 qdcount=1
未超限 21B 原样直写：21B flags=0x80 TC=false ancount=1
```

截断保留 12 字节头加完整 question 段，三个应答计数清零，TC 位置 1。为什么保留 question？客户端回退 TCP 时要重发同一问题，TCP 口收到的是普通查询，走完整业务路径。这是"两条传输一个入口"的又一次复利。

## 五、并发模型与背压：数字说话

默认形态是每包一个 goroutine：读循环收到包就 `go handleQuery(...)`，简单、隔离好、没有队列。代价是失控：1 万个并发查询就是 1 万个 goroutine，每个都可能占着上游 socket 等 2 秒。

`DNS_WORKER_POOL=N` 切换到固定 N 个 worker 吃带缓冲 channel 的形态：缓冲满了，读循环在 `qch <- packet` 处阻塞，这就是背压，代价是排队延迟由队列长度决定。500 个并发查询、客户端只等 20 毫秒，两种形态在同一台机器上对比（假上游回包只需微秒）：

```text
worker pool=2：按时拿到应答 337，超时 163，总耗时 25ms
无 pool：     按时拿到应答 500，超时 0，  总耗时 16ms
```

`dnsproxi_in_flight_queries` 这个 gauge 把差异暴露得更直观：pool=2 时在飞查询数峰值被钉在 2，无 pool 时冲到 333。**在飞数的形状就是并发模型的形状**：一个是水位闸门，一个是洪水漫滩。哪个对？没有上游压力时无 pool 更快（0 排队），上游变慢时 pool 保护自己不被拖死。22 节的任务就是让你把这两种形态都跑一遍，亲手看到水位。

两个安全细节必须提。第一，读循环里 `append([]byte{}, buf[:n]...)` 给每个查询拷贝一份：读缓冲会被下一个包复用，不拷就是数据竞争，这是 06 节"goroutine 不能共享可变缓冲"的最小案例。第二，每个 goroutine 入口都有 `recover`：一个查询的 panic 会让整个进程退出，业务入口必须接住，这是 05 节的纪律在服务器里的形态。

## 六、可观测：请求 ID 与低基数

`logger.go` 只有一百来行，干两件事。一是 `JSONHandler` 输出结构化日志，二是 `ctxHandler` 包装器在每个日志调用里自动注入 `req_id`：`handleQuery` 入口生成一个请求 ID 放进 ctx，之后任何 `logger.InfoContext(ctx, ...)` 都自动带上它，调用点不用管。这就是 15 节"上下文传播"的手艺，一个查询的所有日志行能按 `req_id` 串起来。

指标侧的纪律只有一条：标签必须是有限枚举。`QueriesTotal` 只有 `qtype` 和 `outcome` 两个维度，qtype 归到 A、AAAA、CNAME 等实名加 OTHER 兜底，outcome 只有 hit、miss、forwarded、rate_limited、breaker_open、sinkhole、upstream_error、short 这些枚举。域名和客户端地址是高基数，进日志不进标签，否则一次域名扫描就能把时间序列撑爆。`UpstreamErrorsTotal` 的 reason 也只分 timeout、refused、network 三类（`upstreamErrReason` 用 `errors.As` 判 `net.Error` 超时、`errors.Is` 判 ECONNREFUSED），正好对应 05 节的错误分类三件套。

## 七、测试与压测：这份代码怎么自证

各包测试一览（`go test -race -count=1 ./...` 全绿，本机 2026-10-15 实测，约 20 秒）：

| 包 | 代表性测试 | 守的是什么 |
|---|---|---|
| `wire/` | `TestNamePointerLoop`、`TestDeepPointerChainWithinCap` | 压缩指针环与深链不死循环 |
| `cache/` | `TestSlotConsistency`、`TestExpireRoundNeverKillsLivingUnderSwapRemove` | 逐出时三张表一致；交换删除不误删活条目 |
| `cache/` | `TestAdmissionBlocksCold`、`TestAdmissionAdmitsHot` | 准入判决的两个方向 |
| `guard/` | `TestGuard` | 令牌桶与熔断的状态转移 |
| `tcp/` | `TestTCPPipelined`、`TestTruncConn`、`FuzzDecodeFrame` | 粘包、截断、畸形输入 |
| 根包 | `TestD1Forward`、`TestD1Sinkhole`、`TestD1Timeout`、`TestSIGTERMGracefulExit` | 端到端四跳与优雅关停 |

压测的方法论在 `dnsproxi-go/LESSON.md`：自写的 `dnsq/` 压测器打过两个真实项目（Go 版与 Rust 版同机互打），结论是 Go ~10k qps、Rust ~13 到 19k qps、差距 1.3 到 1.9 倍，瓶颈在每查询临时 socket 的生命周期而不是语言运行时。比数字更值钱的是两条方法纪律：先修压测器自己的 bug（dnsq 曾因 deadline 不刷新产生 96% 假丢包），再读数据；micro-benchmark 显著不等于端到端显著（`labs/experiments/hitpath-ab/` 的省拷贝优化在 micro 里快 1.6 倍，端到端占比不到 5% 测不出来）。

## 小结

1. 两种传输一个入口：TCP 侧用 `streamConn` 适配成 `net.PacketConn`，业务代码零感知；成帧是 2 字节前缀加 `io.ReadFull`。
2. 缓存命中不是纯读：改写 ID、递减 TTL；这个实现还更新 map 中的访问时间，因此使用写锁。
3. 逐出时 keys、slot、m、access 四份状态必须一起动，漏一份就是静默漂移。
4. 准入比频率不比大小：count-min 草图可能因碰撞高估，老化防锁死、随机放行防碰撞攻击。
5. 限流保护代理自身（在缓存前），熔断保护上游（在缓存后、转发前）；回填熔断器要区分"门没过"与"发出去"。
6. 并发模型的形状就是 in_flight 的形状：pool 是闸门（背压），goroutine-per-packet 是漫滩（失控风险）；两者都有入口 recover。
7. 观测的纪律：结构化日志带请求 ID，指标标签只收有限枚举。
8. 测试守的是不变量（表一致、不误删、状态转移），压测先修压测器。

## 常见坑

1. 多张索引表只改一张：容量上限静默失效，测试前毫无症状。
2. 以为缓存命中是纯读：命中也要写访问时间，RLock 不够。
3. 交换删除前先收集一批下标再批量删：下标全体失效，静默删掉活条目。
4. UDP 读缓冲不拷贝就交给 goroutine：被下一个包覆盖，偶发且难复现。
5. 高基数值（域名、IP）放进指标标签：时间序列爆炸。
6. 熔断回填漏掉某个 return 路径：窗口统计失真，熔断早开或不开。
7. 把 micro-benchmark 的 1.6 倍当成端到端收益：占比太小测不出来。

## 判断题与答案

先自己答，再看后面。

1. TCP 查询进 `handleQuery` 前，谁替它背了"包"的概念？如果上游应答 900 字节、客户端走 TCP，两字节前缀由谁加？
2. 为什么 `Cache.Get()` 必须用写锁？有没有既保读锁又记访问顺序的办法（提示：想想 06 节 Rust 版的做法）？
3. 限流在缓存之前，代价是"命中缓存的查询也要花令牌"。反过来放（限流在缓存后）会好一点吗？会坏在哪里？
4. 500 个并发、客户端等 20ms：pool=2 有 163 个超时，无 pool 全过。能不能由此说"无 pool 更好"？

**答案**

1. `tcp.streamConn` 把一帧适配成一次 `ReadFrom`，业务看到的永远是一个"包"。TCP 应答的两字节前缀在 `streamConn.WriteTo` 里加，业务不感知。
2. 这个实现命中时要更新 `access` map，所以用写锁保护。也可以设计为读锁保护条目生命周期、用条目内的原子变量更新访问时间，但要额外处理并发和维护复杂度。
3. 好处是命中查询不消耗令牌。坏处是上游故障时缓存未命中的查询会先占住缓存与锁资源再被熔断门拦下，限流对代理自身的保护后移；且命中路径变长。现在的顺序（限流在缓存前）优先保护自己。
4. 不能。这组数字的前提是上游极快（微秒级回包）。上游一旦变慢，无 pool 的在飞数会涨到数百，每个 goroutine 都占着上游 socket 等 2 秒，内存与文件描述符先耗尽。两个数字说明的是"背压的代价"，不是"孰优"。

练习位：`go run ./dnsq -target 127.0.0.1:15353 -dur 5s -conc 16 -names 100` 打命中路径，再 `-names 5000` 打未命中路径，对比 p50。

## 动手

```sh
cd dnsproxi-go && go vet ./... && go test -race -count=1 ./...
```

三个口同时开，把第一节的实验复跑一遍（假上游 20 行就够：收任何查询回一条 `1.2.3.4`、TTL 60 的应答）：

```sh
DNS_LISTEN=:15353 DNS_UPSTREAM=127.0.0.1:15300 DNS_CACHE=1 \
  DNS_TCP_LISTEN=:15354 DNS_WORKER_POOL=2 go run .
```

改造题（历史两题已落地，不要重复做）：缓存接受带 AD 标志的查询（判据放宽为 `flags2&^0x20`，提交 `ff5f72b`）；`main` 接上 `signal.NotifyContext` 优雅关停（提交 `68aedcd`）。**保留的改造题只有一道：去掉命中路径的应答拷贝，对照 `labs/experiments/hitpath-ab` 的基准**。提示：`cache.ReplyPool` 与 `cache.BufPool` 已经写好（语义契约见 `dnsproxi-go/cache/reply.go` 开头注释），`DNS_HITPATH_POOL=1` 就是这条路径，你的任务是用 `dnsq` 判断它在什么负载下值得切默认，以及给出"不值得"的数据也要。

## 想深入:正文之外的干货

上面正文只借了 TB36 到 TB38 的"定界"一角。这三讲是一整条 TCP 服务器线，下面把它独有的干货补齐，正文讲过的（流无边界、每连接一 goroutine 的形态）不再重复。

### TB36：socket 编程模型与读写语义

- 解决实际问题的固定循环是三步：理解问题，从问题里识别技术点；技术预研与储备，摸清背景、原理、能解决与不能解决什么、优点与不足；设计与实现收尾。问题越难、认知越弱，循环的轮数越多才收敛，这是实战篇三讲的总方法，本节就是照它走完的一轮。〔TB36 p2-3〕
- 选域的依据：Go 官方 2020 用户调查里 Web 编程第一、网络编程第四，作者据此从网络编程里选了实战题。网络编程通常只关注 TCP/IP 模型最上两层，传输层的 TCP/UDP 与应用层；socket 是传输层暴露给用户的编程接口，直接基于 socket 定义应用层协议是最常见的网络编程形式。〔TB36 p3-5〕
- 定界模式与流无边界正文已讲（见"一、请求路径"），这里补 K3 的另一半通信模型：教学协议是典型的请求/响应，连接由客户端发起，之后一请求一响应交替进行，直到客户端主动断开；dnsproxi 的 TCP 口上就是一条连接里这样的一串请求响应对。〔TB36 p6-7〕
- 阻塞 I/O 下一个线程只能陪一个连接干等；非阻塞未就绪立即返回 EAGAIN，调用方通常轮询，单独用在生产上浪费 CPU；多路复用让一个线程挂起在一堆 socket 上，由内核通知哪个可读可写，是主流服务器的选择。〔TB36 p8-11〕
- Go 网络库通常使用运行时网络轮询器：I/O 暂时不能继续时挂起 goroutine，条件满足后再唤醒，避免让每个等待网络的 goroutine 独占线程。不同操作系统的实现不同；例如 Linux 使用 epoll、macOS 使用 kqueue。〔TB36 p11-12〕
- 服务端固定套路是 Listen+Accept 循环加 `go handleConn(c)`；端口被占用时 Listen 直接报 bind: address already in use。客户端 Dial 会阻塞到连接成功或失败，严格时限用 DialTimeout。〔TB36 p12-15〕
- backlog 队列是隐藏的坑：服务端 accept 不及时，客户端 Dial 会阻塞排队；服务端一直不 accept，macOS 上约 1 分多钟、Ubuntu 约 2 分多钟才超时，长度由 somaxconn 等内核参数决定。〔TB36 p14-15〕
- 连接建立后，两边内核各有一对发送/接收缓冲区。写阻塞不是死锁：对端收缓冲和本端发缓冲都满时 Write 才阻塞，服务端读走一段就恢复。〔TB36 p19-20〕
- 读操作有四态：无数据阻塞、有部分数据立即返回（要 10 只到 2 也不等）、缓冲够则填满、设了 SetReadDeadline 就超时返回。deadline 是绝对时间，不重设则之后所有读全部超时，取消要传 time.Time{}。〔TB36 p16-18〕
- 写操作可能部分写入：服务端中途关掉，客户端又写入 24108 字节后才报 broken pipe，这是课程中的一次观察。写成功表示本地接口接受了字节，不证明对端应用已收到或处理；失败后也不能仅凭 `n` 判断对端处理了多少，是否重发要由应用协议决定。写超时后 deadline 不重设就仍然有效。Read/Write 要结合 `n` 和 `err` 判断。〔TB36 p21-22〕
- 运行时用 fdMutex 串行化同一 fd 的读写：单次 write 写完整业务包是安全的，拆成多次写则不保证连续；并发读不重叠，但 buf 给小了会把一个业务包撕给两个 goroutine。〔TB36 p22-26〕
- 对端关闭后，读会先吐完剩余数据再给 io.EOF；但全双工下你这边 Write 仍可能"成功"，数据只是进了本机内核缓冲，永远到不了对方。发现对端关了就收尾自己的 socket，继续写没有意义。〔TB36 p26〕

### TB37：Frame 与 Packet 两层抽象

协议字段就三个，先把每个字段的角色钉死再写代码。第一个字段 totalLength 是整包的总长度，这个协议是高度简化的二进制模式定义协议，它的特点就是采用长度字段标识独立数据包的边界，因此 totalLength 是应用层"分割包"的最重要字段。第二个字段 commandID 标识包类型，共四种：0x01 连接请求包、0x02 消息请求包、0x81 连接响应包、0x82 消息响应包，代码写成两组 const：CommandConn = iota + 0x01（Submit 顺延得 0x02）、CommandConnAck = iota + 0x80（SubmitAck 顺延得 0x82）。第三个字段 ID 是"每个连接上"请求包的消息流水号：在该连接范围内顺序累加、步长为 1、循环使用，供请求发送方匹配响应包，因此一对请求与响应消息的流水号必须相同。请求包与响应包唯一的不同在最后一个字段：请求包是 payload，承载应用层业务数据；响应包换成 result，取值做了简化。成功用 0，失败无论原因一律用 1。〔TB37 p2-3〕

TCP 连接上的数据是没有边界的字节流，但业务层眼里没有字节流、只有协议消息：连接上看到的应是一个挨一个的协议消息流。于是先抽象出 Frame：每个 Frame 表示一个协议消息，由头部的 totalLength 和 Frame 负载 payload 两部分构成，相邻 Frame 靠头上的 totalLength 隔离。再把 Frame payload 定义为 Packet：Packet 才是业务层真正需要的消息，头是 commandID（标识消息类型），Body 是 ID 加 payload（响应包则换成 result），对业务层有价值的数据都在 Packet Body 里。基于这两个抽象再做解包打包：decode 是把字节流转换成特定类型的协议消息结构，encode 反之。〔TB37 p4-5〕

frame 包负责识别 TCP 流边界，定义统一接口 StreamFrameCodec 和类型 FramePayload []byte。接口只认 io.Writer/io.Reader、不认 net.Conn，就是为了能用 bytes.Buffer 冒充连接写测试。Encode(io.Writer, FramePayload) 把 Frame payload 编码成 Frame 写进 outbound 流：先算 totalLen = int32(len(framePayload)) + 4（长度字段自身占 4 字节），binary.Write(w, binary.BigEndian, &totalLen) 写 4 字节长度头，出错即返回；再 w.Write 写 payload，出错即返回；写入字节数不足 len(framePayload) 就返回 ErrShortWrite。Decode(io.Reader) 反方向：先 binary.Read 读 4 字节 totalLen，再 make 一个 totalLen-4 的 buf 用 io.ReadFull 读满，短于 totalLen-4 返回 ErrShortRead。〔TB37 p6-8〕

三条实现注意：收发两端统一大端；binary.Read/Write 按参数宽度动字节（int32 就是 4 字节）；不设 deadline 时 io.ReadFull 会一直读满，只有 EOF 或 ErrUnexpectedEOF 才提前返回。不用 io.ReadFull 而单次 Read，短读会让解析错位。〔TB37 p8〕

单测不需要真实网络：TestEncode 用满足 io.Writer 的 bytes.Buffer 冒充连接，断言两点。对 buffer 做 binary.Read（BigEndian）得到的 totalLen 应为 9（"hello" 5 字节加 4 字节长度头），且 buffer 剩余字节应恰好是 "hello"。TestDecode 直接构造 {0x0,0x0,0x0,0x9,'h','e','l','l','o'} 喂给 bytes.NewReader，断言解出的 payload 为 "hello"。要覆盖所有可测错误分支，就造 ReturnErrorWriter/ReturnErrorReader：内层 W/R 加计数器 wc/rc，每次调用先自增，达到 Wn/Rn 就返回 write error/read error，否则透传内层。Wn=1 时第一次调用（binary.Write 内部的 Write）就报错，模拟写长度头的 binary.Write 失败分支；Wn=2 时第一次（binary.Write 内部）正常、第二次（w.Write 写 payload）报错，模拟写 payload 的 w.Write 失败分支；Rn=1 时第一次调用报错，对应 binary.Read 读长度失败的分支；Rn=2 时第一次正常、第二次报错，对应 io.ReadFull 读 payload 失败的分支。四个用例都断言 err 非 nil。有了这两个注入器，frame 包覆盖率（go test -cover . 可查）到 90% 以上；只测正常分支等于没测编解码器。〔TB37 p8-11〕

包级 Decode 取第一个字节当 commandID，switch 分发到各类型的 Decode；Encode 反向用 type switch 定 commandID 再 bytes.Join 拼包。教学示例 Submit.Decode 不校验 body 长度，不足 8 字节直接越界 panic，抄代码时记得补。〔TB37 p12-14〕

服务端 handleConn 的调用结构是 read frame、frame decode、handle packet、packet decode、ack encode、frame encode、write ack，出错就 return 断连接，日志里的 frame decode error: EOF 是客户端正常退出，不是故障。〔TB37 p14-16〕

客户端收应答的 goroutine 用 SetReadDeadline 1 秒加 continue 轮询 quit 通道，才能从阻塞的 Read 里跳出来响应退出通知；这个"超时当轮询"的手法在写守护 goroutine 时很常用。〔TB37 p17-19〕

### TB38：从基准到 bufio 与 sync.Pool

Go 程序优化有个固定四步循环：第一步建立性能基准，没有初始"参照物"就无法检验优化是否有效；第二步性能剖析，用工具找影响性能的"瓶颈点"；第三步针对瓶颈点分析成因、有针对性地改代码；第四步采集优化后的性能数据与基准比较，确定优化效果。有提升说明这一轮优化有效；若指标仍没达到预期，就把优化后的新指标当新的性能基准，再走一轮，循环往复直到达标。基准分两种建法：Go 原生 benchmark 用例适合给局部热点（算法、数据结构实现）建基准；程序整体则用"度量指标加图形化面板"的方式。〔TB38 p2-3〕

demo2 在 demo1 基础上拷贝而来，新建 metrics 包定义三个能反映服务端性能的度量项：client_connected（当前已连接的客户端数量）用 prometheus.Gauge，Gauge 是对数值的即时测量、反映瞬时快照；req_recv_rate 与 rsp_send_rate（每秒收请求、发响应的量）不直接存"每秒多少"，而是落成累加计数器 ReqRecvTotal、RspSendTotal，类型 prometheus.Counter。Counter 必须与 rate 函数一起使用才能算出每秒处理能力，度量名分别是 tcp_server_demo2_req_recv_total、tcp_server_demo2_rsp_send_total、tcp_server_demo2_client_connected。metrics 包的 init 干两件事：其一 prometheus.MustRegister(ReqRecvTotal, RspSendTotal, ClientConnected) 注册三个度量项；其二启动 http server 监听 8889 端口，/metrics 路径挂 promhttp.Handler。这正是 prometheus 配置里 tcp-server job 的采集目标，Prometheus 定期从 8889 拉取度量值。埋点在 main 包 handleConn：连接建立时 ClientConnected.Inc()；defer 函数在连接断开时 ClientConnected.Dec() 并 c.Close()；每次 frameCodec.Decode 成功拿到 framePayload（收到并解码一个消息请求）后 ReqRecvTotal.Add(1)；frameCodec.Encode 回写响应成功后 RspSendTotal.Add(1)。埋点位置错了速率就失真。〔TB38 p9-11〕

- 观测设施是 Prometheus+Grafana 三容器（prometheus、grafana、node-exporter 采主机指标），scrape_interval 5s；node-exporter 面板不用手搭，import 官方 ID 1860 就有。〔TB38 p3-8〕

- 速率指标要用 Counter 计数、Grafana 里配 rate() 表达式（如 rate(req_recv_total[15s])）才算出每秒处理量，直接取 Counter 值没有意义；Gauge 才是瞬时快照。埋点位置错了速率就失真：ReqRecvTotal 在解码成功后加，RspSendTotal 在回写响应后加。〔TB38 p10-11〕
- 面板怎么建：node-exporter 那种现成看板直接 import，自家的指标要在 tcp-server-demo 仪表板里手动建三个 panel（req_recv_rate、rsp_send_rate、client_connected）；client_connected 直接取度量值，两个速率 panel 必须配 rate 表达式，如 rate(tcp_server_demo2_req_recv_total[15s])，返回 15 秒窗口内的每秒速率，这才是"服务端处理性能"的读法。〔TB38 p12-13〕
- 第一版基准怎么定：压测器由 demo1 的 client 改造而来（demo2/cmd/client）；压测前提是硬件条件一致，同一台 4 核 8G CentOS 先启 server 再启 client，跑几分钟后看 Grafana 曲线，约 18.5w/s 就记为第一个基准，后续 bufio 27w/s、sync.Pool 29.2w/s 都以它为参照。dnsproxi 的压测纪律（先修 dnsq 自己的 bug、同机互打）是这条基准方法论的落地版。〔TB38 p13-14〕
- 4 核 8G 上的第一版基准约 18.5w/s。pprof 用空导入 net/http/pprof 加单独 goroutine 监听 6060 接入；CPU 采样原理是每 10ms 发一次 SIGPROF 中断记下所有 goroutine 的函数栈，出现最多的函数通常就是瓶颈。〔TB38 p14-16〕
- 火焰图里最大的方框是 Syscall，调用者全是网络 read/write：net.Conn 直接当 io.Reader，哪怕只读 1 字节也是一次系统调用。加 bufio.Reader/Writer 把读写攒进内存缓冲，压到 27w/s，提升 45%。代价是写侧必须 defer Flush，忘了就把响应闷在缓冲里。〔TB38 p17-20〕
- 第二刀切堆分配：pprof 的 allocs profile 里 top 按 flat 列排（函数自身分配的字节数，cum 是含调用方的累计），packet.Decode 以 38MB 居首，list 进去定位到 `s := Submit{}` 一行。〔TB38 p20-21〕
- sync.Pool 复用 Submit：Decode 里 `SubmitPool.Get().(*Submit)`，归还点放在 handlePacket 处理完之后；27w/s 再涨到 29.2w/s。用完不归还优化就失效，归还后不清字段就是脏数据。〔TB38 p22-24〕
- dnsproxi 的正文把这些当结论用了（每查询临时 socket 是瓶颈、hitpath-ab 的省拷贝实验、`cache.ReplyPool`），这些是它们的出处推理链。省略的只有 TB38 的 Prometheus+Grafana+node-exporter docker-compose 部署步骤，属搭环境材料，手册 22 节与 15 节另有覆盖。

## 想深入去哪查

- 旧长版：`notes/p6-capstone-dnsproxi.md`（G1 到 G6 完整复盘，含 Rust 对照）、`notes/p17-dnsproxi-metrics.md`、`notes/p18-dnsproxi-slog.md`（日志手艺）、`notes/p34-tcp-server-dns-over-tcp.md`（TCP 成帧与截断的全部细节）。
- `dnsproxi-go/LESSON.md`：同机压测数据与读数；`labs/experiments/`：命中路径 A/B（`hitpath-ab/`）、GC 压力账（`gc-pressure/`）、worker pool 消融（`labs/experiments/results-p9-*.md` 的 E6）。
- 原材料：项目主题来自作者自己的 Rust 版课程线，资料中没有对应单元；TCP 服务器三讲对应 TB36 到 TB38（字节流定界、goroutine per connection、bufio 与 sync.Pool 优化）。
