---
title: 02 工程骨架：包、module、启动次序与工具链
---

# 02 工程骨架：包、module、启动次序与工具链


> 详细程度：速通 · 约 35 分钟
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
| 版本选择策略 | 手动指定版本，传递依赖冲突时由构建工具仲裁 | 语义化区间，取满足约束的最高兼容版本，lock 锁死 | 语义化区间 + lockfile | MVS（最小版本） |
| 可见性 | public / private | pub | export / 不写 | 首字母大小写 |
| 入口前会执行的代码 | 静态初始化块 | 无（const/static 求值） | 顶层 import 副作用 | 包级变量 + init 函数 |
| 项目布局 | 官方约定（Maven 目录） | 较自由 | 较自由 | 无官方标准，通行事实标准 |

表里最需要重新建立的是第一行和最后一行：Go 里包和 module 是两回事，一个目录只属于一个包；而项目布局官方从没给过标准，你看到的 `cmd/`、`internal/` 都是社区约定。下面按顺序讲。

## 一、包：编译单元与可见性

包是 Go 最小的编译单元：一个目录里的所有 `.go` 文件必须声明同一个 `package` 名（`_test` 后缀的外部测试包是例外），编译器把一个目录当整体编译。包名写在每个文件第一行。

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

最后是 `main` 包的两条硬规则：一个可执行程序里只能有一个 `main` 包（同一个仓库里要放第二个程序，就放进另一个目录，各自成一个程序）；`main` 包**不能**被别的包导入，否则报 `import "xx/main" is a program, not an importable package`。〔TB04 p5〕〔TB04 p7〕它是程序入口，不是给别人复用的库。

## 二、module：为什么构建是可重现的

包的上一层是 module。构建在本质上只做三件事：确定每个依赖的版本、编译包、链接目标文件。依赖管理历经三个时代：GOPATH、vendor（1.5）、module（1.11 引入，1.16 起默认）。〔TB06 p2〕〔TB04 p11〕

GOPATH 时代（Go 1.11 之前）依赖直接放在一个全局 `src` 目录里，`go get` 只拉最新主线版本、不锁定。同一个项目，张三今天构建和你明天构建，可能拿到不同版本的依赖，结果不一样，这叫不可重现。〔TB06 p3-4〕Go 1.5 引入 vendor：把依赖源码复制进仓库，首次实现可重现，但要提交庞大的 vendor 目录、手工管版本，体验很差。〔TB06 p5〕

module 用三个机制把问题彻底解决：

1. **go.mod + go.sum 锁定内容**。`go.mod` 记直接依赖和版本，`go.sum` 记依赖内容的哈希，下次下载时比对，防篡改。两者都要提交进仓库。〔TB06 p8-9〕
2. **语义导入版本**。兼容的版本共用同一个导入路径；不兼容的主版本升级，把主版本号写进路径，`.../logrus/v2` 就成了一个新依赖。于是同一个项目可以同时用同一个库的两个不兼容大版本，互不打架。〔TB06 p10-12〕〔TB07 p6-8〕
3. **最小版本选择（MVS）**。A 要 C v1.1.0，B 要 C v1.3.0，最新其实是 v1.7.0，Go 选 v1.3.0：满足所有依赖方要求的最小版本。注意这不是所有已发布版本里最小的那个，而是"每个依赖方的要求都满足"里的最小。多数语言选"最新最大"，Go 团队给 MVS 的理由有三：对开发者可预测（结果不会因为别处升级而变）、实现简单（几十行代码）、可重现（"选最新"在不同时刻给出不同产物）。前提是各方都遵守语义化版本规范，违反契约规则就失效。〔TB06 p12-14〕〔TB06 p20〕

日常就四条命令：

```sh
go mod init example.com/app             # 建 module
go mod tidy                             # 按源码增删依赖，最常用
go get example.com/lib@v1.7.0           # 指定版本，升降级都用它
go install example.com/cmd/tool@latest  # 安装可执行文件
```

两个易错点：Go 1.17 起 `go get` 不再用来装可执行文件，改 `go install ...@版本`（本机 go1.26 的 `go help get` 也写明，现在 `go get` 只负责调整 go.mod）；〔TBX02 p8〕只删掉源码里的 `import` 不会清掉 `go.mod`，要跑 `go mod tidy`。〔TB07 p8-9〕

依赖下载走 `GOPROXY`（默认官方代理，国内常换成 goproxy.cn），拉下来的内容存进本地 module 缓存（`GOMODCACHE`，默认 `$GOPATH/pkg/mod`），同一个版本全机器只存一份、只读。离线或 CI 场景用 `go build -mod=vendor` 走仓库里的 `vendor/` 目录；1.14 起顶层有 `vendor/` 时默认就优先用它。〔TB06 p8〕〔TB07 p10〕

本地联调用 `replace` 指向本地路径，缺点是路径因人而异、而且 `go.mod` 要提交；Go 1.18 的 `go.work` 工作区把这种配置放进不进仓库的文件里，更适合。〔TB35b p2-4〕私有 module 用 `GOPRIVATE` 绕过公共代理和校验；企业内网的标准做法是自建一个 goproxy 统一代理内外所有 module，开发者只配一个地址，心智负担最低。〔TB35b p5-8〕

发布侧的两个冷知识。其一，Go 1.17 起依赖图剪枝：`go.mod` 声明 `go 1.17` 后，构建只用直接依赖与真正被用到的间接依赖的 `go.mod`，但 `go mod tidy` 会把全部间接依赖记进 `go.mod`（分块 `// indirect`），它第一次有了 lock 文件的完整列表效果。〔TBX02 p4-8〕其二，某个版本发坏了（比如 `v1.0.2` 有致命 bug），**删 tag 重发是无效的**：版本一旦被任何一个 goproxy 缓存，后续请求不回源，散布在各代理里的坏版本继续传播，源仓库删了也没用。正解是发 `v1.0.3` 修复版（MVS 保证没显式升级的消费者不受影响），再在 `v1.0.3` 的 `go.mod` 里 `retract v1.0.2` 作废旧版（只提示不强制，`go list` 会显示 retracted）；作废整个大版本用首行 `// Deprecated:` 注释。〔TBX11 p5-12〕

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

开头那段代码为什么先打印"变量 a"？因为 Go 程序启动时先按导入关系初始化所有包，最后才进 `main.main`。整体次序是：**依赖先于使用者**，沿依赖关系递归初始化；每个包内部先常量、再变量、最后 `init` 函数；一个包被多处依赖只初始化一次；这些都做完才轮到 `main` 包自己的变量、`init` 和 `main.main`。〔TB08 p5-8〕材料把同级依赖的先后讲成「按 import 书写顺序」，这在 Go 1.21 之前成立。我在本机 go1.26 上实测：`main` 先写 `import _ "ex/zz"` 再写 `import _ "ex/aa"`，两个包互不依赖，输出却是 `aa` 在前 `zz` 在后。1.21 起规范改成：把所有包按导入路径排序，每次取排在最前、且依赖都已初始化完的那个。所以同级包的先后看路径字典序，不要依赖 import 的书写顺序。

把依赖关系画成树更容易记。假设 `main` 空导入 `pkg1` 和 `pkg2`，两者都依赖 `pkg3`：

```text
main
├── pkg1 ── pkg3
└── pkg2 ── pkg3
```

初始化顺序是 `pkg3 → pkg1 → pkg2 → main`：pkg3 没有依赖，最先；pkg1 和 pkg2 都只等 pkg3，此时按路径排序，pkg1 在 pkg2 前；最后才是 `main`。pkg3 被依赖两次却只初始化一次。〔TB08 p7-8〕

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
- 启动次序：依赖包先于使用者初始化，包内常量 → 变量 → init，最后才进 main；init 只用于注册；main 返回即进程结束。
- 工具链一个 go 命令全覆盖。

## 常见坑

1. 沿用 GOPATH 时代的习惯：vendor 机制要求项目必须放在 GOPATH 的 src 下才生效，module 时代项目放哪都行，默认也不靠 vendor。〔TB06 p5〕
2. 跨大版本升级只改 go.mod 不改导入路径：编译找不到包，因为 v2 版本在路径里。
3. 多 module 仓库打 tag 不带子目录前缀：用户 `go get` 看不到新版本。〔TBX11 p4〕
4. 发了 v2 的 tag 却没把 module path 改成带 `/v2`：这违反语义导入版本的规则，go 命令会拒绝这个版本。〔TB07 p6-7〕
5. 在 init 里做 I/O 或读配置：依赖变隐式，启动顺序难推理，只该做注册。

## 判断题与答案

1. `a` 依赖 `c@v1.1.0`，`b` 依赖 `c@v1.3.0`，你的项目同时用 `a` 和 `b`，实际构建用哪个版本的 `c`？
2. `import _ "x"` 里的下划线在做什么，副作用发生在初始化的哪一步？
3. 两个不同的包都叫 `rand`，能不能同时导入？

答案：

1. v1.3.0。MVS 选的是满足所有依赖方要求的最小版本，不是最新版本 v1.7.0，也不是某个依赖方单独要的 v1.1.0。
2. 空导入，只为触发 x 包的初始化。副作用发生在依赖包初始化阶段，即 x 的包级变量和 init 函数执行时，早于 main。
3. 能。导入路径才是命名空间，包名不必唯一；名字撞了就在 import 里起别名，如 `mrand "math/rand"`。

## 在项目里

`dnsproxi-go/` 是单 module：`go.mod` 里 `module golearn/dnsproxi-go`，配 `go.mod`/`go.sum`。它没套 `cmd/` 约定：主服务是根目录 `main.go` 的 `main` 包，压测发包器是 `dnsq/main.go` 的第二个 `main` 包；`cache/`、`guard/`、`tcp/`、`wire/` 是平铺的小包，没有 `internal/`。回归命令：`cd dnsproxi-go && go vet ./... && go test -race -count=1 ./...`。

## 想深入:正文之外的干货

### TB04-TB08 编译、布局与依赖操作补遗

- 交付形态的差别:go build 产出的是自包含二进制,目标环境不用装 Go;动态语言要解释器跟着走。go run 只用于开发调试,交付永远 go build。〔TB04 p8-9〕
- go mod tidy 一次做完三件事:扫描源码、下载缺失依赖、把直接与间接依赖写进 go.mod;go.sum 记录每个依赖的版本 hash,内容对不上直接拒绝构建,供应链校验就靠它。〔TB04 p12-13〕
- 布局演进三轮:1.0 的 GOPATH 路径约定、vendor 目录、1.11 起 Go Module 把版本绑定到 module 而不是目录;1.13 加上 go.mod/go.sum 的创世项目形态,构建从此可重现。〔TB05 p6〕
- 项目布局约定:`internal/` 下的包只有本 module 能导入,编译器强制;标准库自带包全在 `std` 这一个 module 里。〔TB05 p7〕
- 建 module 三步:`go mod init` 写下 module path,`go mod tidy` 补依赖,`go build` 验证;项目从此放哪都行,不受 GOPATH 约束。〔TB06 p6-7〕
- 添加一个新依赖的标准流程:源码里先 import,`go build` 报 `no required module provides package` 并提示 go get;`go get` 下载进本地 module 缓存并在 require 段加一行,或者一步 `go mod tidy` 代替。〔TB07 p2-3〕
- 版本升降级:`go list -m -versions <module>` 列全部版本,`go get <module>@v1.7.0` 降级(输出 downgraded v1.8.1 => v1.7.0),也可 `go mod edit -require=...` 后 tidy。原理是语义导入版本:主版本相同即兼容,go 命令据此自动选。〔TB07 p4-5〕
- 给自己的 module 发 v3+:仓库里有 go.mod 而导入路径没带 /v3 后缀时,go 命令直接拒绝,主版本不兼容不是建议是语法。〔TB07 p13〕
- 可执行程序的 main 包必须定义 `main` 函数,否则编译报错;`main.main` 无参数无返回值,运行在主 goroutine 里。〔TB08 p2〕

### TB35b 与 TBX11 私有仓库与发布布局

- 私有 module 的导入路径问题:内部 vcs 通常没有域名,导入路径又不能写 IP,解法是为私有 module 自定义统一路径(如 mycompany.com/go/module1),再配 GOPRIVATE/GONOSUMDB 让 go 命令绕过公共代理与校验。〔TB35b p10〕
- 自建 goproxy 的价值:统一内部依赖出口、加速、审计;go.work 只影响本机构建选择,不影响 go.mod 内容。〔TB35b p10-14〕
- 无 monorepo 强约束时的默认解:单 repo 单 module,module path 与仓库地址一致,打 tag 即版本号;pkg 导入路径 = module path + /pkgN。〔TBX11 p2-3〕
- 寄存器调用惯例(1.17)的影响面:绝大多数程序无感,受影响的只有违反 unsafe.Pointer 规则的代码(用 unsafe.Pointer 访问函数参数、比较函数代码指针这类未公开行为)。〔TBX02 p12-14〕
- 1.17 的语言级小改进落在切片上:此前"数组能切片化、切片不能转回数组",只能 unsafe.Pointer 黑魔法;现在 `p := (*[3]int)(b)` 直接把切片转成数组指针,对 `p[1]` 的修改会反映回原切片。十二年前提的 issue 落地成语法。〔TBX02 p2-3〕

### TB03 Go 的安装与版本策略

- 发布节奏是每年二月、八月各一个大版本。官方只为最新的两个大版本提供支持(修重大 bug、改文档、安全更新),再旧的大版本没有安全补丁可拿,长期项目钉在旧版本上要自己承担风险。〔TB03 p2〕
- 选版本的三种思路:用最新版(稳定版平均质量一直很高,本课程推荐)。求稳用次新版,即最新版的前一个版本,这是多数量产项目的选择。或者跟随具体项目:Kubernetes 升级激进,docker 曾落后两个周期还在用 Go 1.15。选版本质是新特性收益与稳定性风险的权衡。〔TB03 p2〕
- 大陆下载用镜像站 golang.google.cn/dl 替代官方 golang.org/dl,下载时间大幅缩短。构建前唯一必须配的是 GOPROXY(02 节正文已讲,默认官方代理,国内常用 goproxy.cn)。〔TB03 p3〕〔TB03 p13〕
- Linux 标准安装是四步。先 `wget` 下 `tar.gz`,解压到 `/usr/local`(生成 `go/` 目录,官方推荐位置)。再把 `/usr/local/go/bin` 追加进 `$HOME/.profile` 的 PATH,source 后用 `go version` 验证。macOS 双击 `.pkg` 或同 Linux 用 `tar.gz`。Windows 双击 msi,默认装 `C:\Program Files\Go`,安装器自动设 GOPATH 并把 `Go\bin` 加进系统 Path。〔TB03 p3-7〕
- 多版本方法一:各版本装不同目录,切换时改 PATH 再 source,适合偶尔切换,状态靠人维护。〔TB03 p7-8〕
- 多版本方法二(官方下载器):先有一个标准 Go,把 $HOME/go/bin 加进 PATH,`go install golang.org/dl/go1.21.0@latest` 装专用下载器,`go1.21.0 download` 把该版本装到 $HOME/sdk/go1.21.0,之后用 `go1.21.0 version`、`go1.21.0 env GOROOT` 使用它。多版本共存交给官方工具,不靠手工 PATH,代价是"先有一个 Go 才能装第二个"。〔TB03 p8-10〕〔1.26 修正〕教材写 `go get golang.org/dl/...`,自 Go 1.17 起报错 `installing executables with 'go get' in module mode is deprecated`,入口命令换成 `go install ...@version`,下载器机制本身仍在(本机 go1.26.4 实测安装可用)。〔TB03 p15〕
- beta 与 tip 版走同一机制:`go install golang.org/dl/go1.17beta1@latest` + `go1.17beta1 download`。gotip 是滚动到最新提交的版本,一次提交就可能让构建失败,装不上是常态,不是你操作错了。〔TB03 p10-11〕
- Go 开箱即用,无需任何配置。配置项以环境变量形式存在,`go env` 查看、`go help environment` 看全部。本机 go1.26 新增了教材年代没有的 GOTOOLCHAIN(1.21 起,设 auto 时可按 go.mod 要求自动切换工具链)。〔TB03 p11-12〕

### TB27 函数调用链跟踪:defer、runtime 与 AST 注入

- 最简实现三行:`Trace(name string) func()` 打印 enter 后返回一个打印 exit 的闭包,`defer Trace("foo")()` 利用 defer 注册时求值、返回前执行的 LIFO 机制,得到 main→foo→bar→exit bar→exit foo→exit main 的完整链路。四个不足:函数名要手动传、多 goroutine 输出混杂、没有缩进层次、每个函数要手工加。〔TB27 p2-3〕
- 自动取函数名靠 runtime:`runtime.Caller(1)` 取调用者栈帧,返回 pc/文件/行号/ok 四值。参数是栈帧号,传 0 拿到的是 Caller 的调用者即 Trace 自己,要拿"谁调了 Trace"必须传 1。〔实测〕go1.26 下传 0 输出 main.Trace,传 1 才是 main.foo。〔TB27 p4-6〕
- `runtime.FuncForPC(pc).Name()` 把 pc 转成含包名的完整函数名(main.foo)。这个"统一的无参调用形式 defer Trace()()"正是后面自动注入能成立的前提:注入器往任何函数里塞同一行代码就行,不用关心函数名。〔TB27 p4-6〕
- Go 核心团队为防滥用故意不暴露 goroutine ID,但标准库 `net/http/h2_bundle.go` 里藏着取法。`runtime.Stack(b, false)` 拿当前栈,栈首行是 `goroutine 4707 [` 格式,TrimPrefix 后取第一个空格前的数字再 ParseUint。复制品改造两处:直接 make byte 切片替代从 pool 取,用 `strconv.ParseUint` 替代私有的 `http2parseUintBytes`。〔TB27 p6-7〕〔实测〕go1.26 上 main goroutine 取到 1,新 goroutine 取到 7,解析仍成立。依赖栈首行格式属实现细节,只在调试工具里用。〔TB27 p6-7〕
- 输出格式用 `fmt.Printf("g[%05d]: enter: [%s]\n", gid, name)`,%05d 左补零把 ID 对齐成 5 位。多 goroutine 交织时,把输出重定向到文件,按 gid grep 就能抽出单条调用链。〔TB27 p7-10〕
- 缩进层次用 `map[uint64]int` 存,以 gid 为 key:进入时取值输出 "->" 再 +1 存回,defer 闭包退出时取值输出 "<-" 再 -1。这张 map 会被多个 goroutine 同时读写,必须配 sync.Mutex(03 节讲过 map 并发写直接 fatal error)。〔TB27 p10-11〕
- trace 包独立成 module(go mod init github.com/bigwhite/instrument_trace)供外部导入。example_test.go 里 `ExampleTrace` 函数加 `// Output:` 注释,go test 扫描 Example 前缀函数、执行并比对注释里的预期输出。示例即单元测试,也是可执行的文档。〔TB27 p12-14〕
- AST 注入五步:go/parser 把源码解析成 AST,astutil.AddImport 加导入,遍历 f.Decls 逐个类型断言 *ast.FuncDecl,注入 defer 语句节点,最后 format.Node 转回源码。Go 把 ast 相关包随语言开放,这是官方标准库直接支持的源码改写路径。〔TB27 p17-18〕
- 注入必须幂等,否则工具跑两遍就把 defer 语句插两遍。addDeferStmt 先遍历函数体,对每条语句做类型断言链 DeferStmt→CallExpr→SelectorExpr→Ident,匹配 tracePkg+traceFunc 已存在就跳过。不存在才构造 DeferStmt 节点插到函数体首部。〔TB27 p18-20〕
- 工程结构按 03 节的布局惯例:cmd/instrument 放 main 包(flag 解析 -w,os.Args 取文件路径,校验 .go 扩展名),注入逻辑放 instrumenter 包。接口只声明一个方法 `Instrument(string) ([]byte, error)`,为换注入方式(注释指令、代码生成)预留。`-w` 写回原文件,不带则输出 stdout。〔TB27 p14-16〕
- 两条生产边界:注入跟踪代码只用于调试和阅读理解,不上生产,Trace 会被编译进产物。goroutine ID 同为核心团队不建议依赖的设施。手工删 Trace 繁琐易错,正解是思考题的 build tag 开关:config 目录下 dev.go 带 `//go:build dev` 实现真 Trace,prod.go 空实现,`go build -tags dev` 切换。〔TB27 p23-26〕

以上省去的:TB03 的三系统安装逐步命令与截图(照官方文档走即可,选读条目只留了目录与 PATH 两条要点)、用 go help 总结命令用法的思考题方向。TB27 的逐版完整源码清单(五版演进只讲机制与坑)与 ioutil.WriteFile 写回(该函数自 Go 1.16 起废弃,现行写法 os.WriteFile)。两单元其余必读条目均已融入本块。

## 想深入去哪查

- 第一步看知识点摘要（不用翻原材料）：`work/digest/TB06.md`（构建三部曲与 MVS）、`TB07.md`（六类依赖操作）、`TB05.md`（布局）、`TB08.md`（初始化次序）、`TB35b.md`（go.work、私有仓库与自建 goproxy）、`TBX11.md`（发布、broken 版本与 major 分支）、`TBX02.md`（1.17 剪枝、寄存器调用惯例、//go:build）。每条带页码，想核对原文再按页码翻 `work/text/` 或对应 PDF。
- 旧长版：`notes/p1-toolchain.md`（cargo 到 go 的映射）、`notes/p22-toolchain-docs.md`（pprof、lint、go.work、文档工程）。
