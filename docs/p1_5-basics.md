---
title: P1.5 · 语法基础速通：12 个小节走完 Go 的日常语法
---

# P1.5 · 语法基础速通：12 个小节走完 Go 的日常语法


> 这章学完你能干什么：不看文档写出 Go 日常代码，包括变量、常量、`for` 全形态、切片、map、字符串、`switch`、`defer`、方法和闭包。
> 读者：会 Rust，没系统看过 Go 语法。前置：P1。后续：P2 换语言直觉，P2.5 惯用法，P3 并发。
>
> 动手方式：开一个空目录，`go mod init demo`，每个例子存成 `main.go`，用 `go run main.go` 运行。片段省略了 `package main` 和 `import`。

## 体系背景：变量住在哪里

变量可能位于栈上，也可能位于由垃圾回收器管理的堆上；具体位置是实现细节，不是类型的属性。Go 编译器做逃逸分析，判断存储是否需要超过当前栈帧的生命周期。比如返回局部变量的地址可能使它逃逸，但内联等优化也会影响最终结果。

```go
func onStack() int {
	p := point{1, 2}
	return p.x + p.y
}

func onHeap() *point {
	p := point{1, 2}
	return &p        // 地址传出去了
}
```

```sh
$ go build -gcflags=-m -o /dev/null main.go
./main.go:16:2: moved to heap: p
```

编译器诊断取决于 Go 版本和优化；上面的输出对应一次构建，不能据此断言所有调用场景的分配位置。`new` 和 `&T{}` 都不保证分配在堆上。

理解拷贝语义时，比具体字节数更重要的是：切片值包含对底层数组的引用以及长度、容量；字符串值包含字节数据和长度；map 值引用运行时管理的数据。赋值拷贝这些值，不会深拷贝其引用的数据。具体内存布局不是语言规范保证的 API。

## 变量与多返回值

声明类型写在名字后面。`:=` 是"声明并推断"的短写法，只能用在函数内：

```go
var a int = 1
var b = 2
c := 3
var x, y = 1, "hi"
fmt.Println(a, b, c, x, y)
```

```sh
$ go run main.go
1 2 3 1 hi
```

Kotlin 的 `val`/`var` 和 TS 的 `const`/`let` 都不强制这一点。Go 把"声明了不用"升格成编译错误。

声明了不用是编译错误，不是 Rust 那样的 warning：

```sh
$ go run main.go
# command-line-arguments
./main.go:4:2: declared and not used: x
```

函数可以返回多个值，`error` 习惯放最后一个：

```go
func div(a, b int) (int, error) {
	if b == 0 {
		return 0, errors.New("div by zero")
	}
	return a / b, nil
}

q, err := div(7, 2)
fmt.Println(q, err)
_, err = div(7, 0)
fmt.Println(err)
```

```sh
3 <nil>
div by zero
```

不想要的返回值用 `_` 丢掉。返回值也可以先命名，最后写一个光秃秃的 `return`：

```go
func split(sum int) (x, y int) {
	x = sum * 4 / 9
	y = sum - x
	return
}
fmt.Println(split(17))
```

```sh
7 10
```

短函数可以这么写，长函数别用，读起来像谜语。

## 常量与 iota

常量在编译期确定，只能是数字、字符串和布尔。要写一组相关的枚举值，用常量块加 `iota`，它是块内的行号，每行加 1：

```go
type Level int

const (
	Debug Level = iota
	Info
	Warn
	Error
)

const (
	_  = iota
	KB = 1 << (10 * iota)
	MB
	GB
)

fmt.Println(Debug, Info, Warn, Error)
fmt.Println(KB, MB, GB)
fmt.Println(Level(99))
```

```sh
$ go run main.go
0 1 2 3
1024 1048576 1073741824
99
```

Java 的 `enum` 和 TS 的 `enum` 都是真正的类型，Go 没有。后几行省略的表达式会隐式重复上一行。最后一行说明了 Go 的"枚举"只是 `int` 加常量，`Level(99)` 照样编译通过，没有 Rust `enum` 的类型安全。练习 `TestIota` 练的就是这个。

## for 与 range

Go 只有 `for` 一种循环，写法有四种，对应 Rust 的 `loop`、`while` 和 `for`：

```go
for i := 0; i < 3; i++ { }     // C 形态
for x < 100 { }                // while
for { }                        // loop，配 break
for i, v := range xs { }       // for (i, v) in xs.iter().enumerate()
```

`range` 遍历切片、map、字符串、channel、整数和函数迭代器。整数形态是 1.22 加的：

```go
for i := range 3 {
	fmt.Print(i, " ")
}
for i, r := range "Go语" {
	fmt.Println(i, string(r))
}
```

```sh
$ go run main.go
0 1 2
0 G
1 o
2 语
```

遍历字符串时，`i` 是字节下标，`r` 是字符。"语"从字节 2 开始，所以下标是 2，下一个字符就是 5 了，下面"字符串"一节会解释。

循环中用 `:=` 声明的变量，从 Go 1.22 语言版本起每轮都是新变量。已有变量用 `=` 赋值的循环仍然共享变量；模块的 `go` 指令决定适用的语言版本。下面的闭包捕获每轮自己的 `i`：

```go
var fs []func()
for i := range 3 {
	fs = append(fs, func() { fmt.Print(i, " ") })
}
for _, f := range fs { f() }
```

```sh
0 1 2
```

对照旧语义时，可将模块的 `go` 指令设为 `1.21` 并改用 `for i := 0; i < 3; i++`；旧语义下闭包共享同一个 `i`，循环结束后输出 `3 3 3`。整数 range 本身需要 Go 1.22。

## 切片

数组 `[3]int` 的长度是类型的一部分，赋值是整体拷贝，日常很少用。你用的是切片，它是 (指针, len, cap) 三元组，指向一块底层数组。

先看 `append` 怎么扩容：

```go
s := make([]int, 0, 2)
for i := range 5 {
	s = append(s, i)
	fmt.Println("len", len(s), "cap", cap(s))
}
```

```sh
$ go run main.go
len 1 cap 2
len 2 cap 2
len 3 cap 4
len 4 cap 4
len 5 cap 8
```

容量不足时 `append` 分配更大的数组并拷贝元素；增长比例是运行时实现细节，不保证翻倍。

Java 的 `List.subList` 同样是视图，JS 的 `Array.slice` 则会拷贝，`Uint8Array.subarray` 才是视图。

子切片 `a[1:3]` 不拷贝，和原切片共享同一块数组。Rust 的借用检查会拦住这种别名，Go 不会：

```go
a := []int{1, 2, 3, 4}
b := a[1:3]
fmt.Println("b:", b, "len", len(b), "cap", cap(b))
b[0] = 99
fmt.Println("改 b[0] 后 a =", a)
b = append(b, 5)
fmt.Println("append 后 a =", a)
```

```sh
b: [2 3] len 2 cap 3
改 b[0] 后 a = [1 99 3 4]
append 后 a = [1 99 3 5]
```

改 `b[0]` 改到了 `a`。`b` 的容量还有富余，`append` 直接写进了 `a` 的第 4 个元素。

想隔离有两个办法：三索引切片限制容量，或者克隆：

```go
a = []int{1, 2, 3, 4}
c := a[1:3:3]            // cap 限到 2
c = append(c, 5)
fmt.Println("三索引 append 后 a =", a, "c =", c)
d := slices.Clone(a)
d[0] = -1
fmt.Println(a[0], d[0])
```

```sh
三索引 append 后 a = [1 2 3 4] c = [2 3 5]
1 -1
```

最后，nil 切片是合法的零值，`len`、`append`、`range` 都能直接用。练习 `TestSliceShare` 把这些都考了一遍。

## map

```go
m := map[string]int{"a": 1}
fmt.Println(m["missing"])
v, ok := m["missing"]
fmt.Println(v, ok)
v, ok = m["a"]
fmt.Println(v, ok)
```

```sh
$ go run main.go
0
0 false
1 true
```

Java 的 `HashMap.get` 缺键返回 `null`，JS 的 `Map.get` 返回 `undefined`，Go 返回零值，没法靠 `null` 判断。

这是第一个坑：取缺失的键返回零值，不报错。需要区分"没有"和"值就是零"时，必须用 `v, ok :=`。

第二个坑是 nil map 能读不能写，写入直接 panic（见 P2 的"零值"一节）。

第三个坑是迭代顺序不保证，并发读写或写写需要同步，否则就是数据竞争，运行时也可能终止进程：

```go
big := map[string]int{"a": 1, "b": 2, "c": 3, "d": 4, "e": 5}
for run := 0; run < 3; run++ {
	for k := range big { fmt.Print(k) }
	fmt.Println()
}
```

```sh
cdeab
eabcd
cdeab
```

以上是一次可能的输出；迭代顺序未指定，不保证每轮都不同。并发写可能出现以下错误：

```sh
$ go run main.go
fatal error: concurrent map writes

goroutine 18 [running]:
```

这是运行时检测到的 `fatal error`，不能用 `recover` 接住。`-race` 可报告实际执行路径上的数据竞争，但不保证阻止这个致命错误，也不能证明不存在未执行到的竞争。解法是同步访问，见 P3。

键必须是可比较的类型：切片、map、函数不能当键，字段都可比较的结构体可以。

## 字符串与 rune

字符串是不可变的字节序列，可以包含任意字节；字符串字面量通常使用 UTF-8。关键认知是 `len` 和索引按字节算，不按字符：

```go
s := "Go语言"
fmt.Println(len(s))
fmt.Println(s[2])
fmt.Println(utf8.RuneCountInString(s), len([]rune(s)))
for i, r := range s {
	fmt.Print(i, ":", string(r), " ")
}
```

```sh
$ go run main.go
8
232
4 4
0:G 1:o 2:语 5:言
```

Java 和 JS 的字符串是 UTF-16，`"Go语言".length` 是 4，遇到 emoji 才出现这类问题。Go 是 UTF-8，坑在汉字上就出现了。

`Go` 占 2 字节，"语言"各占 3 字节，共 8。`s[2]` 是"语"的第一个字节 232，不是字符。

处理第 n 个 Unicode 码点可先转 `[]rune`；一个用户感知的字符仍可能由多个码点组成。纯 ASCII 时字节与码点位置相同。

改字符串只能重建，转 `[]byte` 修改再转回来：

```go
b := []byte("abc")
b[0] = 'X'
fmt.Println(string(b))
fmt.Println(strings.Join([]string{"a", "b"}, ", "), strings.Split("x,y,z", ","))
```

```sh
Xbc
a, b [x y z]
```

数字和字符串互转用 `strconv.Itoa` 和 `strconv.Atoi`，热路径上比 `fmt` 快。练习 `TestRuneIndex` 考这一节。

## switch

`switch` 默认每个分支自动 `break`，不需要写。最常用的是不带表达式的形态，相当于整洁的 `if-else` 链：

```go
func grade(score int) string {
	switch {
	case score >= 90:
		return "A"
	case score >= 60:
		return "B"
	default:
		return "F"
	}
}
fmt.Println(grade(95), grade(70), grade(10))
```

```sh
$ go run main.go
A B F
```

一个 `case` 可以列多个值，type switch 则按接口里的实际类型分支：

```go
switch d {
case "sat", "sun":
	fmt.Println(d, "weekend")
default:
	fmt.Println(d, "weekday")
}
var v any = 3.5
switch x := v.(type) {
case string:
	fmt.Println("string", x)
case float64:
	fmt.Println("float64", x)
}
```

```sh
sat weekend
mon weekday
float64 3.5
```

`fallthrough` 会强行进入下一个分支，几乎不用，见到先怀疑是 bug。练习 `TestGrade` 对应无表达式 `switch`。

## defer

它相当于 Java 的 `try/finally` 和 `try-with-resources`、Kotlin 的 `use`、JS 的 `finally`，但写在资源获取的下一行。

`defer` 把一个调用推迟到当前函数返回前执行。粒度是函数，不像 Rust 的 `Drop` 是作用域。它有三条精确语义。

**（1）后进先出。**

```go
for i := 1; i <= 3; i++ {
	defer fmt.Println("defer", i)
}
```

```sh
$ go run main.go
defer 3
defer 2
defer 1
```

**（2）参数立即求值。** 要看最新值就用闭包：

```go
i := 0
defer fmt.Println("参数立即求值:", i)
defer func() { fmt.Println("闭包看到最新值:", i) }()
i = 7
```

```sh
闭包看到最新值: 7
参数立即求值: 0
```

**（3）能改命名返回值。** `defer` 在 `return` 之后运行，摸得到它们：

```go
func safe() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("recovered: %v", r)
		}
	}()
	panic("boom")
}
fmt.Println(safe())
```

```sh
recovered: boom
```

`defer` 要紧跟资源获取：`defer f.Close()`、`defer mu.Unlock()`、`defer wg.Done()`。练习 `TestDeferOrder` 考前两条。

## panic 与 recover

`panic` 对应 Rust 的 `panic!`，用于程序员的 bug。它不是错误处理机制，错误是返回值。

```go
func mustParse(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		panic(fmt.Sprintf("config parse %q: %v", s, err))
	}
	return n
}
fmt.Println(mustParse("42"))
fmt.Println(mustParse("4x2"))
```

```sh
$ go run main.go
42
panic: config parse "4x2": strconv.Atoi: parsing "4x2": invalid syntax
...
exit status 2
```

`recover` 只在 deferred 函数里有效（上一节的 `safe` 就是例子）。库代码不应该用它吞 panic，那样会把 bug 藏起来。

服务边界不同：`net/http` 会替每个请求 `recover`，进程不死，但那条连接会被关闭。goroutine 的 panic 没有天然边界，P3 会让你亲眼看进程死掉。

## 方法

方法是带接收者的函数，接收者就是第一个参数：

```go
type Counter struct{ n int }

func (c Counter) Value() int { return c.n }     // 值接收者，拿到拷贝
func (c *Counter) Add(d int) { c.n += d }       // 指针接收者，能改原值

var c Counter
c.Add(2)
c.Add(3)
fmt.Println(c.Value())
```

```sh
$ go run main.go
5
```

选型规则全生态一致：要改接收者，或者结构体大、含锁，用指针接收者。一个类型别混用，要么全值要么全指针。

接收者种类还决定接口满足。方法集的精确规则在 P2，这里只看编译器的反应：

```go
var _ Adder = c      // c 是 Counter
_ = Counter.Add
```

```sh
$ go run main.go
# command-line-arguments
./main.go:11:16: cannot use c (variable of struct type Counter) as Adder value in variable declaration: Counter does not implement Adder (method Add has pointer receiver)
./main.go:12:14: invalid method expression Counter.Add (needs pointer receiver (*Counter).Add)
```

改成 `&c` 和 `(*Counter).Add` 就通过。练习 `TestPointerReceiver` 让你亲手改一遍。

## 闭包与方法值

函数是一等公民，可以存进变量，也可以从函数里返回：

```go
func counter() func() int {
	n := 0
	return func() int { n++; return n }
}

next := counter()
fmt.Println(next(), next(), next())
other := counter()
fmt.Println(other())
```

```sh
$ go run main.go
1 2 3
1
```

闭包捕获的是变量本身，不是拷贝。每次调 `counter()` 得到各自独立的 `n`。Go 没有 `Fn`/`FnMut` 的区分，可变捕获不留任何痕迹。

方法也能当值用。`c.Add` 绑定了接收者，`(*Counter).Add` 没绑定，接收者变成第一个参数：

```go
f := c.Add
f(10)
g := (*Counter).Add
g(&c, 100)
fmt.Println(c.n)
```

```sh
115
```

## 值语义与引用语义

Java 和 JS 的对象变量本身就是引用，传参拷贝的是引用。Go 的结构体是值，要引用必须自己写 `*`。

Go 的传参和赋值一律是拷贝。有些类型内部含指针，拷贝的是"头"，这就是体系背景里那组数字的含义：

```go
func bump(p P)               { p.X++ }
func bumpPtr(p *P)           { p.X++ }
func setFirst(s []int)       { s[0] = 99 }
func grow(s []int)           { s = append(s, 4) }
func growRet(s []int) []int  { return append(s, 4) }

p := P{1}
bump(p);    fmt.Println(p.X)
bumpPtr(&p); fmt.Println(p.X)
s := []int{1, 2, 3}
setFirst(s); fmt.Println(s)
grow(s);     fmt.Println(s, len(s))
s = growRet(s); fmt.Println(s, len(s))
```

```sh
$ go run main.go
1
2
[99 2 3]
[99 2 3] 3
[99 2 3 4] 4
```

结构体按值拷贝，改不到原值。切片改元素对调用方可见，但 `grow` 里的 `append` 只改了函数内的头，调用方看不到长度变化。所以要增长就返回新切片。

数组整体拷贝；map 和 channel 值也按值传递，但拷贝后仍引用同一底层数据。

```go
arr := [3]int{1, 2, 3}
arr2 := arr
arr2[0] = 99
fmt.Println(arr, arr2)
```

```sh
[1 2 3] [99 2 3]
```

Rust 最大的观念差异在这里。Go 没有 `&` 和 `mut` 标记，谁在共享可变状态，从签名上看不出来。靠约定加 `-race` 兜底，把"这里是不是共享了"当作每次赋值的自检项。练习 `TestValueSemantics` 考这一节。

## 小结

1. `:=` 声明推断，声明不用是编译错，`error` 放最后一位。
2. 切片是 (ptr, len, cap) 头，子切片共享底层数组。隔离用三索引切片或 `Clone`。
3. map 取缺键得零值，nil map 写入 panic，迭代无序，并发读写需要同步。
4. `len` 和索引按字节算，处理字符要转 `[]rune`。
5. `defer` 三语义：后进先出、参数立即求值、能改命名返回值。
6. 接收者选型：要改、结构体大、含锁用指针。一致性优先。
7. 一切赋值皆拷贝。共享可变在签名上看不出来，这是没有 borrow checker 的代价。

## 练习：p1_5basics

先跑一遍看全绿：

```sh
$ go test ./exercises/p1_5basics/
ok  	golearn/exercises/p1_5basics	0.472s
```

**预测**，先写答案再跑：

1. `TestSliceShare`：`b := a[1:3]; b[0] = 99` 后 `a[1]` 是多少？`append(b, 5)` 后 `a[3]` 呢？改成 `a[1:3:3]` 再 append，`a` 会变吗？
2. `TestMapZero`：`m["missing"]` 返回什么？怎么区分"键不存在"和"值就是零"？向 nil map 写会怎样？
3. `TestDeferOrder`：三个 defer 的输出顺序？`defer fmt.Println(i)` 在 `i = 7` 之后执行，打印几？闭包版本呢？
4. `TestRuneIndex`：`"Go语言"[2]` 的类型和值？`len()` 和 `utf8.RuneCountInString()` 各返回几？

**观察**：写个 10 行小程序，建 5 键 map，连跑几次 `for k := range m` 拼出键序，验证顺序真的会变。`go test -run TestIota -v` 看 iota 的断言，错一个值就 FAIL 并打印实际值。

**对照**：`TestIota`、`TestGrade`、`TestValueSemantics` 的断言即答案，读完对应小节再看。

**改造**：对照 `TestPointerReceiver`，解释为什么 `c.Add` 能自动取址调用，而 `var _ Adder = c` 不满足接口；再分别尝试 `&c` 和值接收者，观察方法集如何变化。

## 章末参考

### range 的八种目标

语言规范里 `range` 共 8 类（Go 1.26 仍是这 8 类），返回值个数不同：

- 切片或数组：`i, v`，`v` 是拷贝。
- 只要下标：`for i := range s`。
- map：`k, v`，无序。
- 字符串：`i, r`，`i` 是字节下标，`r` 是 rune。
- channel：一个值，迭代到 close。
- 整数（1.22）：`for i := range n`，不用下标写 `for range n`。
- 函数一值（1.23）：`iter.Seq[V]`。
- 函数两值（1.23）：`iter.Seq2[K, V]`。

函数迭代器是"推式"：迭代器调用循环体，循环体返回 `false` 即 `break`。Rust 的 `Iterator` 是拉式，所以 Go 里没有 `.map().filter()` 链，组合靠 `slices` 和 `maps` 包的函数（P8 迭代器节）。

### 声明与初始化

- `x := v` 用在函数内，不能包级。
- `var x T` 用在包级或要零值。
- `new(T)` 和 `&T{}` 都得到指向零值 `T` 的指针；`&T{}` 适用于复合类型。Go 1.26 起 `new` 也可带值。
- `make(T, args)` 只用于 slice、map、chan。

### 运算符与类型

- 含 slice 或 map 字段的结构体不可 `==`，用 `slices.Equal` 或 `maps.Equal`。
- `i++` 是语句，不是表达式，也没有 `++i`。
- `int` 平台相关，需要确定位宽就用 `int64` 或 `uint32`。
- 没有隐式转换，`int32` 赋给 `int64` 要写 `int64(x)`。
- 浮点比较用 `math.Abs(a-b) < eps`，金额用整数分或 `big.Rat`。
- 常量可以有类型，也可以无类型；`const Big = 1 << 62` 是无类型常量，可赋给 `int64`。

### 内建 min、max、clear

Go 1.21 起可用，都是泛型：

```go
fmt.Println(min(3, 1, 2), max(2.5, 1), min("b", "a"))
m := map[string]int{"a": 1, "b": 2}
xs := []int{1, 2, 3}
clear(m)
clear(xs)
fmt.Println(len(m), xs)
```

```sh
$ go run main.go
1 2.5 a
0 [0 0 0]
```

`clear` 对 map 是全删，对切片是元素归零、`len` 不变。`min(xs...)` 不行，内建不收变参，要折叠着写。可运行例子在 `exercises/p1_5basics/builtin121.go`，配 TestMinMaxBuiltins 和 TestClearBuiltins。

### 标签

`break` 和 `continue` 可以带标签跳出多层循环，对应 Rust 的 `'outer: loop`：

```go
outer:
	for i := range 3 {
		for j := range 3 {
			if i*j == 2 {
				fmt.Println("命中", i, j)
				break outer
			}
		}
	}
```

```sh
命中 1 2
```

### 与 Rust 的关键差异

| 主题 | Rust | Go |
|---|---|---|
| 声明 | `let x: T`，默认不可变 | `x := v`，默认可变 |
| 所有权 | move 与 borrow | 一切拷贝，大结构体传指针 |
| 枚举 | sum type 带数据 | int 常量加 iota，无类型安全 |
| 错误 | `Result<T, E>` 与 `?` | `(T, error)` 与 `if err != nil` |
| Option | `Option<T>` | 零值加 comma-ok |
| 泛型 | 全功能 | 接口约束（P8） |
| 字符串 | `String` 可变，`&str` 只读 | 统一只读，改靠重建 |
| 迭代器 | 惰性链为主 | 显式 `for` 为主，`iter` 包是补充 |

### Java/Kotlin 与 TS/JS 类比

| 概念 | Java / Kotlin | TS / JS |
|---|---|---|
| `:=` | Kotlin `val`，Java `var` | `const` / `let` |
| `iota` 枚举 | `enum` 类 | `enum` |
| 切片子视图 | `List.subList` | `subarray`（`slice` 会拷贝） |
| map 缺键 | `null` | `undefined` |
| 字符串 | UTF-16 | UTF-16 |
| `defer` | `finally`，`use` | `finally` |
| 闭包循环变量 | 须 effectively final | `let` 每轮新建，`var` 共享 |
| 结构体传参 | 对象是引用 | 对象是引用 |
