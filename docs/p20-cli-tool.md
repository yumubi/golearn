---
title: P20 · 写 CLI 工具：Go 的主场
---

# P20 · 写 CLI 工具：Go 的主场


> 这章学完你能干什么：写一个 Unix 气质的命令行工具，能进管道
> （stdout 不被日志污染）、flag/子命令齐全、退出码符合惯例、文件树
> 遍历带错误收集，`GOOS=... go build` 一发跨平台发布。
> 读者：读完 P1.5 + P2.5。前置：无硬依赖。后续：P5 的 cobra 是
> 本章"多子命令服务"形态的深潜版。
>
> 动手方式：练习终点 `exercises/p20cli/` 全绿 + 亲手 build 一个
> 跨平台二进制。

## 体系背景：fd 0/1/2 与退出码

CLI 工具的整个礼仪，来自 Unix 两条老规矩：

1. **每个进程自带三个文件描述符**：fd 0（stdin）、fd 1（stdout）、
   fd 2（stderr）。管道 `a | b` 接的是 a 的 fd 1 到 b 的 fd 0。
   所以"日志别走 stdout"不是风格问题，是数据通道安全问题。
2. **进程退出时给父进程（shell）留一个 8 位退出码**：`$?` 读的就是
   它。0 表示成功，非 0 是错误分类。`&&`/`if` 都靠它决策。退出码
   是 CLI 的 API，比 flag 更早被调用方依赖。

Go 常用于构建单二进制 CLI（kubectl、docker、hugo、gh），并支持通过 `GOOS`、`GOARCH` 交叉编译。是否静态链接以及交叉编译是否需要额外工具，取决于 CGO、依赖和目标平台。

## stdout 神圣，stderr 才是日志

Unix 契约：**stdout 是数据接口，stderr 是人机界面**。日志打到
stdout 就毁了下游（`mytool | jq` 会把日志一起喂给 jq）：

```go
fmt.Println(data)                       // stdout：数据，进管道
fmt.Fprintln(os.Stderr, "warn: ...")   // stderr：日志，给人看
slog.SetDefault(slog.New(slog.NewTextHandler(
    os.Stderr, nil)))                   // 结构化日志默认走 stderr
```

检验标准（工具设计的试金石）：

```sh
$ mytool data.json | jq .        # 纯数据过管道，日志不混入
$ mytool data.json > out.json    # 重定向拿到干净文件
```

第二条纪律：**库代码接收 `io.Writer` 参数，不自己摸 os.Stdout**。
调用方决定数据流向，测试也能注入 buffer（P2.5"接口在使用方"的
兄弟原则）。彩色/进度条要先检测 tty：`isatty.IsTerminal(fd)`，
管道里自动裸文本（deepsearch 的 `cmd/root.go` 就是范例）。

## flag 起步，cobra 升级

**flag 标准库**：单命令 + 少量开关，10 行起步：

```go
var (
    verbose = flag.Bool("v", false, "verbose output")
    limit   = flag.Int("limit", 10, "max items")
)

func main() {
    flag.Parse()                 // 解析 os.Args[1:]
    if flag.NArg() < 1 {         // 位置参数
        fmt.Fprintln(os.Stderr, "usage: mytool [-v] [-limit N] <file>")
        os.Exit(2)
    }
}
```

支持 `-flag=value` 和 `-flag value`，`h`/`help` 自动处理。对比
clap：没有 derive、没有自动 completion、没有环境变量绑定，全是
手动。小工具够用。

**升级 cobra 的信号**（deepsearch 的 `cmd/` 是完整形态）：

| 信号 | 例 |
|---|---|
| 需要子命令 | `deepsearch serve` / `deepsearch fetch` |
| flag 跨子命令共享 | persistent flag 挂 root |
| 要 shell completion | `cobra.GenBashCompletion` 内置 |

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
        return runServe()      // 将错误交给 Execute 的调用方处理
    },
}
func init() { rootCmd.AddCommand(serveCmd) }
```

简单单命令可先用 flag；子命令、共享 flag 或补全需求出现时，再评估 cobra。flag 数量不是硬门槛，迁移成本也应结合项目规模判断。调用 `rootCmd.Execute()` 的 main 仍须根据返回错误决定退出码。

## fs.WalkDir 遍历文件树

`io/fs.WalkDir`（1.16+）是标准答案，比 `filepath.Walk` 快（不无谓
stat）且能遍历内存文件系统（embed.FS 同接口）：

```go
var errs []error
err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
    if err != nil {
        errs = append(errs, fmt.Errorf("%s: %w", path, err))
        return nil             // 记录但继续：一个权限问题不该杀死全树
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

三个要点：**回调里 return err 等于中止全树**（通常要的是收集后
汇总退出码）；`fs.SkipDir` 只剪当前目录；`err != nil` 分支是"这层
访问失败"的通知，不是中止信号。对照 Rust walkdir 的
`filter_map(|e| e.ok())` 吞错继续：Go 这里要显式写出来。

文件读写三件套（工具最高频）：

```go
data, err := os.ReadFile(path)        // 小文件一步到位
os.WriteFile(path, data, 0o644)       // 权限显式写
scanner := bufio.NewScanner(f)        // 逐行：默认 64KB 行长上限
scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)  // 长行要调大
```

`Scanner.Err()` 必查：**行太长/读出错时 `Scan()` 返回 false 但不
报错**，漏查就是静默截断（练习里专门踩一遍）。

## 退出码是 shell 的 API

| 码 | 含义 | 触发 |
|---|---|---|
| 0 | 成功 | 正常 return |
| 1 | 一般错误 | 运行时失败（cobra RunE 默认） |
| 2 | 用法错误 | 参数错（flag 包解析失败自动 exit 2） |

没有 `Result<T, E>`，main 的错误传播靠显式 if：

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

`run() error` + main 薄壳是全社区的标准形状：main 不写业务，错误
一处收口，退出码一处决定。多错误汇总用 `errors.Join`（P2.5），
退出码非零即可。**别在库代码里 os.Exit**：退出是 main 的专属权力。

## 工具的日志纪律

服务日志（P15/P16）和工具日志不是一个物种。四条：

1. **区分结果与诊断**：查询工具成功时通常要在 stdout 输出结果；没有结果可报告的操作则可以默认安静。详细诊断可用 `--verbose`/`-v` 放开。
2. **stderr 永远**：包括 `--verbose` 的调试输出。
3. **进度要可管道**：长任务进度走 stderr + 检测 tty，非 tty 降级
   纯文本或不输出。
4. **--quiet 是给脚本的**：`-q` 压掉一切非数据输出；
   `toolA -q | toolB` 是常见形态。

```go
lvl := slog.LevelWarn
if *verbose { lvl = slog.LevelInfo }
if *debug   { lvl = slog.LevelDebug }
slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr,
    &slog.HandlerOptions{Level: lvl})))
```

## 动手：练习 p20cli（30-45 分钟）

```sh
go test ./exercises/p20cli/ -v
```

练习包是微型管道工具 `wcgo`（统计行数/词数），六道题：

1. `TestCountReader`：从 `io.Reader` 统计（**不摸 os.Stdin**，
   测试注入 buffer，验证可测试性红利）。
2. `TestParseFlags`：flag 解析成 config struct（对照 Rust
   `Args::parse`），非法参数返回 error（不是直接 exit）。
3. `TestExitCode`：`ExitCode` 把错误映射到退出码，`errors.Is`
   穿透 `%w` 链识别用法错。
4. `TestWalkCollect`：WalkDir 遍历 + 收集错误 + `.git` 剪枝，断言
   部分失败时结果仍完整。
5. `TestScanLongLine`：故意造 512KB 长行，验证不调 `bufio.Scanner.Buffer`
   会静默截断，调了才对。
6. `TestRunPipeline`：stdin/文件两模式 + `-q` 静默 + 坏文件收集
   继续。`TestErrsCombineExtra` 钉住汇总行为：空列表 nil、计数
   + 首个错误（完整合并见 P2.5 的 `errors.Join`）。

**预测题**（跑之前写答案；猜错的地方才是认知边界）：

1. `go run ./exercises/p20cli/cmd/ --bogus` 的退出码是多少？
   build 成二进制再跑呢？（两者不一样，先猜再验。）
2. `wcgo` 读一个 512KB 的单行文件（不带 `-l`），不调
   `Scanner.Buffer` 时输出什么？报错还是给结果？
3. `wcgo /nonexistent 2>/dev/null | wc -c` 输出几个字节？错误
   信息去哪了？
4. WalkDir 遇到无权限目录，`TestWalkCollect` 断言的结果是"部分
   结果 + 错误"还是"整体失败、零结果"？

**观察题**（终端里跑，测试没法替你感受）：

```sh
go run ./exercises/p20cli/cmd/ README.md          # 看输出
go run ./exercises/p20cli/cmd/ README.md | wc -l  # 管道下数据干净
go build -o /tmp/wcgo ./exercises/p20cli/cmd/ && /tmp/wcgo --bogus; echo "exit=$?"  # exit=2
/tmp/wcgo /nonexistent; echo "exit=$?"            # exit=1
```

（`go run` 包装器会吃掉真实退出码，必须 build 后验。）

**改造题**：给 `wcgo` 加 `-l`（只数行）flag + `--verbose` 日志
开关，`2>/dev/null` 验证 stdout 数据不丢。

## 小结

1. stdout 是数据接口，stderr 是人机界面（fd 1/2 分工）；库代码
   收 `io.Writer`，不摸 os.Stdout。
2. 单发工具 flag 起步；要子命令/completion 直接 cobra，别半路迁移。
3. WalkDir 回调可收集错误继续走，`fs.SkipDir` 剪枝；Scanner 必查 `Err()`，默认约 64KB 的 token 上限超出后会报错，忽略错误才会造成静默截断。
4. 退出码是 shell 的 API：0/1/2；`run() error` + main 薄壳一处
   收口，库代码永远不 os.Exit。
5. 工具日志默认安静走 stderr，`-q` 给脚本，`-v` 给人，tty 检测管
   彩色和进度条。

## 章末参考

### 交叉编译与发布

```sh
CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/wcgo-linux-amd64 ./cmd/
CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o dist/wcgo-darwin-arm64 ./cmd/
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/wcgo.exe   ./cmd/
```

版本注入（ldflags 是编译期常量的官方通道）：

```go
var version = "dev"   // 包级变量，ldflags 覆盖
// -ldflags="-X main.version=1.2.3"
```

自动化发布用 **goreleaser**：一个 yaml 声明矩阵，打 tag 自动出
GitHub Release + 校验和 + Homebrew tap 更新。对照 Rust 的
`cargo-dist`。

### init 系统与常驻形态

| 形态 | 要点 |
|---|---|
| systemd | `Type=exec`；日志交 journald，工具侧 slog JSON 到 stdout |
| launchd（macOS） | plist `RunAtLoad`；日志重定向文件 |
| 容器 | `FROM scratch` 需要兼容的自包含二进制及必要运行资源；PID 1 应明确处理 SIGTERM 并执行清理，具体停止期限由运行环境配置 |

常驻进程 = 本章全套 + signal.NotifyContext 优雅关停
（P8 的 `Shutdown(ctx)` 链路）。

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
| gh（cli/cli） | go-gh 库封装、配置层次、人性化输出 |
| hugo | 单二进制的极致：全静态资源 embed |
| tailscale | 常驻服务的日志/诊断子命令设计 |

### Rust 对照总表

| 主题 | Go | Rust | 差异要点 |
|---|---|---|---|
| 参数解析 | flag（手动）→ cobra | clap derive | Go 无宏，升级路径是换库 |
| 退出码 | os.Exit（main 专属） | std::process::exit / main 返回 | 同一套 shell 惯例 |
| 遍历 | fs.WalkDir + 错误收集 | walkdir + filter_map | Go 显式收集，Rust 惯吞错 |
| 日志通道 | slog → os.Stderr | tracing → stderr | 同一 fd 纪律 |
| 彩色 | isatty 手动检测 | atty/owo-colors | 同一 tty 检测思路 |
| 发布 | goreleaser | cargo-dist | 同赛道同思路 |
