# 02 工程骨架：包、module、启动次序与工具链

> 详细程度：速通 · 约 15 分钟
> 材料：TB04 到 TB08、TB35b、TBX11 · 旧长版：`notes/p1-toolchain.md`、`notes/p22-toolchain-docs.md`
> 前置：01 Go 的世界观

## 这一节要建立的认知

Go 的构建模型只有两层：**包**是编译单元（一个目录一个包），**module** 是版本和发布单元（一个 `go.mod` 管一棵目录树）。其余一切（导入路径、可见性、依赖版本、项目布局）都是这两层的推论。

## 一、包与可见性

导入路径是 module path 加子目录；`import "fmt"` 里的 `fmt` 是路径，`fmt.Println` 里的 `fmt` 是包名，两者通常同名所以容易混，命名空间是路径，两个包可以都叫 `util`。〔TB04 p6-7〕导出规则（首字母大写）见 01 的差异表；`main` 包整个程序只能有一个，且不能被别的包导入。〔TB04 p5〕〔TB04 p7〕

## 二、module 与依赖：为什么构建是可重现的

构建的本质是三步：确定包版本、编译包、链接目标文件。依赖管理历经 GOPATH、vendor（1.5）、module（1.11 引入，1.16 起默认）三次迭代。〔TB06 p2〕〔TB04 p11〕

GOPATH 时代的缺陷是只拉最新主线版本、不锁定，不同人不同时间构建结果不同。〔TB06 p3-4〕module 靠三件事解决：

1. **go.mod** 记录直接依赖和版本；**go.sum** 记录依赖内容的哈希，再次下载时比对，防篡改。两者都要提交。〔TB06 p8-9〕
2. **语义导入版本**：兼容的版本导入路径相同，主版本升级进路径（`.../logrus/v2`）。所以同一个项目可以同时依赖同一个库的两个不兼容大版本，升级跨大版本等于"引入一个新依赖"。〔TB06 p10-12〕〔TB07 p6-8〕
3. **最小版本选择（MVS）**：A 要 C v1.1.0，B 要 C v1.3.0，最新是 v1.7.0，Go 选 v1.3.0，即满足所有依赖方的最小版本。多数语言和旧工具选"最新最大"，Go 团队认为 MVS 是持久可重现构建的最佳方案。〔TB06 p12-14〕

日常操作只有几个：

```sh
go mod init example.com/app   # 建 module
go mod tidy                   # 按源码增删依赖，是最常用的一条
go get example.com/lib@v1.7.0 # 指定版本，升降级都用它
go install example.com/cmd/tool@latest   # 安装可执行文件
```

注意 Go 1.17 起 `go get` 安装命令已被弃用（1.18 起不再构建安装），改用 `go install ...@版本`。〔TBX02 p8〕仅删源码里的 `import` 不会清理 `go.mod`，要跑 `go mod tidy`。〔TB07 p8-9〕

本地联调和私有仓库：未发布的 module 用 `replace` 指向本地路径，缺点是路径因人而异、`go.mod` 要提交；更好的方案是 Go 1.18 起的 `go.work` 工作区，配置不进仓库。〔TB35b p2-4〕私有 module 用 `GOPRIVATE` 绕开公共代理和校验。〔TB35b p5〕自己发布 module 时，单仓库单 module 最简单；一个 module 出问题，用新 patch 版本加 `retract` 作废，而不是删 tag 重发。〔TBX11 p1-4〕〔TBX11 p9-12〕

## 三、项目布局：没有官方标准

官方至今没有项目布局的正式定义，通行布局是事实标准。〔TB05 p1〕可执行项目的典型形状：

```text
go.mod  go.sum
cmd/app1/main.go   # 每个子目录一个 main 包，只做参数解析与初始化，要简洁
cmd/app2/main.go
pkg1/  pkg2/       # 非 main 包平铺在根目录，可被外部引用
internal/          # 仅本项目可导入的包
```

库项目去掉 `cmd/`，不放 `vendor/`。〔TB05 p8-12〕`golang-standards/project-layout` 不是官方标准，不能当权威。〔TB05 p13〕

`internal/` 是 Go 1.4 引入的真实访问控制，由编译器强制：另一个 module 导入它会得到 `use of internal package lib/internal/secret not allowed`。〔TB05 p5〕〔TB05 p16〕所以对外要承诺稳定的放在普通包里，不想承诺的放进 `internal/`，这是库作者管理 API 边界的主要手段。

## 四、启动次序：从包到 main

整体次序：按导入顺序递归、深度优先初始化依赖包；每个包内先常量、再变量、最后 `init` 函数；一个包被多处依赖只初始化一次；最后才进 `main.main`。〔TB08 p5-8〕下面是我在本机 go1.26.4 上建的三包小项目（module 名 `x`）：`main` 空导入 `pkg1` 和 `pkg2`，两者都导入 `pkg3`；每个包有一个初始化时打印的包级变量和一个 `init`。

```go
// pkg3/pkg3.go
package pkg3

import "fmt"

func Trace(s string) int { fmt.Println(s); return 0 }

var _ = Trace("pkg3 var")

func init() { Trace("pkg3 init") }

// pkg1/pkg1.go（pkg2/pkg2.go 完全同构，只把 pkg1 换成 pkg2）
package pkg1

import "x/pkg3"

var _ = pkg3.Trace("pkg1 var")

func init() { pkg3.Trace("pkg1 init") }

// main.go
package main

import (
	_ "x/pkg1"
	_ "x/pkg2"
	"x/pkg3"
)

var _ = pkg3.Trace("main var")

func init() { pkg3.Trace("main init") }
func main() { pkg3.Trace("main.main") }
```

```text
pkg3 var
pkg3 init
pkg1 var
pkg1 init
pkg2 var
pkg2 init
main var
main init
main.main
```

`pkg3` 只初始化一次，且先于依赖它的两个包；`pkg1` 先于 `pkg2` 是因为 `main` 里的导入顺序；每个包内变量先于 `init`；最后才是 `main` 自己的变量、`init` 和 `main.main`。

三条行为：`init` 排在包内其他元素之后；每个 `init` 只执行一次；多个 `init` 顺序执行。`init` 不能被显式调用。〔TB08 p4〕〔TB08 p14〕

`init` 最常见的用途是**注册模式**：`import _ "github.com/lib/pq"` 的 `init` 调 `sql.Register("postgres", ...)`，之后 `sql.Open("postgres", ...)` 按名字取驱动。`image` 包的格式注册同理。〔TB08 p11-13〕坑在于它让依赖变隐式，所以只用于这类注册，不要在 `init` 里做 I/O 或读配置。

`main` 返回时整个程序立刻结束，不管子 goroutine 是否还在跑。想优雅退出，要由主 goroutine 决定是否等待。〔TB08 p14-15〕

## 五、工具链一站式

一个 `go` 命令覆盖构建、格式化、依赖、静态检查、测试、文档、剖析。〔TB02 p10〕交付前常用的一组：

```sh
gofmt -l .                   # 列出没格式化的文件，风格不讨论
go vet ./...                 # 静态检查，能抓出很多低级错误
go test -race -count=1 ./... # 竞态检测，并发代码必跑
go tool pprof <profile>      # 剖析：CPU、内存、阻塞
```

pprof 的采集有两条路：在线服务 `import _ "net/http/pprof"` 再开一个监听端口；离线用 `go test -cpuprofile` 或 `-memprofile` 生成文件。剖析的实操见 08 节，更完整的工具链进阶见旧长版 `notes/p22-toolchain-docs.md`。

## 常见坑

1. 把项目放在 `GOPATH` 下并期待 vendor 生效：module 时代完全不需要。〔TB06 p5〕
2. 跨大版本升级只改 `go.mod` 不改导入路径：编译找不到包。
3. 多 module 仓库打 tag 不带子目录前缀：用户 `go get` 看不到新版本。〔TBX11 p4〕
4. 发了 `v2` 没把 module path 加 `/v2`：被 go 命令拒绝。〔TB07 p13〕

## 判断题（先答再看）

1. `a` 依赖 `c@v1.1.0`，`b` 依赖 `c@v1.3.0`，你的项目同时用 `a` 和 `b`，实际构建用哪个版本的 `c`？
2. `import _ "x"` 里的下划线在做什么，副作用发生在初始化的哪一步？

## 在项目里

`dnsproxi-go/` 是单 module 单二进制的最小形状：`go.mod` 里 `module golearn/dnsproxi-go`，根目录 `main.go` 是 `main` 包，`cache/`、`wire/`、`tcp/` 等是平铺的小包，没有 `internal/` 和 `cmd/`，因为它只有一个二进制且不打算被外部导入。`main` 里的启动流程见 01 节。回归命令：`cd dnsproxi-go && go vet ./... && go test -race -count=1 ./...`。

## 想深入去哪查

- 构建模式、MVS、语义导入版本：TB06 全讲；日常依赖操作：TB07。
- 项目布局演进：TB05；初始化次序：TB08；`go.work` 与私有仓库：TB35b。
- 自己发布 module 的坑：TBX11；1.17 的 module 剪枝等变化：TBX02。
- 旧长版：`notes/p1-toolchain.md`（cargo 到 go 的映射）、`notes/p22-toolchain-docs.md`（pprof、lint、`go.work`、文档工程）。
