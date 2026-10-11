---
title: 第 18 章 · 毕业项目日志落地：dnsproxi 的结构化 + req_id
---


> P15/P16 的手艺在毕业项目里内化：不 import 练习包，独立 module 里
> 重写 ctxHandler。这是"毕业检验"：离开脚手架还能不能写出同样纪律的代码。
>
> 前置：P15（slog 模型）、P16（自定义 handler 两坑）、P17（dnsproxi 指标）。

## 这章学完你能干什么

把 dnsproxi 的 `[hit]/[miss]` 自由文本全部换成 JSON 结构化输出，每条带
`req_id`（每查询生成，ctx 自动注入），并回答"离开 httpkv 的 HTTP
中间件脚手架，同一套纪律怎么在 UDP 上落地"。真机实拍：

```json
{"level":"INFO","msg":"cache miss, forwarding","qname":"smoke.example.com","qtype":"A","req_id":"q-1791627126719748000"}
{"level":"INFO","msg":"cache hit","qname":"smoke.example.com","qtype":"A","ttl_left":60,"req_id":"q-1791627126720275000"}
```

同一次查询的 miss→转发→hit 生命周期可用 `req_id` 串起来（每次查询
独立 id；同一查询的多条日志共享）。`DNS_LOG_LEVEL=debug|warn|error`
级别可调。一个主问题贯穿：

### 毕业检验：离开脚手架重写 handler，差异在哪？

httpkv 版（P15/P16）和 dnsproxi 版（P18）的差别不在手艺，在**注入点**。
DNS over UDP 没有 HTTP 中间件层，req_id 的挂靠位置要重新找：

| | httpkv（P15/P16） | dnsproxi（P18） |
|---|---|---|
| req_id 生成 | reqID 中间件（HTTP 层） | 读循环里 `newQueryID()`，`withRequestID` 塞 ctx |
| ctx 传播 | `r.WithContext` 随请求走 | `context.Background()` 派生，传给 handleQuery |
| 回显 | `X-Request-ID` 响应头 | 无（UDP 应答没有头可放，日志对时间戳） |
| 级别配置 | 代码写死 | `DNS_LOG_LEVEL` env |

**UDP 的对应物是"每查询一个 ctx"**：handleQuery 入口建 ctx，函数内所有
日志调用（包括子函数）都带它。panic recover 里的日志没有 ctx（recover
发生在 handler 外层），用无 ctx 的 `logger.Error`。可接受：panic 是
进程级事件，不关联单查询。

第二个差异是**代码组织**：dnsproxi-go 不 import `golearn/exercises/*`
（独立 module，go.mod 无关）。`logger.go` 自包含：reqIDKey/ctxHandler/
newLogger，P16 的两个坑照旧（WithAttrs 转发重包、注入前查重）。这次
没有抄错：坑在 P16 已实测过。`main()` 里 `logger = newLogger(os.Stderr,
logLevel())` 重置包级变量：包级 logger 先建一个 info 级的兜底（测试和
库代码引用不会 nil），main 里按配置重建。

handler 本身（`ctxHandler` 四个方法：Enabled/Handle/WithAttrs/WithGroup，即
slog.Handler 接口全部）和 httpkv 版逐行同构： UDP 上变的是注入点和生命周期，
不变的是 handler 骨架和两坑纪律。

## 级别语义：什么进 Debug

| 级别 | dnsproxi 的内容 |
|---|---|
| Error | 写失败（client/upstream/metrics 口）、panic |
| Warn | 上游读失败（含超时：client 侧有兜底，不算 Error） |
| Info | 启动横幅、cache hit/miss、evict |
| Debug | expire 每轮统计、cache reject（频率不够没进缓存） |

原则对照 P15：**Debug 给运维排障用的高频低值信息**。expirer 一秒 10 轮，
info 级会把日志冲爆；reject 在 TinyLFU 满仓时每次 miss 都可能发生，
同理。`Exchange` 开关现在只控制"要不要收集"，级别控制"要不要输出"，
正交了。

## 日志与指标的分工（P13 三支柱实践）

同一个"cache miss"事件，两个柱子各记各的：

| | 指标（P17） | 日志（P18） |
|---|---|---|
| 记什么 | `queries_total{qtype="A",outcome="miss"}` +1 | qname、qtype、req_id 全文 |
| 回答 | miss 率多少、趋势 | **哪个域名** miss 的、这次查询的完整链路 |
| 成本 | O(1) 计数器 | 每条一行 JSON |

指标丢 qname（基数），日志丢聚合（量大）。两柱合起来才是完整的
"可观测"：告警看指标（miss 率 > 80%），排障看日志（req_id 捞现场）。

## 动手：冒烟复现（15 分钟）

```sh
cd /Users/akka/Dev/learning/go/dnsproxi-go
DNS_LISTEN=:15354 DNS_CACHE=1 DNS_LOG_LEVEL=debug go run . &
# 随便打几个 DNS 查询（或用 dnsq/），然后看 stderr：
# 每条 JSON 带 req_id；启动横幅字段化
```

**预测题**（跑之前写答案）：

1. `DNS_LOG_LEVEL=warn` 起服务，hit/miss 行还在吗？panic 日志呢？
   （级别语义表）
2. handleQuery 里命中缓存的请求会打几条日志？两条日志的 req_id
   相同吗？（每查询一个 ctx）
3. 把 `ctxHandler.Handle` 里的查重逻辑去掉，同时调用点显式传
   `"req_id", ...`，输出会怎样？（P16 两坑）

**观察验证**：

| 观察点 | 期望 |
|---|---|
| 启动横幅 | `msg:"dnsproxi-go starting"` + 字段化配置 |
| miss 行 | `qname/qtype/req_id` 三字段 |
| hit 行 | 多一个 `ttl_left`，**没有** forward 字样 |
| `DNS_LOG_LEVEL=warn` 起服务 | hit/miss 消失，只剩 warn/error |

**改造题**：给 `newQueryID` 换 ulid（`github.com/oklog/ulid`），
验证排序性（同一毫秒内 id 单调）；提示：熵源用 crypto/rand。

## Take-away

1. UDP 没有 HTTP 中间件：req_id 的注入点是 handleQuery 入口的
   `withRequestID(context.Background(), ...)`，生命周期 = 单查询。
2. 毕业检验的实质：handler 骨架逐行同构，变的是注入点和生命周期；
   独立 module 重写一遍，P16 的坑（WithAttrs 转发、查重注入）
   不用再踩。
3. 级别分层：Error=写失败/panic，Warn=上游超时（有兜底），Info=hit/miss，
   Debug=expirer/reject 高频低值。
4. 指标记聚合、日志记细节：miss 率告警看指标，排障捞 req_id 看日志。
5. 包级 logger 先兜底、main 里按 env 重建，测试代码不 nil。

---

## 章末参考

### ctxHandler 现行实现（dnsproxi-go/logger.go）

`Handle` 里查重注入：`requestID(ctx)` 非空且 record 里没有 `req_id`
才 `AddAttrs`。`WithAttrs`/`WithGroup` 转发并重包 `&ctxHandler{...}`。
`recordHas` 遍历 `r.Attrs` 查重。P16 讲过的两个坑的完整形态看这份
97 行的自包含文件。

### 级别开关与收集开关

`DNS_LOG_LEVEL`（debug|info|warn|error，非法回 info）只控制输出门槛；
`DNS_CACHE_EXPIRE_LOG`（expirer 统计的 `Exchange` 开关）控制要不要
收集 expire 统计。两者正交：关掉收集，Debug 级别再低也没有记录可输出。

### panic 日志为何无 ctx

recover 的 defer 挂在 goroutine 入口（`main.go` 读循环 spawn 处），
此时 handleQuery 尚未执行、查询 ctx 尚未创建。panic 日志用无 ctx 的
`logger.Error("query panic", ...)`，字段带 panic 值本身。进程级事件
不关联单查询，这是有意为之，不是遗漏。
