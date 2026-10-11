# 第 16 章 · 自定义 slog handler：把注入做成基础设施

> P15 改造题（ctxHandler）的正解成章。P15 的痛点：req_id 要每处显式带。
> P16 把它下沉到 handler 层，调用点零感知。顺带解决"一份数据多目的地"。
>
> 前置：P15（slog 四对象模型）。后续：P18 在 dnsproxi 独立重写这套手艺；
> P13 §13.8 路线图的 trace 关联也从这章的 extract 接口走。

## 16.0 这章学完你能干什么

改完这章，httpkv 的业务日志调用点从

```go
logger.WarnContext(ctx, "kv miss", "req_id", requestID(ctx), "key", k, "status", 404)
```

瘦身为

```go
logger.WarnContext(ctx, "kv miss", "key", k, "status", 404)
```

`req_id` 照样出现在输出里（handler 注入），且与 `X-Request-ID` 响应头一致。
真机实拍：

```json
{ "level": "WARN", "msg": "kv miss", "key": "ghost", "status": 404, "req_id": "req-1791624607059659000" }
```

能干的三件事，对应三个问题：

1. 写一个不丢字段、不抢字段的包装 handler（两个必踩坑是什么？§16.2）；
2. 把"从 ctx 提取上下文"做成可换的依赖（明天换 trace_id 要改多少？§16.3）；
3. 一条日志同时给人读和给机器查（MultiHandler 怎么扇出？§16.5）。

P15 留下的入口问题"要不要每处显式带 req_id"，这章给出终点答案：
纪律靠人是下策，横切逻辑归基础设施，见 §16.1。

## 16.1 为什么值得专门一章

P15 说"要么每处显式带，要么自定义 handler"。显式带的问题不是打字量：
1. **纪律靠人**：新来的同事忘带 req_id，那条日志在日志系统里就关联不上请求；
2. **横切逻辑散落**：将来加 trace_id、tenant_id，所有调用点再改一遍。

自定义 handler 把"从 ctx 提取上下文"变成基础设施，调用点只写业务字段。
这正是 Rust tracing 的 span 语义：`#[instrument]` 一标，字段自动传播。
Go 标准库没有 span，但 handler 接口给了你同等的扩展点。

## 16.2 问题 1：写包装 handler，两个必踩坑是什么？

slog.Handler 接口四个方法（Enabled/Handle/WithAttrs/WithGroup），
包装（decorator）写法有两个坑，练习里都有测试盯着：

```go
type Handler interface {
    Enabled(ctx, Level) bool
    Handle(ctx, Record) error          // 格式化+写出
    WithAttrs([]Attr) Handler          // With() 链
    WithGroup(string) Handler          // Group
}
```

**坑 1：WithAttrs/WithGroup 忘记转发包装**。`logger.With("svc","x")`
返回的 logger 若用裸内层 handler，注入就静默丢失。转发并重新包一层：

```go
func (h *CtxHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
    return &CtxHandler{inner: h.inner.WithAttrs(attrs), extract: h.extract}
}
```

`TestWithAttrsKeepsInjection` 专测这个。

**坑 2：同名 attr 覆盖方向搞反**。slog 的 attrs 按**首次出现定型**，
不是后者覆盖前者。注入若先加，调用点的显式字段会被盖掉。
解法：注入前查重，显式说了算：

```go
if attrs := h.extract(ctx); len(attrs) > 0 {
    for _, a := range attrs {
        if !recordHas(r, a.Key) {   // 记录里没有才注入
            r.AddAttrs(a)
        }
    }
}
```

`TestExplicitBeatsInjected` 断言显式赢。这个语义是**实测出来**的：
P15 阶段假设"后加覆盖先加"，测试当场打脸，见 §16.4 翻车记。

## 16.3 问题 2：明天换 trace_id，要改多少？

CtxHandler 不硬编码 request id，而是收一个提取函数：

```go
NewCtxHandler(inner, func(ctx context.Context) []slog.Attr {
    if id := p15slog.RequestID(ctx); id != "" {
        return []slog.Attr{slog.String("req_id", id)}
    }
    return nil
})
```

换 OTel 时把 extract 换成"从 ctx 取 span context 拼 trace_id"即可，
handler 骨架不动。这是 P13 §13.8 路线图（logs → traces）的预留接口。

## 16.4 翻车记：ctx key 两套，注入静默失效

第一次冒烟：`req_id` 不出现，无任何报错。排查：中间件塞的是 httpkv
自己的 `reqIDKey struct{}`，extract 取的是 p15slog 的
`ctxKey("request_id")`，两个不同的 key，`ctx.Value` 返回 nil，
extract 返回空，注入静默跳过。

教训两条：
1. **ctx key 必须全仓库单一来源**。修复：httpkv 删掉自己的 key，
   统一 `p15slog.WithRequestID`/`RequestID`；
2. 静默失效比崩溃可怕。若注入是硬需求，可在 extract 返回空时 debug
   级别提示（练习里没做，留给改造题）。

另一个小翻车：接口断言写 `CtxHandler{}`（值类型）编译失败，
方法是指针接收器，断言要写 `(*CtxHandler)(nil)`。

## 16.5 问题 3：一条日志，两个目的地

调试要文本（人读）、落盘要 JSON（机器读），`MultiHandler` 扇出：

```go
slog.New(MultiHandler(
    slog.NewJSONHandler(file, nil),
    slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}),
))
```

语义要点：`Enabled` 是"任一感兴趣就放行"（否则全 Debug 级的 sink 组里
没人收 Info）；`Handle` 尽力而为，错误 `errors.Join` 聚合，一个目的地
故障不拦其他。`TestMultiHandlerLevel` 验证两个 sink 各自过滤。

## 16.6 动手：练习 p16slogh（45 分钟）

```sh
cd /Users/akka/Dev/learning/go
go test -race ./exercises/p16slogh/ ./exercises/p8fast/httpkv/
```

**预测题**（跑之前写答案）：

1. `logger.With("svc","x")` 之后再打日志，req_id 还在输出里吗？
   WithAttrs 忘了转发会怎样？（§16.2 坑 1）
2. 调用点显式写 `"req_id", "override"`，输出里是注入值还是 override？
   为什么？（§16.2 坑 2）
3. extract 的 ctx 和日志调用传的 ctx 是同一个吗？塞了 id 但 extract
   查的 key 不一样，报错吗？（§16.4）

| 练习 | 对应小节 | 检验什么 |
|---|---|---|
| TestAutoInjection | §16.0 | 调用点零字段，req_id 在输出 |
| TestNoCtxNoField | §16.3 | ctx 无 id 时不出现空字段 |
| TestWithAttrsKeepsInjection | §16.2 坑 1 | With 链不丢注入 |
| TestExplicitBeatsInjected | §16.2 坑 2 | 显式字段赢注入 |
| TestLevelForwarding | §16.2 | 级别过滤穿透包装 |
| TestMultiHandler / Level | §16.5 | 双 sink 扇出 + 各自过滤 |
| httpkv 全站 | §16.4 | 业务调用点零 req_id，输出仍有 |

**改造题**：给 CtxHandler 加"注入缺失告警"：extract 返回空且记录级别
≥ Info 时，往 stderr 打一行 debug 提示（用 fmt，别用 slog 自己否则递归）。

## 16.7 Take-away

1. 横切字段下沉到 handler：调用点只写业务字段，注入/脱敏/格式化是
   基础设施的事。
2. 包装 handler 两个坑：WithAttrs/WithGroup 必须转发重包；同名 attr
   按首次出现定型，注入前查重让显式赢。
3. ctx key 全仓库单一来源，两套 key 的注入静默失效（实测踩过）。
4. extract 依赖注入给 traces 留了口：换 OTel 只换提取函数。
5. MultiHandler 扇出时 Enabled 是"任一感兴趣"，级别过滤交给各 sink。

---

## 章末参考

### MultiHandler 的语义细节

扇出语义逐条（对应 multiHandler 实现）：

- `Enabled`：任一 sink 感兴趣就放行，逐个短路返回；全不感兴趣才 false。
  这是"或"语义：级别门槛取所有 sink 的最低者，真正的过滤发生在各
  Handle 前（实现里每个 sink 先查自己的 Enabled）。
- `Handle`：尽力而为。某个 sink 返回错误不中断循环，`errors.Join`
  聚合所有错误一次返回。一个目的地（比如写满的文件）故障不拦其他。
- `WithAttrs`/`WithGroup`：逐 sink 转发，结果重新组成 multiHandler，
  包装链不丢。

`TestMultiHandlerLevel` 验证两个 sink 各自过滤；对照 Rust：`tracing`
的 `Layer` 组合默认就是多 sink 扇出，slog 把"要不要多目的地"做成了
显式包装。
