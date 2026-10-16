# 05 错误与资源：错误是值，清理靠 defer

> 详细程度：深入 · 约 45 分钟
> 材料：TB18、TB22、TB23、TBX09 · 旧长版：`notes/p2_5-idiom.md`、`notes/p1_5-basics.md`、`notes/p19-ecosystem.md`
> 前置：04 接口与组合（error 本身就是一个接口）

## 先看一个让人愣住的现象

下面这段代码，把一个错误包装之后，用两种方式问"它是不是 `ErrNotFound`"，答案竟然相反。先自己猜一下三行输出，再往下读。

```go
package main

import (
	"errors"
	"fmt"
)

var ErrNotFound = errors.New("not found")

func main() {
	err := fmt.Errorf("load user: %w", ErrNotFound)
	fmt.Println("err == ErrNotFound:", err == ErrNotFound)
	fmt.Println("errors.Is(err, ErrNotFound):", errors.Is(err, ErrNotFound))
	fmt.Println(err)
}
```

```text
err == ErrNotFound: false
errors.Is(err, ErrNotFound): true
load user: not found
```

`==` 说不是，`errors.Is` 说是，`fmt.Println(err)` 打出来的却明明白白写着 "not found"。同一个值，两个"是不是"为什么打架？答案藏在两件事里：第一，`error` 只是一个普通接口，错误是**值**，可以被一层层包装；第二，包装之后 `==` 只能比较最外层，而 `errors.Is` 会沿着 `%w` 留下的链一路找到最底层。把这两点想清楚，Go 错误处理的大半就通了。

更底层地说，Go 把"出错"拆成两个互不相干的通道：预期内的失败走返回值，`error` 是普通接口，可以比较、包装、检视、传递；预期外的程序缺陷走 `panic`，它是"逻辑不该走到这里"的断言和最后一道安全网。资源清理不靠异常机制，靠 `defer`。三件事合起来就是 Go 错误处理的全部骨架，剩下都是套路。

## 和你已知语言的差异

| 问题 | Java / Kotlin | Rust | Go |
|---|---|---|---|
| 失败怎么表示 | 异常，checked 与 unchecked | `Result<T, E>` | 多返回值，`error` 放最后 |
| 编译器强制处理吗 | checked 异常强制 | `Result` 不用就有 `must_use` 警告 | 不强制，丢掉 `err` 是合法代码，靠 `errcheck` 等 lint |
| 传播 | 自动沿栈抛出 | `?` 一个字符 | 手写 `if err != nil { return ... }` |
| 加上下文 | 异常链、`getCause` | `anyhow::Context`、`thiserror` | `fmt.Errorf("...: %w", err)` |
| 资源释放 | `try-with-resources`（块级） | `Drop`（作用域级，自动） | `defer`（函数级，手写） |
| 不可恢复故障 | `Error`、`RuntimeException` | `panic!`，可 `catch_unwind` | `panic`，`recover` 只在 defer 里、只管本 goroutine |

最该警惕的两条差异：编译器不会逼你处理错误；`defer` 的作用域是**函数**，不是代码块。前者决定了你要靠纪律和 lint 守住错误处理，后者是一个最容易带着旧习惯写错的地方，第五节专门讲。

## 一、快乐路径：错误分支先返回，成功逻辑靠左

写错误处理的第一步不是选策略，而是先确定代码的形状。Go 社区管这个形状叫"快乐路径（Happy Path）"原则：只用单分支 `if`，出错就在分支内立刻返回，正常逻辑沿函数左边一路写到最后一行，执行到末尾就代表成功。〔TB18 p9-10〕

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

为什么这个形状好，数得出来的理由有三条：成功逻辑不缩进，读者从上到下一眼看到函数正常路径的全貌；每个错误分支自成一段、就近返回，不用在 `else` 里嵌套；最后一行 `return process(u)` 本身就宣告了"走到这里就是成功"。反过来，如果写成 `if 成功 { ... } else { ... }`，成功逻辑被埋进分支，缩进一层叠一层。

如果一段逻辑怎么都压不成快乐路径，通常说明函数在做太多事。办法是把缩进很深的 `else` 里的逻辑析出成独立函数，再对原函数套快乐路径。〔TB18 p10〕Rust 的 `?` 做的是同一件事：遇错提前返回。Go 没有这个语法，靠手写 `if err != nil`，代价是每层多两行，收益是错误路径就在眼前、不用猜 `?` 把控制流拐去了哪。

## 二、错误是值：四种处理策略，按耦合从低到高

`error` 就是 `interface{ Error() string }`。`errors.New` 与不带 `%w` 的 `fmt.Errorf` 返回的是同一个未导出类型 `*errors.errorString`，带 `%w` 时才换成会包装的 `*fmt.wrapError`（两点都用 `%T` 在本机实测过）。〔TB22 p4〕既然错误是值，调用方**怎么检视它**，就决定了两端耦合多深。标准做法按推荐优先级从上到下排，耦合逐级升高：〔TB22 p15-16〕

1. **透明处理**：不看内容，`err != nil` 就走错误路径，最多加一层上下文往上返回。80% 以上的场景属于这种，两端零耦合。〔TB22 p7〕
2. **行为特征检视**：错误类型实现某个小接口（比如 `Timeout() bool`），调用方只问行为、不问身份。这是透明处理之外最松的耦合，`net.Error` 是范本。〔TB22 p13-15〕（材料里的 `net.Error` 还带 `Temporary() bool`，现在该方法在标准库里已标注 Deprecated，只用 `Timeout()`。）
3. **哨兵错误**：包导出 `var ErrXxx = errors.New(...)`，调用方用 `errors.Is` 比较。哨兵成为包 API 的一部分，要长期维护。〔TB22 p8-10〕
4. **错误类型**：包导出 `XxxError` 类型，调用方用 `errors.As` 取字段。同样成为 API，而且耦合到具体字段。〔TB22 p10-13〕

一条反模式必须先记住，它和上面四条都相反：**永远不要拿 `err.Error()` 的字符串内容做分支**。构造方改一个字就破坏处理方，而且是隐式耦合，编译器帮不了你。〔TB22 p7-8〕下面这段代码把四条策略连同反模式的对立面跑一遍：

```go
package main

import (
	"errors"
	"fmt"
)

var ErrNotFound = errors.New("not found")

type ParseError struct{ Line int }

func (e *ParseError) Error() string { return fmt.Sprintf("syntax error at line %d", e.Line) }

func load() error { return fmt.Errorf("open db: %w", ErrNotFound) }

func main() {
	// 策略 1：透明，不关心内容
	if err := load(); err != nil {
		fmt.Println("load failed:", err)
	}

	// 策略 3：哨兵，errors.Is 沿 %w 链逐层比较
	err := fmt.Errorf("load rules: %w", fmt.Errorf("read file: %w", ErrNotFound))
	fmt.Println("errors.Is:", errors.Is(err, ErrNotFound))
	fmt.Println("== :", err == ErrNotFound)

	// 策略 4：错误类型，errors.As 取字段
	pe := &ParseError{Line: 7}
	err2 := fmt.Errorf("parse config: %w", pe)
	var got *ParseError
	if errors.As(err2, &got) {
		fmt.Println("parse line:", got.Line)
	}

	// Go 1.26 的泛型版 AsType，不用先声明变量
	got2, ok := errors.AsType[*ParseError](err2)
	fmt.Println("AsType:", ok, got2.Line)
}
```

```text
load failed: open db: not found
errors.Is: true
== : false
parse line: 7
AsType: true 7
```

对照开头的现象，这里把关键区别放在一起看：`err == ErrNotFound` 是 false，因为 `==` 只比较最外层包装，看不到链底；`errors.Is` 沿 `%w` 链逐层往下比，命中底层的 `ErrNotFound`，所以是 true。`errors.As` 同理，只是匹配目标是类型而非值，命中后把字段带出来。

策略 2（行为特征）和策略 4 用的是同一个 `errors.As`，区别只在目标类型：策略 4 的目标是具体错误类型，策略 2 的目标是一个只含行为方法的小接口：

```go
var t interface{ Timeout() bool }
if errors.As(err, &t) && t.Timeout() {
	// 按超时处理，不关心具体是 os.ErrDeadlineExceeded 还是别的
}
```

问"是否超时"比问"是不是这个哨兵错误"更稳：超时可能来自好几处，本机实测 `context.DeadlineExceeded` 的 `errors.Is(e, os.ErrDeadlineExceeded)` 是 false，但 `Timeout()` 是 true；只比哨兵就会漏掉它。这一层在判断题第 3 题展开。

Go 1.26 本机已有泛型版 `errors.AsType[E error](err error) (E, bool)`，上面的 `errors.As` 可以写成 `pe, ok := errors.AsType[*ParseError](err)`，不用先声明变量。〔实测〕`go doc errors.AsType` 与上面的例子都在 go1.26.4 上跑过。

## 三、什么时候包装，什么时候切断

`%w` 保留链路，`%v` 只留文字，选哪个取决于你想不想让调用方继续识别。

- **包内、层与层之间**：用 `%w`。每层写"这一层在干什么"，顶层读出来就是一条因果链，比如 `load user: open db: not found`。
- **对外 API 的边界**：用 `%v` 翻译。避免内部错误（`sql.ErrNoRows`、第三方库的错误）泄漏成你的公开契约。哨兵和错误类型一旦导出就是 API，所以要主动控制暴露面。

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

`%v` 切链之后，调用方只能看到 "handle failed: ..." 这串字，`errors.Is` 和 `errors.As` 都穿透不到内部错误，内部实现想怎么改就怎么改。代价是调用方失去了按身份或类型分支的能力，所以只对"你不想让外面依赖的内部错误"用。

并发里多个 goroutine 各自出错，用 `errors.Join`（1.20 起）合并，`Is` 和 `As` 会穿透所有子错误。`errgroup` 只返回第一个错误，要全部就用 `Join`。

标准库错误没有调用栈。以前流行用 `pkg/errors` 在抛出处记录栈，用 `%+v` 打印；〔TBX09 p2-7〕现在的主流做法是每层 `%w` 加上下文，确实要栈再接日志或追踪系统。

## 四、别忽略错误

健壮性的三条底线：不要相信外部输入（校验失败立即返回预设错误）；不要忽略任何一个错误；不要假定异常不会发生。〔TB23 p2〕前两条靠纪律，第三条靠 `panic`/`recover`，第六节讲。

Go 不逼你处理，丢掉 `err` 是合法代码，所以漏检只能靠习惯和工具：`errcheck` 这类 linter（`go vet` 不负责查未检查的错误）。最常见的疏漏不是忘了写 `if err != nil`，而是写了但写错地方，比如 `defer f.Close()`：写文件时 `Close` 的错误才是真正的写入失败，直接吞掉会让"看起来成功了"的假象通过。下一节解决。

## 五、defer：清理的纪律

`defer` 把函数调用注册进当前 goroutine 的 deferred 栈，函数退出前按后进先出执行。无论是尾部返回、错误分支 `return` 还是 `panic`，都会执行。〔TB23 p12-13〕申请资源成功后**紧接着**写释放，成对出现，错误分支只剩一个 `return err`，不再有"漏一个分支就泄漏"的问题。〔TB23 p11-14〕

三个必须记住的语义，每一个都能独立咬人。

**1. 参数在注册时求值，函数体在退出时执行。** 〔TB23 p16-18〕这是最容易背错的一条：`defer` 后面写的东西，参数现在就算好，执行却要等到函数退出。

```go
package main

import "fmt"

func main() {
	for i := 0; i < 3; i++ {
		defer fmt.Println("arg:", i) // i 此刻被求值
	}
	j := 10
	defer func() { fmt.Println("closure:", j) }() // 闭包读退出时的 j
	j = 20
}
```

```text
closure: 20
arg: 2
arg: 1
arg: 0
```

输出把两条规则都摆出来了：循环里的 `i` 是注册时求值的，所以打出 2、1、0（后进先出）；闭包没有参数，读的是函数退出那一刻的 `j`，所以是 20 而不是 10。

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

`err` 是命名返回值，defer 里的匿名函数在 `return` 之后、真正返回之前执行，所以能在最后关头把 `Close` 的错误塞进 `err`。`err == nil` 这个条件保证不覆盖前面已经发生的真实错误。

性能上不必犹豫：Go 1.12 时 defer 约比无 defer 慢 8 倍，1.13 起多次优化，1.17 已降到约 1.45 倍，常规代码可以放心用。〔TB23 p19-20〕

## 六、panic 和 recover：一道最后的防线

`panic` 之后，当前函数停止，已注册的 `defer` 照常执行，控制权逐层返回调用者，直到本 goroutine 的栈走完，进程崩溃。`recover` 只在 deferred 函数里调用才有效，捕获后 panicking 过程终止，函数正常返回。〔TB23 p3-6〕先看最基础的用法：

```go
package main

import "fmt"

func main() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("recovered:", r)
		}
	}()
	fmt.Println("before panic")
	panic("boom")
	fmt.Println("after panic")
}
```

```text
before panic
recovered: boom
```

`after panic` 没打印，因为 `panic` 之后函数就停了；但 `defer` 照跑，`recover` 在 deferred 函数里直接把 `"boom"` 接住，程序没有崩溃。

"recover 只在 deferred 函数里调用才有效"这句要抠字眼，看两个实测的细节：

```go
package main

import "fmt"

func helper() any { return recover() }

func a() (r any) {
	defer func() { r = helper() }() // helper 不是被 defer 的那个函数
	panic("boom")
}

func b() (r any) {
	defer func() { r = recover() }() // 直接在 deferred 函数里调，才有效
	panic("boom")
}

func main() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("main recovered:", r)
		}
	}()

	fmt.Println("b:", b())
	a()
	fmt.Println("unreachable")
}
```

```text
b: boom
main recovered: boom
```

`b()` 里 `recover` 直接写在 deferred 函数体里，返回 `"boom"`，正常收场。`a()` 里 `recover` 被包在 `helper` 里，已经不是 deferred 函数本身在调，于是返回 nil，panic 没被接住，继续穿透 `a` 冒到 `main`，才被 `main` 自己的 `defer recover` 接住。最后一个 "unreachable" 永远打不出来。这个差别决定了你在写 recover 时不能把它藏进一个被间接调用的函数里。

另一个关键事实：**`recover` 只管当前 goroutine**。在 `main` 里 defer recover，挡不住子 goroutine 的 panic，整个进程以 exit status 2 退出。所以长期运行的服务里，每个自己启动的 goroutine 入口都要有 `defer recover`。`net/http` 为每个连接的 goroutine 做了这一层。〔TB23 p6-7〕

何时用 `panic`：

- **断言式**：材料的判断是标准库里大多数 `panic` 是"这里不可能到达"的断言，如 `encoding/json` 的 "decoder out of sync"（本机 go1.26 源码里仍在）。去掉它不改逻辑，但失去 bug 提醒。〔TB23 p8-9〕
- **启动期**：配置缺失、模板解析失败这类"没法继续"的初始化，`MustXxx` 风格直接 panic。
- **不要当错误返回**：库的调用者无从知道你会 panic，也没有义务处理。Java 的 checked exception 对应 Go 的 `error`，`panic` 更接近 `RuntimeException` 加 `Error`，别混淆。〔TB23 p9-10〕

## 七、Option 模式：可选参数怎么办

Go 没有默认参数，也没有重载。构造函数参数一多，每次加字段都要改所有调用方，而且可选值只能传零值，分不清"用户传了 0"和"没传"。〔TBX09 p11-12〕解法是函数类型加可变参数：

```go
package main

import (
	"fmt"
	"time"
)

type Server struct {
	addr    string
	timeout time.Duration
	retries int
}

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

func main() {
	def := New(":80")
	custom := New(":80", WithTimeout(time.Second), WithRetries(0))
	fmt.Printf("default: %s %s %d\n", def.addr, def.timeout, def.retries)
	fmt.Printf("custom:  %s %s %d\n", custom.addr, custom.timeout, custom.retries)
}
```

```text
default: :80 5s 3
custom:  :80 1s 0
```

`WithRetries(0)` 里的 0 是用户的明确选择，和"没传"区分开了。新增配置只加一个 `WithXxx`，`New` 的签名不变。材料说 gorm、go-redis 等库都用这种写法，〔TBX09 p14〕这一条我没有逐个核对；本机能确认的例子是 `grpc.NewClient(target string, opts ...DialOption)`。Rust 里对应 builder，Java 里对应 builder 或重载。选项本身也可以返回 `error`，用于参数校验。

## 小结

1. 错误分支先返回，成功逻辑靠左，执行到末尾就是成功；压不平就析出函数。
2. 错误是值，检视方式决定耦合：透明处理零耦合，行为特征、哨兵、错误类型逐级升高；永远不比较 `err.Error()` 字符串。
3. `errors.Is` 沿 `%w` 链找值，`errors.As` 找类型；`==` 只看最外层。
4. 包内用 `%w` 建链，对外 API 边界用 `%v` 切断，控制暴露面。
5. 不信任输入、不忽略错误、不假定无异常；`defer` 的释放紧挨申请处成对出现。
6. defer 参数注册时求值，作用域是函数，能改命名返回值；性能开销已可忽略。
7. `panic` 是断言和安全网，不是错误返回；`recover` 只在 deferred 函数里、只救当前 goroutine。
8. 参数一多，用 Option 模式加 `WithXxx`，构造签名不变。

## 常见坑

1. `if err := f(); err != nil` 里用 `:=` 遮蔽了外层同名的 `err`，外面的 `err` 仍是 nil。〔TB18 p8〕
2. 拿 `err.Error()` 的字符串做 `switch`/`==`，构造方改一个词就破坏处理方。
3. 包装后还拿 `==` 比较哨兵错误，得换成 `errors.Is`。
4. 对外直接 `%w` 暴露第三方或内部错误，把实现细节锁死成 API。
5. 循环里 `defer`，句柄或锁到函数结束才放。
6. `defer f.Close()` 吞掉写入失败；`defer mu.Unlock()` 之后函数跑很久，锁持有时间被拉长。
7. 子 goroutine 没有 `recover`，一个 panic 带走整个进程。
8. `recover` 藏在被间接调用的函数里，返回 nil，panic 照旧往外传。

## 判断题与答案

先自己答，再看后面。

1. 下面的 `defer` 输出什么：`for i := 0; i < 3; i++ { defer fmt.Print(i) }`？
2. 在 `main` 里 `defer recover()` 能挡住子 goroutine 的 panic 吗？
3. 为什么 `Timeout() bool` 比 `errors.Is(err, os.ErrDeadlineExceeded)` 更适合当"调用方依赖点"？
4. `errors.Is(err, ErrNotFound)` 为 true，`err == ErrNotFound` 却为 false，为什么？

**答案**

1. 输出 `210`。`fmt.Print(i)` 的 `i` 在每次注册时求值，压栈后按后进先出打印。
2. 不能。`recover` 只管当前 goroutine，子 goroutine 的 panic 沿自己的栈向上，越不过 goroutine 边界，进程以 exit status 2 退出。
3. 超时的来源不止一个：本机实测 `context.DeadlineExceeded` 不满足 `errors.Is(_, os.ErrDeadlineExceeded)`，却满足 `Timeout() == true`。`Timeout()` 是一个行为契约，一次提问覆盖所有来源；比较哨兵只认一种来源，调用方还得知道有哪些哨兵，耦合更深。〔TB22 p13-16〕
4. 错误被 `%w` 包装后，`==` 只比较最外层，看不到链底；`errors.Is` 沿 `%w` 链逐层找，命中底层的 `ErrNotFound`。

练习包 `exercises/p2_5idiom/` 的 `TestErrorChain`（链路、`Is`、`As`、`%w` 与 `%v`）、`TestGuardClause`（快乐路径重构）；`exercises/p19eco/` 的 `TestFunctionalOptions`（Option 模式）。

## 在项目里

- **行为特征检视**：`dnsproxi-go/metrics.go` 的 `upstreamErrReason` 先 `errors.As` 到 `net.Error` 再问 `Timeout()`，再用 `errors.Is` 比较 `syscall.ECONNREFUSED`，最后兜底归类为 `network`。正是第二节策略 2 和 3 的组合。
- **包装链**：`dnsproxi-go/main.go` 的 `Run` 里对解析监听、绑定、读包失败都用 `fmt.Errorf("...: %w", err)`，调用者看到的是 `listen :5353: ...` 这样的完整因果。
- **goroutine 入口 recover 与 defer 清理**：同一个 `Run` 里为每个查询启动的 goroutine（`handleQuery` 的调用处），开头 `defer func() { if r := recover(); r != nil { ... } }()` 并记日志，防止单个查询的 panic 杀死整个代理；`dnsproxi-go/cache/expire.go` 里 `defer t.Stop()` 与 `defer close(stats)` 紧挨着申请处。

## 想深入:正文之外的干货

### TB18 控制结构的完整规则

- 分支与循环的取舍遵循"一件事情仅有一种做法"：分支保留 if 与 switch-case（填平了 C 里每个 case 都要写 break 的坑，case 还能用表达式列表合并同逻辑分支），循环只留 for 一种，C 的 while 与 do-while 都被去掉。顺序、分支、循环三大基本控制结构能构造任意算法，做减法不损失表达能力。〔TB18 p1-2〕
- if 的三条硬规则：左大括号与 if 同行（gofmt 强制统一）；布尔表达式整体不加括号；表达式求值结果必须是布尔类型，不像 C 那样拿整数当条件。〔TB18 p2-3〕
- 优先级陷阱记一个案例：`a && b != true` 等价于 `a && (b != true)`，不是 `(a && b) != true`，因为 != 的优先级高于 &&。作者自己承认从 C 时代就记不住优先级，他的办法是给每个子表达式加小括号，消除心智负担。〔TB18 p4-6〕
- if 自用变量（`if v, ok := m[k]; ok`）的声明与条件用分号分隔，作用域仅限 if 语句的各级隐式代码块（含 else if 与 else），外部不可访问；惯用法换来行数少、隔离性好，但与短变量声明结合是变量遮蔽的重灾区，03 节的作用域规则在这里最容易踩。〔TB18 p7-8〕
- 三种形式的可读性排序是单分支 > 二分支 > 多分支，日常应减少多分支；真要写多分支，把命中概率高的条件放前面性能最好（表达式按命中概率降序排列）。留言区一条判断值得记住：快乐路径适合错误判断，单纯"选一个值"的场景用 switch-case 更自然。〔TB18 p11〕〔TB18 p14〕

### TB22 错误设计的来路与两个标准库样本

- Go 的错误处理继承自 C：整型返回值作错误状态标识，0 成功、非 0 失败，调用者按值比较选路径。这套机制的好处是错误显式、是普通值，易调试易测试，契合 Go 哲学，所以被沿用。〔TB22 p2〕
- C 的痛点是单返回值被迫"一值多用"：`fprintf` 的返回值既表字符数又表负数错误码，返回字符串时更难融合，只能靠输出参数或自定义结构体，做法不统一。Go 用多返回值把错误状态与返回信息拆开，惯例把 error 放在返回值列表末尾，`fmt.Fprintf` 的 `(n, err)` 就是 C `fprintf` 的解耦版。〔TB22 p2-3〕
- error 接口带来三大好处：统一错误类型（跨包跨项目可读、策略统一）、错误是值（可比较可传递，检视体验与 C 一致）、易扩展（自定义类型携带上下文）。error 接口是错误值提供者与检视者之间的契约，具体上下文与接口解耦，体现"正交"理念。〔TB22 p6〕
- 需要更多上下文时的范本是 `net.OpError`：携带 Op、Net、Source、Addr、Err 五个字段，标准库内部用类型断言检视 `oe.Op == "read"` 来分类网络读错误。正文策略 4 的 `errors.As` 取字段就是这条路的通用化。〔TB22 p5-6〕
- `errors.As` 匹配成功后存进第二参数的是链中最深处的原始错误值，对取出值再与原始错误判 `==` 会得到 true；这正是"用 errors.As 替代类型断言"的原因：断言只看最外层动态类型。〔TB22 p12-13〕
- 策略 2 在标准库的原始用例是 `http.Server.Serve` 对 Accept 错误做 `ne.Temporary()` 判断后 sleep 重试；如今 `Temporary()` 已标注 Deprecated，但"按行为特征分支"的思路由 `Timeout()` 延续。〔TB22 p14〕

### TB23 defer 与 panic 的补充细节

- 不是什么都能直接 defer：自定义函数与方法无条件支持（有返回值会在调度执行时自动丢弃）；内置函数里 close、copy、delete、print、println、recover、panic 可以直接 defer，而 append、cap、len、make、new 这些有返回值的直接 defer 会编译错 `defer discards result`，要用匿名函数包一层。〔TB23 p14-16〕
- `net/http` 连接 goroutine 的 recover 有个细节：`err != ErrAbortHandler` 才打印栈。ErrAbortHandler 是服务器故意用来中止连接的内部哨兵，静默吞掉不算错误；自己写 recover 时可借鉴"有意的 panic 不告警"这个模式。〔TB23 p6-7〕
- defer 必须在 panic 触发之前注册才会执行：panic 之后才写的 defer 来不及进栈，接不住当前这次 panic。留言区把三条铁律收成一句话：defer 要在 panic 前、recover 只能在 defer 中调、recover 只救本协程。〔TB23 p22-23〕

### TBX09 工程化写法的三个补遗

- pkg/errors 对准的需求有两点，官方 error 当年只满足第一点半：附加信息（可嵌套的描述）与附加堆栈（知道出错的具体代码行）。课程例子里 sub1、sub2 两个包返回同名的 "diff error"，标准库版本在 main 里无法分辨是哪一层抛的；换成 pkg/errors 后 `fmt.Printf("%+v", err)` 直接打印到 sub2.go 第 7 行。〔TBX09 p2-6〕
- 打印堆栈的机制是借 fmt 的特性：fmt 打印 error 前检查对象是否实现 `Formatter` 接口（单个 `Format(f State, c rune)` 方法），实现了就走它。pkg/errors 的构造函数封装 `fundamental{msg, stack}` 结构实现该接口，verb 为 v 且带 + 标志时打印 msg 加堆栈，v 或 s 只打 msg，q 打转义信息。这就是只有 `%+v` 才带堆栈的原因。〔TBX09 p6-7〕
- 作者的应用建议是应用层用它换堆栈、库层慎选（避免传染依赖）；正文第三节的收束是 wrapping 普及后回归 `%w` 链，两个结论不冲突：一个是当年的工程惯例，一个是后来的演进。〔TBX09 p2-7〕
- 同一单元还有两个与错误处理主线无关的技巧：slice 初始化能预估容量就用 `make([]int, 0, 5)` 补全 cap，避开 growslice 的扩容搬迁（机制细节归 03 节切片扩容）；多段参数解析逻辑用裸大括号分块，块内 err 每次直接 `:=` 新建，不用纠结前面定义过没有，块内变量不泄漏到外部，IDE 还能按块折叠，读者按需进入。〔TBX09 p8-10〕〔TBX09 p15-17〕

省略未吸收：TBX09 的 growslice 机制细节与 03 节切片扩容重复（此处只留结论）；TB18 的 switch 改进与复杂条件写法（与错误处理主线无涉）及正文笔误辨析（已失去意义）；TB22 两个依赖网络环境的标准库实例（需 mock 改造）；TB23 与正文重复的留言问答。其余必读条目已全部融入正文或本块。

## 想深入去哪查

- 原材料：TB18（控制结构与快乐路径）、TB22（四种错误策略，含 `net.OpError`、`json.UnmarshalTypeError` 实例）、TB23（panic、recover、defer 机制与性能）、TBX09（`pkg/errors` 原理、Option 写法）。
- 旧长版：`notes/p2_5-idiom.md` 的"错误链""提前返回"，`notes/p1_5-basics.md` 的"defer""panic 与 recover"，`notes/p19-ecosystem.md` 的 functional options。
- 不在这一节：`defer` 的底层结构与开放编码，遇到再查。
