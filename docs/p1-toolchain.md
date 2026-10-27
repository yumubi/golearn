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

Go 按包编译，并利用依赖图和构建缓存并行处理可独立编译的包。
通常 `go build` 产出原生可执行文件；是否依赖动态库取决于平台、
CGO 和构建选项，不能把所有 Go 二进制都当成静态链接。

例如 deepsearch 的 Dockerfile 使用 `CGO_ENABLED=0 go build`。纯 Go
程序可以方便地交叉编译并部署，但目标平台、证书、时区数据和程序所需
资源仍须核对；启用 CGO 时还需要目标平台的 C 工具链。

## 命令映射：你会的 cargo 对应什么

| 你会的 | Go 对应 | 差异要点 |
|---|---|---|
| `cargo new` | `go mod init <module>` | go 不建目录，先 `mkdir` 再 init |
| `Cargo.toml` | `go.mod` | 声明模块、Go 版本、依赖及其他指令 |
| `Cargo.lock` | `go.mod` + `go.sum` | 前者参与版本选择，后者记录内容哈希；通常都提交 |
| `cargo add serde` | `go get github.com/...` | **先写 import 再 `go mod tidy`** 是更地道的流 |
| `cargo build` | `go build ./...` | `./...` = 全部包，天天用 |
| `cargo run` | `go run ./cmd/xxx` | go run 只能跑 main 包 |
| `cargo test` | `go test ./...` | `_test.go` 可用同包白盒测试，也可用 `包名_test` 黑盒测试 |
| `cargo test -- --nocapture` | `go test -v ./...` | `-v` 兼打印+进度 |
| `cargo clippy` | `go vet ./...` | 更弱；生态靠 `golangci-lint` 补 |
| `cargo fmt` | `gofmt -w .` | **没有配置项**，全社区一种格式 |
| `cargo doc --open` | `go doc` / `pkg.go.dev` | 本地网页可另装 `golang.org/x/pkgsite/cmd/pkgsite` |
| `[dev-dependencies]` | 无 | 测试和源码同包，依赖不分 dev/prod |
| feature flags | 无 | 用 build tags 手动切 |
| workspace（cargo） | go.work（多 module） | 单 module 多目录不需要 workspace |
| crates.io | proxy.golang.org | 国内配 goproxy.cn |

## go.mod 和 go.sum 各管什么

源码的 import 指向包；`go.mod` 则记录提供这些包的模块及版本要求。
写好 import 后运行 `go mod tidy`，工具会补齐缺少的模块并清理不再需要的
要求，也会考虑测试和构建标签。版本选择不是由 `go.sum` 完成的。

例如先在 `main.go` 导入 `golang.org/x/sync/errgroup` 并实际使用，
再运行 `go mod tidy`，`go.mod` 才会增加相应的 `require`。具体版本取决于
工具链和解析结果；空目录里直接运行 tidy 不会凭空添加这个依赖。
`go.sum` 记录下载内容的校验哈希，供模块校验使用，不是 Cargo.lock
那样的完整版本锁文件；它可能包含不在当前构建列表中的历史条目。

## 可见性：一个目录一个包

Rust 一个 crate 里有多个 mod；Go 通常一个目录对应一个包，包名由文件的 `package` 声明决定，惯例与目录名一致。

```
deepsearch/               ← module root (go.mod)
├── main.go               ← package main
├── cmd/                  ← cobra 子命令
├── internal/             ← 只允许其父目录树中的代码导入
├── pkg/                  ← 可以被外人导入
└── go.mod
```

`internal/` 的导入限制由 Go 工具链执行：导入方须位于该 `internal` 父目录的目录树内，范围不一定等于模块。Rust 的 `pub(crate)` 同样是编译器强制的可见性限制，只是边界不同。
大写标识符可导出，小写仅在本包可见。

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

- `go mod tidy` 根据源码与测试整理模块要求；`go.mod` 参与版本选择，`go.sum` 校验内容。
- internal/ 以父目录树限制导入，大写标识符导出。
- 纯 Go 程序常适合单二进制部署；目标平台和外部资源仍需核对。

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
| 清单 | go.mod（模块与依赖要求） | Cargo.toml（显式声明） | tidy 可根据 import 更新要求 |
| 校验 | go.sum（内容哈希） | Cargo.lock（解析版本及校验信息） | go.sum 本身不锁定构建列表 |
| 私有 | internal/（目录树限制） | pub(crate)（crate 限制） | 两者均由工具链强制 |
| 格式 | gofmt（无配置） | rustfmt（可配置） | 全社区一种格式 |
| 元编程 | 无宏，go:generate 产物进 git | 宏/derive 编译期展开 | Go 代码生成是显式步骤 |
| 交叉编译 | GOOS/GOARCH 内置 | rustup target 逐个装 | CGO 还需目标平台 C 工具链 |
