---
title: P20 · 写 CLI 工具：Go 的主场
---

# P20 · 写 CLI 工具：Go 的主场


> Go 的基本盘不是业务系统，是**工具**：kubectl、docker、hugo、
> terraform、gh 全是 Go 写的。你用 Rust 写过 dnsproxi/rg 类工具，
> 这章把"Go 写工具"的完整肌肉记忆铺开：读 stdin 管道、flag 解析、
> 遍历文件树、正确的退出码、工具的日志纪律，最后交叉编译发布。
>
> 读者：读完 P1.5（语法）+ P2.5（惯例）。前置：无硬依赖。
> 后续：P5 的 cobra 是本章"多子命令服务"形态的深潜版。

## 这章学完你能干什么

写出一个 Unix 气质的命令行工具：能进管道（stdin/stdout 不被日志
污染）、flag/子命令齐全、退出码符合惯例、文件树遍历带错误收集、
Ctrl-C 干净退出，`GOOS=... go build` 一发跨平台发布。练习终点：
`exercises/p20cli/` 全绿 + 亲手构建一个跨平台二进制。

五个问题：

1. 管道工具的第一纪律是什么（stdout 神圣，stderr 才是日志）？
2. flag 标准库够用到哪里，什么时候升级 cobra？
3. 遍历文件树的正确姿势（fs.WalkDir + 错误收集）？
4. 退出码怎么设计才配得上 shell 脚本调用？
5. 工具的日志该长什么样（stderr/quiet/--verbose）？

## 问题 1：stdout 神圣，stderr 才是日志

Unix 契约：**stdout 是数据接口，stderr 是人机界面**。管道工具把
日志打到 stdout 就毁了下游（`mytool | jq` 会把日志一起喂给 jq）。
Rust 用户熟悉 eprintln! 纪律，Go 一一对应：

```go
fmt.Println(data)          // stdout：数据，进管道
fmt.Fprintln(os.Stderr, "warn: ...")   // stderr：日志，给人看
slog.SetDefault(slog.New(slog.NewTextHandler(
    os.Stderr, nil)))      // 结构化日志默认走 stderr（P15 详述）
```

**检验标准**（工具设计的试金石）：

```sh
mytool data.json | jq .        # 纯数据过管道，日志不混入
mytool data.json > out.json    # 重定向拿到干净文件
```

第二条纪律：**别乱动 io.Writer 的默认值**。`fmt.Println` 永远
stdout；`log` 包默认 stderr 但带时间戳前缀（工具里通常被 slog
取代，P15）。库代码**接收 `io.Writer` 参数而不是自己摸 os.Stdout**：
调用方决定数据流向，测试也能注入 buffer（P2.5 的"接口在使用方"
的兄弟原则）。

**二进制输出默认值还有一层**：检测 tty 再决定要不要彩色/进度条
（deepsearch 的 `cmd/root.go` 就是范例：`isatty.IsTerminal(fd)` +
tint 彩色 handler，管道里自动裸文本）。

## 问题 2：flag 够用到哪，cobra 从哪开始

**flag 标准库**：单命令 + 少量开关，10 行起步：

```go
var (
    verbose = flag.Bool("v", false, "verbose output")
    limit   = flag.Int("limit", 10, "max items")
    output  = flag.String("o", "", "output file")
)

func main() {
    flag.Parse()                 // 解析 os.Args[1:]
    if flag.NArg() < 1 {         // 位置参数
        fmt.Fprintln(os.Stderr, "usage: mytool [-v] [-limit N] <file>")
        os.Exit(2)
    }
}
```

flag 包还支持 `-flag=value` 和 `-flag value` 两种姿势，`h`/`help`
自动处理。**对比 clap**：没有 derive，没有自动生成 completion，
没有环境变量绑定，全是手动。小工具够用。

**升级 cobra 的信号**（deepsearch 的 `cmd/` 就是完整形态）：

| 信号 | 例 |
|---|---|
| 需要子命令 | `deepsearch serve` / `deepsearch fetch` |
| flag 要跨子命令共享 | persistent flag 挂 root |
| 要 shell completion / man page | `cobra.GenBashCompletion` 内置 |
| help 要分组、要彩色 | cobra 自带 |

cobra 的形状（对照 clap 的 derive 风格）：

```go
var rootCmd = &cobra.Command{
    Use:   "mytool",
    Short: "one-line description",
    PersistentPreRun: func(cmd *cobra.Command, args []string) {
        // 全局初始化：日志、配置（所有子命令共享）
    },
}
var serveCmd = &cobra.Command{
    Use: "serve",
    RunE: func(cmd *cobra.Command, args []string) error {
        return runServe()      // RunE 返回 error：cobra 负责打印 + 退出码 1
    },
}
func init() { rootCmd.AddCommand(serveCmd) }
```

选型口径：**≤2 个 flag 的单发工具用 flag；要长成服务/全家桶的直接
cobra**（别用 flag 写到一半迁移，代价高于起步）。中间态还有
`spf13/pflag`（POSIX 风格长 flag，cobra 底座）。

## 问题 3：fs.WalkDir 遍历文件树

`io/fs.WalkDir`（1.16+）是标准答案，比 `filepath.Walk` 快（不
无谓 stat）且能遍历内存文件系统（embed/embed.FS 同接口）：

```go
var errs []error
err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
    if err != nil {
        errs = append(errs, fmt.Errorf("%s: %w", path, err))
        return nil             // 记录但继续：一个权限问题不该杀死全树遍历
    }
    if d.IsDir() && d.Name() == ".git" {
        return fs.SkipDir      // 剪枝：跳过整个子树
    }
    if !d.IsDir() && strings.HasSuffix(path, ".go") {
        matches = append(matches, path)
    }
    return nil
})
```

三个要点：**回调里 return err 等于中止全树**（通常想要的是收集后
汇总退出码，不是中止）；`fs.SkipDir` 只剪当前目录；`err != nil`
分支是"这层访问失败"的通知，不是中止信号。对照 Rust 的
`walkdir` crate：`filter_map(|e| e.ok())` 的"吞错继续"语义在这里
要显式写出来。

文件读写三件套（工具最高频）：

```go
data, err := os.ReadFile(path)        // 小文件一步到位
os.WriteFile(path, data, 0o644)       // 权限显式写（0o 前缀，1.13+）
f, err := os.Open(path); defer f.Close()
scanner := bufio.NewScanner(f)        // 逐行：默认 64KB 行长上限
scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)  // 长行要调大
```

`Scanner.Err()` 必查：**行太长/读出错时 Scan() 返回 false 但不报
错**，漏查就是静默截断（数据类工具的大忌）。

## 问题 4：退出码是 shell 的 API

CLI 的返回值就是退出码。惯例（Sysexits + Unix 传统）：

| 码 | 含义 | 触发 |
|---|---|---|
| 0 | 成功 | 正常 return |
| 1 | 一般错误 | 运行时失败（cobra RunE 默认） |
| 2 | 用法错误 | 参数错（flag 包解析失败自动 exit 2） |
| 126/127 | 无权限/命令不存在 | shell 层，工具不用管 |

**没有 `Result<T, E>`，main 的错误传播靠显式 if**：

```go
func main() {
    if err := run(); err != nil {
        fmt.Fprintln(os.Stderr, "error:", err)
        os.Exit(1)
    }
}

func run() error {   // 业务逻辑全在 run：main 只管退出码
    ...
}
```

`run() error` + main 薄壳是全社区的标准形状（stdlib testmain 也是
这么写的）：main 不写业务，错误一处收口，退出码一处决定。多错误
汇总用 `errors.Join`（P2.5 问题 3），退出码非零即可，消息合并输出。

**别在库代码里 os.Exit**：退出是 main 的专属权力。库层 panic 都要
收敛成 error（P1.5 §9 边界），exit 更要。

## 问题 5：工具的日志纪律

服务日志（P15/P16）和工具日志不是一个物种。工具日志四条：

1. **默认安静**：成功时零输出（Unix 静默美德，`ls` 成功时说什么了
   吗）；要报告用 `--verbose`/`-v` 放开，slog Level 用 flag 调。
2. **stderr 永远**：包括 `--verbose` 的调试输出（问题 1）。
3. **进度要可管道**：长任务进度条走 stderr + 检测 tty（`isatty`），
   非 tty 降级成纯文本或不输出。字节估算类进度可直接
   `fmt.Fprintf(os.Stderr, "\r%d/%d")`，上库用
   `schollz/progressbar` 或 `charmbracelet` 系（P19 表格延伸）。
4. **--quiet 是给脚本的**：`-q` 压掉一切非数据输出；工具组合
   `toolA -q | toolB` 是常见形态。

对照 Rust：`clap-verbosity-flag` + `env_logger`/`tracing` 那套
默认 warn、`-vvv` 递增的习惯完全可移植，用 slog + Level flag 实现：

```go
lvl := slog.LevelWarn
if *verbose { lvl = slog.LevelInfo }
if *debug   { lvl = slog.LevelDebug }   // -v 计数递增同理
slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr,
    &slog.HandlerOptions{Level: lvl})))
```

## 动手：练习 p20cli（30-45 分钟）

```sh
go test ./exercises/p20cli/ -v
```

练习包是一个微型管道工具 `wcgo`（统计文本行数/词数），覆盖正文五个问题：

1. `TestCountReader`：从 `io.Reader` 统计（**不摸 os.Stdin**，
   测试注入 bytes.Buffer，验证问题 1 的可测试性红利）。
2. `TestParseFlags`：flag 解析成 config struct（对照 Rust
   `Args::parse`），非法参数返回 error（不是直接 exit）。
3. `TestExitCode`：`ExitCode` 把"文件不存在/权限/用法"映射到
   退出码（1/1/2），`errors.Is` 穿透 `%w` 链识别用法错。
4. `TestWalkCollect`：WalkDir 遍历 + 收集错误 + `.git` 剪枝，断言
   部分失败时结果仍完整、错误清单非空。
5. `TestScanLongLine`：故意造 1MB 长行，验证不调 `scanner.Buffer`
   会静默截断，调了才对（问题 3 的坑直接踩一遍）。
6. `TestRunPipeline`：stdin/文件两模式 + `-q` 静默 + 坏文件收集
   继续（问题 1 的"输出只进注入的 writer"验收）。
7. `TestErrsCombineExtra`：钉住教学包里简化版 `errsCombine` 的行为
   （空列表返回 nil，多个错误计数并带首个），完整的 `errors.Join`
   版本在问题 4。

**观察题**（终端里跑，测试没法替你感受）：

```sh
go run ./exercises/p20cli/cmd/ README.md          # 看输出
go run ./exercises/p20cli/cmd/ README.md | wc -l  # 管道下数据干净
go run ./exercises/p20cli/cmd/ /nonexistent; echo "exit=$?"  # exit=1
go run ./exercises/p20cli/cmd/; echo "exit=$?"               # exit=2 用法错
```

**改造题**：给 `wcgo` 加 `-l`（只数行）flag + `--verbose` 日志
开关，`go run ... -v README.md` 看日志走 stderr（用
`2>/dev/null` 验证 stdout 数据不丢）。

## Take-away

1. stdout 是数据接口，stderr 是人机界面；库代码收 `io.Writer`
   参数，不摸 os.Stdout。
2. 单发工具 flag 起步；要子命令/completion 直接 cobra，别半路迁移。
3. WalkDir 回调收集错误继续走，`fs.SkipDir` 剪枝；Scanner 必查
   `Err()`，默认 64KB 行长上限会静默截断。
4. 退出码是 shell 的 API：0 成功 / 1 运行错 / 2 用法错；
   `run() error` + main 薄壳一处收口。
5. 工具日志默认安静走 stderr，`-q` 给脚本，`-v` 给人，tty 检测管
   彩色和进度条。

---

## 章末参考

### 交叉编译与发布

```sh
# 三平台一发（CGO_ENABLED=0 保纯静态，scratch 镜像/无依赖机器直接跑）
CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/wcgo-linux-amd64 ./cmd/
CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o dist/wcgo-darwin-arm64 ./cmd/
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/wcgo.exe   ./cmd/
```

版本注入（ldflags 是编译期常量的官方通道，对照 Rust `env!` +
build.rs）：

```go
var version = "dev"   // 包级变量，ldflags 覆盖
// -ldflags="-X main.version=1.2.3"
```

自动化发布用 **goreleaser**：一个 yaml 声明矩阵（os/arch/打包），
打 tag 自动出 GitHub Release + 校验和 + Homebrew tap 更新。对照
Rust 的 `cargo-dist`，同赛道同思路。

### init 系统与常驻形态

| 形态 | 要点 |
|---|---|
| systemd unit | `Type=exec` + `ExecStart=/usr/local/bin/tool`；日志交 journald，工具侧只管 slog JSON 到 stdout |
| launchd（macOS） | plist `RunAtLoad`；日志重定向文件 |
| 容器 | `FROM scratch` + 二进制（P1 编译模型）；PID 1 收信号要自己处理（SIGTERM 不处理，k8s 等 30s 后 SIGKILL） |

常驻进程 = 问题 1-5 全套 + signal.NotifyContext 优雅关停
（P8 问题 4 的 `Shutdown(ctx)` 链路）。

### 读 stdin 的三形态

```go
// 1. 全量（小输入）
data, _ := io.ReadAll(os.Stdin)
// 2. 逐行（流式，最常用）
sc := bufio.NewScanner(os.Stdin); for sc.Scan() { line := sc.Text() }
// 3. 结构化 JSON 流（NDJSON 工具）
dec := json.NewDecoder(os.Stdin); for dec.More() { var v T; dec.Decode(&v) }
```

管道没有文件参数时的惯例：`flag.NArg() == 0 && !isatty(stdin)`
→ 读 stdin；`isatty(stdin)` 且无参数 → 打 usage 退出 2（对照
`grep`/`jq` 的行为）。

### Go 工具生态代表（读源码学惯例）

| 工具 | 学什么 |
|---|---|
| kubectl | cobra 大型化：命令树/生成器/补全体系 |
| gh（cli/cli） | go-gh 库封装、配置层次、ISO 日期等人性化输出 |
| hugo | 单二进制的极致：全静态资源 embed（`embed.FS`） |
| jc / jq 系 | 管道过滤器形状：stdin JSON → stdout JSON |
| tailscale | 常驻服务的日志/诊断子命令设计（`tailscale debug`） |
