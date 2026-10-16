---
title: 24 项目线一：dnsproxi-go 从零读懂
---

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

**决定一：命中用写锁。** `Cache.Get()` 的签名是读，干的事却包含"更新最后访问时间"（近似 LRU 的排序依据）。Go 的 map 没有原子操作可用，于是读路径升级成 `mu.Lock()`。代码注释直说了代价：读多写少的 RLock 优势打了折。对照你在 06 节见过的 Rust 版：Rust 在读锁里用 `fetch_max` 原子写，Go 没有等价物，只能付这个代价。这不是实现瑕疵，是语言差异在并发语义上的投影。

**决定二：逐出时三张表一起动。** 缓存用 `keys` 切片加三张 map（`slot` 反查下标、`m` 存条目、`access` 记访问时间）支持 O(1) 的随机抽样加交换删除。`removeSlot` 把最后一个元素搬进被删的洞，再修正被搬动者的 `slot`。源码注释记录了第一版的失误：漏改了 `m`，`len(keys)=3` 而 `len(m)=4`，容量上限静默失效，被测试当场抓住。教训是 05 节讲过的：多份冗余状态要同步修改，漏一处不会有任何报错，只有命中率莫名下降。

**决定三：满仓准入不是比大小，是比频率。** 新条目要挤掉在位者时，比较两者的访问频率估计，新来的不够热就拒收（客户端照样拿到上游应答，只是不留档）。频率用 count-min 草图估计：4 行、每行 4 bit 计数器、16 格打包进一个 `uint64`，取各行最小值，可能高估但永不低估，估计值的"宁高勿低"方向恰好是准入需要的。判决函数在 `cache/sketch.go`：

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
2. 缓存命中不是读：改写 ID、原地递减 TTL；命中要更新访问时间，Go 的 map 逼你用写锁。
3. 逐出时 keys、slot、m、access 四份状态必须一起动，漏一份就是静默漂移。
4. 准入比频率不比大小：count-min 草图宁高勿低、老化防锁死、随机放行防碰撞攻击。
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
2. 命中要更新 `access`（近似 LRU），Go 的 map 没有原子操作，读只能升级写。Rust 版在读锁里用 `fetch_max` 原子写做到了"读锁加访问记录"，这是语言能力差异。
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

- 阻塞 I/O 下一个线程只能陪一个连接干等；非阻塞未就绪立即返回 EAGAIN，调用方通常轮询，单独用在生产上浪费 CPU；多路复用让一个线程挂起在一堆 socket 上，由内核通知哪个可读可写，是主流服务器的选择。〔TB36 p8-11〕
- Go 的"阻塞"是表象：底层 socket 实际是非阻塞的，运行时拦截 EAGAIN，把 goroutine 挂到 netpoller（Linux 用 epoll、macOS 用 kqueue、Windows 用 iocp），可读可写时再唤醒。阻塞的只是 goroutine，不占线程。〔TB36 p11-12〕
- 服务端固定套路是 Listen+Accept 循环加 `go handleConn(c)`；端口被占用时 Listen 直接报 bind: address already in use。客户端 Dial 会阻塞到连接成功或失败，严格时限用 DialTimeout。〔TB36 p12-15〕
- backlog 队列是隐藏的坑：服务端 accept 不及时，客户端 Dial 会阻塞排队；服务端一直不 accept，macOS 上约 1 分多钟、Ubuntu 约 2 分多钟才超时，长度由 somaxconn 等内核参数决定。〔TB36 p14-15〕
- 连接建立后，两边内核各有一对发送/接收缓冲区。写阻塞不是死锁：对端收缓冲和本端发缓冲都满时 Write 才阻塞，服务端读走一段就恢复。〔TB36 p19-20〕
- 读操作有四态：无数据阻塞、有部分数据立即返回（要 10 只到 2 也不等）、缓冲够则填满、设了 SetReadDeadline 就超时返回。deadline 是绝对时间，不重设则之后所有读全部超时，取消要传 time.Time{}。〔TB36 p16-18〕
- 写操作有部分写入：服务端中途关掉，客户端又补写了 24108 字节才报 broken pipe，这 24108 字节没到达对端，得自己留着重发。写超时同理，不重设就一直超时。Read/Write 永远要综合 n 和 err 判断。〔TB36 p21-22〕
- 运行时用 fdMutex 串行化同一 fd 的读写：单次 write 写完整业务包是安全的，拆成多次写则不保证连续；并发读不重叠，但 buf 给小了会把一个业务包撕给两个 goroutine。〔TB36 p22-26〕
- 对端关闭后，读会先吐完剩余数据再给 io.EOF；但全双工下你这边 Write 仍可能"成功"，数据只是进了本机内核缓冲，永远到不了对方。发现对端关了就收尾自己的 socket，继续写没有意义。〔TB36 p26〕

### TB37：Frame 与 Packet 两层抽象

- 教学协议字段就三个：totalLength 定边界，commandID 分类型（0x01/0x02 请求、0x81/0x82 响应，iota 加偏移两组常量），ID 是每连接上的流水号，请求响应用同一个号配对；请求带 payload，响应换成 result（0 成功 1 失败）。〔TB37 p2-3〕
- 业务层眼里不该有字节流，只有一条条 Frame；Frame 靠头上的 totalLength 相互隔离，Frame 的 payload 才是业务要的 Packet（头是 commandID，Body 是 ID 与数据）。〔TB37 p4-5〕
- 编解码器接口 StreamFrameCodec 只认 io.Writer/io.Reader，不认 net.Conn：测试时用 bytes.Buffer 冒充连接，不需要真实网络。Encode 用 binary.Write 写大端 int32 长度再写 payload，短写报 ErrShortWrite；Decode 先读 4 字节长度再 io.ReadFull 读正文，短读报 ErrShortRead。〔TB37 p6-8〕
- 三条实现注意：收发两端统一大端；binary.Read/Write 按参数宽度动字节（int32 就是 4 字节）；不设 deadline 时 io.ReadFull 会一直读满，只有 EOF 或 ErrUnexpectedEOF 才提前返回。不用 io.ReadFull 而单次 Read，短读会让解析错位。〔TB37 p8〕
- 覆盖率怎么上去：ReturnErrorWriter/ReturnErrorReader 包装器按"第 N 次调用"返回错误，逼 Encode/Decode 走进 binary.Write、io.ReadFull 各错误分支，frame 包覆盖率到 90% 以上。只测正常分支等于没测编解码器。〔TB37 p8-11〕
- 包级 Decode 取第一个字节当 commandID，switch 分发到各类型的 Decode；Encode 反向用 type switch 定 commandID 再 bytes.Join 拼包。教学示例 Submit.Decode 不校验 body 长度，不足 8 字节直接越界 panic，抄代码时记得补。〔TB37 p12-14〕
- 服务端 handleConn 的调用结构是 read frame、frame decode、handle packet、packet decode、ack encode、frame encode、write ack，出错就 return 断连接，日志里的 frame decode error: EOF 是客户端正常退出，不是故障。〔TB37 p14-16〕
- 客户端收应答的 goroutine 用 SetReadDeadline 1 秒加 continue 轮询 quit 通道，才能从阻塞的 Read 里跳出来响应退出通知；这个"超时当轮询"的手法在写守护 goroutine 时很常用。〔TB37 p17-19〕

### TB38：从基准到 bufio 与 sync.Pool

- 优化循环固定四步：建基准、剖析找瓶颈、改代码、和基准比，不达标就带着新指标再来一轮。基准分两种：原生 benchmark 适合局部算法，程序整体用"度量指标加图形化面板"。〔TB38 p2-3〕
- 观测设施是 Prometheus+Grafana 三容器（prometheus、grafana、node-exporter 采主机指标），scrape_interval 5s；node-exporter 面板不用手搭，import 官方 ID 1860 就有。〔TB38 p3-8〕
- 速率指标要用 Counter 计数、Grafana 里配 rate() 表达式（如 rate(req_recv_total[15s])）才算出每秒处理量，直接取 Counter 值没有意义；Gauge 才是瞬时快照。埋点位置错了速率就失真：ReqRecvTotal 在解码成功后加，RspSendTotal 在回写响应后加。〔TB38 p10-11〕
- 4 核 8G 上的第一版基准约 18.5w/s。pprof 用空导入 net/http/pprof 加单独 goroutine 监听 6060 接入；CPU 采样原理是每 10ms 发一次 SIGPROF 中断记下所有 goroutine 的函数栈，出现最多的函数通常就是瓶颈。〔TB38 p14-16〕
- 火焰图里最大的方框是 Syscall，调用者全是网络 read/write：net.Conn 直接当 io.Reader，哪怕只读 1 字节也是一次系统调用。加 bufio.Reader/Writer 把读写攒进内存缓冲，压到 27w/s，提升 45%。代价是写侧必须 defer Flush，忘了就把响应闷在缓冲里。〔TB38 p17-20〕
- 第二刀切堆分配：pprof 的 allocs profile 里 top 按 flat 列排（函数自身分配的字节数，cum 是含调用方的累计），packet.Decode 以 38MB 居首，list 进去定位到 `s := Submit{}` 一行。〔TB38 p20-21〕
- sync.Pool 复用 Submit：Decode 里 `SubmitPool.Get().(*Submit)`，归还点放在 handlePacket 处理完之后；27w/s 再涨到 29.2w/s。用完不归还优化就失效，归还后不清字段就是脏数据。〔TB38 p22-24〕
- dnsproxi 的正文把这些当结论用了（每查询临时 socket 是瓶颈、hitpath-ab 的省拷贝实验、`cache.ReplyPool`），这八条是它们的出处推理链。省略的只有 TB38 里 Prometheus+Grafana+node-exporter 的 docker-compose 部署步骤和 TB36 的 2020 官方调查背景，属搭环境与背景材料，手册 22 节与 15 节另有覆盖。

## 想深入去哪查

- 旧长版：`notes/p6-capstone-dnsproxi.md`（G1 到 G6 完整复盘，含 Rust 对照）、`notes/p17-dnsproxi-metrics.md`、`notes/p18-dnsproxi-slog.md`（日志手艺）、`notes/p34-tcp-server-dns-over-tcp.md`（TCP 成帧与截断的全部细节）。
- `dnsproxi-go/LESSON.md`：同机压测数据与读数；`labs/experiments/`：命中路径 A/B（`hitpath-ab/`）、GC 压力账（`gc-pressure/`）、worker pool 消融（`labs/experiments/results-p9-*.md` 的 E6）。
- 原材料：项目主题来自作者自己的 Rust 版课程线，资料中没有对应单元；TCP 服务器三讲对应 TB36 到 TB38（字节流定界、goroutine per connection、bufio 与 sync.Pool 优化）。
