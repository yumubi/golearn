# 第 10 章 · gRPC：协议先行，四种流，拦截器

> 教程章（liaoxuefeng 式：先跑通，再讲为什么）。读者：从 tonic 过来的人，
> 或第一次写 gRPC 的 Go 学习者。前置：P3（goroutine 泄漏、ctx 取消）。
> 你要写的代码全在 `exercises/p10grpc/`。Rust 对照：tonic，同样的 .proto
> 文件、同样的四流模型，词法不同。

## 这章学完你能干什么

写一个 gRPC 服务加客户端：proto 文件 → `protoc` 生成代码 → 实现服务端
四种模式（一元/服务端流/客户端流/双向流）→ bufconn 集成测试 → 拦截器。
终点是这套东西真实跑起来（实拍在"动手"一节，五个测试 `-race` 全绿）：

```sh
cd golearn
go run ./exercises/p10grpc/          # 终端 1：服务端 :9090
go run ./exercises/p10grpc/client/   # 终端 2：客户端四连
```

正文四节依次回答：从 .proto 到能跑的服务有几步、四种流的签名
怎么读、拦截器和 tower 差在哪、测试为什么走 bufconn。

## 体系背景：RPC 栈的三层叠

gRPC 不是"又一个 HTTP 库"，它是一根三层叠起来的管子：
**HTTP/2**（传输：一条 TCP 连接上多路复用 N 个并发请求，P8 的
握手只发生一次）→ **protobuf**（编码：字段号 + 变长整数，比 JSON
小而快，代价是不可 human-read）→ **stub**（生成代码：把"方法调用"
翻译成"序列化加网络"的样板，Rust 的 trait 和 Go 的 interface 都是
它的皮）。分层视角一句话：gRPC 把 HTTP/2 当运输队、protobuf 当
集装箱、proto 文件当报关单。

这解释了两个后文现象：四种"流"是免费送的（HTTP/2 的 stream 帧
天生支持，gRPC 只是暴露成 API）；测试可以走内存（bufconn，第 4
节）：分层管子的底层换了，上层无感知。

## 从 .proto 到能跑的服务

三步：接口先行、代码生成、服务端实现。每步的产出都看得见。

**第一步：接口先行**。REST 的世界是"URL 先行"（想好路径、动词、状态码）；
gRPC 的世界是"协议文件先行"：先写 `.proto` 声明服务和方法签名，再写
一行 Go。

```proto
service KV {
  rpc Put(PutRequest) returns (PutReply);      // 一元
  rpc Watch(WatchRequest) returns (stream WatchEvent);   // 服务端流
  rpc BulkSet(stream PutRequest) returns (BulkSetReply); // 客户端流
  rpc Chat(stream ChatMsg) returns (stream ChatMsg);     // 双向流
}
```

这一份文件同时是：**文档**（方法清单）、**契约**（类型不匹配编译不过）、
**代码生成器输入**（server stub + client stub 一起出）。tonic 的流程
一模一样：`.proto` → `build.rs` 里 `tonic_build::compile` → 同一个 trait
里四个方法形态。你在 Rust 里学的心智原封不动搬过来。

**一个真实的设计决策**（`kv.proto` 的 `GetReply`）：`found bool`。
Go 没有 `Option`，"未命中"是放进协议字段还是返回 NOT_FOUND 错误码？
本教程选 found 字段（调用方少一层错误处理）；tonic 社区两种都常见。

**第二步：代码生成，一条命令**。工具链两件套（装一次）：

```sh
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
brew install protobuf   # protoc 本体
```

生成（在仓库根目录）：

```sh
PATH="$PATH:$(go env GOPATH)/bin" protoc \
  --go_out=. --go_opt=paths=source_relative \
  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  exercises/p10grpc/kvpb/kv.proto
```

出两个文件：

| 生成文件 | 内容 | 对应 tonic |
|---|---|---|
| `kv.pb.go` | message 结构体（字段、Marshal/Unmarshal） | `pub mod kvpb` 里的 struct |
| `kv_grpc.pb.go` | `KVClient`/`KVServer` 接口 + 注册函数 | `kv_server::Kv` trait + client stub |

**心智模型**：生成代码 = 你"本可以手写"的那堆样板：message 的 getter、
接口断言、编解码。不要读它的实现（两份文件近 800 行），读 proto 就够了。
`option go_package = "...;kvpb"` 里的 `;kvpb` 决定包名，改它就是改
import 路径。

tonic 把生成嵌进 `build.rs`（cargo build 时自动），Go 生成是显式命令：
CI 里要记得跑（或用 buf generate 统一管理）。忘跑生成命令是 Go 这边
最常见的"本地好好的，CI 编译不过"。

**第三步：服务端**。一个 struct 实现生成接口：

```go
type KVServer struct {
    kvpb.UnimplementedKVServer // 向前兼容垫片（见下）
    mu sync.RWMutex
    m  map[string]val
    // ...
}

func (s *KVServer) Put(ctx context.Context, req *kvpb.PutRequest) (*kvpb.PutReply, error) {
    if req.Key == "" {
        return nil, status.Errorf(codes.InvalidArgument, "key required")
    }
    // ... 业务逻辑
    return &kvpb.PutReply{Version: ver}, nil
}
```

四个要点：

1. **错误 = `error`，状态码用 `status.Errorf` 包**：
   `status.Errorf(codes.InvalidArgument, "...")` ≈ tonic 的
   `Err(Status::invalid_argument("..."))`。客户端用 `status.Code(err)` 取回码。
2. **`UnimplementedKVServer` 内嵌**是官方给的向前兼容垫片：proto 加了
   新方法、老二进制没实现时，调用回落到它返回 UNIMPLEMENTED 而不是
   panic。代价：漏实现方法从编译错误降级成运行时错误。教程代码里保留
   它（跟着官方走），但你要知道这个 tradeoff：删掉它，结构体不满足
   生成接口，立刻编译失败。
3. **ctx 贯穿**：客户端断开时 ctx.Done() 触发，长操作里要检查
   （P3 的知识直接复用）。
4. **注册**：`kvpb.RegisterKVServer(srv, server.New())` ≈ tonic 的
   `.serve(lis)`。

## 四种流的签名

生成接口的四个方法签名，扫一眼就懂模式，签名本身就是说明书：

```go
Put(ctx, *Req) (*Resp, error)                    // 一元：一问一答
Watch(*Req, KV_WatchServer) error                // 服务端流：在 stream 上 Send
BulkSet(KV_BulkSetServer) error                  // 客户端流：循环 Recv，EOF 收尾
Chat(KV_ChatServer) error                        // 双向：Recv/Send 随意交错
```

读法：req 和 resp 直接出现在签名里的是一元；出现 `KV_XxxServer` 参数的
是流，请求和响应哪个没出现在签名里，哪个就走 stream。

**服务端流**（Watch）：像订阅。

```go
func (s *KVServer) Watch(req *kvpb.WatchRequest, stream kvpb.KV_WatchServer) error {
    ch := make(chan *kvpb.WatchEvent, 16)
    /* 注册进订阅表 ... */
    for {
        select {
        case ev := <-ch:
            if err := stream.Send(ev); err != nil {
                return err // 客户端断开 → Send 报错 → return，defer 清理订阅
            }
        case <-stream.Context().Done(): // ≈ tokio 里 stream 被 drop 触发取消
            return stream.Context().Err()
        }
    }
}
```

关键纪律：**每次 `Send` 都检查 error**。客户端掉线时 Send 报
`codes.Canceled`，不 return 这个 goroutine 就泄漏了（P3 的 goroutine
泄漏检验法在这里同样适用）。

**客户端流**（BulkSet）：攒一批。

```go
for {
    req, err := stream.Recv()
    if err == io.EOF {                       // 客户端 CloseSend = "我发完了"
        return stream.SendAndClose(&kvpb.BulkSetReply{Count: count})
    }
    if err != nil { return err }             // 真错误
    /* 处理 req ... */
}
```

**双向流**（Chat）：Recv/Send 各自独立，没有顺序约束。tonic 里你会
`split()` 成 sender/receiver 两半各起一个 task；Go 通常一个循环边收边发
（或 goroutine 一个 send 循环 + 主循环 recv）。本质上一样：两个独立队列。

**tonic 迁移误判点**：`RequestStream`/`ResponseStream` 的类型包装没有了，
Go 直接给你 `Recv()/Send()` 方法对。少一层抽象，也少了编译期"谁该调谁"
的检查，纪律靠签名命名（`KV_WatchServer` 只有 Watch 该碰）。

客户端 stub 的四种姿势同构（`exercises/p10grpc/client/main.go`）：

```go
conn, err := grpc.NewClient("127.0.0.1:9090",
    grpc.WithTransportCredentials(insecure.NewCredentials()))
kv := kvpb.NewKVClient(conn)

// 一元
rep, err := kv.Put(ctx, &kvpb.PutRequest{Key: "lang", Value: "go"})

// 服务端流
stream, _ := kv.Watch(ctx, &kvpb.WatchRequest{Key: "lang"})
for { ev, err := stream.Recv(); ... }        // 循环 Recv，err != nil 退出

// 客户端流
cs, _ := kv.BulkSet(ctx)
cs.Send(&kvpb.PutRequest{...})               // Send 可多次
sum, err := cs.CloseAndRecv()                // CloseSend+收汇总 的合体便利方法

// 双向流
bs, _ := kv.Chat(ctx)
bs.Send(&kvpb.ChatMsg{Text: "hello"})
rep, _ := bs.Recv()
bs.CloseSend()
```

注意 `grpc.NewClient`（1.63+ 新 API，替代 `grpc.Dial`）：**它也是懒连接**。
返回的 conn 不立即建 TCP，第一次 RPC 才拨号。"连不上"的错误在第一次
调用冒出来，和 `sql.Open` 一样的坑（P9 学过一次了，这里是第二次）。

## 拦截器：函数洋葱

```go
func LoggingUnaryInterceptor(
    ctx context.Context, req any,
    info *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
) (any, error) {
    resp, err := handler(ctx, req)
    log.Printf("%s err=%v", info.FullMethod, err)
    return resp, err
}

srv := grpc.NewServer(grpc.ChainUnaryInterceptor(server.LoggingUnaryInterceptor))
```

签名固定：`(ctx, req, info, handler) -> (any, error)`。`handler(ctx, req)`
就是"下一个环节"，前面插逻辑 = 请求前处理，后面插 = 响应后处理。

对照 tonic/tower 的 `Layer<Service>`：**tower 是类型参数化的洋葱，
Go 拦截器是签名固定的函数洋葱**。表达力低一档（没有类型级组合），
但一眼看懂、没有 trait bound 泥潭。迁移时的误判点：想写一个带状态的
中间件（比如限流器），tower 靠关联类型在层间传导，Go 这里只能在闭包里
自己抱一个 struct 存状态。

流式有对应物 `StreamInterceptor`（参数多一个 `stream grpc.ServerStream`）；
链式用 `grpc.ChainUnaryInterceptor(a, b, c)`，顺序 = 声明顺序。

## bufconn：内存里的真协议栈

```go
lis := bufconn.Listen(1 << 20)               // 内存"网络"，不占端口
srv := grpc.NewServer()
kvpb.RegisterKVServer(srv, New())
go srv.Serve(lis)                            // 别忘了 t.Cleanup(srv.Stop)

conn, _ := grpc.NewClient("passthrough:///bufnet",
    grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
        return lis.DialContext(ctx)
    }),
    grpc.WithTransportCredentials(insecure.NewCredentials()))
client := kvpb.NewKVClient(conn)             // 真客户端走内存管道
```

三个理由：

1. **真协议栈**：序列化、状态码、流控全走一遍。测试里断言
   `codes.InvalidArgument` 这类错误码，只有在真连接上才可靠，mock stub
   给不了。
2. **不占端口**：`bufconn.Listen` 是内存管道，CI 并行跑多少份都不冲突。
3. **没有真实网络的不确定性**：无握手、无超时抖动，`-race` 下照样快
   而稳。

`passthrough:///bufnet` 是惯用咒语：告诉 grpc 别真的解析这个地址，
走我们给的 dialer。对照 tonic 测试的 in-memory channel：同一个思路。

本教程的五个测试（`server_test.go`）覆盖四流加并发，完整清单见"动手"
一节的练习表。

## 动手：真机冒烟（教程的终点验证）

```sh
$ go run ./exercises/p10grpc/                # 终端 1
2026/10/10 kv grpc on :9090

$ go run ./exercises/p10grpc/client/         # 终端 2
unary  Get(lang) = "go" found=true
stream Watch lang = "go" v1
stream Watch lang = "go1" v2
stream Watch lang = "go2" v3
client-stream BulkSet count = 3
bidi Chat echo = "001 echo: hello bidi"
client done
```

服务端日志（拦截器在工作）：
`/kvpb.v1.KV/Put err=<nil>`，每个一元调用一行。

**预测题**（跑之前写答案）：

1. 在 proto 里新加一个 `rpc Delete`，先不实现它，`go build` 过得过吗？
   客户端调用会收到什么状态码？（§问题 1，`UnimplementedKVServer` 垫片）
2. 服务端没起来时客户端跑四连，错误在哪一行代码冒出来？`NewClient`
   本身会报错吗？（§问题 2，懒连接）
3. Watch 的客户端中途 cancel ctx，服务端 Watch 的 goroutine 靠什么退出？
   两个出口分别是什么？（§问题 2，Send 报错与 `stream.Context().Done()`）

**观察验证**：

```sh
go test -race -v ./exercises/p10grpc/server/   # 五个测试全绿
```

| 练习 | 考什么 | 在哪 |
|---|---|---|
| TestUnary | 一元 + found 语义 + InvalidArgument 断言 | p10grpc/server |
| TestServerStreamWatch | 订阅快照 + 版本号单调 | p10grpc/server |
| TestClientStreamBulkSet | 5 条 Send 后 CloseAndRecv 计数 | p10grpc/server |
| TestBidiChat | 回声严格保序 | p10grpc/server |
| TestConcurrentUnary | 20 并发 Put，-race 干净 | p10grpc/server |

**改造题**：给 KV 加一个 `Delete` 一元方法：proto 加 rpc → 重新跑
protoc → 服务端实现（未命中返回什么由你定，和 `GetReply.found` 的设计
决策对齐）→ bufconn 测试。验收：`go test -race ./exercises/p10grpc/...`
全绿。

## Take-away

1. proto = 契约：文档、类型检查、代码生成输入三合一；Go 这边生成是
   显式命令，CI 别忘跑。
2. 生成代码 = 样板，别读它，读 proto；`UnimplementedKVServer` 是向前
   兼容垫片，代价是漏方法从编译错降级为运行时 UNIMPLEMENTED。
3. 四种流 = 四个签名形状；`Send` 必查错，客户端流以 `io.EOF` 收尾，
   取消看 `stream.Context().Done()`。
4. 拦截器 = 签名固定的函数洋葱（tower 是类型洋葱）；`grpc.NewClient`
   懒连接，错误在第一次 RPC 冒头。
5. 测试走 bufconn：真协议栈、不占端口、无网络抖动，配 `-race` 就是
   gRPC 的标准姿势。

---

## 章末参考

### 与 tonic 总对照表

| | Rust/tonic | Go/grpc-go |
|---|---|---|
| 协议 | .proto + build.rs 自动生成 | .proto + protoc 显式命令 |
| 服务端 | `async fn put(&self, req: Request<T>)` | `func (s) Put(ctx, req)` |
| 错误 | `Result<Response<T>, Status>` | `(*T, error)` + `status.Errorf` |
| 服务端流 | `ResponseStream<T>` 类型包装 | `stream.Send()` 循环 + 检查 err |
| 客户端流 | `RequestStream<T>` | `stream.Recv()` 循环 + `io.EOF` |
| 双向 | `Streaming<A, B>` split 两半 | Recv/Send 直接交错 |
| 中间件 | tower Layer（类型参数化） | 拦截器函数（签名固定） |
| 测试 | in-memory transport | `bufconn` |
| 取消 | drop stream → cancel | `stream.Context().Done()` |
| 生态默认值 | TLS + http2 必选 | 同左；`insecure` 仅测试 |

### 生成文件速查

- `kv.pb.go`：message 的 struct、getter、Marshal/Unmarshal。改字段号
  就是改 wire 协议，动 proto 前先想兼容。
- `kv_grpc.pb.go`：`KVClient`/`KVServer` 接口、`RegisterKVServer`、
  `UnimplementedKVServer`。服务端方法签名以它为准。
- `go_package` 的 `;kvpb` 后半段决定包名；`paths=source_relative` 让
  输出落在 proto 同目录。

### 冒烟实拍说明

上文输出是 2026-10-10 的真机实拍（grpc-go v1.84.0）。客户端四连代码在
`exercises/p10grpc/client/main.go`：先 Put 一次，再起 Watch（快照 v1），
另起 goroutine 改两次值（增量 v2/v3），所以事件序列是 v1、v2、v3。
