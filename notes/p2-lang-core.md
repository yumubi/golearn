# P2 · 语言核心：用 Rust 的经验写 Go，哪里需要换直觉？

> 这章学完你能干什么：读懂任意 Go 工程里的接口分派和错误传播，并预言一段代码的行为。包括 `speakAll([]Speaker{Dog{}, Robot{}})` 打印什么、嵌入后为什么不能当父类用、`errors.Is` 顺着什么链找 sentinel。
> 读者：熟悉 Rust。前置：P1.5。后续：P2.5 惯用法，P3 并发。
>
> 动手方式：开一个空目录，`go mod init demo`，每个例子存成 `main.go`，用 `go run main.go` 运行。片段省略了 `package main` 和 `import`。

## 体系背景：接口值在内存里长什么样

先看三个数字：

```go
var s Speaker = Dog{"rex"}
var n int
fmt.Println("int 的大小:", unsafe.Sizeof(n))
fmt.Println("接口值的大小:", unsafe.Sizeof(s))
```

```sh
$ go run main.go
int 的大小: 8
接口值的大小: 16
```

在本次 64 位实现中，接口值占两个机器字。可用（动态类型、动态值）理解它：非空接口的运行时表示通常借助 itab 查找方法；空接口表示有所不同，具体布局是实现细节，不是语言保证。

通过接口调用方法时，运行时依据动态类型选择实现；编译器也可能优化掉间接调用。Rust trait object 的 vtable 是有帮助的类比，但布局和优化并非语言契约。

Go 每种类型都有定义好的零值；不必假设其内存表示一定是全零字节。

## 接口

假设你有 `Dog` 和 `Robot` 两个类型，想用同一个函数处理它们。先给它们各一个 `Speak` 方法：

```go
type Speaker interface{ Speak() string }

type Dog struct{ Name string }
func (d Dog) Speak() string { return d.Name + ": woof" }

type Robot struct{ ID int }
func (r Robot) Speak() string { return fmt.Sprintf("robot-%d: beep", r.ID) }

func speakAll(ss []Speaker) string {
	var lines []string
	for _, s := range ss { lines = append(lines, s.Speak()) }
	return strings.Join(lines, "\n")
}

fmt.Println(speakAll([]Speaker{Dog{"rex"}, Robot{7}}))
```

```sh
$ go run main.go
rex: woof
robot-7: beep
```

`Dog` 和 `Robot` 没写过 `impl Speaker for Dog`，也互相不认识。Go 的规则是结构化满足：类型的方法集覆盖接口的方法集，就自动实现了这个接口。

这和 TypeScript 的结构化类型是同一个思路：对象形状对得上就行。Java 和 Kotlin 则要显式写 `implements` 或 `: Speaker`。

检查发生在编译期。给一个没有 `Speak` 的类型试试：

```sh
$ go run main.go
# command-line-arguments
./main.go:8:18: cannot use Rock{} (value of struct type Rock) as Speaker value in variable declaration: Rock does not implement Speaker (missing method Speak)
```

由此带来一个惯例：接口定义在使用方。`Searcher` 接口写在调用它的包里，被调用的包根本不知道自己被抽象了。标准库全是一两个方法的小接口。

代价是方法必须和类型定义在同一个包里。所以你不能给 `string` 加方法，也不能给别人的类型补方法。Kotlin 的扩展函数、JS 的 `prototype` 扩展，Go 都不给。

## 方法集

接口检查看的是方法集。`T` 的方法集只有值接收者方法，`*T` 的方法集两种都有。

```go
type Adder interface{ Add(int) }
type Counter struct{ n int }
func (c *Counter) Add(d int) { c.n += d }   // 指针接收者

c := Counter{}
var a Adder = c
```

```sh
$ go run main.go
# command-line-arguments
./main.go:13:16: cannot use c (variable of struct type Counter) as Adder value in variable declaration: Counter does not implement Adder (method Add has pointer receiver)
```

把 `c` 改成 `&c` 就能编译。这是新手编译错误的前几名，记住"接口要求什么接收者，就传什么形态"。Rust 的方法集没有值和指针之分，这里要换直觉。

## 嵌入

有了接口，下一个问题是复用。把一个类型嵌进另一个，外层就能直接调用内层的方法：

```go
type Base struct{ Greeting string }
func (b Base) Hello() string { return b.Greeting }

type Greeter struct {
	Base           // 嵌入
	Count int
}

g := Greeter{Base: Base{Greeting: "hi"}}
fmt.Println(g.Hello())
fmt.Println(g.Base.Hello())
```

```sh
$ go run main.go
hi
hi
```

两行完全等价。`g.Hello()` 是编译期转发糖，编译器把它改写成 `g.Base.Hello()`。

Kotlin 读者可以把它看成没有 `by` 关键字的委托。Java 读者要注意，这里没有 `extends`，没有 `super`，也没有 `@Override`。

这不是继承。想把 `g` 赋给 `Base` 类型的变量，编译器会拒绝：

```go
var b Base = g
```

```sh
# command-line-arguments
./main.go:18:15: cannot use g (variable of struct type Greeter) as Base value in variable declaration
```

方法提升只让外层"能调到"内层，不让外层"是"内层。也没有 override。让外层定义同名方法，看内层的方法会不会被替换：

```go
type Base struct{}
func (b Base) Name() string { return "Base" }
func (b Base) Hi() string   { return "我是 " + b.Name() }

type Child struct{ Base }
func (c Child) Name() string { return "Child" }

c := Child{}
fmt.Println(c.Name())
fmt.Println(c.Hi())
```

```sh
$ go run main.go
Child
我是 Base
```

`c.Name()` 用了外层的，但 `Base.Hi` 里的 `b.Name()` 仍然调用 `Base` 自己的。没有虚表，内层不知道外层存在。

结论是：复用用嵌入，多态用接口。别拿嵌入伪装继承。练习 `TestEmbedding` 断言的就是转发这一面。

## 错误

Go 没有 `Result` 和 `?`，也没有 `try/catch`。`error` 是一个普通接口，错误就是返回值，`if err != nil` 是习惯不是语法。

要解决的第一件事，是让错误带上下文，同时调用方还能认出它是哪一类。用 `%w` 包装：

```go
var ErrDivZero = errors.New("除数为零")

func div(a, b int) (int, error) {
	if b == 0 {
		return 0, fmt.Errorf("divide %d by %d: %w", a, b, ErrDivZero)
	}
	return a / b, nil
}

_, err := div(7, 0)
fmt.Println(err)
fmt.Println(errors.Is(err, ErrDivZero), err == ErrDivZero)
```

```sh
$ go run main.go
divide 7 by 0: 除数为零
true false
```

消息里有 7 和 0，给读日志的人看。`errors.Is` 顺着包装链找到 sentinel，直接 `==` 比较则找不到。

第二件事是取出具体类型拿字段，用 `errors.As`，或 Go 1.26 起的泛型版 `errors.AsType`：

```go
type NotFound struct{ Key string }
func (e *NotFound) Error() string { return "未找到 " + e.Key }

err = fmt.Errorf("查询失败: %w", &NotFound{Key: "user:42"})

var nf *NotFound
if errors.As(err, &nf) {
	fmt.Println("key =", nf.Key)
}
if nf, ok := errors.AsType[*NotFound](err); ok {
	fmt.Println("AsType key =", nf.Key)
}
```

```sh
$ go run main.go
查询失败: 未找到 user:42
key = user:42
AsType key = user:42
```

`%w` 对应 Java 的 `new Exception(msg, cause)` 和 JS 的 `new Error(msg, { cause })`，`errors.As` 对应 `catch` 里按类型匹配，也就是 `instanceof`。

分界线是：`Is` 按值比 sentinel，`As` 按类型取结构体。两者都沿着 `Unwrap()` 链穿透。练习 `TestErrorWrapping`、`TestErrorAs` 和 TestAsTypeVsAs 各练一种。

## 接口里的 nil

回到体系背景里的"两个字"。错误是接口，接口是 (类型, 数据) 二元组，这会带来 Go 最著名的坑：

```go
func bad(fail bool) error {
	var e *MyErr          // 具体类型的 nil 指针
	if fail {
		e = &MyErr{msg: "boom"}
	}
	return e
}

err := bad(false)
fmt.Println(err == nil, err)
fmt.Printf("%T\n", err)
```

```sh
$ go run main.go
false <nil>
*main.MyErr
```

打印出来是 `<nil>`，`== nil` 却是 `false`。`return e` 把 (类型=`*MyErr`, 数据=nil) 装进了接口，类型字不是 0，接口就不等于 nil。

修法是 nil 时显式 `return nil`，让两个字都是 0：

```go
func good(fail bool) error {
	if fail {
		return &MyErr{msg: "boom"}
	}
	return nil
}
```

练习 `TestInterfaceNil` 的参考实现就是这样。Rust 没有这个坑，`Option<Box<dyn T>>` 把"没有"写在类型里。Java 和 JS 的 `null` 只有一种，也不会遇到。

## 零值

Go 每个类型天生有零值，递归到每个字段。好的设计让零值直接可用：

```go
var mu sync.Mutex
mu.Lock()
mu.Unlock()
var buf bytes.Buffer
buf.WriteString("hello")
var sb strings.Builder
sb.WriteString("world")
fmt.Println(buf.String(), sb.String())

var s []int
s = append(s, 1)
fmt.Println(s, len(s))
```

```sh
$ go run main.go
hello world
[1] 1
```

Java 的字段也有默认零值，但局部变量没有，且对象引用默认是 `null`。Go 的零值覆盖所有类型，切片和 map 的零值是 nil。

nil 切片能直接 `append`。但 map 不行：

```go
var m map[string]int
fmt.Println(m["k"], len(m))
m["k"] = 1
```

```sh
0 0
panic: assignment to entry in nil map

goroutine 1 [running]:
main.main()
	.../main.go:26 +0x2d4
exit status 2
```

nil map 能读、不能写，写之前要 `make`。nil slice 可直接 `append`；nil channel 的收发会阻塞。三者行为不同，不应仅凭内部表示推断用法。

另外有些类型要求必须构造。`time.Timer` 的文档要求先用 `NewTimer` 或 `AfterFunc` 创建。判断口诀：文档写 "zero value ... ready to use" 就可以直接用，要求 New 或 initialize 的就别省。

## 类型断言与泛型

接口里装的具体类型，用 type switch 取出来：

```go
func describe(v any) string {
	switch x := v.(type) {
	case string:
		return fmt.Sprintf("str:%d", len(x))
	case Robot:
		return fmt.Sprintf("robot:%d", x.ID)
	default:
		return "unknown"
	}
}
```

```sh
$ go run main.go
str:5
robot:7
unknown
"" false
```

最后一行是单断言 `s, ok := any(42).(string)`，类型不对时 `ok` 为 `false`，不会 panic。

泛型函数把类型参数写在函数名后面，约束必须是接口：

```go
func Max[T cmp.Ordered](a, b T) T { if a > b { return a }; return b }
func MapAny[T, R any](xs []T, f func(T) R) []R {
	out := make([]R, len(xs))
	for i, x := range xs { out[i] = f(x) }
	return out
}

fmt.Println(Max(3, 8), Max("go", "rust"))
fmt.Println(MapAny([]int{1, 2, 3}, strconv.Itoa))
```

```sh
$ go run main.go
8 rust
[1 2 3]
```

Go 没有 `.map().filter()` 迭代器链，显式循环是主流。练习 `TestTypeSwitch` 和 `TestGenerics` 对应这两节。

## 小结

1. 隐式满足加方法同包：接口定义在使用方，小接口优先。
2. 方法集决定接口满足，接口要什么接收者就传什么形态。
3. 嵌入是编译期转发糖，不是继承。多态只有接口一条路。
4. 上下文用 `%w` 包装，sentinel 用 `errors.Is`，类型用 `errors.As`。
5. 接口是二元组，`return nil` 要显式。
6. 零值是否可用取决于类型：nil slice 可 append，nil map 写入前要 make；文档要求构造的类型按文档使用。

## 练习：p2lang

先跑一遍看全绿：

```sh
$ go test ./exercises/p2lang/...
ok  	golearn/exercises/p2lang	0.273s
```

练习地图：

- `TestInterface`：隐式满足加接口分派。
- `TestEmbedding`：方法提升不是多态。
- `TestInterfaceNil`：接口二元组的坑。
- `TestZeroValue`：零值 `strings.Builder`。
- `TestGenerics`：泛型函数语法。
- `TestTypeSwitch`：type switch 取具体类型。
- `TestErrorWrapping`、`TestErrorAs`：在 `exercises/p1tool/error_test.go`，练 `%w` 和 `Is`/`As`。

**预测**，先写答案再跑：

1. `speakAll([]Speaker{Dog{"rex"}, Robot{7}})` 输出什么？
2. `Greeter` 嵌入 `Base` 后，改掉 `Base.Hello` 的实现，`g.Hello()` 的结果变吗？`g` 能赋给 `Base` 变量吗？
3. `maybeFail(false)` 返回的 err 是 nil 吗？为什么？

**观察**：打开 `lang_test.go` 的 `maybeFail`，删掉 `if e == nil { return nil }` 三行，再跑 `go test -run TestInterfaceNil ./exercises/p2lang/`：

```sh
--- FAIL: TestInterfaceNil (0.00s)
    lang_test.go:127: after fix, want nil, got <nil>
FAIL
```

看完用 `git checkout` 还原。

**改造**：给 `exercises/p2lang/` 加一个只有 `Speak() string` 的 `Cat` 类型，让 `speakAll` 接受它。验收是 `go vet ./exercises/p2lang/ && go test ./exercises/p2lang/` 全绿，并且你的测试断言了 `Cat` 在输出里。

## 章末参考

### Rust 泛型对照

| Rust | Go | 备注 |
|---|---|---|
| `fn max<T: Ord>(a, b)` | `func Max[T cmp.Ordered](a, b T) T` | 约束必须是 interface，`cmp.Ordered` 和 `comparable` 预置 |
| `impl<T> Trait for T` | 不存在 | 无 impl 泛型，无特化 |
| 关联类型 / GAT | 无 | 接口方法不能带自己的类型参数 |
| 单态化 | GC shape stenciling 加字典 | 泛型不装箱，接口装 |

老代码里 `map[string]interface{}` 是泛型前的写法，新代码用 `any` 加泛型。

### 方法集精确规则

- 接口方法要求指针接收者时，`T` 不满足，`*T` 满足。
- 值接收者方法，`T` 和 `*T` 都满足。
- 可寻址的值调用 `x.M()` 会自动取址。map 元素和接口内的具体值不可寻址，只能调值接收者方法。这是 `map[string]T` 方法调用编译报错的根源。

`var _ json.Marshaler = (*RawMessage)(nil)` 是编译期验证满足关系的惯用法（Effective Go 的 Interface checks）。Rust 的 `impl` 块本身就是登记，不需要它。

### 嵌入的细节

- 字段也会提升：`g.Greeting` 合法。
- 外层自己定义了同名成员就遮蔽内层。同深度两个嵌入有同名方法，选择子非法，编译报错。
- 指针嵌入 `*Base` 也提升方法；nil 接收者是否 panic 取决于调用的方法及其对接收者的使用。
- 接口可以嵌接口，`io.ReadWriter` 就是 `Reader` 加 `Writer`。Go 没有 default impl，靠接口组合拼装。

### 错误处理补充

- `errors.Join(errs...)`（1.20 起）包一个错误列表，格式是换行拼接，`Is` 和 `As` 会穿透。`fmt.Errorf` 也允许多个 `%w`，`Unwrap() []error` 形成多叉树。
- Go 1.26 起 `errors.AsType[E]` 直接返回 `(E, bool)`。`go doc errors.As` 的建议是 "For most uses, prefer AsType"，`As` 保留给 target 不实现 error 的少数场景。并排的对照例子在 `exercises/p2lang/astype.go`（TestAsTypeVsAs）。
- 判断 `context.DeadlineExceeded` 这类 sentinel 也走 `errors.Is`。
- panic 只用于程序员 bug，不可恢复的错误不走 panic。
- 自定义错误类型建议实现 `Unwrap() error`，否则 `Is` 和 `As` 断链。

### 零值与构造器惯例

- `NewXxx()` 返回 `*T` 是"零值不够用"的信号。文档样例都用 `NewXxx` 时，别用 `var x T`。
- 零值可用是传递的：`SyncedBuffer{lock, buffer}` 的两个字段零值都可用，整体就可用（Effective Go 原例）。
- `new(T)` 等价于 `&T{}`，字面量能带字段值。`new` 在 1.26 起也能带初始化值。
- P15 的 `slog.NewJSONHandler` 必须传 writer，opts 传 nil 则落回默认级别。

### Java/Kotlin 与 TS/JS 类比

| 概念 | Java / Kotlin | TS / JS |
|---|---|---|
| 隐式满足接口 | 需 `implements`，Kotlin 需 `: Iface` | 结构化类型，最接近 |
| 嵌入 | Kotlin `by` 委托，Java 无 | 组合或 mixin |
| 方法提升 | 无，Java 用 `extends` | 无 |
| `error` 返回值 | checked exception，Kotlin `Result` | `try/catch`，`Error` 的 `cause` |
| `errors.As` | `catch` 加 `instanceof` | `instanceof Error` 子类 |
| 零值 | 字段默认值，局部变量无 | `undefined` |
| type switch | `switch` 模式匹配（Java 21） | `typeof` 与 `instanceof` |
