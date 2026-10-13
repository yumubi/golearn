---
title: P1 · 工具链速通：cargo → go
---

# P1 · 工具链速通：cargo → go


> 这章学完你能干什么：不看文档敲出完整一圈，`go mod init` 起项目、
> `go get`/`go mod tidy` 管依赖、`go test -v ./...` 跑测试、`gofmt`
> 收尾，并解释 go.sum 为什么存哈希、internal/ 是谁强制的。
> 读者：会 cargo，第一次碰 go 工具链。前置：无。下一步：P1.5 语法
> 速通；P14 学并发组装。
>
> 动手方式：开空目录跟着敲，每个命令都真跑一遍。

## 体系背景：编译模型决定一切

go 工具链的所有"怪"，都从一个事实推出来：**编译是按包 DAG 并行
编的，产物是静态链接的原生二进制**。

没有宏、没有 build.rs、没有 trait solver，依赖解析就是一层 import
扫描。所以 `go build` 快得像没在编译；产物扔到另一台机器直接跑，
不需要装任何 runtime。部署 = `scp` 一个文件。deepsearch 的
Dockerfile 就是 `CGO_ENABLED=0 go build` + `FROM alpine` 塞一个
二进制（P5 精读会看到原文），scratch 镜像是这套打法的极简形态。

这一条后面还会反复兑现：可见性模型粗（零配置）、没有 feature
flags（要么编进去要么不编）、交叉编译零配置。

## 命令映射：你会的 cargo 对应什么

| 你会的 | Go 对应 | 差异要点 |
|---|---|---|
| `cargo new` | `go mod init <module>` | go 不建目录，先 `mkdir` 再 init |
| `Cargo.toml` | `go.mod` | 只有 module 名 + go 版本 + require 列表 |
| `Cargo.lock` | `go.sum` | 存的是**内容哈希**，不是版本号；必须提交 |
| `cargo add serde` | `go get github.com/...` | **先写 import 再 `go mod tidy`** 是更地道的流 |
| `cargo build` | `go build ./...` | `./...` = 全部包，天天用 |
| `cargo run` | `go run ./cmd/xxx` | go run 只能跑 main 包 |
| `cargo test` | `go test ./...` | 测试文件 `_test.go`，和源码**同目录同包** |
| `cargo test -- --nocapture` | `go test -v ./...` | `-v` 兼打印+进度 |
| `cargo clippy` | `go vet ./...` | 更弱；生态靠 `golangci-lint` 补 |
| `cargo fmt` | `gofmt -w .` | **没有配置项**，全社区一种格式 |
| `cargo doc --open` | `go doc -http=:6060` | 或直接看 pkg.go.dev |
| `[dev-dependencies]` | 无 | 测试和源码同包，依赖不分 dev/prod |
| feature flags | 无 | 用 build tags 手动切 |
| workspace（cargo） | go.work（多 module） | 单 module 多目录不需要 workspace |
| crates.io | proxy.golang.org | 国内配 goproxy.cn |

## go.mod 是"导入的投影"，不是清单

最大心智差异：cargo 按版本来 require，go 按**导入路径**来 require。
`go.mod` 里没有的包，只要 `import` 了，`go mod tidy` 自动加；没被
import 的依赖会被 tidy 删掉。

```sh
$ go mod init demo && go mod tidy
$ head go.mod
module demo

go 1.26

require golang.org/x/sync v0.13.0   // ← import 了 errgroup 才出现的行
```

所以 go.mod 不用手维护，它是源码的投影。`go.sum` 才是锁文件，
存每个模块的内容哈希，保证任何人拉到的字节都一样。

## 可见性：一个目录一个包

Rust 一个 crate 里随便 mod；Go **一个目录就是一个包**，包名 = 目录名。

```
deepsearch/               ← module root (go.mod)
├── main.go               ← package main
├── cmd/                  ← cobra 子命令
├── internal/             ← 只许本 module 导入（编译器强制）
├── pkg/                  ← 可以被外人导入
└── go.mod
```

`internal/` 是**编译器强制**的私有：别人 import 它直接编译错。
Rust 的 `pub(crate)` 只是 lint，Go 的 internal 是硬约束。
大写 = 导出（`func Open()` 是 pub），没有中间档，包级私有到底。

这个规则粗，但换来零配置：看目录名就知道包边界，没有任何
"mod.rs 放哪"的争论。

## 动手：练习 p1tool（15-30 分钟）

```bash
go test ./exercises/p1tool/... -v       # 4 个测试，参考实现已全绿
go test -run TestErrorWrapping -v ./exercises/p1tool/   # 单跑某题
```

4 个测试两个文件，对应三个考点：

1. `error_test.go`：`TestErrorWrapping` 验证 `divide` 的错误用
   `errors.Is` 能认出 `ErrDivZero`（`%w` 包装链，P2 的预演）；
   `TestErrorAs` 用 `errors.As` 从 `lookup` 的错误里取回
   `*NotFoundError`（≈ Rust downcast）。
2. `external_test.go`：`TestVisibility` / `TestPrivateCtor` 以外部包
   身份用 `pkg.go` 的 `Counter`：只能走导出的 `Add`/`N`/`MustNew`，
   私有字段 `c.n` 和 `newCounter` 编译器直接拦。

**预测题**：把 `TestVisibility` 里 `c.N()` 改成 `c.n`，`go test`
报"测试失败"还是"编译失败"？跑之前写答案，再跑：

```sh
$ go test ./exercises/p1tool/
# exercises/p1tool/external_test.go:12:9: c.n undefined
#       (type *pkg.Counter has no field or method n)
FAIL    build failed    # ← 测试根本没跑起来：可见性是编译器规则
```

**验收**：`go vet ./exercises/p1tool/ && go test ./exercises/p1tool/`
全绿，`go mod tidy` 后 go.sum 无变化。

## 小结

- `go mod tidy` 是依赖的唯一真相：import 驱动，不是清单驱动。
- internal/ 硬私有、大写导出，可见性模型比 Rust 粗但零配置。
- 静态链接单二进制是部署默认答案：编译模型没有宏和钩子，
  `CGO_ENABLED=0` 产物直接扔 alpine/scratch。

## 章末参考

### 工具链进阶（深潜版在 P22）

```sh
go build -trimpath -ldflags="-s -w" -o bin/app ./cmd/app
#   -trimpath 去本机路径（可复现）；-s -w ≈ strip，小 20-30%
go clean -cache        # 构建诡异报错的第一反应
GOOS=linux GOARCH=arm64 go build -o app ./cmd/app   # 交叉编译零配置
CGO_ENABLED=0 go build ...                          # 纯静态（scratch 标配）

go get pkg@v1.2.3      # 定版本；@latest / @none
go mod edit -replace=old=./local/new   # ≈ cargo [patch]
go mod why pkg         # ≈ cargo tree -i
go list -m -u all      # 哪些依赖有新版

go test -count=1 ./...                 # 禁缓存（CI 必加）
go test -timeout 30s ./...             # 默认 10m，防死锁卡 CI
go test -bench=. -benchmem ./pkg/      # 基准（P7 方法论）
go test -race ./...                    # 数据竞争检测，CI 必开（P3）

go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30
go tool trace trace.out                # 调度/GC 时间线
golangci-lint run                      # vet 的补位，聚合 50+ linter
//go:generate stringer -type=Level     # 代码生成入口，产物进 git
```

pprof 完整取证、GODEBUG/GOEXPERIMENT 体系、go.work、文档工程，
全部在 P22 深潜。

### Rust 对照总表

| 主题 | Go | Rust | 差异要点 |
|---|---|---|---|
| 清单 | go.mod（导入投影） | Cargo.toml（显式声明） | go 按 import 驱动 |
| 锁 | go.sum（内容哈希） | Cargo.lock（版本号） | 字节级可复现 |
| 私有 | internal/（编译器） | pub(crate)（lint） | Go 是硬约束 |
| 格式 | gofmt（无配置） | rustfmt（可配置） | 全社区一种格式 |
| 元编程 | 无宏，go:generate 产物进 git | 宏/derive 编译期展开 | Go 代码生成是显式步骤 |
| 交叉编译 | GOOS/GOARCH 内置 | rustup target 逐个装 | Go 全平台开箱 |
