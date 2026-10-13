---
title: P26 · 部署实战：把毕业项目发上去（compose 编排）
---

# P26 · 部署实战：把毕业项目发上去（compose 编排）


> 这章学完你能干什么：用一个 compose.yaml 把 dnsproxi（DNS 代理）+
> httpkv（KV 服务）+ fake upstream 编排成整栈；服务间用服务名互通
> 而不是 127.0.0.1；启动顺序用 healthcheck 条件而不是 sleep。
> 这是全课程的收官章：P6 的 dnsproxi、P8 的 httpkv、P25 的镜像
> 全部合体。读者：读完 P25。
>
> 实战说明：全部真跑（本机 docker 29.4，compose v2）。文件存档在
> `labs/compose/`。

## 体系背景：编排解决"部署单元之间的拓扑"

编排这层和语言彻底解耦：compose/k8s 只认容器和端口，Go 镜像和
Rust 镜像在同一个 yaml 里各占一个 service 是常态。语言差异只剩
镜像大小（musl 静态 Rust 更小）和启动时间（Go 与 Rust 都是毫秒
级，对比 JVM 秒级）。

单容器是 P25 的事。真实系统是**一组容器 + 网络拓扑 + 启动依赖**：
dnsproxi 要找到 upstream，httpkv 要被探活后 dnsproxi 才起。这三个
问题（组网、发现、顺序）就是编排器（compose 是它的玩具版，k8s 是
生产版）的存在理由。

compose 的模型一句话：**同一个 yaml 里声明的服务自动进同一个
网络，服务名就是 DNS 名**。这个"服务名即 DNS"在 k8s 里原样成立
（Service 对象），所以 compose 玩熟了，k8s 的心智只差 API。

## 编排文件：三个服务一张图

```yaml
# labs/compose/compose.yaml（真跑）
services:
  httpkv: # KV 服务（P8/P13 素材）
    image: golearn-httpkv:ms
    ports: ['18090:8090'] # 宿主:容器
    healthcheck: # P25 的健康检查
      test: CMD nc -z localhost 8090
      interval: 3s
      retries: 3
    cpus: 1.5 # P25 的限额联动
    mem_limit: 64m
  upstream: # fake DNS 上游，Go 写的 fakeup
    build:
      context: .
      dockerfile: upstream.Dockerfile
  dnsproxi: # 毕业项目（P6）
    image: golearn-dnsproxi
    environment:
      DNS_LISTEN: ':15353'
      DNS_UPSTREAM: 'upstream:15300' # ← 服务名当 DNS 名！
    ports: ['15353:15353/udp', '19153:9153']
    depends_on:
      httpkv:
        condition: service_healthy # ← 探活通过才启动
```

三行值得逐字读：

1. `DNS_UPSTREAM: upstream:15300`：**不是 127.0.0.1**。容器里的
   loopback 是它自己，P26 实战第一次 up 时 dnsproxi 打日志
   `upstream read failed: connection refused`，就是踩了这个坑
   （本机裸跑时 upstream 在宿主 loopback，进了容器就没了）。
2. `condition: service_healthy`：依赖的是"健康"不是"启动了"。
   没有 healthcheck 时 compose 只保证启动顺序，不保证可用。
3. `ports` 只声明对外的主机映射；服务间流量走内部网络，不占宿主
   端口（upstream 一个 port 都没写，别的服务照样能到它）。

## 起栈：一条命令全家福

```sh
$ docker compose up -d --build
[+] Running 4/4
 ✔ Container p26lab-httpkv-1  Healthy
 ✔ Container p26lab-upstream-1  Started
 ✔ Container p26lab-dnsproxi-1  Started

$ docker compose ps
NAME                STATUS
p26lab-httpkv-1     Up About a minute (healthy)
p26lab-dnsproxi-1   Up 35 seconds
p26lab-upstream-1   Up 21 seconds
```

启动顺序是编排器算出来的：httpkv 和 upstream 并行起 → healthcheck
探过 httpkv → dnsproxi 启动。**声明式**是关键词：你描述"要什么
状态"，compose 算"怎么到"（对照命令式脚本：先 ssh 这台、再启动
那个、sleep 30 等它活，每个环节都是脆弱点）。

## 端到端验证：三个面一次看全

```sh
# 1. DNS 面：宿主 dig → 容器 dnsproxi → 容器 upstream
$ dig +noedns @127.0.0.1 -p 15353 compose.test A +noall +answer
compose.test.		60	IN	A	10.0.0.1

# 2. HTTP 面：宿主 curl → 容器 httpkv
$ curl -s -X PUT -d '{"Value":"from-compose","TTL":0}' \
    localhost:18090/kv/hello -o /dev/null -w "%{http_code}\n"
201

# 3. 指标面：Prometheus 文本协议（P13/P17）
$ curl -s localhost:19153/metrics | grep "^dnsproxi_queries"
dnsproxi_queries_total{outcome="forwarded",qtype="A"} 10
```

服务间验证（在容器里 curl 别的服务名）：

```sh
$ docker compose exec dnsproxi sh -c 'wget -qO- http://httpkv:8090/kv/hello'
```

注意 wget 的是 `httpkv:8090`（内部网络），不是 `localhost:18090`
（宿主映射）。**"端口映射是给宿主的，服务名是给容器的"**。

服务找不到时的第一现场（第一次 up 的真机日志，127.0.0.1 坑）：

```text
$ docker logs p26lab-dnsproxi-1
{"level":"WARN","msg":"upstream read failed",
 "err":"read udp 127.0.0.1:55068->127.0.0.1:15300: read: connection refused"}
```

k8s 里同样的分界（Service DNS vs NodePort/Ingress）。

## 优雅停机与资源生命周期

```sh
$ docker compose down           # 停+删容器+删网络（卷默认留）
$ docker compose stop           # 只停，留着下次 start
$ docker compose logs -f dnsproxi   # 单服务日志流
```

`down` 对每个容器发 SIGTERM → 等 P23 的优雅关停（httpkv 打
shutting down）→ 超时才 SIGKILL。整栈的退出顺序也是编排器排的
（依赖方先停）。对照 k8s：Pod 的 `terminationGracePeriodSeconds`
同一语义。

`config` 子命令渲染出编排器的"真实视图"（变量已展开、条件已
解析，排 yaml 语法问题用它）：

```sh
$ docker compose config
name: compose
services:
  dnsproxi:
    depends_on:
      httpkv:
        condition: service_healthy    ← 你的意图被原样保留
    environment:
      DNS_LISTEN: :15353
      DNS_UPSTREAM: upstream:15300
```

真机实证（labs/compose 起栈后逐条跑）：

```sh
$ docker compose up -d && sleep 2
 Container compose-dnsproxi-1  Started
$ docker compose ps --format "{{.Name}} {{.Status}}"
compose-dnsproxi-1   Up 2 seconds
compose-httpkv-1     Up 5 seconds (healthy)
compose-upstream-1   Up 5 seconds

$ docker stop compose-httpkv-1            # 或 compose down
$ docker logs compose-httpkv-1 2>&1 | tail -1
{"level":"INFO","msg":"shutting down"}    ← P23 的信号链在容器里兑现
```

## 从 compose 到 k8s：映射表

| compose | k8s | 差异 |
|---|---|---|
| service | Deployment + Service | k8s 多了副本/滚动更新 |
| depends_on + healthcheck | InitContainer + readinessProbe | 条件依赖显式化 |
| cpus/mem_limit | resources.limits | 同 cgroup 机制（P25） |
| ports | Service.ports / Ingress | 集群内不需要映射 |
| environment | ConfigMap/Secret | 配置与镜像分离 |

学 k8s 的正确路径是先把 compose 的四个面（组网/发现/顺序/限额）
玩透，再背 API。概念是同一套，只是声明语法更啰嗦。

Rust 对照：actix/axum 服务的 compose 化是同一份 yaml 换镜像名，
健康探针用 axum 的 health 路由同款写法；停机链路（tokio::signal
→ 优雅关停）在 P23 已逐行对照过。

## Take-away

1. 编排 = 组网（同网络服务名互通）+ 发现（DNS）+ 顺序
   （healthcheck 条件依赖）+ 限额（cgroup）。
2. 容器里 127.0.0.1 是它自己；服务间用服务名，端口映射只给宿主。
3. `depends_on: service_healthy` 依赖"可用"而非"启动"；探活是
   编排器敢排拓扑的前提。
4. 声明式：描述要的状态，编排器算路径；命令式脚本每环节都是
   脆弱点。
5. compose 到 k8s 是同一概念换语法；四个面玩透了再上集群。

## 练习：跟着做（收官实战）

**预测**（跑之前写答案；预测错的地方才是认知边界）：

1. compose 网络里 `docker compose exec dnsproxi ping upstream`
   能通吗？靠的是什么机制（提示：P24 的 DNS）？
2. 把 dnsproxi 的 `DNS_UPSTREAM` 改回 `127.0.0.1:15300` 再 up，
   dig 会怎样？（第一次 up 的真机日志是答案。）

**观察**（全部真跑，存档在 labs/compose/）：

```sh
cd labs/compose
docker compose up -d --build        # 看启动顺序输出
docker compose ps                    # healthy 状态
dig +noedns @127.0.0.1 -p 15353 compose.test A +noall +answer
docker compose down                  # 看优雅停机日志
```

预期输出（起栈顺序 + healthy 判定，真机实拍）：

```text
 ✔ Container p26lab-httpkv-1  Healthy      ← healthcheck 探过
 ✔ Container p26lab-dnsproxi-1  Started    ← 条件满足才启动
NAME                STATUS
p26lab-httpkv-1     Up About a minute (healthy)
```

**改造**（收官题，串起全课程）：

1. 给 compose 加第三个服务：Prometheus（ prom/prometheus 镜像，
   配置抓 dnsproxi:9153），Grafana 看板里画出 P17 的
   `dnsproxi_queries_total`。
2. 把 httpkv 的 `mem_limit` 调到 8m，观察 OOMKilled（P25 的
   `$?`=137 容器版）；加 `GOMEMLIMIT=6MiB` 看 GC 先行让步。

## 章末参考

### compose 常用命令

| 命令 | 用途 |
|---|---|
| `up -d --build` | 起栈（重建镜像） |
| `ps` / `logs -f <svc>` | 状态/日志流 |
| `exec <svc> sh` | 进容器现场 |
| `down` / `down -v` | 停栈 / 连卷一起删 |
| `config` | 校验+渲染 yaml |

### 故障排查三板斧

| 症状 | 第一动作 | 本课对应 |
|---|---|---|
| 服务起不来 | `docker compose logs <svc>` | dnsproxi connection refused 实录 |
| healthy 反复重启 | healthcheck 命令在容器里手动跑 | P25 探针三忌 |
| 服务互相找不到 | `docker compose exec <svc> nslookup <peer>` | 服务名 DNS |

### Rust 对照总表

| 主题 | Go | Rust | 差异要点 |
|---|---|---|---|
| 镜像构建 | 多阶段 go build | cargo + musl | 同一 Dockerfile 套路 |
| compose 编排 | 语言无关 | 语言无关 | 编排在容器层，与应用语言解耦 |
| 优雅停机 | signal.NotifyContext | tokio::signal | P23 已对照 |
| 可观测 | slog JSON + /metrics | tracing + /metrics | 容器层看到的都是 fd 1/2 与 HTTP |
| 服务发现 | compose 服务名 DNS | 同款（k8s Service） | 平台职责，应用零代码 |
| 健康依赖 | depends_on healthy | 同款 | 探针语义语言无关 |

收官一句：从 P1 的 `go build` 到这里的 `docker compose up`，同一
条主线贯穿：**编译产物是一个静态文件，运行时是一组 OS 对象，工程
是把这两件事声明清楚**。compose/k8s 只是声明的格式不同。
