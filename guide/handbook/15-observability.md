# 15 日志、指标与链路：三根支柱怎么互相定位问题

> 详细程度：标准 · 约 45 分钟
> 材料：W05-4 W08-1 W08-2 W18-1、C:dachang/trace、旧长版 `notes/p13-metrics.md` `notes/p15-slog.md` `notes/p16-slog-handler.md` `notes/p17-dnsproxi-metrics.md` `notes/p18-dnsproxi-slog.md`
> 前置：04 接口与组合（装饰器），06 并发一（context）

## 先看一个让人愣住的现象

```go
package main

import (
	"context"
	"log/slog"
	"os"
)

type ctxKey struct{}

type ctxHandler struct{ inner slog.Handler }

func (h ctxHandler) Enabled(ctx context.Context, l slog.Level) bool { return h.inner.Enabled(ctx, l) }
func (h ctxHandler) Handle(ctx context.Context, r slog.Record) error {
	if id, ok := ctx.Value(ctxKey{}).(string); ok {
		r.AddAttrs(slog.String("req_id", id))
	}
	return h.inner.Handle(ctx, r)
}
func (h ctxHandler) WithAttrs(a []slog.Attr) slog.Handler { return ctxHandler{h.inner.WithAttrs(a)} }
func (h ctxHandler) WithGroup(n string) slog.Handler { return ctxHandler{h.inner.WithGroup(n)} }

// ReplaceAttr 去掉时间戳，输出更短更可对照
var quiet = &slog.HandlerOptions{ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
	if a.Key == slog.TimeKey {
		return slog.Attr{}
	}
	return a
}}

var logger = slog.New(ctxHandler{inner: slog.NewJSONHandler(os.Stdout, quiet)})

func main() {
	ctx := context.WithValue(context.Background(), ctxKey{}, "r-42")
	logger.InfoContext(ctx, "cache hit", "key", "a.test")
	logger = logger.With("svc", "dns")
	logger.InfoContext(ctx, "cache miss", "key", "b.test")
	logger.Info("没有 ctx 的日志")
}
```

```text
{"level":"INFO","msg":"cache hit","key":"a.test","req_id":"r-42"}
{"level":"INFO","msg":"cache miss","svc":"dns","key":"b.test","req_id":"r-42"}
{"level":"INFO","msg":"没有 ctx 的日志","svc":"dns"}
```

三行对着看：调用处没写 `req_id`，前两条却有；`With("svc", "dns")` 换过 logger，`req_id` 没丢；第三条没传 `ctx`，`req_id` 立刻消失。干这事的既不是业务代码也不是库，是一个二十行的 `slog.Handler`：从 `ctx` 取出请求 ID，塞进每条日志。这个 ID 就是三根支柱的连接线：指标告警说"有问题"，靠它翻到那次请求的日志；链路说"慢在哪一段"，还是它把日志串起来。〔W08-1 p3-5〕

Go 把这条线做成显式的：进程内它活在 `context.Context` 里，所以**所有可能阻塞或调用外部的方法，第一个参数都应该是 `ctx`**。〔W08-2 p10〕〔W08-2 p11〕本节反复出现的形状就是：ID 放进 ctx，让每个环节自动带上，而不是每个函数手动传。

## 和你已知语言的差异

| 问题 | Java | Rust | Go |
|---|---|---|---|
| 日志 API | SLF4J + Logback，MDC 塞请求 ID | `tracing` 加 `#[instrument]` 传播字段 | 标准库 `log/slog`（1.21 起），可换 zap |
| 指标 | Micrometer | `metrics` / `prometheus` crate | `client_golang`，OpenTelemetry 也行 |
| 链路 | OTel agent 字节码注入，无侵入 | `tracing-opentelemetry`，靠 span 宏 | 手动或中间件、插件接入，没有字节码 agent |

Java 的 MDC 靠线程局部变量，agent 靠字节码注入，都是运行时魔法；Go 没有线程局部存储，也没有 JVM 式的字节码 agent，换来一条明路：ctx 是唯一载体，中间件注入，`slog.Handler` 取出。Rust 的 `tracing` 靠 `#[instrument]` 和 subscriber 自动带字段，最接近 Java 的体验；`slog` 没有对应的宏。所以缺口都得自己写中间件补，这正是本节的重点。

## 一、日志：先定级别和纪律，再选库

四级足够：DEBUG（线上不打）、INFO（线上默认）、WARN（不好但可容忍）、ERROR（需要关注）。多一级就多一份使用成本，FATAL 和 ERROR 很难分清。〔W05-4 p4〕打日志的时机不用纠结：与外部交互（数据库、缓存、RPC）的请求响应用 DEBUG 记，疑似不严重的问题用 WARN，原则是**怀疑该不该打，就打**，宁滥勿缺。〔W05-4 p5〕

几条硬纪律，动机都来自"日志会被谁看"：

1. **敏感信息不进日志**：手机号、密码不打，哪怕 DEBUG；绝不把 `error` 原样返回前端，错误细节只能进日志给你看，不能进响应给用户。第三方请求响应打 DEBUG 的前提是线上永远不开 DEBUG，否则里面的手机号照样落盘。〔W05-4 p7-8〕〔W05-4 p10〕
2. **频繁出现的"发送太频繁"用 WARN**：偶发是正常业务，频繁出现就可能是攻击。级别是给人看的信号。〔W05-4 p8〕
3. **注入 Logger，不用包变量**：全局 `zap.L()` 能用但不好，一旦同一项目要多个 Logger（例如敏感日志单独存储），所有调用点都得改；Logger 作为字段注入，将来只改构造函数。〔W05-4 p11〕〔W05-4 p12-13〕
4. **抽象有代价**：为不绑死日志库而自定义 API，就要把字段转换成库的字段，引入额外分配。三种风格各有取舍：占位符（兼容最好）、`Field{Key,Value}`（对分析友好但用着麻烦）、偶数个参数（key/value 交替，没有编译器检查，需要严格评审）。〔W05-4 p16〕〔W05-4 p18-20〕〔W05-4 p21〕

标准库 `log/slog` 的 `Handler` 接口让你决定"每条日志附带什么"，这是把 `req_id` 变成基础设施的地方，开头的 `ctxHandler` 就是全部：

- `Handle`：每条日志过这里，从 ctx 取 ID 加进 `slog.Record` 再转发。
- `WithAttrs`/`WithGroup`：**必须转发并重新包装成 `ctxHandler`**，否则 `With` 之后注入丢失，这是自定义 handler 最常见的坑。
- 调用要用带 ctx 的方法：`InfoContext(ctx, ...)` 才会把 ctx 递进 `Handle`，`Info(...)` 没有渠道，自然没有 `req_id`。

删掉两个转发方法试试：

```go fail
package main

import (
	"context"
	"log/slog"
)

type ctxHandler struct{ inner slog.Handler }

func (h ctxHandler) Enabled(ctx context.Context, l slog.Level) bool { return h.inner.Enabled(ctx, l) }
func (h ctxHandler) Handle(ctx context.Context, r slog.Record) error {
	return h.inner.Handle(ctx, r)
}

func main() {
	slog.New(ctxHandler{})
}
```

```text
ctxHandler does not implement slog.Handler (missing method WithAttrs)
```

编译器把"忘了转发"挡在上线之前，前提是你真的实现了这个接口。脱敏交给 `LogValuer`：值在输出时才展开，明文长在类型里，任何调用点都泄露不出去：

```go
package main

import (
	"log/slog"
	"os"
)

type phone string

// LogValue 在输出时才调用：明文只在类型里，不进日志
func (p phone) LogValue() slog.Value {
	s := string(p)
	return slog.StringValue(s[:3] + "****" + s[len(s)-4:])
}

var quiet = &slog.HandlerOptions{ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
	if a.Key == slog.TimeKey {
		return slog.Attr{}
	}
	return a
}}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, quiet))
	logger.Info("login", "user", phone("13812345678"))
}
```

```text
{"level":"INFO","msg":"login","user":"138****5678"}
```

显式传入的字段和注入的字段撞名是另一个边界。同一个 handler，调用里再写一个 `req_id`（ctx 里注入 id 是 `q-1791`），〔实测〕`go run main.go` 输出：

```text
{"level":"INFO","msg":"显式优先示例","key":"a","req_id":"manual-1","req_id":"q-1791"}
```

`req_id` 出现两次，值还不一样，检索时两条都算命中。dnsproxi 的做法是注入前先查重（`logger.go` 的 `recordHas`），显式传了就以显式为准；自己写 handler 要么同样查重，要么规定死"这个键只许注入"。

日志规模化后要集中检索，ELK 是常见方案：Logstash 收集、解析、转换，Elasticsearch 存储检索，Kibana 展示。〔W18-1 p4〕Filebeat 是常见的轻量采集器，Kibana 的数据源向导会提示装它，默认直传 ES；课程要的是经过 Logstash，两者路径不同。〔W18-1 p19〕两件事能让 Logstash 少干很多活：

- **结构化日志**：JSON 输出本身就是字段，不用写 grok 正则去解析文本。课程也说，结构化日志里 Kv 这类从文本里抠键值对的过滤插件用不太上。〔W18-1 p11〕
- **展示端分工**：Kibana 深度绑定 ES、专注日志；Grafana 多数据源、告警更成熟。通常指标放 Grafana，日志放 Kibana。〔W18-1 p14〕

文件落盘用 lumberjack 之类做切片。〔W18-1 p21〕课程说文件大了或时间变了就分割；按 lumberjack 自己的文档，它只在文件超过 `MaxSize` 时自动切，`MaxAge` 管的是旧文件保留多少天，想按天切要自己定时调 `Rotate()`。〔自，待核〕

## 二、指标：四种类型与一条生死纪律

指标类型有四种：Counter（只增，如请求总数）、Gauge（可增可减，如当前正在处理的请求数）、Histogram（采样分桶）、Summary（按百分位统计，如 99 线）。〔W08-1 p16-19〕Prometheus 是**拉模型**：服务端主动轮询，你的进程暴露 `/metrics`，它定时来抓。〔W08-1 p9-10〕`scrape_interval` 要注意，高并发应用不宜抓太频繁，抓取本身也是开销。〔W08-1 p11〕

先摆哪几个？**RED**：Rate（请求量）、Errors（错误数）、Duration（耗时）。用 `client_golang` 定义一个按 outcome 计数的 Counter：

```go
var queries = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "demo_queries_total",
	Help: "Queries by outcome.",
}, []string{"outcome"})
```

〔实测〕`go run main.go`（冒烟 3 次命中 1 次未命中后，经 `promhttp` 抓自己的 `/metrics`）输出：

```text
demo_queries_total{outcome="hit"} 3
demo_queries_total{outcome="miss"} 1
```

这就是 Prometheus 拉到的：每行一条时间序列，大括号里是 label。`hit` 除以总数是命中率，`miss` 加 `upstream_error` 是错误率。计时用 Histogram：

```go
var latency = promauto.NewHistogram(prometheus.HistogramOpts{
	Name:    "demo_upstream_duration_seconds",
	Help:    "Upstream round-trip.",
	Buckets: []float64{0.0001, 0.0005, 0.001, 0.0025, 0.005, 0.01, 0.05, 0.1, 0.5},
})
```

〔实测〕`go run main.go`（`Observe(0.0002)`、`Observe(0.4)` 各一次，抓 `/metrics`）输出：

```text
demo_upstream_duration_seconds_bucket{le="0.0001"} 0
demo_upstream_duration_seconds_bucket{le="0.5"} 2
demo_upstream_duration_seconds_bucket{le="+Inf"} 2
demo_upstream_duration_seconds_sum 0.4002
demo_upstream_duration_seconds_count 2
```

桶的原则是**让落到各区间的数据个数在同一个量级**，响应时间跨几个数量级，所以用非等间距。〔W08-1 p22〕看分位数有三个视角：绝对值（99 线慢不慢）、增长率（发版前后对比）、相对值（平均线和 99 线差距大，说明长尾明显）。〔W08-1 p29〕

然后是生死纪律。每个不同的 label 组合是一条独立时间序列，进程内不会自动回收，label 选错内存就爆：

```go
package main

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	bad  = promauto.NewCounterVec(prometheus.CounterOpts{Name: "demo_by_ip_total"}, []string{"client_ip"})
	good = promauto.NewCounterVec(prometheus.CounterOpts{Name: "demo_by_outcome_total"}, []string{"outcome"})
)

// 数一个 Collector 会产出多少条时间序列
func countSeries(c prometheus.Collector) int {
	ch := make(chan prometheus.Metric, 4096)
	go func() { c.Collect(ch); close(ch) }()
	n := 0
	for range ch {
		n++
	}
	return n
}

func main() {
	// 同样 1000 次自增，两种 label 选法
	for i := range 1000 {
		bad.WithLabelValues(fmt.Sprintf("10.0.%d.%d", i/256, i%256)).Inc()
	}
	for range 1000 {
		good.WithLabelValues("hit").Inc()
	}
	fmt.Println("IP 作 label:", countSeries(bad), "条序列")
	fmt.Println("outcome 作 label:", countSeries(good), "条序列")
}
```

```text
IP 作 label: 1000 条序列
outcome 作 label: 1 条序列
```

user id、原始 URL、trace id 一律不能当 label；`req_id` 每请求都不同，只能进日志。label 值只能来自有限集合：路由模板、状态码、方法、outcome。错误文本千变万化，要先归类再进指标：

```go
// 三类错误各归一个 reason，label 值只有这三个
func upstreamErrReason(err error) string {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "timeout"
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return "refused"
	}
	return "network"
}
```

〔实测〕`go run main.go`（超时、连接拒绝、普通 error 各一次）输出：`demo_upstream_errors_total{reason="network"} 1`、`{reason="refused"} 1`、`{reason="timeout"} 1`。`errors.As` 认超时，`errors.Is` 认连接拒绝，其余归 `network`，错误再包装也造不出新 label 值。

课程用中间件统计 HTTP 响应时间，并说响应时间上 Summary 用得比较多，同时会统计 Count 和 Sum。〔W08-1 p27-28〕本节示例改用 Histogram，形状是：进入记时刻，返回后取状态码加 `Observe`，label 取方法、路由模式、状态码三项。〔自〕

## 三、链路：trace、span 与 ctx 传播

链路的核心是 trace 和 span：一个请求一个 trace，每段调用一个 span，span 有父子关系；span 的长度是执行时间，span 之间的空隙是父 span 自己代码的时间，空隙又长又多就该补打点。〔W08-1 p3-5〕〔W08-1 p6〕

OpenTelemetry（otel）是 CNCF 主导的标准，统一 API，可以把数据发往 Jaeger、Prometheus 等后端，优先用 otel 的 API。〔W08-2 p3〕课程里链路后端用的是 Zipkin。〔W08-2 p22〕初始化设置 Propagator 和 TracerProvider，**Provider 要关闭**。〔W08-2 p4〕不关的后果是：批量上报的 span 还缓冲在内存里，进程退出时最后一批丢失。〔自〕span 生命周期一条规则：`Start` 传入的 ctx 里带着 span 信息，新 span 自动挂为子节点；每个 `Start` 配对一次 `End`，谁创建谁结束，惯用 `defer span.End()`。〔W08-2 p6〕接入位置有规律：

1. **入口自动**：Gin 加 `otelgin.Middleware`，每个请求自动建入口 span。〔W08-2 p14-15〕
2. **数据库自动**：GORM 加 tracing 插件，span 里带 SQL 等丰富的调试信息。〔W08-2 p16-17〕
3. **手动打点**：关键步骤、易错步骤、程序出口（调第三方）才手动加；缓存性能敏感，默认不打。〔W08-2 p18〕

没有 otel 时，最小实现也是这个形状：`traceMW` 从 header 取追踪 ID 塞进 ctx，业务代码只管 `logger.InfoContext(r.Context(), "kv get", "key", "a")`（logger 就是开头的 `ctxHandler`）：

```go
func traceMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Trace-Id")
		if id == "" {
			id = "t-local"
		}
		ctx := context.WithValue(r.Context(), ctxKey{}, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
```

〔实测〕`go run main.go`（httptest 发起带 `X-Trace-Id: t-abc` 的请求）输出：

```text
{"level":"INFO","msg":"kv get","key":"a","req_id":"t-abc"}
```

跨进程就是这个形状的翻版：出口把 ID 写进 header 或 gRPC metadata，入口的中间件取出来放回 ctx。〔自〕课程讲的是进程内靠 ctx 传〔W08-2 p10〕，跨进程的 Propagator 见 otel 初始化〔W08-2 p4〕。但 `context.WithValue` 传的数据**不会跨进程**，跨 RPC 必须放进 metadata 或 header。
gRPC 的 metadata key 必须匹配 `[0-9a-z-_.]+`，大写不合法，HTTP header 没有这条限制。〔C:dachang/trace/open_trace/rpc_tracer/client/grpc_client.go:58〕课件注释里"`user_id` 只在本进程内使用"，说的就是这个边界。

span 里的三类数据各有分工：Tag 侧重查询，Log 记录事件发生的时间点，Baggage 随 trace 传播给所有子孙 span，普通 Tag 和 Log 传不给后代。〔C:dachang/trace/open_trace/local_tracer.go:49-56〕span 之间的引用有两种：`ChildOf`（父依赖子的结果，例如等推荐服务返回）和 `FollowsFrom`（父不依赖子的结果，例如异步上报 UV）。〔C:dachang/trace/open_trace/local_tracer.go:68-77〕

context 的纪律全部适用：ctx 只做方法参数且是第一个参数；公共方法都带上；**不要放进结构体字段**，除非结构体本身就表达上下文。〔W08-2 p11〕父 ctx 取消则所有子 ctx 都取消，控制从上到下；`Value` 查找子先查自己再查祖先，从下到上。〔W08-2 p9〕进程内把 ctx 一路传下去，意味着所有方法都带 ctx 参数，这是 Go 业务代码的固定形状。〔W08-2 p10〕

## 四、三根支柱怎么接力

把中间件合起来才是可观测性：`traceMW` 注入 ID，`instrument` 记指标，`ctxHandler` 注日志，一次请求三路留痕。`instrument` 的核心是包住 `ResponseWriter` 拿状态码，退出时记数加计时：

```go
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// instrument：进入记时刻，退出记三个 label 加一次 Observe
func instrument(pattern string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		requests.WithLabelValues(r.Method, pattern, strconv.Itoa(sw.status)).Inc()
		latency.Observe(time.Since(start).Seconds())
	})
}
```

〔实测〕`go run main.go`（`instrument` 套 `traceMW` 套 handler，请求头带 `X-Trace-Id: t-abc`，再抓 `/metrics`）输出：

```text
{"level":"INFO","msg":"kv get","key":"a","req_id":"t-abc"}
demo_request_duration_seconds_bucket{le="0.01"} 1
demo_request_duration_seconds_count 1
demo_requests_total{method="GET",pattern="GET /kv/{key}",status="200"} 1
```

第一行是日志，后面是指标，同一次请求。`pattern="GET /kv/{key}"` 是路由模板不是原始 URL，label 基数有限；`time.Since` 在 `next` 返回之后执行，包住整个 handler。

接力这样发生。假设上游开始超时：

1. **指标告警**：`upstream_errors_total{reason="timeout"}` 增速变高，或 99 线抬高，Grafana 触发告警。告警形态是"某指标超过阈值且持续一段时间"，课程的例子是活跃请求数超阈值并持续一段时间才报，避免抖动误报。〔W08-2 p26〕
2. **链路定位**：按时间窗口找慢 trace，看哪个 span 变长，是上游、缓存还是自己的代码（span 之间的空隙）。〔W08-1 p6〕
3. **日志还原**：拿同一个 ID 搜日志，看那次请求的具体错误和参数。

```go
// 同一个 id 贯穿一次请求的每条日志
ctx := context.WithValue(context.Background(), ctxKey{}, "q-1791")
logger.WarnContext(ctx, "upstream read failed", "qname", "down.test", "err", "read: i/o timeout")
logger.InfoContext(ctx, "cache miss, forwarding", "qname", "down.test")
```

```text
{"level":"WARN","msg":"upstream read failed","qname":"down.test","err":"read: i/o timeout","req_id":"q-1791"}
{"level":"INFO","msg":"cache miss, forwarding","qname":"down.test","req_id":"q-1791"}
```

两行同一个 `req_id`：第一行是读上游超时的 WARN，第二行是转发前的 INFO，按 ID 一搜，那次请求的前后经过全在。

三者信息密度不同：指标最便宜、能长期保存、适合告警；链路和日志按请求存，成本高，要采样或限期。所以**指标做告警，链路和日志做定位**。Grafana 里两个数据源并存是常见形态：Prometheus 管指标，Zipkin 管链路。〔W08-2 p22〕

## 小结

1. 日志记发生了什么，指标回答多少、多快、多频繁，链路回答经过了哪里、每段多久；连接它们的是同一个请求 ID。〔W08-1 p3-5〕
2. ID 进程内靠 `context.Context` 传，跨进程靠 header 或 gRPC metadata 传；ctx 永远是第一个参数。〔W08-2 p10〕〔W08-2 p11〕
3. 自定义 `slog.Handler` 把 `req_id` 变成基础设施：`Handle` 注入，`WithAttrs`/`WithGroup` 必须重新包装转发，用 `InfoContext` 而不是 `Info`。
4. `LogValuer` 让脱敏长在类型里，比调用处的小心可靠。
5. 指标四种类型，先摆 RED；桶让各区间的数据个数同量级；label 值必须是有限集合，基数是生死纪律。〔W08-1 p16-19〕〔W08-1 p22〕
6. 错误先归类再进指标，label 值才收得住。
7. 链路接入点：入口和数据库自动，关键步骤和程序出口手动，缓存默认不打；每个 `Start` 配对 `End`。〔W08-2 p14-15〕〔W08-2 p16-17〕〔W08-2 p18〕〔W08-2 p6〕
8. 指标做告警，链路和日志做定位；告警要"超阈值且持续一段时间"。

## 常见坑

1. 把 `req_id`、user id、原始 URL 放进指标 label，Prometheus 内存暴涨。
2. 自定义 `slog.Handler` 的 `WithAttrs`/`WithGroup` 没转发，`With` 之后注入丢失。
3. 用不带 ctx 的 `log.Info`，日志里没有请求 ID。
4. span 创建后忘记结束（opentracing 叫 `Finish`，otel 叫 `End`），数据不上报；课件注释写的是 Finish 时 SpanContext 才写入后端。〔C:dachang/trace/open_trace/local_tracer.go:43〕
5. 把敏感信息写进日志，或把 `error` 原样返回给前端。
6. 抓取间隔过短，高并发下抓取本身成为负担。〔W08-1 p11〕
7. Provider 不 `Shutdown`，退出时最后一批 span 丢失。〔W08-2 p4〕

## 判断题与答案

先自己答，再看后面。

1. 为什么 `req_id` 能进日志但不能进指标？
2. `slog.With("svc","dns")` 之后，自定义 handler 为什么必须重写 `WithAttrs`？
3. `context.WithValue` 的 `trace_id` 能自动传到下游服务吗？
4. Histogram 的桶应该等间距吗？

**答案**

1. 日志按条存，一条多几个字节无妨；指标每个不同的 label 组合是一条常驻时间序列，`req_id` 每请求都不同，基数无限，内存撑不住。
2. `With` 返回的新 logger 持有的是 `WithAttrs` 的返回值；不重新包装，返回的就是内层裸 handler，注入逻辑被绕过了。
3. 不能。ctx 只在进程内有效，跨进程必须显式把 ID 写进 header 或 metadata，由对端中间件取回 ctx。
4. 不应该。响应时间跨几个数量级，等间距桶要么头部挤在一个桶里，要么尾部大片空桶；要让各区间的数据个数同量级。〔W08-1 p22〕

练习包：`exercises/p15slog`（JSON 字段、级别、`LogValuer` 脱敏、ctx 关联）、`exercises/p16slogh`（自动注入、`With` 链不丢、显式字段优先、双输出扇出）。

## 在项目里

- **日志**：`dnsproxi-go/logger.go` 的 `ctxHandler` 就是本节模式：`withRequestID`/`requestID` 把请求 ID 放进 ctx，`newQueryID` 用 UnixNano 生成 `q-数字`（生产跨实例要换 crypto/rand 或 ulid），`newLogger` 返回带注入的 `*slog.Logger`。它还处理了重复字段：`recordHas` 在注入前查重，显式传过 `req_id` 就不再注入，避免上面演示的双字段问题。`main()` 按 `DNS_LOG_LEVEL` 重置级别，查询路径上的日志走 `InfoContext`/`WarnContext`（如 `upstream read failed`），启动、关停这类与请求无关的日志用不带 ctx 的 `Info`/`Error`。
- **指标**：`dnsproxi-go/metrics.go` 按 RED 摆五个指标：`QueriesTotal{qtype,outcome}` 是 Rate，`outcome` 注释写的是 hit|miss|sinkhole|short|upstream_error，代码里实际还有 `rate_limited`；`UpstreamErrorsTotal{reason}` 是 Errors，`reason` 只有 timeout|refused|network，由 `upstreamErrReason` 归类；`UpstreamLatency` 直方图是 Duration，只在转发上游时 Observe；外加 `InFlight`（worker pool 的背压点）和 `CacheEntries`（逼近 CacheMax 没）两个 Gauge。wire 里的 qtype 是 `uint16` 有 65536 种，`qtypeLabel` 收成 9 个实名加 `OTHER`。`MetricsHandler` 就是 `promhttp.Handler`，抓取口独立于业务端口。
- 链路在 dnsproxi 里没有现成例子；日志里的 `req_id` 起的是最小的关联作用。

## 想深入:正文之外的干货

下面按单元补齐课件里正文没展开的必读点,都压缩成"机制加坑"。

### W05-4 日志工程剩下的干货

- **没有日志的代价**:系统状态没法确认,出问题没法定位,这是日志被放在第一位的理由。〔W05-4 p3〕
- **zap 的全局路线**:课程用 zap 演示,`zap.NewDevelopment()` 建 Logger,`zap.ReplaceGlobals(l)` 换掉全局 `zap.L()`,返回值是恢复函数,调一下就换回旧 logger。自己维护一个包变量和它没有本质区别,都卡在多 Logger 需求上。〔W05-4 p6〕〔W05-4 p11〕〔W05-4 p14〕
- **先想清楚在哪打**:系统出口是调第三方,入口是收请求返响应;能用 AOP(中间件)包的交给 AOP,包不到的手动打,`redis.Cmdable` 上百个方法逐个封装不现实。〔W05-4 p26〕〔W05-4 p31〕
- **读 Body 必须回填**:middleware 里 `GetRawData()` 读走 Body 后要 `io.NopCloser(bytes.NewBuffer(data))` 放回去,Body 是流只能读一次;不回填,后面的 Bind 拿到空 body,这是经典事故。〔W05-4 p27-28〕
- **记响应要包装 `gin.ResponseWriter`**:`gin.Context` 不暴露响应,重写 `WriteHeader`/`Write`/`WriteString` 把状态码和响应体截下来存进 AccessLog。〔W05-4 p29〕
- **AccessLog 要限长截断**:不限大小等于给攻击者开洞,长 URL、巨大 body 全落盘;要截断加脱敏。〔W05-4 p28〕
- **viper 配 zap**:`viper.UnmarshalKey("log", &cfg)` 直接反序列化到 zap 的 config,`cfg.Build()` 出 Logger,再用 IoC 收口到统一的初始化函数。〔W05-4 p23〕
- **什么时候才值得抽象**:中小项目直接用 zap、viper 没问题;中大型公司抽象自己的接口加适配器才有价值,课程里 viper(直接用)和 zap(包一层)正好是两种做法的对照。〔W05-4 p24〕
- **适配器和装饰器的分界**:装饰器前后始终是同一个接口,适配器必然跨接口,用在版本升级、接口对齐。〔W05-4 p22〕
- **日志开销的边界**:百万 QPS 之前不要考虑日志开销;真正要担心的是存储成本和告警噪音,不是 CPU。〔W05-4 p5〕〔W05-4 p32〕

### W08-1 指标埋点的其余做法

- **Summary 的 objectives**:key 是分位(0.5、0.9、0.99、0.999),value 是允许误差,越靠尾部要求越严,从 1% 收到 0.01%。〔W08-1 p23〕
- **Summary 与 Histogram 怎么选**:Summary 的分位数在客户端算,多实例之间没法聚合;Histogram 存桶,分位数由 Prometheus 服务端算。单实例看分位用 Summary,要跨实例聚合用 Histogram。〔W08-1 p19〕〔W08-1 p22〕
- **Vector 与两种 label**:固定 label 用 `ConstLabels`(如 instance_id),动态 label 运行时 `WithLabelValues`;`Observe` 就是一次观测。〔W08-1 p24〕
- **MustRegister 会 panic**:重复注册直接 panic,进程内只注册一次,惯用包级变量加 Init 函数。〔W08-1 p27〕〔W08-1 p38〕
- **Gauge 记活跃请求**:进入 `Inc()`,退出 `defer` 里 `Dec()`,一般只关心总量。〔W08-1 p30〕
- **GORM 自带 prometheus 插件**:`db.Use(prometheus.New(Config{...}))`,配 DBName、刷新间隔和 MySQL 采集器;`VariableNames` 没意义,别用。〔W08-1 p31〕
- **连接池指标怎么读**:`wait_count`、`wait_duration` 大,说明连接数不够要扩;`idle` 大,把最大空闲连接调小;`max_idletime_closed` 大,是最大空闲时间设得太短。〔W08-1 p32-33〕
- **统计 SQL 耗时用 Callback 不用 Hook**:Hook 是单向的;Callback 注册 `Before("*")` 和 `After("*")`,before 里 `db.Set` 存开始时刻,after 里取出来断言 `time.Time` 再 `Observe`。断言失败要直接 return,before 可能没跑,别 panic。〔W08-1 p34-35〕
- **分段错误码**:第一位是类型(2 成功、4 客户端、5 系统),中间两位是模块号,后三位是具体错误;监控只盯 5 开头,但 4xx 突增可能是攻击或前端 bug,业务上另设告警。规范和施行成本要折中,只给需要特殊关注的错误定义具体码。〔W08-1 p36〕
- **错误码埋点在 Wrap 里**:在 Wrap 系列方法里按返回的 Code 计数;别在 middleware 反序列化响应体再统计,性能差。〔W08-1 p38〕
- **第三方监控用装饰器**:短信按模板 id 当 label 记耗时;微信验证码接口要监控,纯内存的 AuthURL 不用;各家错误码五花八门,监控得侵入各自实现,没法统一。〔W08-1 p39-40〕
- **Redis Hook 算缓存命中率**:`ProcessHook` 包住 `next(ctx, cmd)`,按 `cmd.Name()` 区分命令;`err == redis.Nil` 判未命中。只有 Get 类命令能这么判,SET/DEL 的 Nil 要排除,否则命中率失真。〔W08-1 p41〕

### W08-2 otel 初始化与告警的补充

- **Context 接口四个方法**:`Deadline`(ok 为 false 表示没有过期时间)、`Done`(返回一个 channel,监听取消信号,最常用)、`Err`(主动取消是 Canceled,超时是 DeadlineExceeded)、`Value`。〔W08-2 p8〕
- **ctx 的两份职责**:除传值还管超时取消,`WithCancel`/`WithDeadline`/`WithTimeout` 都返回带 Done channel 的新实例;可观测性里传 ID 只是它的一半用途。〔W08-2 p7〕
- **Provider 的设置时机**:`otel.SetTracerProvider` 改的是全局状态,必须在进程启动早期设置;设晚了,中间件拿到的是 noop tracer,打了也没数据。〔W08-2 p4〕
- **Exporter 与批量上报**:Zipkin exporter 指向 `http://localhost:9411/api/v2/spans`,`WithBatcher` 可以配 `WithBatchTimeout(1s)`,攒一批再发。〔W08-2 p5〕
- **span 上的三类操作**:`AddEvent` 记时间点事件,`SetAttributes` 记属性(如短信模板 id),出错时 `RecordError` 把 error 记进 span。〔W08-2 p6〕〔W08-2 p18〕
- **GORM tracing 插件别双采**:`db.Use(tracing.NewPlugin(WithoutMetrics()))`,插件自带 metrics,和 Prometheus 插件重复,二选一。〔W08-2 p16〕
- **Grafana 告警怎么配**:Contact Point 定发给谁、怎么发(企业微信群机器人 webhook,邮箱要另配 SMTP),配完先 Test;规则形如 `WHEN max() IS ABOVE 阈值`,Evaluation group 每分钟评估,Pending period 设几分钟,连续满足才真告,防瞬时毛刺。〔W08-2 p24-26〕
- **一份告警清单**:慢查询(GORM 指标超 100ms,永远不该出现)、慢请求(按接口单独定阈值)、异常请求(非 2xx 或 error 次数)、系统状态(CPU、内存、goroutine 数持续偏高)、业务告警(如短信发送太频繁)。〔W08-2 p27〕

### W18-1 ELK 链路的落地细节

- **Logstash 三段是固定形状**:Input(文件、DB、MQ)收,Filter 解析过滤,Output 写 ES。课程的 pipeline:beats 输入监听 5044 收 Filebeat,`json` filter 把 `message` 字段展开,输出按天索引 `logstash-%{+YYYY.MM.dd}`,方便按时间删旧索引。〔W18-1 p6〕〔W18-1 p22〕
- **Filebeat 的定位**:轻量、实时盯文件变化、自动发现新文件,输出可以给 Logstash、ES 或 Kafka;和 Logstash 组合就是轻量采集加集中过滤。配置核心是 `enabled: true` 加 paths 通配符。〔W18-1 p20〕〔W18-1 p25〕
- **落盘目录和采集目录必须对齐**:应用写 `/var/log`(容器里注意权限),Filebeat 的 paths 指向同一目录,两边对不上等于白搭。〔W18-1 p20-21〕
- **lumberjack 参数是运维契约**:`MaxBackups` 乘 `MaxSize` 决定磁盘上限;`Compress=true` 省六成以上磁盘,但查旧日志要先解压。〔W18-1 p21〕
- **面试怎么讲可观测性**:单独讲 ELK 显单薄;把 ELK(日志)、Prometheus(指标)、OpenTelemetry(链路)合成一套方案,再配一个从发现、定位到解决的案例。〔W18-1 p27〕

没吸收进来的:各单元的选读条目(Grok/Date/Mutate/Kv 插件细节、compose 部署与 ES 7.13 版本对齐、指标命名分层等)和标注待核的版本问题;W05-4/W08-1/W08-2 的 p2 均为单元概览页,内容已全部由正文与本块覆盖。

## 想深入去哪查

- 日志：W05-4（级别、时机、敏感信息、Logger 抽象与注入）；ELK 见 W18-1（Logstash 过滤插件、Filebeat 路径、Kibana 与 Grafana 分工、lumberjack）。
- 指标：W08-1（四类型、Vector、中间件统计、Summary 三视角、GORM 插件与连接池指标）。
- 链路：W08-2（otel 初始化、Gin 与 GORM 接入、手动打点、context 全家）、`C:dachang/trace`（opentracing 的 Tag/Log/Baggage、gRPC 与 HTTP 传播、自实现 tracer）。
- 旧长版：`notes/p13-metrics.md`（RED、label 基数、runtime 指标）、`notes/p15-slog.md` 与 `notes/p16-slog-handler.md`（slog 与自定义 handler）、`notes/p17-dnsproxi-metrics.md`、`notes/p18-dnsproxi-slog.md`。
