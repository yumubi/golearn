---
title: 09 泛型：该用的地方，和绕不过的限制
---

# 09 泛型：该用的地方，和绕不过的限制


> 详细程度：标准 · 约 40 分钟
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
3. **具化**：调用时编译器把 `T` 替换成具体类型，生成一份真正的函数。`Sum(1, 2, 3)` 背后是编译器先做 `sumInt := Sum[int]`，再调 `sumInt(1, 2, 3)`。

类型实参大多能由编译器从实参推断出来，所以通常不用写 `Sum[int](...)`，写 `Sum(1, 2, 3)` 就行。`MyInt` 能传进去，靠的是约束里的 `~`：`MyInt` 底层类型是 `int`，`~int` 把它框了进来；去掉波浪线，`MyInt` 就进不去。〔TBX05 p11-12〕下一节细看这一点。

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

`Vector[int]` 和 `Vector[string]` 是两个不同的类型，底层分别是 `[]int` 和 `[]string`，各有一份自己的 `Dump`。C++ 里 `vector<int>` 和 `vector<string>` 各生成一份代码。Go 同样在编译期具化、不靠 Java 那样的擦除，但实现上更折中：编译器按类型的内存形状（GC shape）分组生成代码，所有指针类型共用一份，再通过隐含的字典传入具体类型信息。这一点材料没讲，细节以官方设计文档为准，写代码时不必依赖它。

## 二、`~` 和类型集合：约束能框住谁

上一节那句"底层类型是它"值得展开，它是 Go 约束里最容易踩空的点。一个约束的本质是它定义了一个**类型集合**，只有落在这个集合里的类型才算满足约束。〔TBX05 p11-12〕

`int` 在约束里写的是"类型恰好是 `int`"；`~int` 写的是"底层类型是 `int` 的一切类型"，包括 `int` 自己，也包括 `type MyInt int` 这类自定义类型。所以同一个 `Sum`，约束里写 `int` 只收 `int` 本身，写 `~int` 才连 `MyInt` 这类自定义类型一起收。想传 `MyInt` 就必须写 `~int`。

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

## 三、三个绕不过的限制和绕法

下面三条来自对泛型中间件一年多的实践，是判断"这里能不能用泛型"的关键。〔TBX04 p2-5〕其中限制一和限制三的报错，本机 go1.26.4 上复现过，信息如下。

### 限制一：方法不能有类型参数

想给 ORM、HTTP、Redis 这类客户端写一个 `Get[T any]` 方法，让调用方写 `client.Get[User](url)`，直接编译不过：〔TBX04 p2-5〕

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

接口和结构体本身可以是泛型（`Orm[User]`、`Orm[Order]`），但类型参数放在类型上，应用里有多少模型就要声明多少个实例；放在方法上，直接编译不过。客户端类库最想要的 `client.Get[User](url)` 写不出来。〔TBX04 p2-5〕

**绕法：把类型参数挪到一个辅助类型上**。类型参数不能挂在方法上，但能挂在结构体上，于是把"请求"做成一个泛型 builder，类型参数落在 builder 上：〔TBX04 p9-10〕

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

API 从想象中的 `client.Get[User](url)` 变成 `NewGet[User](client, path, decoder).Do()`，少一层优雅，但能编译。`dec` 是"拿到原始响应后怎么解析成 `T`"的回调，类型参数 `T` 落在了 `GetReq` 和 `NewGet` 上，绕开了方法不能带类型参数的限制。Java 背景会觉得别扭，因为这是过程式设计，`Client` 被降级成配置载体。

### 限制二：约束不能当类型声明

想限制一个接口只接受几种特定类型（比如 SQL Builder 的 `Select` 只收 `string` 和 `Aggregate` 两种列），有人会想用约束当参数类型：〔TBX04 p7-8〕

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

约束只能和类型参数一起用，不能独立当类型。所以"处理有限几种输入"这件事，泛型约束帮不上忙，长期看 `interface{}` 参数仍会留在中间件设计里。〔TBX04 p7-8〕

**绕法：标记接口**。用一个带私有方法的接口当"准入标记"：〔TBX04 p10-11〕

```go
type Selectable interface{ aggr() }

type Column string
func (Column) aggr() {}

type Aggregate struct{ fun, col string }
func (Aggregate) aggr() {}

func (s *Selector) Select(cols ...Selectable) *Selector
```

为什么不能用空接口？因为空接口所有类型都实现它，等于没拦。塞进一个未导出的方法 `aggr()`，只有包内实现它的 `Column`、`Aggregate` 能进来，外部无法冒充。代价是哪怕只用普通列，也得写成 `Select(Column("name"))`，不能再裸传字符串。

### 限制三：约束只能是接口，不能是结构体

业务里常见需求：ORM 的 `Insert` 只接受嵌入了 `BaseEntity`（共享字段和公共方法）的实体。自然会想写 `func Insert[E BaseEntity](e *E)`，让所有嵌入 `BaseEntity` 的实体通过，行不通：〔TBX04 p8-9〕

```text
in call to Insert, E (type User) does not satisfy BaseEntity
```

Go 的约束必须是接口，不能是结构体，这条路走不通。它堵死的是"共享字段"这条路，对业务开发的影响比中间件更大。〔TBX04 p8-9〕

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

约束写成 `Entity` 接口，`User` 通过嵌入 `BaseEntity` 拿到 `Id`/`SetId`，就能作为 `Insert[*User](u)` 的实参。代价是 Go 习惯直接访问字段，这里被迫给每个共享字段加一对方法，不优雅但管用。〔TBX04 p11-12〕

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
3. 三个限制：方法不能有类型参数（用 Builder 绕）、约束不能当类型声明（用标记接口绕）、约束只能是接口（用 Getter/Setter 接口绕）。
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

1. 不能。去掉 `~` 后约束只收底层类型恰好是 `int`/`float64` 的类型；`MyInt` 的底层类型是 `int` 但本身不是 `int`，进不去。`~int` 才把"底层类型是 int 的一切类型"框进来。
2. 因为 Go 的方法（有 receiver 的函数）本身不能带类型参数，这是语法限制，编译器报 `method must have no type parameters`；普通函数没有 receiver，可以带。
3. 用标记接口：`type Selectable interface{ aggr() }`，让 `Column` 和 `Aggregate` 实现 `aggr()`，`Select(cols ...Selectable)` 就能在编译期只收这两种。不能用空接口，因为它所有类型都实现。
4. `ReadAll(r io.Reader)` 更易读也不慢；`ReadAll[R io.Reader]` 多一层类型参数，代码更复杂，没有收益。对某个值做的全部操作只是调用它的一个方法时，就用接口，不用类型参数。

练习包 `exercises/p33gen/` 里有排序基准（`SortInts`、`SlicesSort`、`Specialized`）、`Set`、标记接口和编译错实拍，可以直接跑。

## 在项目里

这里没有现成的泛型声明：`dnsproxi-go` 没声明过类型参数，只在 `cache/evict.go` 里用到内建的 `min`。`cache/cache.go` 的三张表（`m`、`slot`、`access`）是"同一算法跑在不同值类型上"的候选，但它们的更新要在同一个函数里联动，抽成泛型收益有限，保持具体类型是合理取舍。

## 想深入去哪查

- 设计简史、type set、具化、使用建议：TBX05 全讲；三大限制与绕法：TBX04 全讲；类型系统与泛型的比较视角：TBX01。
- 旧长版：`notes/p33-generics-boundary.md`（排序三选手、编译错实拍）、`notes/p8-fast-track.md`（泛型函数、约束与 `~`、泛型的代价、`slices` 与 `cmp`）。
- 官方设计文档：Go 1.18 type parameters 提案（`43651-type-parameters.md`）。
