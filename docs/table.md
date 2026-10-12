---
title: 课程表：23 章速查
---

# 课程表

> 状态一栏的"tests -race 干净"= 练习包可运行、可作为你的改坏练习靶子。
> 学习顺序见[首页三弧图](/)，本表按编号索引。

| 章 | 主题 | 教程 | 练习 | 状态 |
|---|---|---|---|---|
| P1 | 工具链：cargo → go | [notes](/p1-toolchain) | `exercises/p1tool/` | ✅ 全绿（cheat sheet + 章末进阶命令） |
| P1.5 | 语法基础速通（12 节 Rust 对照） | [notes](/p1_5-basics) | `exercises/p1_5basics/` | ✅ 8 tests（切片/map/defer/rune 预测题） |
| P2 | 语言核心：interface/组合/error | [notes](/p2-lang-core) | `exercises/p2lang/` | ✅ 全绿 |
| P2.5 | 范式与惯用法 | [notes](/p2_5-idiom) | `exercises/p2_5idiom/` | ✅ 6 tests（胖接口拆小/错误链/fake） |
| P3 | 并发：goroutine/channel/select/context | [notes](/p3-concurrency) | `exercises/p3conc/` | ✅ 全绿 -race 干净 |
| P4 | 精读 ejdb | [notes](/p4-ejdb) | `exercises/p4ejdb/` | ✅ 全绿 |
| P5 | 精读 deepsearch | [notes](/p5-deepsearch) | `exercises/p5deep/` | ✅ 全绿 -race 干净（DI 死锁教训） |
| P6 | capstone dnsproxi-go | [notes](/p6-capstone-dnsproxi) | `dnsproxi-go/` | ✅ G1–G6 收官（压测数据见 LESSON.md） |
| P7 | labs 工程实验 | 无 | `labs/experiments/` | ✅ 三组实验 + 三份 results |
| P8 | 泛型/标准库/测试/HTTP | [notes](/p8-fast-track) | `exercises/p8fast/` | ✅ 8 tests -race |
| P9 | 数据库 database/sql+pgx | [notes](/p9-database) | `exercises/p9db/` | ✅ 5 tests -race |
| P10 | gRPC 四种流+拦截器 | [notes](/p10-grpc) | `exercises/p10grpc/` | ✅ 5 tests -race + 真机冒烟 |
| P11 | sqlc+goose 数据层 | [notes](/p11-sqlc-goose) | `exercises/p11sqlm/` | ✅ 3 tests -race（两个翻车记档） |
| P12 | sqlc+pgx/v5 PG 原生 | [notes](/p12-sqlc-pgx) | `exercises/p12pgx/` | ✅ 4 tests -race 真连 PG17 |
| P13 | metrics/observability | [notes](/p13-metrics) | `exercises/p8fast/httpkv/` | ✅ /metrics + 基数纪律 |
| P14 | errgroup+pipeline | [notes](/p14-pipelines) | `exercises/p14pipe/` | ✅ 8 tests -race（消融+泄漏检测） |
| P15 | slog 结构化日志 | [notes](/p15-slog) | `exercises/p15slog/` + httpkv | ✅ 6 tests -race + req_id 冒烟 |
| P16 | 自定义 slog handler | [notes](/p16-slog-handler) | `exercises/p16slogh/` + httpkv | ✅ 7 tests -race + 零显式注入 |
| P17 | dnsproxi 指标落地 | [notes](/p17-dnsproxi-metrics) | `dnsproxi-go/` :9153 | ✅ 7=2+4+1 对账 + errors{reason} |
| P18 | dnsproxi 日志落地 | [notes](/p18-dnsproxi-slog) | `dnsproxi-go/logger.go` | ✅ 每查询 req_id 注入 |
| P19 | 生态与常用库选型地图 | [notes](/p19-ecosystem) | `exercises/p19eco/` | ✅ 4 tests（options/slog Handler/stringer） |
| P20 | 写 CLI 工具（管道/flag/cobra/退出码/发布） | [notes](/p20-cli-tool) | `exercises/p20cli/`（含 cmd/） | ✅ 7 tests + wcgo 退出码实证 |
| P21 | testing/synctest + fuzz（虚拟时钟/性质测试） | [notes](/p21-testing-concurrency) | `exercises/p21test/`（含 cmd/retryrun） | ✅ 10 tests -race + FuzzParseArgs 60s 2853 万 execs |
| P22 | 工具链深潜与文档工程（pprof/lint/generate/GODEBUG/go.work/doc 工程） | [notes](/p22-toolchain-docs) | `exercises/p22tool/`（含 cmd/docstats） | ✅ 4 tests -race + CLI 退出码 0/1/2 + pprof 五 profile 实采 |

## 贯穿主线

1. **Go vs Rust 心智模型**：error vs Result、interface 隐式实现 vs trait、goroutine vs tokio task、GC vs 所有权、race detector vs Send/Sync。每章的"对照表"服务这条线。
2. **工程即目录**：internal/ 编译器强制、包级可见性、interface 定义在使用方。精读 ejdb（库）和 deepsearch（服务）两个真实载体。
3. **可观测性三支柱**（P13 起）：metrics（多少）→ logs（发生了什么）→ traces（谁调的谁），P17/P18 在毕业项目落地。
