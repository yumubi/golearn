# P23 · Linux 运行时：进程、信号、fd 与容器

> 这章学完你能干什么：解释 Ctrl-C 到优雅关停之间 OS 层发生的每一步；
> 用 `lsof` 读一个进程的 fd 表；把 httpkv 塞进容器并解释"为什么
> SIGTERM 不处理会等 30 秒被 SIGKILL"。骨架：把 httpkv 从裸跑跑到
> `docker run`。读者：读完 P8（httpkv 就是这章的道具）。前置：无。
>
> 实战说明：本机是 macOS（本教程开发机），`strace`/`cgroup` 部分
> 给 Linux 命令与预期输出，其余全部实拍。

## 体系背景：你的 Go 进程在 OS 眼里是什么

Rust 程序员对这层的直觉是现成的：std::process::Command 起的进程、
libc 的 signal、std::os::fd，和 Go 共用同一个 OS 接口面。语言差异
在"谁替你包装"：Go runtime 把信号 goroutine 化、把 cgroup 读进
GOMAXPROCS；Rust 要靠 crate（signal-hook、automaxprocs 的对应物）
或手写。

`go build` 的产物是一个普通的可执行文件。OS 运行它时给三样东西：
**一个 PID、一张 fd 表、一组信号处置**。进程活着 = 内核任务结构
存在；"读文件/开端口" = 内核往 fd 表里加一行；"被 Ctrl-C" = 内核
往这个任务投一个 SIGTERM/SIGINT。

Go 程序和 OS 的全部交互就这三样。反过来，运维世界的核心动作
（kill、ulimit、容器限额、容器健康检查）也全是这三样的包装。
这章把包装拆开。

## 进程：PID 与退出码

```sh
$ go build -o /tmp/httpkv ./exercises/p8fast/httpkv/
$ /tmp/httpkv &
$ pgrep -f "^/tmp/httpkv"
91736                     # ← OS 分配的 PID
$ echo $?
0
```

每个进程有父进程（PPID）。shell 启动的程序，父就是 shell；容器里
PID 1 是 ENTRYPOINT 那个进程。**PID 1 特殊**：内核不给它默认信号
处置，没人装 handler 的 PID 1 收到 SIGTERM 什么都不做。这就是
"容器里 Ctrl-C 没反应 / docker stop 卡 30 秒"的根源。

`$?` 读的 8 位退出码（P20 讲过 0/1/2 惯例）：正常退出是 `exit(n)`，
被信号杀死是 128+信号号（`kill -9` 后 `$?` = 137）。

## 信号：SIGTERM 送达之后

信号是内核发往进程的异步通知。默认处置四种：终止、终止+core、
忽略、停止。SIGTERM/SIGINT 的默认处置是**直接终止，Go 代码一行都
不会执行**，所以优雅关停必须先劫持：

```go
// exercises/p8fast/httpkv/main.go:173
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()
// ... srv.ListenAndServe()
<-ctx.Done()          // 收到 SIGTERM：NotifyContext 把 ctx cancel
logger.Info("shutting down")
shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
srv.Shutdown(shutdownCtx)   // 停止接新连接，等在途请求收尾
```

（Rust 侧对照：signal-hook 的 flag 位是同样思路，信号只置位，
主循环轮询；tokio::signal 干脆把接收做成 stream。套路相同。）

真机实证（kill -TERM 后进程自己打出日志、端口释放）：

```sh
$ /tmp/httpkv > /tmp/httpkv.log 2>&1 &
$ kill -TERM $(lsof -ti :8090)
$ grep shutting /tmp/httpkv.log
{"level":"INFO","msg":"shutting down"}
$ lsof -ti :8090 | wc -l
0                        # ← 端口干净释放
```

时间线：SIGTERM 由内核投递 → Go runtime 的信号 handler 把它转成
`ctx.Done()` 就绪 → main 从 `<-ctx.Done()` 醒来 → `Shutdown` 排空
在途请求 → 进程以 0 退出。**信号处理 goroutine 化**是 Go 的独特
设计（C 里 handler 只能调 async-signal-safe 函数）。

两条铁律：不捕获 SIGKILL/SIGSTOP（内核保留，容器强杀就是它）；
信号只发通知，清理逻辑写在主流程里（`Shutdown`），不要塞 handler。

## fd：一切 I/O 的把手

Unix 的"一切皆文件"落到实处就是 fd 表：0=stdin、1=stdout、
2=stderr，之后每开一个 socket/文件/pipe 递增。看 httpkv 的：

```sh
$ lsof -p $(pgrep -f "^/tmp/httpkv") | awk '$4 ~ /^[0-9]+[uwr]$/ {print $4, $5}' | head -6
0u  CHR      # stdin：终端
1w  REG      # stdout：重定向到的日志文件
2w  REG      # stderr：日志文件（stdout 同一文件）
...
```

```sh
$ lsof -p <pid> | grep -c TCP
2                        # :8090 监听 + :6060 pprof 监听（P22 的端点）
```

和 Go 代码对上：`net.Listen` → socket fd；`os.Open` → 文件 fd；
`log/slog` 写 os.Stderr → fd 2。**"日志进 stderr"（P20）在 OS 层
就是写 fd 2**，docker 的日志驱动收的正是容器的 fd 1/2 字节流。

fd 泄漏是长进程的经典死法：每请求开文件不关，fd 表涨到
`ulimit -n`（默认 256/1024），再 open 就是 EMFILE。Go 的
`defer f.Close()` 和连接池都是防它的。

## 容器：隔离的三件套

容器不是轻量虚拟机，是三样内核机制的包装：**namespace**（看见
什么：PID/网络/挂载点各自独立）、**cgroup**（能用多少：CPU/内存
限额）、**联合文件系统**（镜像分层，P25 展开）。

httpkv 容器化全程（真跑）：

```sh
# 1. 交叉编译：静态 ELF，不依赖任何 so
$ CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
  go build -trimpath -ldflags="-s -w" -o /tmp/httpkv ./exercises/p8fast/httpkv/
$ file /tmp/httpkv
/tmp/httpkv: ELF 64-bit LSB executable, ARM aarch64

# 2. 镜像：alpine 垫底 + 一个二进制（多阶段构建见 P25）
$ docker build -t golearn-httpkv -f - . <<'EOF'
FROM alpine:3.22
COPY httpkv /usr/local/bin/httpkv
USER nobody
EXPOSE 8090
ENTRYPOINT ["/usr/local/bin/httpkv"]
EOF

# 3. 跑：真机实拍
$ docker run -d --name golearn-httpkv -p 18090:8090 golearn-httpkv
$ curl -s -X PUT -d '{"Value":"in-container","TTL":0}' localhost:18090/kv/k
{"value":"in-container","at":"2026-10-13T06:44:45.399356555Z"}
$ docker exec golearn-httpkv id
uid=65534(nobody) gid=65534(nobody)   # ← USER nobody 生效，不跑 root
```

`docker stop` 对 PID 1 发 SIGTERM，httpkv 的信号链在容器里照常
工作：

```sh
$ docker stop golearn-httpkv
$ docker logs golearn-httpkv 2>&1 | tail -1
{"level":"INFO","msg":"shutting down"}   # ← 优雅关停在容器里原样成立
```

反例：如果 main 不装 handler（没有 `signal.NotifyContext`），PID 1
无视 SIGTERM，docker stop 等满 30 秒宽限期后发 SIGKILL（`$?`=137）
（在途请求全断）。这就是云原生清单里"必须处理 SIGTERM"的 OS 依据。

## cgroups：限额与 GOMAXPROCS

cgroup 是内核的资源记账与限额单元。容器 CPU limit=2 就是把进程
放进 `cpu.max=200000/100000` 的 cgroup。Go 1.25 起 runtime 自动
读 cgroup 限额设 GOMAXPROCS（`containermaxprocs`，P22 的 GODEBUG
表里有）；之前的时代要靠 automaxprocs 库手动做，否则 GOMAXPROCS
=宿主核数，限了 CPU 的容器里调度器还在开满核数的 P，延迟毛刺。

对照 Linux 机器上验证（本教程 T4 实测过，此处给命令）：

```sh
$ cat /sys/fs/cgroup/cpu.max        # 容器内
200000 100000                       # 2 核
$ go run -exec="env GODEBUG=containermaxprocs=1" -r '.*' main.go
$ GOMAXPROCS gomaxprocs=2           # runtime 自动取限额
```

macOS 无 cgroups，容器里才有；这也是"本机没毛病、进容器毛刺"
这类问题的排查入口：先查限额和 GOMAXPROCS 是否对齐。

## Take-away

1. 进程 = PID + fd 表 + 信号处置；退出码是父进程读的 8 位结果，
   被信号杀死是 128+信号号。
2. 默认信号处置直接杀进程，优雅关停 = `signal.NotifyContext` 把
   信号变成 ctx cancel，清理逻辑在主流程。
3. fd 表是全部 I/O 的把手；日志进 stderr = 写 fd 2 = 容器日志
   驱动的数据源；fd 泄漏到 ulimit 是长进程经典死法。
4. 容器 = namespace（看见什么）+ cgroup（能用多少）+ 联合文件
   系统；PID 1 不装 handler 就收不到 SIGTERM，docker stop 30 秒
   后 SIGKILL。
5. Go 1.25+ 自动按 cgroup CPU 限额设 GOMAXPROCS；"本机好容器差"
   先查限额对齐。

## 练习：跟着做（P23 无练习包，实战就是练习）

**预测**（跑之前写答案；预测错的地方才是认知边界）：

1. `kill -9` 后 `$?` 是多少？`kill -15` 后呢（httpkv 已装
   handler）？
2. `docker stop` 的宽限期参数是哪个 flag？改成 2 秒后再 stop，
   不装 handler 的进程会怎样？

**观察**（全部真跑）：

```sh
go build -o /tmp/httpkv ./exercises/p8fast/httpkv/ && /tmp/httpkv &
lsof -p $(lsof -ti :8090)          # 读 fd 表：找出 :8090 和 :6060 两行
kill -TERM $(lsof -ti :8090)       # 看 shutting down 日志 + 端口释放
```

**改造**：给 httpkv 加 `--version` flag（P20 的 ldflags 注入），
重打镜像，`docker run golearn-httpkv --version` 验证。

## 章末参考

### 信号速查

| 信号 | 号 | 默认 | 用途 |
|---|---|---|---|
| SIGINT | 2 | 终止 | Ctrl-C |
| SIGTERM | 15 | 终止 | kill 默认、docker stop |
| SIGKILL | 9 | 终止（不可捕获） | 强杀、`$?`=137 |
| SIGHUP | 1 | 终止 | 终端断开；daemon 惯例"重读配置" |
| SIGPIPE | 13 | 忽略（Go） | 写端管道断（Go runtime 特殊处理） |

### Linux 诊断命令（macOS 对照）

| 干什么 | Linux | macOS |
|---|---|---|
| 系统调用跟踪 | `strace -f -p <pid>` | 无（SIP；dtruss 需关 SIP） |
| fd 表 | `ls /proc/<pid>/fd` | `lsof -p <pid>` |
| TCP 状态 | `ss -tan` | `netstat -an -p tcp` |
| cgroup 限额 | `cat /sys/fs/cgroup/cpu.max` | 容器内才有 |

### Rust 对照总表

| 主题 | Go | Rust | 差异要点 |
|---|---|---|---|
| 信号 | signal.NotifyContext | signal-hook / tokio::signal | 同样 goroutine/async 化 |
| 退出码 | os.Exit / 128+n | std::process::exit | 同一 shell 惯例 |
| 静态二进制 | CGO_ENABLED=0 默认路径 | musl target 要配 | Go 开箱即容器友好 |
| cgroup 对齐 | 1.25+ runtime 自动 | tokio 无自动（要自己读） | Go 生态先解决 |
| fd 表排查 | lsof | lsof（同一工具） | 内核视角无语言差异 |
| 容器镜像 | distroless/alpine 垫底 | rust 官方镜像 + musl | Rust 二进制更小，Go 构建更快 |
