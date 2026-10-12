---
title: P5 · 项目精读 B：deepsearch，一个 Go 服务的骨架是怎么长出来的？
---

# P5 · 项目精读 B：deepsearch，一个 Go 服务的骨架是怎么长出来的？


> 载体：`/Users/akka/oss/github.com/clarkqaq/deepsearch`，~3500 行。
> 这课练**工程组织**：cobra CLI + echo HTTP + 自写泛型 DI + MCP transport，
> 全是"Java 后端习惯 → Go 习惯"的对照点。读者：读过 P2（接口定义在
> 使用方）、P3（goroutine 生命周期与 recover）。前 15 分钟看四个问题
> 的答案；练习在 `exercises/p5deep/`，参考实现里埋着两次真实翻车。

## 这章学完你能干什么

拿到一个陌生 Go 服务仓库，你能从目录读出分层、判断依赖方向对不对；
能自己用 20 行写一个类型安全的泛型 DI（并解释它为什么会在两种写法下
死锁）；能照抄优雅关停的完整链路起多 runner 服务；能用 slog 打出
结构化日志（知道它和 tracing 差在哪）。练习 `exercises/p5deep/`
3 个测试，验收 `go test -race ./exercises/p5deep/` 全绿（DI 涉及并发，
-race 必开）。四个问题依次是：

1. 目录即架构，从目录能读出什么？
2. 自写泛型 DI 的正确形状是什么，两次翻车翻在哪？
3. 优雅关停的完整链路怎么走？
4. slog 在真实服务里怎么用？

## 问题 1：目录即架构，从目录能读出什么？

```
deepsearch/
├── main.go                  # 3 行：cmd.Execute()
├── cmd/                     # cobra 命令层（root + server 子命令）
├── internal/
│   ├── provider/            # DI 注册：env/http/llm/search 的 provider
│   ├── transport/           # http.go + mcp.go：两种协议共用一个 service
│   └── service/search/      # 业务核心，不知道 transport 存在
├── pkg/                     # 可复用：di/slogtrap/httperr/env/util/agentutil
└── Dockerfile               # CGO_ENABLED=0 静态编译
```

**分层规则一目了然**：`cmd → provider → transport → service`。
service 不 import transport；transport 通过**使用方定义的接口**拿 service
（P2 讲过）。对照 Rust：这就是你 workspace 里 `core / server / cli` crate
的拆分，只是没有 Cargo.toml，**目录就是依赖图**。

## 问题 2：自写泛型 DI 的正确形状是什么，两次翻车翻在哪？

`pkg/di/di.go` 是读 Go 泛型的最佳样本。

`di.go:28` `type Provider[T any] func(ctx context.Context) (T, error)`

核心 API 三件套：

- `Provide[T](ctx, provider, opts...)` 注册（di.go:109）
- `Invoke[T](ctx) (T, error)` 解析（di.go:137）
- `Clean[T](ctx)` 清理 + 递归清理依赖（依赖方开 WithCleanRecursive 才级联，di.go:164）

**实现要点逐个读**：

1. **容器挂在 context 上**（di.go:62-63）：
   `context.WithValue(ctx, ctxKeyContainerKey, &containerImpl{})`，
   不搞全局单例，container 沿 ctx 树传播。**ctx 在这里被当成
   "请求作用域变量"用**，这是 Go 生态惯例（请求追踪、取消、容器全靠它）。
2. **类型当 key**（di.go:123）：
   `reflect.TypeFor[T]()`：泛型拿到编译期类型，运行时当 map key。
   `sync.Map` 存 `serviceEntry`（di.go:57）。Rust 对照：
   `TypeId::of::<T>()` + `HashMap`，一模一样的套路。
3. **单例带锁构造**（di.go:193-200）：
   ```go
   entry.mu.Lock(); defer entry.mu.Unlock()
   if entry.built { return entry.instance, nil }
   ```
   ⚠️ 读点：这**不是 double-checked locking**，是"每次 Invoke 都拿锁"：
   简单但热路径有锁竞争。Java 心智（Dagger 编译期生成、无锁）会想骂，
   Go 心智：**能跑、对、慢一点 = 够了**。[INFERENCE：作者权衡可读性]
4. **环检测走 ctx 链**（di.go:181-184）：解析链存在 ctx value 里，
   `slices.Contains(chain, key)` 报 `ErrCircularDependency`。
5. **依赖图是副产物**（di.go:186-190）：resolve 时顺手
   `LoadOrStore` 记录 parent→child 边，`Clean` 时逆着图递归清理，
   **运行时构建的 DAG 替代了编译期依赖注入图**。

**泛型用得极克制**：只用在 Provide/Invoke 的类型参数上，内部全是
`any` + 断言 `val.(T)`（di.go:149）。这是 Go 泛型的典型用法：
**边界泛型化，内部反射化**。

**两次翻车，答案在 `exercises/p5deep/deep.go` 注释里**（做到练习 2
之前先读它们，细节见下方练习区的预测题）：

- **翻车一（key 撞车死锁）**：所有类型的 provider 存同一个具体类型
  （比如 `providerFn`），`TypeFor` 算出来是同一个 key：后注册覆盖
  先注册，且所有类型共享同一个 `*sync.Once`。`Invoke[*db]` 的
  `Do` 里 provider 又调 `Invoke[config]`：同一个 Once 重入，
  自己等自己，死锁。修法：`providerOf[T]` 包一层，每个 T 有自己的
  key 类型，provider 与结果天然分 namespace（deep.go:66-73）。
- **翻车二（Once 闭包单例假象）**：`o.Do` 的闭包里把结果存进本地
  变量 `cached` 然后 `return cached`。只有第一个调用者真的执行了
  闭包，后续调用者的 `cached` 是零值：单例对一半人失效。修法：
  结果写进类型化缓存表，`Do` 之后统一回读（deep.go:114-120）。

## 问题 3：优雅关停的完整链路怎么走？

`cmd/server.go:53-140` 是完整教材。信号处理（:54-70）：

```go
sigCh := make(chan os.Signal, 1)
signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM, ...)
go func() {
    sig := <-sigCh
    cancel()                      // 第一次信号：取消 ctx
    <-sigCh
    os.Exit(1)                    // 第二次信号：不等清理直接退
}()
```

要点（全是 Go 惯用法）：

- `defer di.Shutdown(shutdownCtx)` 里用 **`context.WithoutCancel(ctx)`**
  （server.go:73）：父 ctx 已 cancel 但清理需要新超时，这个 API 1.21 才有
- runner 数组 + `sync.WaitGroup` + `errCh`（:92-124）：
  多服务并行跑，任一退出 → `cancel()` 全停。**errgroup 的手写版**，
  对照你 P3 练习改造的 errgroup 题
- **每个 goroutine 入口都有 `recover()`**（:101-113）：记日志、发 errCh、
  cancel：P3 讲的"goroutine panic 杀进程"的防御工事
- `errors.Is(e, context.Canceled)`（:121）：正常关停不算错误

## 问题 4：slog 在真实服务里怎么用？

`cmd/root.go:21-28`：tint handler 包上 `slogtrap.NewFinalHandler`，
再 `slog.SetDefault`：

```go
slog.Error("setup provider failed",
    slog.String("error", e.Error()),
    slogtrap.ExitCode(1))       // server.go:85 的真实调用
```

- 结构化字段是**显式的**（`slog.String(...)`），不像 tracing 的 `?field`
- 1.21+ 标准库自带，生态有 tint（彩色）/ slogtrap（作者自写的 final handler）
- **没有 span 概念**：tracing 的 `#[instrument]` 在 Go 世界对应的是
  `slog.Group` + 手动传 logger，或 OpenTelemetry，后者才叠 span。

## 动手：练习 p5deep（30-45 分钟）

```sh
cd /Users/akka/Dev/learning/go
go test -race ./exercises/p5deep/...   # 验收：全绿（DI 涉及并发，-race 必开）
```

**预测题**（先写答案再跑；第 1、2 题动手前先读 `deep.go` 注释里的
两次翻车）：

1. 把 `Provide` 改成所有类型共享一个 `providerFn` key，`Invoke[*db]`
   的 provider 里再 `Invoke[config]`：会发生什么？卡在哪一行？
   （§问题 2 翻车一）
2. `Invoke` 的 `o.Do` 闭包把结果存进局部变量直接 `return cached`：
   第二个 goroutine 调 `Invoke` 拿到什么？（§问题 2 翻车二）
3. `TestGracefulShutdown` 里 "bad" runner 返回 `boom`，"slow" runner
   的返回值被当成错误吗？为什么？（§问题 3，Canceled 不算错）

**观察验证**：

```sh
go test -race -run TestGracefulShutdown -v ./exercises/p5deep/
# 三个场景：正常完成 / 任一失败全停 / 外部取消视为正常
```

配套练习地图（预测/改造时对照）：

| 练习 | 考什么 | 在哪 |
|---|---|---|
| TestTypeKey | reflect.TypeFor 当 map key，未注册返回 false | p5deep |
| TestProvideInvoke | 单例缓存：provider 只跑一次，未注册报错 | p5deep |
| TestGracefulShutdown | errgroup 三语义：全绿/首错/取消不算错 | p5deep |

**改造题**：

1. 复刻 `reflect.TypeFor[T]()` 当 map key 的套路给 `TypedRegistry`
   加 `Remove[T]()`，断言删除后 `Load` 返回 false。
2. 用 `sync.OnceValue` 重写迷你 DI 的单例语义（P8 的官方解），
   对照手写版的锁行为。

## Take-away

1. Go 工程分层 = 目录即依赖图；internal/ 是编译器强制的包私有。
2. 自写 DI 的三件套：ctx 挂容器、TypeFor 当 key、反射边界内部 any；
   类型当 key 的表，注册项本身也要按类型隔离，否则撞 key 死锁。
3. 优雅关停的完整套路：signal→cancel→WaitGroup→errCh→WithoutCancel 清理；
   Canceled 不算错，第二次信号才强杀。
4. Go 泛型典型用法：API 边界泛型化，实现内部 any+断言，反射兜底。

---

## 章末参考

### transport 层：一个 service，两种协议

`internal/transport/http.go:18`：

```go
type Searcher interface {
    Search(ctx context.Context, query string) (search.Result, error)
    Fetch(ctx context.Context, url string) (search.Page, error)
}
```

- 接口在 transport 包定义（使用方），service 包零感知
- `SetupHttp` 用 `di.Invoke[provider.Env]` 等取依赖（http.go:25-40），
  echo v5 注册路由（`/v1/search`、`/v1/fetch`、`/healthz`）
- mcp.go 的 `mcpHandler(service Searcher)`（mcp.go:21）挂在同一个
  echo server 的 `/mcp` 路由上（http.go:53）：**MCP 和 HTTP 是同一个
  Searcher 的两个门面**，不是两棵 provider 树。你之前读过的 MCP
  知识直接对上号。

**对照 Spring**：`@Autowired Searcher` ≈ `di.Invoke[Searcher]`：
但 Go 版是**显式调用**，没有注解处理器、没有反射魔法、启动期报错。

### Dockerfile：P1 讲的静态二进制（Dockerfile:1-30）

```dockerfile
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X deepsearch/pkg/util.version=..." 
FROM alpine:3.24   # 甚至可以 scratch，用 alpine 是为了 ca-certificates + tzdata
```

- `CGO_ENABLED=0`：纯静态，扔哪都能跑
- `-ldflags -X pkg/util.version=...`：**编译期注入版本号**：Go 没有
  `env!()` 宏，惯例就是 ldflags 写变量
- `--mount=type=cache`：BuildKit 缓存 go mod / go build，对照你 cargo 的
  sccache 问题域

### MiniDI 与 deepsearch di.go 的差异清单

| 维度 | deepsearch di.go | p5deep MiniDI |
|---|---|---|
| 容器载体 | ctx value（containerImpl） | struct 字段 |
| key | `reflect.TypeFor[T]()` | 同款 |
| 注册隔离 | serviceEntry 按 T 存 | providerOf[T] 包一层（翻车一修法） |
| 单例锁 | entry.mu 每次拿 | once 表 + 缓存表两笔账 |
| 生命周期 | lifetime（Singleton/Transient）+ Clean 递归 | 仅 Singleton |
| 环检测 | ctx 链 + slices.Contains | 无（练习不要求） |
