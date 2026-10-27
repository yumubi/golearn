# 11 HTTP 与 gRPC：两种接口形态与它们共同的骨架

> 详细程度：深入 · 约 90 分钟
> 材料：TB09 W02-1 W03-1 W04-1 W04-2 W05-1 W05-2 W10-1 W13-2、C:go_basic/http C:go_basic/grpc C:go_basic/protoc、旧长版 `notes/p10-grpc.md` `notes/p8-fast-track.md`
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

客户端用完必须关闭 `resp.Body`，释放资源。HTTP/1.x 中通常还需把响应体读到 EOF 才能复用连接；提前关闭可能使连接无法复用。便捷方法 `http.Get` 也一样要关。〔C:go_basic/http/client/main.go:25〕

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


## 三、认证：从 Session 到长短 token

中间件是鉴权的挂载点，但鉴权本身教什么？这一节把登录态的完整生命周期讲完：怎么建立（Session/JWT）、怎么维持（过期与刷新）、怎么登出（无状态的代价）、第三方登录怎么接（OAuth2）。〔W03-1 p2〕

### Session：状态存在服务端

常见服务端 Session 把会话 ID 放进 Cookie，会话数据存在服务端存储。gin-contrib/sessions 也有 cookie store：它把会话数据放在客户端 Cookie 中，不属于这种服务端存储模式。多实例使用服务端会话时，可以选 Redis 等共享存储，或者设计其他一致的会话访问方式；各实例还需共享合适的密钥。〔W03-1 p6-8〕

过期策略有四种：每次访问都刷（性能差）、快过期才刷、固定间隔刷、长短 token。前三种都能用「登录校验中间件顺手刷新」实现，第四种在下面讲。固定过期时间的问题很具体：设 10 分钟，9:59 还能用、10:01 就被踢,这对持续活跃的用户反而体验差。〔W03-1 p16-18〕

### JWT：把状态签进 token

本例使用签名 JWT：header.payload.signature，前两段是 base64url 编码的 JSON，第三段使用 HMAC 签名。JWT 也可以使用非对称签名，并非都用 HMAC；签名提供完整性保护，不提供保密性。下面只演示 HS256 的基本过程：〔W03-1 p22-23〕

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
	if time.Now().Unix() >= claims.EXP {
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

示例说明 payload 可读、篡改后签名不符，以及过期时间的判断；更换密钥会使依赖旧密钥且不再接受旧密钥的 token 失效。它不是生产级 JWT 校验器：生产代码应使用成熟库，限制允许的算法并校验 issuer、audience、过期时间等要求，妥善保管密钥。课程接 gin 时用 `golang-jwt/jwt/v5`；token 通过响应头下发、`Authorization: Bearer` 上送，CORS 要放行相应头。浏览器将 token 存入 localStorage 会暴露于 XSS，存储方案须按威胁模型选择。〔W03-1 p24-30〕

### 登出：无状态的代价

JWT 不可变，"退出登录"没法把已签发的 token 变无效:多实例下登出请求落实例 0、后续请求落实例 1，任何单机手段都拦不住。唯一根治是外部记录：Redis 里记一组**不可用**的 ssid（UUID，登录时同时签进长短两个 token），登出时 `SET ssid` 进去，登录校验和 refresh 都查它。记"不可用"而不是"可用"，因为退出比登录低频得多，黑名单几百条，白名单上百万条。〔W05-2 p14-15〕〔W05-2 p2〕

黑名单只解决"主动登出"，没解决"token 被盗"。方案是长短双 token：access token 短（几十分钟），高频携带易泄露；refresh token 长（7 天）且只在登录和刷新两个低频场景出现，不易泄露。前端拿到 401 就调 refresh 接口换新短 token，登录校验中间件里的自动续期逻辑删掉。**refresh 的价值恰恰在于它低频**；要是放在中间件里每次请求自动换发，那就和续期没区别，白做了。长 token 也泄露的缓解：把 User-Agent 等登录时信息签进去，或每次刷新轮换长 token，都只能缓解。〔W05-2 p3-8〕〔W05-2 p9〕〔W05-2 p10〕〔W05-2 p16-17〕

### OAuth2 扫码：第三方登录是授权的特例

微信扫码登录本质是 OAuth2 授权码流程，三步：跳微信的授权页（后端拼 URL）、用户扫码确认、微信带着 `code` 回调你注册的 `redirect_uri`，后端再拿 `code+appid+secret` 换 `access_token`，最后 open_id 走 FindOrCreateByWechat 建账号。拼 URL 的五个参数：appid、redirect_uri（必须是注册时填的域名）、response_type=code、scope=snsapi_login、state（随机 UUID）。〔W05-1 p4-8〕〔W05-1 p9-12〕〔W05-1 p15-16〕〔W05-1 p23〕

两个细节最容易被糊弄过去。其一，**URL 由后端拼**而不是前端：state 要在后端校验，后端拼了改起来才方便。其二，state 防的是 CSRF 登录绑定攻击：攻击者先用自己的微信走一遍授权拿到 code，伪造页面诱导已登录用户点击，把攻击者的微信绑到受害者账号上；有 state（登录时发、回调时验）这条路就走不通。换回来的字段有两组：授权侧 access_token/expires_in/refresh_token，ID 侧 open_id/union_id。同一用户在 A、B 两个产品各有 open_id，union_id 全公司同一个，多产品账号打通靠它。你还能认出来：**微信的 access/refresh 就是长短 token 的原型**，自研双 token 方案就是从这里抄的作业。〔W05-1 p20-22〕〔W05-1 p24〕〔W05-1 p28-29〕

### 短信验证码登录：一次"检查并写入"的原子化

验证码若允许前导零，6 位数字有一百万种可能，因此仍要严格限流并防暴力破解。第一条安全线是控制发送频率：同一手机号一分钟以内只能发送一次；验证码有效期十分钟；系统本身也有限流。第二条是限制验证尝试：验证通过后立即作废，三次失败后也作废。失败时不暴露剩余次数。〔W04-2 p26〕

代码上这两条线分别落在两个方法里：在 service 包里放一个 CodeService，里面只定义两个接口。`Send(ctx context.Context, biz string, phone string) error`：根据业务、手机号码生成一个随机验证码并发送，在这个接口里要控制住发送频率；`Verify(ctx context.Context, biz string, phone string, inputCode string) (bool, error)`：验证验证码，在这个接口里要保证验证码不会被暴力破解。两个接口天然按"业务 biz + 手机号"两个维度组织，发送与验证各自的安全职责分别落在两个方法上，而不是混在一个接口里。〔W04-2 p27〕

验证码是一个有有效期的东西，最适合的存储就是 Redis，并设置过期时间十分钟，key 设置为 `phone_code:$biz:$phone` 的形态。发送的思路按 key 状态分四种情况：Redis 中没有这个 key，直接发送；有 key 但没有过期时间，说明系统异常（p29 伪代码注释是"可能有同事手贱改了数据"）；key 有过期时间但剩余过期时间还大于 9 分钟，说明一分钟内已经发过一次，发送太频繁，拒绝；否则重新发送一个验证码。课程先给出的 SendV1 版本按这套思路写：先 `repo.Get` 查验证码，再按上述分支处理。但作者明确说"不行，因为有并发问题"，这就引出下一页的并发分析。〔W04-2 p28〕〔W04-2 p29〕

典型的多发场景：线程 1、线程 2 先后查询 Redis，都看到"没有发过验证码"，于是各自发送，同一手机号收到两条短信。这就是"检查数据-做某事"场景的并发竞态。这种并发属于业务层面上、分布式环境下的并发，不是语言层面上的并发，所以不能使用 channel 或者 sync.Lock 来解决；解法是把"检查并且做某事"的逻辑在 Redis 层面上用 lua 脚本封装成一个整体操作，Redis 之所以安全，是因为它是单线程的，请求挨个执行。Redis 自己发不了验证码，所以约定：验证码在 Redis 里面存好了，就认为可以发送出去了。作者提醒：平时编程中但凡涉及"检查数据-做某事"的场景，都要考虑并发安全问题。〔W04-2 p30〕〔W04-2 p31〕

lua 脚本入参：KEYS[1] 是要发送到的 key（code:业务:手机号形态的验证码 key），`cntKey = key .. ":cnt"` 存验证次数，ARGV[1] 是生成的验证码；脚本先执行 `local ttl = tonumber(redis.call("ttl", key))`，之后按 ttl 走三个分支：ttl == -1 说明 key 存在但没有过期时间（有人误操作，导致 key 冲突），返回 -2；ttl == -2（key 不存在）或者 ttl < 540（已发过一个验证码，且已经超过一分钟）时，依次 set key 为验证码、expire key 600 秒、set cntKey 为 3、expire cntKey 600 秒，返回 0（注释提醒：后续如果验证码有不同的过期时间，要在这里优化）；否则说明已经发送了一个验证码但还不到一分钟，返回 -1。三个返回值的语义：0 = 检查通过且已写入，可以发送；-1 = 发送太频繁；-2 = 系统异常，宁可拒绝服务也不猜。数字的来历：600 秒对应验证码十分钟有效期；剩余 TTL 要小于 540 才允许重发，也就是上一条验证码发出至少一分钟之后。"一分钟一条"的频率线就是这样落进数字里的。cntKey 在这里被初始化为 3，它存的就是验证次数。〔W04-2 p32〕

SendCode 的调用链路是：CodeService 生成验证码 → CodeCache 存储验证码 → CodeRepository 执行 lua 脚本（在 Redis 中检查验证码并存储）；lua 执行 OK 之后，CodeService 再调用 sms（SmsService）发送验证码，发送 OK 结束。作者强调：基本上业务逻辑都在 lua 脚本，Send 方法真的要做的事情，也就是调用了 sms 来真的发送验证码。〔W04-2 p33〕

验证验证码的流程三步：查询验证码，如果不存在说明还没发；存在且验证次数少于等于 3 次，比较输入的验证码和预期验证码是否相等；次数大于 3 次直接返回不相等。由此可以看出为什么发送验证码时要额外存储一个 cnt 字段。它就是验证次数。验证验证码同样要小心并发问题，所以也用 lua 脚本来封装逻辑。验证 lua 的三个分支：cnt <= 0，说明验证次数已经耗尽，返回 -1，不执行任何写命令；code == expectedCode（验证码相等）时不能删除验证码 key。删了就有可能有人跟你过不去、立刻再次发送验证码，所以把 cntKey set 成 -1，认为验证码不可用，返回 0；不相等（可能用户手一抖输错）时 DECR cntKey，返回 -2。相等分支的作废方式是反直觉的点：发码脚本把"key 不存在"当作"从没发过"，删码等于把一分钟冷却线一起抹掉；把 cntKey 置成 -1 一举两得，验证端 cnt ≤ 0 永远返回"次数耗尽"，旧码再也不能通过，发码端 key 还在，剩余 TTL 照常走完冷却，同时挡住"用旧码继续验证"和"删码后立刻重发"两条路。作者自认粗糙点：这个脚本并没有严格区分"验证码还没发"和"验证了太多次"两种情况。〔W04-2 p34〕〔W04-2 p35〕

按照 DDD 的理论，或者按照一般的业务开发理论，前面放在 lua 脚本里的逻辑（检测、校验、保证使用安全和系统安全）应该放在 CodeService 上；然而 CodeService 在这里并不好做，因为它难以解决（分布式）并发问题，因此这段逻辑被放到了 Redis 的 lua 脚本里面。代价是牺牲可移植性：但凡换一个缓存，比如 memcache，就得重新写一遍这个逻辑；把 CodeService 留成薄封装，正是为了把替换缓存时的改动成本控住。〔W04-2 p37〕

用锁模拟 Redis 的单线程语义跑一遍：

```go
package main

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrTooMany   = errors.New("发送太频繁")
	ErrKeyBroken = errors.New("key 异常,拒绝服务")
)

// store 模拟 Redis 执行 lua:检查与写入之间插不进别的命令。
type store struct {
	mu      sync.Mutex
	codeTTL map[string]time.Duration
}

func (s *store) send(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ttl, ok := s.codeTTL[key]
	switch {
	case ok && ttl == -1: // 有 key 无过期时间:疑似误操作,拒绝
		return ErrKeyBroken
	case !ok || ttl <= 9*time.Minute: // 没发过,或冷却已过
		s.codeTTL[key] = 10 * time.Minute
		return nil
	default: // 冷却期内
		return ErrTooMany
	}
}

func main() {
	s := &store{codeTTL: map[string]time.Duration{}}
	fmt.Println(s.send("login:138"))
	fmt.Println(s.send("login:138"))
}
```

```text
<nil>
发送太频繁
```

校验端有个反直觉的细节：验证成功后不能删 key，而是把计数键置成 -1 作废，key 还在但永远校验不过。如果直接删掉，"删除后立刻再发一条"的绕过路径就打开了。〔W04-2 p34-35〕另一个是并发注册：新手机号首次登录，`FindOrCreate` 两个请求同时发现"没有这个用户"就双建账号，靠手机号唯一索引兜底，第二个插入失败转查询。〔W04-2 p45-46〕最后是业务隔离：登录、改密码、危险操作二次验证都用验证码，key 里必须带 biz 维度（`phone_code:$biz:$phone`），各业务冷却与有效期独立；验证码服务和短信发送服务也值得拆开，短信供应商可能从腾讯换成阿里，验证码逻辑不该跟着改。〔W04-2 p10-11〕〔W04-2 p27-28〕登录 handler 本身只是编排：Verify 通过就 `FindOrCreate` 再 `setJWTToken`，ok=false 提示验证码错误，err 分系统异常与验证码错误两路。〔W04-2 p44〕

发码和登录两个接口都挂在 UserHandler 上（`POST /login_sms/code/send` 与 `POST /login_sms`），UserHandler 同时聚合 CodeService 和 UserService；严格做法是再封一个 UserAggrService，但课程认为还没复杂到那步。发码 handler：正则与非空校验手机号、调 CodeService.Send、按 err 分流（nil 成功、ErrCodeSendTooMany 提示太频繁、default 系统错误并打日志）。〔W04-2 p41-43〕

### 压测登录：先测再优化

登录优化最容易犯"上来就加缓存"。课程用 wrk 压注册/登录/Profile 三接口，定位到瓶颈是 bcrypt 的 CPU 耗时加数据库查询；给 Profile 读路径配了业务专属 UserCache（封装过期时间/key 结构/序列化协议），Repository 层组合 DAO+Cache 并做 Redis 故障降级；而登录本身是低频操作，**结论是不值得缓存**。先压测找到真瓶颈，再决定动哪里。优化顺序比优化手段重要。〔W04-1 p12〕〔W04-1 p14-15〕〔W04-1 p19〕〔W04-1 p2〕


## 四、gin 在标准库之上补了什么

标准库解决的是"路由器加 handler 模型"，gin 补的是路由注册的便利和参数处理：参数绑定与校验（`Bind` 系列）、路由分组、中间件链、更顺手的响应方法。它的核心类型是 `gin.Context`，一个对象同时承担读请求和写响应，日常打交道最多的就是它。〔W02-1 p6〕〔W02-1 p10〕〔W02-1 p12〕`gin.Default()` 自带 Logger 和 Recovery 两个中间件，`gin.New()` 是裸引擎。每个 HTTP 方法一个注册快捷方法（GET/POST/DELETE/PATCH/PUT/OPTIONS/HEAD/Any/Match），参数都是路由规则加处理函数。

组织路由的惯例是每个业务一个 `Handler` 结构体，写一个 `RegisterRoutes(server)` 自注册，公共前缀用 `server.Group` 防手抖。集中注册（main 里全注册）打开文件就能看全所有路由但找起来费劲，分散注册（handler 自己注册）有条理但找路由难，两个都要认识。〔W02-1 p21-25〕gin 的 middleware 就是 `func(*gin.Context)` 的衍生类型，和标准库一样适合所有业务都关心的横切问题：CORS、日志、限流、可观测。〔W02-1 p44-45〕

一个典型的横切例子是跨域：浏览器先发一个 `OPTIONS` 预检请求试探，服务端在响应头里用 `Access-Control-Allow-*` 表态，所以 CORS 中间件必须能处理预检请求本身，不能让它落到鉴权之类的后续逻辑里。〔W02-1 p41-42〕（只有跨域的"非简单请求"，比如带 JSON `Content-Type` 的 POST，浏览器才会先发预检。）不做处理时浏览器直接拦截并报 `net::ERR_FAILED`、`No 'Access-Control-Allow-Origin' header`，业务请求根本不会发出。〔W02-1 p40〕

标准库和 gin 怎么选：服务简单、想少依赖，1.22 以后的 `ServeMux` 够用；团队已经熟悉 gin 或者需要它的绑定校验时选 gin。两边的 handler 都能套进同样的"中间件等于包装函数"思路，没有谁更"正确"。

## 四点五、RPC 的三块基石:序列化、粘包、请求关联

前面几节一直在"框架给你什么"的层面看接口形态。gRPC 之所以用起来顺手,是因为它把三件底层的事都替你做完了:把结构体变成字节流(序列化)、在字节流上认出一条条消息(边界)、把响应送回正确的等待者(请求关联)。课程里有一个手写的迷你 RPC,把这三件事全部摊开在源码里,读完再看 gRPC,就能看清它替你做了什么。〔C:dachang/my_rpc/serialization/my_serializer.go:40-97〕

### 序列化:一个接口三个引擎

迷你 RPC 的序列化层只约定两个方法,`Marshal(object any) ([]byte, error)` 和 `Unmarshal(stream []byte, object any) error`,Gob、JSON、手写二进制三个实现全挂在这条最小契约上:〔C:dachang/my_rpc/serialization/serializer.go:3-5〕

| 实现 | 底层引擎 | 写法 |
|---|---|---|
| `Gob{}` | 标准库 `encoding/gob` | `bytes.Buffer` 上套 `gob.NewEncoder/NewDecoder` 做 `Encode/Decode`〔C:dachang/my_rpc/serialization/gob.go:9-20〕 |
| `Json{}` | `bytedance/sonic` | `Marshal/Unmarshal` 直接转发给 sonic〔C:dachang/my_rpc/serialization/json.go:3-15〕 |
| `MySerializer{}` | 手写五段式二进制协议 | 反射取字段再走参数化编解码,下一小节展开〔C:dachang/my_rpc/serialization/my_serializer.go:183-196〕 |

切换实现不碰任何业务代码,`common.go` 里一个全局变量说了算,生效行之外的两行注释着,换实现挪个注释就行:〔C:dachang/my_rpc/common.go:14-16〕

```go
var Serializer = serialization.MySerializer{}

// var Serializer = serialization.Json{}
// var Serializer = serialization.Gob{}
```

测试对象是一个混合了五种基础类型的 `RpcData`:`A int`、`B float32`、`C bool`、`D float64`、`E string`、`Id string`,外加一个故意放进去的小写字段 `f int`。源码注释明说:可导出的字段才会被序列化,`f` 不可导出,不进字节流〔C:dachang/my_rpc/common.go:8-13〕;序列化和反序列化两头的反射循环都用 `IsExported()` 把非导出字段跳过去〔C:dachang/my_rpc/serialization/my_serializer.go:186-196〕。这带来一个容易忽略的约束:字节流里只有"一列导出字段的值",没有字段名。两端结构体的导出字段集(顺序加类型)必须对齐,否则反序列化就整体错位。gRPC 用 `.proto` 契约给两端发同一份生成代码,杜绝的正是这一类错位。

### 手写五段式二进制协议

`MySerializer` 的协议按传输顺序分五段,序列化入口是批量的 `MarshalArguments(arguments ...any)`:〔C:dachang/my_rpc/serialization/my_serializer.go:40-97〕

| 段 | 内容 | 占用 |
|---|---|---|
| ① | 魔数 `MAGIC` | 5 字节,`{37, 61, 57, 23, 111}`〔C:dachang/my_rpc/serialization/my_serializer.go:21〕 |
| ② | 参数个数 | 8 字节,大端 int64 |
| ③ | 每参数 1 字节类型枚举 | `iota` 顺序 `TYPE_INT=0/TYPE_BOOL/TYPE_FLOAT32/TYPE_FLOAT64/TYPE_STRING`〔C:dachang/my_rpc/serialization/my_serializer.go:9-15〕 |
| ④ | 每参数 8 字节长度 | 大端 int64 |
| ⑤ | 所有参数内容按序顺排 | 各参数长度之和 |

两个实现细节值得点出来。第一,一个 `int` 参数写进流之前被显式转成 `int64` 再 `binary.Write`(源码注释:`binary.Write` 要求定长类型,`int` 的长度跟机器有关):〔C:dachang/my_rpc/serialization/my_serializer.go:48-69〕

```go
case int:
	err := binary.Write(buffer, binary.BigEndian, int64(v))
```

第二,拼包是先攒后拼:类型 switch 循环里各参数内容先写进 scratch buffer,最后才按 ①→④ 写头、在尾部追加内容 buffer,总容量按 `len(MAGIC)+8+len(types)+8*len(types)+内容` 一次预分配〔C:dachang/my_rpc/serialization/my_serializer.go:86-95〕。用两个参数 `(int 123, string "go")` 手拼一遍,十六进制是 `25 3d 39 17 6f 00…02 00 04 … 00 7b 67 6f`,共 41 字节:开头 5 字节是魔数,接着 8 字节个数 2,然后类型枚举 `00 04`(int 和 string),两个 8 字节长度(8 和 2),最后 `00 00 00 00 00 00 00 7b` 是 123、"go" 是字符串,harness 复现了同一布局的十六进制输出〔实测:work/run/C_dachang__my_rpc/E1/main.go:9-25〕。

反序列化入口 `UnmarshalArguments` 的检查是层层递进的:总长小于 `len(MAGIC)+8` 报"数据流长度太短";头 5 字节不等于魔数报"魔数校验失败"并附收到的字节;解出参数个数 `n` 后,`n<=0` 直接返回 `(nil, nil)`,是空结果而不是 error;随后在"读类型枚举之前"和"累计完所有长度字段、算出内容总长之后"各做一次剩余长度检查,源码注释写明动机:每获得一个信息就检查一下数据长度,免得将来发生越界 panic(index out of range)。〔C:dachang/my_rpc/serialization/my_serializer.go:100-181〕第二次检查的位置值得体会:它发生在"已知 totalLen"和"真正切内容"之间:拿到长度表之后、消费任何内容字节之前,先确认剩下的字节够不够。外层 `MySerializer.Unmarshal` 还要先过"必须传指针""必须传结构体指针"两道类型检查,再进入这套防御流程〔C:dachang/my_rpc/serialization/my_serializer.go:203-217〕。魔数加长度检查,正是二进制协议不吃陌生字节流的通行做法;gRPC 消息前的压缩标志和 4 字节长度前缀,是同一思路的工业版。

### TCP 粘包与魔数分隔

有了序列化还差一步:字节流上怎么认出一条消息的边界。TCP 是面向字节流的,一次 `Read` 到的数据可能包含多个报文,也可能只包含半个报文,一条报文在什么地方结束需要通信双方事先约定好。传输层这里约定的分隔符是 `MAGIC = []byte{1,1,5,2,0}`。注意它与上一小节序列化协议里的魔数 `{37, 61, 57, 23, 111}` 是两个不同的常量,各自管各层的事。〔C:dachang/my_rpc/transport/server/tcp.go:25-28〕〔C:dachang/my_rpc/transport/common.go:15〕

服务端每次 `Read` 用 256 字节缓冲(注释:设定一个最大长度,防止 flood attack),跨 `Read` 保留一个 `bytes.Buffer` 攒半包,对每次读到的 data 用 `bytes.Index(data, MAGIC)` 循环切分。切分循环三分支:〔C:dachang/my_rpc/transport/server/tcp.go:29-47〕

1. **分隔符在本次读的中间**(`pos > 0`):buffer 里的旧半包加 `data[:pos]` 合成一条完整报文,打印并 `Reset`,然后从分隔符之后继续找下一条。
2. **本次读一开头就是分隔符**(`pos == 0`):若 buffer 非空,说明上次的半包到这条分隔符为止凑齐了,打印并 `Reset`;buffer 为空说明收到一条空报文,跳过即可。
3. **本次读里找不到分隔符**:整段余量写进 buffer 当半包攒着,退出内层循环等下一次 `Read` 拼后续字节。

演示场景里客户端连发 3 条 `"china"+MAGIC` 就关闭，服务端延迟读取以增加多条消息合并到一次读取的机会。〔C:dachang/my_rpc/transport/client/tcp.go:17-20〕〔C:dachang/my_rpc/transport/server/tcp.go:28-31〕但上述示例只在每次读取的数据中搜索分隔符：分隔符跨两次 Read 时可能漏掉，正文中出现分隔符也会误切；固定读取缓冲区也不能限制累计 buffer 的大小。生产协议应处理跨读取边界、限制帧大小，并使用长度前缀或明确的转义规则。

### UDP 报文语义与 xid 请求关联

Echo 示例的传输层选了 UDP,理由写在构造函数注释里:UDP 面向报文实现方便,若用 TCP 需自行按魔数对字节流进行分隔,上文的切分循环就是"若用 TCP"的代价。一次 `Read` 恰好对应一条 datagram,天然没有粘包问题。〔C:dachang/my_rpc/client/echo_client.go:22-27〕

报文语义的代价是"请求-响应不再自动成对":多条响应乱序到达时,谁也说不清哪条响应属于哪个请求。`EchoClient` 的解法是给每个请求发唯一号,靠号回填:〔C:dachang/my_rpc/client/echo_client.go:15-33〕

- 结构体持有 `conn`、`serializer` 和 `sync.Map` 类型的 `requestBuffer`;构造函数 `DialTimeout("udp", …, 30min)` 建连后启动常驻接收协程 `go client.receive()`,此后收发完全异步。
- `Call` 五步:① `xid.New().String()`(rs/xid 分布式唯一 id 生成器)生成 id 写进 `request.Id`;② 序列化整个请求;③ 建容量 1 的 `chan my_rpc.RpcData`,以 id 为 key `Store` 进 `requestBuffer`;④ `conn.Write(data)` 发出请求;⑤ 阻塞在 `resp := <-streamCh` 直到响应送达。〔C:dachang/my_rpc/client/echo_client.go:60-79〕
- `receive` 协程循环:4096 字节缓冲 `Read` → 反序列化成 `RpcData` → 按 `resp.Id` 到 `requestBuffer.Load`;命中则**先 `Delete(resp.Id)` 再把 resp 推进取出的 chan** 唤醒等待方。先删再投,保证派发后 entry 不残留,防止 `sync.Map` 泄漏;未命中(超时后迟到的响应或 id 对不上)只打日志"request buffer里没有id %s",不投递。〔C:dachang/my_rpc/client/echo_client.go:37-56〕
- `Call` 定义为指针接收者,因为要修改成员 `requestBuffer`。〔C:dachang/my_rpc/client/echo_client.go:59-60〕
- main 里 10 个 goroutine 各持独立 EchoClient 串行调 10 次 `Call`,校验 `resp.A` 与发出的随机数相等,验证了并发下"按 id 归位"依然成立。〔C:dachang/my_rpc/client/echo_client.go:82-106〕

服务端那一半:每收到一条 datagram 就 `go server.handle(...)` 并发处理,handle 里随机 `Sleep` 0 到 500ms,故意让"先请求的不一定先拿到响应",逼出客户端按 id 归位的必要性;然后原样把数据序列化回给对方。〔C:dachang/my_rpc/server/echo_server.go:45-59〕

### 三种引擎的速度账

基准测试对象是上面那个 `RpcData`,每次 op = 一次 `Marshal` 加一次 `Unmarshal` 完整往返:〔C:dachang/my_rpc/serialization/test/serializer_test.go:10-17〕〔C:dachang/my_rpc/serialization/test/serializer_test.go:96-117〕

| 引擎 | 作者机器〔C:dachang/my_rpc/serialization/test/serializer_test.go:112-117〕 | 本机复测 |
|---|---|---|
| Json(sonic) | 2221 ns/op | 863 ns |
| Gob | 16154 ns/op | 7385 ns |
| MySerializer(手写二进制) | 2845 ns/op | 1315 ns |

排序在两台机器一致:Json < MySerializer < Gob。Gob 比 Json 慢约 7 倍。Gob 的字段名等自描述信息是它"跨端对齐字段"的底气,也是它慢的主因;手写二进制略慢于 sonic(约 1.3 倍)但比 Gob 快约 5.7 倍。复测命令 `go test ./my_rpc/serialization/test/ -run "^$" -bench "BenchmarkGob|BenchmarkSerializer|BenchmarkJson" -benchtime=100000x`;环境前提:go1.26 下 sonic v1.12.x 因 linkname 依赖 `encoding/json` 内部符号而链接失败,本机复测用 stdlib json 替换后三个数字成立〔实测:work/digest/C_dachang__my_rpc.md〕。

### 回到 gRPC

现在读 gRPC 的选型说明,可以把"序列化用 Protobuf"翻译成前面刚写过一遍的东西:`.proto` 契约对齐两端的字段集,相当于把本节"两端导出字段必须一致"从口头约定变成编译期保证;protobuf 的二进制编码与本节五段式同属"类型加长度加内容"的家族;gRPC 在 HTTP/2 帧上对每条消息做边界处理,就是粘包问题的现成解法;一次一元调用能拿回"属于这次请求"的响应,靠的是 HTTP/2 流 id,和 `requestBuffer` 按 id 派发是同一个思想。底层三基石都清楚了,第五节看契约与流就只是换语法。

## 五、gRPC：契约先行

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

`stream` 写在请求前、响应前还是两边，决定一元、服务端流、客户端流和双向流。客户端流发送完成通常调用 `CloseAndRecv`，双向流不再发送时调用 `CloseSend`，并继续接收直到结束；服务端处理函数返回即可结束流。提前放弃接收时应取消调用的 context，以释放资源。〔C:go_basic/grpc/client/main.go:119〕〔C:go_basic/grpc/client/main.go:155〕

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

## 六、HTTP 还是 gRPC

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


## 想深入：正文之外的干货

### TB09 标准库 Web 服务的组装术

- ServeMux 的老规则是最长前缀匹配，模式 `/` 能兜住所有路径；1.22 之前它分不清方法、取不了路径参数，这就是当年要引 gorilla/mux 的全部原因。〔TB09 p4〕〔TB09 p13-14〕
- 仿 `database/sql` 的工厂注册：factory 包用 `map[string]store.Store` 加 `sync.RWMutex` 管实例类型，`Register` 遇 nil 或重名 panic，`New` 按名返回；internal/store 在 init 里自注册，main 只需空导入 `_ "bookstore/internal/store"`。坑：忘了空导入，`factory.New("mem")` 直接 panic unknown provider，因为注册从未发生。〔TB09 p10-12〕
- 惯用函数原型是"接受接口、返回具体类型"（`NewBookStoreServer(addr, s store.Store) *BookStoreServer`），返回的具体类型组合传入接口的能力；`ListenAndServe` 会阻塞，必须挪进独立 goroutine，用 errChan 把运行状态传回主 goroutine，再 select errChan 与 `time.After(1s)` 判断是否启动成功。〔TB09 p13-14〕〔TB09 p16-17〕
- middleware 用闭包包装 `http.Handler` 而不是继承或配置表：Logging 记请求概要、Validating 校验 `Content-Type` 为 application/json，嵌套包裹后串到业务 handler 之前，横切逻辑与业务正交。〔TB09 p14-16〕
- handler 的共性收尾：`json.Decoder` 解码请求体，调 Store 接口方法，出错用 `http.Error` 带状态码；响应用 `json.Marshal` 加 `Content-Type: application/json`。生产配置不硬编码，配置文件、命令行参数、环境变量是三种来源，标准库没有内置配置框架，社区常用 viper。〔TB09 p14-15〕〔TB09 p21〕

### W02-1 gin 的路由、绑定与 CORS 细节

- gin 路由三态：静态 `/hello` 完全匹配；参数路由 `/users/:name` 用 `Param("name")` 取，尾斜杠变体 `/users/daming/` 的参数值是 `daming/`；通配符 `/views/*.html` 的匹配值含斜杠，且 `*` 不能单独出现（`/users/*` 非法）。查询参数是另一回事，`/order?id=123` 用 `Query("id")`。〔W02-1 p13-16〕
- 两条初学者原则：查数据用 GET 参数放 query，提交数据用 POST 参数放 body。〔W02-1 p15-18〕
- 注册三步是所有写接口的模板：接收请求并校验、调业务逻辑、按结果返回响应；前端 axios.post("/users/signup", values) 用 JSON 传 email/password/confirmPassword 三个字段，后端第一个版本 `ctx.String(200, "hello，你在注册")` 就能接上。先定前端页面再定后端接口的输入输出，是作者"从前端往后端"的顺序。〔W02-1 p26-29〕
- 校验三块与归属：邮箱格式、密码等于确认密码、密码强度（不少于 8 位含数字特殊字符）；校验规则理论上由产品经理给，现在更强调二次验证而非密码强度。正则用 `regexp` 包 `MatchString` 就够，不用专门学，要用时搜；复杂规则拆成多个表达式多次校验。〔W02-1 p32-33〕
- 预编译正则提速：pattern 用 `regexp.MustCompile` 在 `NewUserHandler` 构造函数里编译好，存成 UserHandler 的 `emailRegexExp`/`passwordRegexExp` 字段，校验时直接调方法，不要在请求路径上重复编译。〔W02-1 p34〕
- Bind 按 Content-Type 决定反序列化方式，出错直接把错误响应写给前端（MustBind 语义），所以 `if err := ctx.Bind(&req); err != nil { return }` 之后不用再处理；要自定义错误提示就用 ShouldBind，它只返回 error。〔W02-1 p31〕
- 注册请求的接数结构可以定义在 SignUp 方法内部：方法内部类除该方法外不可见，作者建议优先内部类。〔W02-1 p30〕
- Go 内置 regexp 是 RE2 语法，不支持 `(?=...)` 前瞻断言，密码强度正则直接编译不了，要换 dlclark/regexp2；它是回溯引擎，`MatchString` 返回 `(bool, error)` 双值，忘了判 err 会把系统错误当"密码不合格"。〔W02-1 p35〕
- 错误响应全用 200 加文案是教学简化，生产应区分 400（参数错）与 500（系统错）；课程自己预告后面再讨论 error 处理与状态码。〔W02-1 p36〕
- CORS 三配置之外的坑：`AllowOriginFunc` 用 `strings.Contains(origin, "your_company.com")` 这种子串匹配有安全隐患（evil-your_company.com 也能过），生产用精确域名集合；`MaxAge` 12h 是让浏览器缓存预检结果。〔W02-1 p43-47〕
- gin 启动日志的 trusted all proxies 警告是"未配置 SetTrustedProxies"的不安全默认，生产要设可信代理列表；端口占用（bind: address already in use）是启动失败最常见原因。〔W02-1 p7-8〕
- PPT 用的 `github.com/gin-gonic/contrib` cors 已废弃，现行是 `github.com/gin-contrib/cors`，API 同为 `cors.New(cors.Config{...})`。

### W03-1 Session 存储与限流的硬细节

- memstore.NewStore 要传两个 key：authentication key（建议 32 或 64 位）和 encryption key，cookie/memstore/redis 三个实现都要传。〔W03-1 p7-10〕
- `sess.Options` 的 MaxAge 有双重含义：既控 Cookie，又被 Redis 这类 store 用来控 Session 键值过期；只想"Cookie 过期"会漏掉数据侧过期。〔W03-1 p14-15〕
- JWT vs Session 的账：JWT 免第三方存储、适合分布式、性能高（无 Redis 访问），缺点是 payload 可读、不能放敏感信息；混用方案是 token 只存 userId，敏感数据用 userId 组 key 去 Redis 取。〔W03-1 p30-31〕
- 刷新失败只打日志不拒绝请求：刷新是锦上添花，把刷新失败当 401 会让正常用户随机掉线。〔W03-1 p27〕
- 限流的动机是注册/登录这对天然漏洞：任何人可刷、且每个请求都查库，shell 脚本轻松构造每秒几千上万请求把 Web 和数据库同时打挂；防御是限流，限制"每个人发的请求数"或"系统处理的总量"。〔W03-1 p35-36〕
- 限流三问：对象用 IP（Web 端最好选择，MAC/设备号用不上，共享 IP 误伤靠阈值化解）；阈值标准答案是压测得来，无压测凭经验每秒 100 足够（人手速撑死每秒 1 个，被限的直接拒绝"服务器繁忙"）；多实例必须 Redis 计数，负载均衡后请求不一定落同一台机器，本地计数会漏。〔W03-1 p38-42〕
- token 泄露防护分层：HTTPS 防传输窃取（防不了本机病毒）、二次验证最有效但重、初步手段是 User-Agent 比对（签发时带进 claims，校验时不一样就 401），代价是误伤换浏览器或升级浏览器的用户。〔W03-1 p45-48〕

### W04-1 缓存放哪、怎么降级

- UserCache 屏蔽三件事：过期时间（15min 改一处）、key 结构（`user:info:%d`）、序列化协议（json 与 []byte 互转）；它属于"如何存储数据"，放 Repository 层，Service 无感知。〔W04-1 p13-16〕
- Redis 故障时两种降级写法的对价：默认写法"cache.Get 有 error 就查 DAO 并回写"，可用性优先，数据库承压但业务还在；FindByIdV1 用 switch err，只有 ErrKeyNotExist 才查 DAO，隔离优先，数据库保住但本业务不可用。课程推荐前者。〔W04-1 p17-18〕
- 回写缓存失败可忽略：缓存是优化不是数据源，回写失败下次再 miss；但完全忽略会掩盖 Redis 故障，生产要打点监控。〔W04-1 p17〕
- 压测前置三件事：登录态用 JWT（比 Session 好测）、有效期临时拉长到 30 分钟保证测试期间 token 不过期、去掉 ratelimit 限制，否则压出来的不是接口性能。〔W04-1 p5〕
- wrk 三个对象的脚本差异：signup 用不同邮箱模拟注册，login 的 body 要改成自己注册的账号，profile 要手动登录后把 Bearer token 与 User-Agent 复制进 headers，否则 401 会把压测变成"测鉴权"。〔W04-1 p5-8〕wrk 参数就四个：-t 线程、-d 时长、-c 并发连接、-s lua 脚本。〔W04-1 p3-6〕

### W04-2 验证码登录的补遗

- 需求清单本身可当验收用例：手机号收码登录、新号直接建账号、验证码 10 分钟有效、一分钟一条、登录成功跳首页；非功能需求就一句，防攻击者恶意发短信（真金白银的损失）。〔W04-2 p7〕
- 腾讯实现坚持依赖注入：`NewService(c *sms.Client, appId, signName)` 外部传 client 而不是内部初始化；appId/signName 存指针（ekit.ToPtr）避免零值歧义；Send 里 `req.SetContext(ctx)` 把 ctx 继续下传，遍历 SendStatusSet 判 Code 不为 "Ok" 就带着 code 和原因报错。真发短信要环境变量加自己的 appId/signName/tplId，本地跑用 localsms 控制台打印验证码。〔W04-2 p21-22〕
- 需求分析三板斧：参考竞品、功能与非功能分开（中小公司排序是安全性>扩展性>性能）、正常与异常流程分开想（业务异常 vs 技术异常）。〔W04-2 p5〕
- 抽象由两个变化点决定：不同业务都要用验证码 + 可能换短信供应商；只按当下需求做简单抽象预留接口，别指望设计一个用几十年的接口。〔W04-2 p12〕〔W04-2 p17〕
- 腾讯短信 API 五要素：手机号、appId、签名（标记谁发的）、模板 ID、模板参数；appId 与签名固定，初始化注入，所以 `Send(ctx, tplId, args, numbers...)` 里不用传。〔W04-2 p18-19〕
- 业务逻辑下沉 lua 是为原子性牺牲可移植性：换 memcache 要重写脚本，CodeService 留薄封装是为了把替换成本控住。〔W04-2 p37〕
- 错误信息模糊化是安全默认：三次失败后不提示"过于频繁失败"，防攻击者探测重试上限；前端限流可被脚本绕过，服务端限流才是根。〔W04-2 p26〕〔W04-2 p54〕

### W05-1 OAuth2 的边界细节

- 两个接口加一个新 Handler：`/oauth2/wechat/authURL` 构造跳转 URL（实现就是 `fmt.Sprintf(authURLPattern, appId, redirectURL, uuid.New())`），`/oauth2/wechat/callback` 处理回调；OAuth2WechatHandler 聚合 wechat.Service，与 UserHandler 并列。〔W05-1 p15-16〕
- state 攻击成立的链条要完整记住：攻击者先弄一个绑自己微信的临时授权码 A，伪造页面诱导已登录用户点击，请求带着用户的 Cookie/JWT 和攻击者的授权码 A 去绑定，此后攻击者扫码就能看到用户数据；核心是"让你用他的授权码绑定账号"。绕过它需要 Cookie 本身泄露（本机中毒级别），单纯跨域构造拿不到你的 state。〔W05-1 p28〕
- webook 登录场景（存在即登录、不存在即建号）天然免疫这个攻击，但通用方案要会：AuthURL 时生成 state 与本次会话绑定，回调时比对回调带回来的 state 是不是本会话发出的。〔W05-1 p29〕
- 回调路由用 `Any` 方法注册：微信跳回可能是 GET，也防不同环境方法差异，比只注册 GET 稳。〔W05-1 p15〕
- state 放 JWT 存 Cookie 而非 Redis：免存储依赖、天然随 Cookie 过期；放 Cookie 而非 Header 是因为微信回调直达后端，不经过前端代码。verifyState 三错即敌情：取不到 cookie、不是合法 JWT、state 对不上，任一 error 都视为有人搞鬼，且不细分类别返回，防攻击者探测失败原因。〔W05-1 p30-32〕
- redirect_uri 域名必须预注册，微信侧硬校验，防任意回调劫持 code；本地开发要用内网穿透域名或测试公众号。〔W05-1 p9〕
- access_token 有效期短是故意的：泄露影响面小；refresh_token 长期保存，要更谨慎。union_id 是跨产品主键，只存 open_id 会让同公司多产品认不出同一个自然人。〔W05-1 p20-22〕

### W05-2 ssid 方案的工程细节

- 长 token 用独立签名 key：短 token 的 key 泄露也伪造不了 refresh，两个 token 的权限域隔离。〔W05-2 p6〕
- ssid 方案的推导过程值得记：废 token 有两条路（只废长 token、长短都废），前者要"登录校验时随时拿得到长 token"，后者两样都麻烦；干脆跳过 token 本身，用一个"这次登录"的标识 ssid（UUID）串起长短 token，登录校验、refresh、登出三处只认 ssid，它在 Redis 里一出现就算登录失效。〔W05-2 p16-17〕
- 登录签发收口在 `SetLoginToken`：`uuid.New()` 生成 ssid，一次调用里先后发 `SetJWTToken`（短）与 `SetRefreshToken`（长），RefreshClaims 里带 User-Agent、uid、ssid；两个 token 签进同一个 ssid 是后面三处校验的前提。〔W05-2 p18〕
- 登出是两道闸：`ClearToken` 先把长短 token 响应头置空字符串（让正常用户快速 401），再把 `users:ssid:{ssid}` SET 成 "n" 带 7 天过期（拦还在用旧 token 的客户端）；缺任何一道都放过一类人。ssid 的 TTL 要覆盖长 token 的最长寿命，否则登出有失效窗口。〔W05-2 p21〕〔W05-2 p25〕
- Redis 崩溃降级方向是"跳过 ssid 校验"而不是拒绝服务：可用性优先于登出严格性；refresh 接口不降级，低频且安全敏感。〔W05-2 p19-20〕
- 不要用 user id 当登出标识：登录时要删 id、还要处理"一边登录一边退出"的并发；ssid 每次登录唯一，无此二难。〔W05-2 p29〕
- 坚持登出严格无效就得引入类 Session 机制：JWT 生成后"连它自己都不知道自己可不可用"，Redis 黑名单不是 JWT 的补丁而是这个坚持的必然代价；反过来接受"登出只尽力"就能省掉这次 Redis 依赖。〔W05-2 p28〕
- 布隆过滤器是面试方案：N 个哈希函数置位，判"一定不存在"准、"存在"有假阳性，仍要 Redis 兜底且本身靠 Redis 实现，改进有限。〔W05-2 p30-31〕
- 重构收口：散在三个 handler 与 middleware 里的 JWT 逻辑内聚成 jwt 子包，定义 Handler 接口（ClearToken/SetLoginToken/SetJWTToken/CheckSession/ExtractTokenString）加 RedisHandler 实现；接口不直接经 ctx 写响应，返回值由调用方写前端。〔W05-2 p22-27〕

### W10-1 微服务理论与 Protobuf 细节

- 分而治之的账：两系统合并的复杂度大于两者之和（交互成本），拆分的收益是总体复杂度下降加单模块可理解；单体与微服务的运维基本单位是实例，16G 的单体拆成 4G 的模块；微服务 API 向后兼容才能独立演进；服务边界贴近组织架构能减少交流损耗（康威定律）。〔W10-1 p5-7〕
- RPC 载体有 TCP（Dubbo 类大厂自研）、HTTP（gRPC）、UDP、MQ 封装四种，新协议多基于 HTTP；HTTP 微服务运维简单门槛低、适合异构，RPC 组件多要求高，外企与历史系统 HTTP 不少，微服务不等于 RPC。〔W10-1 p10-11〕
- DDD 词汇速记：实体有 ID（邮箱可改不算标识）、值对象无 ID（价格金额）、聚合体是聚合根加值对象、聚合根是修改唯一入口、聚合体间只能 ID 引用；实体必有表，表不一定都是实体；仓库是聚合体的增删改查并屏蔽缓存与 DB；领域事件多来自聚合体数据变化，发 MQ 的消息就是领域事件；跨聚合的逻辑才用 Domain Service，CRUD 项目里它几乎没代码。〔W10-1 p16-23〕〔W10-1 p15〕
- Protobuf 的账面性能：序列化 0.7ms/0.4MB 对 JSON 的 1.7-4.3ms/1.9-2.8MB，反序列化 0.8ms 对 2.6-6.5ms；`sint` 类型作者视为垃圾设计，`oneof` 在 Go 里生成接口加类型断言（作者基本不用），enum 的 0 必须留给 Unknown（0 是 proto3 默认值，0 当业务值会混淆"未设置"与"合法值"）。〔W10-1 p29-31〕〔W10-1 p36-39〕
- 工具链现状：protoc 加两个插件 protoc-gen-go 与 protoc-gen-go-grpc（GOPATH/bin 进 PATH），一条命令带两组 out/opt，go 与 go-grpc 都用 source_relative 相对输出；产物两个文件，纯结构体 pb.go 与含 gRPC 存根的 `_grpc.pb.go`。〔W10-1 p41-45〕
- 服务端生成接口强制组合 `mustEmbedUnimplementedUserServiceServer`，这是向后兼容的手段：新加方法时旧实现不至于编译失败；另有不推荐的 Unsafe 接口。客户端每方法生成 `(ctx, in, ...grpc.CallOption)`。〔W10-1 p48-49〕
- `grpc.Dial` 惰性连接：Dial 不立即建连，第一次 RPC 才连，测试里 ctx 超时要覆盖调用全程；旧 `WithInsecure` 已废弃，用 `grpc.WithTransportCredentials(insecure.NewCredentials())`。〔W10-1 p52〕

### W13-2 负载均衡与重试的展开

- 默认 pick_first 并非完全无用：单节点加 K8s Service 场景下它就是正确行为；多实例才必须显式配 round_robin，指定方式是 `WithDefaultServiceConfig` 传 JSON 串 `{"loadBalancingConfig":[{"round_robin":{}}]}`。JSON 手写易错，写错一个引号配置静默失效退回 pick_first，建议配置文件加单测校验。〔W13-2 p4-5〕
- 官方加权轮询是客户端自算权重：按 QPS 与错误率实时算（QPS 高权重大、错误率高权重低），用 EDF 挑选，不需要注册中心传权重；要精确控制权重就自定义 Picker，权重经注册中心的 Metadata 传递（该字段已弃用，有的实现放 Address.Attributes，类型可能被 JSON 序列化成 string，必须确认）。〔W13-2 p9〕〔W13-2 p12-13〕
- 自定义负载均衡只碰两个接口：PickerBuilder 的 Build 出 Picker，Picker 的 Pick 返回 PickResult，一般不用顶层的 Balancer 接口（要管连接池状态）；`PickResult.Done` 回调能拿到错误与延迟，是动态升降权的挂载点。算法经 `balancer.Register` 注册成进程级全局（匿名引入 weightedroundrobin 包触发 init 注册），不同服务要不同算法只能靠 service config 区分。〔W13-2 p11-14〕〔W13-2 p16〕
- failover 的两个齿轮缺一不可：只有 retry 是反复撞死节点，只有 LB 剔除是单次失败就丢请求；retryPolicy 字段有 name（去 proto 生成产物里找方法名）、maxAttempts、initialBackoff/maxBackoff/backoffMultiplier（指数退避）、retryableStatusCodes（通常 UNAVAILABLE）。〔W13-2 p29-30〕
- 两个框架参照：Kratos 默认平滑 WRR，负载均衡封装成 Selector（可传 filter 过滤节点），指定算法要 SetGlobalSelector；go-zero 默认写死 P2C（随机选 2 个节点挑负载低的），只能用原生 WithDefaultServiceConfig 覆盖。〔W13-2 p21-24〕
- 面向业务的 LB 选择：本地缓存场景用哈希 LB 保证同 key 必命中同节点，否则缓存节点来回漂移导致重复加载加内存翻倍；客户端发现服务端触发熔断或限流，一段时间内不选它（failover 变体）。〔W13-2 p34-35〕

### C:go_basic/http、C:go_basic/grpc、C:go_basic/protoc 的补充点

- 服务端逐行 Flush 做流式响应：先设 Content-Length，断言 `w.(http.Flusher)` 后每行 Write 加 Flush 强制写到 TCP；客户端提前 Close 断开，服务端 Write 会报错退出，必须检查 err 并 break，否则 goroutine 忙等。不声明长度则走 chunked。〔C:go_basic/http/server/main.go:93-115〕
- header 名大小写不敏感。`http.SetCookie` 可以追加多个 `Set-Cookie` 响应头，浏览器按各 Cookie 的名称、Domain、Path 等处理；它们不能简单合并成一个逗号分隔的头。客户端可用 `request.AddCookie` 添加 Cookie。〔C:go_basic/http/server/main.go:41-44〕〔C:go_basic/http/server/main.go:117-135〕
- 客户端便捷方法 `http.Get/Post/PostForm/Head` 不能设 header，要设就用 NewRequest 加 `client.Do`，超时挂在 `http.Client{Timeout}`；生产应复用 Client 实例（自带连接池）。〔C:go_basic/http/client/main.go:200-227〕
- Recv 循环里 `io.EOF` 是正常结束标志，其他错误才需要处理，把 EOF 当错误会误报。〔C:go_basic/grpc/server/student.go:60-72〕
- gRPC 默认单条消息上限 Recv 4M（Send 默认 MaxInt32），传大对象要 `MaxCallRecvMsgSize` 显式调大；TLS 一行开启：服务端 `grpc.Creds(credentials.NewServerTLSFromFile(...))`，客户端 `NewClientTLSFromFile`，即使 insecure 也必须设 `WithTransportCredentials`。〔C:go_basic/grpc/server/main.go:52-68〕
- 序列化三种格式：`proto.Marshal` 二进制最快且不可读，`protojson` 可读但性能最差分配最多，`prototext` 调试用；实测 proto 约 511ns 对 json 1026ns，protojson 1979ns 加 56 次分配。〔C:go_basic/grpc/idl/model/student_test.go:23-89〕
- 多 proto 文件组织靠 `--proto_path` 多路径解析 import，裸文件名在多路径下有同名歧义，标准做法是 import 全路径 `google/protobuf/timestamp.proto` 并把 proto_path 指向 include 目录；跨包引用字段要带 proto package 名（`model.com.user`）；`--go_opt=Mmodel/user.proto=a/b/c/model` 改写生成代码的 import，目标必须与 go.mod 的 module 名一致，否则编译报 package not in std。Timestamp 这类 well-known 类型生成 `*timestamppb.Timestamp`。〔C:go_basic/protoc/dao/product.proto:8-12〕〔C:go_basic/protoc/model/user.proto:9-14〕〔C:go_basic/protoc/go.mod:1-2〕
- 生成代码头部有版本锚定：`EnforceVersion(20)` 与 MaxVersion 双向断言生成器与运行库兼容，SupportPackageIsVersion9 要求 grpc v1.64 以上；遇到版本不匹配先看这里。〔C:go_basic/protoc/model/user.pb.go:1-24〕

省略未吸收：TB09 的项目布局约定见 02 节，仿 database/sql 工厂已在本节 TB09 干货块展开；W04-2 的 sql.NullString 与 JsonColumn 已在 13 节验证码登录干货块展开，NULL 的三种处理方式见 12 节；W05-1 的 verifyState 实现细节与 W13-2 的 Kratos 作业属于框架与作业范畴；W02-1 的 gin 输入输出 API 全景与 ctx.Get 系列是 API 目录不是机制；W04-1 的 Redis 数据结构清单是选读内容，缓存机制见 13 节；W10-1 的 DDD 八概念正文仅留速记（p15 的概念总览已在速记条目里）；C 单元的模板渲染、Cookie 解析 API 细节与 bufconn 练习未收。W05-1 p2、W10-1 p2 为目录页，无知识点可收。

## 想深入去哪查

- 标准库服务与中间件：TB09，`C:go_basic/http`（响应顺序、流式响应、Cookie、三种路由）。
- gin：W02-1（路由、参数绑定、校验、CORS、middleware）。
- gRPC 与 protobuf：W10-1（选型、IDL、Protobuf 语法）、`C:go_basic/grpc`（四种流、拦截器链、TLS、序列化性能）、`C:go_basic/protoc`（`go_package`、多 `proto_path`）、W13-2（负载均衡与重试）。
- 认证：W03-1（Session 存储与刷新、JWT 结构与 CORS 配合）、W05-2（长短 token 与 ssid 登出）、W05-1（OAuth2 扫码与 state）、W04-1（wrk 压测登录与 UserCache 降级）。
- 旧长版：`notes/p10-grpc.md`、`notes/p8-fast-track.md`。
