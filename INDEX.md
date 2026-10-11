# golearn 课程总索引

> Go for Backend · 以练代学。类比锚点：Java/Rust（后端视角）。
> 配方仿照 `rs/rust-for-frontend`；实测规矩沿用 `_rust_learning`。

## 怎么用这份教程（新读者从这读）

**按阶段读，不按编号读。** 编号是写作顺序，不是学习顺序：

| 阶段 | 顺序 | 时间 | 判过标准 |
|---|---|---|---|
| ① 语法核心 | P2 → P3 → P14 | 半天 | 能向别人复述每章 Take-away |
| ② 服务搭建 | P8 → P12（或 P9）→ P6 LESSON.md | 1 天 | 能画出 dnsproxi 的请求路径 |
| ③ 可观测性 | P13 → P15 → P16 → P17 → P18 | 1 天 | 能解释"为什么 req_id 不进 metrics label" |
| ④ 按需查 | P1 cheat sheet、P4/P5 精读法、P10/P11 | 用到才翻 | — |

每章打开方式（详见 README）：先读章头“这章学完你能干什么”和正文的主要
问题（正文是讲义），卡壳才翻章末参考。练习：预测题先写答案再跑对答案，
观察题看输出，改造题动手改。判过：Take-away 能复述 + 改坏代码能修回。

**一键回归**（怀疑哪章学漏了就跑）：
`go vet ./... && go test -race -count=1 ./...`（dnsproxi-go 单独跑）。

## 课程表与当前状态

| 课 | LESSON | 练习 | 状态 |
|---|---|---|---|
| P1 工具链 cargo→go | [notes/p1-toolchain.md](notes/p1-toolchain.md) | `exercises/p1tool/` | ✅ 全绿（参考实现 2026-10-10） |
| P2 语言核心：用 Rust 经验写 Go 的四个换直觉问题 | [notes/p2-lang-core.md](notes/p2-lang-core.md) | `exercises/p2lang/` | ✅ 全绿（参考实现 2026-10-10） |
| P3 并发 goroutine/channel/select/context | [notes/p3-concurrency.md](notes/p3-concurrency.md) | `exercises/p3conc/` | ✅ 全绿 -race 干净（参考实现 2026-10-10，含死锁测试修复） |
| P4 精读 ejdb | [notes/p4-ejdb.md](notes/p4-ejdb.md) | `exercises/p4ejdb/` | ✅ 全绿（参考实现 2026-10-10） |
| P5 精读 deepsearch | [notes/p5-deepsearch.md](notes/p5-deepsearch.md) | `exercises/p5deep/` | ✅ 全绿 -race 干净（参考实现 2026-10-10；DI 死锁教训见 deep.go 注释） |
| P6 capstone dnsproxi-go | [notes/p6-capstone-dnsproxi.md](notes/p6-capstone-dnsproxi.md) | `dnsproxi-go/` | **G1–G6 全部收官**（wire+proxy+cache+逐出+TinyLFU+主动老化+同机压测），数据见 [dnsproxi-go/LESSON.md](dnsproxi-go/LESSON.md) |
| P7 labs 工程实验 | — | `labs/experiments/` | ✅ P7 三组（GC 压力账 / 计数器四方案 / benchmark 卫生）+ P8 两组（命中路径 A/B / GOMAXPROCS+trace）+ P9（worker pool 消融，`DNS_WORKER_POOL=8` 落地为生产建议）；数据见 labs/experiments/ 三份 results；方法论仿 rust-for-frontend/labs |
| P8 快速扩展 泛型/标准库/测试/HTTP | [notes/p8-fast-track.md](notes/p8-fast-track.md) | `exercises/p8fast/` | ✅ 8 tests -race 干净（约束/trait 三差异、slices/cmp/OnceValue、table-driven、httpkv 服务 1.22 路由+优雅关停，冒烟过） |
| P9 数据库 database/sql+pgx | [notes/p9-database.md](notes/p9-database.md) | `exercises/p9db/` | ✅ 5 tests -race 干净（CRUD/NULL 三策略/事务回滚/ctx 取消/池并发，modernc.org/sqlite 纯 Go）+ pgx 原生 batch 冒烟过真 PG17 |
| P10 gRPC 四种流+拦截器 | [notes/p10-grpc.md](notes/p10-grpc.md) | `exercises/p10grpc/` | ✅ 5 tests -race 干净（一元/Watch/BulkSet/Chat/并发，bufconn）+ 真机 server/client 冒烟过（输出见 notes 动手节）；proto 先行，对照 tonic |
| P11 sqlc+goose 数据层 | [notes/p11-sqlc-goose.md](notes/p11-sqlc-goose.md) | `exercises/p11sqlm/` | ✅ 3 tests -race 干净（生成代码 CRUD/Tx 回滚/goose up-down 循环）；SQL 先行，两个真实翻车记档（正文问题 4） |
| P12 sqlc+pgx/v5 PG 原生 | [notes/p12-sqlc-pgx.md](notes/p12-sqlc-pgx.md) | `exercises/p12pgx/` | ✅ 4 tests -race 干净真连 PG17（数组/jsonb/timestamptz 原生、batch 流水线、withTx 纪律移植）；DSN 门控离线 skip |
| P13 metrics/observability | [notes/p13-metrics.md](notes/p13-metrics.md) | `exercises/p8fast/httpkv/`（挂 /metrics） | ✅ httpkv 全站指标（Counter/Gauge/Histogram）+ 冒烟实拍输出；r.Pattern mux 外恒空踩坑记档（§13.4），label 基数纪律 |
| P14 errgroup+pipeline 并发组装 | [notes/p14-pipelines.md](notes/p14-pipelines.md) | `exercises/p14pipe/` | ✅ 8 tests -race（三板斧/SetLimit 消融实测≤2/首错传播/NumGoroutine 泄漏检测）；P3 思想的工程化组装，对照 futures::stream |
| P15 slog 结构化日志：404 报障怎么查 | [notes/p15-slog.md](notes/p15-slog.md) | `exercises/p15slog/` + httpkv | ✅ 6 tests -race（JSON 字段/级别/LogValuer 脱敏/ctx 关联/Enabled 守卫）+ httpkv req_id 中间件冒烟实拍；P13 §13.8 路线图第一步 |
| P16 自定义 slog handler | [notes/p16-slog-handler.md](notes/p16-slog-handler.md) | `exercises/p16slogh/` + httpkv | ✅ 7 tests -race（自动注入/With 链不丢/显式赢注入/双 sink 扇出）+ httpkv 调用点瘦身冒烟实拍；ctx key 单一来源翻车记档（§16.4） |
| P17 dnsproxi 指标落地 | [notes/p17-dnsproxi-metrics.md](notes/p17-dnsproxi-metrics.md) | `dnsproxi-go/`（:9153/metrics） | ✅ RED 重建（qtype×outcome 低基数）+ cache_entries 双更新点 + 7=2+4+1 冒烟对账；P19 补 upstream_errors_total{reason} 双场景实测（refused/timeout） |
| P18 dnsproxi 日志落地 | [notes/p18-dnsproxi-slog.md](notes/p18-dnsproxi-slog.md) | `dnsproxi-go/logger.go` | ✅ 独立 module 重写 ctxHandler（UDP 版每查询 ctx）+ [hit]/[miss] 结构化冒烟实拍；级别分层表 |

## 用法

```sh
cd /Users/akka/Dev/learning/go
# dnsproxi-go 是独立 module：
cd dnsproxi-go && go test ./...
```

P1-P5 练习已完成（参考实现模式，2026-10-10）：每道题的参考实现写在练习文件里，附"Rust 对照"与考点注释；P5 迷你 DI 的两次翻车（key 撞车死锁、Once 闭包单例假象）记录在 `exercises/p5deep/deep.go` 注释里。
**测试全绿 = 过关**。P3/P5 记得加 `-race`：

```sh
go test -race -timeout 30s ./exercises/p3conc/...
```

## 两条主线（贯穿）

1. **Go vs Rust 心智模型**：error vs Result、interface 隐式实现 vs trait、
   goroutine vs tokio task、GC vs 所有权、race detector vs Send/Sync。
2. **工程即目录**：internal/ 编译器强制、包级可见性、interface 定义在使用方。
   精读 ejdb（库怎么组织）和 deepsearch（服务怎么组织）两个真实载体。

## 载体

- `/Users/akka/oss/github.com/clarkqaq/ejdb`：嵌入式 JSON 库（P4）
- `/Users/akka/oss/github.com/clarkqaq/deepsearch`：LLM 搜索服务（P5）
- `/Users/akka/Dev/learning/rs/dnsproxi`：你自己的 Rust DNS 载体（P6 对照）
