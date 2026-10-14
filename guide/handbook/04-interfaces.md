# 04 接口与组合：Go 用来代替继承的全套工具

> 详细程度：深入 · 约 30 分钟
> 材料：TB24 到 TB30 · 旧长版：`notes/p2-lang-core.md`、`notes/p30-methods-interfaces.md`
> 前置：你写过 Java/Kotlin/Rust/TS 中任意一门的面向对象或 trait 代码

## 这一节要建立的认知

Go 没有类，也没有继承。它把"面向对象"拆成三个互相独立的零件，再让你自己拼：

1. **类型加方法**：方法只是第一个参数叫 receiver 的函数，和类型之间没有层级。
2. **接口**：一组方法签名，**由使用方声明，实现方不用知道它存在**。
3. **嵌入**：编译器替你生成转发代码，不产生任何父子关系。

三者互相正交，这就是 Go 所谓的"组合"。后面所有套路（小接口、装饰器、中间件、依赖注入）都是在这三个零件上的用法。

## 和你已知语言的差异

| 问题 | Java / Kotlin | Rust | TS | Go |
|---|---|---|---|---|
| 类型怎么声明自己实现了接口 | `implements` 显式 | `impl Trait for T` 显式 | `implements` 可选，结构化 | 不声明，方法集合覆盖就算 |
| 接口谁来定义 | 通常实现方一侧 | trait 通常与类型同 crate | 使用方或提供方 | 惯例：**使用方** |
| 复用实现 | 继承、默认方法 | trait 默认方法、Deref | 继承、mixin | 嵌入（转发，不是继承） |
| 值还是引用决定能力 | 否 | `self`/`&self`/`&mut self` | 否 | 是：值类型和指针类型的方法集合不同 |
| 运行时多态 | 虚表 | `dyn Trait` 胖指针 | 原型链 | 接口值是（类型，数据）二元组 |

## 一、方法就是首参数为 receiver 的函数

`t.Get()` 只是 `Get(t)` 的另一种写法，编译器自动完成转换。看穿这一点，"值还是指针"就变成函数传参问题：值接收者拿到副本，指针接收者拿到地址。〔TB24 p6-8〕

```go
type T struct{ n int }

func (t T) Get() int   { return t.n }
func (t *T) Set(a int) { t.n = a }

set := (*T).Set // 方法表达式：func(*T, int)
get := T.Get    // func(T) int
var t T
set(&t, 3)
fmt.Println(get(t), t.n) // 3 3
```

## 二、选 receiver：先看接口，再看修改，最后看拷贝

三条原则有优先级，不要按出现顺序用。〔TB25 p2-6〕〔TB25 p10-11〕

1. **类型要实现某个接口**：这个接口需要的方法统一用同一种接收者，否则值类型会被排除在外（见下一节）。
2. **方法要改原实例**：指针接收者。
3. 以上都不是：默认值接收者，缩小外部修改内部状态的"接触面"；类型很大、拷贝贵时换指针接收者。

同一类型不要混用两种接收者，否则拿值类型的人会"少了一半方法"，且很难一眼看出来。

## 三、方法集合：接口满足的唯一判据

这是整节最值得背下来的规则。〔TB25 p7-10〕值类型 `T` 的方法集合只有值接收者的方法；指针类型 `*T` 的方法集合是两种接收者的方法之和；方法集合是接口方法集合的超集或相等，就实现了接口。

```go
type Iface interface{ M1(); M2() }
type Impl struct{}

func (Impl) M1()  {}
func (*Impl) M2() {}

var _ Iface = &Impl{} // 通过
var _ Iface = Impl{}  // 编译错
```

```text
cannot use Impl{} (value of struct type Impl) as Iface value in variable declaration:
Impl does not implement Iface (method M2 has pointer receiver)
```

为什么调用时 `t.M2()`、`p.M1()` 都能写，赋给接口却不行？因为调用时编译器自动取地址或解引用，只要变量可寻址；而赋给接口，要看类型本身的方法集合，没有"自动"。惯用的编译期断言就利用这一点：`var _ json.Marshaler = (*RawMessage)(nil)`。

## 四、接口由使用方定义，小接口

隐式满足带来一个设计上的反转：**接口应该写在用它的地方**，而不是写在实现它的地方。教材用下面的 `Save` 例子讲的是同一件事：函数只声明自己用到的能力。〔TB30 p6-8〕

```go
// 反例：依赖具体文件，难测试，签名锁死
func Save(f *os.File, data []byte) error

// 正解：只依赖唯一用到的能力
func Save(w io.Writer, data []byte) error {
	_, err := w.Write(data)
	return err
}
```

调用方自己决定落点：磁盘传 `*os.File`，单元测试传 `bytes.Buffer`，网络传自定义 Writer。可测性、接口隔离、可扩展三件事靠同一次修改同时获得。〔TB30 p7-8〕

小接口为什么好，给一个可数的理由：方法越少，能满足它的类型越多，抽象程度越高；极限是空接口，万物皆可装，但信息量为零。〔TB28 p10-13〕落地三步：先不管大小抽象出接口；按使用场合把大接口拆成小接口；再看能否拆到单方法。标准库的 `io.Reader` 也是后期才抽出来的，越偏业务的抽象越难一次抽对。〔TB28 p13-15〕

配套的创建原则：**接受接口，返回结构体**。〔TB30 p10〕`log.New(out io.Writer, ...) *Logger` 就是范本：参数放宽，返回值给调用方具体能力。

## 五、接口值在内存里是什么

接口变量存的是一个二元组：（类型信息，数据指针）。有方法的接口是 `iface{tab, data}`，其中 `tab` 指向一张"接口 x 动态类型"的绑定表；空接口是 `eface{_type, data}`。〔TB29 p1-9〕

这个结构同时解释三件事：

1. **动静兼备**：赋值时编译器静态检查方法集合；运行时记录真实类型，可以断言回来。〔TB29 p1-4〕
2. **相等**：两个接口值相等，要类型信息相同，且数据所指的值相同。`int(17)` 和 `int64(17)` 装箱后不相等。〔TB29 p9-10〕
3. **装箱有成本**：任何类型赋给接口，都可能分配并拷贝一份数据。性能敏感路径少装箱。只需要这个概念，不用背 runtime 结构体字段。〔TB29 p21-23〕

### 最经典的坑：nil 指针装进接口后不等于 nil

```go
func mayFail(bad bool) error {
	var p *MyErr // 类型 *MyErr，值 nil
	if bad {
		p = &MyErr{}
	}
	return p // 返回的接口：类型信息非空，数据为 nil
}

fmt.Println(mayFail(false) == nil) // false
```

接口等于 nil，要求类型信息和数据**都**是空。上面类型信息是 `*MyErr`，所以 `err != nil` 成立。修法：函数返回类型是 `error` 时，没有错误就直接 `return nil`，不要经过一个具体类型的变量。Rust 的 `Option<Box<dyn Error>>` 没有这个坑，因为"没有"在类型里；Java 的 null 引用语义单一，也不会"装箱后非 null"。〔TB29 p3-5〕〔TB29 p23-25〕

## 六、嵌入：编译器生成的转发，不是继承

在结构体里写类型名而不写字段名，就是嵌入。`s.Read` 被编译器改写成 `s.Reader.Read`；被嵌入的类型的方法"提升"进外层的方法集合，造成继承的错觉。本质是组合里的代理模式。〔TB26 p9-10〕

```go
type Base struct{ Name string }

func (b Base) Hello() string { return "hi " + b.Who() }
func (b Base) Who() string   { return b.Name }

type Child struct{ Base }

func (c Child) Who() string { return "child" }

c := Child{Base{"base"}}
fmt.Println(c.Hello(), "|", c.Who()) // hi base | child
```

`c.Hello()` 里调的仍然是 `Base.Who`：**没有虚调用**，被嵌入的类型不知道外层存在。想要"子类重写、父类方法里生效"，Go 不提供，要靠传接口参数。

嵌入后的方法集合有两条精确规则，也是常考点：〔TB26 p15-20〕

- 外层结构体嵌入 `T1` 和 `*T2`：`S` 的方法集合是 `T1` 的加 `*T2` 的；`*S` 的方法集合是 `*T1` 的加 `*T2` 的。
- `type S T` 是新类型，**不继承** `T` 的任何方法，方法集合为空；`type S = T` 是别名，与 `T` 完全相同。

测试里伪造依赖时，可以在结构体里嵌入接口，只覆写用到的方法，其余方法不实现也能编译；未覆写的方法被调用时会 nil 解引用 panic。〔TB26 p13-15〕

## 七、水平组合三式：包装器、适配器、中间件

嵌入是"器官"，接口参数是"关节"。水平组合的基本形态只有一种：函数参数写接口类型。〔TB30 p9-16〕三个衍生模式，复杂度递增：

1. **包装器**：`func Wrap(x I) I`，输入输出同接口，所以能链式叠加。标准库 `io.LimitReader(r, n)` 就是。
2. **适配器**：把"满足某签名的普通函数"变成"实现某接口的值"。`http.HandlerFunc` 是函数类型加一个调用自己的方法，是范本。
3. **中间件**：包装器加适配器。签名是 `func(http.Handler) http.Handler`，内部用 `http.HandlerFunc` 把匿名函数转型：函数体里先做自己的事（打日志、验签），再调用 `next.ServeHTTP(w, r)`。

最外层先跑，`logMW(authMW(h))` 先日志后鉴权。Java 对应 Servlet Filter，Rust 对应 tower 的 Layer。后面的分层、日志、限流、熔断都会用到这三个词。

## 八、空接口与 any

Rob Pike 的说法：空接口不提供任何信息，传 `any` 等于告诉编译器别检查了，错误推迟到运行时。标准库里合理使用的只有容器算法（`sort`、`container/heap`）和格式化日志（`fmt`、`log`）。〔TB30 p16-17〕Go 1.18 起 `any` 是 `interface{}` 的别名，泛型能做的优先用泛型。

## 判断题（先答再看）

1. 为什么 `var _ Iface = &Impl{}` 过而 `Impl{}` 不过，但 `Impl{}.M2()` 在变量可寻址时能调用？
2. `type S T` 和 `type S = T` 的方法集合差别是什么？
3. `c.Hello()` 为什么打印 `hi base`？想要 `hi child` 该怎么改设计？
4. 一个函数返回 `error`，里面有 `var p *MyErr` 并 `return p`，调用方 `err != nil` 为什么是 true？

练习包 `exercises/p30/` 里可以直接跑对答案：`TestMethodSetMatrix`（方法集合矩阵）、`TestTypeAliasMethodSet`、`TestEmbedNotInherit`、`TestNilErrorTrap`、`TestCapReaderChain`（包装器链）、`TestMiddlewareCompose`（中间件）。

## 在项目里

- **隐式满足 + 适配器**：`dnsproxi-go/tcp/server.go` 里的 `streamConn` 没有声明实现 `net.PacketConn`，只是实现了它的方法，就能被传给原本为 UDP 写的 `handleQuery`。同文件的 `Handler` 是函数类型，签名与 `handleQuery` 一致，直接传方法值即可。
- **包装器**：`dnsproxi-go/logger.go` 的 `ctxHandler` 内部持有一个 `slog.Handler`，在 `Handle` 里补充请求 ID 再转发，是装饰器的真实用例。

## 想深入去哪查

- 原材料：TB24（方法本质）、TB25（方法集合与选型）、TB26（嵌入）、TB28（接口即契约）、TB29（运行时表示）、TB30（组合与三式）。
- 旧长版：`notes/p2-lang-core.md`、`notes/p30-methods-interfaces.md`（含 itab 结构、装箱基准、反射）。
- 反射、`runtime` 结构体字段、`trace` 工具：不在这一节，遇到再查 `C:dachang/reflection`。
