# 11 HTTP 与 gRPC：两种接口形态与它们共同的骨架

> 详细程度：标准 · 约 20 分钟
> 材料：TB09 W02-1 W10-1 W13-2、C:go_basic/http C:go_basic/grpc C:go_basic/protoc、旧长版 `notes/p10-grpc.md` `notes/p8-fast-track.md`
> 前置：04 接口与组合，10 分层与依赖注入

## 这一节要建立的认知

HTTP 和 gRPC 在 Go 里是同一种东西的两种面孔：**一个处理函数 + 一串包在外面的中间件**。HTTP 的中间件是 `func(http.Handler) http.Handler`，gRPC 的是拦截器 `func(ctx, req, info, handler)`。限流、日志、鉴权、超时、链路追踪都挂在这一层，业务代码不动。理解了这一点，换框架（标准库、gin、gRPC）只是换了签名。

## 一、HTTP：标准库已经够用

最小服务只要两个调用：`http.HandleFunc` 注册，`http.ListenAndServe` 启动，处理函数的原型固定为 `func(w http.ResponseWriter, r *http.Request)`。〔TB09 p3-4〕

Go 1.22 起 `ServeMux` 支持方法前缀和路径通配符，原来要引入第三方路由库才能做的事，标准库直接支持：

```go
mux := http.NewServeMux()
mux.HandleFunc("GET /books/{id}", func(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "book %s", r.PathValue("id"))
})
mux.HandleFunc("POST /books", func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusCreated)
})
```

〔实测〕go1.26.4 下 `GET /books/42` 返回 `200 book 42`；`POST /books/42` 自动返回 `405 Method Not Allowed`；没注册的路径返回 `404`。方法不匹配自动给 405，是新路由器的一个实用细节。老课程里用 gorilla/mux 做的 `Methods("POST")` 和 `mux.Vars` 现在可以不引入。〔TB09 p13-14〕〔C:go_basic/http/server/main.go:2〕

### 请求生命周期里的几条铁律

1. **响应顺序**：先 `w.Header().Add`，再 `WriteHeader(status)`，最后 `Write(body)`。`WriteHeader` 只能调一次，`Write` 之前没调就隐式是 200，之后再改 Header 无效。〔C:go_basic/http/server/main.go:2〕
2. **客户端必须关 `resp.Body`**，否则连接不复用，造成协程和内存泄漏，便捷方法 `http.Get` 也一样。〔C:go_basic/http/client/main.go:24-25〕
3. **用 `r.Context()` 感知取消**：客户端断开或服务端超时，`r.Context().Done()` 会触发，长耗时逻辑要监听它。
4. **middleware 本质**：接受 `http.Handler` 返回 `http.Handler` 的函数，嵌套包裹后串在业务 handler 之前，避免每个 handler 重复写日志、校验。〔TB09 p14-16〕

```go
srv := &http.Server{
	Handler:           logMW(http.TimeoutHandler(mux, 100*time.Millisecond, "timeout")),
	ReadHeaderTimeout: 2 * time.Second,
}
```

〔实测〕`/slow` 处理函数睡 300ms，超时包装器 100ms 后向客户端返回 `503 timeout`，同时处理函数里的 `r.Context().Err()` 是 `context deadline exceeded`。**只设一个 `ReadHeaderTimeout` 远远不够**：慢速读请求体、慢速写响应都要有超时，不设就是资源泄漏的入口，生产里要把 `ReadTimeout`、`WriteTimeout`、`IdleTimeout` 按业务填上。

### 优雅退出

套路固定：`signal.Notify` 捕获 SIGINT 和 SIGTERM，收到后用带超时的 context 调 `srv.Shutdown(ctx)`。`Shutdown` 立即关闭所有 listener、关闭空闲连接、等活动连接处理完。〔TB09 p9〕〔实测〕发送 SIGTERM 后 `Shutdown` 返回 `nil`，`Serve` 随后返回 `http: Server closed`，这是正常关闭，不要当错误处理。`ListenAndServe` 要放在独立 goroutine，用 channel 把错误传回主 goroutine。〔TB09 p16-17〕

### gin 的位置

标准库解决的是"一个路由器 + handler 模型"，gin 在上面补的是：参数绑定与校验（`Bind` 系列）、路由分组、中间件链、更顺手的响应方法。核心类型是 `gin.Context`，同时承担读请求和写响应。`gin.Default()` 自带 Logger 和 Recovery 两个中间件，`gin.New()` 是裸引擎。〔W02-1 p6〕〔W02-1 p10〕

组织路由的惯例：每个业务一个 `Handler` 结构体，用 `RegisterRoutes(server)` 自注册，公共前缀用 `server.Group` 防手抖。〔W02-1 p21-25〕gin 的 middleware 就是 `func(*gin.Context)` 的衍生类型，适合所有业务都关心的横切问题：CORS、日志、限流、可观测。〔W02-1 p44-45〕

跨域是前后端分离时的典型绊脚石：浏览器先发 `OPTIONS` 预检，服务端在响应头里用 `Access-Control-Allow-*` 表态，所以 CORS 中间件必须能处理预检请求本身，不能让它走到鉴权之类的后续逻辑里。〔W02-1 p41-42〕

标准库路由和 gin 的取舍：服务简单、依赖想少、新项目用 1.22 以后的 `ServeMux` 够用；团队已经熟悉 gin 或者需要它的绑定校验时选 gin。两者的 handler 都能用同样的"中间件 = 包装函数"思路。

## 二、gRPC：契约先行

选型口诀是"遇事不决选 gRPC"：基于 HTTP/2，支持双向流、流控、压缩，跨语言，社区强。〔W10-1 p26〕**契约先行**指先写 `.proto`（IDL，平台无关），再用编译器生成各语言的客户端和服务端代码，Protobuf 比 JSON 的序列化更快、体积更小。〔W10-1 p27-28〕

### 四种流写在 service 定义里

〔C:go_basic/grpc/idl/service/student_service.proto:28-38〕

```proto
service student{
    // Unary RPC 
    rpc QueryStudent(QueryStudentRequest) returns (QueryStudentResponse);
    rpc QueryStudents1(StudentIds) returns (QueryStudentResponse);
     // Server streaming RPC
     rpc QueryStudents2(StudentIds) returns (stream idl.model.student);
     // Client streaming RPC 
     rpc QueryStudents3(stream StudentId) returns (QueryStudentResponse);
     // Bidirectional streaming RPC
     rpc QueryStudents4(stream StudentId) returns (stream idl.model.student);
}
```

`stream` 写在请求前、响应前、还是两边，决定四种形态：一元、服务端流、客户端流、双向流。流的生命周期不对称：**客户端创建的流最终要 `CloseSend`/`CloseAndRecv`，服务端不用关**，类比 HTTP 里客户端关 body、服务端不关 request。〔C:go_basic/grpc/client/main.go:44-107〕

### 拦截器链

`grpc.ChainUnaryInterceptor(a, b, c)` 把多个拦截器串成链，签名是 `(ctx, req, info, handler)`，和 HTTP 中间件同一个思路：调 `handler(ctx, req)` 之前是前置逻辑，之后是后置逻辑。〔C:go_basic/grpc/server/main.go:61〕客户端拦截器的签名里是 `invoker` 而不是 `handler`。练习包 `exercises/p10grpc` 里的 `LoggingUnaryInterceptor` 是最小例子。

### 几条实践

1. **一个 `*grpc.ClientConn` 可以被多个 client 共用**（多路复用），不要每次调用都新建。`grpc.NewClient` 是惰性连接，调用级超时用 `context.WithTimeout`。〔C:go_basic/grpc/client/main.go:44-107〕
2. **负载均衡默认是 `pick_first`**：永远选 resolver 解析出的第一个节点，要做均衡必须在 service config 里指定 `round_robin` 等。〔W13-2 p4〕〔W13-2 p5〕
3. **重试靠 service config 的 `retryPolicy`** 加负载均衡：调用不通就重试，LB 保证不再选坏节点；`retryableStatusCodes` 通常只填 `UNAVAILABLE`。〔W13-2 p29〕
4. **生成代码的命令已变**：课程里的 `--go_out=plugins=grpc` 是旧写法，现行是独立的 `protoc-gen-go-grpc` 插件和 `--go-grpc_out`。〔C:go_basic/protoc/service/rec.proto:6-13〕
5. **`go_package` 必须带 `.` 或 `/`**，分号前是输出路径，分号后是包名。〔W10-1 p33-35〕

### HTTP 还是 gRPC

| 维度 | HTTP/JSON | gRPC |
|---|---|---|
| 运维与门槛 | 组件少，门槛低，兼容所有客户端 | 需要 proto 工具链，要求更高 |
| 异构与对外 | 浏览器、第三方直接用 | 浏览器不直接支持，需网关 |
| 性能与流 | 够用 | 二进制、HTTP/2、四种流 |
| 契约 | 靠文档或 OpenAPI | `.proto` 是契约本身 |

HTTP 微服务运维简单、适合异构；RPC 微服务性能和契约更强，但运维组件多。REST 是一种风格，微服务是一种架构，两者没有必然关系。〔W10-1 p11〕〔W10-1 p12〕经验判断：对外和前端用 HTTP，内部服务之间用 gRPC，两者共用同一套中间件思路。

## 常见坑

1. 只给 `http.Server` 设一个超时，其它方向的慢连接把连接池耗尽。
2. `http.Get` 后忘记 `defer resp.Body.Close()`。
3. `Shutdown` 返回 `http.ErrServerClosed` 被当成错误。
4. gRPC 客户端每次调用新建连接。
5. 用旧的 `plugins=grpc` 命令，新版本工具链不认。
6. 在 proto 里改字段编号或复用已删除的编号，导致线上新旧版本互相读错数据。〔W10-1 p33-35〕

## 判断题

1. `POST /books/42` 没有注册，为什么返回 405 而不是 404？
2. `TimeoutHandler` 超时后，处理函数里怎么知道要停？
3. 服务端流和双向流的客户端各自要调哪个方法结束？
4. 为什么 `grpc.NewClient` 创建成功不代表服务可达？

练习包：`exercises/p10grpc` 的 `TestUnary`、`TestServerStreamWatch`、`TestClientStreamBulkSet`、`TestBidiChat`（四种流，用内存里的 bufconn 跑真协议栈）、`TestConcurrentUnary`。〔实测〕`go test -race -count=1 ./exercises/p10grpc/...` 通过。

## 在项目里

- `dnsproxi-go/metrics.go` 的 `metricsServer` 返回一个带 `ReadHeaderTimeout` 的 `http.Server`，是 HTTP 服务的最小生产写法的例子（暴露 `/metrics`）。
- 这里没有现成的 gRPC 例子，dnsproxi 走的是 UDP 和 TCP 上的 DNS 协议（见 24 节）。gRPC 的真实使用见练习包 `exercises/p10grpc` 和课程的微服务拆分（W10 系列）。

## 想深入去哪查

- 标准库服务与中间件：TB09，`C:go_basic/http`（响应顺序、流式响应、Cookie、三种路由）。
- gin：W02-1（路由、参数绑定、校验、CORS、middleware）。
- gRPC 与 protobuf：W10-1（选型、IDL、Protobuf 语法）、`C:go_basic/grpc`（四种流、拦截器链、TLS、序列化性能）、`C:go_basic/protoc`（`go_package`、多 `proto_path`）、W13-2（负载均衡与重试）。
- 旧长版：`notes/p10-grpc.md`、`notes/p8-fast-track.md`。
