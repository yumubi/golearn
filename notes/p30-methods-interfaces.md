# P30 · 方法、接口与组合

> 这章学完你能干什么：给任何类型选对 receiver，说清"为什么 *T 实现了接口而 T 没有"，用嵌入复用实现而不伪装继承，把胖参数收敛成 io.Writer 这样的小接口，并说清接口变量在运行时长什么样、装箱花在哪。前置：读过 P2（接口初见）与 P1.5（方法初见）。

## 体系背景：方法的三个约束

Go 没有类，也没有继承。类型加方法靠 receiver，而 receiver 把方法"焊"在三个约束上：基类型不能是指针或接口类型，方法必须与基类型同包，一个方法只能有一个 receiver。这三条决定了你只能在**自己的包里、自己的类型上**加方法，扩展第三方类型的路只有组合。整章的方法集合、嵌入、接口判定，都从这三条长出来。

P1.5 已讲过 receiver 的语法和"要改选指针"的口诀；P2 已讲过接口的结构化满足和嵌入的转发糖。这章不再重复那些例子，只补旧章没讲的判定规则、运行时结构和设计套路。旧章讲清的部分我会直接标注"见 notes/pXX §小节名"，占一行，不展开。

## receiver 是第一个参数

P1.5 §方法 给过选型口诀：要改接收者、结构体大、含锁，用指针。这个口诀好用，但它没回答两个问题：方法的类型到底是什么？为什么 `*T` 能调 T 的方法？

〔TB24 p2〕“Go 引入方法这一元素，并不是要支持面向对象编程范式，而是Go践行组合设计哲学的一种实现层面的需要。”

答案是把方法看穿：receiver 就是第一个参数。`(t T) Get() int` 等价于 `func Get(t T) int`，`(t *T) Set(a int)` 等价于 `func Set(t *T, a int) int` 〔TB24 p6-7〕。编译器在你写 `t.Get()` 时自动做这个转换，仅此而已。

等价变换立刻给出一个可验证的推论：方法可以赋给函数类型的变量，只要显式补上第一个参数。

```go
type T struct{ n int }

func (t T) Get() int      { return t.n }
func (t *T) Set(a int)    { t.n = a }

f1 := (*T).Set            // 类型是 func(*T, int)
f2 := T.Get               // 类型是 func(T) int
var t T
f1(&t, 3)
fmt.Println(f2(t), t.n)
```

```sh
$ go run .
3 3
```

〔实测〕 `go run ./exercises/p30/cmd/demo1` 输出 `3 3`，`%T` 打印 f1 为 `func(*main.T, int)`。

这种 `T.Get` 写法叫方法表达式。C++ 的 `this` 是编译器藏起来的，你写不出"把方法当函数用"的形态；Rust 的 `Type::method` 路径语法和 Go 几乎一致。

看穿本质还能解一个旧坑。〔TB24 p9-11〕 的原题：`[]field` 里迭代调 `go v.print()`（指针 receiver），课程年代（Go 1.21 前）输出三个 "six"。等价变换是 `go (*field).print(&v)`：传的是循环变量 `v` 的地址，goroutine 延迟执行时读到循环末尾的值。Go 1.22 起 range 变量每轮新建，这个坑已经不存在。〔实测〕 用三元素 `field` 切片验证：go1.21.5 下跑三次都是 `three three three`，go1.26.4 下是 `one three two` 乱序（每次跑都可能不同）：

```sh
$ GOTOOLCHAIN=go1.21.5 go run .   # go.mod 写 go 1.21
three
three
three
$ go run .                        # go.mod 写 go 1.26.4（本机 go1.26.4）
one
three
two
```

TestReceiverAsFirstParam 等练习里的 range 断言都按 1.22+ 语义写。

receiver 三条声明细则（基类型不能是指针/接口、必须同包、参数名不与形参冲突）各一行带过，编译器会拦，见 notes/p1_5-basics.md §方法 与 P1 工具链章的 vet 节。

## 方法集合：谁实现了接口

P2 §方法集 讲过单格报错：接口要指针 receiver 方法时传值编译不过。这节把它升级成一张完整的判定表，这是旧章没有的。

先有等价变换这个分析工具（把 `(t T) M1()` 看成 `F1(t T)`），再谈方法集合。任何类型都有方法集合这个"类型属性"：无方法的类型方法集合为空（int 和 *int 都是空），接口类型的方法集合就是它的方法列表 〔TB25 p8-10〕。

核心是不对称规则：

- `T` 的方法集合 = 所有 T-receiver 方法
- `*T` 的方法集合 = 所有 *T-receiver 方法 + 所有 T-receiver 方法

接口实现判定只有一句话：类型的方法集合覆盖接口的方法集合（相同或超集），就实现了接口。这就是为什么 `var _ json.Marshaler = (*RawMessage)(nil)` 能当编译期断言用。

原则一支撑这条判定：〔TB25 p4〕“如果Go方法要把对receiver参数代表的类型实例的修改，反映到原类型实例上，那么我们应该选择*T作为receiver参数的类型。” 原则二管修改之外的情况：〔TB25 p5-6〕“一般情况下，我们通常会为receiver参数选择T类型，因为这样可以缩窄外部修改类型实例内部状态的“接触面”，也就是尽量少暴露可以修改类型内部状态的方法。”

动手验证。设接口要 M1、M2，M1 用值 receiver、M2 用指针 receiver：

```go
type Iface interface{ M1(); M2() }
type Impl struct{}

func (Impl) M1()  {}
func (*Impl) M2() {}

var _ Iface = &Impl{} // 通过：*T 方法集合 {M1, M2}
// var _ Iface = Impl{} // 编译错：T 方法集合只有 {M1}
```

```sh
$ go vet .
# 注释打开时：
# ./main.go:12:15: cannot use Impl{} (value of struct type Impl) as Iface value in variable declaration: Impl does not implement Iface (method M2 has pointer receiver)
```

〔实测〕 `go test ./exercises/p30/ -run TestMethodSetMatrix -count=1` 四格矩阵全过。

选 receiver 有了判定表，选型就有了顺序。原课程三条原则，合并成一句决策树：**先看接口，再看修改，最后看拷贝**。〔TB25 p10-11〕“但实际进行Go方法设计时，我们首先应该考虑的是原则三，即T类型是否要实现某一接口。如果T类型需要实现某一接口的全部方法，那么我们就需要使用T作为receiver参数的类型来满足接口类型方法集合中的所有方法。”

1. T 需要实现某接口 → 该接口全部方法用 T receiver（否则 T 被排除在接口外）。
2. 不涉及接口、方法要改原实例 → `*T`。
3. 不改原实例 → 默认 `T`，缩窄修改面；类型大（拷贝贵）时换 `*T`。

反面清单照旧章纪律：同一类型别混用两种 receiver，混用后 T 大概率丢掉接口资格（上面 `Impl{}` 那格）。Rust 对照一句：方法调用时 rustc 会自动插引用/解引用（method resolution 的 autoref），表现上像"不用管值还是指针"，但 receiver 语义仍分 `self`/`&self`/`&mut self`，写 impl 时你选的那个就是"方法集合"的归属，Go 把这个选择暴露得更直白。

### 动手：M2 改成值接收者会怎样

把 `func (*Impl) M2()` 改成 `func (Impl) M2()`，T 与 `*T` 的方法集合都是 `{M1, M2}`，`Impl{}` 和 `&Impl{}` 都能赋给 `Iface`，四格全过：两个方法都在 T 的方法集合里，`*T` 又是超集。反方向才是坑：M1 或 M2 任何一个用 `*T` 接收者，`Impl{}` 那格就挂（上面 vet 实拍）。这个双向改动练习放 TestMethodSetMatrix。

## 嵌入：转发糖，不是继承

P2 §嵌入 已给结论：`g.Hello()` 是编译期转发糖，等价 `g.Base.Hello()`；没有 override，`Base.Hi` 内部调的还是 `Base.Name`。这节只补方法集合层面的精确规则，p2 只有一句话的那部分。

〔TB26 p9-10〕“从外部来看，这种嵌入字段的方法的提升就给了我们一种结构体类型S“继承”了io.Reader类型Read方法的实现，以及*MyInt类型Add方法的实现的错觉。”

提升的机制：调 `s.Read` 时编译器先查 S 自身方法，没有就查嵌入字段类型的方法，改写成 `s.Reader.Read`。被提升的方法进 S 的方法集合。嵌入接口时：〔TB26 p11〕“结构体类型中嵌入接口类型后，结构体类型的方法集合会发生什么变化呢” 答案是 T 与 `*T` 都获得接口的方法集合。嵌入结构体时的精确规则 〔TB26 p15-17〕“T1的方法集合包含：T1M1；*T1的方法集合包含：T1M1、PT1M2”：

- 结构体嵌入接口 I：T 与 `*T` 的方法集合都含 I 的方法集合。
- 结构体嵌入 T1 与 `*T2`：T 的方法集合 = T1 的 + `*T2` 的；`*T` 的方法集合 = `*T1` 的 + `*T2` 的。注意 `*T` 拿到的是 `*T1` 不是 T1（指针的方法集合包含值的方法集合，道理同 2 节的不对称规则）。

```go
type T1 struct{}
func (T1) M1()  {}
func (*T1) M2() {}

type T2 struct{}
func (T2) M3()  {}
func (*T2) M4() {}

type S struct {
	T1
	*T2
}
// S 的方法集合:  M1, M3, M4（+ 提升的 M2 属于 *T1，S 没有）
// *S 的方法集合: M1, M2, M3, M4
```

〔实测〕 `go test ./exercises/p30/ -run TestEmbedNotInherit -count=1` 验证上表四行。

两个旧章没提的边界：

- `type S1 = T` 别名与 T 方法集合完全相同（reflect 都分不出来）；`type S2 T` 新类型方法集合为空，接口关联一并清零。
- Go 1.14 只放宽了**接口嵌接口**的方法交集；结构体嵌入两个有交集的接口，选择子仍然 ambiguous。最小实验（`A interface{ M() }`、`B interface{ M() string }`，S 嵌入 A、B，调 `s.M()`）：

```sh
$ go vet .
vet: ./main.go:15:4: ambiguous selector s.M
```

同签名交集（`A interface{ M() }`、`B interface{ M() }`）也一样报 ambiguous，1.14 放宽只作用于接口嵌接口的类型定义，选择子规则没变。

- 一个测试妙用：`struct{ sql.Stmt }` 嵌入接口，伪对象只覆写要测的那一个方法就能通过编译，database/sql 的测试同款技巧。

Rust 对照一句：Rust newtype `struct S(T)` 同样不带 T 的方法，需要手动 impl 或 Deref；Go 的 defined 新类型行为一致，别名才同型。Kotlin 的 `by` 委托在"代理"这层像，但它有接口约束，Go 的嵌入连接口都可以不用。

## 接口契约：从语法到设计

接口语法、type switch、断言的 comma-ok，p2 §类型断言与泛型 已讲完，这里不复述。设计侧旧章只有结论，这节给方法。

〔TB28 p8〕“隐式契约，无需签署，自动生效……实现者只需要实现接口方法集合中的全部方法便算是遵守了契约，并立即生效了。” 接口是契约：类型作者不知道接口存在，使用方按需定义。这是 Go 与 Java 最大的分野，Java 的接口由提供方声明，类型必须显式声明"我实现了它"才能算数；Go 只看方法集合，实现了就自动生效。

小接口为什么好，给一个可数的理由：单方法接口的"可满足类型空间"大。一个 `interface{ Fly() }` 能被 7 种动物实现；加一个 `Swim()` 变成 FlySwimable 就只剩 4 种；极限是空接口，万物皆可装但信息量为零 〔TB28 p10-13〕。

先补一个空接口的定性：方法集合为空的接口就是空接口，任何类型都实现它 〔TB28 p5-6〕“这个方法集合为空的接口类型就被称为空接口类型”。

1. 先有接口，别管大小：先针对问题域抽象，io.Reader 也是标准库演进后期才抽出来的。
2. 按使用场合拆：大接口按"哪些方法总被一起用"自然分组，一组一个小接口。
3. 单一职责收尾：一个接口只干一件事，能否拆到单方法，看场合。

用旧章案例接一遍：P2.5 §使用方定义的接口 的 Lener 就是第 2、3 步的产物。毕业项目 dnsproxi 也有现成的：`dnsproxi-go/logger.go` 的 `newLogger(w io.Writer, level slog.Level) *slog.Logger`（L42）收的是标准库接口，测试（`logger_test.go` L15）传 `&bytes.Buffer` 就能拿到全部输出，磁盘、网络落点各选各的；`ctxHandler` 只包一层 `slog.Handler` 接口字段 `inner`（L40-42），包装器进构造器，与本章 §8 的 `logHandler` 同一词汇。dnsproxi 的 `cache/` 包反例：目前是具体类型组合、没有抽接口，哪天要换 LRU 或加 mock 就得先补这一刀。

空接口的纪律提前放这里：参数类型写 `any` 等于告诉编译器"别检查了"。标准库只有容器算法（container/heap、sort）和格式化日志（fmt）两类场景合理 〔TB30 p16-17〕。你的业务代码几乎总有更窄的契约可选。〔实测〕 旧课把 `interface{}` 称作"唯一泛型"，今天别这么叫：Go 1.18 起有真泛型（P8），`any` 只是 `interface{}` 的别名，两种写法拿到同一个 `reflect.Type`（`go run` 输出 `true`）：

```go
var a any = 1
var i interface{} = 1
fmt.Println(reflect.TypeOf(a) == reflect.TypeOf(i)) // true
```

"需要任意类型"优先考虑类型参数，空接口留给 fmt 这类真动态场景。

Rust 对照一句：Rust trait 定义在提供方、显式 impl，Go 接口定义在使用方、隐式满足；"接口越大抽象越弱"与 Rust 的 trait 分离原则同向。

## 接口的运行时：eface、iface 与装箱

P2 §体系背景 讲过接口值 16 字节、两个机器字。这节把那两个字拆开看，全部内容旧章没有。

运行时只有两种表示（`$GOROOT/src/runtime/runtime2.go`）：

```go
type eface struct { // interface{}（空接口）
	_type *_type
	data  unsafe.Pointer
}
type iface struct { // 有方法的接口
	tab  *itab
	data unsafe.Pointer
}
```

〔TB29 p5-6〕原稿贴的就是上面这段 runtime2.go 的 `iface`/`eface` 定义。

itab 是"接口 x 动态类型"的绑定表：`itab{inter *interfacetype, _type *_type, hash uint32, fun [1]uintptr}`。`fun` 是动态类型实现该接口各方法的函数地址数组，通过接口调方法 = 查 `fun` 再间接调用。itab 本体在 go1.26 已挪到 `$GOROOT/src/internal/abi/iface.go`（`type ITab struct`，L14 起），runtime2.go 里只剩一行别名 `type itab = abi.ITab`（L1129）〔TB29 p7〕 原稿贴的 `runtime2.go` 版本按 1.26 已过时。

先把 P2 的 nil 陷阱升级成可解释的形式。原稿 TB29 p4-5 的 returnsError：`*MyError` 初值 nil，bad() 为 false 时把 p 原样返回，调用者拿到的 error 不等于 nil。`*MyErr` 类型的 nil 指针装进 error 接口后，`iface.tab` 非空、只有 `data` 为 nil，与 `(nil, nil)` 的真 nil 不相等。P2 §接口里的 nil 给过 maybeFail 案例，这里换 returnsError 变体（接口字段版）做预测题，答案在练习 TestNilErrorTrap。

相等判定规则也顺理成章：两个接口值相等 = 类型信息（`_type` 或 `tab._type`）相同 且 data 所指数据相同。所以 `int(17)` 和 `int64(17)` 装箱后互不相等，类型信息那半就不同了。

装箱是性能账的源头。任何类型赋给接口变量都是装箱：按动态类型分配一块新内存、把值拷进去、填 `_type`/`tab` 与 `data`。汇编层面是 `CALL runtime.convT2E`（空接口）或 `convT2I`（非空接口）〔TB29 p19-21〕。两个直接后果：

- 箱内数据与原变量无关，装箱后再改原变量，箱里还是旧值。
- 每次装箱可能一次堆分配。常用类型有快速路径（convT16/32/64、convTstring、convTslice），255 以内的小整数走 staticuint64s 免分配。

bench 一下装箱的真实开销（本章实验之一）：

```sh
$ go test ./exercises/p30/ -bench BenchmarkBoxing -benchmem -count=1
BenchmarkBoxing/iface-10     221689994    5.446 ns/op    5 B/op    0 allocs/op
BenchmarkBoxing/direct-10    1000000000   0.3759 ns/op   0 B/op    0 allocs/op
```

〔实测〕 本机 Apple M4、go1.26.4，每次跑数字都浮动，量级才是结论：iface 个位数 ns 对 direct 亚 ns。0 allocs 不是"没分配"：`i % 1000` 有 744/1000 的值超出 staticuint64s（0..255 的小整数静态表），那部分每轮真分配 8 字节箱。allocs/op 按平均次数取整，0.744 舍成 0。想看到 1 allocs/op，把装箱值全部抬到 256 以上即可：

```sh
$ go test ./exercises/p30/ -run NoSuchTest -bench BenchmarkBoxAll -benchmem -count=1 -benchtime=2s
BenchmarkBoxAll/above255-10    349087693    6.739 ns/op    8 B/op    1 allocs/op
BenchmarkBoxAll/in255-10       1000000000   1.748 ns/op    0 B/op    0 allocs/op
```

对照这两行：值全在 255 以内时 1.7 ns（查表免分配），全超出时 6.7 ns 加真分配。p8 §泛型的代价 已给过 sort.Slice（反射）对 slices.SortFunc（泛型）的 1.3 到 1.4 倍实测，同源问题，不再复测。

Rust 对照一句：Rust 的 `Box<dyn T>` 胖指针也是"数据指针 + vtable 指针"，但它由借用检查器管生命周期；Go 的接口值是值语义，装箱拷贝，不追踪生命周期。`Option<Box<dyn T>>` 把"没有"写进类型（p2 已写），Go 靠约定显式 `return nil`。

## Save 反例：小接口怎么落地

设计原则一句话带过（详见 MATERIALS 索引的 W21-1 行）：接口有成本，一个函数够用就别包接口；组合是 Go 的骨架，正交性是判断标准。

正题是这条链：胖签名怎么一步步瘦下来。

```go
// 反例：三宗罪
func Save(f *os.File, data []byte) error
// 1. 难测试：必须真实磁盘文件 + 读回校验 + 清理
// 2. 违反接口分离：*os.File 带着用户永远用不到的 Chdir/Chown
// 3. 死扩展性：加"存网络"要改签名，牵连所有调用者
```

正解是把参数收到唯一用到的方法上：

```go
func Save(w io.Writer, data []byte) error {
	if _, err := w.Write(data); err != nil {
		return err
	}
	return nil
}
```

〔实测〕 `go test ./exercises/p30/ -run TestSaveBuffer -count=1`：Save 的测试用 `bytes.Buffer` 跑，无磁盘 IO。〔TB30 p7-8〕

收敛后每个调用者自己选落点：磁盘给 `*os.File`，测试给 `bytes.Buffer`，网络给自定义 Writer。签名从"我要一个文件"变成"我要一个能 Write 的东西"，ISP（接口分离）和可测性是同一次修改带来的。

配套创建模式记一句：接受接口，返回结构体。标准库范本 `log.New(out io.Writer, ...) *Logger`、`bufio.NewWriterSize(w io.Writer, size int) *Writer`。接口进、结构体出，调用方拿到具体类型，能力不被锁死。P2.5 §返回具体类型 讲过"进松出严"，这里是它在构造器上的应用。

Rust 对照一句：Rust 同样 `fn save(w: &mut impl Write)`；差别在 Go 返回结构体后接口字段是隐式满足的，不需要为新类型再 impl 一遍。

## 包装器、适配器与中间件

水平组合的基本模式只有一行：`func F(param 接口类型)`，接口参数当"关节"。三个衍生模式按复杂度排：

**包装器（装饰器）**：`func YourWrapper(param I) I`，返回值与参数同接口类型，所以能链式。

```go
type CapReader struct{ r io.Reader }
func (c *CapReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	for i := range n {
		p[i] = byte(unicode.ToUpper(rune(p[i])))
	}
	return n, err
}
func NewCapReader(r io.Reader) io.Reader { return &CapReader{r: r} }
// CapReader(io.LimitReader(r, 4)) 读 "hello" 得 "HELL"
```

〔实测〕 `go test ./exercises/p30/ -run TestCapReaderChain -count=1` 输出断言 `HELL`，三层链（CapReader + LimitReader + 计数 Reader）顺序无关地叠加。〔TB30 p11-12〕

**适配器函数类型**：把"满足某签名的普通函数"变成"实现某接口的值"。`http.HandlerFunc` 是唯一范本 〔TB30 p14〕“莫过于我们在第21讲中提到过的http.HandlerFunc了。这里，我们再来看一个应用http.HandlerFunc的例子”：

```go
type HandlerFunc func(ResponseWriter, *Request)
func (f HandlerFunc) ServeHTTP(w ResponseWriter, r *Request) { f(w, r) }

http.Handle("/greet", http.HandlerFunc(greetings)) // 普通函数适配为 Handler
```

Java 没有这种"类型即适配器"的写法（要匿名类或方法引用）；Go 用一个定义在标准库里的函数类型，零样板把函数接进接口体系。

**中间件** = 包装器 + 适配器：`func(h http.Handler) http.Handler`，内部用 `http.HandlerFunc` 把匿名函数转型。执行顺序：外层先跑。`logHandler(authHandler(greetings))` 先日志后鉴权。

```sh
$ curl -s localhost:8080/greet
bad auth param
$ curl -s -H "auth: 123456" localhost:8080/greet
Welcome!
```

〔实测〕 `go run ./exercises/p30/cmd/mw` 后 curl 实拍，输出与上面一致。〔TB30 p15-16〕

这节最后的 "设计速查" 提一行：面向接口、DI、单一职责、CQRS、开闭、装饰器洋葱模式等是这套语法的工程化应用，正文不展开，索引见 MATERIALS.md P30 段（W21-1 全组）。P35/P36/P38 的限流、otel 链路中间件、日志装饰器都会以本章这三个模式为词汇。

## trace 工具与反射实操

两块旧章全空白的实战，合成一节。手写 JSON 双向序列化已按审稿意见降级为提：reflection 单元有完整的 Marshal/Unmarshal 教学实现（序列化〔C:dachang/reflection/json.go:16-186〕、反序列化〔C:dachang/reflection/json.go:189-307〕，要点：Unmarshal 必须传指针，`typ.Kind()!=Ptr` 直接报错），本章只取它的反射 API 触点，完整库不进正文，见 MATERIALS.md P30 段。

**trace 工具**：用"方法值 + defer"做函数调用链跟踪。最简实现四行：

〔TB27 p1-3〕“我们具体是怎么用defer来实现函数执行过程的跟踪呢？这里，我给出了一个最简单的实现”

```go
func Trace(name string) func() {
	println("enter:", name)
	return func() { println("exit:", name) }
}
func foo() { defer Trace("foo")(); bar() }
```

`defer Trace("foo")()` 的求值时机是注册时：enter 立刻打印，exit 在函数返回后由 LIFO 兜底。四点不足催生完整工具：自动函数名（runtime.Caller(1) + FuncForPC）、Goroutine ID（runtime.Stack 解析栈头）、缩进层次（map[gid]int + Mutex）、批量注入（go/parser 解析 AST 后插 defer 语句，幂等）。AST 注入与 gid 都只用于调试和代码理解，不能上生产。本章练习 TestTraceInjection 用手写 defer 版（不依赖 AST 工具本体）。

**反射 API 主线**：先立代价。〔C:dachang/reflection/反射语法大全.md:2-14〕“我强调一下反射的2个弊端：1.代码不易阅读，不易维护，容易发生线上panic2.性能很差，比正常代码慢一到两个数量级” 两条线：

Type 管"类型信息"：

```go
t := reflect.TypeOf(&User{})  // *User
t.Elem()                      // User：指针 Type 必须 Elem() 才有字段和方法名
t.NumField(), t.Field(i).Tag  // 结构体字段与 tag
```

〔实测〕 `go test ./exercises/p30/ -run TestReflectAPI -count=1`：Elem 前后 Name/Kind 断言、CanAddr 五连测。摘自〔C:dachang/reflection/reflection.go:14-45〕。

Value 管"取值改值"，写入只有一个前提：

```go
var i int = 10
valueI := reflect.ValueOf(&i)
valueI.Elem().SetInt(8) //必须是可寻址的，才能调用Set进行修改
```

取值改值（struct 字段、slice 元素、string）与 map/切片边界的完整代码在原稿 change_value/change_slice 两个函数〔C:dachang/reflection/reflection.go:217-273〕。

下面是反射调函数〔C:dachang/reflection/reflection.go:345-427〕 的 call_function，Add(a,b int) 返回和：

```go
sumValue := valueFunc.Call(args) //返回[]reflect.Value，因为go语言的函数返回可能是一个列表
if typeFunc.Out(0).Kind() == reflect.Int {
	sum := sumValue[0].Interface().(int) //从Value转为原始数据类型
	fmt.Printf("sum=%d\n", sum)
}
```

Map 永远不可寻址但 `SetMapIndex` 可用；切片 `Index(i)` 可寻址。这些边界记在练习注释里，正文不铺开。

Rust 对照一句：Rust 没有字段级运行时反射，serde 靠编译期过程宏生成代码，属性信息在编译期消费；Java 反射全能但慢。Go 反射是"慢但可用"的中间态，性能敏感路径避开（详见 p8 泛型的代价一节的实测）。

## 小结

带走这五条：

1. 方法就是首参数为 receiver 的函数，方法表达式 `T.Get`/`(*T).Set` 是这本质的直接暴露。
2. 方法集合判定接口实现：`*T` 的集合含 T 的全部方法，T 只有 T-receiver 的。选型顺序：先接口、再修改、后拷贝。
3. 嵌入是编译期转发加方法集合并入，`*T` 拿的是 `*T1` 的方法集合；defined 新类型方法集合为空。
4. 接口变量 = (类型信息, 数据指针)，装箱有分配成本，nil error 陷阱是"类型字非零"的副产品。
5. 设计三件套：参数收到最小接口（Save 正解）、接受接口返回结构体、包装器/适配器/中间件是一族。

## 章末参考

### Rust 对照表

| Go | Rust | 备注 |
|---|---|---|
| receiver 选 T/*T | `self`/`&self`/`&mut self` | Go 值/指针是两个类型、方法集合不同；Rust 靠 autoref 统一调用面 |
| `T.Get` 方法表达式 | `Type::method` 路径 | 二者都把方法当函数用 |
| 嵌入方法提升 | 无；newtype 不带方法 | 复用靠 trait 默认方法或 Deref |
| 隐式接口满足 | 显式 `impl Trait for T` | Rust 提供方定义 trait，Go 使用方定义接口 |
| eface/iface 装箱 | `Box<dyn T>` 胖指针 | Go 拷贝值、不追踪生命周期；Rust 借用检查 |
| 无字段级反射 | serde 编译期宏 | Go 反射运行时可用但慢 1 到 2 个数量级 |
| 中间件 `func(h) h` | trait 对象包装/闭包层 | 洋葱模式两边同构 |

### 速查

- 方法集合：T = T-receiver；*T = 全部。接口实现 = 方法集合覆盖。
- 编译期断言：`var _ Iface = (*T)(nil)`。
- 别名 `type A = B` 同方法集合；defined `type A B` 空。
- 装箱路径：convT2E/convT2I，小整数免分配（staticuint64s）。
- 写值前提：CanAddr + CanSet；map 只能 SetMapIndex。

### 深读

- 原材料单元：TB24 到 TB27（方法三讲 + trace 实战）、TB28 到 TB30（接口三讲）、C:dachang/reflection（反射与手写 JSON，本章降级为提）。
- 旧章交叉：p2 §方法集/§嵌入/§接口里的 nil、p1_5 §方法、p2_5 §使用方定义的接口/§小接口/§返回具体类型。
- Go FAQ: Why is my nil error value not equal to nil?

## 练习：p30iface

先跑全绿：

```sh
$ go test ./exercises/p30/ -count=1
ok  	golearn/exercises/p30	0.4s
```

**预测**（先写答案再跑）：

1. TestMethodSetMatrix：接口要 M1（值接收者）+ M2（指针接收者），`Impl{}` 哪几格挂？把 M2 改成值接收者、让四格全过，再改 M1 试试反方向。
2. TestNilErrorTrap：接口字段里装了一个 `*MyErr` 类型的 nil 指针，`err == nil` 是 true 还是 false？为什么？
3. TestTypeAliasMethodSet：`type S1 = T` 与 `type S2 T`，谁的变量能赋给 `Iface`？

**观察**：

4. TestBoxingCost：跑 `-bench BenchmarkBoxing -benchmem`，对比 iface 与 direct 两组的 ns/op 与 allocs/op。
5. TestEmbedNotInherit：把 S 里 `*T2` 改成 `T2`，`*S` 的方法集合少哪个方法？

**改造**：

6. TestCapReaderChain：给链再加一层 TrimSpace Reader，输入 `" hello "` 断言链尾输出仍是 `HELL`（大写后去空白），再换 `"  ok  "` 断言输出 `OK`。
7. TestMiddlewareCompose：调换 logHandler 与 authHandler 顺序，用表驱动断言"未认证请求是否还被记录"。
8. TestHandwrittenJSON：把 Marshal 换成只支持两个字段的小实现，往返一个 struct 并断言非指针反序列化报错。

测试名：TestReceiverAsFirstParam、TestMethodSetMatrix、TestEmbedNotInherit、TestTypeAliasMethodSet、TestNilErrorTrap、TestBoxingCost、TestCapReaderChain、TestMiddlewareCompose、TestHandwrittenJSON、TestTraceInjection、TestReflectAPI。

配套小测试：TestSaveBuffer（Save 的 `bytes.Buffer` 版，正文 §Save 实测行）。
