# 09 泛型：该用的地方，和绕不过的限制

> 详细程度：标准 · 约 80 分钟
> 材料：TBX04、TBX05、TBX01 · 旧长版：`notes/p33-generics-boundary.md`、`notes/p8-fast-track.md`
> 前置：你写过 Java/Rust/TS 中任意一门的泛型或模板代码

## 先看一段不想再写的代码

下面三个函数做的是同一件事，唯一的差别是参数类型。你八成在同事或自己写过的代码里见过这种"复制粘贴换类型"：

```go
package main

import "fmt"

func AddInt(a, b int) int             { return a + b }
func AddInt64(a, b int64) int64       { return a + b }
func AddFloat64(a, b float64) float64 { return a + b }

func main() {
	fmt.Println(AddInt(1, 2))
	fmt.Println(AddInt64(3, 4))
	fmt.Println(AddFloat64(1.5, 2.5))
}
```

```text
3
7
4
```

三行输出来自三个函数体完全相同的函数。这里只有 `+` 一个操作，复制起来还算便宜；换成排序、查找、树、图，或者链表、栈、队列这些容器，每个类型都要重写一遍，维护成本就上来了。这种重复不是写代码的人懒，是老 Go 一直没有表达"对任意数字类型求和"的工具：给 `Add` 换个参数类型，就得复制一份。Go 直到 1.18 才补上这块，这一节讲它：怎么用，以及三条绕不过的限制。〔TBX05 p2-4〕

Go 迟迟不加泛型，不是没想过。2009 年 Russ Cox 就指出三条实现路线，每条各有一个"慢"，当时只能三选一：〔TBX05 p5〕

| 路线 | 代价 |
|---|---|
| 不实现泛型 | 拖慢程序员，到处复制 `AddInt`、`AddInt64` |
| 每个类型实例化一份（C++ 模板） | 拖慢编译器，产生大量冗余代码 |
| 隐式装箱拆箱（Java 擦除） | 拖慢执行 |

最后落地的是十多年打磨的折中，2020 年 6 月 Wadler 等人的《Featherweight Go》给出形式化证明，才有了共识。〔TBX05 p6-8〕看懂这张表，就明白 Go 泛型为什么克制：它必须同时守住"简单"和"向后兼容 Go1"，所以语法点到为止。〔TBX05 p4-5〕

## 和你已知语言的差异

| 问题 | Java | Rust | TS | Go |
|---|---|---|---|---|
| 类型参数写法 | `<T extends X>` | `T: Trait` | `<T extends X>` | `[T 约束]` |
| 约束是 | 接口/父类/边界 | trait | 结构化约束 | 接口，可含类型集合 |
| 生成代码 | 擦除，运行期一个类 | 单态化，每类型一份 | 编译期查，运行期擦除 | 编译期具化，但不是每类型一份（按内存形状分组，带字典） |
| 方法自带类型参数 | 可以 | 可以 | 可以 | 不可以 |

最陌生的两格在最后一行：Java/Rust/TS 的方法都能声明自己的类型参数（`<U> U pick(...)`），Go 的方法不能。这不是忘了支持，而是语言规范明确写死的限制，第三节专门讲它和绕法。

## 一、从 Add 到 Sum：类型参数、约束、具化

把开头的三个 `Add` 合成一个，就是泛型的三个新概念。先看成品：

```go
package main

import "fmt"

type Number interface {
	~int | ~int64 | ~float64
}

type MyInt int

func Sum[T Number](xs ...T) T {
	var s T
	for _, x := range xs {
		s += x
	}
	return s
}

func main() {
	fmt.Println(Sum(1, 2, 3))
	fmt.Println(Sum(1.5, 2.5))
	fmt.Println(Sum(MyInt(1), MyInt(2)))
}
```

```text
6
4
3
```

对照 Java 的 `<T extends Number>`、Rust 的 `T: Number`，Go 把类型参数写在函数名后的方括号里：`func Sum[T Number]`。拆开三个概念：〔TBX05 p9-11〕

1. **类型参数** `T`：方括号里的类型占位符，声明时不知道它具体是什么，调用时才确定。跟普通参数一样，只是它占的是"类型"这个位置。
2. **约束** `Number`：规定 `T` 可以是哪些类型。约束就是接口，Go 把它扩展成还能写**类型集合**：`~int | ~int64 | ~float64` 意思是"底层类型是 int、int64、float64 之一的类型"。
3. **实例化**：调用时用具体类型实参替换类型参数并检查约束。`Sum(1, 2, 3)` 的类型实参可推断为 `int`，也可显式写成 `Sum[int](1, 2, 3)`；是否为每个类型生成独立机器码是编译器实现细节。

类型实参大多能由编译器从实参推断出来，所以通常不用写 `Sum[int](...)`，写 `Sum(1, 2, 3)` 就行。`MyInt` 能传进去，靠的是约束里的 `~`：`MyInt` 底层类型是 `int`，`~int` 把它框了进来；去掉波浪线，`MyInt` 就进不去。〔TBX05 p11-12〕下一节细看这一点。

### 具化与调用：一台"排序机器"的生产过程

带类型参数的函数怎么从声明变成一次真正的调用？原课用 `Sort` 函数走了一遍完整流程：〔TBX05 p12-13〕

```go
func Sort[Elem interface{ Less(y Elem) bool }](list []Elem) {
}

type book struct{}

func (x book) Less(y book) bool {
	return true
}

func main() {
	var bookshelf []book
	Sort[book](bookshelf) // 泛型函数调用
}
```

原课的结论：`Sort[book](bookshelf)` 这样的泛型函数调用分两个阶段，先具化（instantiation），后调用（invocation）。〔TBX05 p12-13〕具化好比一家生产"排序机器"的工厂，按要排序对象的类型把机器生产出来，分三步：〔TBX05 p13〕

1. **工厂接单**：`Sort[book]`，发现要排序的对象类型是 `book`。
2. **模具检查与匹配**：检查 `book` 是否满足模具的约束要求，也就是有没有实现约束定义中的 `Less` 方法。满足，就把它作为类型实参替换 `Sort` 函数中的类型形参；不满足，编译器就会报错。
3. **生产机器**：把泛型函数 `Sort` 具化为一个新函数，起名 `booksort`，函数原型是 `func([]book)`。本质上就是 `booksort := Sort[book]`。

注意失败发生的时机：`book` 没实现 `Less` 的话，卡在第二步"模具检查与匹配"，编译器直接报错，可执行程序根本不会产生。机器生产出来后进入第二阶段调用，这和普通函数调用没有区别：只需检查传入的实参 `bookshelf` 的类型，与 `booksort` 函数原型中的形参类型 `[]book` 是否匹配。〔TBX05 p13〕用伪代码概括两个阶段：〔TBX05 p13〕

```text
Sort[book](bookshelf)

<=>

具化：booksort := Sort[book]
调用：booksort(bookshelf)
```

如果每次调用都要显式写 `Sort[book](bookshelf)`，和普通函数相比就繁琐了不少。好在 Go 编译器会根据传入的实参变量自动推导类型实参（Argument type inference），所以上面这个例子直接写 `Sort(bookshelf)` 就可以；有了自动推导，大多数泛型函数的调用方式与常规函数调用一致，不给 Gopher 带去额外的代码编写负担。〔TBX05 p13-14〕

类型本身也能带参数，叫泛型类型。`type Set[T comparable] map[T]struct{}` 就是一个用 map 实现的集合，`comparable` 是 Go 内置的约束，表示"可以做 map 键、能比较相等的类型"。方法里能直接引用 `T`，但方法自己不能再加类型参数，这是第三节的限制一。〔TBX05 p14-15〕

```go
package main

import "fmt"

type Vector[T any] []T

func (v Vector[T]) Dump() {
	fmt.Printf("%#v\n", v)
}

func main() {
	iv := Vector[int]{1, 2, 3, 4}
	sv := Vector[string]{"a", "b", "c", "d"}
	iv.Dump()
	sv.Dump()
}
```

```text
main.Vector[int]{1, 2, 3, 4}
main.Vector[string]{"a", "b", "c", "d"}
```

`Vector[int]` 和 `Vector[string]` 是两个不同的实例化类型，底层分别是 `[]int` 和 `[]string`，都可以调用 `Dump`。使用泛型类型同样遵循先具化、再使用的顺序：先显式用类型实参对泛型类型具化，得到 `Vector[int]` 和 `Vector[string]`，再对具化后的类型进行操作。〔TBX05 p14-15〕编译器如何共享或生成机器码属于实现细节，不应作为语言保证。原课排序示例使用的 `golang.org/x/exp/slices` 已在 1.21 转正为标准库 `slices`，现行 `Sort[S ~[]E, E cmp.Ordered]` 就是泛型函数，具化与类型推导机制本身没有变化。〔实测：/Users/akka/Toolchain/go/current/src/slices/sort.go:15-19〕

## 二、`~` 和类型集合：约束能框住谁

上一节那句"底层类型是它"值得展开，它是 Go 约束里最容易踩空的点。一个约束的本质是它定义了一个**类型集合**，只有落在这个集合里的类型才算满足约束。〔TBX05 p11-12〕

`int` 在约束里写的是"类型恰好是 `int`"；`~int` 写的是"底层类型是 `int` 的一切类型"，包括 `int` 自己，也包括 `type MyInt int` 这类自定义类型。所以同一个 `Sum`，约束里写 `int` 只收 `int` 本身，写 `~int` 才连 `MyInt` 这类自定义类型一起收。想传 `MyInt` 就必须写 `~int`。

### 一个约束可以同时要类型和方法：满足性要过两关

约束接口不只装类型列表。原课给了一个同时声明两者的约束，正好用来把"满足"说精确：〔TBX05 p11-12〕

```go
type C1 interface {
	~int | ~int32
	M1()
}

type T struct{}

func (T) M1() {}

type T1 int

func (T1) M1() {}

func foo[P C1](t P) {}

func main() {
	var t1 T1
	foo(t1) // 通过
	var t T
	foo(t) // 编译报错：T does not satisfy C1
}
```

`C1` 声明了一个方法 `M1`，以及两个可用作类型实参的类型（`~int | ~int32`），类型列表中的多个类型用 `|` 分隔。`T` 和 `T1` 都实现了 `M1`，但 `T` 的底层类型是 `struct{}`，`T1` 的底层类型是 `int`。判定要过两关：`T` 过了方法集合这一关，却因底层类型不是 `int` 或 `int32` 而不满足类型列表这一关，`foo(t)` 在编译阶段报错；`T1` 两关都过，是合法的类型实参。〔TBX05 p11-12〕

这背后是一条统一规则：类型实参 `T` 满足约束 `C`，当且仅当 `T` 是 `C` 定义的类型集合中的一个元素。〔实测：/Users/akka/Toolchain/go/current/doc/go_spec.html:2753-2762〕约束必须是接口这一点也是规范条款：类型约束是一个定义了"哪些类型实参可用"的接口。〔实测：/Users/akka/Toolchain/go/current/doc/go_spec.html:2693-2699〕原课写作年代更早，记录的报错是 `T does not implement C1`；现行 go1.26.4 在约束语境里把措辞从 implement 换成了 satisfy，拒绝的时机和原因没有变。〔实测：/Users/akka/Toolchain/go/current/src/go/types/instantiate.go:240-257〕

反过来，一个类型不满足约束时，报错会把"缺谁"点出来：

```go fail
package main

type Number interface {
	~int | ~float64
}

func Sum[T Number](xs ...T) T {
	var s T
	for _, x := range xs {
		s += x
	}
	return s
}

func main() {
	_ = Sum("a", "b")
}
```

```text
string does not satisfy Number (string missing in ~int | ~float64)
```

`string` 既不是 `~int` 也不是 `~float64`，编译器直接拒绝。这就是泛型相比老办法（`interface{}` 参数加运行时类型断言）的核心收益：错误从运行期提前到了编译期，还不用写一堆 `switch v.(type)`。

组织上有一条原课建议：做约束的接口类型与做传统接口的接口类型最好分开定义，除非约束真的既需要方法集合，也需要类型列表。〔TBX05 p11-12〕原因在下一节就能看到：混用之后，这个接口拿去当普通类型用会直接编译报错。

## 三、三个绕不过的限制和绕法

下面三条来自对泛型中间件一年多的实践，是判断"这里能不能用泛型"的关键：〔TBX04 p2〕接口和结构体不支持泛型方法；泛型约束不能作为类型声明；泛型约束只能是接口，而不能是结构体。〔TBX04 p2〕先补一句容易误解的区分：接口或结构体本身可以是泛型（类型级参数允许），被禁的只是它们的方法再携带类型参数。〔TBX04 p2〕

### 限制一：方法不能有类型参数

原课从 ORM 一步步推出这条限制。没有泛型时，操作任意类型的接口只能用 `interface{}`：

```go
type Orm interface {
	Insert(data ...interface{}) (sql.Result, error)
}
```

问题出在混用：用户可以写 `Insert(&User{}, &Order{})`，插入失败而编译器无法检测。〔TBX04 p2-3〕ORM 真正想要的是把 `data` 限制为单一类型的多个实例，于是有了第一条改造路线，把类型参数声明在接口上：

```go
type Orm[T any] interface {
	Insert(data ...T) (sql.Result, error)
}
```

这样 `T` 确实保证了传入的都是同一个类型的不同实例，但每种模型都要一个实例：插 `User` 声明 `var userOrm Orm[User]`，插 `Order` 声明 `var orderOrm Orm[Order]`。应用有多少个模型，就要声明多少个 `Orm` 实例，这显然不可接受：`Orm` 应该是一个可以操作任意模型的接口，一个实例既能操作 `User` 也能操作 `Order`。所以类型参数不能声明在接口上。〔TBX04 p3〕

第二条路线是声明在方法上：

```go
type Orm interface {
	Insert[T any](data ...T) (sql.Result, error)
}
```

乍看完全达标，用户只需 `orm.Insert[*User](&User{}, &User{})`。但一编译就被拦下，原文记录的报错是 `interface method cannot have type parameters`。〔TBX04 p3-4〕结构体方法同样被禁，原文记录的报错是 `invalid AST: method must have no type parameters`：〔TBX04 p4〕

```go fail
package main

type Orm struct{}

func (o Orm) Get[T any](id int) T {
	var zero T
	return zero
}

func main() {}
```

```text
method must have no type parameters
```

现行 go1.26.4 的两条报错略有变化：接口方法报 `interface method must have no type parameters`；结构体方法去掉了 `invalid AST` 前缀，只剩 `method must have no type parameters`。〔实测：/Users/akka/Toolchain/go/current/src/cmd/compile/internal/syntax/parser.go:1814-1816〕〔实测：/Users/akka/Toolchain/go/current/src/cmd/compile/internal/syntax/parser.go:1465-1467〕这不是编译器实现偷懒，规范里方法声明的文法本来就没有类型参数列表的位置，receiver 上的类型参数只是泛型基类型对应参数的占位。〔实测：/Users/akka/Toolchain/go/current/doc/go_spec.html:2980-2991〕〔实测：/Users/akka/Toolchain/go/current/doc/go_spec.html:3033-3039〕

被禁的 API 形态恰恰是客户端类中间件最想要的。原课举的是 HTTP 客户端 `type HttpClient interface { Get[T any](url string) (T, error) }`，期望 `client.Get[User]("/user/123")` 直接得到 `User` 实例；缓存客户端同样想要 `type CacheClient interface { Get[T any](key string) (T, error) }`，它们都无法通过编译。〔TBX04 p4-5〕原文的定性是：这个限制对所有的客户端类应用都很不友好，这些客户端包括 Redis、Kafka 等各种中间件，也包括 HTTP、ORM 等框架。〔TBX04 p5〕逻辑也好理解：被禁的正是"调用方指定返回类型"的 API（`Get[T]` 返回 `T`），客户端类中间件正是这类 API 的主要需求方，所以杀伤最大。〔TBX04 p4-5〕

**绕法：Builder 模式，把类型参数挪到一个辅助类型上**。类型参数不能挂在方法上，但能挂在结构体上，于是把"请求"做成一个泛型 builder，类型参数落在 builder 上：〔TBX04 p9-10〕

```go
package main

import "fmt"

type Client struct{ base string }

type GetReq[T any] struct {
	c    *Client
	path string
	dec  func(string) T
}

func NewGet[T any](c *Client, path string, dec func(string) T) *GetReq[T] {
	return &GetReq[T]{c: c, path: path, dec: dec}
}

func (r *GetReq[T]) Do() T {
	return r.dec(r.c.base + r.path)
}

func main() {
	c := &Client{base: "http://api"}
	asStr := NewGet(c, "/user/1", func(raw string) string { return raw })
	asLen := NewGet(c, "/user/1", func(raw string) int { return len(raw) })
	fmt.Println(asStr.Do())
	fmt.Println(asLen.Do())
}
```

```text
http://api/user/1
17
```

API 从想象中的 `client.Get[User](url)` 变成 `NewGet[User](client, path, decoder).Do()`；类型参数落在 `GetReq` 和 `NewGet` 上，绕开了方法不能声明额外类型参数的限制。这里的 `Do` 只是拼接字符串并调用回调，**没有发起 HTTP 请求**；真实客户端还需要处理网络、解码和错误。

原课的 Builder 版本结构相同，值得照着看一遍调用流程和角色分工：〔TBX04 p9〕

```go
type HttpClient struct {
	endpoint string
}

type GetBuilder[T any] struct {
	client *HttpClient
	path   string
}

func (g *GetBuilder[T]) Path(path string) *GetBuilder[T] {
	g.path = path
	return g
}

func (g *GetBuilder[T]) Do() T {
	// 真实发出 HTTP 请求
	url := g.client.endpoint + g.path
}
```

原课示例的边界要如实交代：第一版 `Do()` 的正文只有 `url := g.client.endpoint + g.path` 一行，注释虽写着"真实发出 HTTP 请求"，代码里既没有请求逻辑也没有返回值，这个版本不能编译运行。真正发请求的版本在下一页的变体里：〔TBX04 p10〕

```go
func (g *GetBuilder[T]) Do() T {
	var t T
	g.client.get(g.path, &t)
	return t
}
```

完整调用流程是 `NewGetRequest[User](client).Path("/user/123").Do()`：构造函数返回初始 Builder，`Path` 链式设置路径并返回自身，`Do` 执行动作拿到 `T`。原课指出两者最大的不同在于 `NewGetRequest` 是一种过程式的设计：Builder 之下，`HttpClient` 被视作各种配置的载体，而不是一个真实发出请求的客户端；也可以反过来，把 `HttpClient` 做成真的客户端，而把 `GetBuilder` 看成一层泛型的皮。〔TBX04 p10〕有 Java 之类面向对象背景会不习惯这种写法，但无论哪一种，本质上都是过程式写法，核心都是 Client + Builder，将来设计客户端中间件时可以尝试这个思路。〔TBX04 p9-10〕

### 限制二：含类型项的接口不能当普通值类型

想限制一个接口只接受几种特定类型（比如 SQL Builder 的 `Select` 只收 `string` 和 `Aggregate` 两种列），有人会想用约束当参数类型：〔TBX04 p7-8〕

先把问题摆出来。`Select` 最基本的设计是直接收 `string` 列名，但这样用户想用聚合函数就得自己拼接 `Select("AVG(age)")`；于是中间件会提供 `Avg(col string)` 加 `As(alias)` 帮用户把聚合函数变成方法调用，期望的用法是 `Select(Avg("age").As("avg_age"), "name")`。这时 `Select` 必须同时接收 `string` 和 `Aggregate` 两种输入，而它们没有共同接口。〔TBX04 p5-6〕没有泛型时大多数时候用 `interface{}` 参数加 switch-case 判断：

```go
func (s *Selector) Select(cols ...interface{}) *Selector {
	for _, col := range cols {
		switch c := col.(type) {
		case string:
			s.columns = append(s.columns, Aggregate{col: c})
		case Aggregate:
			s.columns = append(s.columns, c)
		default:
			panic("invalid type")
		}
	}
	return s
}
```

这个旧方案有两个只能到运行期才暴露的错误：类型对不对，要到运行期 default 分支 panic 时才知道；漏写展开也一样，本意是传一个 `var cols []string`，结果写成 `Select(cols)` 而不是 `Select(cols...)`，同样运行期 panic，编译器丝毫帮不上忙。〔TBX04 p7〕

自然的泛型设想是把联合约束同时用作参数类型和字段类型，既拿到编译期检查又消灭 panic：

```go fail
package main

type Aggregate struct{ fun, col string }

type Selectable interface {
	string | Aggregate
}

type Selector struct{}

func (s *Selector) Select(cols ...Selectable) *Selector { return s }

func main() {}
```

```text
cannot use type Selectable outside a type constraint: interface contains type constraints
```

含类型项的非基本接口只能作为约束，不能作为普通参数类型；只包含方法的基本接口则既可作值类型也可作约束。〔实测：/Users/akka/Toolchain/go/current/doc/go_spec.html:1556-1558〕原文由此得出的结论是：泛型约束不能被用于做参数，它只能和泛型结合在一起使用，这就导致我们并不能用泛型的约束来解决某个接口可以处理有限多种类型输入的问题。所以长期来看，`interface{}` 这种参数类型还会广泛存在于所有中间件的设计中。〔TBX04 p8〕泛型函数可以限制同一个类型参数的输入，但不能用这个约束直接创建同时容纳 `string` 和 `Aggregate` 的异构参数列表。〔TBX04 p7-8〕

**绕法：标记接口**。用一个带私有方法的接口当"准入标记"：〔TBX04 p10-11〕

```go
type Selectable interface{ aggr() }

type Column string
func (Column) aggr() {}

type Aggregate struct{ fun, col string }
func (Aggregate) aggr() {}

func (s *Selector) Select(cols ...Selectable) *Selector
```

空接口允许任何类型，不能限制输入，所以标记接口必须至少声明一个私有方法。〔TBX04 p10-11〕未导出方法让其他包无法自行声明同一个包私有方法，但其他包仍可能通过嵌入该接口或已有实现来满足接口，所以这不是严格封闭的类型枚举。代价是普通列要写成 `Select(Column("name"))`，不过相比接收 `interface{}` 的旧方案，至少拿到了编译器对类型的检查。〔TBX04 p11〕

### 限制三：嵌入结构体不形成子类型约束

业务里常见需求：ORM 的 `Insert` 只接受嵌入了 `BaseEntity`（共享字段和公共方法）的实体。原课的设想是利用 Go 的组合特性，用泛型约束限制实体必须组合了承载公共字段 `Id int64` 与公共方法的 `BaseEntity`：〔TBX04 p8〕

```go
type BaseEntity struct {
	Id int64
}

func Insert[Entity BaseEntity](e *Entity) {}

type myEntity struct {
	BaseEntity
	Name string
}
```

行不通。Go 泛型约束必须是一个接口，而不能是结构体，原文记录的报错是 `myEntity does not implement BaseEntity`。〔TBX04 p8〕作者自己指出这个报错并不准确，更准确的信息应该指出 `BaseEntity` 只能为接口。现行 go1.26.4 的报错已经改准确了：约束的底层类型不是接口时，报 `does not satisfy` 加括号说明 `is not an interface`，不再出现误导性的 implement 措辞。〔实测：/Users/akka/Toolchain/go/current/src/go/types/instantiate.go:240-257〕规范条款本身未变：类型约束是接口。〔实测：/Users/akka/Toolchain/go/current/doc/go_spec.html:2693-2699〕

从机制上看，`[E BaseEntity]` 相当于只接受 `BaseEntity` 本身，并非接受所有嵌入它的结构体；嵌入不会建立子类型关系，Go 也没有直接表达"包含某个字段"的通用约束。〔TBX04 p8-9〕这个限制直接堵死了设计共享字段的泛型方法的道路，因为约束拿不到字段。〔TBX04 p8〕影响面也不对称：对业务开发的影响大于对中间件开发，因为业务开发会经常遇到必须共享某些数据的场景，例如这里的 ORM 实体，还有前端接收参数的场景等。〔TBX04 p8-9〕

**绕法：Getter/Setter 接口**。把共享字段提取成方法，约束写成接口，再配合嵌入一个 `BaseEntity` 让每个实体不用重复实现：〔TBX04 p11-12〕

```go
type Entity interface {
	Id() int64
	SetId(id int64)
}

type BaseEntity struct{ id int64 }

func (b *BaseEntity) Id() int64      { return b.id }
func (b *BaseEntity) SetId(id int64) { b.id = id }

type User struct {
	BaseEntity
	Name string
}

func Insert[E Entity](e E) {}
```

约束写成 `Entity` 接口，`User` 通过嵌入 `BaseEntity` 拿到 `Id`/`SetId`，就能作为 `Insert[*User](u)` 的实参；通过组合 `BaseEntity`，解决了所有结构体都要实现一遍 `Entity` 接口的问题。〔TBX04 p11-12〕代价是 Go 习惯直接访问字段，这里被迫给每个共享字段加一对方法，不优雅但管用。〔TBX04 p11-12〕

### 收束：三条限制对应三条绕法，以及今天的核对

原课的总结把两组东西对齐：三个限制（接口和结构体不支持泛型方法、泛型约束不能作为类型声明、泛型约束只能是接口）对应三个解决思路（Builder 模式、标记接口、Getter/Setter 接口）。〔TBX04 p12〕作者的自评是：这些解决思路只能算是效果不错，但是不够优雅，所以还是很期盼 Go 将来能够放开这种限制；在这三个约束之下，Go 泛型使用场景过于受限。〔TBX04 p12〕

对中间件作者的建议分两层：对大多数中间件来说，可以使用泛型来提供对用户友好的 API，但是对于内部实现来说，使用泛型的收益非常有限；如果现在已有的代码风格就是过程式的，那么可以尝试用泛型将它完全重构。〔TBX04 p13〕对泛型的普及，作者的预判比较悲观：预计泛型从发布到大规模应用还有一段非常遥远的距离，甚至有些公司可能长时间为了保持代码风格统一而禁用泛型特性。〔TBX04 p13〕

这个预判以 2026 年视角核对，标准库层面已经落空：go1.26.4 的标准库自带泛型的 `slices` 与 `maps` 包，`Sort[S ~[]E, E cmp.Ordered]` 就是标准库里的泛型函数。〔实测：/Users/akka/Toolchain/go/current/src/slices/sort.go:15-19〕〔实测：/Users/akka/Toolchain/go/current/src/maps〕但三大限制本身并没有放开：方法仍不能自带类型参数，约束仍必须是接口，含类型项的非 basic 接口仍只能作约束。〔实测：/Users/akka/Toolchain/go/current/src/cmd/compile/internal/syntax/parser.go:1814-1816〕〔实测：/Users/akka/Toolchain/go/current/src/cmd/compile/internal/syntax/parser.go:1465-1467〕〔实测：/Users/akka/Toolchain/go/current/doc/go_spec.html:2693-2699〕〔实测：/Users/akka/Toolchain/go/current/doc/go_spec.html:1556-1558〕"公司是否仍为风格统一而禁用泛型"没有可核对的运行数据，原课的悲观态度保留为作者当时的判断。

## 三点五、泛型落进真实数据结构：二分、堆、container/heap、Set

前面学的类型参数、约束、`~` 与类型集合，落到真实数据结构上才见真章。这一小节的四个例子全部来自课程的数据结构课（泛型版）：一个泛型二分、一个手写小根堆、一个适配 `container/heap` 的优先队列、一个泛型 `Set`,它们分别练习 `[T cmp.Ordered]`、数组隐式树、接口适配五方法和 `comparable` 约束。第十三节榜单 TopN 会用到 `container/heap`，那里只讲用法，这里补齐它的底层。

### 泛型二分：begin<=end 的边界与区间版双端短路

第一个例子：泛型二分查找。签名是 `func BinarySearch[T cmp.Ordered](arr []T, target T) int`，约束用标准库 `cmp.Ordered`（1.21 起 `cmp` 包承担了旧 `constraints` 包的角色），一份实现就能服务所有可排序类型，测试里以 `int` 实例化。〔C:go_frame/data_structure/binary_search.go:1-52〕

```go
func BinarySearch[T cmp.Ordered](arr []T, target T) int {
	begin := 0
	end := len(arr) - 1
	for begin <= end {
		middle := (begin + end) / 2
		if arr[middle] == target {
			return middle
		}
		if arr[middle] < target {
			begin = middle + 1
		} else {
			end = middle - 1
		}
	}
	return -1
}
```

注意边界是 `begin <= end` 而不是 `begin < end`。源码注释原话：之所以是 `<=`，是因为"区间内只剩下一个元素时也应该跟 target 进行比较，而不是直接返回-1"。写成 `<`，区间收缩到只剩一个元素（`begin == end`）时就退出循环返回 -1，这个唯一的候选被漏判。用 `arr = []int{1, 3, 6, 8, 10}` 查 `6` 推演（教学推导）：begin=0、end=4，middle=2，`arr[2]=6` 命中返回 2；查 `2` 则走三轮：middle=2，6>2 使 end=1 → middle=0，1<2 使 begin=1 → begin=end=1，middle=1，3>2 使 end=0，此时 begin>end 退出返回 -1。〔C:go_frame/data_structure/binary_search.go:1-52〕

两点预期管理：其一，数组里有多个等于 target 的元素时，注释明确"只返回其中某一个的下标"，不保证最左也不保证最右；其二，未命中时 begin 与 end 一路向对方滑，begin 只增（middle+1）、end 只减（middle-1），正常退出循环返回 -1。官方测试验证了两条路径：`TestBinarySearch` 跑 100 轮（每轮生成长度 100 的随机数组、排序、随机 target），命中下标校验 `arr[index] == target`，返回 -1 时全表扫描反证 target 确实不存在；`TestBinarySearch1` 用固定数组查 1、6、10。〔C:go_frame/data_structure/binary_search.go:1-52〕这个二分有配套基准：1200 个元素的数组，模拟 B+树单个节点内的查找，实测 46.83 ns/op（源码注释里的 55ns 是旧机器数据）。〔C:go_frame/data_structure/binary_search_test.go:50-107〕〔实测：work/run/C_go_frame__data_structure/E2/out.txt〕

第二个例子是**区间版** `BinarySearch4Section[T cmp.Ordered]`，返回第一个 `>=target` 的元素下标，即插入位置。前置条件写在注释里：arr 必须单调递增且不存在重复元素，空数组直接返回 -1。它的骨架每轮循环先做双端短路检查：

```go
for {
	if arr[begin] >= target {
		return begin
	}
	if arr[end] < target {
		return end + 1
	}
	...
}
```

两个短路各自兜住一类边界：`arr[begin] >= target` 说明 begin 已经滑到 target 之后（target 比首元素还小时首轮即返回 0）；`arr[end] < target` 说明 end 已经滑到 target 之前，target 大于全部元素时 `end+1` 即 `len(arr)`,这正是"比最后一个元素还大返回长度、比第一个元素还小返回 0"的由来。短路不成立才进入二分：`arr[middle] > target` 时 `end = middle-1`（注释提醒 `arr[end]` 可能会跑到 target 前面），`arr[middle] < target` 时 `begin = middle+1`（`arr[begin]` 可能会跑到 target 后面），相等直接返回 middle。以 `arr=[1,3,6,8,10]`、`target=4` 推演（教学推导）：begin=0、end=4，双端检查 1<4、10>=4 均不触发；middle=2，6>4 使 end=1；再查 arr[0]=1<4、arr[1]=3<4 触发第二个短路，返回 `end+1=2`,正是 4 的插入位置。官方测试 `TestBinarySearch4Section` 跑 30 轮随机 float64 严格验证：target=arr[0]-1 期望 0、target=末元素+1 期望 len(arr)、target=arr[0] 期望 0、每个相邻元素中点期望 i+1、每个 arr[i+1] 期望 i+1。〔C:go_frame/data_structure/binary_search.go:26-52〕〔C:go_frame/data_structure/binary_search_test.go:65-107〕

### 手写小根堆：数组隐式树、O(N) 建堆、ReplaceTop

第三个例子是泛型小根堆 `Heap[T cmp.Ordered]`，内部只有一个切片 `arr`，用数组隐式存完全二叉树：下标 i 的左孩子是 `2i+1`、右孩子是 `2i+2`、父节点是 `(i-1)/2`。`NewHeap` 先 `slices.Clone` 输入切片再包装，避免污染调用方数据。〔C:go_frame/data_structure/heap.go:1-21〕

```go
func (heap *Heap[T]) downwardAdjust(parent int) {
	left := 2*parent + 1
	if left >= len(heap.arr) { //没有左孩子，说明parent已经是树的叶节点了
		return
	}
	//从父子三人（如果没有右孩子，则是父子二人）中找到最小者
	minIndex := parent
	...
	// 最小者跟父节点交换
	if minIndex != parent {
		heap.arr[minIndex], heap.arr[parent] = heap.arr[parent], heap.arr[minIndex]
		heap.downwardAdjust(minIndex) //递归向下调整
	}
}
```

下滤 `downwardAdjust(parent)`：左孩子下标越界说明 parent 已是叶子，直接返回；否则在父子三人（无右孩子则父子二人）中找最小者，最小者不是父节点就交换，并从被换下去的位置递归继续下滤。这与第十三节"维持前 100"的小顶堆是同一棵树：父节点比子节点小，堆顶是当前第 N 名。〔C:go_frame/data_structure/heap.go:22-49〕〔W09-1 p19〕

关键在 `Build`。从 `NewHeap` 拿到的只是元素摆放未调整的切片，还不能保证堆序。`Build` 是自底向上的 O(N) 建堆：

```go
n := len(heap.arr)
if n <= 1 {
	return
}
lastIndex := n / 2 * 2              //最下层、最右那个三角形区域的右孩子在数组里的下标
for i := lastIndex; i > 0; i -= 2 { //逆序检查每一个三角形区域
	right := i
	parent := (right - 1) / 2   //右孩子可能是不存在的，但父节点一定存在
	heap.downwardAdjust(parent)
}
```

三个细节：起点 `lastIndex = n/2*2` 是最下层、最右那个三角形区域的右孩子下标,把数组按"父+两个孩子"分成一个个三角形区域，从最后一个三角形扫起；步长 `i -= 2` 从右往左逐三角形逆序，每个三角形由右孩子下标反推 `parent = (right-1)/2`；注释强调"右孩子可能是不存在的，但父节点一定存在",最下层可能只有左孩子没有右孩子，但只要右孩子下标被拿来当锚点，反推出的父节点必然落在数组里。n<=1 直接返回。〔C:go_frame/data_structure/heap.go:50-76〕

之后的增删各是两步：`Push` 把元素 append 到数组尾部，再对新下标 `len-1` 做 `upwardAdjust`（比父小则交换并向上递归，到下标 0 为止）；`Pop` 取出 `arr[0]`，用最后一个元素覆盖堆顶、缩容 `arr[:len-1]`，再 `downwardAdjust(0)`，空堆返回错误。ReplaceTop 是维护 TopN 的关键路径：非空时直接 `arr[0] = ele` 后 `downwardAdjust(0)`，一次下滤完成换顶；先 `Pop` 再 `Push` 要经历一次下滤加一次上滤，调整动作约为其两倍,所以"堆满后新分数比堆顶大才替换"应走 ReplaceTop 语义；堆为空时 ReplaceTop 退化为 Push。〔C:go_frame/data_structure/heap.go:77-125〕官方测试 `TestHeap` 用 `{50, 20, 49, 15, 30, 62}` `NewHeap` 后 `Build`、`Push(5)`，循环 `Pop` 全部元素，即一次堆排序。〔C:go_frame/data_structure/heap.go:16-21〕

### container/heap 适配：五方法、两个坑

标准库 `container/heap` 不提供现成堆类型，任何类型实现五个方法就能被 `heap.Init`/`heap.Push`/`heap.Pop` 操作：`Len() int`、`Less(i, j int) bool`、`Swap(i, j int)`、`Push(x interface{})`、`Pop() interface{}`。课程用 `PriorityQueue[T cmp.Ordered]`（底层 `[]*Item[T]`，`Item` 含 `Info string` 与 `Value T`）示范：〔C:go_frame/data_structure/std_heap.go:1-36〕

```go
func (pq PriorityQueue[T]) Less(i, j int) bool {
	return pq[i].Value < pq[j].Value
}

// 往slice里append,需要传slice指针
func (pq *PriorityQueue[T]) Push(x interface{}) {
	item := x.(*Item[T])
	*pq = append(*pq, item)
}

// 让slice指向新的子切片，需要传slice指针
func (pq *PriorityQueue[T]) Pop() interface{} {
	n := len(*pq)
	item := (*pq)[n-1]
	*pq = (*pq)[0 : n-1]
	return item
}
```

两个坑。**坑一：Less 的方向决定堆序语义**。`Less(i,j)` 返回 `pq[i] < pq[j]` 就是小根堆（堆顶最小），返回 `>` 就是大根堆（堆顶最大）,调转比较方向即在小根与大根之间切换。源码注释声称"golang 默认提供的是小根堆，而优先队列是大根堆，所以这里要反着定义 Less"，这个说法与代码实际行为不符：`<` 就是常规小根堆 Less，并未反向（反向应写 `>`）；`TestStdHeap` 的注释也写明 `heap.Pop` 删除的堆顶"即最小的元素"，逐个 Pop 得到按 Value 升序的输出。想要"每次弹出 Value 最大的元素"的优先队列，把 Less 改成 `>` 即可。〔C:go_frame/data_structure/std_heap.go:5-23〕〔C:go_frame/data_structure/std_heap.go:12-23〕

**坑二：Push/Pop 必须用指针接收者**。`Push` 里 `*pq = append(*pq, item)`，append 可能更换底层数组，必须把新 slice header 写回；`Pop` 里 `*pq = (*pq)[0:n-1]` 切掉尾元素改变了长度。两者都修改 slice header，因此接收者必须是 `*PriorityQueue[T]`,值接收者的修改作用在副本上会丢失。源码在两个方法上方分别标注"往slice里append,需要传slice指针""让slice指向新的子切片，需要传slice指针"。还有一处签名细节：接口要求 `Push(x interface{})`，实现里需要 `x.(*Item[T])` 把 any 断言回具体元素类型。〔C:go_frame/data_structure/std_heap.go:24-36〕

调用侧的用法（`TestStdHeap`）：`make(PriorityQueue[int], 0, 10)` 后直接对 pq 逐个 `Push` 三个元素（普通 append，未建堆），`heap.Init(&pq)` 建堆，再 `heap.Push(&pq, D)` 走接口增元素，循环 `heap.Pop(&pq)` 实现堆排序；调用处一律传 `&pq`。对照第十三节 TopN：那里 `heap.Push(h, it)` 进堆、堆满后 `heap.Pop(h)` 再 `heap.Push(h, it)` 挤掉当前最小者，用的正是这套五方法协议。〔C:go_frame/data_structure/std_heap.go:12-23〕〔W09-1 p22〕

顺带对照手写版与标准库版的取舍：手写 `Heap[T]` 值语义内聚可控，标准库要求实现五方法接口但能适配任意类型与优先级规则。〔C:go_frame/data_structure/binary_search.go:1-52〕

### 泛型 Set：comparable 约束与 struct{} 值

第四个例子回到“类型本身带参数”。用 map 模拟 set 是 Go 社区多年的惯用法，Go 1.18 起就可以定义这样的泛型具名类型；它不是 Go 1.24 引入的泛型类型别名。〔C:go_basic/v24/generic_type_alias.go:1-11〕

```go
type Set[T comparable] map[T]struct{}

func NewSet[T comparable](n int) Set[T] {
	m := make(map[T]struct{}, n)
	return Set[T](m)
}
```

三个设计点：**`comparable` 约束**允许 `T` 作为 map key；不过 Go 1.20 起接口类型也可能满足它，接口动态值若不可比较，作为 key 时仍会 panic。**`struct{}` 值**不携带成员数据，只借 key 的存在性表达集合。**参数 n** 是 `make` 的容量提示，不是硬上限；`Set[T](m)` 显式转换成具名类型，也可以直接 `make(Set[T], n)`。〔C:go_basic/v24/generic_type_alias.go:1-37〕

五操作与底层 map 操作一一对应：`Add` 置 `set[ele] = struct{}{}`；`Len` 复用 `len(set)`；`Remove` 复用 `delete(set, ele)`；`Exists` 用 `_, exists := set[ele]` 的 comma-ok 双返回判存在；`Range` 用 `for key := range set` 遍历并对每个 key 调用回调。调用方从回调拿到元素，但这里的 `Set` 本身是导出的 map 类型，仍可直接访问底层 map。`Exists` 必须用双返回，因为 `struct{}` 的零值无法区分 key 存在与否。〔C:go_basic/v24/generic_type_alias.go:12-37〕

官方测试 `TestSet` 位于外部测试包 v24_test、只依赖公开 API：`NewSet[int](10)` 后 `Add 2、4、6`，断言 `Len()==3`、`Exists(2)` 为真；`Remove(2)` 后断言 `Exists(2)` 为假、`Len()==2`；最后 `Range` 逐个回调打印，实测输出 4、6（map 遍历无序）。实测 TestSet PASS。〔C:go_basic/v24/generic_type_alias_test.go:9-52〕〔实测：work/run/C_go_basic__v24/E1/out.txt〕

### 小结与连线

- 泛型二分的边界是 `begin <= end`：区间剩一个元素时仍要比较一次；区间版 `BinarySearch4Section` 用 begin/end 双端短路找第一个 `>=target` 的下标。多命中只返回其中某一个，不保证最左或最右。〔C:go_frame/data_structure/binary_search.go:26-52〕
- 手写堆用数组隐式存完全二叉树（左 2i+1、右 2i+2、父 (i-1)/2）；`Build` 自底向上按三角形逆序下滤是 O(N) 建堆；`Push` 上滤、`Pop` 尾换头后下滤；`ReplaceTop` 一次下滤，比 Pop+Push 少一半调整。〔C:go_frame/data_structure/heap.go:22-125〕
- `container/heap` 适配五方法 `Len/Less/Swap/Push/Pop`；Less 方向决定小根/大根，Push/Pop 改 slice header 必须指针接收者。〔C:go_frame/data_structure/std_heap.go:1-36〕
- `type Set[T comparable] map[T]struct{}` 自 Go 1.18 可用；`comparable` 允许类型参数作 key，但接口动态值仍须可比较。〔C:go_basic/v24/generic_type_alias.go:1-37〕

这些结构都不是玩具：第十三节榜单 TopN 的小顶堆、业务里常见的去重集合，落点就是它们。判断"这里能不能用泛型"的判据，下一节展开。

## 四、该用与不该用

适合用泛型的三种情况：〔TBX05 p16-17〕

1. 函数操作 slice、map、channel，且不对元素类型做假设。这种场合泛型可以替代反射，更快也更有类型安全。
2. 通用数据结构（有序 Set、堆、优先队列、链表、二叉树），替换 `interface{}` 存储，免去类型断言，编译期全面检查。
3. 发现自己多次写仅类型不同的样板代码（开头那三个 `Add` 就是）。

不适合的两种情况：〔TBX05 p17〕

1. 对某类型的值只做一件事，就是调用它的一个方法。用接口：`func ReadAll(r io.Reader)` 是对的，`func ReadAll[R io.Reader](r R)` 是错的，后者更啰嗦也不快。
2. 泛型让代码更复杂，就不要用。判据只有一条：类型参数没有让代码更清晰，就回退到接口或具体类型。

先看标准库：1.21 起有 `slices`、`maps`、`cmp` 三个包，覆盖了大部分容器辅助函数，`go doc slices` 能看全部。它们就是泛型的最佳范例，先抄它们的用法，比从零写泛型更靠谱。

## 五、性能：别背旧数字

课程里的基准来自 Go 1.18 beta2：泛型版 `slices.Sort` 比当时的 `sort.Ints` 快近一倍，而且零分配。〔TBX05 p15〕这个结论今天已经失效，失效原因一句话就能讲清：`sort.Ints` 早就不自己排序了。

本机 go1.26.4 上，〔1.26 修正〕`grep -n "func Ints" $(go env GOROOT)/src/sort/sort.go` 输出 `func Ints(x []int) { slices.Sort(x) }`。标准库把 `sort.Ints` 改成了对泛型 `slices.Sort` 的薄封装，也就是说，1.18 时泛型版的"对手"，现在本身就是泛型版。

重新跑一遍排序基准（Apple M4、10 万个 `int`）：〔实测〕`cd exercises/p33gen && go test -run '^$' -bench 'Benchmark(SortInts|SlicesSort|Specialized)$' -benchmem -count=1`

```text
BenchmarkSortInts-10       	     322	   3789321 ns/op	  802868 B/op	       1 allocs/op
BenchmarkSlicesSort-10     	     324	   3763293 ns/op	  802817 B/op	       1 allocs/op
BenchmarkSpecialized-10    	     318	   3789612 ns/op	  802817 B/op	       1 allocs/op
```

三个数字在误差范围内相等，每次 1 次分配来自基准里克隆输入。另一个对照是用比较闭包的 `sort.Slice`，同机器上单独测过一次（不在练习包里），约慢 1.6 到 2 倍、多两次分配（闭包带来的）。结论不是"泛型总是更快"，而是：泛型让标准库能把 `sort.Ints` 的内部换成更快的专用实现，调用方一行都不用改。数字随机器浮动，看相对差距，别背绝对值。

## 小结

1. 泛型把算法和类型解耦：类型参数 `[T 约束]` 是占位符，约束是接口（可含类型集合），具化在编译期替换类型参数。
2. `~int` 表示"底层类型是 int 的一切类型"；漏写 `~`，`MyInt` 这类自定义类型传不进去。
3. 三个限制：方法不能声明额外类型参数；含类型项的接口不能作为普通值类型；嵌入结构体不形成子类型约束。
4. 适合：操作容器且不假设元素类型、通用数据结构、多次出现的样板代码。不适合：只调用一个方法、让代码更复杂。
5. 泛型的性能收益来自"标准库能用更快的专用实现替换内部"，不是泛型本身更快。

## 常见坑

- 想给方法加类型参数，编译报 `method must have no type parameters`。〔TBX04 p2-4〕
- 约束里漏写 `~`，`MyInt` 之类自定义类型传不进去。〔TBX05 p11-12〕
- 为了"通用"把只调用一个方法的函数写成泛型，反而更难读。〔TBX05 p17〕
- 用空接口当标记接口：所有类型都实现它，等于没拦。〔TBX04 p10-11〕
- 还在背 1.18 的"泛型快一倍"结论，没注意到 `sort.Ints` 已变成 `slices.Sort` 的薄封装。

## 判断题与答案

先自己答，再看后面。

1. `type Number interface{ ~int | ~float64 }` 里去掉 `~`，`MyInt` 还能调用 `Sum` 吗？
2. 为什么 `Get[T any](id int) T` 能作为普通函数，却不能作为方法？
3. 你要写一个只接受 `Column` 和 `Aggregate` 两种输入的接口，怎么用 Go 表达？
4. `ReadAll[R io.Reader]` 比 `ReadAll(r io.Reader)` 好在哪里，差在哪里？

**答案**

1. 不能。去掉 `~` 后约束只收类型本身是 `int` 或 `float64` 的类型；`MyInt` 底层虽是 `int`，本身却是不同的定义类型。
2. 因为 Go 的方法（有 receiver 的函数）本身不能带类型参数，这是语法限制，编译器报 `method must have no type parameters`；普通函数没有 receiver，可以带。
3. 用未导出方法的标记接口限制直接实现，再用 `Column` 和 `Aggregate` 实现；注意外部仍可能通过嵌入满足接口，不能把它当严格封闭的枚举。
4. `ReadAll(r io.Reader)` 更易读也不慢；`ReadAll[R io.Reader]` 多一层类型参数，代码更复杂，没有收益。对某个值做的全部操作只是调用它的一个方法时，就用接口，不用类型参数。

练习包 `exercises/p33gen/` 里有排序基准（`SortInts`、`SlicesSort`、`Specialized`）、`Set`、标记接口和编译错实拍，可以直接跑。

## 在项目里

这里没有现成的泛型声明：`dnsproxi-go` 没声明过类型参数，只在 `cache/evict.go` 里用到内建的 `min`。`cache/cache.go` 的三张表（`m`、`slot`、`access`）是"同一算法跑在不同值类型上"的候选，但它们的更新要在同一个函数里联动，抽成泛型收益有限，保持具体类型是合理取舍。

## 想深入：正文之外的干货

### TBX05 泛型为什么来得这么晚，又是怎么定型的

- Go 团队对泛型的顾虑写在 FAQ 里：不紧迫（早期优先级是规模化、可读性、并发）、与"简单"哲学有悖（新语法加类型系统与运行时的复杂实现）、一直没找到价值抵得过复杂性的方案。直到语言成熟才下决心，与包依赖、错误处理并列为三大待解问题。〔TBX05 p4-5〕
- 2009 年 Russ Cox 那张"三选一"表（拖慢程序员、拖慢编译器、拖慢执行）如 CAP 一样不可兼得：不实现泛型就到处复制 `AddInt`；学 C++ 每类型实例化一份就拖慢编译器、代码膨胀；学 Java 装箱拆箱就拖慢执行。Go 最终选了编译期 hybrid 方案，接受编译器一度变慢约 15%（1.19 起已兑现优化承诺）。〔TBX05 p5〕〔TBX05 p19〕
- 设计简史值得记住三个转折点：2018.8 的第一版草案用 `contract` 关键字定义约束；2020.6 Wadler（Haskell 设计者之一、Java 泛型设计者）发表《Featherweight Go》给出形式化证明，官方随后弃 contract 改用扩展 interface 作约束；2020.11 定稿方括号语法并去掉 `type` 关键字。今天写 `[T Stringer]` 就是这三步的产物。〔TBX05 p5-8〕
- `any` 不是新类型，是 `interface{}` 的别名（1.18 起的预定义标识符），语义与性能完全等价；`Vector[T any]` 里写 `interface{}` 是一回事。〔TBX05 p14〕
- 写约束的接口和写传统行为的接口建议分开定义。约束接口扩展了类型集合（`~int | ~int32` 加方法集），把它当普通接口变量用会报 `interface contains type constraints`（见限制二）；一个接口真的既需要方法集合又需要类型列表时才合并。〔TBX05 p11-12〕
- 官方使用建议有正反两面，正文第四节只留了结论，这里补判据：容器操作且不假设元素类型（可替代反射方案，更快更安全）、通用数据结构（链表、二叉树等 Go 无原生支持的类型，替换 `interface{}` 存储免去类型断言）、多个类型实现完全相同时用类型参数，实现各异就用不同方法；第一遍就抽象是滥用，先重复再抽象。〔TBX05 p16-17〕
- 老教程要过筛：`golang.org/x/exp/slices` 已转正为标准库 `slices`（1.21），`constraints` 包的角色现在由标准库 `cmp` 承担，示例里的 `constraints.Integer` 现行写法多用 `cmp.Ordered`。〔TBX05 p12〕〔TBX05 p18〕

### TBX04 三个限制的完整影响面与绕法代价

- 限制一的影响面：客户端类应用最想要方法级泛型 API（`func Get[T any](url string) (T, error)`），Redis、Kafka 这类中间件客户端和 ORM、HTTP 框架全部受阻；把类型参数挪到接口上则每种模型一个实例（`Orm[User]`、`Orm[Order]`），模型数等于实例数，不可接受。〔TBX04 p2-5〕
- 结构体名称可以作为约束类型项，但只匹配该具体类型；嵌入不产生子类型关系，因此无法用它接受所有嵌入该结构体的实体。〔TBX04 p8-9〕
- 三种绕法的代价对齐看：Builder 换编译可行，代价是链式样板与过程式风格；标记接口换编译期类型检查，代价是每列包一层（`Select(Column("name"))`）加私有方法噪音，但远好于 `interface{}` 参数拖到运行期 panic；Getter/Setter 换约束可行，代价是违背 Go 直接访问字段的习惯、代码量接近翻倍。〔TBX04 p9-12〕
- 当年的悲观预判部分落空：泛型已进入标准库（`slices`、`maps`）和主流框架（ent、go-zero），但"泛型方法"缺位仍压制客户端 API 设计，标准库因此把容器辅助做成包级函数（`slices.Sort`）而不是方法。〔TBX04 p12〕〔TBX05 p15〕

### TBX01 类型系统视角：Go 泛型站在两条路线之间

- 类型强度和静态/动态是两个独立维度：Go 编译期拒绝 `int + float64`（`invalid operation: mismatched types`）且变量声明后类型不可改，是静态强类型；Python 同样严格但检查在运行期、变量可先赋 int 再赋 str，是动态类型；JS 弱类型且动态，整数加字符串会转成拼接。〔TBX01 p2-4〕
- C++ 模板的本质是实例化：`Stack<int>` 和 `Stack<string>` 经 `g++` 编译后 `objdump -d` 能看到生成了两个 `push`，不同泛型参数真的生成了不同的类型；把泛型声明看成"输入类型、返回类型"的类型级函数更直观，`template <int n> class A` 里 `A<0>` 与 `A<1>` 甚至是两个不同类型。〔TBX01 p7-8〕
- Java 用擦除换字节码兼容：`ArrayList<Integer>` 与 `ArrayList<String>` 的 `getClass()` 相等，整个程序只有一种类型；两个只有泛型实参不同的重载报 name clash（擦除后参数都变成 `ArrayList` 变同名方法）。代价是运行时没有泛型信息，无法按 `ArrayList<String>` 重载或反射取实参。〔TBX01 p9-11〕
- C++ 的值参数模板还能做编译期计算（模板元编程）：`fib<n>` 以 `fib<1>`、`fib<0>` 特化为递归基，`fib<10>::v` 编译后直接是常量 55；坑是递归没有特化基例就不终止，直接爆编译器。Go 没有图灵完备的编译期计算，只有常量表达式。〔TBX01 p8-9〕
- 三条实现路线就是"泛型窘境"三个 slow 的当代答案：Rust 单态化与 C++ 实例化同族（编译期生成、零运行时开销、二进制膨胀），Java 擦除省空间但运行期慢，Go 取中间（GC shape 分组共享代码加字典）。判断一门新语言的泛型走哪条路线，基本就摸清了它的性能特征。〔TBX01 p7-11〕
- 静态强类型更易阅读维护但灵活性不如动态弱类型，动态弱类型多是脚本语言、不适合大型程序；Go 从 1.18 开始支持泛型，TypeScript 的泛型借鉴 C++，同样产生新类型。〔TBX01 p11-13〕
- Go 团队从未拒绝过泛型，只是长期没有合适的实现方案；十多年打磨出向后兼容 Go1 的方案后，官方预期泛型和当年的 Go Module 机制一样，还要几个发布版本才成熟稳定。〔TBX05 p18-19〕

省略未吸收：TBX01 的类型系统七问与复合类型清单（K1/K5，与本节泛型主线关系较远）；TBX05 的发布节奏、go2go playground 等历史细节（K1）。其余必读条目已全部融入正文或本块。

## 想深入去哪查

- 设计简史、type set、具化、使用建议：TBX05 全讲；三大限制与绕法：TBX04 全讲；类型系统与泛型的比较视角：TBX01。
- 旧长版：`notes/p33-generics-boundary.md`（排序三选手、编译错实拍）、`notes/p8-fast-track.md`（泛型函数、约束与 `~`、泛型的代价、`slices` 与 `cmp`）。
- 官方设计文档：Go 1.18 type parameters 提案（`43651-type-parameters.md`）。
