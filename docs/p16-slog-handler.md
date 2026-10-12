---
title: 第 16 章 · 自定义 slog handler：把注入做成基础设施
---

# 第 16 章 · 自定义 slog handler：把注入做成基础设施


> 这章学完你能干什么：写一个包装 handler，让 `req_id` 从 ctx 自动进日志，调用点只写业务字段。同一条日志还能同时写给人看和给机器查。
> 读者：读完 P15。前置：P15（slog 四对象模型）、P2（接口与嵌入）。后续：P18 在 dnsproxi 重写这套手艺，P13 路线图里的 trace 关联也走这章的提取接口。
>
> 动手方式：开一个空目录，`go mod init demo`。下面出现的 `ctx.go`、`handler.go`、`multi.go` 都放进去，每个例子的 `main.go` 单独替换，用 `go run .` 运行。

## 体系背景：一条日志要付多少钱

一次 `logger.Info` 看着轻，背后有三笔账。先格式化成字节，再抢一把锁，最后调一次 `Write`。

对 `os.File` 来说，`Write` 最终是一个 write 系统调用。系统调用要陷入内核，比用户态函数贵得多。

先验证"一条记录一次 `Write`"。8 个 goroutine 各打 125 条，用一个只会计数的 writer 接住：

```go
type countingWriter struct {
	writes atomic.Int64
	mu     sync.Mutex
	buf    strings.Builder
}

func (c *countingWriter) Write(p []byte) (int, error) {
	c.writes.Add(1)
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

logger := slog.New(slog.NewJSONHandler(w, nil))
var wg sync.WaitGroup
for g := range 8 {
	wg.Go(func() {
		for i := range 125 {
			logger.Info("tick", "g", g, "i", i)
		}
	})
}
wg.Wait()
// 之后把 w.buf 按行切开，逐行 json.Unmarshal，统计损坏行数
```

```sh
$ go run .
日志条数: 1000
Write 调用次数: 1000
输出行数: 1000 损坏行数: 0
```

1000 条日志，1000 次 `Write`，没有一行被并发写坏。`JSONHandler` 内部加锁，保证一条记录整行写出。

这把锁和这次写，就是日志的主要成本。下面是四个基准，分别是级别被过滤、写 `io.Discard`、写 `/dev/null` 和多一层注入的 `CtxHandler`：

基准代码放在 `bench_test.go` 里，和 `ctx.go`、`handler.go` 同目录：

```go
func BenchmarkDisabled(b *testing.B) {
	l := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelWarn}))
	b.ReportAllocs()
	for b.Loop() {
		l.Info("tick", "k", 1)
	}
}
// BenchmarkEnabledDiscard：级别放开，写 io.Discard
// BenchmarkEnabledDevNull：写 os.DevNull 打开的文件
// BenchmarkCtxHandlerDevNull：和上一个基准一样，外面包一层 NewCtxHandler，用 InfoContext
```

```sh
$ go test -bench . -run xxx -count 3
BenchmarkDisabled-10             	139562498	         8.443 ns/op	       0 B/op	       0 allocs/op
BenchmarkDisabled-10             	144960726	         7.542 ns/op	       0 B/op	       0 allocs/op
BenchmarkDisabled-10             	195467620	         6.286 ns/op	       0 B/op	       0 allocs/op
BenchmarkEnabledDiscard-10       	 2706700	       409.1 ns/op	       0 B/op	       0 allocs/op
BenchmarkEnabledDiscard-10       	 3324148	       355.9 ns/op	       0 B/op	       0 allocs/op
BenchmarkEnabledDiscard-10       	 3387603	       356.6 ns/op	       0 B/op	       0 allocs/op
BenchmarkEnabledDevNull-10       	 1000000	      1367 ns/op	       0 B/op	       0 allocs/op
BenchmarkEnabledDevNull-10       	  918088	      2375 ns/op	       0 B/op	       0 allocs/op
BenchmarkEnabledDevNull-10       	  894162	      1525 ns/op	       0 B/op	       0 allocs/op
BenchmarkCtxHandlerDevNull-10    	 1000000	      1248 ns/op	      48 B/op	       1 allocs/op
BenchmarkCtxHandlerDevNull-10    	  973992	      1567 ns/op	      48 B/op	       1 allocs/op
BenchmarkCtxHandlerDevNull-10    	  726589	      1654 ns/op	      48 B/op	       1 allocs/op
```

三个结论。级别被过滤时只要 6-8 ns，零分配，所以 `Enabled` 是最便宜的优化。格式化本身约 360-410 ns。真去写文件，涨到 1.4-2.4 µs，其中系统调用占了大头。

最后一组说明两点：包装一层注入多 1 次分配（48 B，就是注入的那个 attr），而时间被写文件的抖动盖过了。本机 Apple M4，数字只看量级。

这就是自定义 handler 的舞台：每条日志都经过它，所以它要小、要稳、不能出错。

## 调用点的负担

P15 留下一个问题：`req_id` 要每处显式带。

```go
logger.WarnContext(ctx, "kv miss", "req_id", requestID(ctx), "key", k, "status", 404)
```

这有两个麻烦。一是纪律靠人：新同事忘了带，那条日志在日志系统里就关联不上请求。二是横切逻辑散落：将来加 `trace_id`、`tenant_id`，所有调用点再改一遍。

目标是把调用点瘦身成这样：

```go
logger.WarnContext(ctx, "kv miss", "key", k, "status", 404)
```

`req_id` 照样出现在输出里，由 handler 注入。

Java 读者会想到 Logback 的 MDC：把 `req_id` 放进线程本地变量，layout 自动打出来。Go 的 ctx 是显式传递的，所以 MDC 的角色由 handler 加 ctx 扮演。JS 读者可以对应 `AsyncLocalStorage`。Rust 的 `tracing` 靠 span 传播字段，`#[instrument]` 一标就行，标准库 slog 没有 span，但 handler 接口给了同等的扩展点。

## Handler 接口

`slog.Handler` 有四个方法：

```go
type Handler interface {
	Enabled(ctx context.Context, level Level) bool
	Handle(ctx context.Context, r Record) error     // 格式化并写出
	WithAttrs(attrs []Attr) Handler                 // logger.With 走这里
	WithGroup(name string) Handler                  // logger.WithGroup 走这里
}
```

只想改 `Handle` 怎么办？P2 学过的嵌入正好派上用场：嵌入内层 handler，只覆盖 `Handle`，其余三个方法靠方法提升。

## 注入

先写最偷懒的版本，先放 `ctx.go`，提供 ctx 里存取 id 的函数、提取函数，以及一个去掉时间字段的选项（让输出稳定）：

```go
type ctxKey struct{}

func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}

func reqIDExtract(ctx context.Context) []slog.Attr {
	if id := RequestID(ctx); id != "" {
		return []slog.Attr{slog.String("req_id", id)}
	}
	return nil
}

var quiet = &slog.HandlerOptions{ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
	if a.Key == slog.TimeKey {
		return slog.Attr{}
	}
	return a
}}
```

再写偷懒的 handler。`main.go`：

```go
type LazyHandler struct {
	slog.Handler
}

func (h LazyHandler) Handle(ctx context.Context, r slog.Record) error {
	r.AddAttrs(slog.String("req_id", RequestID(ctx)))
	return h.Handler.Handle(ctx, r)
}

ctx := WithRequestID(context.Background(), "req-7")
lazy := slog.New(LazyHandler{slog.NewJSONHandler(os.Stdout, quiet)})
lazy.InfoContext(ctx, "直接打")
lazy.With("svc", "httpkv").InfoContext(ctx, "With 之后")
```

```sh
$ go run .
{"level":"INFO","msg":"直接打","req_id":"req-7"}
{"level":"INFO","msg":"With 之后","svc":"httpkv"}
```

第一条看着对。第二条的 `req_id` 不见了，也没有任何报错。

原因就在嵌入。`With` 调用的是提升上来的 `WithAttrs`，它返回的是内层的裸 handler，我们的 `Handle` 被丢掉了。这就是包装 handler 的第一个坑：`WithAttrs` 和 `WithGroup` 必须转发，并且重新包一层。

## WithAttrs 与 WithGroup

正确的写法把四个方法都实现，`handler.go`：

```go
type CtxHandler struct {
	inner   slog.Handler
	extract func(context.Context) []slog.Attr
}

func NewCtxHandler(inner slog.Handler, extract func(context.Context) []slog.Attr) slog.Handler {
	return &CtxHandler{inner: inner, extract: extract}
}

func (h *CtxHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h *CtxHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, a := range h.extract(ctx) {
		if !recordHas(r, a.Key) {
			r.AddAttrs(a)
		}
	}
	return h.inner.Handle(ctx, r)
}

func (h *CtxHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &CtxHandler{inner: h.inner.WithAttrs(attrs), extract: h.extract}
}

func (h *CtxHandler) WithGroup(name string) slog.Handler {
	return &CtxHandler{inner: h.inner.WithGroup(name), extract: h.extract}
}

func recordHas(r slog.Record, key string) bool {
	found := false
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			found = true
			return false
		}
		return true
	})
	return found
}
```

`Enabled` 直接转发，级别判断是内层的职责。`WithAttrs` 和 `WithGroup` 转发后再包一层 `CtxHandler`，注入就不会在 `With` 链上丢失。`recordHas` 的用途下一节讲。

换成它再跑同样的三行：

```go
h := NewCtxHandler(slog.NewJSONHandler(os.Stdout, quiet), reqIDExtract)
logger := slog.New(h)
logger.InfoContext(ctx, "直接打")
logger.With("svc", "httpkv").InfoContext(ctx, "With 之后")
logger.Info("没有 ctx")
```

```sh
$ go run .
{"level":"INFO","msg":"直接打","req_id":"req-7"}
{"level":"INFO","msg":"With 之后","svc":"httpkv","req_id":"req-7"}
{"level":"INFO","msg":"没有 ctx"}
```

`With` 之后注入还在。ctx 里没有 id 时也没有空字段，输出保持干净。练习 `TestWithAttrsKeepsInjection` 专测这个坑，`TestNoCtxNoField` 测没有 id 的情况，`TestAutoInjection` 测最基本的注入。

接口断言有个顺带的坑。方法是指针接收者，所以断言要写 `(*CtxHandler)(nil)`：

```go
var _ slog.Handler = CtxHandler{}
```

```sh
$ go run .
# p16demo
./main.go:5:22: cannot use CtxHandler{} (value of struct type CtxHandler) as slog.Handler value in variable declaration: CtxHandler does not implement slog.Handler (method Enabled has pointer receiver)
```

写成 `(*CtxHandler)(nil)` 就能编译通过。

## 同名字段

第二个坑：调用点已经显式写了 `req_id`，handler 又注入一遍，会怎样？先看标准 handler 怎么对待同名 attr：

```go
plain := slog.New(slog.NewJSONHandler(os.Stdout, quiet))
plain.Info("t", "req_id", "explicit", "req_id", "second")
```

```sh
{"level":"INFO","msg":"t","req_id":"explicit","req_id":"second"}
```

它不去重，两个 `req_id` 并存，首值在前。读的人用 `jq` 或 `Unmarshal` 到 map，取到哪一个由读取端的实现决定。

那朴素的注入会怎样？用一个转发都对、但注入时不查重的 `NaiveHandler` 对比：

```go
naive := slog.New(NaiveHandler{slog.NewJSONHandler(os.Stdout, quiet)})
naive.InfoContext(ctx, "朴素注入", "req_id", "explicit")

good := slog.New(NewCtxHandler(slog.NewJSONHandler(os.Stdout, quiet), reqIDExtract))
good.InfoContext(ctx, "查重注入", "req_id", "explicit")
good.InfoContext(ctx, "没有显式值")
```

```sh
$ go run .
{"level":"INFO","msg":"t","req_id":"explicit","req_id":"second"}
{"level":"INFO","msg":"朴素注入","req_id":"explicit","req_id":"req-7"}
{"level":"INFO","msg":"查重注入","req_id":"explicit"}
{"level":"INFO","msg":"没有显式值","req_id":"req-7"}
```

朴素注入产生了重复 key。查重版本做到了"显式说了算，没写才注入"，这就是 `recordHas` 的用途。

练习 `TestExplicitBeatsInjected` 断言的就是这个。这个行为是实测出来的：P15 阶段假设"注入先加也没事"，冒烟发现输出里 `req_id` 重复、读侧取值不可控，当场被打脸。

## 提取函数

`CtxHandler` 不硬编码 request id，而是收一个提取函数。明天要换成 trace_id，只换这个函数，handler 骨架不动：

```go
func byTrace(ctx context.Context) []slog.Attr {
	if id, ok := ctx.Value(traceKey{}).(string); ok {
		return []slog.Attr{slog.String("trace_id", id)}
	}
	return nil
}

logger := slog.New(NewCtxHandler(slog.NewJSONHandler(os.Stdout, quiet), byTrace))
ctx := context.WithValue(context.Background(), traceKey{}, "4bf92f3577b34da6")
logger.InfoContext(ctx, "handled", "status", 200)
```

```sh
$ go run .
{"level":"INFO","msg":"handled","status":200,"trace_id":"4bf92f3577b34da6"}
```

换 OTel 时，提取函数改成从 ctx 的 span context 拼 `trace_id`。这是 P13 路线图（日志关联 trace）预留的接口。

## ctx key

注入最阴险的失败不是崩溃，是静默失效。第一次冒烟时 `req_id` 没出现，没有任何报错。

排查发现，中间件塞的是 httpkv 自己定义的 `reqIDKey struct{}`，提取函数取的是 `p15slog` 里的另一个 key。两个不同类型的 key，`ctx.Value` 返回 nil，提取返回空，注入悄悄跳过。

复现很简单，用另一个类型的 key 塞同一个 id：

```go
type otherKey struct{}

ok := WithRequestID(context.Background(), "req-1")
logger.InfoContext(ok, "同一个 key")

wrong := context.WithValue(context.Background(), otherKey{}, "req-2")
logger.InfoContext(wrong, "另一个 key")
```

```sh
$ go run .
{"level":"INFO","msg":"同一个 key","req_id":"req-1"}
{"level":"INFO","msg":"另一个 key"}
```

第二条没有 `req_id`，也没有报错。教训有两条。

**（1）ctx key 必须全仓库单一来源。** 修复是 httpkv 删掉自己的 key，统一用 `p15slog.WithRequestID` 和 `RequestID`。

**（2）静默失效比崩溃可怕。** 如果注入是硬需求，提取函数返回空时可以给 debug 级别提示。练习里没做，留给改造题。

## MultiHandler

调试想看文本，落盘想要 JSON，同一条日志要去两个地方。写一个 `MultiHandler` 把记录扇出到多个 handler：

```go
type multiHandler []slog.Handler

func MultiHandler(handlers ...slog.Handler) slog.Handler { return multiHandler(handlers) }

func (m multiHandler) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range m {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (m multiHandler) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, h := range m {
		if !h.Enabled(ctx, r.Level) {
			continue
		}
		if err := h.Handle(ctx, r); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
```

`WithAttrs` 和 `WithGroup` 逐个转发，结果重新组成 `multiHandler`，写法和前面一样，这里不重复。

文件只收 Warn 以上，终端收 Debug 以上：

```go
logger := slog.New(MultiHandler(
	slog.NewJSONHandler(&file, &slog.HandlerOptions{Level: slog.LevelWarn, ReplaceAttr: quiet.ReplaceAttr}),
	slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug, ReplaceAttr: quiet.ReplaceAttr}),
))
logger.Debug("only text")
logger.Warn("both", "k", 1)
```

```sh
$ go run .
level=DEBUG msg="only text"
level=WARN msg=both k=1
文件里:
{"level":"WARN","msg":"both","k":1}
```

Debug 只到了终端，Warn 两边都有。两个语义要点：`Enabled` 是"任一感兴趣就放行"，否则全是 Debug 级 sink 的组里没人收 Info。`Handle` 尽力而为，每个 sink 先查自己的 `Enabled`。

再看一个目的地坏了会怎样：

```go
bad := MultiHandler(
	slog.NewJSONHandler(failWriter{}, nil),   // Write 永远返回 disk full
	slog.NewTextHandler(os.Stdout, quiet),
)
slog.New(bad).Info("一个目的地坏了")
fmt.Println("Handle 返回:", bad.Handle(ctx, record))
```

```sh
level=INFO msg=一个目的地坏了
level=INFO msg="直接 Handle"
Handle 返回: disk full
```

文本 sink 照常输出，错误被收集后一次返回。一个目的地故障不拦其他。练习 `TestMultiHandler` 验证扇出，`TestMultiHandlerLevel` 验证两个 sink 各自过滤。

Kotlin 和 Java 读者会觉得眼熟：这相当于给 Logback 配了多个 appender。JS 里 pino 的 transport 做的是同一件事。

## 真机验证

把 `CtxHandler` 接进 httpkv 看真实效果。`NewCtxLogger` 就是 JSON 输出加 `req_id` 自动注入的封装：

```sh
$ go run ./exercises/p8fast/httpkv/ &
$ curl -si localhost:8090/kv/nope
HTTP/1.1 404 Not Found
Content-Type: application/json
X-Request-Id: req-1791805945162727000
Date: Mon, 12 Oct 2026 11:52:25 GMT
Content-Length: 26

{"error":"key not found"}
```

服务日志里对应的那一行：

```text
{"time":"2026-10-12T19:52:25.162739+08:00","level":"WARN","msg":"kv miss","key":"nope","status":404,"req_id":"req-1791805945162727000"}
```

响应头的 `X-Request-Id` 和日志里的 `req_id` 一致。回到 `exercises/p8fast/httpkv/main.go` 看这行日志的调用点，只有 `key` 和 `status`，没有 `req_id`。

写入请求同理，`kv put` 那一行也带着自己的 `req_id`，调用点同样没写：

```sh
$ curl -s -X PUT localhost:8090/kv/a -d '{"value":"1"}' -o /dev/null -w "%{http_code}\n"
201
```

```text
{"time":"2026-10-12T19:52:25.171278+08:00","level":"INFO","msg":"kv put","key":"a","req_id":"req-1791805945171185000"}
```

## 小结

1. 横切字段下沉到 handler：调用点只写业务字段，注入、脱敏、格式化是基础设施的事。
2. 包装 handler 有两个坑：`WithAttrs` 和 `WithGroup` 必须转发并重包，标准 handler 对同名 attr 不去重，所以注入前要查重。
3. ctx key 全仓库单一来源，两套 key 的注入静默失效。
4. 提取函数是依赖注入，换 OTel 只换它。
5. `MultiHandler` 的 `Enabled` 是"任一感兴趣"，级别过滤交给各 sink。

## 练习：p16slogh

练习在 `exercises/p16slogh/`，httpkv 走 `NewCtxLogger`。先跑一遍：

```sh
$ go test -race ./exercises/p16slogh/ ./exercises/p8fast/httpkv/
ok  	golearn/exercises/p16slogh	1.416s
ok  	golearn/exercises/p8fast/httpkv	1.315s
```

七个测试各检验一件事：

- `TestAutoInjection`：调用点零字段，`req_id` 在输出里。
- `TestNoCtxNoField`：ctx 没有 id 时不出现空字段。
- `TestWithAttrsKeepsInjection`：`With` 链不丢注入。
- `TestExplicitBeatsInjected`：显式字段赢过注入。
- `TestLevelForwarding`：级别过滤穿透包装。
- `TestMultiHandler`：多 sink 扇出。
- `TestMultiHandlerLevel`：两个 sink 各自过滤。

**预测**，跑之前先写答案：

1. `logger.With("svc", "x")` 之后再打日志，`req_id` 还在输出里吗？`WithAttrs` 忘了转发会怎样？
2. 调用点显式写 `"req_id", "override"`，输出里是注入值还是 override？为什么？
3. 塞了 id 但提取函数查的 key 不一样，会报错吗？

**观察**：起 httpkv，`curl -si localhost:8090/kv/nope`。确认响应头的 `X-Request-Id` 和日志里 `kv miss` 那行的 `req_id` 相同，再去 `main.go` 里确认调用点没写 `req_id`。

**改造**：给 `CtxHandler` 加"注入缺失告警"。提取函数返回空且记录级别不低于 Info 时，往 stderr 打一行提示。用 `fmt`，别用 slog 自己，否则会递归。

## 章末参考

### MultiHandler 的语义细节

- `Enabled`：任一 sink 感兴趣就放行，逐个短路返回，全不感兴趣才是 `false`。这是"或"语义，级别门槛取所有 sink 的最低者。真正的过滤发生在各 sink 的 `Handle` 之前。
- `Handle`：尽力而为。某个 sink 返回错误不中断循环，`errors.Join` 聚合所有错误一次返回。
- `WithAttrs`、`WithGroup`：逐 sink 转发，结果重新组成 `multiHandler`，包装链不丢。

### Rust 对照

| Rust | Go | 差异要点 |
|---|---|---|
| `tracing` 的 span 字段自动传播 | `CtxHandler` 从 ctx 提取 | Go 没有 span，靠显式 ctx 加 handler |
| `#[instrument]` | 无，调用点传 ctx | 纪律靠约定 |
| `Layer` 组合 | `MultiHandler` | `tracing` 默认多 sink，slog 要显式包装 |
| `Subscriber` | `slog.Handler` | slog 的接口只有四个方法 |

### Java/Kotlin 与 TS/JS 类比

| 概念 | Java / Kotlin | TS / JS |
|---|---|---|
| ctx 里的 `req_id` 自动进日志 | Logback 的 MDC，Kotlin 协程用 `MDCContext` | pino 的 `mixin`，`AsyncLocalStorage` 传上下文 |
| `CtxHandler` 包装 | 装饰一个 `Appender` 或 `Layout` | 包一层 pino 的 `mixin` |
| `MultiHandler` | 一个 logger 挂多个 appender | pino 的多 transport |
| 同名字段不去重 | 取决于 layout 的实现 | `JSON.parse` 取最后一个同名 key |
| ctx key 两套静默失效 | MDC 的 key 拼错同样静默 | `AsyncLocalStorage` 用错实例同样静默 |
