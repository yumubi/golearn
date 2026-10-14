# 15 日志、指标与链路：三根支柱怎么互相定位问题

> 详细程度：标准 · 约 25 分钟
> 材料：W05-4 W08-1 W08-2 W18-1、C:dachang/trace、旧长版 `notes/p13-metrics.md` `notes/p15-slog.md` `notes/p16-slog-handler.md` `notes/p17-dnsproxi-metrics.md` `notes/p18-dnsproxi-slog.md`
> 前置：04 接口与组合（装饰器），06 并发一（context）

## 这一节要建立的认知

可观测性是三种数据加一条连接线：**日志**记录发生了什么，**指标**回答多少、多快、多频繁，**链路**回答一次请求经过了哪里、每段多久。〔W08-1 p3-5〕单独哪一种都不够，它们的用法是接力：指标告警告诉你"有问题"，链路告诉你"慢在哪一段"，日志告诉你"那一次请求里到底发生了什么"。

把它们连起来的线是同一个 ID（`req_id` 或 `trace_id`）。它在进程内靠 `context.Context` 传，跨进程靠 HTTP header 或 gRPC metadata 传。所以 Go 里**所有可能阻塞或调用外部的方法，第一个参数都应该是 `ctx`**。〔W08-2 p10〕〔W08-2 p11〕

## 和你已知语言的差异

| 问题 | Java | Rust | Go |
|---|---|---|---|
| 日志 API | SLF4J + Logback | `tracing` / `log` | 标准库 `log/slog`（1.21 起），可换 zap |
| 请求级上下文怎么带 | MDC（线程本地） | `tracing` span 的上下文 | 显式 `ctx` 参数，没有线程本地变量 |
| 指标 | Micrometer | `metrics` / `prometheus` crate | `prometheus/client_golang`，OpenTelemetry 也行 |
| 链路 | OpenTelemetry agent 字节码注入 | `tracing-opentelemetry` | 手动或中间件、插件接入，没有 agent |

## 一、日志：先定级别和纪律，再选库

四级足够：DEBUG（线上不打）、INFO（线上默认）、WARN（不好但可容忍）、ERROR（需要关注）。多一级就多一份使用成本，FATAL 和 ERROR 很难分。〔W05-4 p4〕打日志的时机不用刻意：统一记录与外部交互（数据库、缓存、RPC）的请求和响应用 DEBUG，疑似不严重的问题用 WARN，原则是**怀疑该不该打，就打**。〔W05-4 p5〕

几条硬纪律：

1. **敏感信息不进日志**：手机号、密码不打，哪怕是 DEBUG；绝不把 `error` 原样返回前端。第三方请求响应打 DEBUG 的前提是线上不开 DEBUG。〔W05-4 p7-8〕〔W05-4 p10〕
2. **频繁出现的"发送太频繁"用 WARN**：偶发可接受，频繁可能是攻击。〔W05-4 p8〕
3. **注入 Logger，不用包变量**：全局 `zap.L()` 是 60 分实践，要多个 Logger（例如敏感日志单独存储）就得全改；把 Logger 存在结构体字段里，将来只改构造函数。〔W05-4 p11〕〔W05-4 p12-13〕
4. **抽象有代价**：为了不绑死某个日志库定义自己的 API，需要把字段转换成库的字段，会引入额外的内存分配。三种风格：占位符、`Field{Key,Value}`、偶数个参数（key/value 交替，没有编译器检查）。〔W05-4 p16〕〔W05-4 p21〕

标准库 `log/slog` 的 `Handler` 接口让你自己决定"每条日志附带什么"，这是把 `req_id` 变成基础设施的地方：

```go
type ctxHandler struct{ inner slog.Handler }

func (h ctxHandler) Handle(ctx context.Context, r slog.Record) error {
	if id, ok := ctx.Value(ctxKey{}).(string); ok {
		r.AddAttrs(slog.String("req_id", id))
	}
	return h.inner.Handle(ctx, r)
}
func (h ctxHandler) WithAttrs(a []slog.Attr) slog.Handler { return ctxHandler{h.inner.WithAttrs(a)} }
func (h ctxHandler) WithGroup(n string) slog.Handler      { return ctxHandler{h.inner.WithGroup(n)} }
```

〔实测〕用 `log.InfoContext(ctx, "cache hit", ...)` 输出 `"req_id":"r-42"`；`log.With("svc","dns").InfoContext(ctx, ...)` 依然带 `req_id`；没有 `ctx` 的 `log.Info` 不带。**`WithAttrs` 和 `WithGroup` 必须转发并重新包装**，否则 `With` 之后注入就丢了，这是自定义 handler 最常见的坑。调用日志时要用 `InfoContext`（带 ctx 的方法），才会走到注入逻辑。

日志在大规模下要集中检索，ELK 是常见方案：Filebeat 收集，Logstash 解析和转换，Elasticsearch 存储检索，Kibana 展示。〔W18-1 p4〕〔W18-1 p6〕结构化日志（JSON）让解析几乎不用写 grok。Kibana 专注日志和 ES，Grafana 通用多数据源、告警更成熟，通常指标放 Grafana、日志放 Kibana。〔W18-1 p14〕日志文件用 lumberjack 之类的库做切片。〔W18-1 p21〕

## 二、指标：四类型和一条生死纪律

指标类型有四种：Counter（只增，如请求总数）、Gauge（可增可减，如活跃请求）、Histogram（采样分桶）、Summary（分位数）。〔W08-1 p16-19〕Prometheus 是**拉模型**：服务暴露 `/metrics`，服务端定时抓取。〔W08-1 p9-10〕

先摆哪几个？**RED**：Rate（请求量）、Errors（错误数）、Duration（耗时）。dnsproxi 的做法：`dnsproxi_queries_total{qtype,outcome}` 是 Rate，`dnsproxi_upstream_errors_total{reason}` 是 Errors，`dnsproxi_upstream_duration_seconds` 是 Duration 直方图，外加一个活跃请求 Gauge 和一个缓存条目 Gauge。

Histogram 的桶要按"各区间数据个数同量级"来设，响应时间常用非等间距。〔W08-1 p22〕看分位数有三个视角：绝对值（99 线慢不慢）、增长率（发版前后对比）、相对值（平均线和 99 线的差距，反映长尾）。〔W08-1 p29〕

**生死纪律：label 基数必须有限。** 每个不同的 label 值是一条独立时间序列，永不回收：

```go
bad.WithLabelValues(fmt.Sprintf("10.0.%d.%d", i/256, i%256)).Inc() // 每个 IP 一条序列
```

〔实测〕`qtype` 加 `outcome` 两个有限枚举的指标只有 2 条序列，同样一个计数器若用客户端 IP 作 label，1000 个 IP 就是 1000 条。user id、原始 URL、trace id 一律不能当 label；`req_id` 每个请求都不同，只能进日志，不能进指标。label 值只能来自有限集合：路由模板、状态码、方法。

```go
queries.WithLabelValues("A", "hit").Inc()
```

〔实测〕通过 `promhttp` 抓到的文本是 `queries_total{outcome="hit",qtype="A"} 3` 这种形式。中间件里统计响应时间，课程用 `defer` 里 `Observe`，label 取方法、路由模式和状态码三项。〔W08-1 p27-28〕

## 三、链路：同一个 ID 贯穿一次请求

链路追踪的核心是 trace 和 span：一个请求一个 trace，每一段调用一个 span，span 有父子关系，span 的长度是执行时间，span 之间的空隙是父 span 自己的代码时间。〔W08-1 p3-5〕〔W08-1 p6〕

OpenTelemetry（otel）是 CNCF 的可观测性标准协议，统一 API，导出器适配 Zipkin、Jaeger、Prometheus 等，建议优先用 otel 的 API。〔W08-2 p3〕接入的位置有规律：

1. **入口自动**：Gin 加 `otelgin.Middleware`，每个请求自动建 root span。〔W08-2 p14-15〕
2. **数据库自动**：GORM 加 tracing 插件，span 带完整 SQL 和影响行数。〔W08-2 p16-17〕
3. **手动打点**：关键步骤、易出错步骤、程序出口（调第三方）才手动加；缓存访问性能敏感，默认不打。〔W08-2 p18〕
4. 每个 `Start` 必须配对 `End`，谁创建谁结束，通常 `defer span.End()`。〔W08-2 p6〕

**进程内传递靠 `context.Context`，跨进程靠 header 或 metadata**，中间件负责提取和注入。没有 otel 时，最小实现也是这个形状：

```go
func traceMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Trace-Id")
		ctx := context.WithValue(r.Context(), ctxKey{}, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
```

〔实测〕请求头带 `X-Trace-Id: t-abc` 时，后续用 `InfoContext(r.Context(), ...)` 的日志自动带 `"req_id":"t-abc"`，这就是三根支柱靠同一个 ID 接上的最小演示。

用 `context.WithValue` 传的数据**不会跨进程**，跨 RPC 必须放到 metadata 或 header。gRPC 的 metadata key 必须匹配 `[0-9a-z-_.]+`，大写不合法，HTTP header 则没有这条限制。〔C:dachang/trace/open_trace/rpc_tracer/client/grpc_client.go:55-56〕

Span 里的三类数据：Tag 侧重查询，Log 记录时间点事件，Baggage 随 trace 传播给所有子孙 span。〔C:dachang/trace/open_trace/local_tracer.go:42-57〕引用关系有两种：`ChildOf`（父依赖子的结果）和 `FollowsFrom`（父不依赖子的结果，例如异步上报）。〔C:dachang/trace/open_trace/local_tracer.go:62-78〕

`context.Context` 的使用纪律：作为方法第一个参数；公共方法都带；**不要放进结构体字段**，除非结构体本身就表达一个上下文。〔W08-2 p11〕父 `ctx` 取消，所有子 `ctx` 都取消，控制从上到下；`Value` 查找则是子先查自己再查祖先，从下到上。〔W08-2 p9〕

## 四、三根支柱怎么接力

一次线上问题的典型排查路径：

1. **指标告警**：`queries_total{outcome="upstream_error"}` 增速升高，或 99 线抬高。
2. **链路定位**：按时间窗口找慢 trace，看哪个 span 变长（是数据库、缓存还是下游）。
3. **日志还原**：拿 trace id，在日志里搜同一个 ID，看那次请求里的具体错误和参数。

三者的信息密度不同：指标最便宜、能长期保存、适合告警；链路和日志按请求存，成本高，要采样或限期。所以**指标做告警，链路和日志做定位**。

## 常见坑

1. 把 `req_id`、user id、原始 URL 放进指标 label，Prometheus 内存暴涨。
2. 自定义 `slog.Handler` 的 `WithAttrs`/`WithGroup` 没转发，`With` 之后注入丢失。
3. 用不带 ctx 的 `log.Info`，日志里没有请求 ID。
4. `span` 创建后忘记 `End`，数据不上报。〔C:dachang/trace/open_trace/local_tracer.go:39-40〕
5. 把敏感信息写进日志，或把 `error` 原样返回给前端。
6. 抓取间隔过短，高并发下抓取本身成为负担。〔W08-1 p11〕
7. Provider 不 `Shutdown`，退出时最后一批 span 丢失。〔W08-2 p4〕

## 判断题

1. 为什么 `req_id` 能进日志但不能进指标？
2. `slog.With("svc","dns")` 之后，自定义 handler 为什么必须重写 `WithAttrs`？
3. `context.WithValue` 的 `trace_id` 能自动传到下游服务吗？
4. Histogram 的桶应该等间距吗？

练习包：`exercises/p15slog`（JSON 字段、级别、`LogValuer` 脱敏、ctx 关联）、`exercises/p16slogh`（自动注入、`With` 链不丢、显式字段优先、双输出扇出）。

## 在项目里

- **日志**：`dnsproxi-go/logger.go` 的 `ctxHandler` 就是上面的模式，`withRequestID`/`requestID` 把请求 ID 放进 ctx，`newLogger` 返回带注入的 `*slog.Logger`。它还处理了重复字段：`recordHas` 在注入前查重，避免显式传入的 `req_id` 与注入的并存。
- **指标**：`dnsproxi-go/metrics.go` 的 `QueriesTotal`（`qtype`、`outcome`）、`UpstreamErrorsTotal`（`reason`）、`UpstreamLatency`、`InFlight`、`CacheEntries`，label 全是有限枚举，`metricsServer` 暴露 `/metrics`。
- 链路在 dnsproxi 里没有现成例子；日志里的 `req_id` 起的是最小的关联作用。

## 想深入去哪查

- 日志：W05-4（级别、时机、敏感信息、Logger 抽象与注入）；ELK 见 W18-1。
- 指标：W08-1（四类型、Vector、中间件统计、三视角）。
- 链路：W08-2（otel 初始化、Gin 与 GORM 接入、手动打点、`context` 全家）、`C:dachang/trace`（opentracing、自实现 trace、gRPC 与 HTTP 传播）。
- 旧长版：`notes/p13-metrics.md`（RED、label 基数、runtime 指标）、`notes/p15-slog.md` 与 `notes/p16-slog-handler.md`（slog 与自定义 handler）、`notes/p17-dnsproxi-metrics.md`、`notes/p18-dnsproxi-slog.md`。
