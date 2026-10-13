---
title: P25 · 容器与云原生：镜像、限额、健康检查
---

# P25 · 容器与云原生：镜像、限额、健康检查


> 这章学完你能干什么：写一个多阶段 Dockerfile 把 351MB 的构建环境
> 和 26MB 的交付镜像分开；解释 `--cpus 1.5` 为什么让 GOMAXPROCS 变
> 2；给服务配 healthcheck 让编排器敢依赖它。骨架：把 P23 的
> golearn-httpkv 镜像化做完整。读者：读完 P23（容器三件套已拆过
> 包装）。
>
> 实战说明：全部真跑（本机 docker 29.4，arm64）。
Rust 读者可以把这章当"cargo 产物的部署学"读：musl 静态链接对应
CGO_ENABLED=0，cargo-chef 对应依赖层前置，scratch/distroless 对应
alpine。镜像层、cgroup、探针三件事完全语言无关，编排器眼里只有
容器没有语言。

## 体系背景：镜像是一叠只读层的挂载结果

容器文件系统 = 镜像层（只读，一层叠一层）+ 可写层（容器独占）。
Dockerfile 每条指令生成一层；`COPY`/`RUN` 层有内容，`EXPOSE`/
`ENV` 层是纯元数据（0B）。docker history 看真实分层：

```text
$ docker history golearn-httpkv:ms
0B      ENTRYPOINT ["/usr/local/bin/httpkv"]
0B      EXPOSE 8090
0B      USER nobody
8.85MB  COPY /out/httpkv /usr/local/bin/httpkv
0B      CMD ["/bin/sh"]
10.2MB  ADD alpine-minirootfs-3.22.6-aarch64.tar.gz
```

层的两个推论：**层被多个镜像共享**（都基于 alpine:3.22 时磁盘只存
一份）；**层不可变**，改一行 Dockerfile，其后所有层重建。这也是
构建缓存的工作原理：指令不变 + 输入不变 = 直接复用。

Rust 对照：cargo-chef/sccache 解决同一问题（依赖先编译成缓存层），
思路同款，机制在构建器侧。

## 多阶段构建：builder 不进交付

Go 静态二进制（P23 的 `CGO_ENABLED=0`）让交付镜像可以小到荒谬：
构建用的工具链（351MB）全部丢在 builder 阶段。

```dockerfile
# labs/compose/Dockerfile（真跑）
FROM golang:1.26-alpine AS build     # 阶段1：351MB 工具链
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download                  # 依赖层：go.mod 不变就命中缓存
COPY exercises/p15slog/ ./exercises/p15slog/
COPY exercises/p16slogh/ ./exercises/p16slogh/
COPY exercises/p8fast/httpkv/ ./exercises/p8fast/httpkv/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/httpkv ./exercises/p8fast/httpkv/

FROM alpine:3.22                     # 阶段2：交付
COPY --from=build /out/httpkv /usr/local/bin/httpkv
USER nobody                          # 不跑 root（最小权限）
EXPOSE 8090
ENTRYPOINT ["/usr/local/bin/httpkv"]
```

实测：`golang:1.26-alpine` 351MB，最终 `golearn-httpkv:ms` 26.6MB
（alpine 10.2MB + 二进制 8.85MB）。`COPY --from=build` 是两个阶段
之间唯一的桥，builder 里的 Go、模块缓存、源码全部留在阶段 1。
`inspect` 一条命令看镜像元数据（真跑）：

```sh
$ docker inspect golearn-httpkv:ms \
    --format '{{.Os}}/{{.Architecture}} layers={{len .RootFS.Layers}} user={{.Config.User}}'
linux/arm64 layers=2 user=nobody
```

两层 = alpine 基础层 + COPY 二进制层；`USER nobody` 在镜像
元数据里，运行时不可逃逸（除非显式 `--user` 覆盖）。

三个实战细节（每个都真踩过）：**依赖先 COPY 再 COPY 源码**（依赖
层缓存住，改源码不触发 `go mod download`）；**只 COPY 需要的包**
（httpkv 依赖 p15slog/p16slogh，漏了会 build 报错，构建上下文不
是整个 git 仓库）；**COPY 上下文是 docker build 的目录参数**，
`.dockerignore` 排除 .git/node_modules（上下文越大传输越慢）。

## 资源限额：cgroup 与 GOMAXPROCS 的联动

`--cpus 1.5` 在 cgroup 里写 `cpu.max = 150000 100000`
（1.5 核配额）。容器内实测：

```sh
$ docker run -d --name hk-lim --cpus 1.5 -p 18091:8090 golearn-httpkv:ms
$ docker exec hk-lim cat /sys/fs/cgroup/cpu.max
150000 100000
```

关键联动：Go 1.25+ runtime 读这个文件算 GOMAXPROCS（容器内跑探针
程序，真机输出）：

| cpu.max | NumCPU | GOMAXPROCS |
|---|---|---|
| 150000/100000（1.5 核） | 10 | **2**（向上取整） |
| 100000/100000（1 核） | 10 | 2 |
| 400000/100000（4 核） | 10 | 4 |
| 无限额 | 10 | 10 |

规律：**GOMAXPROCS = ceil(CPU 限额)，上限是 NumCPU**。内存限额
（`--memory 64m` → `memory.max=67108864`）不影响 GOMAXPROCS。

探针程序就一行（`labs/compose/` 素材的 `probe.go`，交叉编译后
`docker cp` 进容器跑）：

```go
package main

import (
    "fmt"
    "runtime"
)

func main() {
    fmt.Println("GOMAXPROCS=", runtime.GOMAXPROCS(0), "NumCPU=", runtime.NumCPU())
}
```

```sh
$ CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o probe probe.go
$ docker cp probe hk-lim:/tmp/probe && docker exec hk-lim /tmp/probe
GOMAXPROCS= 2 NumCPU= 10        ← 1.5 核限额那次
```

内存限额的实证（真跑）：`memory.max` 写进 cgroup，但
GOMAXPROCS 仍是 10：

```sh
$ docker run -d --rm --name hk-mem --memory 64m golearn-httpkv:ms
$ docker exec hk-mem cat /sys/fs/cgroup/memory.max
67108864                        ← 64MiB
$ docker exec hk-mem /tmp/probe
GOMAXPROCS= 10 NumCPU= 10       ← 内存不影响调度器
```

为什么这联动重要：Go 的调度器按 GOMAXPROCS 开 P（逻辑处理器），
每个 P 上跑的 goroutine 会占 CPU 时间片。限额 1.5 核却开 10 个 P
= 10 个执行流抢 1.5 核的配额，节流（throttling）时全队停摆，延迟
毛刺。Go 1.25 前要 automaxprocs 库手动对齐；1.25 起
`containermaxprocs` GODEBUG 默认开（P22 的表）。

## 健康检查：让编排器敢依赖你

容器"活着"（PID 1 在跑）不等于"能用"（端口能服务、依赖连得上）。
healthcheck 把这个判断交给编排器：

```yaml
# compose 版（P26 展开）
healthcheck:
  # test: ['CMD', 'wget', '-q', 'localhost:8090/kv/__probe']
  test: CMD wget -q localhost:8090/kv/__probe # 打真实请求
  interval: 3s # 每 3 秒探一次
  retries: 3 # 连败 3 次判 unhealthy
```

真跑（compose 起栈后）：

```text
$ docker ps --format "{{.Names}} {{.Status}}"
p26lab-httpkv-1  Up About a minute (healthy)   # ← healthcheck 生效
```

httpkv 用 `nc -z` 探端口（进程装死但端口不收包的场景能抓住）。
更彻底的探法是打一条真实请求：

```go
mux.HandleFunc("GET /kv/__probe", func(w http.ResponseWriter, r *http.Request) {
    writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
})
```

探针设计三忌：别查下游（下游挂了你也被判死，雪崩）；别太频
（探针流量也是流量）；`start_period` 给启动留宽限（JVM 类慢启动
的 Go 服务一般不用）。

探针端点代码（P22 的 mux 惯用法，P26 编排就用它）：

```go
mux.HandleFunc("GET /kv/__probe", func(w http.ResponseWriter, r *http.Request) {
    writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
})
```

与 `nc -z` 的取舍：`nc -z` 只证"端口有 listener 在 accept"，
不证业务能响应（死锁的 handler 照样探活）；打真实请求才覆盖
"handler 链路通"。代价是探针流量进业务日志，`__probe` 前缀
方便日志里过滤。

## 可观测性进容器：stdout 纪律的兑现

P20 的"日志进 stderr/fd 2"和 P22 的"pprof 单独端口"在这里兑现：

```sh
$ docker logs p26lab-httpkv-1          # docker 收容器的 fd 1/2
{"level":"INFO","msg":"httpkv on :8090 (pprof on :6060/debug/pprof)"}
$ docker exec p26lab-httpkv-1 sh       # 进容器现场
$ curl localhost:6060/debug/pprof/heap  # 宿主转发 -p 18090:8090 后也可以
```

指标端点同理：`-p 19153:9153` 转发 dnsproxi 的 /metrics，
Prometheus 抓的就是它（P13/P17 的落地）。日志收集器
（Promtail/Filebeat）挂 `/var/lib/docker/containers/*/` 的
JSON 文件。你的 slog JSON 直接被全文索引，**不打多行栈**（P15
的纪律）在这里变成硬约束。

## Take-away

1. 镜像 = 只读层叠挂载；`docker history` 逐层对账；层共享与
   缓存键（指令+输入）是构建速度的全部来源。
2. 多阶段构建：`COPY --from=build` 唯一桥接，351MB→26.6MB；
   依赖层前置保缓存，构建上下文最小化。
3. `--cpus` 写 `cpu.max`；Go 1.25+ 自动按 ceil(限额) 设
   GOMAXPROCS，限 CPU 不对齐 = 调度毛刺。
4. healthcheck 区分 alive 与 ready，探自己不探下游；编排器的
   `depends_on: condition: service_healthy` 靠它。
5. 容器内日志 = fd 1/2 被收集；slog 单行 JSON 是日志管线的硬约束；
   pprof/metrics 用独立端口转发。

## 练习：跟着做（实战即练习）

**预测**（跑之前写答案；预测错的地方才是认知边界）：

1. `--cpus 0.5` 时 GOMAXPROCS 是多少？`--memory 32m` 呢？
2. Dockerfile 里把 `COPY go.mod` 和 `COPY *.go` 顺序对调，缓存
   命中怎么变？

**观察**（全部真跑）：

```sh
docker build -t golearn-httpkv:ms -f labs/compose/Dockerfile .
docker history golearn-httpkv:ms               # 逐层对账
docker run -d --cpus 1.5 --rm golearn-httpkv:ms   # 对照 cpu.max
```

**改造**：给 httpkv 加 `/kv/__probe` 端点，healthcheck 从 `nc -z`
换成打真实请求；重启 compose 看状态变 healthy。

## 章末参考

### Dockerfile 常用指令速查

| 指令 | 层 | 备注 |
|---|---|---|
| FROM | 基础层 | `AS name` 命名阶段 |
| RUN | 有内容 | 每条一层；`&&` 合并少层数 |
| COPY | 有内容 | `--from=` 跨阶段；`.dockerignore` 先生效 |
| ENV/EXPOSE | 0B 元数据 | 不增镜像体积 |
| USER | 0B | 生产禁 root |
| HEALTHCHECK | 元数据 | compose 里写更常见 |

### 限额速查

| flag | cgroup 文件 | Go 感知 |
|---|---|---|
| `--cpus 1.5` | cpu.max | GOMAXPROCS=2（1.25+ 自动） |
| `--memory 64m` | memory.max | GC 看不到（要 GOMEMLIMIT 手动） |
| `--pids-limit` | pids.max | fork 炸弹防护 |

内存限额的 Go 侧联动：`GOMEMLIMIT=48MiB`（限额的 75%惯例）让 GC
在撞墙前加速，OOMKilled（`$?`=137 的容器版）是撞墙后内核杀。

### Rust 对照总表

| 主题 | Go | Rust | 差异要点 |
|---|---|---|---|
| 静态交付 | CGO_ENABLED=0 | musl/scratch | Go 更省事，Rust 更小 |
| 构建缓存 | 层缓存（依赖前置） | cargo-chef | 同一思路 |
| GOMAXPROCS | 1.25+ 自动读 cgroup | tokio worker_threads 手动 | Go 免配置 |
| 内存限额 | GOMEMLIMIT | 无直接对标（alloc 计数） | Go GC 感知限额 |
| 健康探针 | 端点自己写 | axum health 同款 | 编排器视角无差异 |
| 进容器排查 | docker exec + curl pprof | 同款 | Rust 用 tokio-console 替代 |
