---
title: P33 · 泛型的边界
---

# P33 · 泛型的边界


> 这章学完你能干什么：说清 Go 为什么拖了十年才加泛型（三个 slow 的取舍），读得懂约束接口的类型集合语义（type set、`~int | ~int32`、具化两阶段），背得出三大局限的报错原文（方法不能带类型参数、约束不能当类型用、约束只能是接口）和三条绕开路（Builder、标记接口、Getter/Setter），并用 1.26 实测判断什么时候值得用（排序三选手已无差，容器与算法才是主场）。前置：P28（方法、接口语法）。

## 动机与迟迟不加：三个 slow 的取舍

泛型多年霸榜官方用户调查"最想要特性"。泛型编程的中心思想：对具体高效算法做抽象得到通用算法，与不同数据表示结合，算法与类型解耦实现复用。`Add(a, b int32)` 要支持 int64 得复制出 AddInt/AddInt64/AddByte，泛型版 `func Add[T constraints.Integer](a, b T) T` 一份通吃；排序、查找、树、图这些算法与链表、栈、队列这些容器，缺泛型更痛。无泛型时的替代品 interface{} 有两大不足：没有类型安全检查、性能有损。〔TBX05 p2-4〕

那为什么迟迟不加？Go FAQ 给过三个理由：不紧迫，非早期设计目标（早期优先级是规模化、可读性、并发性）；与"简单"哲学有悖（新语法加运行时复杂度）；一直没找到价值足以抵消复杂性的理想方案。〔TBX05 p4-5〕
"理想方案"难产的原因，Russ Cox 2009 年就写透了（泛型窘境）：三条路各有一个 slow，拖慢程序员（不实现，重复写 AddInt）、拖慢编译器（C++ 方案，每类型一份实现）、拖慢执行性能（Java 方案，隐式装箱拆箱），三者如 CAP 不可兼得。〔TBX05 p5〕 设计史从 2010 Type Functions 演进到 2020.6 Wadler《Featherweight Go》形式化证明、2020.11 方括号定稿，此处只记结论。〔TBX05 p5-8〕

对照三条路的当代答案：Rust 单态化（同 C++ 族，编译期生成，冷编译热运行，二进制膨胀）；Java 擦除（运行期共享，编译期检查，装箱开销）；Go 取中间（GC shape 分组共享代码加字典）。〔TBX04 p5〕
Rust 的 trait bound 表达力最强（关联类型、where 子句、常量泛型），但也没有 Go 这种类型列表约束。

预测：Go 1.18 发布时（2022.3）泛型版排序比 sort.Ints 快近一倍，这个优势到 1.26 还在吗？先猜，下一节实测。

## 性能：排序三选手，1.18 的差距已被抹平

1.18beta2 时代的三选手基准（TBX05 原文）：标准库 sort.Ints 12407700 ns/op 1 alloc，泛型 slices.Sort 6961381 ns/op 0 alloc，手写特化 IntSort 6881815 ns/op 0 alloc。当年泛型比标准库快近一倍且零分配。〔TBX05 p15-19〕
本机 1.26 重跑 10 万元素（M4 实测）：

```text
BenchmarkSortInts-10     3919986 ns/op
BenchmarkSlicesSort-10   4505858 ns/op
BenchmarkSpecialized-10  4126259 ns/op
```

三个数字挤在 4 ms 附近，1.18 的"快近一倍"到 1.26 已被抹平：标准库排序这些年被持续优化，泛型版不再有数量级优势。当年编译器比 1.17 慢约 15%，官方承诺后续版本摊薄，1.26 也已兑现。〔TBX05 p15-19〕 泛型实现用的是 GC shape stenciling（形状相同的类型共享一份代码加字典），在二进制大小与性能间折中，这正是三 slow 的 Go 式答案。

观察：把 `makeSrc()` 里 100000 改成 1000 再跑，三选手差距是否还是抹平的？小数据集下排序算法本身（pdqsort）主导，泛型不背锅。

## 语法核心：type set、约束、具化

语法三件套按 1.18 定稿讲。类型参数：在函数声明、receiver 或类型定义中声明的占位符，方括号括起，不支持变长；`GenericFoo[P aConstraint](x, y P)` 里 P 是类型形参，具化时被类型实参替换。〔TBX05 p9-11〕

约束用 interface 定义，扩展后既可声明方法集合也可声明类型列表：

```go
type C1 interface {
	~int | ~int32
	M1()
}
```

`~int` 是近似元素，匹配底层类型为 int 的所有命名类型；类型列表用 `|` 分隔。T1 自定义类型 `type T1 int` 实现 M1 后满足 C1；底层 struct{} 的类型虽实现 M1，底层类型不符，编译报 `T does not implement C1`。官方建议：做约束的接口与做传统接口的接口分开定义。〔TBX05 p11-12〕

这背后是 type set 模型（2021.4 取代 type list）：每个类型都有 type set，非接口类型仅含自身；普通接口是无限集合（实现其方法的类型）；约束接口的 type set 是类型列表与方法集合的交集运算，嵌入接口取交集。T 实现 I 当且仅当 T 属于 I 的 type set。〔TBX05 p8-9〕 Rust 的 trait implementors 集合是编译器内部概念，Go 把"类型的集合"提升为一等语法（联合、交集、近似），独此一家。

调用分两阶段：具化（工厂比喻：接单 `Sort[book]`，检查约束，生成 `booksort`）与调用（与普通函数无异）；类型实参自动推导让多数调用写成 `Sort(bookshelf)` 不带方括号。〔TBX05 p12-14〕 类型也能带参数：`type Vector[T any] []T`，先具化再使用（`Vector[int]{1,2,3,4}`）。〔TBX05 p14-15〕

实验两例（exercises/p33gen）：Set[T comparable] 五操作（NewSet/Add/Remove/Exists/Len，Go 1.24 泛型容器样例同构）；AbstractHeap[K, V] 泛型堆（实现 container/heap 接口，orderBy 函数决定序，`heap.Push`/`heap.Pop` 直接可用）。约束误用编译错 1.26 实拍：

```text
./e3b.go:8:8: cannot use type T outside a type constraint: interface contains type constraints
```

约束 `C interface{ ~int | ~string }` 不能当普通类型声明变量或别名，它是集合不是类型。

预测：`Set[int64]` 与 `Set[int32]` 共享同一份运行时代码吗？提示：GC shape 按指针形状分组，两种整数形状相同。

## 三大局限与绕开：ORM 视角

**局限一：接口与结构体的方法不能有类型参数**。接口可以是泛型（`type Orm[T any] interface{ Insert(data ...T) }`），方法不行：类型参数放接口层则每种模型一个实例（Orm[User]、Orm[Order]，模型数等于实例数，不可接受），放方法上编译报错。〔TBX04 p2-4〕 1.26 实拍两个报错：

```text
./struct_method.go:5:22: syntax error: method must have no type parameters
./generic_method.go:5:7: interface method must have no type parameters
```

影响面：客户端类应用（Redis/Kafka 客户端、HTTP、ORM）最想要的 `Get[T any](url string) (T, error)` 方法级泛型 API 全部无法编译。〔TBX04 p4-5〕 Rust 侧同类需求用 trait 关联类型或泛型方法表达，不受此限。绕开一 Builder 模式：HttpClient 配置载体加 `GetBuilder[T any]` 链式设置 Path、Do 返回 T，把理想 API `client.Get[User]("/user/123")` 变成过程式链（Rust 的 builder 惯用法同构）。〔TBX04 p9-10〕

**局限二：泛型约束不能当类型用**。报错 `interface contains type constraints` 见"语法核心"一节实拍；推论是无法用约束表达"接口只接受有限几种类型"，interface{} 参数仍将广泛存在于中间件设计。〔TBX04 p7-8〕 绕开二标记接口：Go 不能用空接口当标记（所有类型都实现它），必须至少声明一个私有方法限定实现者集合：

```go
type Selectable interface{ aggr() }

func (Column) aggr()    {}
func (Aggregate) aggr() {}
```

代价是纯字符串列也要 `Select(Column("name"), Avg("age"))` 包一层，换得编译器类型检查（对比 interface{} 运行期 panic）。〔TBX04 p10-11〕 exercises/p33gen/agg_test.go 可跑：`TestSelect` 断言 `name, avg(age)` 拼接。

**局限三：约束只能是接口，不能是结构体**。想让实体组合 BaseEntity 共享字段（`func Insert[E BaseEntity](e *E)`）不支持，1.26 实拍报错：

```text
./sc4.go:9:12: struct{ID int64} does not satisfy BaseEntity
./sc3.go:11:12: in call to Insert, E (type MyEntity) does not satisfy BaseEntity
```

（报错信息不准确，没说 BaseEntity 只能是接口。）堵死共享字段的泛型方法道路，对业务开发影响大于中间件。〔TBX04 p8-9〕 绕开三 Getter/Setter：字段提升为方法凑约束（`type Entity interface{ GetID() int64; SetID(int64) }`），配合组合免重复实现；Go 习惯直接访问字段，这写法 Java 背景会眼熟，不够优雅但少量可用。〔TBX04 p11-12〕 exercises/p33gen/builder_test.go 的 `TestGetterSetterConstraint` 可跑。

三大限制 1.26 全部未放开。三思路效果不错但不优雅；大明对泛型普及偏悲观，甚至预判有公司会禁用泛型。〔TBX04 p12-13〕

## 适用判断：该用与不该用

适合场景四条：函数操作 slice/map/channel 等容器且不对元素类型做假设（替代反射拿性能）；通用数据结构（链表、二叉树，Go 无原生支持，替换 interface{} 存储，编译期全面静态检查）；不同类型有共同方法且实现相同时用类型参数；多次写仅类型不同的样板代码时收敛。〔TBX05 p16-17〕

不该用的反例：对某类型值的全部操作只是调用一个方法时用 interface，`func ReadAll(r io.Reader)` 正确，`func ReadAll[reader io.Reader](r reader)` 是错误做法，io.Reader 易读高效。〔TBX05 p17〕 官方节奏：先重复再抽象，第一次写样板就上泛型大概率是滥用。

对照：Rust 社区同样按动态/静态分派选 dyn Trait 还是 impl Trait，ReadAll 反例对应 impl Read 过度泛型化；Java 擦除反而鼓励接口编程。改造：审查你项目里一个 `func F(v interface{})` 加类型断言的函数，改成 `func F[T ...](v T)` 对比调用方手感与 allocs/op；若只调用一个方法，维持 interface。

1.26 回看大明的悲观预判：标准库 slices、maps 已全面泛型化，容器与算法层泛型是默认选择；业务层（ORM 实体、HTTP handler）依旧克制，局限三的 Getter/Setter 约束在开源 ORM 里也少见。中间件可用泛型提供用户友好 API，内部实现收益有限。〔TBX04 p12-13〕

## 小结

- 三个 slow（程序员/编译器/执行）不可兼得：Go 选 GC shape 折中，Rust 单态化，Java 擦除。〔TBX05 p5〕
- 排序三选手 1.18 差近一倍，1.26 已抹平（本机 3.9-4.5 ms 挤在一起）；泛型主场是容器与算法，不是微优化。〔TBX05 p15-19〕
- 约束即 type set：`~int | ~int32` 近似元素加方法集合，具化两阶段，自动推导。〔TBX05 p8-14〕
- 三大局限（方法不能带类型参数、约束不能当类型、约束只能是接口）1.26 未放开，报错原文背下来省调试时间。〔TBX04 p2-9〕
- 适用判断一条主线：容器抽象与算法收敛用泛型，单方法调用维持 interface。〔TBX05 p16-17〕

## 在 dnsproxi 里看这一章

dnsproxi 是"该用与不该用"的正反样本（工作副本实读）：

- 现成例子只有一处借用泛型标准库的边角：cache/evict.go:32 的 `k = min(k, n)`（1.21 内建泛型 min），以及全库依赖 `wire`/`slices` 等泛型化标准库，但自身没有声明过一个类型参数。
- 该泛型而没泛型的点：cache.Cache 的三张表 `m map[Key]*Entry`、`slot map[Key]int`、`access map[Key]uint64`（cache/cache.go:54-58）加 `removeSlot` 的 swap_remove 三表联动（cache/evict.go:55-65），是"同一算法跑在不同值类型上"的教科书场景，值得抽 `indexTable[K comparable, V any]`；没抽的代价是三个 delete 加一次 slot 修正手写在同一函数里。
- 不该泛型的点也现成：`handleQuery` 对 query 的全部操作是 `wire.ParseName`/`ParseQuestion` 两个具体函数调用（main.go:262 起），按 ReadAll 反例维持具体类型，套 `[Q any]` 纯属噪音。

## Take-away

预测：`Set[int64]` 与 `Set[int32]` 是否共享代码？观察：`go build -gcflags="-m"` 下泛型函数具化有没有额外输出？改造：把 agg_test.go 的 Selectable 标记接口换成 interface{} 加类型断言，对比编译期检查的损失。

## 章末参考

- 语法速查：`[T constraints.Integer]`（现行写法多用 `cmp.Ordered`）、`type Set[T comparable] map[T]struct{}`、`any` 是 `interface{}` 的别名（1.18 起）。
- 编译错速查（1.26 实拍）：`interface method must have no type parameters`；`method must have no type parameters`；`cannot use type T outside a type constraint: interface contains type constraints`；`does not satisfy BaseEntity`。
- 延伸：type parameters 提案 43651；Go FAQ generics；golang.org/x/exp/constraints 已转正为 cmp（1.21+）。
- 旧章交叉：方法表达式（`T.Get` 把方法当函数用）在 p30 §receiver 是第一个参数 有实义（p28 未写出，旧章无泛型收敛内容）；b.Loop 与 -benchmem 属 p8 §benchmark 与 pprof（p21 旧章无 bench 内容）。
- 原材料单元：TBX05（泛型总览）、TBX04（三大局限与绕开）、C:go_basic/v24（Set 与 b.Loop）。
- C:go_frame/data_structure 泛型容器合集：二分 K1/堆 K4/链表 K6/ring K8/快排 K9/Jaccard K10，索引 K2/K3/K7/K11；C:dachang/algorithm：AbstractHeap K1、优先队列 Item[T] K14。

## 练习：p33gen

先跑全绿：

```sh
$ go test -race -count=1 ./exercises/p33gen/
ok  	golearn/exercises/p33gen	1.3s
```

6 个测试各考一件事：

- `TestSet`：语法核心一节的泛型容器五操作（1.24 Set 样例同构）。
- `TestAbstractHeap`：语法核心一节的泛型堆接 container/heap（orderBy 决定序）。
- `TestBinarySearch`：适用判断一节的泛型二分（多类型约束同一实现）。
- `TestGetterSetterConstraint`：三大局限一节的局限三绕开（Getter/Setter 接口）。
- `TestConstraintNotType`：三大局限一节的局限二对照（约束可作约束不可作类型）。
- `TestSelect`：三大局限一节的局限二绕开（Selectable 标记接口拼 SQL 列）。
- 基准 `BenchmarkSortInts`/`BenchmarkSlicesSort`/`BenchmarkSpecialized`：性能一节的三选手，本机 1.26 已无差。
- 编译错复现：正文语法核心与三大局限两节的四个报错都有对应注释掉的源码行，解开注释跑 `go build` 看原文。
