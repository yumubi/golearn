# HANDOFF：golearn 阅读体验改造，交给 OMP

更新日期：2026-10-12。**改造已全部完成**：P2/P15 试稿经用户确认后，已推广到全部 17 章 + 入口文件。完整结果见文末"执行结果（2026-10-12）"。以下原始规划保留作背景。

原始指令："做好计划和发散然后更新 handoff，具体内容我会让 omp 来做。"
已确认方向："问题驱动讲义，细节按需查"。

## 接手先做什么

先读这份交接、`notes/p2-lang-core.md`、`notes/p15-slog.md` 和它们引用的练习。按下文提纲准备 P2/P15 试稿，用户确认阅读体验后再推广。提纲是实施建议，允许根据代码证据和试读反馈调整。

参考先放同章章末，保留源码与站点的阅读体验；这是本次规划的默认选择，用户尚未单独确认。若用户后续要求独立页面，以最新要求为准。已确认的风格无需重新询问。

用户不喜欢小步骤汇报。每阶段只汇报影响阅读或执行的发现，日常操作合并说明。

## 读者与目标

读者熟悉 Rust，有 Java/Kotlin 底子，有自己的 Rust DNS 代理。学 Go 是为了读懂代码、写服务、上线后能观察运行情况。核心反馈是"讲解太琐碎了"，既涉及对话中的步骤汇报，也提示教程需要重新安排重点。

保留 Rust 心智模型对照，用例子解释迁移时容易误判的地方。jyy 风格主要借鉴问题、实验与因果关系的组织方式。口头禅和吐槽由内容决定，技术判断要有证据。

## 样例诊断

以下来自当前文件，不代表全课程审计。P2 为 138 行，P15 为 165 行，行数只作定位。

| 位置 | 当前阅读障碍 | 改造动作 |
| --- | --- | --- |
| P2 §1 | 先给 Rust/Java/Go 全面对照表，再给接口例子；因果链中夹着自我纠正 | 先展示两个类型被同一接口接收，再讲隐式满足与使用方接口；方法归属规则单独准确解释 |
| P2 §3 | struct 小节同时收纳 Option、Result、match、enum、泛型，主题不断切换 | 零值留在叙事中，其余按是否支撑主问题分配到正文或章末参考 |
| P2 §5 与练习 | type switch 独立展开，练习指向不存在的 error 正文 §6；§6 实际是 Take-away | 补足 error 解释，重建指向；包装错误的现有练习在 `exercises/p1tool/error_test.go`，不要说成 p2lang 已覆盖 |
| P2 自检 | 又列一套问题；练习表遗漏实际存在的零值、泛型、interface nil 测试 | 将关键自检并入练习的预测问题；核对实际测试，保留必要陷阱 |
| P15 开头与 §15.0 | 两次交代日志柱定位；开头已经有很好的 JSON 输出 | 合并目标与输出，从请求定位问题进入 |
| P15 §15.2 | 请求日志故事中插入四对象表与架构图，Record/Attr 比当前任务更靠前 | 正文先给可用 Logger/JSONHandler；完整对象职责放章末参考，定制细节交给 P16 |
| P15 §15.3 到 §15.6 | 守卫、脱敏、ctx、middleware 并列铺开，重要性相同 | req_id 关联作主线，守卫与脱敏作上线边界，解释它们为什么影响当前服务 |
| P15 §15.6 | 把 middleware 层次与 label 基数写成因果关系 | 核查代码后区分指标标签取哪些字段，与 middleware 怎样传递请求上下文 |
| P15 练习与 Take-away | 练习再次复述每节，Take-away 有 5 条；handler 改造题与 P16 相邻 | 选一项可观察的行为改造，其余测试作证据；handler 题链接 P16；收拢成可复述的判断 |
| README | 固定 15 分钟仪式让人先看测试全绿，中间正文被称为字典 | 试改确认后，入口说明何时读正文、何时查细节，以及怎样用练习验证理解 |

另有技术表述需要 OMP 核实：P2 的 Rust 孤儿规则、泛型 interface、GC 与 aliasing，以及 P15 的 ctx 传播说法。查语言规范或官方文档，记录依据后修正。类比必须写清适用边界。

## 发散结果与取舍

| 候选方向 | 读者体验 | 本课程中的位置 |
| --- | --- | --- |
| 问题驱动讲义 | 带着一个工程问题，观察例子，再理解原因和边界 | 用户已选择，作为正文 |
| 极简速查 | 直接查语法、API 和 Rust 对应概念，解释空间较少 | 适合 P1 和章末参考 |
| 项目串讲 | 跟着 httpkv 或 dnsproxi 的变化理解知识，章节依赖更强 | 用于服务搭建、可观测性阶段的实例 |
| 完整参考手册 | 覆盖主题的大量细节，容易回到当前平铺状态 | 以官方文档链接补足 |

采用问题驱动正文，加章末参考，在工程阶段沿现有项目展开。保持三个学习阶段和按需参考入口，先调整章内结构；试读后再判断是否需要合并或拆章。

详细程度按决策价值分配。正文讲明白遇到什么问题、为什么这样写、哪里会失效。API 清单、罕见语法、历史版本背景放到参考。一个问题用一个主要例子，例子可以持续演进。复杂陷阱允许多解释几段，避免每个小节都新增孤立 demo。

不设每章 60 行的硬上限。试读目标是读者能快速找到主问题，读完能解释原因并完成行为改造。初稿正文以 3 到 4 个主要问题为参照，章节需要时可调整。参考先放在同一文件，以明确标题区分；大到妨碍导航时再拆分。

章头目标合并成一处。练习与自检合成行为预测、实际改造和观察结果。Take-away 收拢为少量可用于工程判断的结论。Rust 对照出现在产生迁移误判的地方；完整语法表按需查。

## 参考阅读

2026-10-11 直接读取以下原站内容。当前会话没有可调用的 taily/Tavily 搜索连接器，通用搜索工具没有返回可用内容，因此改为直接读取教程原站。这里没有声称用过 Tavily。

| 参考 | 读到的组织方式 | 借鉴边界 |
| --- | --- | --- |
| Go by Example，Errors：`https://gobyexample.com/errors` | 代码旁解释对应行为，再提供运行输出和延伸文档 | 解释紧贴代码；熟练程序员已懂的语法无需逐行讲 |
| Learn Go with Tests，Hello, world：`https://quii.gitbook.io/learn-go-with-tests/go-fundamentals/hello-world` | 行为要求、测试、实现、重构持续推进 | 借鉴行为验收；本课程已有参考实现，练习应预测或改造行为，不照搬从零 TDD 全流程 |
| Diátaxis：`https://diataxis.fr/` | 区分学习教程、操作指南、参考和解释 | 用来判断内容该放哪一层；当前规模无需建立四套目录 |
| jyy 并发 Bug 讲义：`/Users/akka/jyywiki/OS/2023/build/lect13.ipynb.html` | 背景问题、课堂 demo、Take-away、实践题 | 借鉴问题到实验再到结论的顺序，以及简短的重点收拢 |

这些提供组织方式的参考。适配判断由本次诊断提出，效果需用户试读验证。

## 两章试改提纲

### P2：用 Rust 的经验写 Go，哪里需要换直觉？

目标：能读懂小接口、区分嵌入与多态、正确处理错误。入口用现有 Dog/Robot/Speaker，减少新背景。

正文候选顺序：

1. 两个类型没写 implements，为什么能被同一个函数接收？接隐式满足、使用方小接口、方法归属规则。
2. 嵌入后能调用方法，为什么仍不能当作继承？沿现有 Base/Greeter 对比方法提升与接口分派。
3. 没有 Result 和 `?`，错误怎样保留上下文并被调用方识别？用现有 P1 error 练习支撑 `%w`、`Is/As`，明确参考位置。
4. 什么时候零值直接可用，什么时候必须初始化？用现有 Builder 测试建立直觉，给必要边界。

type switch、泛型与语法对应表进入章末参考，并保留实际练习链接。interface nil 已有测试，至少在正文相关位置提示陷阱和参考入口。

练习围绕一个预测和一个改造展开，例如预测接口分派结果，再增加一个满足 Speaker 的类型。选择后写清观察结果与失败原因。Take-away 回答上述主要问题。

### P15：用户报了一个 404，怎样找到对应请求？

目标与 JSON 实拍合并。保留 httpkv，先展示请求与 `X-Request-ID`、日志字段的关系。历史实拍注明来源，新输出执行后再记录。

正文候选顺序：

1. 这条日志怎样变成可查询事件？给 JSONHandler 最小配置与一次查询，Rust tracing 对照在此出现。
2. req_id 从哪里来，为什么 `InfoContext` 本身没有替你输出它？串起 middleware、context 与日志调用，区分标准 handler 和后续 P16。
3. 上线时怎样避免敏感字段和无效计算？用当前行为说明 LogValuer 与 Enabled，保留对应测试证据。

Logger/Handler/Record/Attr 完整表、级别清单及 handler 扩展放章末参考。高基数纪律链接 P13。P16 负责自动注入与 Handler 契约，这章用明确入口承接。

练习选择一次请求，核对响应头与日志中的 req_id，并验证指定敏感字段不出现。使用已有测试与服务实现。若运行代码已演进到 P16，明确当前版本与 P15 教学阶段的区别，避免贴过期调用点。Take-away 聚焦可查询、请求关联和上线边界。

## OMP 实施顺序

1. 核对样例。每个主要判断对应实际代码或官方文档，查清失效引用与前后章边界。旧内容分别保留、合并、移到参考、删除或修正，交付时简述重要迁移即可。
2. 试改两章。先修改 `notes/`，再同步同名 `docs/`。学习目标、练习与 Take-away 均有明确位置，Rust 对照支撑主问题，读者能通过链接找到移走的细节。
3. 验证试稿。完成相关测试、镜像检查、lint 和站点构建，交给用户读 P2/P15。确认重点是否清楚、解释是否足够、参考是否打断阅读。
4. 推广。用户确认两章阅读体验后，调整其余 notes，按章节需要采用结构。更新 README/INDEX、站点入口和跨章引用，保留原课程能力覆盖。
5. 交付。完成最终检查，简述改造范围、内容迁移位置和验证结果。遇到环境限制，记录未验证内容与原因。

## 约束与验收

内容源为 `notes/*.md`，网站是镜像。当前站点把 h1 转为 frontmatter `title`，正文随后同步。更新标题或小节时，查找跨章引用、练习说明、README/INDEX 和站点导航。入口镜像包括 `docs/index.md`、`docs/table.md`、`docs/dnsproxi.md`，按实际映射核对。

保留每章学习目标、练习与 Take-away，允许合并重复部分。保留 Rust 心智模型线和原课程覆盖。已有参考实现、module 与实验数据继续使用，代码确需调整时保持行为验收通过。

修改过的 Markdown 跑：

```sh
node /Users/akka/.agents/skills/no-slop/slop-lint.mjs <file>
```

要求 0 violations。中文文档的 lint 分数不能证明阅读质量，人工检查例子、因果链和层次。本次 HANDOFF 修改前后均为 0 violations。

试改阶段在根目录执行：

```sh
go vet ./exercises/p2lang/ ./exercises/p1tool/ ./exercises/p15slog/ ./exercises/p8fast/httpkv/
go test -race -count=1 ./exercises/p2lang/ ./exercises/p1tool/ ./exercises/p15slog/ ./exercises/p8fast/httpkv/
pnpm docs:build
```

最终推广后，在根 module 与 `dnsproxi-go/` 各自执行：

```sh
go vet ./...
go test -race -count=1 ./...
```

根目录再执行 `pnpm docs:build`，检查所有 notes 镜像正文与标题一致。页面人工检查章头、正文到参考的分界、练习入口和链接可用性。用户试读确认是不琐碎、知道重点在哪的验收依据。

旧交接记录的 18+3 包全绿是历史基线，本次未运行 Go 测试或站点构建。包数与结果以执行时为准。练习是参考实现，测试全绿需要配合预测与行为改造才能证明理解。

本次 `git status` 确认 `/Users/akka/Dev/learning/go` 不在 Git 仓库中。OMP 修改前重新确认状态，采用本地备份或 diff 保存基线。实际 package.json 的构建命令是 `pnpm docs:build`，原交接的 `pnpm build` 不存在。

5353 为 mDNS 端口，DNS 冒烟用 15353 到 15356。shell 中注意 zsh 的 `$IDENTIFIER:modifier` 解析。

---

## 执行结果（2026-10-12）

### 范围

| 交付 | 内容 |
|---|---|
| 试稿（用户已确认） | P2（四问题：隐式满足/嵌入非继承/error 上下文/零值）、P15（三问题：可查询事件/req_id 链路/上线边界） |
| 推广 | P1 轻触（cheat sheet 保留）、P3/P4/P5/P8/P9/P10/P11/P12/P13/P14/P16/P17 问题式重组、P6 轻触（G1-G6 复盘是工程日志，只加"怎么读"导语+修 30 处 em-dash）、P18 合并结构差异节 |
| 入口 | README/INDEX/docs/index.md 的"固定 15 分钟仪式+中间是字典"改为新读法（正文当讲义读→卡壳翻章末参考→预测/观察/改造三类练习）；课程表 P2/P15 行更新定位 |
| 镜像 | docs/*.md 全部从 notes 重生成（h1→frontmatter title），20 文件与源逐字节一致 |

### 顺手修正的技术错误（均有代码/官方文档证据）

- P16：slog.Handler 接口是 4 个方法，原文写 5 个但只列 4 个
- P13：练习表"故意造 UNMATCHED"行与代码矛盾（逐路由包装下未注册路径不经过任何 Instrument，404 不进 requests_total），已实测改写；§13.7/§13.8 特意保号，P15/P16/dnsproxi-go 引用不断
- P9：原文件有两个 Take-away（结构错误），已合并
- P4：KVStore 方法数 8→10（storage/storage.go:58）；"P3 练习 5"改为实际测试名 TestCounters
- P10：行数描述改为实测 790 行
- Go 方法同包 vs Rust 孤儿规则、GC 与 aliasing 表述、slog"标准 handler 不读 ctx"：均按语言规范/官方文档核实改写（P2/P15）

### 验证

- slop-lint：21 个文件（17 notes + README/INDEX + docs/index/table）全部 0 violations
- `go vet ./... && go test -race -count=1 ./...`：根 module 18 包全绿；dnsproxi-go 3 包全绿（exercise 注释里 3 处旧引用同步后编译通过）
- `pnpm docs:build`：通过，新标题进 dist
- httpkv 冒烟：X-Request-ID 响应头与日志 req_id 逐字一致（2026-10-12 新实拍）

## 基础内容扩充（2026-10-12 第二轮）

用户反馈"基础的内容讲的有点少"，且"大家用 go 很多是写工具的"。先广泛
调查（rust-for-frontend / learn-go-with-tests / Go by Example / Tour of
Go / 极客兔兔 / 字节青训营 / 100 Go Mistakes / AI 时代范式），确认四块
缺口后落地：

| 新章 | 定位 | 练习 |
|---|---|---|
| P1.5 语法基础速通 | 12 节 Rust 对照（iota/切片底层/map 三坑/rune/defer 三语义等，全部是 grep 零命中缺口） | p1_5basics 8 tests |
| P2.5 范式与惯用法 | 三条设计哲学推出惯例、接口黄金法则、错误链、包组织命名 | p2_5idiom 6 tests + legacy.go 反面样板 |
| P19 生态选型地图 | 标准库优先铁律、默认选型表、本仓依赖点名、新库评估法 | p19eco 4 tests + stringer |
| P20 写 CLI 工具 | Go 的基本盘是工具：stdout 神圣/flag→cobra/WalkDir/退出码 0-1-2/日志四纪律/交叉编译发布 | p20cli 6 tests + wcgo cmd/ |

配套：P1 章末工具链进阶（ldflags/交叉编译/pprof/golangci-lint/go:generate）；
①弧改为 P1.5 → P2 → P2.5 → P3 → P14；课程表 23 章；侧边栏①④组更新；
P6 骨架引用对齐收官实况（proxy.go+TODO → 实装目录）+ wire_test 陈年
TODO 清理。

验证：全部 slop-lint 0（em-dash 连表格占位符都被 eslint no-em-dash 拦，
已全改写）；`pnpm run ci` 全绿（eslint/knip/docs:build/双 module go
test，含 wcgo 真机退出码 0/1/2 实证）；新页浏览器实测 h1/侧边栏/课程表。
提交：4236290（补基础）→ 5a0c2a1（P20）→ d6be429（P6 对齐）。

### 遗留（有意保留）

- P3 章末参考的 goroutine/tokio 切换成本实验保留 `[INFERENCE，待你实测]` 标记：这是留给读者的实测作业，不是待修内容
- P6 复盘内文未重写（历史价值），仅导语+lint 修复
- 基线已入 git：仓库根 2026-10-12 `git init` + 首次提交 0786ae9（135 文件，排除 node_modules/dist/cache）；`/Users/akka/Data/tmp/golearn-handoff-baseline-20261012/` 备份保留为双保险

## T1-T6 流水线执行（2026-10-12 第三轮，PLAN-OMP.md）

按 `PLAN-OMP.md` 六任务流水线执行，验收者=另一 Claude（重跑闸门、
抽查台账、重跑变异）。完成裁定：`python3 -I scripts/gate.py` 末行
`GATE: PASS`。

| 任务 | 内容 | 提交 |
|---|---|---|
| T1 | 三批审读九章 + FIXED 51 条（批A baad78d P3/P13-18、批B 4d4e4ba P8-12、批C c4e9fa5 九章台账） | baad78d / 4d4e4ba / c4e9fa5 |
| T2 | P21 新章 testing/synctest + fuzz（4 问题、Sleep 版压测 3 次 flake 实锤 vs synctest 确定性、Rust 对照 6 项）+ exercises/p21test（Retry/Debounce/cmd/retryrun 退出码 0/1/2/130）+ FuzzParseArgs（60s 28,529,755 execs 0 crash + 植 bug 演示）+ 变异 3 条 KILLED（累计 99）+ 五处入口 + P3/P14 章末链接 | 1da476a |
| T3 | 1.21-1.26 七特性逐个 go doc 核实 + 可编译例子（p1_5basics/p2lang/p3conc/p8fast/p20cli 各一，-race 全绿）+ audit/t3-features.md | 17114c7 |
| T4 | GOMAXPROCS 1.25 cgroups 边界（源码核实 + P8/P9/LESSON 落点）、Green Tea GC 双跑（results-gc-2026-10-12.md，区间交叠噪声内）、jsonv2 实证（默认关 + Unmarshal 2 倍速/分配 6→1，P19 专段 + P8 句）+ audit/t4-experiments.md | e91e4db |
| T5 | 浏览器实测 audit/t5-site.md：桌面 1280 + 移动 390，P21/P14 + 5 旧章 + 课程表 + 首页，12 项断言全 OK（含 mermaid SVG 实渲染、汉堡菜单、表格 contained） | （无代码改动） |
| T6 | 本节 + 完整闸门（无 --fast，99 变异）+ 全量回归 | （见提交） |

验证：每任务 `gate --fast` PASS + `pnpm run ci` 全绿后提交；T6 跑
完整闸门（含 99 变异 + P12 PG 容器）。slop-lint 全部改动的 md 0
violations（含 results/LESSON 历史 em-dash 全文件清理）。

### 补充（同日）：P22 工具链深潜与文档工程

用户反馈"工具链讲太少、文档也太少"。缺口调查：P1 只有 196 行速通 +
章末命令表；godoc 注释规范全仓只有 P2.5 一段；Example 测试/pkg.go.dev/
版本发布/go.work/GODEBUG 体系全零覆盖。落新章 P22 + 练习 p22tool。

- notes/p22-toolchain-docs.md：5 问题。pprof 完整取证流程（httpkv 实采
  heap 2681kB/allocs 5329kB/goroutine）、lint 与 generate 纪律、
  GODEBUG（默认值跟 go.mod 版本走）与 GOEXPERIMENT 双开关体系、
  go.work 不提交/replace 污染、文档工程三层（doc comment 规范/
  Example 测试/go/doc 可编程访问）+ semver 发布（v2 进 module path）
- exercises/p22tool：docstats 库（go/parser+go/doc 统计导出符号文档
  覆盖率；单 spec 块注释回退 GenDecl.Doc 的 godoc 语义，实测踩过）+
  cmd/docstats CLI（-d/-version，退出码 0/1/2，ldflags -X 注入版本
  实证）+ 4 tests -race
- 五处入口 + P1 章末指引 + docs 镜像。gate --fast PASS + pnpm ci 全绿。
- 提交：e948159

## 廖雪峰风重写执行记录

全 24 章重写 + P23-P26 四章新增（体系线），按 HANDOFF-REWRITE.md
分 7 批完成。每批验证：slop-lint 0 → mirror 逐字节 → gate（批7 为
完整闸门）→ pnpm run ci → 提交。

- 批1 `c75b569`：P2、P3、P1.5（样板批，试读通过后推进）
- 批1补记 `e0fab3f`：P1.5 补遗
- 批2 `ca2c59f`：P2.5、P14、P4、P5
- 批3 `6cfd4b7`：P8、P9、P12、P11
- 批4 `7db63f5`：P13、P15、P16、P17、P18
- 批5 `fa53ee1`：P1、P19、P20、P21、P22。gate 两处对齐（提交信息
  有旧值）：结构检查 `"## "` 小节 ≥4；占位语正则裸 `略$` 改
  `（略）`（原正则误伤"策略"行尾）
- 批6 `8a2428b`：P23（Linux 运行时，httpkv 骨架）、P24（网络协议栈，
  dnsproxi 教具 + tcpdump 逐字节）。章数 24→26。macOS 无 strace/ss
  沿批4 先例文字说明 + Linux 对照表
- 批7（本次）：P25（容器云原生：多阶段构建 351MB→26.6MB 实测、
  GOMAXPROCS 按 ceil(cgroup cpu.max) 取整联动 1.5核→2/4核→4、
  healthcheck）、P26（compose 编排收官：dnsproxi+httpkv+Go fake
  upstream 三服务真跑，服务名互通，健康条件依赖；实战文件存档
  labs/compose/）。章数 26→28。完整闸门（含 99 变异 + PG 容器）
  PASS。

- 批8 `a5e3f3b`（批后巡检补遗）：P10（gRPC）漏在批次表外（24 章
  清单笔误），补齐：问题式骨架 → 名词小节（从 .proto 到能跑的
  服务/四种流的签名/拦截器：函数洋葱/bufconn）+ 体系背景
  （RPC 三层叠：HTTP/2/protobuf/stub）。
- 批9 `f6e914d`（§8.2 落地）：用户裁决豁免批1-5 旧章，P23-26 四章
  代码占比 20-27% → 25-31%（全部真跑素材：SIGKILL 对照/exit=137/
  三次握手 tcpdump 真抓/dig +tcp 结果反转/BPF 修正/inspect 元数据/
  compose config 渲染）。40% 硬线对表格密集实战章不适用，主旨
  （每章 3 分钟跑出例子）已满足。
- 批10 `bf22d2d`（audit UNKNOWN 清理）：p9 `sql.NullXxx` 泛指改
  `sql.NullString`（2 处）；p20 `scanner.Buffer` 改全
  `bufio.Scanner.Buffer`；p6 `binary.BigEndian.Uint16` 保留
  UNKNOWN（var 方法，go doc 局限，正文无错）。27 章 MISSING 0。
- 批11 `d806326`（社区调研落地）：P21 14%→16%、P22 18%→21%
  （批9 式真跑补块）。P21 新增「-race：动态分析武器」节（DATA
  RACE 双栈实拍）+ 假时钟起点实拍（2000-01-01 UTC）+ fuzz 语料
  -run 重放实拍（0.2s）；修正错误：-race 与 fuzz 非互斥（实测
  execs 40 万/s→6 万/s）。P22 pprof top 实拍（syscall 60% flat
  读法）+ goroutine 快照（idle 7 个）+ schedtrace 实拍（SCHED 行
  读 GMP）+ 新增 dlv 节（真机会话，ambiguous 断点坑）。预测题各
  +2（对应真实误区）。验证：slop-lint 0×2、mirror、gate --fast
  PASS、ci 全绿、audit MISSING 0（P21 18 条/P22 9 条）。
- 批12 `cbe4c2d`（调研裁决延伸：预测环节补齐）：P20 补全新
  预测环节（27 章唯一缺预测的实战章，4 道误区对应题：go run 吃
  退出码实拍 exit=1 vs build exit=2、Scanner 长行静默截断、
  2>/dev/null 错误去向、WalkDir 部分失败）；P23 +1（kill -9
  可否优雅处理）；P24 +1（cache hit 上游不转发，fake upstream
  计数实证 upstream got 1）。P13/P15 复查合格不动。验证：
  slop-lint 0×3、mirror、gate --fast PASS、ci 全绿。

体系词汇随文解释（§10.2 第三层）落实：fd 0/1/2、signal、握手、
镜像层、sidecar 型探针、namespace/cgroup 等首现均有一句话解释。
实战命令全部真跑实拍；踩坑实录（Mach-O exec format error、容器内
127.0.0.1、yaml lint 三轮）进入正文。

### 社区风格调研结论（2026-10-13，tavily）

- 标杆文体（Go by Example / LGWT / Effective Go / Diátaxis）无"代码行数
  占比"标准；真实指标是"每节可跑例子 + 真实输出"（Effective Go 的
  `prints` 体、Diátaxis 的 deliver visible results early）。
- Write the Docs 原则明言"太多例子伤 skimmable"；40% 行数线不再作为
  追求目标，P23-26 已达 25-31% 视为收敛。
- "预测后运行"有 UCLA 实证（predict 组显著优于 tell-and-practice）；
  理想预测题 = 40-60% 首答错误率（对应真实误区）。
- 廖雪峰本人哲学"代码越少越好"，他的短板是缺实战项目（社区评价
  "看完什么都做不了"）；本教程以 dnsproxi/httpkv 载体补齐，定位正确。
- 后续执行裁决：P21/P22 做批9 式真跑补块（文体是 tutorial，特性天然
  可跑）；P19 生态（10%）是 reference/explanation 文体不补；P6 豁免。
