# 09 泛型：该用的地方，和绕不过的限制

> 详细程度：标准 · 约 25 分钟
> 材料：TBX04、TBX05、TBX01 · 旧长版：`notes/p33-generics-boundary.md`、`notes/p8-fast-track.md`
> 前置：04 接口与组合

## 这一节要建立的认知

泛型的核心是把具体算法抽象成通用算法，让算法和类型解耦：`Add` 不必为 `int`、`int64`、`byte` 各写一遍。〔TBX05 p2-4〕但 Go 把它当成"补一个缺口"的工具，而不是设计手段。

Go 迟迟不加泛型，不是没想到：Russ Cox 2009 年就指出三条路各有一个"慢"，当时只能三选一。〔TBX05 p5〕

| 路线 | 代价 |
|---|---|
| 不实现泛型 | 拖慢程序员，到处复制 `AddInt`、`AddInt64` |
| 每个类型实例化一份（C++ 模板） | 拖慢编译器，大量冗余代码 |
| 隐式装箱拆箱（Java 擦除） | 拖慢执行 |

最后落地的方案经历十多年打磨，2020 年 6 月 Wadler 等人的《Featherweight Go》给出形式化证明，才有了共识。〔TBX05 p6-8〕理解了这一点，就明白 Go 泛型为什么克制：它必须同时守住"简单"和"向后兼容 Go1"。〔TBX05 p4-5〕

## 一、语法速览：只看和你已知语言不同的

三个概念：**类型参数**是方括号里的类型占位符，**约束**规定类型实参必须满足什么，**具化**是把类型参数替换成实参。〔TBX05 p9-11〕和 Java 的 `<T extends X>`、Rust 的 `T: Trait` 对应，差别在约束的写法：约束就是接口，扩展后除方法集合外还能写**类型集合**，用 `|` 并列，`~` 表示"底层类型是它"。

```go
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

fmt.Println(Sum(1, 2, 3), Sum(1.5, 2.5), Sum(MyInt(1), MyInt(2))) // 6 4 3
```

类型实参通常能由编译器推断，不必写 `Sum[int](...)`。`MyInt` 能用，正是因为约束里写了 `~int`，没有波浪线就不行。〔TBX05 p11-12〕不满足约束时报错清楚：

```text
string does not satisfy Number (string missing in ~int | ~int64 | ~float64)
```

泛型类型同理，`type Set[T comparable] map[T]struct{}`；方法里可以直接用 `T`，但**方法自己不能再带类型参数**，下面会看到。〔TBX05 p14-15〕

## 二、三个绕不开的限制和对应的绕法

这三条来自对泛型中间件的一年多实践，是判断"这里能不能用泛型"的关键，其中限制一和限制三的报错我在本机 go1.26.4 上复现过，信息如下。〔TBX04 p2-5〕

### 限制一：方法不能有类型参数

```go
func (o *Orm) Get[T any](id int) T { ... }
```

```text
method must have no type parameters
```

接口或结构体本身可以是泛型，但把类型参数放在类型上，每个模型都要一个实例（`Orm[User]`、`Orm[Order]`）；放在方法上，直接编译不过。客户端类库（HTTP、Redis、ORM）最想要的 `client.Get[User](url)` 就写不出来。〔TBX04 p2-5〕

**绕法：Builder 加过程式入口**。把类型参数放到一个辅助类型上：

```go
type GetReq[T any] struct {
	c    *Client
	path string
	dec  func(string) T
}

func NewGet[T any](c *Client, path string, dec func(string) T) *GetReq[T] {
	return &GetReq[T]{c, path, dec}
}
func (r *GetReq[T]) Do() T { return r.dec(r.c.base + r.path) }
```

API 从 `client.Get[User]` 变成 `NewGet(client, path, decoder).Do()`，不够优雅但可用。〔TBX04 p9-10〕

### 限制二：不能用泛型约束表达"处理有限几种输入"

想让一个接口只接受几种特定类型，约束帮不上忙，长期看 `interface{}` 参数仍会出现在中间件设计里。〔TBX04 p7-8〕

**绕法：标记接口**。空接口不能当标记，因为所有类型都实现它，要在接口里至少放一个私有方法：

```go
type Selectable interface{ aggr() }
```

只有包内的 `Column`、`Aggregate` 实现 `aggr()`，外部就无法冒充。〔TBX04 p10-11〕

### 限制三：约束只能是接口，不能是结构体

想写 `func Insert[E BaseEntity](e *E)` 并让所有嵌入了 `BaseEntity` 的实体通过，行不通：

```text
in call to Insert, E (type User) does not satisfy BaseEntity
```

**绕法：Getter/Setter 接口**。把共享字段提取成方法，约束写成接口，配合嵌入一个 `BaseEntity` 避免每个实体重复实现。代价是 Go 习惯直接访问字段，这里却被迫加方法。〔TBX04 p8-9〕〔TBX04 p11-12〕

## 三、该用与不该用

适合：〔TBX05 p16-17〕

1. 函数操作 slice、map、channel，且不对元素类型做假设，可替代反射方案并更快。
2. 通用数据结构（有序 Set、堆、优先队列等），替换 `interface{}` 存储，免去类型断言，编译期全面检查。
3. 发现多次写仅类型不同的样板代码。

不适合：〔TBX05 p17〕

1. 对某类型的值只做一件事，就是调用它的一个方法，用接口。`func ReadAll(r io.Reader)` 是对的，写成 `func ReadAll[R io.Reader](r R)` 是错的：`io.Reader` 更易读也不慢。
2. 泛型让代码更复杂，就不要用。

先看标准库：1.21 起有 `slices`、`maps`、`cmp` 三个包，覆盖了大部分容器辅助函数，`go doc slices` 可看全部。这些函数就是泛型的最佳范例。

## 四、性能事实：不要背旧数据

课程里 Go 1.18 beta 的基准显示，泛型版 `slices.Sort` 比标准库 `sort.Ints` 快近一倍且零分配。〔TBX05 p15-19〕**1.26 修正**：`sort.Ints` 已经变成对 `slices.Sort` 的薄封装，本机源码 `$(go env GOROOT)/src/sort/sort.go` 里就是 `func Ints(x []int) { slices.Sort(x) }`。本机 go1.26.4 实测 10 万个 `int` 排序：

```text
BenchmarkSortInts     4840896 ns/op   802841 B/op   1 allocs/op
BenchmarkSlicesSort   4799854 ns/op   802816 B/op   1 allocs/op
BenchmarkSortSlice    8176885 ns/op   802882 B/op   3 allocs/op
```

前两个一致（每次 1 次分配是基准里克隆输入）；用比较闭包的 `sort.Slice` 慢约 1.7 倍，多两次分配。结论不是"泛型总是更快"，而是：**泛型 API 能让标准库以更快的专用实现替换内部**，调用方什么都不用改。数字随机器浮动，看相对差距。

## 常见坑

1. 想给方法加类型参数，编译报 `method must have no type parameters`。〔TBX04 p2-4〕
2. 约束里漏写 `~`，`MyInt` 之类自定义类型传不进去。〔TBX05 p11-12〕
3. 为了"通用"把只调用一个方法的函数写成泛型，反而更难读。〔TBX05 p17〕
4. 用空接口当标记。〔TBX04 p10-11〕
5. 还在背老基准结论。

## 判断题（先答再看）

1. `type Number interface{ ~int | ~float64 }` 里去掉 `~`，`MyInt` 还能调用 `Sum` 吗？
2. 为什么 `Get[T any](id int) T` 能作为普通函数，却不能作为方法？
3. 你要写一个只接受 `Column` 和 `Aggregate` 两种输入的接口，怎么用 Go 表达？
4. `ReadAll[R io.Reader]` 比 `ReadAll(r io.Reader)` 好在哪里，差在哪里？

练习包 `exercises/p33gen/` 里有排序基准（`SortInts`、`SlicesSort`、`Specialized`，文中的 `sort.Slice` 对照要自己加一个）、`Set`、标记接口和编译错实拍，可以直接跑。

## 在项目里

这里没有现成的泛型声明：`dnsproxi-go` 没有声明过类型参数，只在 `cache/evict.go` 里用到内建的 `min`。`cache/cache.go` 的三张表（`m`、`slot`、`access`）是"同一算法跑在不同值类型上"的候选，但它们的更新要在一个函数里联动，抽成泛型收益有限，保持具体类型是合理取舍。

## 想深入去哪查

- 设计简史、type set、具化、使用建议：TBX05 全讲；三大限制与绕法：TBX04 全讲；类型系统与泛型的比较视角：TBX01。
- 旧长版：`notes/p33-generics-boundary.md`（排序三选手、编译错实拍）、`notes/p8-fast-track.md`（泛型函数、约束与 `~`、泛型的代价、`slices` 与 `cmp`）。
- 官方设计文档：Go 1.18 type parameters 提案（`43651-type-parameters.md`）。
