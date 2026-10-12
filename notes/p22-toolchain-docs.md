# 第 22 章 · 工具链深潜与文档工程：从"能编译"到"能交付"

> P1 讲了 cargo 对应什么（go mod init/build/test 一圈速通）。这章讲
> P1 留下的深水区：性能取证（pprof/trace 的完整流程）、lint 与
> generate、GODEBUG 与 GOEXPERIMENT 两个开关体系、多模块工作区，以及
> Go 文化里最被低估的一块：文档工程（doc comment、Example 测试、
> pkg.go.dev、版本发布）。Rust 侧对照 cargo flamegraph、clippy、
> rustdoc、crates.io 发布。
>
> 这章学完你能干什么：给一个 Go 服务采 CPU/heap/goroutine profile 并
> 读出瓶颈；给一个包写 godoc 级注释并用工具验证覆盖率；发布一个
> v1.2.3 的 module 让别人 `go get`。

## 问题 1：pprof 的完整取证流程长什么样？

P1 只说了"一行 import 即得端点"。完整流程四步：

```go
import _ "net/http/pprof"   // 把 /debug/pprof 挂到 DefaultServeMux
go http.ListenAndServe("localhost:6060", nil)  // 单独端口！
```

```sh
# 1. 采：CPU 采 30 秒（采样期间在打负载）
go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30
# 2. 瞬时类不需要 seconds：heap/goroutine/block/mutex 都是快照
go tool pprof http://localhost:6060/debug/pprof/heap
# 3. 读：终端 top、web 火焰图、文本调用树
(pprof) top5
(pprof) web          # 需要 graphviz
go tool pprof -http=:8080 cpu.pb.gz   # 本地交互界面（自带火焰图 tab）
# 4. 对比：改完再采一份，-base 差分定位"这次改动省了什么"
go tool pprof -base=before.pb.gz after.pb.gz
```

五种 profile 的分工（实测本仓 httpkv 服务，Apple M4）：

| profile | 回答的问题 | 采集方式 | 本仓实测 |
|---|---|---|---|
| CPU | 时间花在哪个函数 | `profile?seconds=N` 采样 | 端点实采成功 |
| heap | 现在驻留内存谁占的 | 快照 | 2680.94kB 总驻留 |
| allocs | 累计分配谁贡献的（GC 压力） | 快照（含已释放） | 5329.16kB 累计 |
| goroutine | 多少 goroutine、卡在哪 | 快照 | 采到 `Saved profile` |
| block/mutex | 锁等待/阻塞时长 | 需 `runtime.SetBlockProfileRate` | 低频场景才开 |

取证的纪律（P8 的"先估占比再跑端到端"在 profile 上的版本）：先
allocs（分配大户 = GC 税源头，P6 已实证 mark assist 8.1 万次/3s），
再 CPU，最后才 trace。heap 看驻留、allocs 看流量，搞反了会盯着
"驻留小但分配疯狂"的对象错过优化点。

**观察验证**：`go run ./exercises/p8fast/httpkv/`，然后上面四步
命令逐个跑。`top` 输出的 flat/cum 两列：flat 是函数自身耗时，
cum 是含被调用的总耗时。优化 flat 高的 leaf 函数、cum 高的调度
路径（比如每请求一个 goroutine 的 spawn 税，P8 E5 实测过）。

Rust 对照：`cargo flamegraph`（perf 采样）≈ CPU profile；
`tokio-console` ≈ goroutine profile（看任务卡在哪）；Rust 没有
内置的 heap profile（要 valgrind/自定义 allocator），Go 把五种全
塞进了运行时，这是"服务内置可观测"的Go 立场。

## 问题 2：lint 和 generate 怎么用到生产级？

`go vet` 是编译器附带的静态检查（printf 格式串、锁拷贝、
unreachable 等），CI 必开。它比 clippy 弱多少？弱在"项目级规则"，
补位工具 golangci-lint（50+ 检查器聚合，配置文件一条命令）：

```yaml
# .golangci.yml（本仓未启用，给了能直接用的最小配置）
linters:
  enable:
    - errcheck # 未检查的 error（vet 不查所有返回值）
    - copyloops # for range 拷贝大结构（P3 的坑）
    - gofumpt # gofmt 的严格版（脚本化格式纪律）
```

```sh
golangci-lint run ./...   # 本仓 CI 用 go vet + gofmt -l 兜底
```

`go:generate` 是"代码生成器的统一入口"（不是构建步骤！）：

```go
//go:generate stringer -type=Level
type Level int
```

```sh
go generate ./...   # 人手动跑，生成结果提交进 git
```

三条纪律（对应 Rust 的 build.rs 差异）：

1. 生成物**提交进 git**（Rust 的 OUT_DIR 产物不提交，Go 相反，
   因为 go build 不跑 generate，消费者不该被要求装工具链外的
   东西）。
2. 生成文件头写 `// Code generated ... DO NOT EDIT.`（工具自动加，
   其他工具链靠它识别跳过）。
3. `go:generate` 指挥的是"源码到源码"，运行时构建钩子用别的
   （`make` 或脚本），别把下载依赖塞进 generate。

## 问题 3：GODEBUG 和 GOEXPERIMENT 两套开关怎么用？

Go 的行为微调有两层，管的事不同：

| 体系 | 管什么 | 粒度 | 例子 |
|---|---|---|---|
| GODEBUG | 运行时行为的兼容开关 | 每个开关一个行为 | `gctrace=1` 打印每轮 GC；`asyncpreemptoff=1` 关异步抢占 |
| GOEXPERIMENT | 编译器/runtime 的实验特性 | 整个工具链重编译 | `jsonv2`、`nogreenteagc`（T4 实测过） |

GODEBUG 的关键机制：**默认值跟 go.mod 里的 go 版本走**。module
声明 `go 1.22`，`asynctimerchan` 等行为就按 1.22 的语义跑，就算你
用 1.26 的工具链编译。这是 Go 的兼容性承诺：升级工具链不改行为，
想用新语义就显式改 go.mod 或 GODEBUG 环境变量。已知开关的全表在
`$(go env GOROOT)/src/internal/godebugs/table.go`（26+ 项，T4 核
实的 `containermaxprocs` 也在里面）。

GOEXPERIMENT 是"未定型的未来"：默认关的实验（jsonv2，P19 实测
编译都过不去）、默认开可退回的 baseline（greenteagc，T4 双跑实测
`GOEXPERIMENT=nogreenteagc` 可关）。生产二进制别带非 baseline 的
EXPERIMENT；排查"升级后行为差异"先查 GODEBUG 默认值表。

Rust 对照：GODEBUG ≈ `RUSTFLAGS` + cfg 开关的杂交，但没有"跟
Cargo.toml 的 rust-version 联动"这个机制（Rust 用 edition 分层，
Go 用 go.mod 版本分层，动机相同）。

## 问题 4：多模块工作区（go.work）解决什么？

monorepo 里两个 module 互相引用时，没工作区就得推 tag 才能
`go get` 自己人的改动：

```sh
go work init ./exercises ./dnsproxi-go   # 生成 go.work
# 之后 ./exercises 里 import dnsproxi-go 直接用本地代码
go work use -add ./labs   # 增补
```

go.work 是**本地开发文件，不提交**（.gitignore 它；CI 不认它，
CI 靠 module 的 require/replace）。`replace` 指令也能解，但改的是
go.mod（会污染其他人的构建），go.work 不碰 go.mod。Rust 的
`[patch.crates-io]` ≈ replace，cargo workspace ≈ go.work，但
cargo workspace 要求共享一个 Cargo.lock，go.work 各 module 独立
go.sum（更松，代价是重复下载）。

本仓单 module（golearn），用不上工作区；拆成多 module 的时机是
"某个子目录想有独立版本号"，比如 dnsproxi-go 单独发版。

## 问题 5：文档工程，Go 的"注释即文档"到底有多认真？

Go 是唯一一个把文档当成**编译产物同级**的主流语言：pkg.go.dev 上
每个包的页面，就是你源码注释的渲染结果，没有手写站点。三层设施：

### （1）doc comment 语法（1.19+ 官方规范）

```go
// Package docstats 解析 Go 包的文档覆盖率。
//
// 导出类型/函数/方法/常量里，带 doc 注释的比例。
// 用法：
//
//	r, err := docstats.Dir("./some/pkg")
//	fmt.Println(r.Ratio())
//
// 详情见 [Result] 与 [Dir]。链接语法 [Symbol] 会解析成包内引用。
package docstats
```

规则：注释以标识符名开头（`Package xxx`、`func Dir ...`）；空行
分段；缩进一格以上是代码块；`[Symbol]` 是 1.19+ 的 doc link；
列表用 `# ` 或 `- ` 开头。渲染器是标准库 `go/doc/comment`（简化
版 Markdown，不支持嵌套列表）。

### （2）Example 测试：文档里的代码是跑过的

```go
func ExampleDir() {
    r, _ := docstats.Dir("./exercises/p1tool")
    fmt.Println(r.Documented() > 0)
    // Output: true
}
```

`ExampleXxx` 放在 `_test.go` 里，`go test` 会执行并比对 `// Output:`
注释：**文档里的代码从此不可能悄悄烂掉**。这是 rustdoc 的 doctest
的 Go 对应物，两者立场相同：文档代码是测试用例。pkg.go.dev 会把
Example 渲染到对应符号的文档页尾部。

### （3）可编程访问：文档也是数据

`go/parser` + `go/doc` 能把包解析成结构化文档对象（`doc.Package.Doc`
、`Func.Doc`），本仓练习 docstats 就是干这个的：**用 AST 统计导出
符号的文档覆盖率，100% 覆盖的包解析出 100%**。

```sh
go run ./exercises/p22tool/cmd/docstats ./exercises/p1tool
# pkg: 5/6 documented (83%), missing: NotFoundError.Error   ← 退出码 1
go run ./exercises/p22tool/cmd/docstats ./exercises/p22tool
# docstats: 8/8 documented (100%), missing:                 ← 退出码 0
```

**改造练习**：给 docstats 加 `-min 0.9` 参数（低于 90% 覆盖率才退
出码 1），接进你的项目 CI。再给 `exercises/p1tool` 补上缺的
`NotFoundError.Error` 的 doc（练习里读错误，P2 的错误链章）。

### 发布纪律：版本号与 v2 路径

```sh
git tag v1.2.3 && git push origin v1.2.3
# v2+ 必须改 module path（Go 的硬规则）：
#   module example.com/foo/v2
# 消费者：go get example.com/foo/v2@v2.0.0
```

Go module 的版本规则三条：语义化版本强制（`golang.org/x/mod/semver`
可编程比较）；主版本 ≥2 必须写进 module path（同仓库共存 v1/v2，
import 路径区分）；`go get pkg@none` 降级/移除依赖。Rust 侧
`cargo publish` 是中心化注册表，Go 是去中心化（module path 即地
址，proxy.sum 兜底完整性，P19 讲过 GOPROXY 机制）。

## 动手：练习 p22tool（30-45 分钟）

```sh
cd /Users/akka/Dev/learning/go
go test -race -count=1 ./exercises/p22tool/     # 4 测试全绿
go run ./exercises/p22tool/cmd/docstats ./exercises/p22tool   # 100% → 退出码 0
go run ./exercises/p22tool/cmd/docstats ./exercises/p1tool    # 83% → 退出码 1
go run ./exercises/p22tool/cmd/docstats -d ./exercises/p19eco # -d 打印 doc 首行
go build -ldflags "-X golearn/exercises/p22tool.Version=v0.1.0" \
  -o /tmp/docstats ./exercises/p22tool/cmd/docstats/
/tmp/docstats -version    # v0.1.0：ldflags 注入的版本号
```

预测题（跑之前写答案；预测的意义在 P5 讲过：猜错的地方才是认知
边界）。四个测试的看点：`TestDirRealPackage`（包自己统计自己是
100%）、`TestDirUndocumented`（临时包 3 缺 1 覆盖，断言
Summary 精确串）、`TestDirSyntaxError`（语法错误必须浮出，不静默
跳文件）、`TestEmptyPackage`（无导出符号的包覆盖率按 1 算）：
1. `docstats` 对 `exercises/p21test` 统计，覆盖率是多少？缺失的
   符号是哪几个（提示：`t` 参数类型算导出吗）？
2. 把 `Result` 的 doc 注释删掉，`-d` 输出会怎么变？退出码呢？
3. 方法 `Result.Exporteds` 在统计里的键名是什么？为什么不是裸
   `Exporteds`（godoc 站点上方法挂在类型名下）？
4. `go.work` 文件该不该提交？`replace` 呢？为什么一个提交一个
   不提交？

**观察验证**：上面命令块的每条输出都实拍过（本节引用的就是实跑
结果，退出码 0/1/2 与 P20 惯例一致）。

## Take-away

- pprof 五种 profile 分工：CPU 采样、heap 驻留、allocs 流量、
  goroutine 快照、block/mutex 低频。取证的顺序是 allocs → CPU →
  trace，`-base` 差分是优化验证的标准动作。
- lint：go vet 必开，golangci-lint 补项目级规则；generate 是源码
  到源码的统一入口，产物提交 git，与 Rust 的 build.rs 立场相反。
- GODEBUG 默认值跟 go.mod 的 go 版本走（升级工具链不改行为）；
  GOEXPERIMENT 是未定型的未来，生产别带非 baseline 实验。
- go.work 本地开发不提交，replace 会污染 go.mod；拆多 module 的
  时机是"要独立版本号"。
- 文档工程三层：doc comment 规范（`[Symbol]` 链接）、Example
  测试（文档代码是测试用例，同 rustdoc doctest）、go/doc 可编程
  访问（docstats 的原理）。发布三条：semver 强制、v2 进 module
  path、tag 即发布。

## 章末参考

### 速查：本章命令

| 命令 | 用途 |
|---|---|
| `go tool pprof -http=:8080 <profile>` | 交互界面（火焰图/调用树） |
| `go tool pprof -base=a.pb.gz b.pb.gz` | 两次采样的差分 |
| `go tool pprof -top <endpoint>` | 终端 top 表 |
| `go vet ./...` / `golangci-lint run` | 静态检查（CI 必配） |
| `go generate ./...` | 跑 `//go:generate` 指令 |
| `go work init/use` | 多模块工作区（go.work 不提交） |
| `go get pkg@v2.0.0` / `@none` | 依赖升级/移除 |
| `gofmt -l .` | 格式漂移检查（CI 兜底） |

### GODEBUG 常用五项（本仓正文用过的加粗）

| 开关 | 包 | 作用 |
|---|---|---|
| **gctrace=1** | runtime | 每轮 GC 打一行摘要（P6/P8 实测用过） |
| **containermaxprocs** | runtime | 1.25+ 容器内按 cgroup 限额取 GOMAXPROCS（T4 核实） |
| asynctimerchan | time | 1.23 改的计时器/channel 语义回退开关 |
| gotypesalias | go/types | 泛型别名语义（1.23 起） |
| http2client | net/http | HTTP/2 客户端开关 |

### Rust 对照总表

| 主题 | Go | Rust |
|---|---|---|
| CPU 火焰图 | `go tool pprof`（内置） | `cargo flamegraph`（perf 依赖） |
| 任务/goroutine 观测 | goroutine profile | `tokio-console` |
| heap profile | 内置（heap/allocs） | 无内置（valgrind/dhat） |
| lint | vet + golangci-lint | clippy（能力对齐 clippy 更强） |
| 代码生成 | `go:generate`（产物提交） | build.rs（产物不提交） |
| 文档 | doc comment + pkg.go.dev | rustdoc + docs.rs |
| 文档代码测试 | Example（`// Output:`） | doctest |
| 发布 | tag + module path 即地址 | `cargo publish`（中心化） |
| 行为开关 | GODEBUG（跟 go.mod 版本走） | edition + RUSTFLAGS |

### 深读路径

- doc comment 官方规范：go.dev/doc/comment（1.19 起的语法全表）
- pprof 使用文档：`go doc runtime/pprof` 的 Profile 类型注释
- module 发布流程：go.dev/doc/modules/publishing
- GODEBUG 全表：`$(go env GOROOT)/src/internal/godebugs/table.go`
