---
title: 第 18 章 · 毕业项目日志落地：dnsproxi 的结构化 + req_id
---

# 第 18 章 · 毕业项目日志落地：dnsproxi 的结构化 + req_id


> 这章学完你能干什么：把 dnsproxi 的 `[hit]/[miss]` 自由文本全部换成 JSON 结构化输出，每条带 `req_id`（每查询生成，ctx 自动注入），并回答"离开 httpkv 的 HTTP 中间件，同一套纪律怎么在 UDP 上落地"。
> 前置：P15（slog 模型）、P16（自定义 handler 两坑）、P17（dnsproxi 指标）。
>
> 动手方式：在仓库里操作，不改 `dnsproxi-go/` 的任何文件。本章端口避开 P17：代理监听 25353，上游 25300，指标口 9253。输出里的时间戳和 `req_id` 每次可能不同；基于纳秒时间戳的 ID 只是示例，不能保证并发或多实例下唯一。

## 体系背景：日志是写给谁的字节流

服务进程启动时，操作系统给它三个文件描述符（fd，进程用来指代打开文件的整数）：0 是标准输入，1 是标准输出，2 是标准错误。

dnsproxi 把日志写到 fd 2。让它的 stdout 和 stderr 各进一个文件，发一次查询：

```sh
$ ./dnsproxi > fd1.out 2> fd2.err &
$ dig +noedns +noadflag @127.0.0.1 -p 25353 smoke.example.com A +short
10.0.0.1
$ wc -c < fd1.out; wc -c < fd2.err
       0
     486
$ lsof -p $PID -a -d 0,1,2
FD TYPE NAME
0u unix
1w REG /private/tmp/liao-b4/p18/fd1.out
2w REG /private/tmp/liao-b4/p18/fd2.err
```

stdout 一个字节没有，486 字节全在 stderr。服务本身不管日志去哪，只往 fd 2 写。落文件、转发、轮转，都是启动它的那一层（shell、systemd、容器运行时）的事。

这就是 12-factor 里"日志是事件流"的意思。Java 里 logback 默认也是往控制台写，JS 的 `console.error` 同理。

另一个体系事实是日志量。后面"日志量"一节会实测：一个每秒三万查询的代理，每次查询一行日志，一小时就是十几 GB。

## 起一个假上游

dnsproxi 需要一个上游 DNS。`dnsproxi-go/run.sh` 里内嵌了一个 Python 最小应答器，固定回 `10.0.0.1`。把它抽出来单独用：

```sh
$ sed -n '/^  python3 - .* <<.PY. &$/,/^PY$/p' dnsproxi-go/run.sh | sed '1d;$d' > upstream.py
$ python3 upstream.py 25300 &
fake upstream on :25300
```

再把代理编译到仓库外面，这样不会在仓库里留文件：

```sh
$ cd dnsproxi-go && go build -o /tmp/dnsproxi . && cd ..
```

## 跑起来

```sh
$ DNS_LISTEN=:25353 DNS_UPSTREAM=127.0.0.1:25300 DNS_CACHE=1 DNS_CACHE_MAX=100 \
  DNS_METRICS_LISTEN=:9253 /tmp/dnsproxi
{"time":"2026-10-12T19:51:26.430712+08:00","level":"INFO","msg":"dnsproxi-go starting","listen":":25353","upstream":"127.0.0.1:25300","sinkhole":"","cache":true,"max":100,"admit":"off","active_ttl":false,"worker_pool":0}
{"time":"2026-10-12T19:51:26.430783+08:00","level":"INFO","msg":"metrics endpoint up","addr":":9253"}
```

启动横幅是一条 JSON，配置全是字段，不再是一行自由文本。`DNS_CACHE_MAX` 必须设，缺省是 0，缓存不会生效。

## 一次查询一条事件

发三个查询，同一个域名两次：

```sh
$ for n in smoke other smoke; do
>   dig +noedns +noadflag @127.0.0.1 -p 25353 $n.example.com A +short
> done
10.0.0.1
10.0.0.1
10.0.0.1
```

代理的 stderr 多了三行：

```sh
{"time":"2026-10-12T19:51:47.327921+08:00","level":"INFO","msg":"cache miss, forwarding","qname":"smoke.example.com","qtype":"A","req_id":"q-1791805907327896000"}
{"time":"2026-10-12T19:51:47.333147+08:00","level":"INFO","msg":"cache miss, forwarding","qname":"other.example.com","qtype":"A","req_id":"q-1791805907333133000"}
{"time":"2026-10-12T19:51:47.340254+08:00","level":"INFO","msg":"cache hit","qname":"smoke.example.com","qtype":"A","ttl_left":60,"req_id":"q-1791805907340178000"}
```

miss 行有 `qname`、`qtype`、`req_id` 三个字段。hit 行多一个 `ttl_left`，并且没有转发的字样。每次查询的 `req_id` 都不一样。

## 为什么 dig 默认没有日志

上面的命令带了 `+noedns +noadflag`，不是装饰。去掉它们，直接 `dig @127.0.0.1 -p 25353 smoke.example.com A`，查询会正常返回，但 stderr 上没有 miss 或 hit。

看指标就明白了。用默认参数的 dig，以及只加 `+noedns` 的 dig，共发 6 次：

```sh
$ curl -s localhost:9253/metrics | grep '^dnsproxi_queries_total'
dnsproxi_queries_total{outcome="forwarded",qtype="A"} 6
```

6 次全是 `forwarded`，没有 `hit` 也没有 `miss`。同一个域名问两遍，也照样转发两遍。

原因在 `dnsproxi-go/cache/plan.go` 的 `Request`：缓存只接受"普通查询"，要求头部标志字节恰好是 RD=1，且附加记录数为 0。dig 默认带 EDNS 附加记录，还置了 AD 位，两条都不满足，查询被判为不可缓存，直接转发，也就不走打 miss 或 hit 日志的那段代码。

这是排障时的一个真实坑：日志里没有，不等于没发生。指标里的 `forwarded` 才是兜底的计数。

## req_id

req_id 是怎么到每一行里的？httpkv 靠 HTTP 中间件，dnsproxi 是 UDP，没有中间件。注入点在 `handleQuery` 入口：

```go
// dnsproxi-go/main.go，handleQuery 第一件事
ctx := withRequestID(context.Background(), newQueryID())
```

之后所有日志调用都用 `logger.InfoContext(ctx, ...)`，调用点只写业务字段：

```go
logger.InfoContext(ctx, "cache miss, forwarding", "qname", q.Name, "qtype", qtypeLabel(q.Type))
```

`req_id` 由 handler 在输出前从 ctx 里取出来加上。两种场景的差别：

| | httpkv（P15/P16） | dnsproxi（P18） |
|---|---|---|
| req_id 生成 | reqID 中间件（HTTP 层） | `handleQuery` 入口调用 `newQueryID()` 并放入 ctx |
| ctx 传播 | `r.WithContext` 随请求走 | 从 `context.Background()` 派生，供本次查询内部使用 |
| 回显 | `X-Request-ID` 响应头 | 本实现不回显；可结合时间和查询字段查日志 |
| 级别配置 | 代码写死 | `DNS_LOG_LEVEL` env |

UDP 上的对应物是"每查询一个 ctx"。Java 读者会想到 SLF4J 的 `MDC.put("req_id", ...)`，区别是 MDC 绑在线程上，Go 的 ctx 是显式传的参数。

## 把一次查询的日志串起来

同一次查询如果打了多条日志，它们共享同一个 `req_id`。制造这个场景：杀掉上游，再查一个新域名。

```sh
$ kill %1        # 上游
$ dig +noedns +noadflag +time=5 +tries=1 @127.0.0.1 -p 25353 down.example.com A +short
;; connection timed out; no servers could be reached
```

dig 超时了，因为代理没有回包。代理的日志：

```sh
$ tail -n 2 proxy.err | jq -r '[.req_id,.level,.msg] | @tsv'
q-1791805915999527000	INFO	cache miss, forwarding
q-1791805915999527000	WARN	upstream read failed
```

两行 `req_id` 相同，一个 `grep` 就能捞出这次查询的完整现场。第二行的 `err` 字段是 `read: connection refused`，上游端口没有进程在听。

## 级别

什么该进哪个级别：

| 级别 | dnsproxi 的内容 |
|---|---|
| Error | 写失败（client、upstream、metrics 口）、panic |
| Warn | 上游读失败（含超时：client 侧有兜底，不算 Error） |
| Info | 启动横幅、cache hit 和 miss、evict |
| Debug | expire 每轮统计、cache reject（频率不够没进缓存） |

用 `DNS_LOG_LEVEL=warn` 重起代理，重复前面的场景：先查两次 `smoke`，再停上游查 `down`。

```sh
$ DNS_LOG_LEVEL=warn DNS_LISTEN=:25353 ... /tmp/dnsproxi 2> warn.err
$ wc -l < warn.err
1
$ cat warn.err
{"time":"2026-10-12T19:52:11.862803+08:00","level":"WARN","msg":"upstream read failed","err":"read udp 127.0.0.1:53503->127.0.0.1:25300: read: connection refused","req_id":"q-1791805931862668000"}
```

启动横幅、hit、miss 都没了，只剩一条 warn。但指标一个不少：

```sh
$ curl -s localhost:9253/metrics | grep '^dnsproxi_queries_total'
dnsproxi_queries_total{outcome="forwarded",qtype="A"} 1
dnsproxi_queries_total{outcome="hit",qtype="A"} 1
dnsproxi_queries_total{outcome="miss",qtype="A"} 2
dnsproxi_queries_total{outcome="upstream_error",qtype="A"} 1
```

Debug 级别给运维排障用，放高频低值的信息。把缓存开到最小并打开准入过滤和后台回收，再用 debug 级别查五个不同域名：

```sh
$ DNS_CACHE_MAX=2 DNS_CACHE_ADMIT=tinylfu DNS_CACHE_ACTIVE_TTL=1 \
  DNS_CACHE_EXPIRE_LOG=1 DNS_CACHE_EXPIRE_HZ=2 DNS_LOG_LEVEL=debug ... /tmp/dnsproxi 2> dbg.err
$ jq -c '[.level,.msg]' dbg.err | sort | uniq -c | sort -rn
   5 ["INFO","cache miss, forwarding"]
   4 ["DEBUG","expire round"]
   3 ["DEBUG","cache reject"]
   2 ["INFO","cache hit"]
   1 ["INFO","metrics endpoint up"]
   1 ["INFO","dnsproxi-go starting"]
$ grep '"cache reject"' dbg.err | head -1
{"time":"2026-10-12T19:52:28.117827+08:00","level":"DEBUG","msg":"cache reject","qname":"c.example.com","cand_freq":1,"victim_freq":1,"req_id":"q-1791805948117390000"}
```

后台回收每秒 10 轮（默认），这里调成每秒 2 轮，两秒也产出 4 行。放到 info 级就会把日志冲爆。`reject` 在缓存满仓时，每次 miss 都可能发生。

还有一个细节：`DNS_CACHE_EXPIRE_LOG`（`Exchange` 开关）只控制要不要收集 expire 统计，`DNS_LOG_LEVEL` 控制要不要输出。两者正交。

## 日志量

每次查询一行日志，量到底多大？用仓库自带的压测器 `dnsproxi-go/dnsq`，它发普通查询，会走缓存路径：

```sh
$ cd dnsproxi-go && go build -o /tmp/dnsq ./dnsq && cd ..
$ /tmp/dnsq -target 127.0.0.1:25353 -dur 3s -conc 4 -rate 0 -names 100
sent=94653 recv=94653 lost=0 (0.00%)
elapsed=3.001s qps=31546
latency p50=82µs p90=229µs p99=851µs max=7.726ms n=94632
```

info 级别下，stderr 的结果：

```sh
$ wc -l < vol_info.err; wc -c < vol_info.err
   94655
14945073
$ jq -r .msg vol_info.err | sort | uniq -c
94553 cache hit
  100 cache miss, forwarding
    1 dnsproxi-go starting
    1 metrics endpoint up
```

3 秒产出 94655 行，共 14945073 字节，平均每行约 158 字节，约 5 MB/s。照这个速度，一小时是 17.9 GB。

同样的压测，换成 `DNS_LOG_LEVEL=warn`：

```sh
$ /tmp/dnsq -target 127.0.0.1:25353 -dur 3s -conc 4 -rate 0 -names 100
sent=138122 recv=138122 lost=0 (0.00%)
elapsed=3s qps=46033
latency p50=73µs p90=132µs p99=365µs max=3.45ms n=138082
$ wc -l < vol_warn.err
0
```

日志文件 0 行，qps 从 31546 升到 46033，p99 从 851µs 降到 365µs。这是同一台机器上各跑一次，有抖动，只能说明日志不是免费的。

结论是：压测或生产高 QPS 时，hit 这种每次都发生的事件不该默认打到 info。要么提到 debug，要么采样，聚合信息交给指标。

## ctxHandler

`req_id` 靠 `ctxHandler` 注入。`dnsproxi-go/logger.go` 是自包含的 97 行，不 import 任何练习包，dnsproxi 是独立 module。这就是"毕业检验"：离开脚手架，还能不能写出同样纪律的代码。

`ctxHandler` 实现 `slog.Handler` 接口的四个方法（`Enabled`、`Handle`、`WithAttrs`、`WithGroup`），和 httpkv 版逐行同构。变的是注入点和生命周期，不变的是骨架和 P16 的两个坑：

- `WithAttrs` 和 `WithGroup` 必须转发并重新包装。
- 注入前查重，调用点显式写的 `req_id` 优先。

`main()` 里 `logger = newLogger(os.Stderr, logLevel())` 重置包级变量。包级 logger 先建一个 info 级的兜底，测试和库代码引用时不会是 nil。

## 改一处看变化

验证"注入前查重"到底有没有用。在仓库外复制一份 `dnsproxi-go`，删掉 `Handle` 里的查重判断，并在 miss 日志调用点显式加一个 `"req_id", "manual-1"`：

```sh
$ cp -r dnsproxi-go /tmp/dp-copy && cd /tmp/dp-copy
$ # logger.go：Handle 里去掉 if !recordHas(r, "req_id")，直接 AddAttrs
$ # main.go：miss 日志加 "req_id", "manual-1"
$ go build -o ../dnsproxi-nodedup . && ../dnsproxi-nodedup ... 2> nodedup.err
$ sed -n 3p nodedup.err
{"time":"2026-10-12T19:53:19.42+08:00","level":"INFO","msg":"cache miss, forwarding","qname":"x.example.com","qtype":"A","req_id":"manual-1","req_id":"q-1791805999419963000"}
```

同一行 JSON 里有两个 `req_id`。JSON 规范不禁止重复键，但解析器的行为不一致。实测 `jq`、Python 的 `json` 和 Node 的 `JSON.parse` 都取最后一个，手写的 `manual-1` 悄悄丢了。

守护测试会抓住它：

```sh
$ go test -run TestLoggerExplicitBeatsInjected .
--- FAIL: TestLoggerExplicitBeatsInjected (0.00s)
    logger_test.go:39: req_id duplicated: {"time":"2026-10-12T19:53:51.470811+08:00","level":"INFO","msg":"both","req_id":"q-explicit","req_id":"q-ctx"}
FAIL
```

仓库里的原版测试在 `dnsproxi-go/logger_test.go`，一共四个：`TestLoggerInjectsReqID`、`TestLoggerExplicitBeatsInjected`、`TestLoggerWithAttrsKeepsInjection`、`TestLogLevelFallback`，在原版上全部通过。

## 日志与指标的分工

同一个 cache miss 事件，两根支柱各记各的：

| | 指标（P17） | 日志（P18） |
|---|---|---|
| 记什么 | `queries_total{qtype="A",outcome="miss"}` 加 1 | qname、qtype、req_id 全文 |
| 回答 | miss 率多少、趋势如何 | 哪个域名 miss 的、这次查询的完整链路 |
| 成本 | O(1) 计数器 | 每条一行 JSON |

指标丢掉 qname（标签基数太大），日志丢掉聚合（量太大）。告警看指标，比如 miss 率超过 80%。排障看日志，用 req_id 捞现场。

## 小结

1. UDP 没有 HTTP 中间件：req_id 的注入点是 `handleQuery` 入口的 `withRequestID(context.Background(), ...)`，生命周期是单次查询。
2. 毕业检验的实质：handler 骨架逐行同构，变的是注入点和生命周期。P16 的坑不用再踩。
3. 级别分层：Error 是写失败和 panic，Warn 是上游超时，Info 是 hit 和 miss，Debug 是 expirer 和 reject。
4. 日志不是免费的：info 级每查询一行，实测一小时约 17.9 GB。
5. 指标记聚合，日志记细节。日志里没有，指标里的 `forwarded` 能兜底。

## 练习

按"环境，跑起来，改一处"的顺序，上面已经走过一遍。现在自己重做，并回答。

**预测**，跑之前先写答案：

1. `DNS_LOG_LEVEL=warn` 起服务，hit 和 miss 行还在吗？panic 日志呢？
2. 命中缓存的请求，`handleQuery` 里会打几条日志？它们的 `req_id` 相同吗？
3. 把 `ctxHandler.Handle` 里的查重去掉，同时调用点显式传 `"req_id", ...`，输出会怎样？

**观察**，期望的结果：

- 启动横幅是 `msg:"dnsproxi-go starting"` 加字段化的配置。
- miss 行有 `qname`、`qtype`、`req_id` 三个字段。
- hit 行多一个 `ttl_left`，没有 forward 字样。
- `DNS_LOG_LEVEL=warn` 起服务，hit 和 miss 消失，只剩 warn 和 error。

**改造**：把 `newQueryID` 换成 ulid（`github.com/oklog/ulid`），验证排序性：同一毫秒内的 id 要单调。提示：熵源用 `crypto/rand`。

## 章末参考

### ctxHandler 现行实现

`Handle` 里做查重注入：`requestID(ctx)` 非空，且 record 里没有 `req_id` 才 `AddAttrs`。`WithAttrs` 和 `WithGroup` 转发并重新包装成 `&ctxHandler{...}`。`recordHas` 只遍历当前 `r.Attrs`，看不到通过 `With` 预置给内层的字段；分组后注入字段也会进入当前组。需要更强的去重或根字段保证时，须另行设计。P16 讲过这两个包装问题，完整实现见 `dnsproxi-go/logger.go`。

### 级别开关与收集开关

`DNS_LOG_LEVEL`（debug、info、warn、error，非法值回到 info）只控制输出门槛。`DNS_CACHE_EXPIRE_LOG` 控制要不要收集 expirer 统计。两者正交：关掉收集，Debug 级别再低也没有记录可输出。

### panic 日志为何没有 ctx

recover 的 defer 挂在 goroutine 入口，也就是 `dnsproxi-go/main.go` 读循环 spawn 处，此时 `handleQuery` 还没执行，查询 ctx 还没创建。panic 日志用不带 ctx 的 `logger.Error("query panic", ...)`，字段带 panic 值本身。panic 是进程级事件，不关联单次查询，这是有意为之。

### Rust 对照

| Rust（tracing） | Go（slog） | 差异 |
|---|---|---|
| `tracing::info!(qname = %n, "cache miss")` | `logger.InfoContext(ctx, "cache miss", "qname", n)` | 字段写法不同，都是结构化事件 |
| span 里的 `req_id` 字段 | ctx 里的值，handler 取出注入 | span 自动继承，Go 靠显式传 ctx |
| `tracing_subscriber::fmt().json()` | `slog.NewJSONHandler` | 都是 JSON 输出 |
| `EnvFilter` 读 `RUST_LOG` | 自己写 `logLevel()` 读 `DNS_LOG_LEVEL` | Go 标准库不读 env |
| `Layer` 自定义 | 自定义 `slog.Handler` | 都要处理 With 链 |

### Java/Kotlin 与 TS/JS 类比

| 概念 | Java / Kotlin | TS / JS |
|---|---|---|
| ctx 注入 req_id | SLF4J `MDC`，绑线程，Kotlin 协程用 `MDCContext` | pino 的 `logger.child({ req_id })`，或 `AsyncLocalStorage` |
| JSON 日志 | logback 的 JSON encoder | pino，默认写 stdout |
| 级别由 env 配 | Spring Boot 的 `logging.level` 配置 | `LOG_LEVEL` 环境变量，自己读 |
| 日志写到哪 | 控制台，由容器收集 | `console.error` 或 pino 的 destination |
| 重复 JSON 键 | 取决于解析库 | `JSON.parse` 取最后一个 |
