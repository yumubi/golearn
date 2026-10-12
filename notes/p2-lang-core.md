# P2 · 语言核心：用 Rust 的经验写 Go，哪里需要换直觉？

> 读者：熟悉 Rust，有 Rust DNS 代理。目标：读懂小接口、区分嵌入与多态、
> 正确处理错误。前 15 分钟只需要看四个问题的答案和练习；细节查章末参考。
> 前置：P1.5（语法速通）。后续：P2.5 惯用法（把"能跑"升级成"地道"）、
> P3 上并发。

## 这章学完你能干什么

读懂任意 Go 工程里的接口分派和错误传播，并且能预言这段代码的行为：
包括 `speakAll([]Speaker{Dog{}, Robot{}})` 打印什么、嵌入后为什么不能当
父类用、`errors.Is` 顺着什么链找 sentinel。四个问题依次是：

1. 两个类型没写 `implements`，为什么能被同一个函数接收？
2. 嵌入后能调用方法，为什么仍不能当作继承？
3. 没有 Result 和 `?`，错误怎样保留上下文并被调用方识别？
4. 什么时候零值直接可用，什么时候必须初始化？

练习在 `exercises/p2lang/`（参考实现已写好），错误处理用
`exercises/p1tool/error_test.go` 支撑。读完你应该能预测练习输出、
再动手加一个满足 `Speaker` 的新类型。

## 问题 1：两个类型没写 implements，为什么能被同一个函数接收？

入口例子（`exercises/p2lang/lang.go`）：

```go
type Speaker interface { Speak() string }

type Dog struct{ Name string }
func (d Dog) Speak() string { return d.Name + ": woof" }

type Robot struct{ ID int }
func (r Robot) Speak() string { return fmt.Sprintf("robot-%d: beep", r.ID) }

func speakAll(ss []Speaker) string { ... }   // Dog 和 Robot 都能放进去
```

Dog 和 Robot 互相不认识，也没声明过 `impl Speaker for Dog`。Go 的规则是
**结构化满足**：一个类型的方法集覆盖接口的方法集，它就自动实现这个接口，
检查发生在编译期（传参、赋值给接口变量时）。Rust 的 trait 也可以这样
想，但 Rust 要求每个实现显式写 `impl` 块登记；Go 连登记都省了。

代价在哪？**方法必须定义在类型所属的包里**。这是 Go 的硬规则
（语言规范：方法的接收者基类型必须与该方法同包定义）。所以：

- 你不能给 `string` 加方法：它定义在别的包（语言内置）。这回答了
  "没有孤儿规则为什么还是不能给 string 加方法"：Go 没有 trait-impl
  分离，方法天生长在类型所在包，效果上比 Rust 的孤儿规则更严
  （Rust 还允许你给自己的 trait 实现到别人的类型上，Go 什么都不给）。
- "trait 定义在使用方"因此成了 Go 的默认惯例：`Searcher` 接口写在
  `internal/transport/http.go`，`service/search.Service` 根本不知道自己
  被谁抽象了。Rust 里你也可以这么干，但 Go 社区把它当默认，标准库
  全是 1-2 个方法的小接口。

**迁移误判点**：Rust 的方法集没有"值/指针"之分，Go 有。`T` 的方法集
只有值接收者方法；`*T` 的方法集两者都有。所以如果接口方法定义在
指针接收者上，`T` 值不满足接口，`*T` 才满足。这是 Go 新手编译错误
的前三名，记住"接口要求什么接收者，就传什么形态"。

**动态分派发生在接口上**：`s.Speak()` 通过接口调用时走 itab
（≈ trait object 的 vtable）。具体类型上的方法调用是静态绑定。
FAQ 原话：动态分派的唯一途径是 interface。

## 问题 2：嵌入后能调用方法，为什么仍不能当作继承？

```go
type Base struct{ Greeting string }
func (b Base) Hello() string { return b.Greeting }

type Greeter struct {
    Base   // 嵌入
    Count  int
}

g := Greeter{Base: Base{Greeting: "hi"}}
g.Hello()         // 能调：方法提升（promoted）
g.Base.Hello()    // 与上一行完全等价
```

`g.Hello()` 是**编译期转发糖**：编译器把它改写成 `g.Base.Hello()`，
接收者是 Greeter 里那个 Base 字段的拷贝。没有虚表，没有动态查找，
没有 override，Greeter 上不能重写 Hello 来拦截这次调用，因为根本
不存在"Greeter 的 Hello"这个方法，存在的是 Base 的。

两个直接后果：

- **嵌入不产生子类型**。`Greeter` 不能赋给 `Base` 形态的接口或变量；
  方法提升只让外层"能调到"，不让外层"是"内层。
- **需要多态时用 interface**。想让不同 Greeter 有不同 Hello？定义
  `Greeter` 接口，写不同实现。嵌入只管复用（has-a 的省略写法），
  不管多态（is-a 的替身）。

Rust 对照：`impl Deref` 可以模拟"外层自动获得内层方法"，但 Rust 官方
不鼓励；Java 的 `extends` 是真继承。Go 的答案是两者都不要：组合归
嵌入，多态归接口，别拿嵌入伪装继承。

自测：`type A struct { B }` 之后，`A` 能当 `B` 的接口用吗？不能。
A 的方法集里有提升来的 B 的方法，但接口检查看的是 A 自己的方法集
是否覆盖接口；A 不是 B，只是方法提升了。（练习 `TestEmbedding`
的断言就是这个。）

## 问题 3：没有 Result 和 ?，错误怎样保留上下文并被调用方识别？

Go 的 error 是个接口：`type error interface { Error() string }`。
错误就是普通返回值，`if err != nil` 是习惯不是语法。三个问题
对应三件工具，练习在 `exercises/p1tool/error_test.go`：

| 你要什么 | 工具 | Rust 对照 |
|---|---|---|
| 错误信息带上下文 | `fmt.Errorf("divide %d by %d: %w", a, b, ErrDivZero)` | `thiserror` 的 `#[error("...")]` + `#[source]` |
| 判断"是不是这个错误"（值比较） | `errors.Is(err, ErrDivZero)` | `matches!(e, DivError::Zero)` |
| 取出具体类型（拿字段） | `errors.As(err, &nf)` | downcast_ref |

分界线：**Is 按值比 sentinel，As 按类型取结构体**。两者都沿着
`Unwrap()` 链穿透（Go 1.20 起支持 `Unwrap() []error` 多叉树，
`fmt.Errorf` 也允许多个 `%w`），depth-first 遍历。

`%w` 是关键：它把被包装的错误链起来，`errors.Is` 才能认出 sentinel；
消息本身带上 a/b 上下文是给看日志的人的。P1 练习的断言明确拒绝
裸 sentinel：只有 sentinel 没有上下文，排查时你只知道"除了错"
不知道"哪个除法"。

**interface nil 的坑在这章讲**（error 是接口最常见的受害者）：

```go
func maybeFail(fail bool) error {
    var e *MyErr        // 具体类型指针，nil
    if fail { e = &MyErr{msg: "boom"} }
    return e            // ❌ fail=false 时返回的接口 != nil！
}
```

接口底层是 (type, value) 二元组。`return e` 把 `(type=*MyErr,
value=nil)` 装进接口，type 字段非 nil，接口判 `== nil` 为 false，
调用方 `err != nil` 中招。修法：nil 时显式 `return nil`，让 type
字段也是 nil（练习 `TestInterfaceNil` 的参考实现）。Rust 没有这个坑：
`Option<Box<dyn T>>` 把"没有"表达在类型里，不存在"装着 null 的
非空 fat pointer"。

## 问题 4：什么时候零值直接可用，什么时候必须初始化？

Go 每个类型天生有零值（规范保证：`false`/`0`/`""`/`nil`，递归
初始化到每个字段）。**零值可用**是 Go 的构造器哲学：

```go
var mu sync.Mutex      // 直接 Lock()，零值=未锁
var b bytes.Buffer     // 直接 WriteString，零值=空缓冲区 ready to use
var sb strings.Builder // 同上
```

Effective Go 的设计建议就是往这个方向凑：数据结构设计成"零值
即可用"，用户 `var v T` 就能开工，不需要 `NewXxx()`。这是为什么
Go 标准库大量 API 没有 Init、没有构造器。

什么时候零值**不**可用：

- 引用形态的类型（slice/map/channel）：零值 nil，能判等、能传参，
  但不能写入。`var m map[string]int` 然后 `m["k"]=1` 会 panic，
  要 `make`。不是"未初始化"，是"指向底层存储的指针还没分配"。
- 依赖不变量的结构体：比如 `time.Timer`、`http.Client` 的部分字段、
  带非零默认值语义的对象。文档明确说"零值不可用"或"必须 New"时
  听文档的。
- 练习里 `Join` 用零值 `strings.Builder` 直接拼，这是零值可用的
  正例；P15 的 `slog.NewJSONHandler` 必须传 writer 和 level，
  这是必须构造的反例（`NewLogger` 封装）。

判断口诀：**标准库文档说 "zero value ... ready to use" 就是零值可用；
要 make 的、要 New 的、文档要求 "initialize" 的就别省**。
拿不准时跑一下，panic 会教你。

## 动手：练习 p2lang（15-30 分钟）

```sh
cd /Users/akka/Dev/learning/go
go test ./exercises/p2lang/...        # 先跑一遍看全绿长什么样
go test -run TestEmbedding -v ./exercises/p2lang/   # 单跑某题
```

**预测题**（先写答案再跑）：

1. `speakAll([]Speaker{Dog{"rex"}, Robot{7}})` 输出什么？（§问题 1）
2. `Greeter` 嵌入 `Base` 后，改掉 `Base.Hello` 的实现，`g.Hello()`
   的结果变吗？`g` 能赋给 `Base` 类型变量吗？（§问题 2）
3. `maybeFail(false)` 返回的 err 是 nil 吗？为什么？（§问题 3）

**观察题**：打开 `lang_test.go` 的 `maybeFail`，删掉
`if e == nil { return nil }` 三行（回到 `return e`），再跑
`go test -run TestInterfaceNil ./exercises/p2lang/`。实测输出是
`want nil, got <nil>`：打印出来是 nil，`!= nil` 却为真，这就是
(type, value) 二元组的坑。看完用 `git checkout` 还原。

**改造题**：给 `exercises/p2lang/` 加一个 `Cat` 类型，只定义
`Speak() string`，让 `speakAll` 接受它。验收：`go vet
./exercises/p2lang/ && go test ./exercises/p2lang/` 全绿，
且你的测试断言了 Cat 在输出里。

配套练习地图（改造/预测时对照，不必逐个过）：

| 练习 | 考什么 | 在哪 |
|---|---|---|
| TestInterface | 隐式满足 + 接口动态分派 | p2lang |
| TestEmbedding | 方法提升 ≠ 多态 | p2lang |
| TestInterfaceNil | (type, value) 二元组坑 | p2lang |
| TestZeroValue | 零值 strings.Builder | p2lang |
| TestGenerics | 泛型函数语法 | p2lang |
| TestTypeSwitch | downcast 分支 | p2lang |
| TestErrorWrapping / TestErrorAs | %w + Is/As | p1tool |

## Take-away

1. 隐式满足 + 方法同包：接口定义在使用方，小接口优先；不能给别人的
   类型加方法，这是比 Rust 孤儿规则更严的结构。
2. 嵌入是编译期转发糖（静态提升），不是继承；多态只有 interface
   一条路。
3. error 是接口：上下文用 `%w` 包装，sentinel 用 `errors.Is`，
   类型提取用 `errors.As`；`return nil` 要显式，接口二元组不会
   替你判断"装着 nil 指针"。
4. 零值可用是设计契约：标准库 API 尽量让 `var x T` 直接能用；
   slice/map 要 make，文档要求 initialize 的听文档。

---

## 章末参考

### 方法集与指针接收者（精确规则）

语言规范：类型 `T` 的方法集 = 所有值接收者方法；`*T` 的方法集 =
值接收者 + 指针接收者方法。接口满足看的是方法集覆盖，所以：

- 接口方法要求指针接收者 → `T` 不满足，`*T` 满足。
- 值接收者方法 → `T` 和 `*T` 都满足（通过指针可解引用）。
- 调用侧比赋值侧宽松：可寻址的值 `x.M()` 能自动取址
  （`(&x).M()`）；map 元素、接口内的具体值不可寻址，只能调
  值接收者方法。这就是 `map[string]T` 的方法调用经常编译报错的
  根源。

练习里 `var _ json.Marshaler = (*RawMessage)(nil)` 这类断言
（Effective Go "Interface checks"）是编译期验证满足关系的惯用法，
迁移自 Rust 的 `impl Trait for Type` 不需要这个，因为 impl 块
本身就是登记。

### 嵌入的细节

- 字段也提升：`g.Greeting` 合法。
- 深度优先、浅层优先：外层自己定义了 `Hello` 就用外层的，遮蔽
  内层的；同深度两个嵌入有同名方法 → 选择子非法（编译错）。
- 指针嵌入 `*Base` 也提升，nil 时 panic。
- 接口可以嵌接口（`io.ReadWriter = Reader + Writer`），这是
  "Go 的默认方法"的替代品：没有 default impl，但接口组合能拼装。

### 泛型速查（对比 Rust）

| Rust | Go | 备注 |
|---|---|---|
| `fn max<T: Ord>(a, b)` | `func Max[T cmp.Ordered](a, b T) T` | 约束必须是 interface；`cmp.Ordered`/`comparable` 是预置 |
| `impl<T> Trait for T` | 不存在 | 无 impl 泛型，无特化；要么接口要么类型断言 |
| 关联类型 / GAT | 无 | interface 级泛型没有；接口方法不能带自己的类型参数 |
| `T::default()` | 零值 + `any` 约束自己写 | Go 泛型 1.18 才有，1.21 起 slices/maps 标准库 |
| 单态化（编译期展开） | 实现：GC shape stenciling + 字典 | 表现近似：泛型不装箱，接口装 |

老代码（ejdb 大量）`map[string]interface{}` 是泛型前写法；新代码
`any`（= `interface{}` 别名）+ 泛型。读老代码时见到类型断言链
多半是这个历史原因。

### type switch 与运行时扩展点

```go
var v any = ...
s, ok := v.(string)      // 单断言，ok=false 不 panic
switch x := v.(type) {   // downcast 分支，每个 case 自动收窄
case string: ...
case *MyErr: ...
default: ...
}
```

Rust enum sum type 做不到的"运行时扩展点"（外部类型塞进统一入口、
按能力分派）Go 靠 interface + type switch 做，MCP SDK 大量这样用。
代价是分派逻辑散在运行时，编译器不帮你查漏分支。

### 错误处理补充

- `errors.Join(errs...)`（1.20+）：包一个错误列表，格式是换行拼接；
  `Is`/`As` 会穿透。
- 判断 `context.DeadlineExceeded` 这类 sentinel 也走 `errors.Is`。
- panic 只用于程序员 bug（≈ Rust panic!），不可恢复错误不进 panic。
- 自定义错误类型建议实现 `Unwrap() error` 而不是埋字段，否则
  `Is/As` 断链。

### 零值与构造器惯例

- `var _ = assertable` 惯例、`NewXxx()` 返回 `*T` 是"零值不够用"
  的信号；文档样例都用 `NewXxx` 时别用 `var x T`。
- 零值可用是传递的：`SyncedBuffer{lock, buffer}` 两个字段零值
  都可用，整体就可用（Effective Go 原例）。
- `new(T)` vs `&T{}`：等价，字面量能带字段值，`new` 在 1.26 起
  能带初始化值。
