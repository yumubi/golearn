# PLAN-OMP-2（已作废，被 PLAN-OMP-3.md 取代）：把教程和你手上的材料串成一条线（执行者：OMP，验收者：Claude）

起点提交见 `scripts/BASE`。先读 `HANDOFF.md`（只追记，不重写）、`INDEX.md`、`PLAN-OMP.md` 的第 1 节（反简化机制，本文全部沿用），再读本文。

## 0. 为什么要这份计划（读一遍，别跳）

用户的原话意思：下载的材料（`downloads/`）已经非常全，问题不在缺内容，而在三点：

1. **不连贯**：27 章、52 讲、21 周、三个代码包各讲各的，没有人告诉读者"先看什么，再看什么，哪些可以不看"。
2. **没和实际项目结合**：知识点停在练习包里，没落到一个真项目上。
3. **别搞复杂**：目标是**短时间内掌握**，不是再写一大堆新章。

所以本轮**不是写新教程**，而是：

- 用一条主线把现有教程和用户手上的材料串起来（一份路线图 + 一份材料索引）。
- 全程锚定一个真项目：`dnsproxi-go`。每一站都标出"这个知识点在 dnsproxi 的哪个文件里用到"。
- 只补三处真正的缺口：错误处理的判断线、并发小模式、服务保护（限流/熔断）。最后一处直接落到 dnsproxi 上。

审计结论在 `audit/gap-analysis.md`（事实与映射表）。`audit/restructure-plan.md` 是第一版方案，**已作废**：它加了 16 个新章，太重，且深潜类章节需要对 Go 1.26 运行时做源码级观察，不适合你来写。本文是唯一有效的计划。

## 1. 执行规则（给你的，比 PLAN-OMP.md 更严）

1. 一次只做一个任务，做完跑该任务的验收命令，贴输出，再开下一个。
2. 只读本文点名的文件。不要通读仓库，不要"顺便"改别的。
3. **不要编造**：输出、数字、版本、命令结果，必须来自你刚跑过的命令。没跑过就写"未验证"。
4. 笔记里每个代码块的输出，必须把代码真放进临时目录 `go run` 过。临时目录放 `/tmp`，不要放仓库里。
5. 不得删除或放宽已有测试；测试函数总数只增不减。
6. 不碰 `downloads/` 里的任何文件（读文件名可以，抽文字可以，但不得把内容复制进仓库）。
7. 不引用素材原文。所有写出来的话是你自己的话。
8. 卡住就停，用 `PLAN-OMP.md` 第 4 节的"阻塞报告"格式，不要换个更容易的任务。
9. 硬规则沿用：零 em-dash（U+2014）、整数 range、新章从 P27 起、五处入口同步、`scripts/mirror.sh` 镜像、一句一个意思、体系名词首现一句话解释、不用 AI 味词。
10. 每个任务一个提交，信息写清范围。**不推远端。**

## 2. 任务清单

顺序固定：T1 → T2 → T3 → T4 → T5 → T6。T1、T2 只产出文档，最先做，价值最大。

### T1 路线图 `guide/ROUTE.md`

新建目录 `guide/`（不在 `notes/` 里，不进 docs 镜像，不触发镜像检查）。

内容是**一条**学习主线，共 10 站。每站固定五项：

- **读什么**：本仓库章节 + 用户材料（见 T2 的索引，只写引用，不抄内容）。
- **做什么**：对应练习包和命令。
- **项目锚点**：这站的知识在 `dnsproxi-go/` 哪个文件、哪个函数里能看到。必须是真实存在的文件和函数名。
- **过关标准**：一条可重跑的命令，或一句"能向别人讲清……"。
- **预计时间**：小时。

10 站的骨架（站名、顺序、章节、时间已定，你补全其余项）：

| 站 | 主题 | 本仓库 | 预计 | 项目锚点提示 |
|---|---|---|---|---|
| 1 | 语法与工具链 | P1、P1.5 | 3h | `dnsproxi-go/main.go` 的 `loadConfig`、`envInt` 一族 |
| 2 | 类型、接口、错误 | P2、P2.5 | 4h | `wire/wire.go`、`cache/` 的接口和错误返回 |
| 3 | 并发 | P3、P14、P21 | 5h | `main.go` 的 `Proxy.Run`、`handleQuery`（每查询一个 goroutine） |
| 4 | 标准库、HTTP、测试 | P8 | 4h | `exercises/p8fast/httpkv/main.go` |
| 5 | 网络与 DNS | P24 | 3h | `wire/wire.go` 报文解析，`main.go` 的 UDP 收发 |
| 6 | 数据层（按需） | P9 或 P12、P11 | 3h | 本站项目里没有数据库，写"无锚点，学完做练习包即可" |
| 7 | 可观测 | P13、P15、P16、P17、P18 | 6h | `dnsproxi-go/metrics.go`、`logger.go` |
| 8 | 服务保护：限流、熔断 | P27（T4 新写） | 4h | `main.go` 的 `handleQuery`（T4 接入点） |
| 9 | 容器与部署 | P23、P25、P26 | 3h | `labs/compose/` |
| 10 | 毕业：读懂并压测 dnsproxi | P6、`dnsproxi-go/LESSON.md` | 4h | 整个 `dnsproxi-go/` |

合计约 39 小时，按每天 5 小时是 8 天。写在文件开头，并写一句"站 6 可跳，站 4、5 可以压缩"，让读者知道最短路径约 5 天。

文件结构：开头 10 行说明怎么用；一张总表；然后每站一节。**总字数控制在 400 行以内。** 不要写成新教程，是导航。

验收：

```sh
python3 -I scripts/check-route.py guide/ROUTE.md
```

`scripts/check-route.py` 由你在本任务里一并写（≤60 行，标准库）：解析 ROUTE.md，检查（a）10 站齐全，每站五项齐全；（b）所有出现的 `notes/…`、`exercises/…`、`dnsproxi-go/…`、`labs/…` 路径真实存在；（c）所有"项目锚点"里标注的函数名在对应文件里能 grep 到；（d）文件中不含 U+2014。任一不满足打印原因并退出码 1。

### T2 材料索引 `guide/MATERIALS.md`

把用户的四套材料整理成**"读什么、跳什么、小心什么"的索引**。数据来源是 `audit/gap-analysis.md` 第 2、3、4、5 节，你负责把它们整理成表并核对文件真实存在。

三张表：

1. **Tony Bai 52 讲**：每讲一行：讲号、标题（取自 `downloads/Tony Bai · Go语言第一课/` 文件名，去掉水印后缀）、`必读/选读/跳过`、对应的 ROUTE 站号、一句"为什么"。分类直接取 gap-analysis 第 2 节。**另加一列"过期提醒"**：取自下面第 3 节清单（只对有提醒的讲填，不要改动措辞含义）。
2. **项目课 21 周**：每份 PDF 一行：周次、主题、`看提纲/跳过`、对应站号。说明**幻灯片文字少，只当提纲，不逐页读**。第一周文件未下载完（`.qkdownloading`），标"缺失"。
3. **代码包模块**：每个模块一行：模块路径、`可读/可参考/跳过`、能支撑哪一站、一句风险（来自 gap-analysis 第 4 节，如"需真实 MySQL""有真 bug 可当练习""含凭据，不要入库"）。

第 3 节清单（过期提醒，来自审计，不要自己扩写）：

| 讲 | 提醒 |
|---|---|
| 16 | map 的实现细节讲的是旧结构。Go 1.24 起默认是 Swiss table。使用结论（无序、非并发安全、不可取址）仍成立 |
| 19、23、24 | 循环变量示例（三个 six、4 4 4 4）在 Go 1.22 起不再复现，每次迭代一份新变量 |
| 15 | 扩容"翻倍"的说法过时，Go 1.18 起大切片平滑增长 |
| 22、36 | `net.Error.Temporary()` 已弃用，改用 `errors.As` 加 `Timeout()` |
| 29 | 讲的 runtime 结构体和 `convT2E` 已不是现状，只看语义结论 |
| 32 | 全篇基于 Go 1.12，不含 1.14 起的异步抢占。思考题答案在新版本里相反 |
| 33、34 | 基准数字来自 2021 年 Intel 机器，不要引用。`time.After` 的泄漏警告在 Go 1.23 起不再成立 |
| 23 | defer 开销数据过时 |
| 51（曹春晖 GC） | 不含 `GOGC`、`GOMEMLIMIT`；Go 1.25 起有新的 GC 实验，以官方 release notes 为准。图是位图，抽不出文字 |
| 泛型加餐 | 用的是 `x/exp/constraints`，现在用标准库 `cmp.Ordered` |

验收：

```sh
python3 -I scripts/check-materials.py guide/MATERIALS.md
```

`scripts/check-materials.py` 你来写（≤60 行）：检查表里提到的每个 Tony Bai 讲号、周次、模块路径，都能在 `downloads/` 下找到对应文件或目录（用文件名前缀匹配）；52 讲齐全无遗漏；无 U+2014。**注意：`downloads/` 在 `.gitignore` 里，没有它时脚本必须打印 `SKIP: downloads 不存在` 并退出码 0，不得报错。**

### T3 三处小补强（只加练习文件，章内加少量文字）

目的：让练习和路线图的站 2、3 对上，不扩大篇幅。**不改任何已有测试文件，只新增文件。** 每章正文增量不超过 50 行，其余放章末参考。

**P2（样板已存在）**：Claude 先做了一版样板，已在工作区（未提交）：`exercises/p2lang/methodset_test.go`、`notes/p2-lang-core.md` 的新增小节。你**不要重做**，把它当格式模板。如果用户通知撤销，再 `git restore` 这两个文件。

**P2.5**：新增 `exercises/p2_5idiom/errcouple_test.go`。主题：错误的四种暴露方式（透明、哨兵、错误类型、行为接口）是一个耦合光谱。要求至少 4 个测试，各对应一种方式。每个测试构造"被调包"改了错误文案或包装层数之后，对应调用方是否还成立：

- 透明（只判 `err != nil`）：改文案仍成立。
- 哨兵（`errors.Is`）：包装多层仍成立；用 `==` 比较在包了一层后失败（写一个断言证明）。
- 错误类型（`errors.As` 取字段）：包装后仍能取到。
- 行为接口（`interface{ Timeout() bool }` + `errors.As`）：不依赖具体类型。

章内加一小节"错误暴露方式是耦合光谱"，不超过 30 行，配一张 4 行小表。每行结论都必须被上面某个测试守住。

**P3**：新增 `exercises/p3conc/patterns_test.go`。至少 4 个测试：

- 用容量为 N 的 channel 做信号量，原子计数记录并发峰值，断言 ≤ N。
- nil channel 在 `select` 里禁用分支：两个 channel 关闭后不再空转读零值，读到的值个数恰好等于发送数。
- 向已关闭 channel 发送、关闭 nil channel、重复关闭，三种 panic 各一个子测试，断言 `recover()` 非空并检查文案包含的关键词（运行 `go run` 看真实文案，不要猜）。
- 一个含 `sync.Mutex` 的结构体按值拷贝，`go vet` 会报 copylocks：用 `go vet` 的真实输出写进章末参考，不放进测试。

章内加一小节"channel 的三个小模式"，不超过 30 行。

验收（三章都要）：

```sh
gofmt -l exercises/ && go vet ./exercises/... && go test -race -count=1 ./exercises/p2_5idiom/ ./exercises/p3conc/ ./exercises/p2lang/
sh scripts/mirror.sh && python3 -I scripts/gate.py --fast
```

测试数量只增不减。笔记里每个输出块都要来自真机运行。

### T4 新章 P27：给 dnsproxi 加保护（限流、熔断）

这是全计划**唯一**改动 dnsproxi 的地方，范围由本节裁定，其余一律不动。

**为什么选它**：dnsproxi 已有缓存（`cache/`）、指标（`metrics.go`，含 `upstream_errors_total`）、日志（`logger.go`）。限流和熔断是项目课第 14 周、代码包 `dachang/algorithm` 的主题，但那些素材依赖中间件或质量差。这里在 dnsproxi 上直接做，和 P17 指标衔接，形成"加保护 → 指标能看到 → 压测能验证"的闭环。

**范围（只许这些）**：

1. 新增包 `dnsproxi-go/guard/`，纯标准库：
   - `limiter.go`：令牌桶，按 key（客户端 IP）限流。接口：`Allow(key string) bool`。时钟必须可注入（构造时传 `now func() time.Time`），测试里不用 `time.Sleep`。
   - `breaker.go`：熔断器，三态（closed、open、half-open）。接口：`Do(func() error) error`，open 时立即返回 `ErrOpen`。连续失败阈值、open 持续时间、half-open 放行探测数都是构造参数，时钟可注入。
2. 测试 `guard/limiter_test.go`、`guard/breaker_test.go`：
   - 限流：令牌耗尽后拒绝；时间推进后恢复；不同 key 互不影响；并发 `-race` 下计数正确。
   - 熔断：连续失败到阈值后 open；open 期内不调用函数（用计数验证）；到期后 half-open 放行一次探测，成功则 closed，失败则回到 open。
   - 至少 10 个测试函数。可以用 `testing/synctest`（参考 `exercises/p21test`），也可以用注入时钟；二选一，写明理由。
3. 在 `main.go` 接入，**默认关闭**：环境变量 `DNS_RATE_LIMIT`（每个客户端每秒请求数，0=关）和 `DNS_BREAKER=1`。接入点是 `handleQuery`（限流）和上游调用处（熔断）。被限流的查询回 REFUSED，熔断 open 时回 SERVFAIL。两种情况各增一个 Prometheus 计数器（`metrics.go` 里照现有写法，label 保持低基数，沿用 P17 的纪律：不要把客户端 IP 放进 label）。
4. 现有 `dnsproxi-go` 全部测试必须原样通过，数量不减。默认配置下行为与改动前完全一致（用一条测试证明：不设环境变量时，同一批查询的结果不变）。

**不许做**：改缓存逻辑；改 `wire/`；加 TCP；引入第三方库；改已有测试文件。

**笔记 `notes/p27-dnsproxi-guard.md`**：沿用 P21 之后新章的模板（见 `scripts/gate.py` 第 9 项检查，先看懂它要什么）。至少三个问题：

1. 为什么不限流会怎样？用 `dnsq` 压测器（`dnsproxi-go/dnsq/main.go`）开高并发，贴开关限流前后的真实读数。读数缺了就跑，不要编。
2. 令牌桶怎么工作？一个手算例子 + 一个测试对应。
3. 熔断三态怎么转？画一张 mermaid 状态图（先看 `HANDOFF.md` 对 mermaid 链路的规定，不要动 mermaid 插件），配 fake 上游（可用 `labs/compose/fakeup.go` 的思路，自己写一个返回错误的 UDP 服务）真跑一次开合循环，贴 `/metrics` 的计数变化。

Rust 对照至少 4 处，必须准确，写之前核对：`tower::limit::RateLimit`、`governor` crate、`tower` 的熔断中间件生态（如 `tower-resilience` 或同类，不确定就写"生态里没有标准库级实现"，不要编名字）、`tokio::time::pause`（对应 synctest/注入时钟）。Java 对照：Resilience4j。TS 对照：`opossum`。拿不准的库名就不写。

**入口同步**：INDEX.md、README.md、docs/index.md、docs/table.md、docs/.vitepress/config.ts 侧边栏，放在"③ 可观测性"之后（读者路径上这是站 8）。`scripts/mirror.sh`。

验收：

```sh
cd dnsproxi-go && go vet ./... && go test -race -count=1 ./... && cd ..
sh scripts/mirror.sh && python3 -I scripts/gate.py
```

完整闸门（不带 `--fast`）必须 `GATE: PASS`。还要往 `scripts/mutations.json` 加至少 3 条变异（令牌桶多发一个、熔断阈值差一、half-open 不限探测数），`mutate.py` 全杀。

### T5 P6 重排（只搬段落，不改字）

`notes/p6-capstone-dnsproxi.md` 的复盘日志顺序乱了（现为 G2、G4、G5、G6、G3、G1）。把各个"Gn 复盘"和"Gn 起手"按 G1 到 G6 的顺序重排，使新读者能线性读。

**机械验收**（这是此任务的全部裁决标准）：

```sh
# 重排前先 cp notes/p6-capstone-dnsproxi.md /tmp/p6.orig
diff <(sort /tmp/p6.orig) <(sort notes/p6-capstone-dnsproxi.md)
```

允许的差异只有：新增的一个目录小节（≤15 行，列出 G1 到 G6 的跳转）和必要的小标题。**如果出现任何被删除或被改写的行，视为失败。** 之后 `sh scripts/mirror.sh && python3 -I scripts/gate.py --fast`。

在目录小节里加一句指向 P27 和 `guide/ROUTE.md` 的话。

### T6 入口与收尾

1. INDEX.md 和 README.md 顶部各加一小段"**新读者先看 `guide/ROUTE.md`**"，不改现有表格。
2. HANDOFF.md 末尾追记"第四轮"：做了什么、验证命令、遗留。
3. `pnpm run ci`、`go test -race -count=1 ./exercises/...`、`cd dnsproxi-go && go test -race ./...`、完整闸门全部通过。
4. 提交，不推远端。

## 3. 交付报告（格式固定）

沿用 `PLAN-OMP.md` 第 3 节：先贴完整闸门输出；每任务一行（做了什么、提交号、闸门之外的验证命令）；偏差记录（没有就写"无"）；未完成项逐条列。禁用措辞：没附命令的"已验证"、"基本完成"、"其余类似"。

## 4. 验收者（Claude）会查什么

1. 重跑 `check-route.py`、`check-materials.py`，并随机抽 5 个"项目锚点"，打开文件确认函数确实在那里。
2. 随机抽 10 行 MATERIALS 的分类，对照 `audit/gap-analysis.md` 是否一致；抽 3 条"过期提醒"对照第 2 节清单，措辞没被改歪。
3. 重跑 P2.5、P3 新增测试；把一个参考实现改坏，确认对应测试会失败。
4. P27：重跑 `guard` 的测试 20 次看是否抖动；默认配置下 dnsproxi 行为不变；`mutate.py` 重跑。
5. P6：自己再跑一遍 `diff <(sort …)`。
6. 通读 P27，质疑 Rust 对照里每一个库名和 API。

## 5. 明确不做（防范围膨胀）

1. 不写语言深潜章（GC、调度器、map 内部、接口运行时）。这类内容需要对 Go 1.26 源码级观察，且 Tony Bai 对应讲（16、29、32、51）读者自己有，MATERIALS.md 已标出过期点。
2. 不写业务栈章节（Gin、GORM、JWT、Kafka、ES、IM、Feed、支付等），不做分布式事务、Raft、注册发现。需要时作为后续选题，另立计划。
3. 不教、不引入 Gin、GORM、Hertz、Kitex、GoFrame。
4. 不改 `dnsproxi-go` 除 T4 范围外的任何代码；不加 TCP（作为后续选题保留）。
5. 不改 `vendored/`、`labs/` 现有内容；`exercises/` 只新增文件。
6. 不改已有章的编号、slug；不合并、不删除任何章。
7. 不把 `downloads/` 内任何文件、文字、图片、代码复制进仓库。
8. 不为"更完整"而增加站数、章数、小节数。篇幅限额写在各任务里，超了就删。

## 6. 后续选题（本轮不做，记在这里供用户挑）

按价值排序，每项都可单独立计划：给 dnsproxi 加 DNS over TCP（接 Tony Bai 36-38）；语言深潜系列（由 Claude 而不是 OMP 写）；服务注册发现与负载均衡（内存版）；链路追踪与 OpenTelemetry；WebSocket 与 IM；Feed 流。

## 偏差记录

（OMP 在此追记，没有写"无"。）
