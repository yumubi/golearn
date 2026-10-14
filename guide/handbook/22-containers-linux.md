# 22 容器与 Linux 运行时

> 详细程度：速通 · 约 15 分钟
> 材料：旧章 `notes/p23-linux-runtime.md`、`notes/p25-containers-cloud.md`（本节命令已在本机复核的会标明，容器相关的标"未在本机验证"）
> 前置：06 并发一、07 并发二（context 与优雅退出）

## 这一节要建立的认知

容器里的 Go 进程，在操作系统眼里只是一个普通进程，外面套了三层东西：命名空间（它看得见什么）、cgroup（它能用多少）、镜像文件系统（它的根目录是什么）。要关心的只有四件事：**怎么收到停止信号、怎么用文件描述符、被限了多少 CPU 和内存、编排器怎么判断它活着**。

| 问题 | Java | Rust | Go |
|---|---|---|---|
| 信号默认行为 | JVM 注册了 shutdown hook | 默认终止，要用 signal-hook 或 tokio::signal | 默认终止，用 `signal.NotifyContext` 转成 `ctx.Done()` |
| 容器里的 CPU 感知 | JVM 读 cgroup | 线程数通常由代码或运行时库决定 | Go 1.25 起 runtime 读 cgroup CPU 限额 |
| 交付物 | jar 加 JRE 镜像 | 静态二进制 | 静态二进制，镜像可以只有几十 MB |

## 一、信号与优雅关停

`SIGTERM` 和 `SIGINT` 的默认处置是直接终止进程，**Go 代码一行都不会执行**（这是普通进程的行为；容器内 PID 1 的信号行为本机未验证）。优雅关停必须先捕获信号，把它转成 context 的取消：

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()

srv := &http.Server{Addr: "127.0.0.1:18222", Handler: mux}
go srv.ListenAndServe()

<-ctx.Done() // 收到信号
sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
err := srv.Shutdown(sctx) // 停接新连接，等在途请求收尾
```

我在本机写了一个 `/slow` 接口（睡 2 秒）验证：请求发出后立刻 `kill -TERM`，客户端仍然收到了 `done`，进程打印 `got signal, draining` 与 `shutdown err: <nil>` 后退出。对照组 `kill -9`：客户端拿到连接错误（状态码 000），进程什么都没打印。

两条规则：

1. `SIGKILL` 和 `SIGSTOP` 不能被捕获。`docker stop` 先发 `SIGTERM`，超时（默认 10 秒）才发 `SIGKILL`，所以你的 `Shutdown` 超时要小于这个窗口。（docker stop 的默认超时未在本机验证）
2. 信号 handler 里只通知，清理逻辑放在主流程里。Go 把信号变成 goroutine 能等待的事件，没有 C 里 async-signal-safe 的限制。

容器里的另一个坑：如果入口是 shell 脚本，信号会发给 shell 而不是你的程序。`ENTRYPOINT` 用 exec 形式（JSON 数组），让 Go 程序成为 PID 1。

## 二、文件描述符

一切 I/O 都是 fd：文件、socket、管道。每个连接占一个，进程有数量上限（`ulimit -n`）。排查泄漏用 `lsof -p <pid>` 看数量随时间是否单调增长。常见泄漏源：`http.Response.Body` 没关、`os.Open` 后没 `defer Close()`。

## 三、cgroup：限额与 Go 运行时

容器的 `--cpus 1.5` 在 cgroup 里是 `cpu.max = 150000 100000`。关键联动：**Go 1.25 起，runtime 默认读这个文件来设置 `GOMAXPROCS`**，并周期性更新。限额低于机器核数时，不会再开出一堆 P 去抢配额、造成节流和延迟毛刺。另外有个下限：默认值不会低于 2（机器本身少于 2 核除外），所以 `--cpus 0.5` 得到的也是 2。我核对了本机 Go 1.26.4 的文档：`go doc runtime.GOMAXPROCS` 写明默认值来自逻辑 CPU 数、亲和性掩码和 Linux 上的 cgroup 配额。

一个很容易漏掉的细节：**这个行为只在 `go.mod` 的 `go` 行是 1.25 及以上时默认开启**。`containermaxprocs` 和 `updatemaxprocs` 这两个 GODEBUG 项，在语言版本 1.24 及以下默认是关的。升级了工具链但 `go.mod` 还写 1.24，容器里仍然会用宿主核数。（依据：本机 `go doc runtime.GOMAXPROCS` 与 `$GOROOT/doc/godebug.md`）

旧章在容器里实测了取整规律：1.5 核限额时 `GOMAXPROCS` 是 2（向上取整），4 核限额是 4，无限额等于宿主核数；内存限额不影响它。（未在本机验证：需要 Linux 容器，来自旧章 `notes/p25-containers-cloud.md`）

内存同理：`GOMEMLIMIT`（或 `debug.SetMemoryLimit`）是**软限制**，让 GC 在接近上限时更积极；它不包含 cgo 分配的内存和内核替进程持有的内存。给容器设内存限额时，`GOMEMLIMIT` 设成限额的 90% 左右留余量，是常见做法。（90% 是经验值，待核；机制见 `go doc runtime/debug.SetMemoryLimit`）

## 四、镜像：多阶段构建

Go 编译出静态二进制，交付镜像可以只有基础层加这一个文件：

```dockerfile
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download            # 依赖层：go.mod 不变就命中缓存
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app .

FROM alpine:3.22
COPY --from=build /out/app /usr/local/bin/app
USER nobody
ENTRYPOINT ["/usr/local/bin/app"]
```

本机验证了编译这一步（交叉编译到 linux/arm64）：`file` 显示 `statically linked`；加上 `-trimpath -ldflags="-s -w"` 后二进制从 7.86 MB 降到 5.37 MB。旧章实测的整套镜像从 351 MB 的构建镜像降到 26.6 MB。（Dockerfile 与镜像大小未在本机验证，来自旧章）

三个习惯：先 COPY 依赖清单再 COPY 源码（缓存）；非 root 运行；用 `.dockerignore` 排除 `.git` 等（构建上下文越大传输越慢）。

## 五、健康检查

"进程在跑"不等于"能服务"。编排器靠探针决定要不要把流量发给你、要不要重启你。设计原则：

- 别在探针里查下游：下游挂了你也被判死，故障被放大。
- 别探得太频繁，探针流量也是流量。
- 只测端口 `nc -z` 能发现进程装死，但抓不到 handler 死锁；打一个真实的轻量端点（如 `GET /healthz`）更可靠，代价是探针请求进业务日志，用固定前缀方便过滤。

## 常见坑

1. 以为 `kill` 会让 Go 程序优雅退出：不捕获信号就直接终止。
2. 入口用 shell 形式，信号没送到程序。
3. `go.mod` 里 go 版本停在 1.24，容器里 `GOMAXPROCS` 仍然等于宿主核数。
4. `Shutdown` 超时设得比容器的强杀窗口还长。
5. 把 `GOMEMLIMIT` 当硬限制：它是 GC 的软目标，超限不会被运行时拦住。

## 判断题

1. 为什么 `kill -9` 下连一行日志也打不出来？
2. 容器限 1.5 核，Go 1.25 以后 `GOMAXPROCS` 是多少？`go.mod` 写 `go 1.24` 又是多少？
3. 探针里调用下游数据库有什么问题？

## 在项目里

`dnsproxi-go/main.go` 的 `Proxy.Run(ctx)` 已经按本节的方式写好了关停路径：一个只等 `<-ctx.Done()` 就关闭监听口的 goroutine，让阻塞在读取上的主循环立刻返回，再用 `WaitGroup` 等在途查询排空。但 `main` 传给它的是 `context.Background()`，**没有接 SIGTERM**，所以 SIGTERM 到来时不会触发任何清理（容器内的具体表现未在本机验证）。这是一个现成的改造题：把它换成 `signal.NotifyContext`，再用 `kill -TERM` 验证 `Run` 返回 `nil`。

## 想深入去哪查

- 旧长版：`notes/p23-linux-runtime.md`（进程、信号、fd、容器三件套）、`notes/p25-containers-cloud.md`（多阶段构建、限额实测、健康检查）。
- 原材料：无直接对应单元，来自旧章。
- 官方：`go doc runtime.GOMAXPROCS`、`$GOROOT/doc/godebug.md`、`go doc runtime/debug.SetMemoryLimit`。
