# P1 · 工具链速通：cargo → go

> 读者：会 cargo，第一次碰 go 工具链。目标：把肌肉记忆一一映射到
> go 命令，10 分钟读完，练习 15 分钟。这章是速查表：类比总表在开头，
> 它本身就是章末参考，记住"import 驱动"这一条差异就够开工。
> 下一步：P2 换语言直觉，P14 学并发组装。

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
