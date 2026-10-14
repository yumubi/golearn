# 速学版章节地图（Claude 制定，最终，omp 不得修改）

目标：29 个文件，按学习顺序分六阶段。文件名沿用现有 `notes/` 名（不重命名，避免动工具链与入口）。合并后被吸收的文件在该章完成时删除（连同 `docs/` 镜像与入口链接）；它们的练习包 `exercises/` **全部保留**，由主章引用。

阅读顺序以 `guide/ROUTE.md` 为准，下表即顺序。

## 阶段一：语言速通（读完能读写日常 Go 代码）

| # | 文件 | 标题 | 吸收 | 主要原材料 | 练习包 |
|---|---|---|---|---|---|
| 1 | p1-toolchain | 工具链、module 与项目骨架 | | TB04–TB08, TB35b | p1tool |
| 2 | p1_5-basics | 变量、类型与数据结构 | | TB10–TB17 | p1_5basics |
| 3 | p27-functions-errors（新文件） | 函数、控制流、错误与 panic | 原 p1_5 的函数/错误/defer 部分 | TB18–TB23, TBX09 | p1_5basics 的对应测试 |
| 4 | p2-lang-core | 方法、接口与组合 | p30 | TB24–TB30 | p2lang, p30 |
| 5 | p2_5-idiom | 惯用法：写得像 Go | | Go Proverbs, TB 各讲零散 | p2_5idiom |
| 6 | p8-fast-track | 泛型与标准库速览 | p33 | TBX04–05, TB 标准库讲 | p8fast, p33gen |

## 阶段二：并发与运行时

| # | 文件 | 标题 | 吸收 | 主要原材料 | 练习包 |
|---|---|---|---|---|---|
| 7 | p3-concurrency | 并发基础：goroutine、channel、select | p31 | TB31–TB35, C:concurrence | p3conc, p31sched |
| 8 | p14-pipelines | 并发工程化：errgroup、pipeline、context | | W 系列并发相关, C:dachang/concurrence | p14pipe |
| 9 | p32-memory-gc | 内存、逃逸与 GC | | TBX10 | p32gc |

## 阶段三：服务、网络与数据

| # | 文件 | 标题 | 吸收 | 主要原材料 | 练习包 |
|---|---|---|---|---|---|
| 10 | p28-http-service（新文件） | HTTP 服务与标准库 | 原 p8 的 HTTP 部分 | TB09 | p8fast |
| 11 | p24-network-stack | 网络：协议栈、TCP 与 DNS | p34 | TB36–TB38 | p34frame |
| 12 | p9-database | 数据库：database/sql 与连接池 | | W 系列数据库相关 | p9db |
| 13 | p11-sqlc-goose | 数据层：sqlc、pgx 与迁移 | p12 | | p11sqlm, p12pgx |
| 14 | p10-grpc | gRPC | | W10-1, W13-2 | p10grpc |
| 15 | p4-ejdb | 项目精读 A：ejdb | | | p4ejdb |
| 16 | p5-deepsearch | 项目精读 B：deepsearch | | | p5deep |
| 17 | p6-capstone-dnsproxi | 毕业项目 dnsproxi-go 总览 | | | dnsproxi-go/ |

## 阶段四：工程化

| # | 文件 | 标题 | 吸收 | 主要原材料 | 练习包 |
|---|---|---|---|---|---|
| 18 | p21-testing-concurrency | 测试：表驱动、fuzz、synctest | 原 p8 的测试部分 | TB 测试讲 | p21test |
| 19 | p13-metrics | 指标与可观测 | p17 | W5, W8 | httpkv |
| 20 | p15-slog | 结构化日志 | p16, p18 | W5 | p15slog, p16slogh |
| 21 | p22-toolchain-docs | 工具链进阶：pprof、lint、文档工程 | | | p22tool |
| 22 | p19-ecosystem | 生态选型与 CLI 工具 | p20 | | p19eco, p20cli |
| 23 | p35-guard（新文件） | 限流、熔断与容错 | | W14 | dnsproxi-go/guard |

## 阶段五：运行环境与部署

| # | 文件 | 标题 | 吸收 | 主要原材料 | 练习包 |
|---|---|---|---|---|---|
| 24 | p23-linux-runtime | Linux 运行时与容器 | p25 | | |
| 25 | p26-deploy | 部署实战 | | | labs/compose |
| 26 | p42-k8s（新文件） | Kubernetes 与多集群（读材料型） | | W03-2, K:krm-* | 无 |

## 阶段六：minifeed 实战（可选，阶段一至五完成后再做）

| # | 文件 | 标题 | 主要原材料 |
|---|---|---|---|
| 27 | p36-minifeed-user | 用户、鉴权与分层 | W2–W5 |
| 28 | p37-minifeed-post | 缓存、计数与榜单 | W6–W9 |
| 29 | p39-minifeed-gov | 服务治理与数据迁移 | W10–W13, W11, W15 |

说明：原计划中的搜索/Feed/IM（P41）并入第 29 章的"延伸"，不单独成章；原 P38 可观测并入第 19、20 章；P40 分布式事务并入第 29 章。

## 被吸收后删除的文件

p30-methods-interfaces, p31-concurrency-scheduler, p33-generics-boundary, p34-tcp-server-dns-over-tcp, p12-sqlc-pgx, p16-slog-handler, p17-dnsproxi-metrics, p18-dnsproxi-slog, p20-cli-tool, p25-containers-cloud。

## 约束

- 每章只用该章 `rewrite/edit/<文件>.md` 里的要点，不得增删（`scripts/check-edit.py` 强制）。
- 编辑单由 Claude 逐章写成；omp 只在编辑单存在的章上工作。
