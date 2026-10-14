# PLAN-OMP-4：收官计划（P35 到 P42 与收尾）

本文件给 omp 自主执行。读它之前先读 `PLAN-OMP-3.md`（总体设计，S3 每章要求、S4 项目约束、S5 收尾仍以它为准）。本文件只补三件事：剩余工作清单、自驱循环、从 P30 到 P34 总结的硬性纪律。

**每开始一章之前，以及上下文被压缩之后，必须重新读：本文件、`PLAN-OMP-3.md` 的 S3 和 S4 两节、`work/STATUS.md`。**

## 1. 现状（以仓库为准，不以记忆为准）

- S1、S2 完成并冻结：`work/digest/`、`work/dispo.tsv`、`work/chapters.md` 不得再改。
- 已完成并提交：P30 到 P34（含 `dnsproxi-go/tcp/`）。可当样板读：`notes/p30-methods-interfaces.md`（样式）、`notes/p34-tcp-server-dns-over-tcp.md`（以项目代码为主线的写法）。
- 待做：P35 到 P42，然后 S5。P27 到 P29 暂缓，不属于本计划（主线先通到项目章，用户之后再决定）。

## 2. 自驱循环

对 P35、P36、……、P42 依次执行，不用等我指令：

1. 读 `work/chapters.md` 里本章条目和 `work/dispo.tsv` 里本章的"讲"集合。
2. 写 `work/outline/pNN.md`，`python3 -I scripts/check-coverage.py --outline work/outline/pNN.md` 通过。
3. 有项目代码的章，先写代码并提交（见第 3 节），再写正文。
4. 写 `notes/pNN-<slug>.md`、`exercises/pNN…/`，五处入口同步（INDEX.md、README.md、docs/index.md、docs/table.md、vitepress 侧边栏），`scripts/mirror.sh`。
5. 跑 `python3 -I scripts/chapter-check.py pNN --gate`，必须输出 `CHAPTER-CHECK: PASS`。失败就修，不要降低标准、不要改脚本。
6. 对照第 4 节的自查清单逐条过一遍，把结果写进 STATUS。
7. 单独提交本章文件（`git add` 具体路径；不得 add 根目录 `go.mod`、`go.sum`、`.githooks/`、`work/`、`scripts/`）。提交信息：`PNN 标题：一句话`。
8. 向 `work/STATUS.md` 追加一段：章号、提交哈希、`chapter-check` 的输出、行数、本章未验证项和偏差（诚实写，包括做不到的事）。
9. 进入下一章。

**停点**（到这里必须停下，等 Claude 与用户审，其余时候不停）：

- 停点 A：P37 完成后（minifeed 第一次可运行）。
- 停点 B：P42 完成后。
- 停点 C：S5 完成后。

**卡住时**：同一个问题重试两次仍不行，写 `work/BLOCKED.md`（现象、你试过什么、命令输出），然后停下。不要靠改检查脚本、改测试、降低要求来通过；不要编造能过检查的内容。

## 3. 各章规格

细节（"讲"条目、练习构想、旧章重叠）以 `work/chapters.md` 与 `work/dispo.tsv` 为准，下面只写额外约束。

| 章 | 项目工作 | 额外约束 |
|---|---|---|
| P35 限流、熔断与容错 | `dnsproxi-go/guard/`：令牌桶限流 + 三态熔断，时钟可注入，默认关闭；`main.go` 只加默认关闭的接入；指标不把客户端 IP 放进 label | 先提交代码（含测试，`go vet`、`go test -race -count=3` 全绿），再写章。章以 `guard/` 真实代码为主线，素材（W14 等）补背景 |
| P36 用户、鉴权与分层测试 | 新建 `minifeed/`：独立 Go module；`user/`、`auth/`；SQLite（`modernc.org/sqlite`）、`net/http` 1.22 路由 | 见 PLAN-OMP-3 S4 minifeed 约束；每章增量 ≤400 行（不含测试）；代码自己写，参考处标出处 |
| P37 缓存、计数与榜单 | `minifeed/post/`、`counter/`；Redis/Kafka 用内存实现的小接口 | 同上。做完到停点 A |
| P38 可观测 | minifeed 接入日志、指标、trace（标准库 `log/slog`、自写小 metrics，不引入新大依赖） | 与旧章 P13 到 P18 衔接，旧章已讲的写"见 notes/pXX §小节"，不重讲 |
| P39 服务治理 | minifeed 在进程内拆出两个服务，接口隔离；注册发现、负载均衡用内存实现 | 不引入 gRPC/etcd 真依赖；真实中间件写成"换成真的要改哪里" |
| P40 数据迁移与分布式事务 | minifeed 双写迁移演练 + Saga（内存模拟） | 不起外部数据库、不启停 docker |
| P41 搜索、Feed 与 IM | minifeed `feed/`（推拉）、`hub/`（内存广播） | 搜索只做倒排索引的最小内存实现；不接 ES |
| P42 K8s 与多集群 | 无新代码 | 读材料型章节：以 `K:krm-*` 与 W03-2 的代码为对象，讲 client-go 模式与多集群后端结构；不引入 client-go 依赖，练习是离线读代码题和可运行的纯 Go 小模拟。章首明说"本章不跑集群" |

P35 之后每章若发现 `work/chapters.md` 里的"讲"装不下 450 行，把多出的降为章末一行延伸，不要超行数，也不要改 `dispo.tsv`；在 STATUS 里记录哪些降了。

## 4. 硬性纪律（P30 到 P34 实际出过的错）

自查清单，提交前逐条过：

1. **项目锚点必须真读过**。dnsproxi-go、minifeed 里的文件、函数、行为，写之前自己 `grep` 和 `sed -n` 读；行号易漂移，正文优先用函数名定位，必须用行号时提交前重新 `grep -n` 核对。找不到锚点就写"这里没有现成例子"，不得编造（P30 曾编出不存在的 Getter/Setter）。
2. **旧章交叉引用只写真实小节**：`pNN §小节名` 必须是该章的真实标题（`check-quotes` 会检查），没读过就写"旧章无"。不得引用尚未写出的章（P28 等）带 `§`。
3. **只写你验证过的机制**。`〔实测〕` 必须是你本次真的跑的命令和原样输出，且命令不能只是 `go version` 或 `go env`。解释现象时，没验证的原因写"待核"，不要写成结论（P30 的 "sync 缓存复用"、P34 的 "io.ReadFull 中间态" 都是编的）。需要证明分配、逃逸、性能时用 `-gcflags=-m`、`-benchmem`、`pprof` 的真实输出。
4. **Go 版本事实以本机 go1.26.4 为准**，写之前 grep 本机源码或跑命令（Green Tea GC 在 1.26 默认开启，`grep -n GreenTeaGC $(go env GOROOT)/src/internal/buildcfg/exp.go`）。digest 里的"过期判断"是待核线索，不是事实。
5. **引文**：代码用代码块并指向源文件标签；不要引 PDF 抽取出的无空白乱码；正文不得出现 `downloads/` 路径；摘录占比 ≤30%。
6. **Rust/Java 对照**拿不准就不写；写了的要准确（Rust 的 `self`/`&self` 在调用时有区别，不要写"没有区别"）。
7. **读者视角**：不得泄露内部编号（K17、dispo、§N 这类章内编号）；预测题答案不写在题面里（放练习包测试注释）；不写"最早版本有 bug"这类只有你知道的历史。
8. **精炼**：一章 ≤450 行；一个意思不讲两遍；素材里与本章无关的内容不要为了"用上素材"硬塞（P34 曾塞进 TB09 的 HTTP 段）。
9. **项目章以真实代码为主线**，素材补背景，不按课件顺序复述。
10. **取舍要明说**：丢帧还是排队、简化实现的限制，都写在正文里，不让读者误以为完备。
11. **练习包**：预测、观察、改造各至少 2 题；正文提到的测试名必须真实存在（`chapter-check` 会查）；测试名与正文一一对应。

## 5. 禁止事项（副作用）

- 不得修改 `scripts/`、`PLAN-OMP-3.md`、`PLAN-OMP-4.md`、`work/chapters.md`、`work/dispo.tsv`、`work/digest/`。
- 根目录与 `dnsproxi-go/` 下不得运行 `go mod tidy`、`go get`；不得改根 `go.mod`、`go.sum`。`minifeed/` 是独立 module，可在其目录内运行 `go mod tidy`，且只能引入 `modernc.org/sqlite`（及其间接依赖）。
- 不得启停 docker 容器、不得 `git checkout` 非本章文件、不得 `git push`。
- 变异测试（gate 的一部分）可能在 `exercises/` 留下残留 diff：先 `git status` 看清，只还原确实是变异残留的 `exercises/` 文件。
- 不得为了通过检查而改检查：改脚本、改测试断言、删测试、降低 `--max-ratio` 之外的阈值，一律不允许。

## 6. S5 收尾（停点 B 通过后做）

按 `PLAN-OMP-3.md` S5：`guide/ROUTE.md`（单一学习路线，约 10 站）、`guide/MATERIALS.md`（原材料每个单元的必读/选读/跳过/过期提醒索引，数据来自 digest；把各章里写的"见 MATERIALS"落实）、INDEX.md 与 README.md 顶部加入口、HANDOFF.md 末尾追记。`pnpm run ci`、所有测试、完整 gate 通过。提交，不推远端。

## 7. STATUS.md 格式

```
## P35 限流、熔断与容错
- 提交: <哈希>（代码）, <哈希>（章节）
- 行数: notes N / exercises M
- chapter-check: <原样粘贴最后一行与 FAIL 项>
- 偏差与未验证: <逐条>
- 降为延伸的"讲"条目: <条目列表>
```

STATUS 写给审稿人读：诚实比好看重要。
