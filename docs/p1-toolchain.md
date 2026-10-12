---
title: P1 · 工具链速通：cargo → go
---

# P1 · 工具链速通：cargo → go


> 读者：会 cargo，第一次碰 go 工具链。目标：把肌肉记忆一一映射到
> go 命令，10 分钟读完，练习 15 分钟。这章是速查表：类比总表在开头，
> 它本身就是章末参考，记住"import 驱动"这一条差异就够开工。
> 下一步：P1.5 语法速通 → P2 换语言直觉；P14 学并发组装。

## 这章学完你能干什么

不看文档敲出完整一圈：`go mod init` 起项目、`go get`/`go mod tidy`
管依赖、`go test -v ./...` 跑测试、`gofmt` 收尾，并解释为什么
go.sum 存哈希、internal/ 是编译器强制的。练习终点：三道题全绿
（`error_test.go` 的 `%w` 包装链、`external_test.go` 的跨包可见性、
`notfound.go` 的第一次隐式实现）。

三个问题：

1. cargo 的命令和文件，go 里各对应什么？
2. 为什么说 go.mod 是"源码导入的投影"，不是清单？
3. 可见性规则比 Rust 粗在哪，换来什么？

## 问题 1：cargo 的命令和文件，go 里各对应什么？

| 你会的 | Go 对应 | 差异要点 |
|---|---|---|
| `cargo new` | `go mod init <module>` | go 不建目录，先 `mkdir` 再 init |
| `Cargo.toml` | `go.mod` | 只有 module 名 + go 版本 + require 列表 |
| `Cargo.lock` | `go.sum` | 存的是**内容哈希**，不是版本号；必须提交 |
| `cargo add serde` | `go get github.com/...` | **先写 import 再 `go mod tidy`** 是更地道的流 |
| `cargo build` | `go build ./...` | `./...` = workspace 全部包，天天用 |
| `cargo run` | `go run ./cmd/xxx` | go run 只能跑 main 包 |
| `cargo test` | `go test ./...` | 测试文件 `_test.go`，和源码**同目录同包** |
| `cargo test -- --nocapture` | `go test -v ./...` | `-v` 兼打印+进度 |
| `cargo clippy` | `go vet ./...` | 更弱；生态靠 `golangci-lint` 补 |
| `cargo fmt` | `gofmt -w .` / `go fmt ./...` | **没有配置项**。全社区一种格式，没有 prettier 之争 |
| `cargo doc --open` | `go doc -http=:6060` | 或直接看 pkg.go.dev |
| `[dev-dependencies]` | 无 | 测试和源码同包，依赖不分 dev/prod |
| edition 2021/2024 | go 1.x in go.mod | 每 6 个月一个版本，兼容性极好（Go1 兼容承诺） |
| feature flags | 无 | 要么编译进去要么不编；用 build tags 手动切 |
| workspace（cargo） | go.work（多 module） | 单 module 内多目录不需要 workspace |
| crates.io | proxy.golang.org | 默认走 GOPROXY，国内配 goproxy.cn |

## 问题 2：为什么说 go.mod 是"源码导入的投影"，不是清单？

**最大心智差异**：cargo 按版本来 require，go 按导入路径来 require。
`go.mod` 里没有的包，只要 `import` 了，`go mod tidy` 会自动加。
反过来，没被 import 的依赖会被 tidy 删掉，go.mod 是"源码导入的
投影"，不是清单。

## 问题 3：可见性规则比 Rust 粗在哪，换来什么？

Rust 一个 crate 里可以随便 mod；Go **一个目录就是一个包**，包名 =
目录名（`_test` 结尾的目录除外）。同一目录下的文件必须同一个包名。

```
deepsearch/               ← module root (go.mod)
├── main.go               ← package main
├── cmd/                  ← cobra 子命令
├── internal/             ← 只许本 module 导入（编译器强制）
├── pkg/                  ← 可以被外人导入
└── go.mod
```

- **`internal/` 是编译器强制可见性**：别人 `import deepsearch/internal/...`
  直接编译错。Rust 的 `pub(crate)` 只有编译器 lint，Go 的 internal 是
  硬约束。
- **大写 = 导出**。`func Open()` 是 pub，`func open()` 是私有。
  没有 `pub(crate)`/`pub(super)` 的中间档，包级私有就到底了。

### 编译模型：为什么 go build 这么快

Java 心智：javac → jar → JVM。Rust 心智：crate 图 + 单体产物。

Go：**编译 = 按包 DAG 并行编译，依赖只有一层 import 扫描**（没有宏、
没有 build.rs、没有 trait solver），产物是**静态链接**的原生二进制。
`go build` 完扔到另一台机器直接跑，不需要 runtime 安装。

这是 Go 工具链设计的因果：没有宏 → 增量编译便宜；静态链接 → 部署 =
scp 一个文件。你后面精读 ejdb/deepsearch 时会发现它们的 Dockerfile
都只有 `FROM scratch` + 一个二进制。

## 动手：练习 p1tool（15-30 分钟）

```bash
go test ./exercises/p1tool/...          # 跑一遍，看哪些红
go test -run TestNotFound -v ./exercises/p1tool/   # 单跑某题
```

打开 `exercises/p1tool/` 按注释修到绿，三道题对应三个考点：

1. `error_test.go`：`errors.Is` + `%w` 包装链（P2 问题 3 的预演）。
2. `external_test.go`：跨包可见性，大写导出 vs 小写私有。
3. `notfound.go`：实现 `NotFound()`，interface 隐式实现的第一次手感。

**验收**：`go vet ./exercises/p1tool/ && go test ./exercises/p1tool/`
全绿，且 `go mod tidy` 后 go.sum 无变化（依赖卫生）。

## Take-away

- `go mod tidy` 是依赖的唯一真相：import 驱动，不是清单驱动。
- internal/ 硬私有、大写导出，可见性模型比 Rust 粗但零配置。
- 静态链接单二进制是 Go 部署的默认答案，Dockerfile 见 deepsearch。

## 章末参考

### 工具链进阶：这半年会真用到的命令

**构建与缓存**：

```sh
go build -trimpath -ldflags="-s -w" -o bin/app ./cmd/app
#   -trimpath   去掉本机路径（可复现构建）
#   -s -w       去符号表/调试信息（≈ strip，产物小 20-30%）
#   -o          输出路径
go clean -cache        # 清编译缓存（构建诡异报错时的第一反应）
go env GOMODCACHE      # 模块缓存在哪（只读；要改代码得 replace 本地路径）
```

交叉编译是**零配置**的（cargo 要装 target，Go 全内置）：

```sh
GOOS=linux GOARCH=arm64 go build -o app ./cmd/app
GOOS=windows GOARCH=amd64 go build -o app.exe ./cmd/app
CGO_ENABLED=0 go build ...   # 纯 Go 静态编译（scratch 镜像的标配）
```

**依赖管理进阶**：

```sh
go get pkg@v1.2.3      # 定版本；@latest / @upgrade / @none
go get pkg@master      # 跟分支（临时试 patch）
go mod edit -replace=old=./local/new   # 本地路径替换（调试依赖必会）
go mod why pkg         # 这个依赖为什么在依赖树里
go mod graph | head    # 依赖图
go list -m -u all      # 哪些依赖有新版
```

对照 cargo：`-replace` ≈ `[patch]`，`go mod why` ≈ `cargo tree -i`。
主版本 2+ 的导入路径带 `/v2`（P2.5 模块纪律节）。

**测试工具族**（P8 问题 3 展开 table-driven，这里是命令面）：

```sh
go test -run TestXxx/子测试名 ./pkg/    # 精确到子测试
go test -count=1 ./...                  # 禁用测试缓存（CI 必加）
go test -timeout 30s ./...              # 默认 10m，防死锁卡 CI
go test -bench=. -benchmem ./pkg/       # 基准测试（P7 方法论）
go test -coverprofile=c.out ./pkg/ && go tool cover -html=c.out
go test -race ./...                     # 数据竞争检测，CI 必开（P3）
```

**诊断三剑客**（Rust 没有的 gift，直接在二进制里）：

```sh
go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30
#   CPU 火焰图：net/http/pprof 一行 import 即得端点
go tool trace trace.out  # 调度延迟/GC/系统调用时间线
go vet ./...             # 静态检查（printf 格式串、lock 拷贝等）
```

P7/P13 的实验全用这套：pprof 看热点，trace 看调度，pprof heap 看
分配。**生产环境 pprof 端点是 Go 服务的标配**（默认不开，net/http/pprof
 路由挂上即可）。

**golangci-lint**（go vet 太弱的补位）：

```sh
golangci-lint run        # 聚合 20+ linter：errcheck（漏查 error）、
                         # govet、staticcheck、revive……
```

对照 clippy 的 `-W clippy::all`：golangci-lint 是聚合器，配置文件
`.golangci.yml` 挑启用集。社区 CI 常开：errcheck、govet、staticcheck、
ineffassign、unused 五件套起步。

**go generate**（代码生成的口子，不是宏）：

```sh
//go:generate stringer -type=Level     # 写在 .go 文件注释里
go generate ./...                       # 显式触发，产物进 git
```

对照 Rust 的宏/derive：Go 没有编译期元编程，**约定是生成代码提交
进仓库**（stringer、mockgen、sqlc 都是这条路，P11 全靠 sqlc）。
