# 02 工程骨架：包、module、启动次序与工具链

> 详细程度：速通 · 约 15 分钟
> 材料：TB02、TB04 到 TB08、TB35b、TBX02、TBX11 · 旧长版：`notes/p1-toolchain.md`、`notes/p22-toolchain-docs.md`
> 前置：01 Go 的世界观

## 先看一段跑在 main 之前的代码

你八成以为 Go 程序从 `func main()` 开始执行。下面这段代码里 `main` 只有一行打印，运行结果却有四行，而且前两行发生在两句"变量赋值"里。先猜猜输出顺序，再往下读。

```go
package main

import "fmt"

var a = trace("变量 a")
var b = trace("变量 b")

func trace(s string) string {
	fmt.Println(s)
	return s
}

func init() {
	fmt.Println("init 函数")
}

func main() {
	fmt.Println("main 函数")
}
```

```text
变量 a
变量 b
init 函数
main 函数
```

`main` 不是第一个执行的代码：包级变量的初始化先于 `init` 函数，`init` 又先于 `main`。要讲清楚这件事，得先明白 Go 的构建模型只有两层：**包**是编译单元（一个目录一个包），**module** 是版本和发布单元（一个 `go.mod` 管一棵目录树）。导入路径、可见性、依赖版本、项目布局，全是这两层的推论。这一节从包讲起，经过 module、项目布局，最后回到"启动次序"，把开头这段代码彻底解释掉。

## 和你已知语言的差异

| 问题 | Java / Kotlin | Rust | TS | Go |
|---|---|---|---|---|
| 组织与编译单元 | 类 + 包（jar） | crate + mod | 文件 + npm 包 | 包（目录）+ module |
| 依赖与版本文件 | pom.xml / build.gradle | Cargo.toml + Cargo.lock | package.json + lockfile | go.mod + go.sum |
| 版本选择策略 | 手动指定 / 默认最新 | 语义化 + lock 锁死 | 语义化区间 + lockfile | MVS（最小版本） |
| 可见性 | public / private | pub | export / 不写 | 首字母大小写 |
| 入口前会执行的代码 | 静态初始化块 | 无（const/static 求值） | 顶层 import 副作用 | 包级变量 + init 函数 |
| 项目布局 | 官方约定（Maven 目录） | 较自由 | 较自由 | 无官方标准，通行事实标准 |

表里最需要重新建立的是第一行和最后一行：Go 里包和 module 是两回事，一个目录只属于一个包；而项目布局官方从没给过标准，你看到的 `cmd/`、`internal/` 都是社区约定。下面按顺序讲。

## 一、包：编译单元与可见性

包是 Go 最小的编译单元：一个目录里的所有 `.go` 文件必须声明同一个 `package` 名，编译器把一个目录当整体编译。包名写在每个文件第一行。

容易踩的第一个混淆点是**导入路径和包名是两回事**。`import "fmt"` 里的 `fmt` 是导入路径，指向标准库下的 fmt 目录；`fmt.Println` 里的 `fmt` 是包名，是代码里引用它时用的名字。两者约定俗成同名（路径最后一段就是包名），所以几乎没人意识到这是两个概念。〔TB04 p6-7〕

真正决定身份的是路径，不是包名。**包名不必唯一**，两个完全不同的包可以都叫 `rand`，只要导入路径不同。标准库里就有一对现成的：`math/rand` 和 `crypto/rand`，包名都是 `rand`。要同时用它们，就得给其中一个起别名，否则编译器分不清你说的是哪个 `rand`：

```go
package main

import (
	crand "crypto/rand"
	"fmt"
	mrand "math/rand"
)

func main() {
	fmt.Printf("%T\n", mrand.Int)
	fmt.Printf("%T\n", crand.Read)
}
```

```text
func() int
func([]uint8) (int, error)
```

`mrand` 和 `crand` 是我起的别名，`%T` 打印出的类型证明这是两个不同的包。Java/TS 里 import 的最后一段基本就是类名或包名；Go 里 import 的最后一段只是"默认用这个名字"，随时可以改。〔TB02 p9〕

一条包规则和可见性绑在一起：**首字母大写才导出，小写仅包内可见**（01 节的差异表讲过）。〔TB04 p7〕导入路径的构成是 module path 加子目录：`import "golearn/dnsproxi-go/wire"` 里，`golearn/dnsproxi-go` 是 module path，`/wire` 是包所在子目录。〔TB04 p11〕

最后是 `main` 包的两条硬规则：整个程序只能有一个 `main` 包；`main` 包**不能**被别的包导入，否则报 `import "xx/main" is a program, not an importable package`。〔TB04 p5〕〔TB04 p7〕它是程序入口，不是给别人复用的库。

## 二、module：为什么构建是可重现的

包的上一层是 module。构建在本质上只做三件事：确定每个依赖的版本、编译包、链接目标文件。依赖管理历经三个时代：GOPATH、vendor（1.5）、module（1.11 引入，1.16 起默认）。〔TB06 p2〕〔TB04 p11〕

GOPATH 时代（Go 1.11 之前）依赖直接放在一个全局 `src` 目录里，`go get` 只拉最新主线版本、不锁定。同一个项目，张三今天构建和你明天构建，可能拿到不同版本的依赖，结果不一样，这叫不可重现。〔TB06 p3-4〕Go 1.5 引入 vendor：把依赖源码复制进仓库，首次实现可重现，但要提交庞大的 vendor 目录、手工管版本，体验很差。〔TB06 p5〕

module 用三个机制把问题彻底解决：

1. **go.mod + go.sum 锁定内容**。`go.mod` 记直接依赖和版本，`go.sum` 记依赖内容的哈希，下次下载时比对，防篡改。两者都要提交进仓库。〔TB06 p8-9〕
2. **语义导入版本**。兼容的版本共用同一个导入路径；不兼容的主版本升级，把主版本号写进路径，`.../logrus/v2` 就成了一个新依赖。于是同一个项目可以同时用同一个库的两个不兼容大版本，互不打架。〔TB06 p10-12〕〔TB07 p6-8〕
3. **最小版本选择（MVS）**。A 要 C v1.1.0，B 要 C v1.3.0，最新其实是 v1.7.0，Go 选 v1.3.0：满足所有依赖方要求的最小版本。多数语言选"最新最大"，Go 认为 MVS 才是持久可重现的关键。〔TB06 p12-14〕

日常就四条命令：

```sh
go mod init example.com/app             # 建 module
go mod tidy                             # 按源码增删依赖，最常用
go get example.com/lib@v1.7.0           # 指定版本，升降级都用它
go install example.com/cmd/tool@latest  # 安装可执行文件
```

两个易错点：Go 1.17 起 `go get` 不再用来装可执行文件，改 `go install ...@版本`；〔TBX02 p8〕只删掉源码里的 `import` 不会清掉 `go.mod`，要跑 `go mod tidy`。〔TB07 p8-9〕

本地联调用 `replace` 指向本地路径，缺点是路径因人而异、而且 `go.mod` 要提交；Go 1.18 的 `go.work` 工作区把这种配置放进不进仓库的文件里，更适合。〔TB35b p2-4〕私有 module 用 `GOPRIVATE` 绕过公共代理和校验。〔TB35b p5〕自己发布时，单仓库单 module 最简单；某个版本发坏了，用新 patch 版本加 `retract` 作废，不要删 tag 重发。〔TBX11 p1-4〕〔TBX11 p9-12〕

## 三、项目布局：没有官方标准

官方至今没给过项目布局的正式定义，通行的是事实标准。〔TB05 p1〕可执行项目的典型形状：

```text
go.mod  go.sum
cmd/app1/main.go   # 每个子目录一个 main 包，只做参数解析和初始化，保持简洁
cmd/app2/main.go
pkg1/  pkg2/       # 非 main 包平铺在根目录，可被外部引用
internal/          # 仅本项目可导入
```

库项目去掉 `cmd/`，也不放 `vendor/`。〔TB05 p8-12〕注意 `golang-standards/project-layout` 不是官方标准，别当权威引用。〔TB05 p13〕

其中 `internal/` 是 Go 1.4 引入的、由编译器强制的真实访问控制：别的 module 想导入 `lib/internal/secret`，会得到 `use of internal package lib/internal/secret not allowed`。〔TB05 p5〕〔TB05 p16〕所以对外承诺稳定的放普通包，不想承诺的塞进 `internal/`，这是库作者划 API 边界的主要手段。

## 四、启动次序：从包初始化到 main

开头那段代码为什么先打印"变量 a"？因为 Go 程序启动时先按导入关系初始化所有包，最后才进 `main.main`。整体次序是：按导入顺序递归、**深度优先**初始化依赖包；每个包内部先常量、再变量、最后 `init` 函数；一个包被多处依赖只初始化一次；这些都做完才轮到 `main` 包自己的变量、`init` 和 `main.main`。〔TB08 p5-8〕

把"深度优先"画成树更容易记。假设 `main` 空导入 `pkg1` 和 `pkg2`，两者都依赖 `pkg3`：

```text
main
├── pkg1 ── pkg3
└── pkg2 ── pkg3
```

初始化顺序是 `pkg3 → pkg1 → pkg2 → main`：先沿第一条边走到底（pkg3），回退再走 pkg1，再走 pkg2。pkg3 被依赖两次却只初始化一次；pkg1 在 pkg2 前，是因为 `main` 里写在前面的 `import` 先初始化。〔TB08 p7-8〕

`init` 函数有三条硬规则：不能带参数和返回值；不能被显式调用；可以有多个，按出现顺序执行。〔TB08 p4〕〔TB08 p14〕想显式调用它，编译器直接拒绝：

```go fail
package main

import "fmt"

func init() { fmt.Println("init") }

func main() {
	init()
}
```

```text
undefined: init
```

`init` 最正当的用途是**注册模式**：`import _ "github.com/lib/pq"` 里的下划线叫空导入，只为触发这个包的初始化，它的 `init` 会调 `sql.Register("postgres", ...)`，之后 `sql.Open("postgres", ...)` 才能按名字取到驱动。〔TB08 p11-13〕标准库的 `crypto` 也靠这套机制：`crypto/sha256` 的 `init` 把 SHA-256 注册进 `crypto` 的哈希表，所以下面这段代码里 `SHA256.Available()` 是 true，而没被导入的 MD4 是 false：

```go
package main

import (
	"crypto"
	_ "crypto/md5"
	_ "crypto/sha256"
	"fmt"
)

func main() {
	fmt.Println(crypto.SHA256.Available())
	fmt.Println(crypto.MD4.Available())
}
```

```text
true
false
```

代价是依赖变隐式：光看 `import _` 看不出它在注册什么。所以 `init` 只用来做注册这类副作用，不要在 `init` 里读配置、做 I/O。

最后一条和启动次序同样重要：`main` 返回，整个进程立刻结束，不管还有没有子 goroutine 在跑。〔TB08 p14-15〕下面这个 goroutine 睡了 50 毫秒，但 `main` 直接返回，它永远没机会打印：

```go
package main

import (
	"fmt"
	"time"
)

func main() {
	go func() {
		time.Sleep(50 * time.Millisecond)
		fmt.Println("子 goroutine 打不到这行")
	}()
	fmt.Println("main 返回")
}
```

```text
main 返回
```

想优雅退出，要由主 goroutine 决定是否等待子 goroutine。

## 五、工具链一站式

一个 `go` 命令覆盖构建、格式化、依赖、静态检查、测试、文档、剖析，还有语言服务器和运行时跟踪。〔TB02 p10〕交付前最常用的一组：

```sh
gofmt -l .                   # 列出没格式化的文件，风格不讨论
go vet ./...                 # 静态检查，抓低级错误
go test -race -count=1 ./... # 竞态检测，并发代码必跑
go tool pprof <profile>      # 剖析：CPU、内存、阻塞
```

pprof 采集有两条路：在线服务 `import _ "net/http/pprof"` 再开一个监听端口；离线用 `go test -cpuprofile` 或 `-memprofile` 生成文件。剖析实操见 08 节，更完整的工具链进阶见旧长版 `notes/p22-toolchain-docs.md`。

## 小结

- 包是编译单元（一个目录一个包），module 是版本和发布单元（一个 go.mod 管一棵目录树）。
- 命名空间是导入路径，不是包名；包名不必唯一，冲突就起别名；首字母大写才导出。
- module 靠 go.mod/go.sum、语义导入版本、MVS 三个机制实现可重现构建。
- 项目布局没有官方标准，internal/ 是唯一的编译器级访问控制。
- 启动次序：依赖包深度优先初始化，包内常量 → 变量 → init，最后才进 main；init 只用于注册；main 返回即进程结束。
- 工具链一个 go 命令全覆盖。

## 常见坑

1. 把项目放 GOPATH 下期待 vendor 生效：module 时代完全不需要。〔TB06 p5〕
2. 跨大版本升级只改 go.mod 不改导入路径：编译找不到包，因为 v2 版本在路径里。
3. 多 module 仓库打 tag 不带子目录前缀：用户 `go get` 看不到新版本。〔TBX11 p4〕
4. 发了 v2 没把 module path 加 /v2：被 go 命令拒绝。〔TB07 p13〕
5. 在 init 里做 I/O 或读配置：依赖变隐式，启动顺序难推理，只该做注册。

## 判断题与答案

1. `a` 依赖 `c@v1.1.0`，`b` 依赖 `c@v1.3.0`，你的项目同时用 `a` 和 `b`，实际构建用哪个版本的 `c`？
2. `import _ "x"` 里的下划线在做什么，副作用发生在初始化的哪一步？
3. 两个不同的包都叫 `rand`，能不能同时导入？

答案：

1. v1.3.0。MVS 选的是满足所有依赖方要求的最小版本，不是最新版本 v1.7.0，也不是某个依赖方单独要的 v1.1.0。
2. 空导入，只为触发 x 包的初始化。副作用发生在依赖包深度优先初始化阶段，即 x 的包级变量和 init 函数执行时，早于 main。
3. 能。导入路径才是命名空间，包名不必唯一；名字撞了就在 import 里起别名，如 `mrand "math/rand"`。

## 在项目里

`dnsproxi-go/` 是单 module：`go.mod` 里 `module golearn/dnsproxi-go`，配 `go.mod`/`go.sum`。它没套 `cmd/` 约定：主服务是根目录 `main.go` 的 `main` 包，压测发包器是 `dnsq/main.go` 的第二个 `main` 包；`cache/`、`guard/`、`tcp/`、`wire/` 是平铺的小包，没有 `internal/`。回归命令：`cd dnsproxi-go && go vet ./... && go test -race -count=1 ./...`。

## 想深入去哪查

- 构建模式、MVS、语义导入版本：TB06 全讲；日常依赖操作：TB07。
- 项目布局演进：TB05；初始化次序：TB08；go.work 与私有仓库：TB35b。
- 自己发布 module 的坑：TBX11；1.17 的 module 剪枝等变化：TBX02。
- 旧长版：`notes/p1-toolchain.md`（cargo 到 go 的映射）、`notes/p22-toolchain-docs.md`（pprof、lint、go.work、文档工程）。
