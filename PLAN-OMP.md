# PLAN-OMP：下一轮工作计划（执行者：OMP，验收者：Claude）

起点提交见 `scripts/BASE`。上下文先读 `HANDOFF.md`（历史决策，不重写，只追记）、`INDEX.md`、`README.md`，再读本文。用户的两条最高优先级意图不变：基础要厚（不留"默认你会"的空洞）；Go 的基本盘是写工具（新练习尽量是带 `cmd/` 入口的可跑工具）。HANDOFF.md 里的硬规则（零 em-dash、整数 range、章号 P21 起、五处入口同步、镜像逐字节一致、mermaid 链路勿动）全部继续有效。

## 0. 先说清楚为什么有这份文件

上一轮之前的几次返工有同一个模式：报告"已完成"，但笔记写的测试名不存在、"覆盖五节"后面列了六项、命令打出来根本没有那个测试、一句"不带参数退出码 2"在真机上是错的。每一次都是"没真正跑过、没逐条对过"。所以本轮的规则是：

**完成与否由 `python3 -I scripts/gate.py` 的输出裁定，不由你的报告裁定。** 你写的任何"已验证"都必须能对应到一条可重跑的命令。下面所有任务的验收都落在闸门或可重跑命令上。

## 1. 反简化机制（先读，违反 = 整轮返工）

| 机制 | 内容 | 防的是什么 |
|---|---|---|
| 闸门 | `scripts/gate.py`：CI、镜像、slop-lint、em-dash、整数 range、占位语、引用对账、变异测试、测试数不减少、台账格式。交付前必须不带 `--fast` 跑出 `GATE: PASS` | 自述完成 |
| 引用对账 | `scripts/audit-claims.py`：笔记里的路径、测试名、标准库符号逐条查证，并反查"有测试笔记没提" | 笔记与代码漂移 |
| 变异测试 | `scripts/mutate.py` 读 `scripts/mutations.json`：把参考实现改坏，对应测试必须失败且失败原因对得上 | 恒绿的测试、没守住行为的练习 |
| 审读台账 | `audit/pXX.md`，格式见 `audit/TEMPLATE.md`；每条断言要有可重跑的命令或出处，闸门数合格条目 | "看了一遍没问题" |
| 偏差登记 | 任何对本文范围、数量、做法的缩减，必须先写进文末"偏差记录"并停下来问用户，不得自行降级 | 悄悄缩小问题 |
| 抽查 | 验收者会随机重跑台账里至少 5 条、重跑 2 条变异、通读新章并质疑 Rust 对照的准确性。台账里任何一条重跑不一致，整份台账作废重做 | 凑数 |

禁止事项（每条都有人踩过）：

1. 把测试改成不会失败（删断言、放宽比较、`t.Skip`）来让它变绿。`t.Skip` 必须带 `skip-ok: 理由`，测试函数总数不得减少。
2. 在笔记里写没跑过的数字、输出、"实测"。实验数据缺了就去跑；跑不出来就如实写"未能复现"和原因，不得编。
3. 发现问题但不修、只记在台账里。台账不允许有 OPEN 状态，要么 FIXED 要么证明它不是问题。
4. 用"同上""类似""其余同理""略"代替具体内容（闸门会扫）。
5. 只改 notes 不跑它引用的代码，或只跑 `--fast` 就交付。
6. 把"我找不到问题"当作结论而不给证据。某章 FIXED 为 0 是可疑信号，必须附"零发现自检"≥10 条。

遇到真的卡住（外部仓库缺失、联网不可用、需求有歧义）：停下，用下面"阻塞报告"格式写清楚，不要自己找个更容易的替代任务。

## 2. 任务清单

按 T1 到 T6 顺序做，每个任务一个提交（提交信息写清范围），提交前跑 `gate.py --fast`，T6 之前跑完整闸门。

### T1 全章事实审读（体量最大，先做）

范围见 `scripts/audit-scope.txt`（21 章，批 A 到 C，P6 最少 12 条，其余 20 条）。上一轮只对账了"练习三类是否齐、测试名是否存在"，**没有核查事实**，这才是本任务的内容。

每章产出 `audit/pXX.md`，并满足：

1. 合格条目数达标（见 scope），且六个必须覆盖类别每类至少 2 条（版本与依赖、API 形态、数字、实拍输出、章间引用、Rust 对照）。
2. 每章至少重跑 3 条笔记里的实拍输出或命令，台账"结果"栏贴实际输出的关键行；与笔记不一致就改笔记。
3. 每章至少新增 2 条变异（加进 `mutations.json`），选的是该章 Take-away 里最核心的行为；`mutate.py` 必须全杀。已有变异不得删。
4. 练习三类（预测/观察/改造）是否齐、答案是否实测，在台账末表里逐项填写；缺的补，并且补的题答案要先实测（做法见 git log 里 151dbfa）。
5. 能自动查的先用脚本：`python3 -I scripts/audit-claims.py notes/pXX-*.md`，UNKNOWN 项必须在台账逐条处理（它们是脚本判定不了、必须人看的地方）。注意脚本只覆盖路径、测试名、标准库符号，P17、P18 这类章它几乎抓不到东西，所以台账是主力，脚本只是兜底。
6. 版本类断言一律以本机为准：`go version`（现为 go1.26.4）、`go.mod`、`go doc`、`$(go env GOROOT)/src`。联网查官方文档时在台账写 URL 加引文。
7. 修正笔记后跑 `scripts/mirror.sh`，再 `gate.py --fast`。

特别点名要查（历史上这些地方出过错或最容易过期）：

- P3：goroutine 与 tokio 的切换成本实验保留 `[INFERENCE，待你实测]` 标记，勿动，但它周围的其余数字要查。
- P13/P15/P16/P17/P18：笔记里贴的 /metrics 与 JSON 日志实拍，重跑对比；指标名、label 名、端口逐个对代码。
- P10：proto 与生成代码的版本、`grpc.NewClient` 与已弃用的 `grpc.Dial` 说法。
- P11/P12：goose 与 sqlc 的命令行参数、版本，对 go.mod 与 `go run` 实际可用的子命令。
- P4/P5：精读对象在 /Users/akka/oss/github.com/clarkqaq/ 下；仓库不在就在台账顶部写明，并把"需要该仓库才能核"的条目标为不可核（不计入合格条数，其余条数照样要达标）。
- P6：G1 到 G6 是历史日志，只核事实性引用（文件名、函数名、数字出处），不改叙述。

### T2 新章 P21：并发代码与输入处理怎么测（synctest 虚拟时间 + fuzz）

缺口依据：现有 P3、P14 的并发测试靠 `time.Sleep` 制造重叠窗口（`exercises/p14pipe/p14pipe_test.go:75`、`exercises/p3conc/conc_test.go:90`），仓库里没有一处 `testing/synctest`，也没有任何可运行的 `FuzzXxx`（`notes/p8-fast-track.md:179` 只有一段示意代码块 `FuzzParseQuestion`，不在任何练习包里）。P21 的 fuzz 部分要和这段示意衔接：正文引用它并说明 P21 给出可运行版本，同时核对那段示意代码本身是否正确（能否编译，`-fuzz` 命令是否可用）。本机 Go 1.26 里 `go doc testing/synctest.Test`、`synctest.Wait` 可用，`$(go env GOROOT)/src/testing/synctest/synctest.go` 是一手文档。

产出：

1. `notes/p21-testing-concurrency.md`，沿用三件套。问题驱动，至少 4 个问题，每个问题"场景、例子、原因、失效边界"四段齐：
   - 问题 1：基于 `time.Sleep` 的并发测试为什么既慢又会偶发失败？**必须有实测数据**：写一个靠 Sleep 的 debounce 测试，在 CPU 压力下 `go test -count=500 -cpu 1,4` 记录失败次数与总耗时，与 synctest 版本对比（失败数、耗时）。如果真机压不出偶发失败，如实写"压到 N 次未复现"并把时间余量收紧到能复现为止，不得编数字。
   - 问题 2：synctest 的 bubble 怎么让时间变成可控的？`Test` 与 `Wait` 的语义，假时钟起点，什么叫 durably blocked（读源码注释，写出哪些操作算、哪些不算，至少用一个实验证明一个"不算"的例子，例如被互斥锁或真实 I/O 阻塞）。
   - 问题 3：bubble 里会出什么事？goroutine 泄漏、死锁时的行为，用最小例子触发并贴真实输出。
   - 问题 4：fuzz 测试怎么发现解析代码的洞？对象用 `exercises/p20cli` 的 `ParseArgs`（工具定位）。
   - Rust 对照至少 6 处且必须准确：`tokio::time::pause`/`advance`（`start_paused`）、`proptest`、`cargo fuzz`/libFuzzer。写之前核对 Rust 侧事实，写进台账 `audit/p21.md`。
   - 章末参考放 API 清单与 fuzz 命令行参数，不堵正文。
2. `exercises/p21test/`，**工具形态**：`cmd/retryrun/main.go` 是可跑的"失败自动重试地执行命令"小工具（flag：最大次数、基础退避、退出码透传，退出码规则沿用 P20 的 0/1/2）。库代码有可注入时钟的退避重试 `Retry`、`Debounce`。测试至少 8 个，覆盖预测、观察、改造三类，其中必须包括：
   - 用 synctest 断言退避总耗时恰好是预期的虚拟时间（如 100+200+400ms），而真实耗时在毫秒级；
   - ctx 取消时重试立刻停止；
   - 一个故意制造 bubble 内死锁/泄漏的观察用例（用子进程或 `t.Run` 隔离，不能让整包挂住）；
   - `FuzzParseArgs`（针对 `p20cli.ParseArgs`）：种子语料≥8 条，性质至少三条（不 panic；出错时 `errors.Is(err, ErrUsage)` 或明确的其它类别；`ExitCode(err)` 只会是 0/1/2）。
3. fuzz 必须真跑：`go test -fuzz=FuzzParseArgs -fuzztime=60s ./exercises/p21test/`，把 execs 数与结果写进笔记。若发现真问题就修 `ParseArgs`（并让 P20 笔记同步），把触发输入留在 `testdata/fuzz/` 作回归；若没发现问题，再做一次"植入已知 bug"演示（改坏 ParseArgs 的一处，证明 fuzz 在 60 秒内能抓到，贴输出，然后还原），这条演示同时作为一条变异记录。
4. `mutations.json` 至少 3 条 P21 变异（例如把退避倍数改错、去掉 ctx 检查、让 Debounce 不重置计时器），全杀。
5. 五处入口同步（INDEX.md、README.md、docs/index.md、docs/table.md、docs/.vitepress/config.ts 侧边栏放第④组"按需查"或并发组，择一并写明理由），P3 与 P14 的章末参考各加一句指向 P21 的链接（只加链接，不改其余内容）。
6. `-race` 干净；所有 `for` 循环遵守整数 range 规则。

### T3 Go 1.21 到 1.26 新特性覆盖审计

本机已核实存在而 notes 全文检索为零的：内建 `min`/`max`/`clear`（1.21）、`strings.SplitSeq`（1.24）、`testing.B.Loop`（1.24）、`os.Root`（1.24）、json 标签 `omitzero`（1.24）、`errors.AsType`（1.26，`go doc errors.AsType`）。`sync.WaitGroup.Go`（1.25）只在 P8 出现，P3 没有。

做法：对下列每个特性，先 `go doc` 或读源码确认签名与行为，再决定落点并写进对应章（P1.5 语法类、P2.5 或 P2 错误链类、P3 并发类、P8 标准库类、P20 与文件系统类），每个特性要有一段 Rust 对照和一个小例子（例子必须编译并实际运行过，必要时放进该章练习包并配测试）。`errors.AsType` 要在 P2/P2.5 的错误链处与 `errors.As` 对照并给出适用边界。产出一张"特性 → 落点章 → 例子所在文件"的表，放进 `audit/t3-features.md`，闸门不查这张表，验收者会逐行点开例子。不确定是否该写进正文的，放进该章章末参考，不要省略。

### T4 版本新鲜度与实测校正

1. GOMAXPROCS：Go 1.25 起在容器内默认按 cgroup CPU 限额取值（读 `$(go env GOROOT)/src/runtime/cgroup_linux.go` 与 `cgroup_linux_test.go` 里的 `containermaxprocs`）。检查 P7/P8 的 labs 结论（`labs/experiments/results-p8-2026-10-10.md` 的 GOMAXPROCS 消融）与 P9 的 worker pool 建议，补一句适用边界：这些数据在 macOS 上测得，Linux 容器里默认值可能不同；`DNS_WORKER_POOL=8` 的建议要写明与 GOMAXPROCS 的关系。
2. GC：`$(go env GOROOT)/src/internal/buildcfg/exp.go` 显示 Green Tea GC 在 Go 1.26 是 baseline（默认开）。把 `labs/experiments/gc-pressure` 用默认与 `GOEXPERIMENT=nogreenteagc` 各跑一遍，结果追加到 `labs/experiments/` 的新 results 文件（带日期、机器、命令），在 P7 相关笔记补一句。若 labs 包不支持这样跑，先说明为何，不得跳过。
3. encoding/json/v2：本机源码树里有该包，`GOEXPERIMENT=jsonv2` 才启用（`go doc` 默认看不到）。P19 与 P8 里涉及 JSON 的地方，加一句"实验性、默认关、何时值得关注"，并用 `GOEXPERIMENT=jsonv2 go build` 实测至少一个小程序能编译运行，结果写进台账。

### T5 站点实测

站点改动（新章、侧边栏、任何 notes 变更）后必须真浏览器验：`npx vitepress dev docs --port 5173 --host 127.0.0.1`（改过 config 要重启）。桌面与 390px 两个宽度，检查：P21 页面渲染与侧边栏位置、课程表 P21 行、新章里所有 mermaid 图渲染、文档级横向溢出为 0、至少抽 5 个旧章页面无回归。截图或 DOM 断言的结果写进 `audit/t5-site.md`（用 agent-browser skill 或等价工具）。`theme/mermaid-plugin.ts`、`MermaidDiagram.vue` 勿动。

### T6 收尾

1. 在 HANDOFF.md 末尾按既有格式追记"第三轮"：做了什么、发现并修了哪些错误（逐条，带章与证据）、遗留。不重写前文。
2. `pnpm run ci`、`go test -race -count=1 ./exercises/...`、dnsproxi-go 内 `go test ./...`、完整闸门全部通过。
3. 提交；**不推远端**。

## 3. 交付报告（格式固定，缺项视为未交付）

报告必须包含，按此顺序：

1. `python3 -I scripts/gate.py` 的**原样完整输出**（不是摘要）。
2. 每个任务一行：做了什么、对应提交号、闸门之外的验证命令。
3. 发现并修复的错误清单：章、原文、实际情况、证据命令。数量要和各台账里 FIXED 条目总数一致。
4. 偏差记录（即使为空也要写"无"）：与本文不一致的地方、原因、是否已获用户同意。
5. 未完成或不可核项：逐条列，不得省略不提。
6. 禁止的措辞：未附命令的"已验证"、"基本完成"、"其余类似"。

## 4. 阻塞报告格式

卡住时只汇报这个，不要继续做别的：卡在哪个任务的哪一步；已经试过什么（命令与输出）；已知的最小解释；你认为可选的 2 个处理办法及各自代价。等用户选择。

## 5. 验收者会做什么（让你知道会被查什么）

- 自己重跑完整闸门；
- 随机抽台账至少 5 条，用其命令列重跑，对比"结果"栏；
- 随机抽 2 条变异重跑，并自己再构造 1 条没写进去的变异看测试能否杀死；
- 通读 P21 与 T3 新增段落，核对每个 Rust 对照的事实；
- 打开 T3 表里的例子文件，看是否真被测试覆盖；
- 看提交历史是否一任务一提交、提交信息是否与实际改动相符。

## 偏差记录

无（T1-T6 全部按原范围完成，未触发范围缩减）。
