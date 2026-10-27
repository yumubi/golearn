---
title: 课程表
---

# 课程表

> 本表是第二层「细节库」的速查；第一层手册见
> [手册目录](/handbook/README)。学习顺序见[首页两层阅读图](/)。

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
| P20 | 写 CLI 工具（管道/flag/cobra/退出码/发布） | [notes](/p20-cli-tool) | `exercises/p20cli/`（含 cmd/） | ✅ 7 tests + wcgo 退出码实证 + 预测环节 |
| P21 | testing/synctest + fuzz（虚拟时钟/性质测试） | [notes](/p21-testing-concurrency) | `exercises/p21test/`（含 cmd/retryrun） | ✅ 10 tests -race + 2853 万 execs + -race 节（DATA RACE 实拍/race×fuzz 同跑实证） |
| P22 | 工具链深潜与文档工程（pprof/lint/generate/GODEBUG/go.work/doc 工程） | [notes](/p22-toolchain-docs) | `exercises/p22tool/`（含 cmd/docstats） | ✅ 4 tests -race + pprof top/goroutine 实拍 + schedtrace + dlv 会话节 |
| P23 | Linux 运行时：进程/信号/fd/容器（httpkv 骨架） | [notes](/p23-linux-runtime) | 实战即练习（httpkv + docker） | ✅ 优雅关停/容器内 SIGTERM/cgroup 实拍 + 预测环节（kill -9 可否优雅） |
| P24 | 网络协议栈：从 ping 到 DNS 全链路（dnsproxi 教具） | [notes](/p24-network-stack) | 实战即练习（tcpdump 抓包） | ✅ DNS 逐字节/TTL 账本/TCP 状态机实拍 + 预测环节（cache hit 不转发） |
| P25 | 容器与云原生：镜像/限额/健康检查 | [notes](/p25-containers-cloud) | 实战即练习（labs/compose/） | ✅ 多阶段 351MB→26.6MB + GOMAXPROCS 取整实测 + healthcheck |
| P26 | 部署实战：compose 编排收官 | [notes](/p26-deploy) | `labs/compose/` | ✅ 三服务编排真跑（dnsproxi+httpkv+upstream）+ 健康条件依赖 |
| P30 | 方法、接口与组合 | [notes](/p30-methods-interfaces) | `exercises/p30/`（含 cmd/demo1、cmd/mw） | ✅ 10 tests -race 干净（方法集合矩阵/装箱 bench/cmd 实拍） |
| P31 | 并发调度：GMP 与 channel/select | [notes](/p31-concurrency-scheduler) | `exercises/p31sched/` | ✅ 12 tests（11 -race 干净 + 1 skip-ok；脏写分布/锁矩阵/信号量批分布实测） |
| P32 | 内存与 GC | [notes](/p32-memory-gc) | `exercises/p32gc/` | ✅ 4 tests -race 干净 + 3 bench（逃逸矩阵/语义垃圾/三级分配/pprof 四级） |
| P33 | 泛型的边界 | [notes](/p33-generics-boundary) | `exercises/p33gen/` | ✅ 6 tests -race 干净 + 3 bench（排序三选手/Set/标记接口/编译错实拍） |
| P34 | TCP 服务器与 DNS over TCP | [notes](/p34-tcp-server-dns-over-tcp) | `exercises/p34frame/` | ✅ 8 tests -race 干净 + 2 bench（帧三边界/丢帧语义/Pool 改造/4 字节前缀） |
| P35 | 限流、熔断与容错 | [notes](/p35-ratelimit-breaker) | `dnsproxi-go/guard/` | ✅ 11 tests -race 干净 + 3 bench（令牌桶欠账/熔断三态/冒烟 breaker_open 5/5） |
| P36 | 用户、鉴权与分层测试（webook 主线） | [notes](/p36-user-auth-layering) | `minifeed/` | ✅ 3 tests -race 干净（双 Store 同套用例/token 四连/httptest 全链路） |
| P37 | 缓存、计数与榜单 | [notes](/p37-cache-counter-rank) | `minifeed/` | ✅ 6 tests -race 干净（post 分页/counter 并发 50 恰 50/rank 单飞/httptest 全链路） |

## 贯穿主线

1. **Go vs Rust 心智模型**：error vs Result、interface 隐式实现 vs trait、goroutine vs tokio task、GC vs 所有权、race detector vs Send/Sync。每章的"对照表"服务这条线。
2. **工程即目录**：internal/ 编译器强制、包级可见性、interface 定义在使用方。精读 ejdb（库）和 deepsearch（服务）两个真实载体。
3. **可观测性三支柱**（P13 起）：metrics（多少）→ logs（发生了什么）→ traces（谁调的谁），P17/P18 在毕业项目落地。
