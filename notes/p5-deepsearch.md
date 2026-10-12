# P5 · 项目精读 B：deepsearch，一个 Go 服务的骨架是怎么长出来的？

> 这章学完你能干什么：拿到一个陌生 Go 服务仓库，能从目录读出分层，把它编出来、跑起来、发信号让它退出，并看懂它的依赖注入（DI）和优雅关停。还能用 20 行写出类型安全的泛型 DI，并说清它为什么会在两种写法下出错。
> 载体：`/Users/akka/oss/github.com/clarkqaq/deepsearch`，约 3500 行（含测试）。读者：读过 P2（接口定义在使用方）和 P3（goroutine 与 recover）。
>
> 动手方式：本章以"跟着做"为主。先编译并跑起来，再改一处配置看变化，最后读源码。例子程序放在你自己的空目录里，用 `go run main.go` 运行。

## 体系背景：进程怎么被请走

服务进程不是自己想停就停。运维、容器平台、你在终端按 Ctrl+C，都是向进程发信号。

信号是内核递给进程的一个小整数。Ctrl+C 发 `SIGINT`（2），`kill` 默认发 `SIGTERM`（15）。进程没安排处理时，内核直接终止它。

shell 记录的退出码是 128 加信号编号，所以被 `SIGTERM` 杀掉的进程退出码是 143。先看一个没有任何处理的程序：

```go
func main() {
	fmt.Println("pid 就绪, mode = plain")
	time.Sleep(time.Hour)
}
```

```sh
$ ./sig plain & p=$!; sleep 0.5; kill -TERM $p; wait $p; echo "exit=$?"
pid 就绪, mode = plain
exit=143
```

143 说明进程被信号打断，没来得及清理连接、没来得及写完日志。服务要的是另一种结果：收到信号，收尾，再正常退出。

用 `signal.NotifyContext` 把信号变成 ctx 取消：

```go
ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
defer stop()
<-ctx.Done()
fmt.Println("收到信号，清理中")
time.Sleep(200 * time.Millisecond)
fmt.Println("清理完成")
```

```sh
$ ./sig graceful & p=$!; sleep 0.5; kill -TERM $p; wait $p; echo "exit=$?"
pid 就绪, mode = graceful
收到信号，清理中
清理完成
exit=0
```

清理卡住怎么办？常见做法是再收到一次信号就强杀：

```go
ch := make(chan os.Signal, 1)
signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
go func() {
	<-ch
	fmt.Println("第一次信号：开始清理（清理卡住了）")
	<-ch
	fmt.Println("第二次信号：直接退出")
	os.Exit(1)
}()
time.Sleep(time.Hour)
```

三种模式放在同一个程序里，用第一个命令行参数 `plain`、`graceful`、`twice` 切换。

```sh
$ ./sig twice & p=$!; sleep 0.5; kill -TERM $p; sleep 0.3; kill -TERM $p; wait $p; echo "exit=$?"
pid 就绪, mode = twice
第一次信号：开始清理（清理卡住了）
第二次信号：直接退出
exit=1
```

这三种退出方式，后面都会在 deepsearch 的 `cmd/server.go` 里对上号。Java 读者可以把第二种看成 JVM 的 shutdown hook。JVM 默认收到 `SIGTERM` 也是退出码 143，但会先跑 hook。Node 里对应 `process.on('SIGTERM', ...)`。

## 环境：把它编出来

deepsearch 是一个搜索服务：REST API 加 MCP 端点，后端调用大模型。先看仓库根目录：

```sh
$ cd /Users/akka/oss/github.com/clarkqaq/deepsearch
$ ls
AGENTS.md  cmd  compose.yaml  Dockerfile  go.mod  go.sum  internal  LICENSE  main.go  pkg  README_ZH.md  README.md  scripts
```

它的 `go.mod` 要求 `go 1.27.0`，比章外其他练习用的 1.26 新。在这个目录里运行 `go version`，工具链会自动切换：

```sh
$ go version
go version go1.27.0 darwin/arm64
```

编译到临时目录，不弄脏仓库：

```sh
$ go build -o /tmp/deepsearch .
$ /tmp/deepsearch --help
deepsearch service

Usage:
  deepsearch [command]

Available Commands:
  completion  Generate the autocompletion script for the specified shell
  help        Help about any command
  server      Start the REST API and the MCP endpoint

Flags:
  -h, --help   help for deepsearch
```

这是 cobra 命令行库生成的帮助。`server` 子命令只有一个参数 `--dotenv`，默认读当前目录的 `.env`。

## 第一次运行：看它怎么失败

直接启动，只改监听地址。配置用 `APP_` 前缀的环境变量：

```sh
$ APP_HTTP_ADDR=127.0.0.1:18231 /tmp/deepsearch server
2026-10-12 19:37:05 INF cmd/server.go:83 setup provider...
2026-10-12 19:37:05 INF provider/env.go:46 loading env from environment variables
2026-10-12 19:37:05 INF cmd/server.go:88 provider setup complete
2026-10-12 19:37:05 INF cmd/server.go:130 all server runners started, waiting for signal...
2026-10-12 19:37:05 INF cmd/server.go:118 starting server runner name=http
2026-10-12 19:37:05 ERR cmd/server.go:105 server runner panic recovered name=http stack=... panic="failed to invoke provider.LLM: MODEL_API_KEY is not set"
2026-10-12 19:37:05 INF cmd/server.go:115 server runner stopped name=http
2026-10-12 19:37:05 ERR cmd/server.go:138 server runner error detected error="runner http panic: failed to invoke provider.LLM: MODEL_API_KEY is not set"
```

进程退出码是 1。`stack=` 后面是很长的调用栈，这里省略了。

这条日志已经交代了三件事。缺少模型密钥，是在第一次 `Invoke` 那个服务时才发现的，不是启动时。runner goroutine 里发生了 panic，被入口的 `recover` 接住并记了日志。最后主流程汇总错误，以退出码 1 结束。

补上两个必填项再跑一次：

```sh
$ export APP_HTTP_ADDR=127.0.0.1:18231 APP_MODEL_API_KEY=fake APP_MODEL=fake-model
$ /tmp/deepsearch server &
$ curl -s -i http://127.0.0.1:18231/healthz
HTTP/1.1 200 OK
Content-Type: application/json
X-Request-Id: WmYZBpEXZLSsofTkyhlksVnFxLruNqmz
Date: Mon, 12 Oct 2026 11:37:23 GMT
Content-Length: 16

{"status":"ok"}
```

密钥是假的，实测启动时不校验它，所以服务能起来。`/healthz` 返回 200。再发一个空请求体到搜索接口：

```sh
$ curl -s -i -X POST http://127.0.0.1:18231/v1/search -H 'content-type: application/json' -d '{}'
HTTP/1.1 400 Bad Request
Content-Type: application/json
...
{"error":{"code":"invalid_request","message":"query is required"}}
```

现在向它发 `SIGTERM`，对照上一节的"优雅退出"：

```sh
$ pkill -TERM -f "deepsearch server"
2026-10-12 19:37:23 INF cmd/server.go:59 shutdown signal received, stopping services... signal=terminated
2026-10-12 19:37:23 INF cmd/server.go:161 http server stopped
2026-10-12 19:37:23 INF cmd/server.go:115 server runner stopped name=http
2026-10-12 19:37:23 INF cmd/server.go:71 shutting down provider...
2026-10-12 19:37:23 INF cmd/server.go:80 shutdown complete, see you next time!
```

日志里每一行的 `cmd/server.go:83` 这样的来源，是下面"slog"一节里 `AddSource: true` 的效果。

## 改一处：打开鉴权

配置里有个可选的 `AUTH_TOKEN`。设上它，再用同样的请求试一次：

```sh
$ export APP_AUTH_TOKEN=s3cret
$ /tmp/deepsearch server &
$ curl -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1:18231/healthz
200
$ curl -s -w "\n%{http_code}\n" -X POST http://127.0.0.1:18231/v1/search \
    -H 'content-type: application/json' -d '{"query":"x"}'
{"error":{"code":"unauthorized","message":"a valid bearer token is required"}}

401
$ curl -s -w "\n%{http_code}\n" -X POST http://127.0.0.1:18231/v1/search \
    -H 'content-type: application/json' -H 'authorization: Bearer s3cret' -d '{}'
{"error":{"code":"invalid_request","message":"query is required"}}

400
```

`/healthz` 依然免鉴权，业务接口没带令牌就是 401。带上令牌后请求通过鉴权，因为 `query` 为空，被业务校验拦成 400。

这就是 Java 读者熟悉的 `@Value` 或 `@ConfigurationProperties`：环境变量由 `caarlos0/env` 绑定到一个结构体。Node 里则是 `process.env` 加 dotenv，区别是 Go 在启动时一次绑定好，业务代码不再读环境变量。

## 目录

跑起来以后，回头读目录。从上面的 `ls` 往下展开：

```sh
$ ls cmd internal internal/provider internal/transport internal/service/search pkg
cmd:
root.go  server.go

internal:
provider  service  transport

internal/provider:
env.go  http.go  llm.go  logger.go  provider.go  search.go

internal/service/search:
search_test.go  search.go

internal/transport:
http_test.go  http.go  mcp_test.go  mcp.go  middleware.go

pkg:
agentutil  di  env  httperr  slogtrap  util
```

分层规则是 `cmd → provider → transport → service`。`main.go` 只有一句 `cmd.Execute()`，命令层在 `cmd/`，依赖的装配在 `internal/provider/`，两种协议的入口在 `internal/transport/`，业务核心在 `internal/service/search/`。`pkg/` 放可复用的小工具，其中 `agentutil` 是三个与 agent 相关的包。

方向对不对，可以用 grep 验证。service 不应该 import transport：

```sh
$ grep -rn "deepsearch/internal/transport" internal/service | wc -l
0
$ grep -rln "deepsearch/internal/service" internal/transport
internal/transport/mcp.go
internal/transport/http_test.go
internal/transport/http.go
```

零命中，而 transport 依赖 service。transport 通过自己定义的接口拿 service，P2 讲过这个惯例。`internal/` 是编译器强制的包私有，仓库外的代码 import 不了它。

Go 没有 `Cargo.toml` 那样的依赖清单，目录就是依赖图。这和 Java 的 Maven 多模块不同：Java 靠 `pom.xml` 声明模块依赖，Go 靠 `internal/` 加 import 路径约束。

## 依赖注入

`internal/provider/` 里每个文件都在向容器注册一个服务。容器是 `pkg/di/di.go`，读 Go 泛型的好样本。它的核心类型是：

```go
type Provider[T any] func(ctx context.Context) (T, error)   // di.go:28
```

三个核心函数：`Provide[T]` 注册（di.go:109），`Invoke[T]` 解析（di.go:137），`Clean[T]` 清理（di.go:164）。先用它们写个小程序。`replace` 指向本地仓库，就能 import：

```go
ctx := di.New(context.Background())

di.Provide(ctx, func(ctx context.Context) (Config, error) {
	fmt.Println("构造 Config")
	return Config{Port: 9090}, nil
})
di.Provide(ctx, func(ctx context.Context) (*DB, error) {
	fmt.Println("构造 DB")
	cfg := di.MustInvoke[Config](ctx)
	return &DB{DSN: fmt.Sprintf("port-%d", cfg.Port)}, nil
})

a := di.MustInvoke[*DB](ctx)
b := di.MustInvoke[*DB](ctx)
fmt.Println(a.DSN, a == b)

_, err := di.Invoke[string](ctx)
fmt.Println(err)
```

```sh
$ go run main.go
构造 DB
构造 Config
port-9090 true
service not found: string
```

注册时只登记闭包，不执行。第一次 `Invoke[*DB]` 才构造，构造 `DB` 时又触发 `Config`。第二次拿到的是同一个实例，这是单例。没注册的类型直接报错。

Spring 读者对照：`@Autowired Searcher` 约等于 `di.Invoke[Searcher]`。区别是 Go 版是显式调用，没有注解处理器，没有反射魔法，缺依赖在启动期就报错。TS 里的 NestJS 或 InversifyJS 容器也是类似的东西。

## 容器的实现

读 `di.go` 有五处要点。

**（1）容器挂在 ctx 上。** `di.go:62-63` 里 `context.WithValue(ctx, ctxKeyContainerKey, &containerImpl{})`。没有全局单例，容器沿 ctx 树传播。ctx 在这里充当"请求作用域变量"，Java 里的 `ThreadLocal`、Kotlin 的 `CoroutineContext`、Node 的 `AsyncLocalStorage` 做的是同类事情。

**（2）类型当 key。** `di.go:123` 的 `reflect.TypeFor[T]()` 在编译期拿到类型，运行时当 map key，存进 `sync.Map`（`di.go:57`）。Rust 里是 `TypeId::of::<T>()` 加 `HashMap`，一模一样的套路。

**（3）单例带锁构造。** `di.go:193-200` 每次 `Invoke` 都拿锁，再看 `built` 标志。这不是 double-checked locking，热路径有锁竞争。简单、对、慢一点，作者的选择是够用，这是我的推断，没有设计文档佐证。

**（4）环检测走 ctx 链。** `di.go:181-184` 把解析链存在 ctx 里，`slices.Contains` 发现重复就报错。

```go
di.Provide(ctx, func(ctx context.Context) (A, error) { _, err := di.Invoke[B](ctx); return A{}, err })
di.Provide(ctx, func(ctx context.Context) (B, error) { _, err := di.Invoke[A](ctx); return B{}, err })
_, err := di.Invoke[A](ctx)
fmt.Println(err)
fmt.Println(errors.Is(err, di.ErrCircularDependency))
```

```sh
$ go run main.go
circular dependency detected: main.A -> main.B -> main.A
true
```

**（5）依赖图是副产物。** `di.go:186-190` 在 resolve 时顺手记录 parent 和 child 的边。`Clean` 时逆着这张图递归清理，用运行时构建的 DAG 替代了编译期的注入图。

递归清理不是默认行为，要依赖方显式开 `WithCleanRecursive(true)`：

```go
func run(recursive bool) {
	ctx := di.New(context.Background())
	di.Provide(ctx, newChild, di.WithCleanup(cleanChild))
	di.Provide(ctx, newParent, di.WithCleanRecursive(recursive), di.WithCleanup(cleanParent))
	di.MustInvoke[Parent](ctx)
	di.Clean[Child](ctx)
}
```

```sh
$ go run main.go
WithCleanRecursive = false ，清理 Child：
  清理 Child
WithCleanRecursive = true ，清理 Child：
  清理 Child
  清理 Parent
```

清理 `Child` 时，`Parent` 因为依赖它，只有在自己开了递归标志时才跟着清理。`di.go` 的注释写的是"默认 true"，实测零值是 false，以实现为准。

最后注意泛型有多克制：只出现在 `Provide`、`Invoke` 的类型参数上，内部全是 `any` 加断言 `val.(T)`（`di.go:149`）。这是 Go 泛型的典型用法：边界泛型化，内部走反射。

## 两次翻车

练习包 `exercises/p5deep/deep.go` 里是一个 20 行的迷你 DI，参考实现里埋着两次真实翻车。两个都复现一遍。

**（1）key 撞车导致死锁。** 如果所有类型的 provider 都存成同一个具体类型 `providerFn`，`TypeFor` 算出的是同一个 key。后注册的覆盖先注册的，而且大家共享同一个 `*sync.Once`。`Invoke[*db]` 的 provider 里又调 `Invoke[config]`，就是同一个 Once 重入：

```go
fmt.Println("key 相同:", reflect.TypeFor[providerFn]() == reflect.TypeFor[providerFn]())

var once sync.Once
done := make(chan struct{})
go func() {
	once.Do(func() {
		once.Do(func() {})   // provider 里又 Invoke
	})
	close(done)
}()
select {
case <-done:
	fmt.Println("完成")
case <-time.After(500 * time.Millisecond):
	fmt.Println("500ms 后仍然卡住：Once 自己等自己")
}
```

```sh
$ go run main.go
key 相同: true
500ms 后仍然卡住：Once 自己等自己
```

修法是给每个 T 一个专属的 key 类型 `providerOf[T]`，`TypeFor[providerOf[T]]()` 对每个 T 都不同（`deep.go` 的 Provide 注释）。教训是：类型当 key 的表，注册项本身也要按类型隔离。

**（2）Once 闭包的单例假象。** 在 `Do` 的闭包里把结果存进局部变量，然后 `return cached`：

```go
func brokenInvoke() config {
	var cached config
	once.Do(func() {
		cached = config{port: 9090}
	})
	return cached
}

fmt.Println("第一个调用者:", brokenInvoke())
fmt.Println("第二个调用者:", brokenInvoke())
```

```sh
$ go run main.go
第一个调用者: {9090}
第二个调用者: {0}
```

只有第一个调用者真的执行了闭包。后续调用者的 `cached` 是零值，单例对一半人失效。修法是把结果写进类型化缓存表，`Do` 之后统一回读（`deep.go` 的 Invoke 注释）。Kotlin 的 `by lazy` 默认带同步，帮你包了这一步。Java 里写 lazy holder 或双检锁也要小心同类错误。

## 优雅关停

回到开头的信号实验。`cmd/server.go:53-140` 是完整教材，分四块。

**（1）信号处理。** `server.go:54-70`：

```go
sigCh := make(chan os.Signal, 1)
signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)

go func() {
	sig := <-sigCh
	slog.Info("shutdown signal received, stopping services...", slog.String("signal", sig.String()))
	cancel()          // 第一次信号：取消 ctx

	<-sigCh
	os.Exit(1)        // 第二次信号：不等清理直接退
}()
```

和体系背景里的 `twice` 模式是同一个结构。注意第二次信号只调 `os.Exit(1)`，不再 cancel，也不等清理，`defer` 里的清理会被跳过。

**（2）清理要用新的 ctx。** 父 ctx 已经取消，但清理还需要一个新的超时：

```go
shutdownCtx, shutdownCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
```

`context.WithoutCancel` 是 Go 1.21 加的，保留父 ctx 的值，但不继承取消。

**（3）runner 数组加 WaitGroup 加 errCh。** 多个服务并行跑，任一退出就 `cancel()` 全停。每个 goroutine 的入口都有 `recover()`（`server.go:101-113`），记日志、发 errCh、`cancel()`。这正是"第一次运行"那段日志里发生的事。

**（4）Canceled 不算错。** `errors.Is(e, context.Canceled)`（`server.go:121`）：正常关停不应该被记成错误。

这个结构就是手写的 errgroup。练习包的 `RunAll` 用 `errgroup.WithContext` 实现同样的语义。Kotlin 的 `coroutineScope` 同样是任一子协程失败就取消兄弟，JS 的 `Promise.all` 则只是快速失败，并不取消另外几个。

## slog

日志在 `cmd/root.go:21-28` 配置：用 tint 做彩色输出，外面包一层作者自写的 `slogtrap.NewFinalHandler`，再 `slog.SetDefault`。

`FinalHandler` 的用处是：日志记完之后，带 `ExitCode` 属性的错误日志会直接让进程退出。`server.go:85` 的真实调用就是它。写一个最小例子：

```go
slog.SetDefault(slog.New(slogtrap.NewFinalHandler(h)))

slog.Info("starting", slog.String("addr", "127.0.0.1:8231"), slog.Int("runners", 1))
slog.Error("setup provider failed", slog.String("error", "boom"), slogtrap.ExitCode(3))
slog.Info("这行不会打印")
```

```sh
$ go build -o slogd . && ./slogd; echo "exit=$?"
level=INFO msg=starting addr=127.0.0.1:8231 runners=1
level=ERROR msg="setup provider failed" error=boom exit_code=3
exit=3
```

结构化字段是显式的 `slog.String(...)`，不像 Rust tracing 的 `?field`。slog 在 Go 1.21 进入标准库。它没有 span 的概念，`#[instrument]` 在 Go 里对应的是 `slog.Group` 加手动传 logger，或者 OpenTelemetry。

Java 读者会觉得像 SLF4J 2 的 key-value 日志接口，JS 里最像 pino。区别是 slog 的 handler 是可替换的接口，tint 和 `FinalHandler` 都是在它上面套一层。

## 版本注入

服务要报告自己的版本。Go 没有 `env!()` 宏，惯例是编译期用 `-ldflags -X` 给包级变量赋值。`Dockerfile` 里的构建命令：

```dockerfile
CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w -X deepsearch/pkg/util.version=${BUILD_VERSION}" \
    -o /out/deepsearch .
```

`CGO_ENABLED=0` 关掉 C 调用，Linux 上得到纯静态二进制，扔进空镜像也能跑。`-s -w` 去掉符号表和调试信息，`-trimpath` 去掉本机路径。本章不构建镜像，只在本机编译同样的参数。

改一处看变化：用同一份源码编两个版本，再调 MCP 的 `initialize`。

```sh
$ CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X deepsearch/pkg/util.version=v1.2.3" -o /tmp/ds-static .
$ cd /tmp && ls -l deepsearch ds-static | awk '{print $5, $9}'
52624082 deepsearch
35684978 ds-static
```

去符号表后体积从约 50 MiB 降到约 34 MiB。分别启动两个二进制，请求 `/mcp`：

```sh
$ curl -s -X POST http://127.0.0.1:18232/mcp \
    -H 'content-type: application/json' -H 'accept: application/json, text/event-stream' \
    -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}'
{"jsonrpc":"2.0","id":1,"result":{"capabilities":{...},"protocolVersion":"2025-03-26","serverInfo":{"name":"deepsearch","version":"v1.2.3"}}}
```

没注入的版本回落到 VCS 提交号，也就是 `"version":"b7157af"`。后端的版本信息通过 `util.BuildVersion()` 暴露给 MCP 客户端（`internal/transport/mcp.go`）。

Webpack 的 `DefinePlugin` 和 esbuild 的 `--define` 在 JS 里做同样的事：构建期替换常量。Java 里则常见 Maven 资源过滤或 Gradle 往 `MANIFEST.MF` 写版本。

静态二进制的好处要在 Linux 容器里才看得出来。本机是 macOS，`otool -L` 列出的是系统库，这里看不出静态与否，本章没有验证。

## 一个 service，两种协议

最后看 transport 层。`internal/transport/http.go:18`：

```go
type Searcher interface {
	Search(ctx context.Context, query string) (search.Result, error)
	Fetch(ctx context.Context, url string) (search.Page, error)
}
```

接口在 transport 包定义，service 包对它零感知。`SetupHttp` 用 `di.Invoke` 取依赖（`http.go:25-40`），用 echo v5 注册路由：`/v1/search`、`/v1/fetch`、`/healthz`。

`mcp.go` 的 `mcpHandler(service Searcher)`（`mcp.go:21`）挂在同一个 echo server 的 `/mcp` 路由上（`http.go:53`）。MCP 和 HTTP 是同一个 `Searcher` 的两个门面，不是两棵 provider 树。前面 `initialize` 请求打的就是这个端点。

## 小结

1. Go 工程分层就是目录即依赖图，`internal/` 由编译器强制包私有。
2. 自写 DI 的三件套：ctx 挂容器，`TypeFor` 当 key，反射边界内部用 `any`。注册项也要按类型隔离，否则撞 key 死锁。
3. 单例用 `Once` 时，结果必须回读缓存，不能 return 闭包里的局部变量。
4. 优雅关停的套路：signal、cancel、WaitGroup、errCh、WithoutCancel 清理。Canceled 不算错，第二次信号才强杀。
5. 看陌生服务的顺序是：编译、跑起来、发信号、改一处配置，最后读源码。

## 练习：p5deep

练习在 `exercises/p5deep/`，3 个测试，DI 涉及并发，`-race` 必开：

```sh
$ go test -race -count=1 -v ./exercises/p5deep/
=== RUN   TestTypeKey
--- PASS: TestTypeKey (0.00s)
=== RUN   TestProvideInvoke
--- PASS: TestProvideInvoke (0.00s)
=== RUN   TestGracefulShutdown
--- PASS: TestGracefulShutdown (0.05s)
PASS
ok  	golearn/exercises/p5deep	1.347s
```

- `TestTypeKey`：用 `reflect.TypeFor` 当 map key，未注册返回 false。
- `TestProvideInvoke`：单例缓存，provider 只跑一次，未注册报错。
- `TestGracefulShutdown`：errgroup 三种语义，全部正常、首错全停、取消不算错。

**预测**，先写答案再跑，第 1、2 题动手前先读 `deep.go` 的注释：

1. 把 `Provide` 改成所有类型共享一个 `providerFn` key，`Invoke[*db]` 的 provider 里再 `Invoke[config]`，会发生什么？卡在哪一行？
2. `Invoke` 的 `o.Do` 闭包把结果存进局部变量直接 `return cached`，第二个 goroutine 调 `Invoke` 拿到什么？
3. `TestGracefulShutdown` 里 "bad" runner 返回 `boom`，"slow" runner 的返回值被当成错误吗？为什么？

**观察**：

```sh
go test -race -run TestGracefulShutdown -v ./exercises/p5deep/
```

这一条测试覆盖三个场景：正常完成、任一失败全停、外部取消视为正常。

另一个观察题在服务那边：对比"改一处：打开鉴权"那一节里的响应码和访问日志。日志里 `POST /v1/search` 的 `status` 写的是 500，响应实际是 401 和 400。去 `internal/transport/middleware.go` 找原因，我没有追过。

**改造**：

1. 复刻 `reflect.TypeFor[T]()` 当 map key 的套路，给 `TypedRegistry` 加 `Remove[T]()`，断言删除后 `Load` 返回 false。
2. 用 `sync.OnceValue` 重写迷你 DI 的单例语义（P8 的官方解），对照手写版的锁行为。

## 章末参考

### MiniDI 与 deepsearch 的差异

| 维度 | deepsearch di.go | p5deep MiniDI |
|---|---|---|
| 容器载体 | ctx value（containerImpl） | struct 字段 |
| key | `reflect.TypeFor[T]()` | 同款 |
| 注册隔离 | serviceEntry 按 T 存 | providerOf[T] 包一层（翻车一修法） |
| 单例锁 | entry.mu 每次拿 | once 表加缓存表两笔账 |
| 生命周期 | Singleton 与 Transient，Clean 递归 | 仅 Singleton |
| 环检测 | ctx 链加 slices.Contains | 无（练习不要求） |

### Rust 对照

| Rust | Go（本章） | 差异 |
|---|---|---|
| `TypeId::of::<T>()` 加 `HashMap<TypeId, Box<dyn Any>>` | `reflect.TypeFor[T]()` 加 `sync.Map` | 同一套路，同样会撞 key |
| `OnceLock<T>` | `sync.Once` 加缓存表 | Go 拆成两笔账，要自己回读 |
| `tokio::signal` 加 `CancellationToken` | `signal.Notify` 加 `cancel()` | Go 用 ctx 传取消 |
| `try_join!` 加取消 | `errgroup.WithContext` | 首错取消兄弟 |
| `tracing` 的 `?field` 与 span | `slog.String(...)`，无 span | span 要上 OpenTelemetry |
| `env!()` 与 `build.rs` | `-ldflags -X` | 编译期给包级变量赋值 |
| Cargo workspace 的 crate 拆分 | `internal/` 加目录 | 目录就是依赖图 |

### Java/Kotlin 与 TS/JS 类比

| 概念 | Java / Kotlin | TS / JS |
|---|---|---|
| DI 容器 | Spring `@Autowired`，Dagger，Koin | NestJS，InversifyJS |
| ctx 里放请求作用域值 | `ThreadLocal`，`CoroutineContext` | `AsyncLocalStorage` |
| 单例懒初始化 | lazy holder，Kotlin `by lazy` | 模块级变量缓存 |
| 优雅关停 | `Runtime.addShutdownHook`，Spring `server.shutdown=graceful` | `process.on('SIGTERM')` |
| 未处理信号的退出码 | JVM 默认 143（先跑 hook） | Node 被信号终止，143 |
| 任一失败取消兄弟 | Kotlin `coroutineScope` | `Promise.all` 只快速失败，不取消 |
| 构建期注入版本 | Maven 资源过滤，Gradle 写 manifest | `DefinePlugin`，esbuild `--define` |
| 结构化日志 | SLF4J 2 key-value，Logback | pino |
| 环境变量绑定配置 | `@ConfigurationProperties` | `process.env` 加 dotenv |
