---
title: 11 HTTP 与 gRPC：两种接口形态与它们共同的骨架
---

# 11 HTTP 与 gRPC：两种接口形态与它们共同的骨架


> 详细程度：深入 · 约 55 分钟
> 材料：TB09 W02-1 W03-1 W04-1 W05-1 W05-2 W10-1 W13-2、C:go_basic/http C:go_basic/grpc C:go_basic/protoc、旧长版 `notes/p10-grpc.md` `notes/p8-fast-track.md`
> 前置：04 接口与组合，10 分层与依赖注入

## 先看一个反直觉的现象

注册了 `GET /books/{id}` 和 `POST /books` 两条路由之后，`POST /books/42` 应该返回什么？直觉是 404：`POST /books/42` 这个"方法加路径"的组合我根本没注册过。Go 1.22 起的新路由器给出的却是 405。先跑一遍再往下读：

```go
package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /books/{id}", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "book %s", r.PathValue("id"))
	})
	mux.HandleFunc("POST /books", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	for _, req := range []struct{ method, path string }{
		{"GET", "/books/42"},
		{"POST", "/books/42"},
		{"GET", "/nope"},
	} {
		r, _ := http.NewRequest(req.method, srv.URL+req.path, nil)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			panic(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		fmt.Printf("%s %s -> %d %s\n", req.method, req.path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
}
```

```text
GET /books/42 -> 200 book 42
POST /books/42 -> 405 Method Not Allowed
GET /nope -> 404 404 page not found
```

405 而不是 404 的原因：新路由器把"方法和路径"当成一段来匹配。`/books/42` 的路径段命中了 `GET /books/{id}` 里的 `{id}` 通配符，所以这个路径它是"认识"的，只是方法 POST 对不上，于是返回 405，并在响应头里写上 `Allow: GET, HEAD` 声明它支持哪些方法。而 `/nope` 连路径都不认识，才落到 404。

这个细节本身不是重点，它揭开的是整章的骨架：**一个 router 把请求分给一个 handler，handler 外面再套一层层中间件**。HTTP 和 gRPC 是这同一副骨架的两种面孔，区别只在签名。理解了骨架，换标准库、gin、gRPC 只是换签名。

## 和你已知语言的差异

| 问题 | Java / Kotlin | Rust | TS | Go |
|---|---|---|---|---|
| HTTP 处理函数 | Spring `@GetMapping` 方法 | axum `async fn` + extractor | Express `(req, res)` 回调 | 固定原型 `func(w, r)` |
| 横切逻辑怎么写 | Servlet Filter / HandlerInterceptor | tower `Layer<Service>` 洋葱 | Express middleware `(req, res, next)` | `func(http.Handler) http.Handler` |
| RPC 契约 | gRPC-java：`.proto` 生成 stub | tonic：`.proto` 生成 trait | @grpc/grpc-js 生成 | grpc：`.proto` 生成 interface |
| 路由能力放哪 | 框架注解 | 框架宏 | 框架路由表 | 1.22 起标准库自带 |

最陌生的是最后一行：Java 里写 Web 服务几乎默认配一个框架，Go 里标准库 `net/http` 自己就能把路由这件事做完整。这一节先讲标准库能干什么，再讲 gin 到底补了什么。

## 一、HTTP：标准库已经够用

最小服务只需要两个调用：`http.HandleFunc` 注册处理函数，`http.ListenAndServe` 启动监听。处理函数的原型是固定的 `func(w http.ResponseWriter, r *http.Request)`：`r` 是进来的请求，`w` 用来写响应。所有基于标准库的 handler 都长这个样。〔TB09 p3-4〕

2021 年的课程还在为"方法加路径"的路由引入第三方包 gorilla/mux，靠 `Methods("POST")` 和 `mux.Vars` 才能做的事，现在标准库直接支持：〔TB09 p13-14〕〔C:go_basic/http/server/main.go:176-185〕

```go
mux := http.NewServeMux()
mux.HandleFunc("GET /books/{id}", func(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "book %s", r.PathValue("id"))
})
```

`{id}` 是路径通配符，`r.PathValue("id")` 把它取出来；`GET` 前缀限制了方法。方法对不上自动 405，没匹配的路径自动 404，这些都不用自己写。

### 响应顺序是一条铁律

写响应头、状态码、响应体三件事有严格先后：先 `w.Header().Add`，再 `w.WriteHeader(status)`，最后 `w.Write(body)`。〔C:go_basic/http/server/main.go:32-37〕

原因是状态码和 header 在 `WriteHeader` 被调用（或者 `Write` 隐式调用它）的那一刻就定下来了，之后再改 `w.Header()` 已经影响不到发出去的响应头。具体有三条推论：

1. `WriteHeader` 只有第一次生效，第二次不起作用，标准库还会在日志里打一条 `superfluous response.WriteHeader call`。
2. `Write` 之前没调 `WriteHeader`，就隐式按 200 发。
3. `WriteHeader` 之后再 `w.Header().Add` 无效。〔实测〕Go 1.26 下先 `WriteHeader(201)` 再 `Add` 一个头，客户端读不到。

配套的另一半是客户端侧：**用完必须关 `resp.Body`**，否则这条连接不复用，每次请求都新建连接，协程和内存一起泄漏。便捷方法 `http.Get` 也一样要关。〔C:go_basic/http/client/main.go:25〕

### 超时：只设一个远远不够

长耗时处理函数要感知"服务端或客户端已经放弃"，靠的是 `r.Context()`。用 `http.TimeoutHandler` 包一层，超时后处理函数的 context 会被取消：

```go
package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"
)

func main() {
	done := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		select {
		case <-time.After(300 * time.Millisecond):
			w.Write([]byte("finished"))
		case <-r.Context().Done():
			fmt.Printf("handler sees: %v\n", r.Context().Err())
		}
	})
	srv := httptest.NewServer(http.TimeoutHandler(mux, 100*time.Millisecond, "timeout"))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/slow")
	if err != nil {
		panic(err)
	}
	resp.Body.Close()
	<-done
	fmt.Printf("client gets: %d\n", resp.StatusCode)
}
```

```text
handler sees: context deadline exceeded
client gets: 503
```

超时包装器 100ms 一到就向客户端回 503，同时把处理函数的 `r.Context()` 取消，处理函数里的 `r.Context().Err()` 变成 `context deadline exceeded`，它就可以停下。这里也暴露一个生产上的坑：`http.Server` 的 `ReadHeaderTimeout` 只管"读完请求头"，慢速读 body、慢速写响应它都不管。只设一个超时是资源泄漏的入口，`ReadTimeout`、`WriteTimeout`、`IdleTimeout` 要按业务一起填上。

### 优雅退出

套路固定：`signal.Notify` 捕获 SIGINT 和 SIGTERM，收到后用带超时的 context 调 `srv.Shutdown(ctx)`。`Shutdown` 立即关闭所有 listener、关闭空闲连接、等活动连接处理完才返回。〔TB09 p9〕这个行为可以直接跑出来验证：

```go
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	srv := &http.Server{Addr: "127.0.0.1:0", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	fmt.Println("serving")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		time.Sleep(50 * time.Millisecond)
		syscall.Kill(os.Getpid(), syscall.SIGTERM)
	}()
	<-sig
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	fmt.Println("shutdown:", srv.Shutdown(ctx))
	fmt.Println("serve returned:", <-errCh)
}
```

```text
serving
shutdown: <nil>
serve returned: http: Server closed
```

两行关键输出：`Shutdown` 返回 `nil`（正常退出），`ListenAndServe` 随后返回 `http: Server closed`。这个错误是 `http.ErrServerClosed`，是关闭成功的标志，不要当错误处理。`ListenAndServe` 会阻塞，所以必须放进独立 goroutine，再用 channel 把它的返回值传回主 goroutine。〔TB09 p16-17〕

## 二、中间件：一个处理函数加一串包装

前面已经看到 router 和 handler 的分工，现在看第三层：中间件。它的类型签名一句话说清：

```go
func middleware(next http.Handler) http.Handler
```

接受一个 handler，返回一个 handler。返回的那个在调用 `next` 之前做前置逻辑，之后做后置逻辑，于是任意多个中间件可以像洋葱一样套在业务 handler 外面。日志、鉴权、限流、跨域都挂在这一层，业务代码不动。〔TB09 p14-16〕下面同时演示洋葱顺序和响应头顺序：

```go
package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
)

func withLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("before:", r.URL.Path)
		next.ServeHTTP(w, r)
		fmt.Println("after:", r.URL.Path)
	})
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /hello", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("X-Note", "set-before-write")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("hi"))
	})
	srv := httptest.NewServer(withLogger(mux))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/hello")
	if err != nil {
		panic(err)
	}
	resp.Body.Close()
	fmt.Println("client sees X-Note:", resp.Header.Get("X-Note"))
}
```

```text
before: /hello
after: /hello
client sees X-Note: set-before-write
```

`before` 打印在 handler 之前、`after` 在之后，说明中间件确实"包"住了 handler；`X-Note` 在 `WriteHeader` 之前设置所以客户端能读到。这套"处理函数加包装"的骨架，是 gRPC 拦截器在 HTTP 里的对应物，第五部分会看到两边只是签名不同。


## 四、认证：从 Session 到长短 token

中间件是鉴权的挂载点，但鉴权本身教什么？这一节把登录态的完整生命周期讲完：怎么建立（Session/JWT）、怎么维持（过期与刷新）、怎么登出（无状态的代价）、第三方登录怎么接（OAuth2）。〔W03-1 p2〕

### Session：状态存在服务端

登录成功后服务端建一张会话，会话 ID 放进 Cookie，会话数据（user_id 之类）存在服务端存储里。gin-contrib/sessions 的存储实现有 cookie、memstore、redis、gorm 等一排，选型一句话：**单机随便，多实例必须选 redis**，否则实例 0 签发的会话实例 1 认不得。sess_id 永远放 Cookie，Session 数据才是 store 里存的东西，别搞混。〔W03-1 p6-8〕

过期策略有四种：每次访问都刷（性能差）、快过期才刷、固定间隔刷、长短 token。前三种都能用「登录校验中间件顺手刷新」实现，第四种在下面讲。固定过期时间的问题很具体：设 10 分钟，9:59 还能用、10:01 就被踢,这对持续活跃的用户反而体验差。〔W03-1 p16-18〕

### JWT：把状态签进 token

JWT 是三段式：header.payload.signature，前两段是 base64 的 JSON，第三段是对前两段的 HMAC 签名。它的哲学和 Session 正相反：**状态不在服务端，由签名保证不可篡改**。用标准库就能把签发与校验的全过程走一遍：

```go
package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func sign(parts []string, key []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(strings.Join(parts, ".")))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func issue(userID int64, ttl time.Duration, key []byte) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload, _ := json.Marshal(map[string]any{
		"uid": userID,
		"exp": time.Now().Add(ttl).Unix(),
	})
	p := base64.RawURLEncoding.EncodeToString(payload)
	return header + "." + p + "." + sign([]string{header, p}, key)
}

// validate 返回 uid。任何一段被改过，HMAC 都对不上。
func validate(token string, key []byte) (int64, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return 0, fmt.Errorf("格式不对")
	}
	want := sign(parts[:2], key)
	if !hmac.Equal([]byte(want), []byte(parts[2])) {
		return 0, fmt.Errorf("签名不符")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0, err
	}
	var claims struct {
		UID int64 `json:"uid"`
		EXP int64 `json:"exp"`
	}
	if err := json.Unmarshal(raw, &claims); err != nil {
		return 0, err
	}
	if time.Now().Unix() > claims.EXP {
		return 0, fmt.Errorf("已过期")
	}
	return claims.UID, nil
}

func main() {
	key := []byte("test-key-32-bytes-long-xxxxxxxx")
	token := issue(42, time.Minute, key)
	fmt.Println("段数:", len(strings.Split(token, ".")))

	uid, err := validate(token, key)
	fmt.Printf("正常校验: uid=%d err=%v\n", uid, err)

	parts := strings.Split(token, ".")
	ev, _ := base64.RawURLEncoding.DecodeString(parts[1])
	forged := strings.Replace(string(ev), `"uid":42`, `"uid":100`, 1)
	parts[1] = base64.RawURLEncoding.EncodeToString([]byte(forged))
	_, err = validate(strings.Join(parts, "."), key)
	fmt.Println("篡改后:", err)

	_, err = validate(token, []byte("another-key-32-bytes-long-xxxxx"))
	fmt.Println("换密钥:", err)

	expired := issue(42, -time.Second, key)
	_, err = validate(expired, key)
	fmt.Println("过期:", err)
}
```

```text
段数: 3
正常校验: uid=42 err=<nil>
篡改后: 签名不符
换密钥: 签名不符
过期: 已过期
```

四行输出就是 JWT 的全部性质：payload 只是 base64 不是加密（谁都能解开看），但改一个字节签名就废；换密钥等于全量登出；过期是 payload 里的一个时间戳比较。课程接 gin 时用 `golang-jwt/jwt/v5` 的原始 API 而不是插件，因为插件把签发校验包起来反而看不清机制；token 走 `x-jwt-token` 响应头下发、`Authorization: Bearer` 上送，CORS 要相应放行 `AllowHeaders: Authorization` 和 `ExposeHeaders: X-Jwt-Token`，否则浏览器读不到。〔W03-1 p24-30〕

### 登出：无状态的代价

JWT 不可变，"退出登录"没法把已签发的 token 变无效:多实例下登出请求落实例 0、后续请求落实例 1，任何单机手段都拦不住。唯一根治是外部记录：Redis 里记一组**不可用**的 ssid（UUID，登录时同时签进长短两个 token），登出时 `SET ssid` 进去，登录校验和 refresh 都查它。记"不可用"而不是"可用"，因为退出比登录低频得多，黑名单几百条，白名单上百万条。〔W05-2 p14-15〕

黑名单只解决"主动登出"，没解决"token 被盗"。方案是长短双 token：access token 短（几十分钟），高频携带易泄露；refresh token 长（7 天）且只在登录和刷新两个低频场景出现，不易泄露。前端拿到 401 就调 refresh 接口换新短 token，登录校验中间件里的自动续期逻辑删掉。**refresh 的价值恰恰在于它低频**；要是放在中间件里每次请求自动换发，那就和续期没区别，白做了。长 token 也泄露的缓解：把 User-Agent 等登录时信息签进去，或每次刷新轮换长 token，都只能缓解。〔W05-2 p3-8〕

### OAuth2 扫码：第三方登录是授权的特例

微信扫码登录本质是 OAuth2 授权码流程，三步：跳微信的授权页（后端拼 URL）、用户扫码确认、微信带着 `code` 回调你注册的 `redirect_uri`，后端再拿 `code+appid+secret` 换 `access_token`，最后 open_id 走 FindOrCreateByWechat 建账号。拼 URL 的五个参数：appid、redirect_uri（必须是注册时填的域名）、response_type=code、scope=snsapi_login、state（随机 UUID）。〔W05-1 p4-16〕

两个细节最容易被糊弄过去。其一，**URL 由后端拼**而不是前端：state 要在后端校验，后端拼了改起来才方便。其二，state 防的是 CSRF 登录绑定攻击：攻击者先用自己的微信走一遍授权拿到 code，伪造页面诱导已登录用户点击，把攻击者的微信绑到受害者账号上；有 state（登录时发、回调时验）这条路就走不通。换回来的字段有两组：授权侧 access_token/expires_in/refresh_token，ID 侧 open_id/union_id。同一用户在 A、B 两个产品各有 open_id，union_id 全公司同一个，多产品账号打通靠它。你还能认出来：**微信的 access/refresh 就是长短 token 的原型**，自研双 token 方案就是从这里抄的作业。〔W05-1 p20-22〕

### 压测登录：先测再优化

登录优化最容易犯"上来就加缓存"。课程用 wrk 压注册/登录/Profile 三接口，定位到瓶颈是 bcrypt 的 CPU 耗时加数据库查询；给 Profile 读路径配了业务专属 UserCache（封装过期时间/key 结构/序列化协议），Repository 层组合 DAO+Cache 并做 Redis 故障降级；而登录本身是低频操作，**结论是不值得缓存**。先压测找到真瓶颈，再决定动哪里。优化顺序比优化手段重要。〔W04-1 p2〕


## 五、gin 在标准库之上补了什么

标准库解决的是"路由器加 handler 模型"，gin 补的是路由注册的便利和参数处理：参数绑定与校验（`Bind` 系列）、路由分组、中间件链、更顺手的响应方法。它的核心类型是 `gin.Context`，一个对象同时承担读请求和写响应，日常打交道最多的就是它。〔W02-1 p6〕〔W02-1 p10〕`gin.Default()` 自带 Logger 和 Recovery 两个中间件，`gin.New()` 是裸引擎。

组织路由的惯例是每个业务一个 `Handler` 结构体，写一个 `RegisterRoutes(server)` 自注册，公共前缀用 `server.Group` 防手抖。〔W02-1 p21-25〕gin 的 middleware 就是 `func(*gin.Context)` 的衍生类型，和标准库一样适合所有业务都关心的横切问题：CORS、日志、限流、可观测。〔W02-1 p44-45〕

一个典型的横切例子是跨域：浏览器先发一个 `OPTIONS` 预检请求试探，服务端在响应头里用 `Access-Control-Allow-*` 表态，所以 CORS 中间件必须能处理预检请求本身，不能让它落到鉴权之类的后续逻辑里。〔W02-1 p41-42〕（只有跨域的"非简单请求"，比如带 JSON `Content-Type` 的 POST，浏览器才会先发预检。）

标准库和 gin 怎么选：服务简单、想少依赖，1.22 以后的 `ServeMux` 够用；团队已经熟悉 gin 或者需要它的绑定校验时选 gin。两边的 handler 都能套进同样的"中间件等于包装函数"思路，没有谁更"正确"。

## 六、gRPC：契约先行

选型上，资料给的口诀是"遇事不决选 gRPC"：利用 HTTP/2 的双向流，支持流控、压缩，跨语言，社区强。〔W10-1 p26〕（该页还写了"基于 QUIC"，这是错的，gRPC 跑在 HTTP/2 上。）"契约先行"指先写 `.proto`（接口描述语言，平台无关），再用编译器生成各语言的客户端和服务端代码；序列化用 Protobuf，二进制格式，课程的说法是序列化速度和压缩效率在同类协议里名列前茅。〔W10-1 p27-29〕

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

`stream` 写在请求前、响应前还是两边，决定四种形态：一元、服务端流、客户端流、双向流。读法：请求前写了 `stream`，客户端就是流式发送；响应前写了 `stream`，服务端就是流式返回；两边都写就是双向流。四种流的生命周期有一处不对称，最容易被忘：**客户端创建的流最终要调 `CloseSend`（双向流）或 `CloseAndRecv`（客户端流），服务端不用关**。课程里的类比是 HTTP 客户端要关 response body，而服务端不用关 request body。〔C:go_basic/grpc/client/main.go:119〕〔C:go_basic/grpc/client/main.go:155〕

### 拦截器链：签名变了，骨架没变

`grpc.ChainUnaryInterceptor(a, b, c)` 把多个拦截器串成链，单个拦截器的签名是 `(ctx, req, info, handler)`，和 HTTP 中间件是同一个思路：调 `handler(ctx, req)` 之前是前置逻辑，之后是后置逻辑。〔C:go_basic/grpc/server/main.go:61〕客户端拦截器的签名里是 `invoker` 而不是 `handler`。

下面的例子不引真 gRPC（那需要生成代码，测试跑不了），只复刻拦截器链的形状，看它和 HTTP 中间件是同一副洋葱：

```go
package main

import "fmt"

// 模拟 grpc.UnaryHandler 和一元拦截器的形状：
// 真实签名是 (ctx, req, info, handler)，这里只留 req 和 handler。
type unaryHandler func(req string) string
type unaryInterceptor func(req string, next unaryHandler) string

func withLog(name string) unaryInterceptor {
	return func(req string, next unaryHandler) string {
		fmt.Println(name, "before")
		out := next(req)
		fmt.Println(name, "after")
		return out
	}
}

func main() {
	handler := func(req string) string { return "resp:" + req }

	chain := func(req string) string {
		steps := []unaryInterceptor{withLog("a"), withLog("b")}
		h := handler
		for i := len(steps) - 1; i >= 0; i-- {
			next := h
			step := steps[i]
			h = func(r string) string { return step(r, next) }
		}
		return h(req)
	}

	fmt.Println(chain("hi"))
}
```

```text
a before
b before
b after
a after
resp:hi
```

`a` 包在 `b` 外面：请求先经过 a 再经过 b，响应先经过 b 再经过 a。`ChainUnaryInterceptor(a, b)` 的执行顺序就是这个声明顺序。练习包 `exercises/p10grpc` 里的 `LoggingUnaryInterceptor` 是真实的最小例子，签名里多了 `info *grpc.UnaryServerInfo`，作用是在 `handler` 返回后打一条方法名加错误的日志。

### 几条实践

1. **一个 `*grpc.ClientConn` 可以被多个 client 共用**（HTTP/2 多路复用），不要每次调用都新建。`grpc.NewClient` 是惰性连接，调用级超时用 `context.WithTimeout`。〔C:go_basic/grpc/client/main.go:43-69〕
2. **负载均衡默认是 `pick_first`**：课程的演示里永远只用 resolver 解析出的第一个节点（严格说是第一个连得上的地址，这一点我没在本机验证），等于没有均衡。要均衡必须在 service config 里指定 `round_robin` 等。〔W13-2 p4〕〔W13-2 p5〕
3. **重试靠 service config 的 `retryPolicy` 加负载均衡**：调用不通就重试，重试时会再经过一遍负载均衡，同时要求负载均衡策略别再挑中已经不可用的节点；`retryableStatusCodes` 通常只填 `UNAVAILABLE`。〔W13-2 p29〕
4. **生成代码的命令已经变了**：课程里的 `--go_out=plugins=grpc` 是旧写法，现行是独立的 `protoc-gen-go-grpc` 插件配 `--go-grpc_out`。〔C:go_basic/grpc/idl/service/student_service.proto:46-47〕
5. **`go_package` 必须带 `.` 或 `/`**：分号前是输出路径，分号后是包名。〔W10-1 p34〕〔C:go_basic/grpc/idl/model/student.proto:5〕

## 七、HTTP 还是 gRPC

| 维度 | HTTP/JSON | gRPC |
|---|---|---|
| 运维与门槛 | 组件少，门槛低，兼容所有客户端 | 需要 proto 工具链，要求更高 |
| 异构与对外 | 浏览器、第三方直接用 | 浏览器不直接支持，需网关 |
| 性能与流 | 够用 | 二进制、HTTP/2、四种流 |
| 契约 | 靠文档或 OpenAPI | `.proto` 是契约本身 |

选型的出发点是一条概念上的澄清：REST 是一种风格（编程风格/架构风格），微服务是一种架构，两者没有必然关系。〔W10-1 p12〕落到实践上的常见分工是：对外和前端用 HTTP，内部服务之间用 gRPC，两边共用同一套"处理函数加中间件"的思路。

## 小结

- HTTP 的最小骨架是 router 加 handler，handler 原型固定为 `func(w http.ResponseWriter, r *http.Request)`；1.22 起标准库路由器自带方法前缀和路径通配符。
- 响应顺序有铁律：先 Header，再 `WriteHeader`，最后 `Write`；客户端用完必须关 `resp.Body`。
- 中间件是 `func(http.Handler) http.Handler`，洋葱式包在 handler 外面；gin 的中间件是 `func(*gin.Context)`，同一个思路。
- gRPC 契约先行：`.proto` 声明四种流，生成客户端和服务端；拦截器是 `(ctx, req, info, handler)` 的链，和 HTTP 中间件同构。
- HTTP 与 gRPC 不是非此即彼，而是"对外用 HTTP、对内用 gRPC、中间件思路共用"。

## 常见坑

HTTP 侧的坑（超时只设一个、忘关 `resp.Body`、把 `http.ErrServerClosed` 当错误）已在上面铁律里讲过，这里只列 gRPC 侧：

1. gRPC 客户端每次调用新建连接，丢掉 HTTP/2 的多路复用。
2. 用旧的 `plugins=grpc` 命令，新版本工具链不认。
3. 在 proto 里改字段编号或复用已删除的编号，导致线上新旧版本互相读错数据。〔W10-1 p35〕

## 判断题与答案

1. `POST /books/42` 没有注册，为什么返回 405 而不是 404？
2. `TimeoutHandler` 超时后，处理函数里怎么知道要停？
3. 客户端流和双向流的客户端各自要调哪个方法结束？
4. 为什么 `grpc.NewClient` 创建成功不代表服务可达？
5. 为什么"登录校验中间件里自动换发新短 token"等于没做长短 token？

**答案**

1. 1.22 路由器按"方法加路径"一起匹配；`/books/42` 命中了 `GET /books/{id}` 的路径段，只是方法对不上，所以回 405 并在 `Allow` 头里声明允许的方法。`/nope` 连路径都没匹配上才回 404。
2. `TimeoutHandler` 超时后取消请求的 `r.Context()`，处理函数里 select `r.Context().Done()` 会收到 `context deadline exceeded`，据此停下不再白干。
3. 客户端流的客户端用 `CloseAndRecv()` 发完并一次性收服务端的汇总响应；双向流的客户端用 `CloseSend()` 声明不再发，然后继续 `Recv` 收剩余消息。服务端两个都不需要主动关闭。
4. `grpc.NewClient` 是惰性连接，创建时不拨号，第一次 RPC 才真正建立连接；所以创建成功只代表参数合法，服务端是否可达要到第一次调用才见分晓。
5. 因为 refresh 的安全价值就在低频：它只该在登录和 401 刷新两个场景出现。放进中间件每次请求都换发，攻击者盗到一个短 token 照样能一直续命，和自动续期没有区别，refresh token 反而多暴露了一次。

练习包：`exercises/p10grpc` 的 `TestUnary`、`TestServerStreamWatch`、`TestClientStreamBulkSet`、`TestBidiChat`（四种流，用内存里的 bufconn 跑真协议栈）、`TestConcurrentUnary`。〔实测〕`go test -race -count=1 ./exercises/p10grpc/...` 通过。

## 在项目里

- `dnsproxi-go/metrics.go` 的 `metricsServer` 返回一个带 `ReadHeaderTimeout` 的 `http.Server`，是 HTTP 服务最小生产写法的例子（独立端口暴露 `/metrics`）。
- 这里没有现成的 gRPC 例子，dnsproxi 走的是 UDP 和 TCP 上的 DNS 协议（见 24 节）。gRPC 的真实使用见练习包 `exercises/p10grpc` 和课程的微服务拆分（W10 系列）。

## 想深入去哪查

- 标准库服务与中间件：TB09，`C:go_basic/http`（响应顺序、流式响应、Cookie、三种路由）。
- gin：W02-1（路由、参数绑定、校验、CORS、middleware）。
- gRPC 与 protobuf：W10-1（选型、IDL、Protobuf 语法）、`C:go_basic/grpc`（四种流、拦截器链、TLS、序列化性能）、`C:go_basic/protoc`（`go_package`、多 `proto_path`）、W13-2（负载均衡与重试）。
- 认证：W03-1（Session 存储与刷新、JWT 结构与 CORS 配合）、W05-2（长短 token 与 ssid 登出）、W05-1（OAuth2 扫码与 state）、W04-1（wrk 压测登录与 UserCache 降级）。
- 旧长版：`notes/p10-grpc.md`、`notes/p8-fast-track.md`。
