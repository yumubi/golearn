# 05 错误与资源：错误是值，清理靠 defer

> 详细程度：深入 · 约 30 分钟
> 材料：TB18、TB22、TB23、TBX09 · 旧长版：`notes/p2_5-idiom.md`（错误链、提前返回）、`notes/p1_5-basics.md`（defer、panic）、`notes/p19-ecosystem.md`（functional options）
> 前置：04 接口与组合（错误本身就是一个接口）

## 这一节要建立的认知

Go 把"出错"拆成两个互不相干的通道：

1. **预期内的失败**走返回值。`error` 是一个普通接口，错误和其他数据一样可以比较、包装、检视、传递。
2. **预期外的程序缺陷**走 `panic`。它不是控制流，是"逻辑不该走到这里"的断言和最后一道安全网。

资源清理不靠异常机制，靠 `defer`：在申请资源的地方紧挨着写释放，函数退出时按后进先出执行。三件事合起来，就是 Go 错误处理的全部骨架，剩下都是套路。

## 和你已知语言的差异

| 问题 | Java / Kotlin | Rust | Go |
|---|---|---|---|
| 失败怎么表示 | 异常，checked 与 unchecked | `Result<T, E>` | 多返回值，`error` 放最后 |
| 编译器强制处理吗 | checked 异常强制 | `Result` 不用就有 `must_use` 警告 | 不强制，丢掉 `err` 是合法代码，靠 `errcheck` 等 lint |
| 传播 | 自动沿栈抛出 | `?` 一个字符 | 手写 `if err != nil { return ... }` |
| 加上下文 | 异常链、`getCause` | `anyhow::Context`、`thiserror` | `fmt.Errorf("...: %w", err)` |
| 资源释放 | `try-with-resources`（块级） | `Drop`（作用域级，自动） | `defer`（函数级，手写） |
| 不可恢复故障 | `Error`、`RuntimeException` | `panic!`，可 `catch_unwind` | `panic`，`recover` 只在 defer 里、只管本 goroutine |

最该警惕的差异是两条：编译器不会逼你处理错误；`defer` 的作用域是**函数**，不是代码块。

## 一、快乐路径：错误分支先返回，成功逻辑靠左

控制结构里最影响可读性的一条：只用单分支 `if`，出错就在分支内立刻返回，正常逻辑沿着函数左边一路写到最后一行。函数执行到末尾，就代表成功。〔TB18 p9-10〕

```go
func handle(name string) error {
	if name == "" {
		return ErrNoUser
	}
	u, err := load(name)
	if err != nil {
		return fmt.Errorf("load %s: %w", name, err)
	}
	if !u.Active {
		return ErrInactive
	}
	return process(u)
}
```

做不到快乐路径，说明函数在做太多事。办法是把缩进很深的 `else` 里的逻辑析出成独立函数，再对原函数重构。〔TB18 p10〕Rust 的 `?` 和卫语句是同一个方向，Java 里对应的是尽早 `return` 或 `throw`。

## 二、错误是值：四种处理策略，按耦合从低到高

`error` 就是 `interface{ Error() string }`。`errors.New` 和 `fmt.Errorf` 返回的是同一个未导出类型。〔TB22 p3-4〕既然是值，调用方怎么检视它，就决定了两端耦合多深。四种策略，推荐优先级从上到下：〔TB22 p15-16〕

1. **透明处理**：不看内容，`err != nil` 就走错误路径，最多加一层上下文往上返回。80% 以上的场景属于这种，两端零耦合。〔TB22 p7〕
2. **行为特征检视**：错误类型实现某个小接口（比如 `Timeout() bool`），调用方只问行为、不问身份。这是透明处理之外最松的耦合。`net.Error` 是范本。〔TB22 p13-15〕
3. **哨兵错误**：包导出 `var ErrXxx = errors.New(...)`，调用方用 `errors.Is` 比较。哨兵成为包 API 的一部分，要长期维护。〔TB22 p8-10〕
4. **错误类型**：包导出 `XxxError` 类型，调用方用 `errors.As` 取字段。同样成为 API，而且耦合到字段。〔TB22 p10-13〕

一条反模式要记住：**永远不要拿 `err.Error()` 的字符串内容做分支**。构造方改一个字就破坏处理方，而且是隐式耦合。〔TB22 p7-8〕

```go
err := fmt.Errorf("load rules: %w", fmt.Errorf("read /etc/rules: %w", ErrNotFound))
fmt.Println(err)                          // load rules: read /etc/rules: not found
fmt.Println(errors.Is(err, ErrNotFound))  // true，沿 %w 链逐层比较
fmt.Println(err == ErrNotFound)           // false，== 在包装后失效
```

取字段用 `errors.As`，行为检视也可以用接口做目标类型：

```go
var pe *ParseError
if errors.As(err, &pe) { fmt.Println(pe.Line) }

var t interface{ Timeout() bool }
if errors.As(err, &t) && t.Timeout() { /* 按超时处理 */ }
```

Go 1.26 本机已有泛型版 `errors.AsType[E error](err error) (E, bool)`，写成 `pe, ok := errors.AsType[*ParseError](err)`，不用先声明变量。〔实测〕`go doc errors.AsType` 与上面的例子都在 go1.26.4 上跑过。

## 三、什么时候包装，什么时候切断

`%w` 保留链路，`%v` 只留文字，选哪个取决于你想不想让调用方继续识别。

- **包内、层与层之间**：用 `%w`，每层写"这一层在干什么"，顶层读起来就是一条因果链。
- **对外 API 的边界**：用 `%v` 翻译，避免内部错误（`sql.ErrNoRows`、第三方库的错误）泄漏成你的公开契约。哨兵和错误类型一旦导出就是 API，所以要主动控制暴露面。

```go
func Public(name string) error {
	if err := handle(name); err != nil {
		if errors.Is(err, ErrInactive) {
			return ErrInactive // 稳定的、你愿意承诺的错误
		}
		return fmt.Errorf("handle failed: %v", err) // %v 切断链路
	}
	return nil
}
```

并发里多个 goroutine 各自出错，用 `errors.Join`（1.20 起）合并，`Is` 和 `As` 会穿透所有子错误。`errgroup` 只返回第一个错误，要全部就用 `Join`。

关于调用栈：标准库错误没有栈。以前流行用 `pkg/errors` 在抛出处记录栈，用 `%+v` 打印。〔TBX09 p2-7〕现在的主流做法是：每层 `%w` 加上下文，足够定位；确实要栈再接日志或追踪系统。

## 四、再强调一遍：别忽略错误

健壮性的三条底线：不要相信外部输入（校验失败立即返回预设错误）；不要忽略任何一个错误；不要假定异常不会发生。〔TB23 p2〕Go 不逼你处理，所以这条要靠习惯和 `errcheck`、`go vet`。最常见的疏漏恰恰是 `defer f.Close()`：写文件时 `Close` 的错误才是真正的写入失败，下一节解决。

## 五、defer：清理的纪律

`defer` 把函数调用注册进当前 goroutine 的 deferred 栈，函数退出前按后进先出执行。无论是尾部返回、错误分支 `return` 还是 `panic`，都会执行。〔TB23 p12-13〕申请资源成功后**紧接着**写释放，成对出现，错误分支只剩一个 `return err`，不再有"漏一个分支就泄漏"的问题。〔TB23 p11-14〕

三个必须记住的语义：

**1. 参数在注册时求值，函数体在退出时执行。** 〔TB23 p16-18〕

```go
for i := 0; i < 3; i++ {
	defer fmt.Print("arg=", i, " ") // i 此刻被求值
}
j := 10
defer func() { fmt.Print("closure j=", j, " ") }() // 闭包读退出时的 j
j = 11
// 输出：closure j=11 arg=2 arg=1 arg=0
```

**2. defer 的作用域是函数，不是代码块。** 循环里 `defer f.Close()` 要等整个函数返回才关，循环打开几千个文件会耗尽句柄。要么把循环体抽成函数，要么手动关闭。Rust 的 `Drop` 和 Java 的 `try-with-resources` 都是块级，这是最容易带着旧习惯写错的地方。

**3. defer 可以改命名返回值。** 这是"把 Close 的错误带出来"的标准写法：

```go
func write() (err error) {
	f, err := os.CreateTemp("", "x")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr // 写入没错但落盘失败，别吞掉
		}
	}()
	_, err = f.WriteString("hi")
	return err
}
```

性能上不必犹豫：Go 1.12 时 defer 约比无 defer 慢 8 倍，1.13 起多次优化，1.17 已降到约 1.45 倍，常规代码可以放心用。〔TB23 p19-20〕

## 六、panic 和 recover：一道最后的防线

`panic` 之后，当前函数停止，已注册的 `defer` 照常执行，控制权逐层返回调用者，直到本 goroutine 的栈走完，进程崩溃。`recover` 只在 deferred 函数里调用才有效，捕获后 panicking 过程终止，函数正常返回。〔TB23 p3-6〕

两个实测的细节：

```go
func helper() any { return recover() }

func a() (r any) {
	defer func() { r = helper() }() // helper 不是被 defer 的那个函数，recover 返回 nil
	panic("boom")
}
func b() (r any) {
	defer func() { r = recover() }() // 直接在 deferred 函数里调，才有效
	panic("boom")
}
```

```text
go run .   → a() 的 panic 逃出，被外层的 recover 接住；b() 返回 "boom"
go run . x → panic: in goroutine ... exit status 2
```

第二行对应另一个关键事实：**`recover` 只管当前 goroutine**。在 `main` 里 defer recover，挡不住子 goroutine 的 panic，整个进程直接退出。所以长期运行的服务里，每个自己启动的 goroutine 入口都要有 `defer recover`。`net/http` 为每个连接的 goroutine 做了这一层。〔TB23 p6-7〕

何时用 `panic`：

- **断言式**：标准库里大多数 `panic` 是"这里不可能到达"的断言，如 `encoding/json` 的 "decoder out of sync"。去掉它不改逻辑，但失去 bug 提醒。〔TB23 p8-9〕
- **启动期**：配置缺失、模板解析失败这类"没法继续"的初始化，`MustXxx` 风格直接 panic。
- **不要当错误返回**：库的调用者无从知道你会 panic，也没有义务处理。Java 的 checked exception 对应 Go 的 `error`，`panic` 对应 `RuntimeException` 和 `Error`，别混淆。〔TB23 p9-10〕

## 七、Option 模式：可选参数怎么办

没有默认参数，也没有重载。构造函数参数一多，每次加字段都要改所有调用方，而且可选值只能传零值，分不清"用户传了 0"和"没传"。〔TBX09 p11-12〕解法是函数类型加可变参数：

```go
type Option func(*Server)

func WithTimeout(d time.Duration) Option { return func(s *Server) { s.timeout = d } }
func WithRetries(n int) Option           { return func(s *Server) { s.retries = n } }

func New(addr string, opts ...Option) *Server {
	s := &Server{addr: addr, timeout: 5 * time.Second, retries: 3} // 先填默认值
	for _, o := range opts {
		o(s)
	}
	return s
}

New(":80", WithTimeout(time.Second), WithRetries(0)) // 0 是用户的明确选择
```

新增配置只加一个 `WithXxx`，`New` 的签名不变。`gorm`、`go-redis` 等库都这样。〔TBX09 p12-14〕Rust 里对应 builder，Java 里对应 builder 或重载。选项本身也可以返回 `error`，用于参数校验。

## 常见坑

1. `if err := f(); err != nil` 里用 `:=` 遮蔽了外层同名的 `err`，外面的 `err` 仍是 nil。〔TB18 p8〕
2. 用 `err == ErrX` 判断被 `%w` 包装过的错误，永远 false，必须 `errors.Is`。
3. 对外 API 直接 `%w` 暴露第三方库的错误，升级依赖就破坏调用方。
4. 循环里 `defer`，句柄或锁到函数结束才放。
5. `defer f.Close()` 吞掉写入失败；`defer mu.Unlock()` 之后函数跑很久，锁持有时间被拉长。
6. 子 goroutine 没有 `recover`，一个 panic 带走整个进程。

## 判断题（先答再看）

1. 为什么 `fmt.Errorf("x: %v", err)` 之后 `errors.Is(w, ErrX)` 变 false？什么场景下你**希望**这样？
2. 下面的 `defer` 输出什么：`for i := 0; i < 3; i++ { defer fmt.Print(i) }`？
3. 在 `main` 里 `defer recover()` 能挡住子 goroutine 的 panic 吗？
4. 为什么 `net.Error` 的 `Timeout()` 比 `errors.Is(err, os.ErrDeadlineExceeded)` 更适合当"调用方依赖点"？

练习包：`exercises/p2_5idiom/` 的 `TestErrorChain`（链路、`Is`、`As`、`%w` 与 `%v`）、`TestGuardClause`（快乐路径重构）；`exercises/p19eco/` 的 `TestFunctionalOptions`（Option 模式）。

## 在项目里

- **行为特征检视**：`dnsproxi-go/metrics.go` 的 `upstreamErrReason` 先 `errors.As` 到 `net.Error` 再问 `Timeout()`，再用 `errors.Is` 比较 `syscall.ECONNREFUSED`，最后兜底归类，保证分类器自己不会出错。正是上面策略 2 和 3 的组合。
- **包装链**：`dnsproxi-go/main.go` 的 `Run` 里对监听、读包失败都用 `fmt.Errorf("...: %w", err)`，调用者看到的是"listen :5353: ..."这样的完整因果。
- **goroutine 入口 recover**：同一个 `Run` 里为每个查询启动的 goroutine，开头 `defer func() { if r := recover(); r != nil { ... } }()` 并记日志，防止单个查询的 panic 杀死整个代理。
- **defer 清理**：`dnsproxi-go/cache/expire.go` 里 `defer t.Stop()` 与 `defer close(stats)` 紧挨着申请处，是定时器和通道清理的写法。

## 想深入去哪查

- 原材料：TB18（控制结构与快乐路径）、TB22（四种错误策略，含 `net.OpError`、`json.UnmarshalTypeError` 实例）、TB23（panic、recover、defer 机制与性能）、TBX09（`pkg/errors` 原理、Option 写法）。
- 旧长版：`notes/p2_5-idiom.md` 的"错误链""提前返回"，`notes/p1_5-basics.md` 的"defer""panic 与 recover"，`notes/p19-ecosystem.md` 的 functional options。
- 不在这一节：`pkg/errors` 的 `Formatter` 实现细节〔TBX09 p6-7〕、`defer` 的底层结构与开放编码，遇到再查。
