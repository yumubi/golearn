# 22 容器与 Linux 运行时

> 详细程度：速通 · 约 30 分钟
> 材料：旧章 `notes/p23-linux-runtime.md`、`notes/p25-containers-cloud.md` · 本节命令在本机（go1.26.4，docker 29.4，arm64）复核过
> 前置：06 并发一、07 并发二（context 与优雅关停）

## 先看一个让人愣住的对照

同一个容器，两种 PID 1。第一种入口是 shell 脚本，里面再启动 Go 程序；第二种 `ENTRYPOINT` 直接是 Go 二进制。`docker stop` 的表现：

```text
$ docker run -d --name c1 alpine:3.22 sh -c 'sleep 500'
$ docker stop -t 12 c1        # 卡满 12 秒才结束
$ docker run -d --name c2 goapp-image
$ docker stop c2              # 1 秒内退出，日志里有 shutdown 记录
```

〔实测〕本机 docker 29.4 上测的：shell 做 PID 1 的容器，`docker stop -t 12` 花了 12.3 秒；exec 形式直起 Go 程序的容器不到 1 秒就停了（复核时测得 0.45 秒），容器日志里能看到 `got signal, draining` 和 `shutdown err: <nil>`。

为什么差这么多？关键是**信号送到了谁手里**。`docker stop` 默认先给容器主进程发 `SIGTERM`（镜像或运行参数可指定其他停止信号）。上面的 shell 作为 PID 1 没有把信号转发给子进程；超时后 Docker 发 `SIGKILL`。exec 形式里 Go 程序自己是 PID 1，注册信号通知后才能进入优雅关停。不同 shell 和脚本的转发、退出行为可能不同。

这一个现象背后是本节的全部内容：容器里的 Go 进程在操作系统眼里只是个普通进程，外面套了三层东西：命名空间管它看得见什么，cgroup 管它能用多少，镜像文件系统管它的根目录是什么。要操心的只有四件事：**怎么收到停止信号、文件描述符用得对不对、被限了多少资源、编排器怎么判断它活着**。前两件是普通 Linux 进程的知识，后两件是容器加的。

## 和你已知语言的差异

| 问题 | Java / Kotlin | Rust | Go |
|---|---|---|---|
| 信号默认行为 | JVM 装了 shutdown hook | 默认终止，要 signal-hook 或 tokio::signal | 默认终止，用 `signal.NotifyContext` 转成 `ctx.Done()` |
| 容器里的 CPU 感知 | JVM 自己读 cgroup | 线程数由代码或库决定 | Go 1.25 起 runtime 读 cgroup CPU 限额 |
| 交付物 | jar 加 JRE 镜像 | 可配置静态链接 | 禁用 cgo 等条件下可构建静态二进制 |
| 内存上限 | JVM 堆参数 | 无 GC，不需对齐 | `GOMEMLIMIT` 软限制，GC 对齐 |

纯 Go 程序通常可用 `CGO_ENABLED=0` 构建不依赖动态 C 库的二进制；依赖 cgo 的程序不能一概这样处理。即使二进制静态链接，也可能需要 CA 证书、时区数据或其他运行时文件；还要匹配目标操作系统与架构。

## 一、信号：SIGTERM 送达之后发生了什么

先看两种行为差多少。同一个程序做两个实验：起一个不装 handler 的自己，发 `SIGTERM`，等它死；再起一个装了 handler 的自己，发同一个信号，看结果：

```go
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

// 子进程分支：当靶子。os.Args[2] 是 raw（不装 handler）或 ctx（装）。
func main() {
	if len(os.Args) > 2 && os.Args[1] == "term" {
		if os.Args[2] == "ctx" {
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
			defer stop()
			syscall.Kill(os.Getpid(), syscall.SIGTERM)
			<-ctx.Done()
			fmt.Println("ctx: captured, exit normally")
			return
		}
		time.Sleep(10 * time.Second) // raw：安静等信号，默认处置接管
		fmt.Println("raw: survived")
		return
	}

	cmd := exec.Command(os.Args[0], "term", "raw") // 实验 1：默认处置
	_ = cmd.Start()
	time.Sleep(200 * time.Millisecond)
	syscall.Kill(cmd.Process.Pid, syscall.SIGTERM)
	err := cmd.Wait()
	fmt.Println("raw mode wait err:", err)

	cmd2 := exec.Command(os.Args[0], "term", "ctx") // 实验 2：装 handler
	_ = cmd2.Start()
	time.Sleep(200 * time.Millisecond)
	syscall.Kill(cmd2.Process.Pid, syscall.SIGTERM)
	err2 := cmd2.Wait()
	fmt.Println("ctx mode wait err:", err2)
}
```

```text
raw mode wait err: signal: terminated
ctx mode wait err: <nil>
```

两个实验，同一个信号，两种结局。不装通知的普通 Go 进程被 `SIGTERM` 终止（`signal: terminated`），装了通知的进程把信号变成 `ctx.Done()`，从主流程正常退出（`<nil>`）。默认终止不执行应用的优雅清理，但不能据此推断信号发生前没有日志或代码运行。shell 通常用 128 加信号编号表示信号终止，内核的等待状态则记录终止信号。〔实测〕shell 里 `kill -TERM` 得 143（128+15），`kill -9` 得 137（128+9）。

所以优雅关停必须先捕获信号。Go 的做法是把信号变成 context 的取消：

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()

srv := &http.Server{Addr: "127.0.0.1:18222", Handler: mux}
go srv.ListenAndServe()

<-ctx.Done() // 收到信号，从这里醒来
sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
err := srv.Shutdown(sctx) // 停止接新连接，等在途请求收尾
```

〔实测〕我在本机写了个 `/slow` 接口（睡 1.5 秒）验证完整时序：请求发出后 300 毫秒发 `kill -TERM`，客户端仍然收到了响应体 `done`，进程依次打印 `got signal, draining; inflight= 1`、`shutdown err: <nil>` 后退出。对照组同一时刻发 `kill -9`：进程当场消失，客户端拿到连接错误（curl 状态码 000）。上面的片段就是这条路径的形状，不是可运行程序（`mux` 没定义）。

三条规则，前一条是后一条的前提：

1. **`SIGKILL` 和 `SIGSTOP` 不能被捕获**，这是内核保留的。`docker stop` 先发 `SIGTERM`，等默认 10 秒（`docker stop -t` 可改）才发 `SIGKILL`。你的 `Shutdown` 超时必须小于这个窗口，否则容器照样被强杀。〔实测〕本机 `docker kill -s KILL` 杀掉的容器 `ExitCode` 是 137。
2. **信号 handler 里只通知，清理逻辑放在主流程**。`NotifyContext` 把信号变成 `ctx.Done()`，你阻塞在 `<-ctx.Done()` 的那个 goroutine 被唤醒，该干什么在主流程里干。C 语言里 handler 只能调 async-signal-safe 函数，Go 把这件事整个搬进了普通 goroutine 的世界，没有那条限制。
3. **PID 1 特殊**：PID 命名空间的 init 进程对信号有特殊规则，未安装处理动作的 `SIGTERM` 不会像普通进程一样按默认动作终止。上面实测的 shell 没有将停止信号转发给子进程，因此容器等到超时。〔实测〕`docker stop -t 12` 耗时 12.3 秒；不加 `-t` 实测 10.2 秒。具体行为取决于入口程序。

   这条规则对 Go 二进制要小心套用：Go runtime 启动时就替进程装好了自己的信号处理，所以"没人装 handler"在 Go 里并不成立。〔实测〕把一个不调用 `signal.Notify` 的 Go 程序直接当 PID 1，`docker stop` 不会卡，0.2 秒就返回，退出码是 2（不是 143）：进程被 runtime 就地终止，没有排空、没有日志。所以 shell 入口的问题是"信号被 shell 吃掉"，Go 入口的问题是"信号到了，但没有人把它变成优雅关停"。

开头的结论现在能完整推出来了：**exec 形式的 `ENTRYPOINT` 让你的 Go 程序当 PID 1，装上 handler 才接得住 `docker stop` 的 `SIGTERM`**。缺 exec 形式，信号到不了 Go 程序，容器卡满宽限期被强杀；缺 handler，信号到了也只是被当场终止。两种缺法，在途请求都会断。

## 二、文件描述符：一切 I/O 的把手

Unix 里"一切皆文件"落到实处就是 fd 表：0 是 stdin，1 是 stdout，2 是 stderr，之后每开一个文件、socket、管道就占一个新号。进程能开的 fd 有上限（`ulimit -n`），只开不关的话，fd 只增不减，直到撞上上限。下面这段程序自己数给自己看：

```go
package main

import (
	"fmt"
	"os"
)

func countFDs() int {
	entries, _ := os.ReadDir("/dev/fd") // macOS 和 Linux 都有这个目录
	return len(entries)
}

func main() {
	fmt.Println("start:", countFDs())
	var keep []*os.File
	for i := 0; i < 30; i++ {
		f, err := os.Open("/etc/hosts")
		if err != nil {
			fmt.Println("open err:", err)
			return
		}
		keep = append(keep, f) // 拿到 fd 从不 Close
	}
	fmt.Println("after 30 opens:", countFDs())
	for _, f := range keep {
		f.Close()
	}
	fmt.Println("after close all:", countFDs())
}
```

```text
start: 5
after 30 opens: 35
after close all: 5
```

〔实测〕本机输出如上；`Close` 后文件描述符释放。上限是 `ulimit -n`，〔实测〕把循环改成无限并设 `ulimit -n 64` 后，约第 60 个打开失败，具体数量取决于运行时已占的 fd。常见资源泄漏包括忘记关闭 `http.Response.Body` 或 `os.Open` 返回的文件；HTTP 连接是否复用或保持打开还取决于传输实现和响应是否读完。

排查时用 `lsof -p <pid>` 比较 fd 类型和数量；增长也可能来自正常负载，需结合请求量和连接状态判断。容器日志通常写 stdout 或 stderr，由 Docker 日志驱动收集，不一定要写 stderr。

## 三、cgroup：限额与 GOMAXPROCS 的联动

容器限了 CPU，你的程序知道吗？先看限额长什么样。`--cpus 1.5` 落到 cgroup 里是一个文本文件：

```text
$ docker run -d --name demo --cpus 1.5 goapp-image
$ docker exec demo cat /sys/fs/cgroup/cpu.max
150000 100000
```

〔实测〕`docker exec demo cat /sys/fs/cgroup/cpu.max` 打出 `150000 100000`，150000/100000 = 1.5 核，这是 cgroup v2 的"每 100ms 配额 150ms"写法。

关键联动在 Go 侧：**Go 1.25 起，runtime 默认读这个文件来设 `GOMAXPROCS`，并周期性更新**（`GODEBUG=containermaxprocs` 与 `updatemaxprocs` 默认开）。〔实测〕本机 `go doc runtime.GOMAXPROCS` 的 Default 与 Implementation details 两节写明：默认值取逻辑核数、亲和性掩码、cgroup 配额三者的最小值，配额不是整数时向上取整，且不会低于 2（机器本身少于 2 核除外）。

我在 1.5 核限额的容器里跑了探针程序（`GOMAXPROCS(0)` 加 `NumCPU`），〔实测〕输出 `GOMAXPROCS= 2 NumCPU= 10`：限额向上取整成 2，而宿主的 10 个核只看不用。〔实测〕同法测的其余组合：`--cpus 0.5` 也是 2（下限兜底）、`--cpus 4` 是 4、不限额是 10、`--memory 64m` 不影响它（还是 10）。

为什么要做这个联动？调度器按 `GOMAXPROCS` 设置 P 的数量。限额 1.5 核却开 10 个 P，多个执行流可能很快耗尽该周期的配额，随后受到 cgroup 节流，产生延迟毛刺。自动对齐能减少过度并行带来的风险，但 2 个 P 在 1.5 核配额下仍可能触发节流，不能保证完全消除。

一个很容易漏掉的细节：**这个行为只在 `go.mod` 的 `go` 行是 1.25 及以上时默认开启**。`containermaxprocs` 和 `updatemaxprocs` 在语言版本 1.24 及以下默认是关的。〔实测〕A/B 验证过：同一个容器（`--cpus 1.5`，`cpu.max` 同为 `150000 100000`）、同一套工具链，只把 `go.mod` 从 1.26.4 换成 1.24.0 重新编译，GOMAXPROCS 从 2 变回 10。升级了工具链但 `go.mod` 还写 1.24，容器里仍然会用宿主核数。

内存是另一套机制：`GOMEMLIMIT`（或代码里的 `debug.SetMemoryLimit`）是**软限制**，它告诉 GC 在接近上限时更积极，但不会拦住分配。〔实测〕设了 10MB 限制的程序继续分配到 100MB 照样活着，输出 `allocated MB: 100 still running, limit not enforced`。它也不覆盖全部内存：按 `go doc runtime/debug.SetMemoryLimit` 的说法，cgo 分配的内存、内核替进程持有的内存都不算在内。给容器设内存限额时，把 `GOMEMLIMIT` 设成限额的九成左右留出余量，是常见做法（九成是经验值，不是文档规定）。

## 四、镜像：多阶段构建，构建环境不进交付

对可静态构建的 Go 程序，多阶段构建可把工具链留在构建阶段，运行阶段只复制产物和必要的运行时文件：

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

`COPY --from=build` 是两个阶段之间唯一的桥，builder 里的 Go 工具链、模块缓存、源码都不进交付。大小怎么变？旧章 `notes/p25-containers-cloud.md` 在同款结构上量过：构建镜像 `golang:1.26-alpine` 351MB，交付镜像 26.6MB。这组数字来自旧章，这次复核没有重新构建镜像，未在本机复核；基础层 `alpine:3.22` 本机显示 14.4MB，可以看出交付镜像的大头不是你的二进制。

〔实测〕交叉编译这一步单独验证过：`CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build` 产物用 `file` 看是 `ELF 64-bit ... statically linked`；对一个只用 `fmt` 和 `os/signal` 的小程序，加 `-trimpath -ldflags="-s -w"` 把二进制从 2.4MB 压到 1.6MB（去掉符号表和调试信息，`-trimpath` 去掉的是源码路径）。

示例使用 `CGO_ENABLED=0` 避免动态 C 库依赖；程序依赖证书、时区或其他数据时仍需提供。漏了 `GOOS=linux` 会得到什么？〔实测〕把本机（macOS）编的 Mach-O 二进制挂进 alpine 容器执行，失败并返回 126。Docker Desktop 报 `Cannot run macOS (Mach-O) executable in Docker: Exec format error`；原生 Linux 的记录见旧章 `notes/p23-linux-runtime.md`。`file` 可区分 ELF 与 Mach-O，还应检查 CPU 架构。

三个习惯，动机都能从层机制推出来：

1. **先 COPY 依赖清单再 COPY 源码**：镜像是一叠只读层，指令不变加输入不变就直接复用。依赖层前置，改源码不会触发 `go mod download`。
2. **非 root 运行**：`USER nobody` 写进镜像元数据，容器里 `id` 显示 `uid=65534(nobody)`。〔实测〕这一条本机复核过。
3. **用 `.dockerignore` 排除 `.git` 等**：构建上下文越大传输越慢，上下文是 `docker build` 的目录参数。

## 五、健康检查：活着不等于能用

进程在跑（PID 1 活着）不等于能服务（端口通、业务逻辑没死锁）。探针就是用来区分这两件事的，编排器可以据此决定要不要发流量、要不要重启：

```text
$ docker run -d --name hc --health-cmd "nc -z localhost 9999 || exit 1" \
    --health-interval 1s --health-retries 2 alpine:3.22 sleep 300
$ docker ps --format "{{.Status}}"
Up 4 seconds (unhealthy)      # 没人监听 9999
$ docker exec -d hc sh -c 'while true; do nc -l -p 9999; done'
$ docker ps --format "{{.Status}}"
Up 8 seconds (healthy)        # 端口通了
```

〔实测〕上面是本机 docker 的输出（秒数每次略有浮动）：探针命令连续失败达到 `retries` 次判 unhealthy，恢复后翻回 healthy。监听用 `while` 循环是因为 busybox 的 `nc -l` 接受一次连接就退出，单次监听只能让探针成功一次，随后又会翻回 unhealthy。另一个观察：容器一直处于 unhealthy，`docker inspect` 里状态仍是 `running`、`RestartCount` 为 0。单机 docker 只负责把状态标出来，不会替你重启；"据此摘流量、重启"是 Swarm 或 K8s 这类编排器的事（K8s 的探针语义本节未在本机验证）。

设计原则有三条，核心是**探自己，不探下游**（这是业界经验，不是某份文档的规定）：

- 别在探针里查数据库或别的服务：如果探针失败会触发重启，下游挂了你也被判死并重启，故障被放大成雪崩。
- 别探得太频繁：探针流量也是流量。
- 探法上，TCP 探针只证明连接可以建立，不证明业务 handler 正常；轻量 HTTP 端点可覆盖更多应用路径，但也不能证明所有业务都正常。

## 小结

1. 不处理停止信号时进程会终止，不能依赖其完成优雅清理；`signal.NotifyContext` 可把信号转成 `ctx.Done()`，清理放在主流程。
2. 退出码 128 加信号编号：143 是 SIGTERM，137 是 SIGKILL（也是容器 OOM 或强杀的标志）。
3. PID 1 对未安装处理动作的信号有特殊规则；优先用 exec 形式的 `ENTRYPOINT` 直接启动 Go 程序。shell 是否转发信号取决于具体实现；本机实测 Go 程序未注册通知时退出码为 2，没有排空。
4. fd 是一切 I/O 的把手，泄漏到 `ulimit -n` 就是 `too many open files`；`lsof -p` 看趋势。
5. cgroup 的 `cpu.max` 是"每 period 配 quota"；Go 1.25 起 runtime 自动按它设 `GOMAXPROCS`（向上取整、下限 2、周期更新），前提是 `go.mod` 的 `go` 行在 1.25 及以上。
6. `GOMEMLIMIT` 是 GC 的软目标，不是硬墙；不含 cgo 和内核内存。
7. 多阶段构建把构建环境留在第一阶段，交付镜像只有基础层加静态二进制；依赖层前置保缓存，非 root 运行。
8. 探针探自己不探下游；`nc -z` 抓不到 handler 死锁，真实端点才覆盖业务链路。

## 常见坑

1. 以为发送停止信号就会优雅退出：未安排清理逻辑时，在途请求可能中断。
2. 入口用 shell 形式（`ENTRYPOINT ["sh", "-c", "go-app"]`）：shell 不一定转发信号，可能等到宽限期后强杀；具体行为取决于 shell。优先直接 exec 程序，或显式实现信号转发。
3. `go.mod` 里 `go` 行停在 1.24：容器感知默认关闭，默认值仍可能受 CPU 亲和性等影响；cgroup 的 CPU 限额本身仍然生效。
4. `Shutdown` 超时设得比容器强杀窗口还长：等不到排空就被 `SIGKILL`。
5. 把 `GOMEMLIMIT` 当硬限制：它是 GC 的软目标，超限不会被运行时拦住。
6. `http.Response.Body` 不关、`os.Open` 不 `defer Close`：fd 缓慢泄漏，长跑必死。

## 判断题与答案

先自己答，再看后面。

1. 为什么 `kill -9` 下连一行日志也打不出来？
2. 容器限 1.5 核，Go 1.25 以后 `GOMAXPROCS` 是多少？`go.mod` 写 `go 1.24` 呢？
3. 探针里调用下游数据库有什么问题？

**答案**

1. `SIGKILL` 不可捕获，默认处置由内核执行，进程没有机会跑任何用户代码；被信号杀死的进程退出码是 128 加信号号，137 就是这么来的。
2. 在逻辑 CPU 和亲和性都不小于 2、没有显式覆盖的前提下，1.5 核配额向上取整为 2。`go.mod` 是 1.24 时容器感知默认关闭，本机相同容器实测从 2 变回 10；其他环境还受亲和性与配置影响。
3. 下游挂了探针也失败，编排器把健康的你也判死并重启，故障从下游放大到整个服务；探针应该只测自己的端口或一个不打下游的端点。

## 在项目里

`dnsproxi-go/main.go` 的关停路径就是本节的形状：`main` 用 `signal.NotifyContext` 接住 `SIGTERM`/`SIGINT` 转 ctx 取消，`Proxy.Run(ctx)` 里一个只等 `<-ctx.Done()` 的 goroutine 关闭 UDP 监听口，阻塞在 `ReadFromUDP` 的读循环立刻返回，先 `close(done)` 停掉 expirer 再 `wg.Wait()` 等在途查询排空。sigterm 优雅退出与关停死锁的回归测试在 `dnsproxi-go/sigterm_test.go` 和 `dnsproxi-go/shutdown_repro_test.go`。容器内行为未在本机验证（需要 Linux 容器），机制同第一节。

## 想深入去哪查

- 旧长版：`notes/p23-linux-runtime.md`（进程、信号、fd、容器三件套）、`notes/p25-containers-cloud.md`（多阶段构建、限额实测、健康检查）。
- 原材料：无直接对应单元，本节事实来自旧章的实测记录与本机复核。
- 官方：`go doc runtime.GOMAXPROCS`（Default 与 Implementation details 两节）、`$GOROOT/doc/godebug.md`（containermaxprocs、updatemaxprocs）、`go doc runtime/debug.SetMemoryLimit`、`go doc os/signal.NotifyContext`。
