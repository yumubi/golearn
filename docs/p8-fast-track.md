---
title: P8 · 快速扩展：泛型、标准库、测试、HTTP 的四个问题
---

# P8 · 快速扩展：泛型、标准库、测试、HTTP 的四个问题


> 这章学完你能干什么：写出 `MaxK[T cmp.Ordered]` 这样的泛型函数，说清它和 Rust 的 `T: PartialOrd` 差在哪。用 `slices`、`cmp`、`sync.OnceValue` 替掉手写工具函数。用 table-driven 加 `-race` 加 `go vet` 搭起最小门禁。用纯标准库写出带 1.22 路由、超时和优雅关停的 HTTP 服务。
> 读者：会 Rust，也写过 Java/Kotlin 或 TS/JS。前置：P1-P6。后续：httpkv 是 P13（metrics）、P15/P16（slog）的实验台。
>
> 动手方式：开一个空目录，`go mod init demo`，每个例子存成 `main.go`，用 `go run main.go` 运行。片段省略了 `package main` 和 `import`。终点证据是 `exercises/p8fast/` 的 11 个测试全绿，以及 `httpkv` 起服务后的 curl 冒烟。

## 体系背景：一个 HTTP 请求在 TCP 上怎么走

本章最后一半讲 HTTP 服务。先把它下面的一层看清楚，后面的超时配置才不是玄学。

HTTP 不直接发包，它跑在 TCP 上。TCP 是内核维护的一份连接状态：双方先握手三次（SYN、SYN-ACK、ACK）建立连接，才能收发字节流。所以连接是状态，不是管道。

写一个最小的服务，监听 8091 端口。用 `curl -v` 看一次请求的原文：

```sh
$ curl -sv http://localhost:8091/hello 2>&1 | grep -E "Connected|^> |^< |^hello"
* Connected to localhost (::1) port 8091
> GET /hello HTTP/1.1
> Host: localhost:8091
> User-Agent: curl/8.7.1
> Accept: */*
>
< HTTP/1.1 200 OK
< Date: Mon, 12 Oct 2026 11:43:54 GMT
< Content-Length: 6
< Content-Type: text/plain; charset=utf-8
<
hello
```

`Connected` 那行是握手完成。其后全是文本：一行请求行、几行头、一个空行。不用 curl，用 `nc` 手敲同样能行：

```sh
$ printf 'GET /hello HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n' | nc localhost 8091
HTTP/1.1 200 OK
Date: Mon, 12 Oct 2026 11:43:55 GMT
Content-Length: 6
Content-Type: text/plain; charset=utf-8
Connection: close

hello
```

连接还是状态的证据是 keep-alive。一条命令请求两次，第二次不再握手：

```sh
$ curl -sv http://localhost:8091/hello http://localhost:8091/hello 2>&1 | grep -E "Connected|Re-using|left intact|^hello"
* Connected to localhost (::1) port 8091
* Connection #0 to host localhost left intact
hello
* Re-using existing connection with host localhost
* Connection #0 to host localhost left intact
hello
```

内核里能看到这些状态。macOS 用 `netstat`，Linux 用 `ss -tan`。服务启动后，监听端口是 `LISTEN`：

```sh
$ netstat -an -p tcp | grep 8091 | grep LISTEN
tcp46      0      0  *.8091                 *.*                    LISTEN
```

一条空闲的连接是两行 `ESTABLISHED`（一行客户端视角，一行服务端视角）。连接关闭后，主动关闭的一方留下 `TIME_WAIT`，内核等一会儿再回收：

```sh
$ netstat -an -p tcp | grep -E "\.8091 " | grep -v LISTEN
tcp6       0      0  ::1.8091               ::1.54192              ESTABLISHED
tcp6       0      0  ::1.54192              ::1.8091               ESTABLISHED
tcp6       0      0  ::1.54130              ::1.8091               TIME_WAIT
```

现在是重点。服务端读请求头是要时间的，客户端可以故意发半截头然后不再发，连接就一直挂着。每条挂着的连接占服务端一个文件描述符（fd，内核给打开的文件或连接编的号）和一个 goroutine。这就是 slowloris 攻击：它打的是 TCP 连接数，不是带宽。

对比两个服务：8092 没设超时，8091 设了 `ReadHeaderTimeout: 2s`。各用 50 个客户端发半截头，然后数服务端一侧的 `ESTABLISHED`：

```sh
$ for i in $(seq 1 50); do (printf 'GET /hello HTTP/1.1\r\nHost: x\r\n'; sleep 8) | nc localhost 8092 >/dev/null 2>&1 & done
$ sleep 2; netstat -an -p tcp | awk '$4 ~ /\.8092$/ && /ESTABLISHED/' | wc -l
      50
$ # 8091 同样 50 条，4 秒后
$ netstat -an -p tcp | awk '$4 ~ /\.8091$/ && /ESTABLISHED/' | wc -l
       0
```

没有超时的服务，50 条连接一直占着。设了超时的服务，2 秒后主动关掉了它们。这就是 `ReadHeaderTimeout` 必设的原因，正文最后的 HTTP 小节会回到这里。

## 泛型函数

假设你要写一个"取较大者"的函数。没有泛型时，`int`、`string`、`float64` 要写三份。Go 1.18 起可以把类型作为参数：

```go
func Max[T cmp.Ordered](a, b T) T {
	if a > b {
		return a
	}
	return b
}

func MaxK[T cmp.Ordered](xs []T) (T, bool) {
	if len(xs) == 0 {
		var zero T
		return zero, false
	}
	m := xs[0]
	for _, x := range xs[1:] {
		m = max(m, x)
	}
	return m, true
}

fmt.Println(Max(3, 8), Max("go", "rust"), Max(2.5, 1.5))
fmt.Println(MaxK([]string{"go", "rust", "zig"}))
fmt.Println(MaxK([]int{}))
```

```sh
$ go run main.go
8 rust 2.5
zig true
0 false
```

方括号里是类型参数和它的约束。调用时类型由实参推断出来，不用写 `Max[int](3, 8)`。

空切片没有"最大值"。Go 没有 `Option`，惯例是返回 `(T, bool)`，练习 `TestMaxK` 考的就是这个。

约束 `cmp.Ordered` 是内建的，允许 `>`、`<`、`==` 这些运算符。它对应 Rust 的 `T: PartialOrd`，对应 Java 的 `<T extends Comparable<T>>`。TS 的 `<T extends number | string>` 也类似，但 TS 的类型在运行时就擦除了，Go 不会。

## 约束与 ~

如果只写 `any`，编译器不让你用运算符：

```go
func Add[T any](a, b T) T { return a + b }
```

```sh
$ go run main.go
# command-line-arguments
./main.go:6:9: invalid operation: operator + not defined on a (variable of type T constrained by any)
```

约束必须说明"这个类型能干什么"。它是一个接口，可以列出类型集合，比如 `int | float64`。

还有一个常见需求：自定义类型 `type MyInts []int`，底层是 `[]int`，想让它也能传进来。这时要在约束里加 `~`：

```go
type MyInts []int

func Total[S ~[]int](s S) int {
	t := 0
	for _, v := range s {
		t += v
	}
	return t
}

fmt.Println(Total(MyInts{1, 2, 3}))
```

```sh
$ go run main.go
6
```

把 `~` 删掉，编译器会提示：

```sh
./main.go:16:19: MyInts does not satisfy []int (possibly missing ~ for []int in []int)
```

`~[]int` 的意思是"底层类型是 `[]int` 的都算"。练习 `TestTotalUnderlying` 考这一点。

## 泛型的代价

Go 的泛型实现和 Rust 不同。Rust 为每个类型生成一份代码（单态化），零开销。Go 对内存布局相同的类型共享一份代码，再通过字典传类型信息，叫 GC shape stenciling。Java 是类型擦除，Kotlin 的泛型默认也擦除，除非用 `reified`。

代价是热路径上有字典查找的开销。`sort.Slice`（反射）和 `slices.SortFunc`（泛型）对比，审读台账 `audit/p8.md` 里的 M4 实测是：10 万元素 int 切片纯排序 8.01 ms 对 6.38 ms，带 clone 的 10 万元素版本 512 µs 对 356 µs，约 1.3-1.4 倍，分配次数 3 对 0。

本机复跑一遍，同样是 Apple M4，带 clone：

```sh
$ go test -bench . -benchmem ./sortb
BenchmarkSortSlice-10         	      92	  12411383 ns/op	  802954 B/op	       3 allocs/op
BenchmarkSlicesSortFunc-10    	     139	   8297573 ns/op	  802819 B/op	       1 allocs/op
```

两次测的口径不同，绝对数不能互比，但方向一致：泛型版更快，分配更少。

Go 泛型也有明确的边界。没有特化，没有 `impl<T> Trait for T`，没有 const 泛型。Go 官方的定位是容器和工具函数，不是 Haskell 的 typeclass。想给别人的类型加方法也做不到，`providerOf` 那样包一层是唯一办法（P5 踩过）。

## slices 与 cmp

2026 年写工具函数之前，先查 `slices`、`cmp` 和 `sync.OnceValue`。完整速查在章末。

```go
xs := []int{3, 1, 4, 1, 5, 9, 2, 6}
fmt.Println(slices.Contains(xs, 9), slices.Index(xs, 4))

ys := slices.Clone(xs)
slices.Sort(ys)
fmt.Println(ys, xs)
fmt.Println(slices.Compact(ys))
```

```sh
$ go run main.go
true 2
[1 1 2 3 4 5 6 9] [3 1 4 1 5 9 2 6]
[1 2 3 4 5 6 9]
```

`Clone` 先拷贝，排序不动原切片。`Compact` 去掉相邻的重复值，所以要先排序。

自定义排序用 `SortFunc`，比较函数返回负数、零或正数。多字段比较用 `cmp.Or`，它返回第一个非零值：

```go
us := []User{{"bob", 30}, {"amy", 25}, {"cat", 30}}
slices.SortFunc(us, func(a, b User) int {
	return cmp.Or(
		cmp.Compare(b.Age, a.Age),
		cmp.Compare(a.Name, b.Name),
	)
})
fmt.Println(us)
fmt.Println(cmp.Compare(1, 2), cmp.Compare("b", "a"), min(3, 1, 2))
fmt.Println(cmp.Or("", "default"), cmp.Or(0, 0, 7))
```

```sh
[{bob 30} {cat 30} {amy 25}]
-1 1 1
default 7
```

先按年龄降序，同龄再按名字升序。这和 Java 的 `Comparator.comparing(...).thenComparing(...)` 是同一个意思，也和 JS 里 `sort((a, b) => b.age - a.age || a.name.localeCompare(b.name))` 一样。练习 `TestTopN` 和 `TestUniq` 考 `SortFunc` 加 map 当 set 这两个最高频动作。

## sync.OnceValue

懒初始化，比如第一次用到时才连数据库。手写 `sync.Once` 有两个坑：重入会死锁，闭包的结果要自己存。`sync.OnceValue` 把它们封装掉了：

```go
var calls atomic.Int32
get := sync.OnceValue(func() []int {
	calls.Add(1)
	fmt.Println("构造中...")
	return []int{0, 1, 4, 9}
})

var wg sync.WaitGroup
for range 10 {
	wg.Go(func() { _ = get() })
}
wg.Wait()
fmt.Println("构造次数:", calls.Load(), "值:", get())
```

```sh
$ go run -race main.go
构造中...
构造次数: 1 值: [0 1 4 9]
```

10 个 goroutine 同时调用，构造函数只跑了一次。它对应 Kotlin 的 `by lazy`，Java 的静态内部类单例，Rust 的 `OnceLock`。练习 `NewCounter` 和 `TestOnceValue` 验证的就是这个。

## 迭代器

Go 1.23 起有 range over func，标准库给常用函数加了迭代器版本：

```go
for i, v := range slices.All([]string{"a", "b"}) {
	fmt.Println(i, v)
}
m := map[string]int{"x": 1, "b": 2, "a": 3}
fmt.Println(slices.Sorted(maps.Keys(m)))

n := 0
for part := range strings.SplitSeq("go,rust,ts,python", ",") {
	fmt.Print(part, " ")
	n++
	if n == 2 {
		break
	}
}
fmt.Println()
```

```sh
$ go run main.go
0 a
1 b
[a b x]
go rust
```

`maps.Keys` 返回迭代器，配合 `slices.Sorted` 得到有序的键。`strings.SplitSeq`（1.24）不建切片，大输入加早停时省分配。练习 `TestSplitSeq` 用它实现 `FieldLens`。

Rust 的 `.iter().filter().map().collect()` 链在 Go 里没有对应物，生态主流仍是显式 `for`。

## table-driven 测试

Go 的测试写法几乎只有一种：把用例列成一张表，循环里用 `t.Run` 起子测试。

```go
func TestMax(t *testing.T) {
	tests := []struct {
		name string
		a, b int
		want int
	}{
		{"a bigger", 3, 1, 3},
		{"equal", 2, 2, 2},
		{"b bigger", 1, 3, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Max(tt.a, tt.b); got != tt.want {
				t.Errorf("Max(%d,%d) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
```

```sh
$ go test -v ./tt
=== RUN   TestMax
=== RUN   TestMax/a_bigger
=== RUN   TestMax/equal
=== RUN   TestMax/b_bigger
--- PASS: TestMax (0.00s)
    --- PASS: TestMax/a_bigger (0.00s)
    --- PASS: TestMax/equal (0.00s)
    --- PASS: TestMax/b_bigger (0.00s)
PASS
```

子测试名里的空格变成下划线，可以单跑一条：

```sh
$ go test -v -run 'TestMax/equal' ./tt
=== RUN   TestMax
=== RUN   TestMax/equal
    --- PASS: TestMax/equal (0.00s)
ok  	p8demo/tt	0.219s
```

故意把 `Max` 里的 `>` 改成 `<`，失败信息精确到子测试：

```sh
--- FAIL: TestMax (0.00s)
    --- FAIL: TestMax/a_bigger (0.00s)
        tt_test.go:18: Max(3,1) = 1, want 3
    --- FAIL: TestMax/b_bigger (0.00s)
        tt_test.go:18: Max(1,3) = 1, want 3
```

这对应 JUnit 的 `@ParameterizedTest` 和 Jest 的 `test.each`，区别是 Go 不需要任何框架。断言就是 `if` 加 `t.Errorf`，社区不用 assert 库。练习 `TestFizzBuzz` 是这张表的实物。

## vet、race 与 fuzz

门禁有三道，都在 `go test` 一条命令的范围内。第一道是 `go vet`，抓编译器不管的错误：

```go
var mu sync.Mutex
copied := mu
fmt.Printf("%d\n", "not a number")
```

```sh
$ go vet ./vetd
vetd/main.go:14:12: assignment copies lock value to copied: sync.Mutex
vetd/main.go:15:6: assignment copies lock value to _: sync.Mutex
vetd/main.go:16:14: fmt.Printf format %d has arg "not a number" of wrong type string
```

第二道是 `go test -race`，P3 演示过 race detector 的报告。官方文档给的开销是执行慢 2-20 倍，内存多 5-10 倍，所以只配测试，不上生产。

第三道是 fuzz，让工具自己造输入找崩溃。下面这个函数在 value 为空时越界：

```go
func First(s string) (byte, error) {
	_, v, err := Parse(s)
	if err != nil {
		return 0, err
	}
	return v[0], nil
}

func FuzzFirst(f *testing.F) {
	f.Add("a=b")
	f.Fuzz(func(t *testing.T, s string) {
		_, _ = First(s)
	})
}
```

```sh
$ go test -fuzz FuzzFirst -fuzztime 5s ./fz
fuzz: elapsed: 0s, gathering baseline coverage: 1/1 completed, now fuzzing with 10 workers
fuzz: minimizing 30-byte failing input file
--- FAIL: FuzzFirst (0.04s)
    --- FAIL: FuzzFirst (0.00s)
        testing.go:1927: panic: runtime error: index out of range [0] with length 0
```

fuzz 不到一秒就找到了，并且把输入最小化成 `"="`，存进 `testdata/fuzz/FuzzFirst/`。之后普通的 `go test` 会自动回放这个用例，成了永久回归测试。

## benchmark 与 pprof

benchmark 用 `b.Loop()`（1.24 起），替代老的 `for i := 0; i < b.N; i++`。框架自动处理计时起点，也防止编译器把不变量提出循环：

```sh
$ go test -run xxx -bench FieldLensLoop -benchmem ./exercises/p8fast/
BenchmarkFieldLensLoop-10    	17806718	        74.79 ns/op	       0 B/op	       0 allocs/op
```

每次都加 `-benchmem`，别只看时间。对比两次结果用 `benchstat`，它是 `golang.org/x/perf` 里的第三方工具。

pprof 是标准库内建的。`httpkv` 在 `exercises/p8fast/httpkv/` 里挂了 `net/http/pprof`，监听 6060：

```sh
$ curl -s 'localhost:6060/debug/pprof/goroutine?debug=1' | head -3
goroutine profile: total 7
1 @ 0x1025cdfd8 0x102691fcc 0x1027174d8 0x10271f4a4 0x1027d8868 0x102644ae4
#	0x102691fcb	internal/poll.(*FD).Read+0x20b			/Users/akka/Toolchain/go/current/src/internal/poll/fd_unix.go:164
```
栈帧里的 GOROOT 路径是编译机上的（`/Users/.../Toolchain/go/...`），你跑出来的
形态一样、路径换成你自己的 Go 安装位置，地址列也会不同。

接入只要两行：`import _ "net/http/pprof"` 加一个 `go http.ListenAndServe("localhost:6060", nil)`。只绑本机，不能暴露公网。抓 CPU 画像用 `go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30`。P13 把它当第四根支柱。

## 路由

回到 HTTP。Go 1.22 起，标准库的路由支持方法和路径参数：

```go
mux := http.NewServeMux()
mux.HandleFunc("GET /api/items/{id}", func(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Item{ID: r.PathValue("id"), Name: "demo"})
})
```

```sh
$ curl localhost:8093/api/items/42
{"id":"42","name":"demo"}
$ curl -si -X POST localhost:8093/api/items/42 | grep -E "^HTTP|^Allow"
HTTP/1.1 405 Method Not Allowed
Allow: GET, HEAD
$ curl -s -o /dev/null -w "%{http_code}\n" localhost:8093/api/nothing
404
```

方法不匹配自动回 405 并带 `Allow` 头，路径不存在是 404。这对应 Spring 的 `@GetMapping("/items/{id}")` 加 `@PathVariable`，Express 的 `app.get('/items/:id')` 加 `req.params.id`。axum 里的 extractor 在 Go 里没有，参数全手写，换来的是零魔法。

练习 `TestPathValue` 考路径参数，`TestHTTPKV` 跑 httpkv 全链路。

## JSON

`encoding/json` 是 Go 的 serde，够用但原始。先看字段省略的两个标签：

```go
type Old struct {
	Name string    `json:"name"`
	Meta Meta      `json:"meta,omitempty"`
	At   time.Time `json:"at,omitempty"`
}
type New struct {
	Name string    `json:"name"`
	Meta Meta      `json:"meta,omitzero"`
	At   time.Time `json:"at,omitzero"`
}
```

```sh
$ go run main.go
omitempty: {"name":"x","meta":{},"at":"0001-01-01T00:00:00Z"}
omitzero : {"name":"x"}
```

`omitempty` 对结构体无效，零值照样输出，这是老坑。1.24 加的 `omitzero` 补上了，还认自定义的 `IsZero() bool`。练习 `TestOmitZero` 有可编译例子，在 `exercises/p8fast/modern_std_test.go`。

第二个要记的是默认行为：解码时，未知字段被静默忽略。Jackson 默认是报错的，Go 反过来。

```go
var v struct{ Name string }
err := json.Unmarshal([]byte(`{"Name":"a","bogus":1}`), &v)
fmt.Println("默认忽略未知字段:", v, err)
dec := json.NewDecoder(strings.NewReader(`{"Name":"a","bogus":1}`))
dec.DisallowUnknownFields()
fmt.Println("收紧后:", dec.Decode(&v))
```

```sh
默认忽略未知字段: {a} <nil>
收紧后: json: unknown field "bogus"
```

`DisallowUnknownFields` 对应 serde 的 `deny_unknown_fields`。httpkv 的 PUT handler 开着它：

```sh
$ curl -s -w "%{http_code}\n" -X PUT localhost:8090/kv/bad -d '{"value":"x","bogus":1}'
{"error":"json: unknown field \"bogus\""}
400
```

和 serde 比，Go 没有 enum 序列化策略，没有 `flatten`，没有 zero-copy。复杂 schema 手写 `UnmarshalJSON`，二十行内能解决。`encoding/json/v2` 是实验性的下一代，要 `GOEXPERIMENT=jsonv2` 才启用。1.26 实测反序列化快 2 倍、分配从 6 次降到 1 次，序列化暂无优势，API 没定型，别上生产（详见 P19）。

## 超时

回到体系背景。`http.Server` 有三个超时，分别管连接的三个阶段：

```go
srv := &http.Server{
	Addr:              ":8090",
	Handler:           mux,
	ReadHeaderTimeout: 5 * time.Second, // 读请求头
	WriteTimeout:      10 * time.Second, // 写响应
	IdleTimeout:       60 * time.Second, // keep-alive 空闲
}
```

`ReadHeaderTimeout` 是 slowloris 的解药，漏了就是事故。`IdleTimeout` 回收空闲连接，防止 `ESTABLISHED` 越堆越多。Tomcat 的 `connectionTimeout` 和 Node 的 `server.headersTimeout`（默认 60 秒）是同一类配置。

每个请求自带 ctx。客户端断开时，`r.Context()` 会被取消，handler 可以及时放弃：

```go
mux.HandleFunc("GET /slow", func(w http.ResponseWriter, r *http.Request) {
	select {
	case <-time.After(3 * time.Second):
		fmt.Fprintln(w, "处理完了")
	case <-r.Context().Done():
		log.Println("slow: 客户端断开，放弃处理:", r.Context().Err())
	}
})
```

```sh
$ curl --max-time 1 localhost:8093/slow; echo "curl 退出码 $?"
curl 退出码 28
```

服务日志里是：

```sh
2026/10/12 19:46:04 slow: 客户端断开，放弃处理: context canceled
```

Node 里对应的是 `req.on('close')`，Kotlin 协程里是取消异常。

## 优雅关停

最后一块是停服务。直接 `kill` 会掐断在飞的请求。正确做法：收到信号后停止接新连接，等在飞的请求做完。

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()
go func() {
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}()
<-ctx.Done()
shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()
srv.Shutdown(shutdownCtx)
```

在 `/slow` 处理到一半时发 `SIGINT`：

```sh
$ (curl -s localhost:8093/slow; echo "[curl 拿到响应]") & sleep 1; kill -INT $pid; wait $pid; echo "服务退出码: $?"
处理完了
[curl 拿到响应]
服务退出码: 0
```

服务日志的先后顺序说明了一切：

```sh
2026/10/12 19:46:06 shutting down
2026/10/12 19:46:08 slow: 处理完毕
2026/10/12 19:46:08 bye
```

`shutting down` 之后 2 秒，在飞的请求才处理完，然后才 `bye` 退出。这对应 Spring Boot 的 `server.shutdown=graceful`，Node 的 `server.close()`。真正的 httpkv 同样处理 `SIGINT` 和 `SIGTERM`，实测两种信号退出码都是 0。

## 小结

1. 泛型约束是接口，不是 trait。没有特化，热路径优先 `slices.SortFunc`。
2. 标准库 1.21-1.25 把很多靠 crate 补的东西内建了：`slices`、`maps`、`cmp`、`iter`、`-race`、pprof。先查标准库再找第三方。
3. 门禁三件套：table-driven、`-race`、`go vet`。fuzz 和 benchmark 也是内建的。
4. HTTP 跑在 TCP 上。连接是状态，`ReadHeaderTimeout` 必设。
5. 优雅关停是 `signal.NotifyContext` 加 `Shutdown`。

## 练习：p8fast 与 httpkv

先跑一遍看全绿：

```sh
$ go test -race ./exercises/p8fast/...
ok  	golearn/exercises/p8fast	1.235s
ok  	golearn/exercises/p8fast/httpkv	1.275s
```

练习地图：

- `TestMaxK`、`TestTotalUnderlying`：`cmp.Ordered` 约束、`(T, ok)` 惯例、`~[]E`。
- `TestTopN`、`TestUniq`：`slices.SortFunc`、`Clone`，map 当 set。
- `TestOnceValue`：`sync.OnceValue` 懒单例。
- `TestSplitSeq`：迭代器版 `Split`，可早停。
- `TestOmitZero`：`omitzero` 标签。
- `TestFizzBuzz`：table-driven 加子测试。
- `TestHTTPKV`、`TestPathValue`：httpkv 全链路和 1.22 路由参数。
- `TestMetricsGuarded`：P13 加的，守护 `/metrics` 三个行为。

**预测**，先写答案再跑：

1. `MaxK([]string{"go", "rust", "zig"})` 返回什么？`MaxK([]int{})` 呢？
2. `Total(MyInts{1, 2, 3})` 能编译吗？把约束里的 `~` 删掉呢？
3. 并发 10 个 goroutine 各调一次 `OnceValue` 包出来的 `get()`，构造函数执行几次？
4. `TestFizzBuzz` 里 `FizzBuzz(15)` 和 `FizzBuzz(30)` 两条用例，子测试名各叫什么？

第 4 题实测：`go test -run TestFizzBuzz -v` 输出里是 `TestFizzBuzz/FizzBuzz` 和 `TestFizzBuzz/FizzBuzz#01`。子测试名取自期望输出，重名时后缀 `#01`。

**观察**：起 httpkv，发请求，再 `Ctrl-C`，看日志的 `shutting down`。

```sh
go run ./exercises/p8fast/httpkv/       # 终端 1，服务起在 :8090
curl -s -X PUT localhost:8090/kv/a -d '{"value":"x","ttl":5}'
curl -s localhost:8090/kv/a
curl -s -w "%{http_code}\n" -X PUT localhost:8090/kv/bad -d '{"value":"x","bogus":1}'
curl -s -X DELETE localhost:8090/kv/a -o /dev/null -w "%{http_code}\n"
```

实测依次是 `{"value":"x","at":...,"ttl":5}`、`400`、`204`。第一条 PUT 的响应体里 `at` 是零值，因为响应回显的是请求体，不是存进去的那份。

另外用 `netstat` 复现背景小节：把 `ReadHeaderTimeout` 去掉，再用 50 个 `nc` 挂半截头，看 `ESTABLISHED` 的数量。

**改造**：给 `MaxK` 写一个兄弟 `MaxKFunc[T any](xs []T, cmp func(a, b T) int) (T, bool)`，比较函数版，对照 Rust 的 `max_by`。写 table-driven 测试覆盖空切片、单元素、`strings.Compare` 和自定义结构体按字段比较。验收是 `go vet ./exercises/p8fast/ && go test -race ./exercises/p8fast/` 全绿。

## 章末参考

### Rust 对照

| Rust | Go | 差异 |
|---|---|---|
| `fn max<T: PartialOrd>` | `func Max[T cmp.Ordered]` | Go 不区分 `Ord` 与 `PartialOrd` |
| 单态化 | GC shape stenciling 加字典 | Go 有字典开销 |
| `impl Trait for T`、特化、const 泛型 | 都没有 | 泛型只做容器和工具函数 |
| `OnceLock` | `sync.OnceValue` | 闭包版 |
| `rstest` / `test-case` | table-driven 加 `t.Run` | 内建 |
| `cargo-fuzz` / `criterion` | `go test -fuzz` / `b.Loop` | 内建 |
| axum `Router` 加 extractor | `ServeMux` 加 `PathValue` | 参数全手写 |
| `serde` | `encoding/json` | 无 enum 策略、`flatten`、zero-copy |
| `deny_unknown_fields` | `DisallowUnknownFields` | Go 默认忽略 |
| `tokio::signal` 加 graceful | `signal.NotifyContext` 加 `Shutdown` | 一样的思路 |

### Java/Kotlin 与 TS/JS 类比

| 概念 | Java / Kotlin | TS / JS |
|---|---|---|
| 泛型约束 | `<T extends Comparable<T>>` | `<T extends number>`，运行时擦除 |
| 泛型实现 | 类型擦除，Kotlin `reified` | 类型擦除 |
| 多字段排序 | `Comparator.thenComparing` | `sort` 里用 `\|\|` 串联 |
| `OnceValue` | `by lazy`，静态内部类 | 模块级 `??=` |
| table-driven | `@ParameterizedTest` | `test.each` |
| fuzz | jqwik，Jazzer | fast-check |
| 路由 | `@GetMapping`，`@PathVariable` | Express `req.params` |
| 未知字段 | Jackson 默认报错 | `JSON.parse` 全接受，zod `.strict()` |
| 超时 | Tomcat `connectionTimeout` | Node `headersTimeout` |
| 优雅关停 | `server.shutdown=graceful` | `server.close()` |

### 标准库速查

**maps**：`maps.Keys(m)` 返回迭代器（1.23 起），`maps.Clone(m)` 拷贝。

**sync 扩展**：

- `sync.Map`：读多写少的并发 map，类型是 `map[any]any`。1.24 起内部实现换成 hash-trie，不是新公开 API。日常优先 `Mutex` 加 `map[K]V`，除非 profile 证明锁是瓶颈。
- `sync.OnceFunc(f)`：返回只执行一次的闭包。
- `sync.OnceValue(f)`：和 `sync.OnceFunc` 一样只执行一次，并缓存返回值。
- `sync.WaitGroup.Go(f)`：1.25 起，替代 `Add` 加 `Done` 的三行。
- `sync.Mutex.TryLock()`：非阻塞尝试。

**errgroup 与 context**：

- `errgroup.WithContext(ctx)` 对应 `try_join!` 加 `CancellationToken`。
- `errgroup.SetLimit(n)` 内建并发上限。
- `context.AfterFunc(ctx, f)`：1.21 起，ctx 取消时异步跑 f。
- `context.WithoutCancel(ctx)`：保留 values，不继承取消。
- `context.WithTimeoutCause(ctx, d, err)`：1.21 起，超时错误带原因。

**时间与随机**：

- `time.After`、`NewTicker`、`NewTimer` 之外还有 `time.AfterFunc(d, f)`。`Timer.Stop` 要配 drain，是经典坑。
- `math/rand/v2` 是项目规则要求的，`N` 和 `IntN` 并发安全，`rand.New(rand.NewPCG(seed1, seed2))` 可复现。

**encoding**：`json`、`binary.BigEndian`（wire 包手撕过）、`strconv`（比 `fmt` 快）。

### 迭代器签名

`slices.All` 的实际签名，来自 `go doc slices.All`：

```go
func All[Slice ~[]E, E any](s Slice) iter.Seq2[int, E]
```

链式有了对应物，生态主流仍是显式 `for`。
