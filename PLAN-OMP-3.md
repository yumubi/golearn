# PLAN-OMP-3：从材料里提炼，按一条主线重排（执行者：OMP，验收者：Claude）

先读：`HANDOFF.md`（只追记）、`PLAN-OMP.md` 第 1 节（反简化机制，本文沿用并加码）、本文。
本文取代 `PLAN-OMP-2.md`（它让 omp 只读摘要、自己生成，方向反了）。`audit/gap-analysis.md` 只当"线索"，**不是事实来源**：它对项目课幻灯片没读过，对 Tony Bai 只精读了 19 讲。事实以原材料为准，和它冲突时以原材料为准并在 digest 里写明。

## 0. 用户意图（已和用户对齐，不要再改）

1. omp 要**完整阅读**全部材料，包括 Tony Bai 52 讲、项目课 21 周幻灯片、三个代码包、krm 项目。不是凭印象写，也不是只读我的摘要。
2. 成品是**按一条主线重排的精炼版**，少数几章，不跟着原课程的顺序走。
3. 项目线是**两个都要**：已有的 `dnsproxi-go`，加上项目课那条业务线（用户 → 帖子 → Feed 的服务）。
4. 原材料的文字**允许大段摘录，但必须标出处**。这是用户明确承担的风险（素材无许可证，疑似付费课程转载）。因此本计划**不推远端**，含摘录的章节是否公开发布，由用户在最后单独决定。
5. 要求短时间内掌握：精炼意味着**删水分、去重、按 Go 1.26 修正、串连贯**，不是把 52 讲原样搬一遍。
6. 读者仍是 Rust/Java 老手，每章保留 Rust 对照，练习仍是 `go test` 全绿。

## 1. 核心机制：来源可追溯，能机械核对

弱模型最容易的失败是"看起来读过了，其实是编的"。所以全流程围绕三个可由脚本检查的东西：

| 机制 | 内容 | 防的是什么 |
|---|---|---|
| 出处标签 | 成品里每个知识点、每段摘录，必须带标签 `〔TB24 p5〕`、`〔W12 p31〕`、`〔C:dachang/raft/raft.go:120-180〕`、`〔K:krm-backend/…〕`。无标签的断言只能是 omp 自己跑出来的实验结果，并标 `〔实测〕` 加命令 | 凭印象编 |
| 摘录核验 | 引用块里的文字，规范化空白后必须是该出处**抽取文本**的子串（允许用 `…` 省略中间）。`scripts/check-quotes.py` 机械检查 | 伪造引文 |
| 覆盖账 | 每份 digest 的每个知识点，在 S2 之后必须有去向：写进某章，或写明丢弃理由。`scripts/check-coverage.py` 检查没有悬空条目 | 读了不用、悄悄丢 |

这三个检查脚本、抽取脚本由 **Claude 在 S0 写好**，omp 不得修改（改了视为篡改验收）。

## 2. 工作区

```
downloads/            只读。不得修改，不得复制进仓库。
work/                 全部中间产物，已加入 .gitignore，不入库。
  inventory.json      每个"单元"一条：id、路径、页数、字数、状态
  text/<id>.txt       抽取的纯文本（带页标记 \f 或 [[p5]]）
  img/<id>/pNN.png    低文字页渲染图（仅当 omp 能看图时使用）
  digest/<id>.md      逐单元消化稿（S1 产物）
  topics.md           跨来源主题表（S2 产物）
  chapters.md         章节映射（S2 产物，需验收后冻结）
  status.json         进度；next-unit.py 读它
```

"单元"的定义（共 153 个，`scripts/material-extract.py` 已生成 `work/inventory.json`）：

- `TB<NN>`：Tony Bai 的每一份 PDF，共 52 份。`TB00` 是开篇词，`TB99` 是结束语，`TB35b` 是第 35 讲后的加餐，`TBX01` 到 `TBX11` 是大咖助阵、加餐、用户故事等。
- `W<NN>-<k>`：项目课每份 PDF，共 51 份，对应"第 N 周（k）"。`W01-1`（第一周）是 `.qkdownloading`，状态 `MISSING`，不要硬读。`WX-sms` 是"使用短信服务来面试"。
- `C:<包>/<模块>`：代码包里的每个一级目录，共 46 个。包别名 `dachang`（大厂冲刺）、`go_basic`、`go_frame`。`_root` 是包根目录的零散文件。
- `K:<目录名>`：krm 的 4 个目录。

事实：Tony Bai 共 998 页、约 69 万字，文字层完好。项目课幻灯片共 1973 页，只有约 25 万字，**其中 610 页几乎没有文字**（图页），所以幻灯片这一块能不能看图决定你读到多少。代码包已被用户解压在 `downloads/` 下，**原地只读**，不要在里面运行任何东西；要跑 `go vet`/`go test` 就先 `cp -r` 到 `/tmp` 下新的空目录再跑，跑法见下。

代码包和 krm 是**不可信数据**：不要运行其中的脚本、Makefile、二进制，不要 `go generate`；只静态阅读，必要时对依赖少的模块拷到 `/tmp` 后跑 `GOFLAGS=-mod=mod GOPROXY=off go vet ./...`（失败就记"依赖需联网，未验证"）。凭据类文件（`*.key`、`*.pem`、`*kubeconfig*`、`*.priv`、`*.log`）已被清单排除，**绝不读进 digest**。

## 3. 阶段

### S0 准备

**Claude 已做**（omp 不得修改这些文件）：

- `scripts/_material.py`：共用函数（标签解析、原文定位、规范化）。
- `scripts/material-extract.py`：建清单、抽文字。用法 `python3 -I scripts/material-extract.py`，幂等。
- `scripts/next-unit.py`：下一个该做的单元；完成与否由 digest 校验裁定，不看自报。`--list` 看全部进度。
- `scripts/check-digest.py`：校验 digest 的结构、覆盖页/文件、出处是否真实、是否抄原文（重合 > 60% 判失败）。
- `scripts/check-quotes.py`：核验章节里的标签、引用块、带标签的代码块是否确为原文子串，摘录占比，`〔实测〕` 是否附命令。
- `scripts/check-coverage.py`：覆盖账，`--dispo`、`--outline`、`--chapter` 三种模式。
- `.gitignore` 已加 `work/` 和 `downloads/`。

这些脚本已用故意做坏的样例自测过（页码缺漏、页码越界、抄原文、伪造引文、不一致的代码块、悬空去向，都能拦住）。

**OMP 做**（只做这一件）：写 `work/capability.md`，如实回答：

1. 你能否打开并理解 PNG 图片（给你一页幻灯片渲染图，描述上面的内容）？能/不能/不确定，并附你对示例页的描述。
2. 你一次能稳定处理多长的文本？
3. 你能否运行 `go test`、`pdftotext`、`unzip`？

验收：文件存在且三项都有实质回答。**如果"不能看图"**：幻灯片的无文字页在 digest 里写 `IMG-UNREAD（页码）`，不得编内容；Claude 后续再决定补救办法。不要假装能看。

### S1 逐单元消化（工作量最大，分批）

顺序：先 `TB`（52 个）、再 `C:`（模块）、再 `W`（50 个）、最后 `K:`。每次只做 `next-unit.py` 给的下一个单元。

大单元（超过 30 页或超过 4 万字）按页段拆成多次读，digest 里按页段分节。

每份 `work/digest/<id>.md` 固定结构，缺一节视为不合格：

```
# <id> <标题>
状态: DONE | PARTIAL(原因) | MISSING
覆盖页: 1-24            # 必须覆盖全部页；有图页/读不懂的页要单列：IMG-UNREAD: 7,12

## 一句话主题
## 知识点
- K1 <一句话要点> 〔<出处 页/行>〕 [必读|选读|低价值]
- K2 ...
## 代码例子
- E1 〔出处〕 <做什么> 可运行: 是|否|未验证  过期: 无|<说明>
## 设计取舍与易错
- ...〔出处〕
## 与 Go 1.26 冲突或过期
- ...〔出处〕 依据: <你跑过的命令或 go doc 输出>
## 可做成练习的点
- X1 预测|观察|改造: <题目构想>
## Rust/Java 对照点
- ...（只写你能确认的；拿不准写"待核"）
```

PDF 单元用 `覆盖页:`。读不了的页另起一行 `IMG-UNREAD: 7,12`（看不懂的图页）或 `UNREADABLE: 30`（乱码、截断），出现任何一行，状态必须写 `PARTIAL(原因)`。

代码和 krm 单元不用 `覆盖页`，改用：

```
覆盖文件: all            # 或者下面逐条列路径（目录前缀也行）
跳过文件:
- <路径>: <理由>         # 有跳过项，状态必须写 PARTIAL(原因)
```

文件路径用 `work/inventory.json` 里的写法（如 `dachang/raft/raft.go`）。出处标签：`〔TB24 p5〕`、`〔W12-1 p31〕`、`〔C:dachang/raft/raft.go:120-180〕`、`〔K:krm-backend-第八章/main.go:10-30〕`。

规则：

1. 知识点必须来自你读到的文本，**每条带出处**。不允许"一般来说""通常"这类无出处的补充；你自己的补充放"Rust/Java 对照点"并标 `〔自〕`。
2. "与 Go 1.26 冲突或过期"一节必须有依据：你实际跑的命令（`go version`、`go doc`、小程序输出）。凭记忆判断过期不算，写成 `待核`。
3. 代码例子标"可运行：是"的，必须真放到 `/tmp` 下 `go run` 过。标"未验证"是允许的，标"是"但没跑过是违规。
4. 不允许把整页文字抄进 digest。digest 是索引和判断，原文在 `work/text/`，成品阶段按需摘录。

验收（每个单元）：

```sh
python3 -I scripts/check-digest.py work/digest/<id>.md
```

脚本检查：结构齐全；覆盖页完整（含 `IMG-UNREAD` 声明）；每条知识点有合法出处；出处页码不越界；过期一节每条有依据或 `待核`。

**批次复核（Claude）**：每完成 10 个单元，Claude 随机抽 3 份，对照 `work/text/` 原文检查：漏读（原文有重要内容 digest 没有）、编造（digest 有而原文没有）、判断错误。任何一份不合格，**整批 10 个重做**，并追记偏差。

### S2 汇总、主线定稿（omp 起草，Claude 与用户审定后冻结）

1. `work/topics.md`：把所有 digest 的知识点按**主题**归并。每个主题一行：主题名、来源列表（多个来源讲同一件事要全列）、是否重复、是否冲突（版本或说法不同）、与现有 27 章的重叠（指出具体章节和小节，需要你真去 grep 现有 notes）。
2. `work/chapters.md`：用 `## P30 标题` 的格式声明每章，并把主题映射到章（骨架见第 4 节）。要求：
   - 每个 digest 里标 `[必读]` 的知识点都有去向；`[低价值]` 可以丢但要写理由。
   - 跨来源的重复内容，指定一个"主讲来源"，其余作印证。
   - 每章列出：覆盖的来源单元、项目锚点、练习构想（取自 digest 的 X 条目）。
   - 章数上限 **16**。证据支持时可以合并或删减，**不得新增**。
3. `work/dispo.tsv`：每个 digest 知识点一行，TAB 分隔 `<单元>#<K编号>  <P30 或 DROP>  <理由>`，例如 `TB24#K3<TAB>P30<TAB>方法表达式`。`[必读]` 不许 DROP，DROP 要有理由。
4. 验收：`python3 -I scripts/check-coverage.py --dispo` 输出 OK。然后**停下**，等 Claude 与用户审定，通过后才进入 S3。

### S3 逐章写作

按主线顺序，一章一个任务、一个提交。**第一章是试稿**：写完停下，等 Claude 与用户确认风格和篇幅，再继续后面的章。

每章必须：

1. 先写 `work/outline/pNN.md`：这章的小节、每节用哪些知识点、哪些摘录、哪些实验；知识点引用写成独立行 `K: TB24#K3, TB24#K4`。`python3 -I scripts/check-coverage.py --outline work/outline/pNN.md` 通过后才写正文。
2. 正文 `notes/pNN-<slug>.md`，沿用新章模板（`scripts/gate.py` 第 9 项）。风格按 `HANDOFF-REWRITE.md` 第 3 节：问题驱动、一句一个意思、体系名词首现一句话解释、无 AI 味词、零 em-dash。
3. **精炼标准**：一章 ≤ 450 行；同一个意思不讲两遍；原材料的啰嗦、寒暄、广告、与读者无关的入门铺垫全部丢弃。篇幅从严，超了就删。
4. 摘录规则：允许大段，但文字摘录必须写成引用块 `> 〔TB24 p5〕 “…”`，代码摘录是紧跟在独占一行的 `〔C:dachang/raft/raft.go:120-180〕` 之后的代码块；单段 ≤ 800 字，全章摘录占比默认 ≤ 30%（用户可调高，`--max-ratio`）。章头的第一个引用块是"这章学完你能干什么"，不用标签；之后的普通提示不要用引用块。摘录之外的叙述用你自己的话，不得改写成"看似原创的逐句翻译"。
5. 每个知识点带出处；过期点显式标 `〔1.26 修正〕` 并附你跑的命令与输出。
6. 每章至少一处 Rust 对照，准确性写之前核对；Java/Kotlin、TS/JS 类比按已有记忆偏好覆盖。
7. 练习：优先取 digest 的 X 条目；每章配 `go test` 全绿的练习包（`exercises/pNN…`），或落到项目模块的测试里。测试名在正文里逐个对得上。预测题的答案先跑再写。
8. 代码包里的 bug 和粗糙处（如 snowflake 序号回绕、raft 数据竞争、ioc 工厂笔误）是现成的"改造题"素材，但必须由你读到并复现，再写进章。
9. 五处入口同步（INDEX.md、README.md、docs/index.md、docs/table.md、vitepress 侧边栏）、`scripts/mirror.sh`。
10. 验收：`python3 -I scripts/check-quotes.py notes/pNN-*.md` 通过；`python3 -I scripts/check-coverage.py --chapter notes/pNN-*.md` 通过；练习包 `go vet` 加 `go test -race -count=1` 全绿；完整闸门 `python3 -I scripts/gate.py` 为 `GATE: PASS`。

### S4 两个项目

**dnsproxi-go**（项目一）：扩展范围由下表裁定，别的一律不动。每项都是新增文件，默认关闭，原有测试数量不减、原样通过。

| 扩展 | 对应章 | 约束 |
|---|---|---|
| DNS over TCP（2 字节长度前缀成帧，UDP 截断 TC 位后回退） | P34 | 新文件；加 `FuzzDecodeFrame`；不碰 UDP 路径 |
| 限流（令牌桶）与熔断（三态） | P35 | 新包 `guard/`，时钟可注入；`main.go` 只加默认关闭的接入；指标不把客户端 IP 放进 label |

**minifeed**（项目二，新建，独立 Go module，目录 `minifeed/`）：

- 目标：一个单进程的"用户 → 帖子 → 关注 → Feed"服务，随 P36 到 P41 逐章长大。
- 约束：标准库 + `net/http` 1.22 路由 + `modernc.org/sqlite`（仓库 P9 已用）。Redis、Kafka、ES、etcd、MySQL 一律用**内存实现的小接口**代替；真实中间件只在章末写成"换成真的要改哪里"，不进测试。
- 每章新增一个 package 加测试，`go test -race ./...` 全绿；每章结束 `minifeed` 都是可运行的。
- 代码是你自己写的，可以参考代码包的思路和模块，但不得原样搬入仓库；参考处在章里标出处。
- 每章 minifeed 增量 ≤ 400 行（不含测试）。超了砍功能，不要放宽。

### S5 收尾

1. `guide/ROUTE.md`：10 站左右的单一学习路线。每站：读什么（本仓库章 + 对应原材料单元）、做什么（练习命令）、项目锚点（dnsproxi 或 minifeed 的真实文件与函数）、过关标准、预计时间。配套 `guide/MATERIALS.md`：原材料每个单元"必读/选读/跳过/过期提醒"的索引，数据来自 digest。
2. INDEX.md、README.md 顶部加"新读者先看 `guide/ROUTE.md`"。
3. HANDOFF.md 末尾追记；`pnpm run ci`、全部测试、完整闸门通过。提交，**不推远端**。

## 4. 主线骨架 v0（S2 用证据修正，上限 16 章，只能合并删减，不能新增）

现有 27 章全部保留，作为"速通版"；新章是"精炼深化版"，在 INDEX 里分阶段，读者可以只走速通。

| 章 | 主题 | 主要来源 | 项目锚点 |
|---|---|---|---|
| P27 | 程序骨架：包、初始化次序、作用域与遮蔽、常量 | TB 04-08、10-11、14 | dnsproxi `main.go` 的初始化与配置 |
| P28 | 数据的真相：切片、字符串、map、结构体 | TB 12-13、15-17 | dnsproxi `wire/` 的字节处理，`cache/` 的 map |
| P29 | 函数、错误与 panic | TB 18-23、叶剑峰 | dnsproxi 各层的错误返回 |
| P30 | 方法与接口 | TB 24-30 | `cache/` 的接口，`wire/` |
| P31 | 并发与调度 | TB 31-35、`concurrence` 模块 | `Proxy.Run`、`handleQuery` |
| P32 | 内存、逃逸与 GC | TB 曹春晖 | dnsproxi 压测 `labs/experiments/gc-pressure` |
| P33 | 泛型的边界 | TB 泛型三篇 | `cache/sketch.go` 等可泛化处 |
| P34 | TCP 服务器与 DNS over TCP | TB 36-38、09 | dnsproxi 新增 TCP |
| P35 | 限流与熔断 | W14、`algorithm` 模块 | dnsproxi `guard/` |
| P36 | 用户、鉴权、分层与测试 | W2-5、go_frame `ioc` | minifeed `user/`、`auth/` |
| P37 | 缓存、计数、消息与榜单 | W6、7、9、`algorithm` | minifeed `post/`、`counter/` |
| P38 | 可观测：指标、日志、追踪 | W5(日志)、8、`trace` | 衔接 P13-P18，minifeed 接入 |
| P39 | 服务治理：拆分、注册发现、负载均衡 | W10、12、13、etcd 模块 | minifeed 拆出两个进程内服务 |
| P40 | 数据迁移与分布式事务 | W11、15、`distributed_transaction`、`raft` | minifeed 双写迁移与 Saga |
| P41 | 搜索、Feed、IM | W17-20、go_frame `im`、`radic` | minifeed `feed/`、`hub/` |
| P42 | K8s 与多集群 | W3 后半、krm、go_frame `k8s` | 衔接 P25、P26；用 client-go fake 做无集群练习 |

骨架只是起点。S2 读完全部 digest 后，如果证据显示某章素材不足（例如某周幻灯片信息量太少），就合并或删掉，并在 `work/chapters.md` 写明理由。

## 5. 反简化机制（在 PLAN-OMP.md 基础上加码）

1. **进度由脚本裁定**：`next-unit.py` 和 `status.json` 记录；不允许跳单元、不允许"批量标 DONE"。
2. **digest 不合格不算数**：`check-digest.py` 不过，不得进入下一个单元。
3. **摘录核验不过，整章退回**。
4. **读不了就如实写**：读不了的页（图、乱码、被截断）写 `IMG-UNREAD`/`UNREADABLE` 加页码，绝不编。Claude 看到这类标记会决定补救办法，这比编内容好得多。
5. **不得修改检查脚本**，不得修改已有测试以通过检查。
6. 所有"已验证"必须附命令。PLAN-OMP.md 第 1 节的禁止事项全部有效。
7. 卡住用阻塞报告格式（PLAN-OMP.md 第 4 节）。

## 6. 不做

1. 不教 Gin、GORM、Hertz、Kitex、GoFrame 的 API；它们作为"课程里用了什么"在 digest 里记，成品里只讲背后的设计，并给出标准库写法。
2. 不引入需要外部服务才能跑的练习（Kafka、RabbitMQ、Redis、MySQL、ES、etcd、真 K8s 集群）。
3. 不复制 PPT、PNG、中文笔记 md 等课件商品；不复制任何凭据文件内容；不把 `downloads/` 或 `work/` 入库。
4. 不重排现有 27 章的编号和 slug，不删现有章。
5. 不改 `vendored/`、`labs/` 现有内容，`exercises/` 只新增。
6. 不改 dnsproxi 除 S4 表里列出的两项。
7. 不为凑数而写章；证据不足就合并或删。
8. 不推远端。

## 7. 交给用户之后再定的事

1. 含大段摘录的章节是否随 GitHub Pages 公开发布。当前仓库已公开，推送即发布。Claude 会在 S3 试稿后提醒一次。
2. P42（K8s）和 P41（搜索/IM）是否真做：S2 之后，看素材和精力再定。
3. 摘录占比上限（默认 30%）是否放宽。

## 偏差记录

（OMP 追记，没有写"无"。）
