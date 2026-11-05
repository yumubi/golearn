---
title: 04 方法、接口与组合：从调用行为到运行机制
---

# 04 方法、接口与组合：从调用行为到运行机制


> 详细程度：深入 · 约 190 分钟
> 材料：TB24 到 TB30 · 旧长版：`notes/p2-lang-core.md`、`notes/p30-methods-interfaces.md`
> 前置：写过一种语言的函数、结构体或类，了解基本指针与接口概念

## 从类型的方法，走到可组合的能力

给用户发欢迎通知，最初可以直接调用邮件发送代码；需要短信时，我们希望保留欢迎流程，只替换发送方式。写出一段数据也有相似的需求：文件、网络连接和内存缓冲区不是同一种类型，但调用者可能只关心它能不能写入字节。本章用通知串起接口的使用，再用写入场景看清真实的接口边界。不是所有差异都该抽象成接口；这里要解决的是调用者确实需要在不同实现之间保持同一套调用方式的问题。

Go 没有 Java 那样的类声明和类继承树。它把数据放在类型里，把操作通过方法绑定到类型上，再用接口表达调用方需要的能力。结构体嵌入承担一部分纵向组合，接受接口参数的函数承担横向组合；这两者不等于继承。方法因此不只是面向对象语法的缩写，它也是组合设计在类型系统中的支点。〔TB24 p2〕

接下来的路径是连续的：先声明并调用方法，再让 Email 和 SMS 共同参与 Welcome 流程；随后把方法调用还原成参数传递，解释值、指针和方法集合；最后检查接口值与 nil、嵌入、包装器和中间件。方法承载的执行逻辑仍可按函数调用理解，不需要另造一套参数传递规则。反射在本章只用作观察方法的工具，完整反射与 trace 工具留在后续专题。

## 一、先给类型添加方法

### 读取一个值，与改变原来的值

发送通知之前，先用一个计数器观察方法的基本行为。它只有一个整数字段，读它和改它的区别不会被网络调用掩盖。这是一个可以独立运行的程序：

```go
package main

import "fmt"

type Counter struct {
    value int
}

func (c Counter) Value() int {
    return c.value
}

func (c *Counter) Add(n int) {
    c.value += n
}

func main() {
    var c Counter
    c.Add(3)
    fmt.Println(c.Value()) // 3
}
```

func 与方法名之间多出的括号是接收者段，其中的 c 是接收者参数，英文叫 receiver。Value 的接收者类型是 Counter，Add 的接收者类型是 *Counter。调用时，接收者写在点号左边：c.Value()、c.Add(3)。方法不是写在结构体声明内部的成员，而是在同一个包中单独声明，再通过接收者与类型关联。〔TB24 p2-3〕

Go 的参数按值传递，接收者也一样。调用 Value 时传入 Counter 的副本；调用 Add 时传入指针的副本，这个指针仍指向原计数器，所以 c.value += n 改的是原值。如果把 Add 的接收者改成 Counter，增加的就只是方法里的副本。这里的“副本”不是深拷贝：结构体中若含有 slice、map 或指针，复制这些字段后仍可能访问同一份底层数据。因此值接收者不等于不可变或线程安全。〔TB25 p2-4〕

调用也能写成 Counter.Value(c) 和 (*Counter).Add(&c, 3)。此时接收者显式排到第一个参数的位置。第三节会用这套写法分析 goroutine 接收到的究竟是值还是地址，现在先把它当作同一个调用的另一种表示。〔TB24 p6-8〕

### 一个方法声明里，哪些部分与函数相同

标准库 net/http 的 TLS 服务启动方法使用同样的结构。下面摘出声明签名，并非一个可单独编译的完整实现：

```go
func (s *Server) ListenAndServeTLS(certFile, keyFile string) error
```

普通函数的 func、名字、参数列表、返回值列表、函数体五个部分仍然存在，只在名字之前增加了接收者段，成为六个部分。除接收者之外，其他部分的形式与语义与函数一致：方法名以大写字母开头时可导出，参数列表可以有变长参数，返回值也可以具名。这里 receiver 参数 s 的类型是 *Server，因此这项声明使用的是指针接收者，不能据此说 Server 值的方法集合已经包含 ListenAndServeTLS。〔TB24 p2-3〕

接收者写成 T 或 *T 时，其中的 T 都叫接收者基类型。Counter.Value 的基类型是 Counter，(*Counter).Add 的基类型也仍是 Counter。一个方法只能声明一个接收者参数，而且它不能是变长参数；普通参数列表可以变长，不代表接收者段也可以。〔TB24 p3〕

### 基类型的限制，不是禁止指针接收者

接收者必须基于本包定义的类型 T，写成 T 或 *T；T 本身不能是指针类型或接口类型。这条限制检查的是基类型，而不是看到接收者段中有星号就拒绝。Counter 是结构体，所以*Counter 完全合法。下面两个类型定义各自合法，但接在后面的两项方法声明都有意违反接收者限制；放进 main 包核对错误时，需要导入 fmt 和 io。〔TB24 p4〕

```go
type MyInt *int

func (r MyInt) String() string { // 有意错误：基类型的底层类型是指针
    return fmt.Sprintf("%d", *(*int)(r))
}

type MyReader io.Reader

func (r MyReader) Read(p []byte) (int, error) { // 有意错误：基类型是接口类型
    return r.Read(p)
}
```

MyInt 不是“指向一个合法基类型的接收者”，而是自己定义成了指针类型；MyReader 则定义成接口类型。两者都不能绑定这样的新方法。go1.26.4 编译器先从接收者类型剥掉外围指针，再检查得到的基类型及其底层类型；对应错误包含 invalid receiver type 和 pointer or interface type。MyReader 的方法体也不是一个可用的转发包装器：它在这里根本无法通过接收者检查，不能靠重复调用同名方法实现接口委托。〔TB24 p4〕

### 只能在定义类型的包里添加方法

基类型和方法声明必须位于同一个包，不要求同一个文件。于是既不能直接给预声明的 int、float64 等类型加方法，也不能到自己的包中给 http.Server 加方法。map 等未命名类型也不能直接拿来作为接收者基类型。下面分别是有意不能编译的声明；第二个片段依赖 net/http 导入：〔TB24 p5〕

```go
func (i int) Foo() string { // 有意错误：int 不是本包定义的类型
    return fmt.Sprintf("%d", i)
}
```

```go
func (s http.Server) Foo() {} // 有意错误：Server 定义在 net/http 包
```

这两处错误的关键措辞是 cannot define new methods on non-local type。它约束方法的归属：第三方不能通过另一个包，悄悄给既有类型追加方法。需要扩展时，可以定义本包的新类型，或把现有值放进本包的包装结构体，再为这个新类型声明方法。比如下面的定义基于 int，却获得了本包自己的类型名：〔TB24 p5〕

```go
type Count int

func (c Count) IsZero() bool { return c == 0 }
```

这里必须区分类型定义与别名。type Count int 定义了新类型；type Count = int 只是为 int 起另一个名字，并不能绕过非本包类型的限制。新定义类型也不会自动继承来源类型的方法，第三节会给出验证。封闭的是可直接声明的方法归属；嵌入还能使既有方法被提升，但那是组合规则，不是跨包添加声明。

### 接收者名字与参数共享方法体作用域

接收者参数、普通参数和具名返回值变量的作用域都覆盖方法体。它们不是各占一个独立命名空间，因此非空白的名字不能在方法签名里重复。下面的 t 同时作为接收者名和普通参数名，有意不能编译：〔TB24 p3-4〕

```go
type T struct{}

func (t T) M(t string) {} // 有意错误：t 重复声明
```

把任意一处改成不同的名字就能消除冲突。旧课程把错误写作 duplicate argument t，当前编译器的诊断可能写作 t redeclared in this block，并指向另一个声明；需要理解的是同一签名里的名字冲突，而不是背某个版本的报错句子。具名返回值也不能再取同一个 t。

方法体完全不使用接收者时，可以只保留类型，省略参数名。以下是前一段声明的替代写法，不要与它同时声明同名 M：〔TB24 p4〕

```go
type T struct{}

func (T) M(t string) {}
```

省略名字不代表取消了接收者，也没有把方法变成普通函数；它仍属于 T，只是方法体不需要引用接收者的值。日常方法多数要访问实例状态，所以通常仍保留一个简短名字。对于同一个基类型，非空白方法名还必须唯一；结构体的非空白字段名与方法名也不能相同。这些命名限制与接收者参数是否省略是两回事。

### 同一个值方法，可以从值或指针调用

下面是独立的调用示例。M 使用值接收者，既可以从 T 变量调用，也可以从 *T 变量调用：〔TB24 p5-6〕

```go
package main

type T struct{}

func (t T) M(n int) {}

func main() {
    var t T
    t.M(1)
    p := &T{}
    p.M(2)
}
```

第二次调用不会把值接收者改成指针接收者。p 指向一个 T，调用 M 时取出该 T 的值，传入的仍是副本。后面会看到，*T 的方法集合本来就包含值接收者方法；而反方向调用指针方法，需要额外检查值是否可寻址。这使我们能先按方法是否需要修改实例来设计接收者，而不把“点号左边碰巧写了什么类型”当成方法归属的判断依据。

## 二、接口：让同一段欢迎逻辑使用不同的发送方式

注册成功后，我们要给用户发送一条欢迎消息。先只有邮件，可以直接让函数接收 Email。下面的 Send 只打印消息，帮助观察调用过程，不连接邮件服务器，也没有实现真实发送和发送失败处理。

```go
type Email struct{}

func (Email) Send(message string) {
    fmt.Println("发送邮件：", message)
}

func Welcome(sender Email) {
    sender.Send("欢迎注册")
}
```

这个片段需要导入 fmt。调用 Welcome(Email{})，会打印“发送邮件： 欢迎注册”。后来我们增加短信：

```go
type SMS struct{}

func (SMS) Send(message string) {
    fmt.Println("发送短信：", message)
}
```

SMS 同样提供 Send(string)，但此时写 Welcome(SMS{}) 仍然不能编译。Welcome 要求的是 Email，SMS 和 Email 是两个不同的具体类型；同名、同签名的方法不会把两个具体类型变成同一个类型。再写 WelcomeSMS 可以解决这一次调用，却复制了并未改变的欢迎逻辑，以后增加站内信还要继续复制。

观察 Welcome 的函数体：它不读取邮件地址，不连接邮件服务器，只调用一次 Send。它真正需要的不是“一个 Email”，而是“一个支持 Send(string) 的值”。把这个要求写成接口，就能让参数表达业务真正依赖的能力。

```go
type Sender interface {
    Send(message string)
}
```

Sender 是一个接口类型。声明里不写 func，也不写方法体，只写方法名、参数列表和返回值列表。这里约定的方法名为 Send，接收一个 string，没有返回值。接口描述怎样调用，不负责实现发送过程。〔TB28 p3〕将 Welcome 的参数改成 Sender，函数体保持不变。下面是这一阶段的完整程序：

```go
package main

import "fmt"

type Sender interface {
    Send(message string)
}

type Email struct{}

func (Email) Send(message string) {
    fmt.Println("发送邮件：", message)
}

type SMS struct{}

func (SMS) Send(message string) {
    fmt.Println("发送短信：", message)
}

func Welcome(sender Sender) {
    sender.Send("欢迎注册")
}

func main() {
    var sender Sender = Email{}
    Welcome(sender)

    sender = SMS{}
    Welcome(sender)
}
```

这段程序的预期输出是：

```text
发送邮件： 欢迎注册
发送短信： 欢迎注册
```

main 里只有一个 sender 变量，它声明时的类型一直是 Sender。第一次赋值保存 Email 值，第二次改为 SMS 值。两次都进入同一个 Welcome；执行 sender.Send 时，前一次调用 Email.Send，后一次调用 SMS.Send。Welcome 决定“发什么”，具体实现决定“怎么发”。这就是这里的多态：同一段业务代码通过接口使用不同实现。增加站内信时，只要新类型满足 Sender，Welcome 本身不必修改。

### 实现关系由方法集合决定，不靠 implements 声明

Email 和 SMS 都没有写“实现 Sender”，甚至可以先定义它们，再由使用方定义 Sender。Go 检查的是方法集合：对这里仅包含方法要求的普通接口，如果类型 T 的方法集合包含接口 I 要求的全部方法，也就是等于或大于这个方法集合，T 就实现了 I，T 的值可以赋给 I 的变量，也可以传给接收 I 的函数。这个关系称为隐式实现。〔TB28 p5,8〕T 和 *T 的方法集合可能不同，下一部分再具体求它们的集合；现在先把实现条件和调用语法分开。

“隐式”不等于“运行时试试看”。给 Sender 赋值或给 Welcome 传参时，编译器已经检查实现关系；不满足就不能编译。只匹配方法名还不够。例如保持 Sender 不变，把 Email.Send 改为下列版本，再传入 Email{} 就会失败：

```go
// 用来替换前面的 Email.Send；这是有意制造的签名不匹配。
func (Email) Send(message string) error {
    return nil
}
```

新方法的签名是 Send(string) error，接口要求的是 Send(string)。多出一个返回值，即使调用方愿意忽略它，也不算满足接口。反过来，Email 额外提供 Close() 不会妨碍它实现 Sender，因为接口要求的 Send 仍然存在；但 Welcome 中的 sender 静态类型是 Sender，不能仅因某次保存 Email 就直接调用 sender.Close()。调用方能直接使用的能力，以接口声明为准。

签名里的参数名只是给人看的。Send(message string)、Send(s string) 和 Send(string) 表达相同要求，具名返回值的名字也不参与匹配。将同一规则用于多个方法，可以写成下列声明；此片段需要导入 io：

```go
type MyInterface interface {
    M1(int) error
    M2(io.Writer, ...string)
}
```

M1(a int) error、M1(n int) error 都与 M1(int) error 相同；M2(w io.Writer, strs ...string)、M2(w io.Writer, args ...string) 都与 M2(io.Writer, ...string) 相同。参数和返回值的类型、顺序以及是否为可变参数才影响签名；参数名和返回值名不影响。一个类型必须满足这里的两个方法，才能作为 MyInterface 使用。〔TB28 p3〕

### 同名方法：显式重复与嵌入交集不是同一条规则

接口中的方法必须有名字。直接在同一接口中声明两个 M1，即使参数名不同，甚至想用不同参数类型来“重载”，也会触发 duplicate method。Go 不提供这种按参数列表区分同名方法的重载。下面是独立的、故意不能编译的声明：

```go
type Duplicate interface {
    M1(int) error
    M1(a int) error
}
```

接口还可以嵌入其他接口：裸写接口名，把它的方法要求合并进来。Go 1.14 起允许被嵌入接口的方法集合相交，但交集中的同名方法必须有相同签名。下面也是故意不能编译的例子：

```go
type Interface1 interface {
    M1()
}

type Interface2 interface {
    M1(string)
    M2()
}

type Interface3 interface {
    Interface1
    Interface2
    M3()
}
```

编译器对这组嵌入冲突报 duplicate method M1。冲突来自 Interface1.M1() 与 Interface2.M1(string)：名字相同，参数列表不同，触发报错的是同一个方法名下的两个不同签名；M3 与冲突无关。把 Interface2 中的方法改成 M1() 后，两份相同要求可以合并，Interface3 的方法集合是 M1、M2、M3；不是见到重复的名字就报错。这个放宽从 Go 1.14 开始，早于它的版本不允许这类嵌入交集。显式写两次 M1 仍然不合法，不要用嵌入规则解释直接重复声明。〔TB28 p3–4〕

### 接口也能包含非导出方法

Send 首字母大写，因此包外也能使用。接口中的方法不必全部导出。标准库 context 用下面这个接口描述能够被直接取消的 context；它的实现包括 *cancelCtx 和*timerCtx：

```go
// Go 1.26.4，src/context/context.go；这是源码片段，不是独立程序。
type canceler interface {
    cancel(removeFromParent bool, err, cause error)
    Done() <-chan struct{}
}
```

Done 是导出方法，cancel 是非导出方法，放在同一个接口中完全合法。标准库中这类带非导出方法的接口例子并不多，日常实际编码极少使用，了解即可。这样的接口通常连自身也不导出，服务于包内协作，而不是给包外实现者提供公共扩展点。非导出方法的身份包含所属包：另一个包自行声明一个同拼写的 cancel，并不等于 context 包的 cancel。这里的“通常包内使用”是使用惯例，不是“接口只要含小写方法就不能导出”的语法限制。〔TB28 p4–5〕

<details>
<summary>源码版本说明：cancel 为什么比旧资料多一个参数？</summary>

TB28 p4 展示的是 cancel(removeFromParent bool, err error)。Go 1.26.4 的 context/context.go:417–420 中加入了 cause，因此本章按现行签名展示。旧签名用于理解非导出方法仍然合适，但不能当作现行源码逐字复制。方法签名变化也再次说明：签名匹配看完整的参数与返回值，不只看名字。


</details>

### 没有方法要求时，任何类型都能放进去

再把方法集合的规则推到零个方法：

```go
type EmptyInterface interface{}
```

这个接口的方法集合是空集合，因此任何类型都满足它。不必专门声明 EmptyInterface，通常直接使用 interface{}。Go 1.18 起还可以使用 any，它是 interface{} 的预声明别名，不是另一种新接口类型，也不是关键字。〔TB28 p5–6,18〕下面的连续赋值都合法：

```go
// 放入 main 中即可观察；最后的 _ = i 只是让片段可以独立放进函数。
var i interface{} = 15
i = "hello, golang"

type T struct{}
var t T
i = t
i = &t
_ = i

var a any = i
_ = a
```

同一个 i 可以保存整数、字符串、结构体值和指针；变化的是它保存的具体值，不是 i 的静态类型。泛型语法加入之前，interface{} 常被用来保存通用数据结构里的不同类型元素，避免为每种元素复制一套容器代码。这个历史作用不等于现代泛型：空接口不承诺元素有哪些操作，读取后往往需要断言；类型参数则能让同一算法保留元素类型之间的约束。能明确需要 Send 时应接收 Sender，而不是先接收 any 再运行时寻找 Send。

接口类型和其他类型一样可以声明变量。例如 var err error、var r io.Reader，尚未显式初始化时都为 nil。要赋予非 nil 值，右侧类型仍须实现接口：任何类型可以赋给 any，不代表任何类型都能赋给 error 或 io.Reader。〔TB28 p5〕

### 类型断言：取回具体值，或者检查另一种能力

把 Email 放进 Sender，是让调用方只依赖 Send。有时拿到一个接口值后，还需要知道里面实际保存的是什么。类型断言写成 i.(T)，其中 i 必须是接口类型的表达式。它并不是把任意值转换成 T；它检查接口里已有的动态类型和值。以下先用空接口装入 int64(13)，方便把成功和失败分开看。代码放在 main 中，需要导入 fmt：

```go
var a int64 = 13
var i any = a

v1, ok := i.(int64)
fmt.Printf("v1=%d, the type of v1 is %T, ok=%t\n", v1, v1, ok)

v2, ok := i.(string)
fmt.Printf("v2=%s, the type of v2 is %T, ok=%t\n", v2, v2, ok)

v3 := i.(int64)
fmt.Printf("v3=%d, the type of v3 is %T\n", v3, v3)
```

按语言规则，三行输出分别为：

```text
v1=13, the type of v1 is int64, ok=true
v2=, the type of v2 is string, ok=false
v3=13, the type of v3 is int64
```

目标 T 是具体类型时，断言要求 i 的动态类型恰好是 T，不是“数值能转换成 T”就算成功。成功时 v 的静态类型是 T，值为原来保存的值；失败时 comma-ok 形式返回 ok=false，v 仍是 T 类型，值为 T 的零值。所以 v2 是空字符串，不会由于失败变成 any，也不会由于 i 曾保存 int64 而变成整数。即使改成 i.(int)，这里也会失败：int64 和 int 不是同一个类型。〔TB28 p6–7〕

单值形式 v := i.(T) 没有失败结果可供处理，失败就 panic。将下一段接到前面的代码后，程序会在第一句中止，后面的 Printf 不执行：

```go
// 故意触发运行时错误，单独验证这一段。
v4 := i.([]int)
fmt.Printf("the type of v4 is %T\n", v4)
```

这里装的是 int64，而不是 []int，会产生 interface conversion 一类的 panic。对于不受控制的输入，应使用 comma-ok 分支；只有程序本身能保证成功、失败确实代表程序约束被破坏时，才考虑单值形式。nil 接口没有动态类型，断言也不会成功：comma-ok 返回目标零值与 false，单值形式 panic。〔TB28 p6–7〕

断言目标也可以是接口，这时问题从“是不是这个具体类型”变成“保存的具体类型是否还满足这份接口要求”。回到通知程序，若 var i any = Email{}，那么 sender, ok := i.(Sender) 成功，sender 的静态类型是 Sender，动态类型是 Email，可以通过 Sender 调用 Send。下面保留一个独立的多方法命名练习，完整展示成功、失败以及两种类型信息的差别：

```go
package main

import "fmt"

type MyInterface interface {
    M1()
}

type T int

func (T) M1() {
    fmt.Println("T's M1")
}

func main() {
    var t T
    var i any = t

    v1, ok := i.(MyInterface)
    if !ok {
        panic("the value of i is not MyInterface")
    }
    v1.M1()
    fmt.Printf("the type of v1 is %T\n", v1)

    i = int64(13)
    v2, ok := i.(MyInterface)
    fmt.Printf("ok=%t, the type of v2 is %T, nil=%t\n", ok, v2, v2 == nil)
    // v2 = 13 // 故意取消注释会编译失败：int 不实现 MyInterface。
}
```

预期先打印 T's M1，然后打印 the type of v1 is main.T，最后一行是 ok=false, the type of v2 is <nil>, nil=true。v1 和 v2 的静态类型从声明起都确定为 MyInterface，并不随断言成功或失败而改变。成功时 v1 这个接口值保存的动态类型是 main.T；失败时 v2 是 MyInterface 的零值，也就是没有动态类型的 nil 接口。fmt 的 %T 对接口参数报告动态类型，因此成功时显示 main.T，失败时显示 <nil>，它并不能用来证明变量的静态类型。〔TB28 p7–8〕

可以用编译器看到另一面：不论 v1 还是 v2，都不能写成 v1 = 13 或 v2 = 13，因为 int 没有 M1，不能赋给 MyInterface。并且断言成功并不会把 MyInterface 中没有的方法开放给 v1。断言表达式 i.(MyInterface) 的静态类型就是 MyInterface；这条规则成功与失败一致。这里必须区分“断言表达式的类型”和“接口内保存的动态类型”，不能把 %T 的显示结果当成前者。

还有一个编译期边界：若 i 的静态类型是 Sender，写 i.(int) 本身就不合法，因为 int 不实现 Sender，不可能被存进这个接口；不是所有错误都能留给 comma-ok。若断言另一接口，则运行时保存的具体类型必须同时满足来源接口和目标接口。需要按多个动态类型分支时，可以使用 type switch；第 01 章 switch 部分已经解释了它的分支语法。〔TB28 p8〕

<details>
<summary>类型断言的来源辨析</summary>

TB28 p7 对成功的接口目标断言用了“变量 v 的类型为 i 的值的类型”的表述；若把这里的“类型”理解为静态类型，会与 Go 规范冲突。这里保留原例的行为，并按规范 Type assertions 的明确规定区分：x.(T) 表达式的类型是 T，接口内部的动态类型另算。可查 https://go.dev/ref/spec#Type_assertions；Go 1.26.4 随附的 doc/go_spec.html 在 Type assertions 一节写明这些规则：4115–4131 行说明断言成功时表达式取接口中保存的值、其类型是 T，4147–4160 行说明 comma-ok 形式失败时 ok 为 false、v 取 T 的零值且不发生运行时 panic。


</details>

### 这和鸭子类型有什么关系？

如果你学过 Python，可能会想起鸭子类型：一个对象能不能用，重点在于它有没有需要的方法，而不是它继承自哪个类。上面的 `Email` 和 `SMS` 没有共同父类，却都能交给 `Welcome`，看起来也是这个思路。

不过，Go 和 Python 检查的时机不同。Python 通常在运行到 `sender.send(...)` 时查找对象的方法；如果传入的对象没有这个方法，运行时才会报错。Go 的 `Welcome` 参数明确声明为 `Sender`，编译器在赋值或传参时，就会检查传入类型的方法集合是否满足接口。因此，你可以把 Go 的接口理解为**带静态类型检查的鸭子类型**：重视方法，而不是继承关系；同时在编译阶段检查接口是否满足。

这种说法是帮助理解的类比，不意味着 Go 和动态语言的规则完全相同。比如 Go 要求方法签名匹配，值类型和指针类型的方法集合也可能不同；下一节会专门解释后者。

### 和 Java、TypeScript 的接口有什么不同？

如果你从 Java 过来，可能习惯了先定义接口，再让实现类写 `implements Sender`。这样一来，类与接口的关系需要显式声明；仅仅写出同名方法，并不会自动让类成为该接口的实现。在 Go 中则不需要这一步：只要方法集合满足要求，类型就能赋给接口变量。

如果你熟悉 TypeScript，这一点会更亲切：TypeScript 通常按结构检查类型，一个对象具有接口要求的成员，就可以赋给该接口类型，即使没有写 `implements`。Go 的接口也按方法集合判断是否满足，而不是靠实现声明。不过，两者的检查规则并不完全一样：TypeScript 的接口可以描述属性和方法；这里讨论的 Go 普通接口描述的是方法，且方法签名要匹配。不要把 TypeScript 的结构兼容规则直接照搬到 Go。

### 为什么使用方可以自己定义接口？

隐式实现带来一个重要结果：`Email` 的作者不需要提前知道所有使用它的代码。通知服务可以根据自己的需要定义 `Sender`；另一个程序也可以定义只要求同一个 `Send(string)` 方法的接口。只要 `Email` 的方法满足要求，它就可以用于这两个地方，而不必修改 `Email` 或让它依赖这两个使用方。

这也是 Go 倾向于用**组合**组织程序的原因之一：使用方通过一个小接口接收协作者，再把工作交给具体实现；需要换邮件、短信或测试替身时，换传入的值即可。接口解决的是“这些部分怎样协作”，不要求先把它们安排进同一棵继承树。结构体嵌入可以帮助复用已有实现，但与接口是不同机制，后面会继续讲。

不过，方法签名匹配只是编译器能检查的部分。假设一个 `Sender.Send` 什么也不做，它仍然满足上面的接口，却未必满足业务对发送通知的期望。接口的行为约定、错误处理和测试仍然需要设计；在真实系统中，也通常要让 `Send` 返回错误，供调用方处理。

### 标准库里的例子：io.Writer

刚才的 `Sender` 由我们自己定义。Go 标准库也提供了许多只描述一种能力的接口，其中一个是 `io.Writer`：

```go
type Writer interface {
	Write(p []byte) (n int, err error)
}
```

它要求实现者提供一个 `Write` 方法，接收字节并返回写入数量和错误。文件、内存缓冲区都可以提供这种能力。因此，一个原本只接受 `*os.File` 的保存函数，可以改为接受 `io.Writer`：

```go
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
)

func Save(w io.Writer, data []byte) error {
	n, err := w.Write(data)
	if err == nil && n != len(data) {
		return io.ErrShortWrite
	}
	return err
}

func main() {
	if err := Save(os.Stdout, []byte("to stdout\n")); err != nil {
		panic(err)
	}

	var buf bytes.Buffer
	if err := Save(&buf, []byte("to buffer")); err != nil {
		panic(err)
	}
	fmt.Printf("%q\n", buf.String())
}
```

输出：

```text
to stdout
"to buffer"
```

`Save` 的调用方式和 `Welcome` 一样：它不需要知道写入目标的具体类型，只通过接口调用 `Write`。示例中检查了错误和写入数量，以免调用者把未完整写入的数据当作成功。〔TB30 p6-8〕

本节从 Email、SMS 一路讲到 io.Writer，例子换了几个，模式始终是同一个：调用方声明自己需要的行为，实现方用方法集合去满足。收束本节之前，先补一段选读背景（选读：原课开篇对核心篇的定位，几句话，跳过不影响后面的主线推导）。原课从这一讲起进入核心篇：核心篇主要涵盖接口类型语法与 Go 原生提供的三个并发原语（Goroutine、channel 与 select），之所以把它们放在核心语法的位置，是因为它们不仅代表了 Go 语言在编程语言领域的创新，更是影响 Go 应用骨架（Application Skeleton）设计的重要元素。所谓应用骨架，就是指将应用代码中的业务逻辑、算法实现逻辑、错误处理逻辑等“皮肉”逐一揭去后所呈现出的应用结构，这就好比 Gopher（地鼠）通过 X 光机所看到的骨骼结构。从静态视角看，能看到应用的组成部分以及各部分之间的连接，对应应用内部的耦合设计；从动态视角看，能看到骨架上可独立运动的几大机构，对应应用的并发设计。接口类型与 Go 并发语法恰分别是耦合设计与并发设计的主要参与者，因此 Go 应用的骨架设计离不开它们；一个良好的骨架设计又决定了应用的健壮性、灵活性与扩展性，甚至是应用的运行效率。〔TB28 p1-2〕原课随后安排三讲系统学习接口类型，围绕接口的基础知识与定义惯例、接口类型的内部表示以及接口的应用模式三方面展开，本讲是其中的基础知识部分。〔TB28 p1-2〕后两讲的落点在本章第四节、第五节和第七节。

### 接口即契约

选读到此为止，回到那个共同的模式。接口类型的背后，是通过把类型的行为抽象成契约，建立双方共同遵守的约定，这种契约将双方的耦合降到了最低的程度。〔TB28 p8〕生活、工作中的契约有繁有简，签署方式多样；代码间的契约同样有多有少、有大有小，达成契约的方式也有所不同。而 Go 选择了去繁就简的形式，主要体现在以下两点上。〔TB28 p8〕

**第一点：隐式契约，无需签署，自动生效。**Go 语言中接口类型与它的实现者之间的关系是隐式的，不需要像其他语言（比如 Java）那样要求实现者显式放置 `implements` 进行修饰，实现者只需要实现接口方法集合中的全部方法，便算是遵守了契约，并立即生效了。〔TB28 p8〕本节开头的 Email 与 SMS 就是这么履约的：两个类型都没有写过“实现了 Sender”之类的声明，仅靠各自匹配的 Send 方法就能传进接收 Sender 的 Welcome；“类型是什么时候实现接口的？”小节解释过这个次序为什么成立，“和 Java、TypeScript 的接口有什么不同？”小节则对照过显式声明的写法。对实现者来说没有“签署”这个动作，方法写完，契约即生效。〔TB28 p8〕

**第二点：更倾向于小契约。**契约太繁杂了就会束缚手脚、缺少灵活性、抑制表现力，所以 Go 选择了使用“小契约”，表现在代码上就是尽量定义小接口，即方法个数在 1~3 个之间的接口。Go 语言之父 Rob Pike 曾说过的“接口越大，抽象程度越弱”，这也是 Go 社区倾向定义小接口的另外一种表述。〔TB28 p8〕

### 尽量定义小接口

Go 对小接口的青睐在标准库中体现得淋漓尽致，原课列出了几个日常开发中常用接口的定义：〔TB28 p9〕

```go
// $GOROOT/src/builtin/builtin.go
type error interface {
	Error() string
}

// $GOROOT/src/io/io.go
type Reader interface {
	Read(p []byte) (n int, err error)
}

// $GOROOT/src/net/http/server.go
type Handler interface {
	ServeHTTP(ResponseWriter, *Request)
}

type ResponseWriter interface {
	Header() Header
	Write([]byte) (int, error)
	WriteHeader(int)
}
```

这些接口的方法数量都在 1~3 个之间：error、io.Reader 与 http.Handler 各有 1 个方法，http.ResponseWriter 有 3 个。这种“小接口”的 Go 惯例也已经被 Go 社区项目广泛采用。〔TB28 p9〕原课还统计了早期版本的 Go 标准库（Go 1.13 版本）、Docker 项目（Docker 19.03 版本）以及 Kubernetes 项目（Kubernetes 1.17 版本）中定义的接口类型方法集合中方法数量，从统计结果看：无论是 Go 标准库，还是 Go 社区知名项目，它们基本都遵循了“尽量定义小接口”的惯例，接口方法数量在 1~3 范围内的接口占了绝大多数。〔TB28 p9-10〕两点说明：具体百分比只出现在原课的统计图中，正文没有复述数值；统计基于 2021 年前后的旧版本，数字已陈旧，但“1~3 个方法的接口占绝大多数”这一方向仍然成立。〔TB28 p9-10〕

那么在编码层面，小接口究竟有哪些优势？原课总结了三点。〔TB28 p10〕

**第一点：接口越小，抽象程度越高。**抽象就是对同类事物去除它具体的、次要的方面，抽取它相同的、主要的方面；抽象程度越高，对应的集合空间就越大，抽象程度越低、越具象化、越接近事物真实面貌，对应的集合空间越小。〔TB28 p10〕原课用一张示意图建立了三个生活化的抽象：会飞的，对应蝴蝶、蜜蜂、麻雀、天鹅、鸳鸯、海鸥和信天翁共 7 种；会游泳的，对应鸭子、海豚、人类、天鹅、鸳鸯、海鸥和信天翁，也是 7 种；会飞且会游泳的，只剩天鹅、鸳鸯、海鸥和信天翁 4 种。前两个抽象对应的事物集合，要大于“会飞且会游泳的”所对应的事物集合空间，也就是说这两个抽象的程度更高。〔TB28 p10-11〕把这三个抽象转换成 Go 代码，就是原课给出的三个接口定义：〔TB28 p11-12〕

```go
// 会飞的
type Flyable interface {
	Fly()
}

// 会游泳的
type Swimable interface {
	Swim()
}

// 会飞且会游泳的
type FlySwimable interface {
	Flyable
	Swimable
}
```

Flyable 只有一个 Fly 方法，FlySwimable 则包含两个方法 Fly 和 Swim。FlySwimable 自己一个方法都没有声明，只写下了对 Flyable 与 Swimable 的嵌入；按第六节讲过的嵌入语义，新接口会把被嵌入接口的方法集合并入自己的方法集合，所以它的方法集合就是 Fly 和 Swim 两个方法。〔TB26 p2-3〕原课只给出这三个类型定义，没有配套的 main 函数与运行输出，这里照录，也未编译运行。〔TB28 p11-12〕具有更少方法的 Flyable，抽象程度相对于 FlySwimable 要高，包含的事物集合（7 种动物）也要比 FlySwimable 的事物集合（4 种动物）大。也就是说，接口越小（接口方法少），抽象程度越高，对应的事物集合越大。〔TB28 p11-12〕而这种情况的极限恰恰就是无方法的空接口 interface{}，空接口的这个抽象对应的事物集合空间，包含了 Go 语言世界的所有事物。〔TB28 p12〕

**第二点：小接口易于实现和测试。**小接口拥有比较少的方法，一般情况下只有一个方法，要想满足这一接口，只需要实现一个方法或者少数几个方法就可以，这显然比实现拥有较多方法的接口容易得多；尤其是在单元测试环节，构建类型去实现只有少量方法的接口，要比实现拥有较多方法的接口付出的劳动少许多。〔TB28 p12〕本手册有现成的对照：第六节“冲突、歧义与测试伪对象”小节里，Stmt 接口声明了 Close、NumInput、Exec、Query 四个方法，测试伪对象把它嵌入自身之后，只需覆写真正用到的 Exec 就能满足接口；14 节开头的 Store 接口只有一个 Get 方法，手写一个 fakeStore 假仓库几行就完成了。接口方法越少，测试替身要补的方法就越少。

**第三点：小接口表示的“契约”职责单一，易于复用组合。**Go 推崇通过组合的方式构建程序，Go 开发人员一般会尝试通过嵌入其他已有接口类型的方式来构建新接口类型，就像通过嵌入 io.Reader 和 io.Writer 构建 io.ReadWriter 那样。〔TB28 p12-13〕那构建时，如果有众多候选接口类型供我们选择，我们会怎么选择呢？显然，我们会选择那些新接口类型需要的契约职责，同时也要求不要引入我们不需要的契约职责。在这样的情况下，拥有单一或少数方法的小接口便更有可能成为我们的目标，而那些拥有较多方法的大接口，可能会因引入了诸多不需要的契约职责而被放弃。由此可见，小接口更契合 Go 的组合思想，也更容易发挥出组合的威力。〔TB28 p13〕

### 定义小接口，你可以遵循的几点

保持简单有时候比复杂更难。小接口虽好，但如何定义出小接口是摆在所有 Gopher 面前的一道难题。这道题没有标准答案，但有一些点可供我们在实践中考量遵循。〔TB28 p13〕

首先，别管接口大小，先抽象出接口。要设计和定义出小接口，前提是需要先有接口：在定义小接口之前，先针对问题领域进行深入理解，聚焦抽象并发现接口，针对领域对象的行为进行抽象，形成一个接口集合。〔TB28 p13〕初期，先不要介意这个接口集合中方法的数量，因为对问题域的理解是循序渐进的，在第一版代码中直接定义出小接口可能并不现实。标准库中的 io.Reader 和 io.Writer 也不是在 Go 刚诞生时就有的，而是在发现对网络、文件、其他字节数据处理的实现十分相似之后才抽象出来的；并且越偏向业务层，抽象难度就越高，这或许也是前面图中 Go 标准库小接口（1~3 个方法）占比略高于 Docker 和 Kubernetes 的原因。〔TB28 p14〕

第二，将大接口拆分为小接口。有了接口后，接口会被用在代码的各个地方；一段时间后，就来分析哪些场合使用了接口的哪些方法。原课给了这样一个示意：大接口 1 定义了多个方法，方法 1 和方法 2 经常用在场合 1 中，方法 3 和方法 4 经常用在场合 2 中，方法 5 和方法 6 经常用在场合 3 中，大接口 1 的方法呈现出一种按业务逻辑自然分组的状态；这时可以把这三组方法分别提取出来放入三个小接口，也就是将大接口 1 拆分为三个小接口 A、B 和 C。〔TB28 p14-15〕拆分后，原应用场合 1~3 使用接口 1 的地方就可以无缝替换为接口 A、B、C 了。〔TB28 p14-15〕替换之所以“无缝”，是因为接口 A 的方法集合（方法 1、2）是大接口 1 方法集合的子集：凡是实现了大接口 1 的类型必然也实现了接口 A，场合 1 又只调用方法 1、2，赋值与调用都仍然合法。这句推导是本手册补的，原课只给出“无缝替换”的结论。

最后，注意接口的单一契约职责。上面已经被拆分成的小接口，是否需要进一步拆分、直至每个接口都只有一个方法呢？这个依然没有标准答案，不过可以考量一下现有小接口是否需要满足单一契约职责，就像 io.Reader 那样；如果需要，就可以进一步拆分，提升抽象程度。〔TB28 p15〕

定义自己的接口时，也可以从使用方实际需要的方法出发，不必预先为每个类型都创建接口。如果标准库已经有合适的接口，直接使用即可。接口应当完整表达使用方的需要，并不是方法越少越好。〔TB28 p10-15〕这段是本手册的补充建议，不是原课原文，但有两个落点：其一，方法数量跟着契约的职责走，而不是为小而小，对应上面第三步的单一契约职责考量〔TB28 p15〕；其二，第 30 讲批评 *os.File 让 Save 被迫依赖 Chdir、Chmod 等保存用不到的方法，违反接口分离原则，而 io.Writer 仅含 Save 唯一需要的 Write 方法。〔TB30 p6-8〕


## 三、为什么指针能实现接口，值却不行？

前面的 `Save(&buf, ...)` 用的是 `&buf`，而不是 `buf`。为什么指针能实现接口、值却不行？回答这个问题需要两步准备：先看清方法的本质，再引入**方法集合**。

### 方法的本质：接收者是第一个参数

C++ 的对象调用方法时，编译器自动传入指向对象自身的 `this` 指针作为方法的第一个参数；Go 的原理相似：**将 receiver 参数以第一个参数的身份并入方法的参数列表**。〔TB24 p6-7〕

按这个原理，任何方法都能机械地改写成普通函数。以第一节的 `Counter` 为例，receiver 变成第一个参数，其余参数原样排在后面：

```go
// (c Counter) Value() int 的等价函数
func Value(c Counter) int { return c.value }

// (c *Counter) Add(n int) 的等价函数
func Add(c *Counter, n int) { c.value += n }
```

原课程用 `Get`/`Set` 举的也是同一套改法：`(t T) Get() int` 等价于 `func Get(t T) int`，`(t *T) Set(a int) int` 等价于 `func Set(t *T, a int) int`。〔TB24 p6-7〕

**等价转换后的函数的类型，就是方法的类型**。这个转换由 Go 编译器在编译和生成代码时自动完成，源码层面看不到它，但它决定了方法调用时参数如何传递。〔TB24 p6-7〕

这套等价关系有一个直接用处：分析"方法会不会修改原实例"时，把方法改写成普通函数，按函数参数值传递的语义推一遍即可。下一节的并发难题会再用一次。

### 方法表达式：把方法当函数用

方法本质上既然是函数，就可以不写 `t.Set(1)`，而是直接以类型名调用：`(*T).Set(&t, 1)`。这种形式叫**方法表达式**：使用时显式补上作为第一个参数的 receiver 实例，类似 C++ 的静态方法。〔TB24 p7-8〕

方法自身的类型就是普通函数类型，所以方法可以像函数值一样赋给变量：

```go
var c Counter
f1 := (*Counter).Add // f1 的类型：func(*main.Counter, int)
f2 := Counter.Value  // f2 的类型：func(main.Counter) int
f1(&c, 3)            // 等价于 c.Add(3)
fmt.Println(f2(c))   // 3
```

用 `%T` 打印 `f1`、`f2`，输出 `func(*main.Counter, int)` 和 `func(main.Counter) int`，receiver 就是函数的第一个普通参数。这是"方法的本质是函数"最直接的证据。〔TB24 p7-8〕

方法表达式有一个限制：通过它，`Counter` 只能调用 `Counter` 方法集合中的方法，`*Counter` 同理。方法集合正是下一小节的主角。〔TB24 p7-8〕

### 三个 six：用方法本质分析并发难题

方法的本质有什么用？看一个真实的并发问题。下面的代码在两个切片上做同样的事，输出却不一样〔TB24 p9〕：

```go
package main

import (
	"fmt"
	"time"
)

type field struct {
	name string
}

func (p *field) print() {
	fmt.Println(p.name)
}

func main() {
	data1 := []*field{{"one"}, {"two"}, {"three"}}
	for _, v := range data1 {
		go v.print()
	}

	data2 := []field{{"four"}, {"five"}, {"six"}}
	for _, v := range data2 {
		go v.print()
	}

	time.Sleep(3 * time.Second)
}
```

多核 macOS 上的输出是 one、two、three 后面跟着三个 six（行序可能因 goroutine 调度而异）。奇怪的地方在 `data2`：为什么不是 four、five、six？〔TB24 p9-10〕

末尾的 `time.Sleep(3 * time.Second)` 不是可有可无的装饰。main goroutine 一退出，整个应用程序就跟着退出〔TB31 p8〕，不给这 3 秒的话，main 大概率等不到那 6 个子 goroutine 被调度执行；它给的是 6 个子 goroutine（data1 三个加 data2 三个）被调度执行的机会。

按方法本质做等价变换，把 `go v.print()` 写成方法表达式形式，参数怎么绑的一目了然（变换前后输出一致）〔TB24 p10〕：

```go
for _, v := range data1 {
	go (*field).print(v) // data1 的元素类型是 *field，v 就是元素地址
}

for _, v := range data2 {
	go (*field).print(&v) // data2 的元素类型是 field，必须取地址
}
```

- `data1`：元素类型 `*field` 与 receiver 参数类型一致，`v` 就是各元素的地址，直接传入，延迟执行也能打印各自的 one、two、three。〔TB24 p10〕
- `data2`：元素类型 `field` 与 receiver 类型不同，必须取地址。每次传入的 `&v` 是**循环变量 `v` 的地址**，不是切片各元素的地址。〔TB24 p11〕

而 `for range` 的循环变量 `v` 在整个循环过程中只有一个（旧版语义下 for range 声明的循环变量只创建一次、逐轮复用），`data2` 迭代完成后，`v` 是最后一个元素 six 的拷贝。这些子 goroutine 往往要等 main 执行到 `time.Sleep` 才被调度，此刻解引用 `&v`，读到的都是同一个 six。〔TB24 p11〕

**版本边界**：上面的分析出自原课程，按 Go 1.21 及更早的循环变量语义讲解〔TB24 p9-11〕。Go 1.22 起，采用相应语言版本的代码里，`for` 用 `:=` 声明的循环变量每轮迭代都新建一份，`&v` 各轮指向不同变量，同样的代码在延迟调度下会输出 four、five、six，而不是三个 six（这是语言规范的修正，原课文本未涉及；本书并发一章 `guide/handbook/06-concurrency-basics.md` 有同样的提醒）。读老资料里的"三个 six"分析时，先确认它写在哪个语义阶段。

〔实测〕这套新语义的预言可以直接验证：在 go1.26.4 darwin/arm64 上运行上面的原始问题代码，输出是 six、one、four、five、three、two，六个值互不相同，没有出现三个 six，两个循环各打各的元素，这正是每轮新建循环变量的直接结果。

### 修法：换接收者类型

课程给的修复方法只有一行：把 `print` 的 receiver 从 `*field` 改成 `field`，程序就输出 one 到 six。〔TB24 p11-12〕修后的完整代码只动 receiver 这一处，其余逐行不变：

```go
package main

import (
	"fmt"
	"time"
)

type field struct {
	name string
}

func (p field) print() {
	fmt.Println(p.name)
}

func main() {
	data1 := []*field{{"one"}, {"two"}, {"three"}}
	for _, v := range data1 {
		go v.print()
	}

	data2 := []field{{"four"}, {"five"}, {"six"}}
	for _, v := range data2 {
		go v.print()
	}

	time.Sleep(3 * time.Second)
}
```

修改后程序的输出是 one、two、three、four、five、six（因 Goroutine 调度顺序不同，你机器上的输出顺序可能与这里不同）。〔TB24 p12〕

原因是：receiver 变成 `field` 后，方法表达式的第一个参数类型也是 `field`，两个循环传入的都是**元素的值拷贝**：`data1` 的循环是 `field.print(*v)`，`data2` 的循环是 `field.print(v)`，每个 goroutine 在 go 语句处就拿到了自己那一轮的值，不再依赖循环变量的地址〔TB24 p14〕；这正好绕开了旧语义的循环变量复用问题。原课程把这个修法的原理留作思考题，留言区给出了与分析一致的解释。

留言区还有另一种不改 receiver 的修法：让每个 goroutine 各拿一份循环变量。迭代 `data2` 时，在循环体开头加一行 `v := v`，用本轮新建的副本遮蔽外层的循环变量，`go v.print()` 捕获的就是这份各轮各自的副本。〔TB24 p15〕

### 方法集合：判断接口实现的唯一手段

先看一个表面矛盾的程序〔TB25 p6-7〕：

```go
type Interface interface {
	M1()
	M2()
}

type T struct{}

func (t T) M1()  {}
func (t *T) M2() {}

func main() {
	var t T
	var pt *T
	var i Interface

	i = pt
	i = t // 编译错误：T does not implement Interface（缺 M2）
}
```

`i = pt` 编译通过，`i = t` 却报错，编译器的理由是 T 没有实现接口方法列表中的 M2。表面上讲不通：如果说 T 因为"只实现了 M1"而不合格，那 `*T` 不也只实现了 M2、没实现 M1 吗？为什么它能赋值？有些事情并不是表面看起来的样子。〔TB25 p6-7〕

解开悖论要靠**方法集合**（method set）：它是判断一个类型是否实现了某接口类型的唯一手段，可以说"方法集合决定了接口实现"。〔TB25 p6-7〕

Go 中任何一个类型都有属于自己的方法集合，它是类型的一个"属性"。没有定义方法的类型（比如 `int`）拥有**空方法集合**。接口类型比较特殊：它只列出方法、不定义方法体，接口类型的方法集合就是它方法列表里的所有方法；下面重点讲非接口类型的方法集合。〔TB25 p7〕

想亲眼看一个类型的方法集合，可以用这个工具函数：〔TB25 p7-8〕

```go
func dumpMethodSet(i interface{}) {
	dynTyp := reflect.TypeOf(i) // 第一步：取出接口参数的动态类型
	if dynTyp == nil {
		fmt.Printf("there is no dynamic type\n") // 为 nil 时打印提示并返回
		return
	}

	n := dynTyp.NumMethod() // 第二步：拿到方法个数
	if n == 0 {
		fmt.Printf("%s's method set is empty!\n", dynTyp) // 为 0 说明方法集合为空
		return
	}

	fmt.Printf("%s's method set:\n", dynTyp)
	for j := 0; j < n; j++ {
		fmt.Println("-", dynTyp.Method(j).Name) // 第三步：逐个打印方法名
	}
}
```

拿 `int` 和一个自定义类型试一下〔TB25 p8-9〕：

```go
type T struct{}

func (T) M1() {}
func (T) M2() {}

func (*T) M3() {}
func (*T) M4() {}

func main() {
	var n int
	dumpMethodSet(n)
	dumpMethodSet(&n)

	var t T
	dumpMethodSet(t)
	dumpMethodSet(&t)
}
```

输出：

```text
int's method set is empty!
*int's method set is empty!
main.T's method set:
- M1
- M2

*main.T's method set:
- M1
- M2
- M3
- M4
```

`T` 的方法集合包含 M1、M2，符合预期；但 `*T` 的方法集合除了预期的 M3、M4，居然还包含 `T` 定义的方法 M1、M2，一共四个。这不是程序输出错误，而是 Go 语言的规定：**`*T` 类型的方法集合，包含所有以 `*T` 为 receiver 参数类型的方法，以及所有以 `T` 为 receiver 参数类型的方法**；`T` 的方法集合则只包含值接收者方法。〔TB25 p9〕

现在回到悖论。对开头那对 `T`（M1 值接收者、M2 指针接收者）再跑一次 `dumpMethodSet`：

```text
main.T's method set:
- M1

*main.T's method set:
- M1
- M2
```

`T` 的方法集合只有 M1，缺接口要求的 M2，所以 `t` 赋给 `Interface` 被拒绝；`*T` 的方法集合是 M1、M2，与 `Interface` 接口类型的方法集合完全一样，所以 `pt` 能赋值。〔TB25 p9-10〕`*T` 从来不是"只实现了 M2"，它的方法集合里本来就有 M1，这就是为什么编译器放行 `i = pt`、拦下 `i = t`。一个把接口方法一部分声明成值接收者、一部分声明成指针接收者的类型，值形式一定无法实现该接口。本章开头的 `Email`、`SMS` 全用值接收者，`Sender` 才同时接受 `Email{}` 和 `&Email{}`。

现在可以给出精确的判定条件：**如果类型 `T` 的方法集合与接口类型 `I` 的方法集合相同，或者 `T` 的方法集合是 `I` 方法集合的超集，就说 `T` 实现了接口 `I`**。方法集合这个概念在 Go 中的主要用途，正是判断某个类型是否实现了某个接口；它像"胶水"一样把自定义类型与接口隐式地粘结在一起，后面讲结构体嵌入时，判断新类型拥有哪些方法还要靠它。〔TB25 p10-12〕

想动手验证，练习包里有现成的矩阵：`exercises/p30/iface_test.go:43` 的 `TestMethodSetMatrix` 枚举 T/`*T` 对同一接口的赋值组合，`exercises/p30/iface.go:40` 用 `var _ Iface = (*Impl)(nil)` 做编译期断言。

### 接收者选型的第三原则：先看要不要实现接口

第三条原则站在方法集合之上，前两条则落在单个方法的实现层面。原课的推导顺序是先分析 receiver 参数类型对方法的影响，得出前两条原则，最后才在方法集合的基础上提出第三条；前文只在括注里带过前两条，这里按原课顺序补齐推导，再回到本条。

原课分析 receiver 影响时走的路径是：上一讲已经得出"Go 方法实质上是以方法的 receiver 参数作为第一个参数的普通函数"的结论，本章第一节"方法的本质：接收者是第一个参数"也做过同样的机械改写；既然函数参数类型对函数的影响我们已经很熟悉，就可以把方法等价转换为函数，再借用函数参数的传递语义，间接得出 receiver 参数类型对方法的影响〔TB25 p2〕。原课选了两个方法做这次转换的代表：

```text
func (t T) M1()  <=> F1(t T)
func (t *T) M2() <=> F2(t *T)
```

M1 是 receiver 参数类型为 T 的一类方法的代表，M2 代表 receiver 参数类型为 *T 的另一类〔TB25 p2〕。转换之后 receiver 就是普通参数，剩下的问题只有一个：值拷贝传递时，函数体里拿到的是副本还是地址。

纸面推演之外，原课接着给出一个更直观的对照例子，用真实输出证明上面的分析〔TB25 p3〕：

```go
package main

type T struct {
	a int
}

func (t T) M1() {
	t.a = 10
}

func (t *T) M2() {
	t.a = 11
}

func main() {
	var t T
	println(t.a) // 0

	t.M1()
	println(t.a) // 0

	p := &t
	p.M2()
	println(t.a) // 11
}
```

两个方法体一模一样，都是通过 receiver 参数 t 修改字段 a，差别只在 receiver 类型。把两次调用按等价函数推一遍：`t.M1()` 等价于 `F1(t)`，Go 函数的参数采用值拷贝传递，进入函数体的 t 是 T 类型实例的一个副本，对副本的任何修改都只影响副本本身，不会影响原实例；`p.M2()` 等价于 `F2(p)`，传给函数的是实例的地址，通过地址做的任何修改都会反映到原 T 类型实例上〔TB25 p2-3〕。运行结果与推演一致，三次输出分别是 0、0、11〔TB25 p3-4〕：M1 调用后 t.a 仍是 0，因为它修改的仅是实例 t 的副本；M2 调用后 t.a 变成 11，因为它通过地址修改的是实例本身。写成正式结论：receiver 参数类型为 T 时，以值传递方式进入方法体的是 T 类型实例的副本，方法体内对副本的任何修改操作都不会影响原实例〔TB25 p2〕；receiver 参数类型为 *T 时，以值传递方式进入方法体的是 T 类型实例的地址，方法体通过该地址可以对原 T 类型实例进行任何修改操作〔TB25 p3〕。顺带留意调用形式：这个例子先 `p := &t` 显式取出指针，再通过指针变量 p 调用 M2，走的是显式指针；值变量不取地址直接调用 `*T` 方法的情形，见下面的语法糖例子。

有了这组对照，就得到原课选择 receiver 参数类型的第一个原则：**如果方法要把对 receiver 参数代表的类型实例的修改，反映到原类型实例上，那么 receiver 参数的类型应该选择 `*T`**〔TB25 p4〕。它直接立在刚才的现象上：只有 `*T` receiver 让方法体通过地址改到原实例，`T` receiver 改的只是副本〔TB25 p3-4〕。这条原则落地后立刻引出一个新问题：选了 `*T`，是不是就只能通过 `*T` 类型的变量调用该方法？

原课改造上面的例子回答这个问题，类型 T 和两个方法原样沿用，main 里换成两个实例：

```go
package main

type T struct {
	a int
}

func (t T) M1() {
	t.a = 10
}

func (t *T) M2() {
	t.a = 11
}

func main() {
	var t1 T
	println(t1.a) // 0
	t1.M1()
	println(t1.a) // 0
	t1.M2()
	println(t1.a) // 11

	var t2 = &T{}
	println(t2.a) // 0
	t2.M1()
	println(t2.a) // 0
	t2.M2()
	println(t2.a) // 11
}
```

先看 T 类型的实例 t1。它不仅能调用 receiver 为 T 的 M1，还能直接调用 receiver 为 *T 的 M2。这是 Go 编译器在背后自动转换的结果，原课把 `t1.M2()` 这种用法称作"语法糖"：编译器判断 t1 的类型 T 与 M2 的 receiver 参数类型 `*T` 不一致，会自动把 `t1.M2()` 转换为 `(&t1).M2()`〔TB25 p5〕。对称方向同样成立：t2 是 `&T{}`，类型为 `*T`，它调用 receiver 为 `T` 的 M1 时，编译器判断 t2 的类型 `*T` 与 M1 的 receiver 参数类型 `T` 不一致，自动把 `t2.M1()` 转换为 `(*t2).M1()`〔TB25 p5〕。

输出要逐步看。t1 的三次输出是 0、0、11：`t1.M1()` 的调用者类型与 receiver 类型完全一致，不触发糖，传入的本来就是副本，所以第二次输出仍是 0；`t1.M2()` 经 `(&t1).M2()` 修改原实例，第三次输出 11。t2 的三次输出也是 0、0、11，但中间那步要特别注意：`t2.M1()` 被转换为 `(*t2).M1()`，解引用之后传进方法体的仍是实例的副本，M1 修改的还是副本，所以第二次输出仍是 0。也就是说，语法糖只解决"能不能调"的资格问题，并不改变 receiver 的传递语义；直到 `t2.M2()`，才通过地址把 11 写进 t2 指向的实例〔TB25 p4-5〕。

原课由此给出结论：无论是 T 类型实例还是 *T 类型实例，都既可以调用 receiver 为 `T` 的方法，也可以调用 receiver 为 `*T` 的方法；为方法选择 receiver 参数类型时，不需要担心这个方法不能被与 receiver 参数类型不一致的类型实例调用〔TB25 p5〕。第一节"方法怎么调用"里 `*T` 实例调用绑定在 `T` 上的方法，从调用侧看正是这颗糖的对称方向。不过糖只覆盖调用这一层：上一小节的方法集合决定的是接口实现，`T` 的方法集合并不会因为 `t1.M2()` 能编译而多出 M2，下一节"调用不等于实现"会把这两层的差别钉死。

〔实测：在 go1.26.4 darwin/arm64 上运行上面两个程序，第一个输出 0、0、11；第二个里 `t1.M2()` 之后 `t1.a` 变为 11，`t2.M1()` 之后 `t2.a` 仍为 0，与逐步分析一致。〕

原则一覆盖了"要修改"的情形。方法不需要修改实例时选 `T` 还是 `*T`？原课的回答是"这也得分情况"，并把结论称作选择 receiver 参数类型的第二个原则〔TB25 p5-6〕。一般情况下选 `T`：这样可以缩窄外部修改类型实例内部状态的"接触面"，也就是尽量少暴露可以修改类型内部状态的方法〔TB25 p5-6〕。例外在性能：Go 方法调用时 receiver 参数以值拷贝形式传入，如果 receiver 参数类型的 size 较大，值拷贝会带来较大的性能开销，这时选择 `*T` 可能更好〔TB25 p6〕。两条合起来才是完整的决策：先看方法要不要把修改反映到原实例，要改就选 `*T`（原则一）；不需要修改时一般选 `T` 缩窄修改面，仅当基类型 size 较大、值拷贝开销明显时换成 `*T`（原则二）。

结合方法集合，可以得到接收者选型的第三条原则，它比前两条高一个层次：

**选择依据是 `T` 类型是否需要实现某个接口。**〔TB25 p10-11〕

- 如果 `T` 需要实现某个接口，就把接口要求的**所有方法**都用 `T` 作 receiver 声明，让值类型 `T` 的方法集合覆盖接口方法集合。混用接收者（比如 M1 用 `T`、M2 用 `*T`）会导致 `T` 无法实现接口，就像上面悖论里的类型。
- 如果 `T` 不需要实现某个接口，而 `*T` 需要，不必专门处理：`*T` 的方法集合包含 `T` 的方法集合，按原则一、二逐个方法选就行。

前两条原则聚焦类型内部、单个方法的实现层面；第三条是从全局设计层面考虑，聚焦类型与接口类型之间的耦合关系。所以实际设计时应该**先考虑第三条**：先问这个类型要不要作为某个接口使用。需要，就把接口方法全声明成值接收者；不需要，再按原则一（要把修改反映到原实例，选 `*T`）和原则二（否则一般选 `T`，缩窄外部修改内部状态的"渠道"；receiver 基类型的 size 较大、值拷贝开销明显时例外，选 `*T`）逐个方法确定。〔TB25 p10-11〕

本节的 `Email` 是第三条原则的正面例子：`Send` 未来可能作为 `Sender` 接口被 `Welcome` 使用，所以它的接收者用值 `Email`，而不是 `*Email`。

### 调用不等于实现：自动取地址不改变方法集合

最后补一块拼图，它解释了 `Save(&buf, ...)` 为什么必须传 `&buf`。看这个程序：

```go
package main

import "fmt"

type Incrementer interface {
	Add(int)
}

type Counter struct{ value int }

func (c *Counter) Add(n int) { c.value += n }

func main() {
	var c Counter
	c.Add(3) // c 可寻址，等价于 (&c).Add(3)

	var inc Incrementer = &c // *Counter 有 Add 方法
	inc.Add(2)
	fmt.Println(c.value) // 5

	// var bad Incrementer = c // 编译错误：Counter 的方法集合没有 Add
}
```

同一个 `Counter`，为什么 `c.Add(3)` 能编译、`var bad Incrementer = c` 却不能？关键在检查对象不同：

- `c.Add(3)` 是**方法调用**。`c` 是可寻址的变量，编译器自动改写为 `(&c).Add(3)`（这是 TB25 p4-5 讲过的语法糖）。这不会把 `Add` 加进 `Counter` 的方法集合，只是给调用行开方便之门；map 元素、接口里装的值这样的不可寻址值连这扇门都没有（`notes/p2-lang-core.md` 的"方法集精确规则"记录了同样的限制）。
- `var inc Incrementer = &c` 是**接口赋值**。编译器检查的是赋入类型本身的方法集合：`*Counter` 的方法集合包含 `Add`，通过；`Counter` 的不包含，拒绝。赋值路径上没有自动取地址。

**(本教程推导，非原课内容)** 方法集合的不对称，可以从"值的可得性"理解：手上有 `*T`，解引用总能得到一个 `T` 值，所以 `*T` 的方法集合包含 `T` 的全部方法；反过来，`T` 值要变成 `*T` 必须取地址，而取地址要求值可寻址。方法调用发生在现场，编译器能判断可寻址性、自动取地址；接口赋值检查的是类型本身的方法集合，不走这条路。

想在编译期确认某个类型实现了接口，可以写 `var _ Incrementer = (*Counter)(nil)`。`_` 丢弃变量值，`(*Counter)(nil)` 不需要任何实例，这行代码不执行任何方法，只在编译时检查 `*Counter` 的方法集合是否覆盖接口。这是 Go 社区的常见惯用法（原课未讲；练习包 `exercises/p30/iface.go:40` 用它对 `Iface` 做编译期断言，`notes/p2-lang-core.md` 也把它列为惯用法）。

所以第三节开头的问题现在有了完整答案：`Save(&buf, ...)` 传 `&buf`，是因为接口赋值检查的是赋入类型的方法集合，编译器不会在赋值时自动把值变成指针。

## 四、接口值和 nil：动静兼备的接口，内部如何表示

上一节的 `Sender`、`error` 都是接口类型，接口变量可以先后保存不同具体类型的值。Go 是一门静态语言，接口却是这门静态语言中唯一“动静兼备”的语法特性：静态的一面在编译期，动态的一面在运行时。Russ Cox 说过：“如果要从 Go 语言中挑选出一个特性放入其他语言，我会选择接口。”〔TB29 p1〕接口与并发原语，正是作者最喜欢的两类 Go 语法元素，这个特性的地位可见一斑。〔TB29 p1〕也正因为它动静兼备，初学者常在接口上遇到困惑；要解开困惑，必须深入到运行时层面，看接口类型的运行时表示。〔TB29 p1〕

先看静态的一面。接口变量有静态类型：`var err error` 中 `err` 的静态类型是 `error`。编译器在编译阶段对所有接口类型变量的赋值操作做类型检查，检查右值的类型是否实现了该接口方法集合中的所有方法，不满足就报错：

```go
var err error = 1
```

原课示例的报错是：cannot use 1 (type int) as type error in assignment，即把 `int` 赋给 `error` 接口类型变量时编译失败。〔TB29 p2〕

再看动态的一面。接口变量在运行时还存储了右值的真实类型信息，称为动态类型：

```go
var err error
err = errors.New("error1")
fmt.Printf("%T\n", err) // *errors.errorString
```

`err` 的静态类型始终是 `error`，`%T` 打印的却是它运行时保存的动态类型 `*errors.errorString`。〔TB29 p2〕运行时接口变量可以被赋值为不同动态类型的变量，每次赋值后其中存储的动态类型信息都会变化。〔TB29 p2〕

关于“类型靠行为决定”的思想，第二节“这和鸭子类型有什么关系？”已经讲过：Duck/Dog/Bird 没有共同的父类型，只凭各自实现了 `Quack` 就都能赋给 `QuackableAnimal`。这里补一层运行时的视角：某类型能否作为某接口类型的右值，不由它的“基因”（如 C++ 中的父类）决定，而由它表现出的行为（拥有的方法）决定；每次赋值后接口变量里存的动态类型不同，`a.Quack()` 的执行结果就由这份动态类型信息决定。〔TB29 p2-4〕与动态语言不同，编译器在编译期就能捕捉把 `int` 变量传给 `QuackableAnimal` 这类明显错误，决不会让错误遗漏到运行时才被发现：Python 式的灵活与编译期安全并存。〔TB29 p2-4〕

有了这些铺垫，本节开头的问题可以回答得更透。看一个改编自 Go FAQ 的例子：

```go
type MyError struct {
	error
}

var ErrBad = MyError{
	error: errors.New("bad things happened"),
}

func bad() bool {
	return false
}

func returnsError() error {
	var p *MyError = nil
	if bad() {
		p = &ErrBad
	}
	return p
}

func main() {
	if err := returnsError(); err != nil {
		fmt.Printf("error occur: %+v\n", err)
		return
	}
	fmt.Println("ok")
}
```

初学者预期 `bad()` 恒为 false，`p` 保持为 nil，`main` 输出 ok 后退出；真实输出却是：

```text
error occur: <nil>
```

程序进入了错误处理分支。〔TB29 p4-5〕为什么 `err != nil` 会成立？要解释这个问题，必须换一个视角：接口值在运行时如何表示。先记住结论：返回的 `error` 接口变量 `err` 的数据指针虽为空，但类型信息 `iface.tab` 并不为空，而是 `*MyError` 对应的类型信息，因此 `err` 与 nil（内部表示 `(0x0,0x0)`）相比自然不相等。〔TB29 p14〕下面把这个结论拆开讲。

### 内部表示：eface 与 iface

在运行时，Go 用两个结构体表示接口变量，定义在 `$GOROOT/src/runtime/runtime2.go`：

```go
// iface 表示拥有方法的非空接口类型变量
type iface struct {
	tab  *itab
	data unsafe.Pointer
}

// eface 表示没有方法的空接口类型变量
type eface struct {
	_type *_type
	data  unsafe.Pointer
}
```

〔TB29 p5-6〕两个结构都有两个指针字段，第二个字段功能相同：指向当前赋给该接口类型变量的动态类型变量的值。〔TB29 p6〕区别在第一个字段。空接口没有方法列表，`eface` 的第一字段直接指向动态类型的类型信息 `_type`；非空接口除了动态类型信息，还要存储接口自身的信息（接口类型信息、方法列表）以及动态类型所实现方法的信息，所以第一字段指向 `itab`。〔TB29 p6〕

这一双表示至今未变。本机 go1.26.4 源码 `$GOROOT/src/runtime/runtime2.go:184-192` 中，`iface{tab *itab; data unsafe.Pointer}` 与 `eface{_type *_type; data unsafe.Pointer}` 的布局与书中完全一致；不过 `itab` 与 `_type` 现在都是别名（`runtime2.go:1129` 的 `type itab = abi.ITab`、`runtime/type.go:25` 的 `type _type = abi.Type`），物理定义移到了 `internal/abi` 包，见后文。

### 动态类型的统一档案：_type

`_type` 定义于 `$GOROOT/src/runtime/type.go`，描述一个类型的元信息：

```go
type _type struct {
	size       uintptr // 大小
	ptrdata    uintptr // 内含指针的内存前缀大小
	hash       uint32  // 类型哈希
	tflag      tflag   // 额外的类型信息标志
	align      uint8   // 该类型变量对齐
	fieldAlign uint8   // 结构体字段对齐
	kind       uint8   // 类型种类
	// function for comparing objects of this type
	// (ptr to object A, ptr to object B) -> ==?
	equal func(unsafe.Pointer, unsafe.Pointer) bool  // 比较函数指针
	gcdata  *byte // GC 类型数据
	str     nameOff // 类型名字符串的偏移
	ptrToThis typeOff // 指向该类型的指针的类型信息的偏移
}
```

〔TB29 p6〕各字段的职责：`size` 是该类型值的大小；`ptrdata` 是含指针的内存前缀的大小；`hash` 是类型哈希；`tflag` 存放额外的类型信息标志；`align` 与 `fieldAlign` 分别是该类型变量、结构体字段的对齐；`kind` 标记类型种类；`equal` 是比较函数指针，比较两个对象指针是否相等；`gcdata` 存 GC 类型数据，`kind` 设置 `KindGCProg` 位时是 GC program，否则是 ptrmask 位图（见 `runtime/mbitmap.go`）；`str` 与 `ptrToThis` 是偏移量，分别对应类型名字符串与“指向该类型的指针”的类型信息。〔TB29 p6〕

运行时还有一个关键事实：Go 中每种类型，无论内置原生类型还是自定义类型，都有唯一的 `_type` 信息；运行时会为程序内全部类型建立只读的共享 `_type` 信息表。因此，拥有相同动态类型的同类接口变量，其 `_type`/`tab` 信息是相同的。〔TB29 p9〕据此，每个接口变量运行时表示的两部分可以统一记作 `eface(_type, data)` 与 `iface(tab, data)`：虽然第一字段有差别，但 `tab` 与 `_type` 都可统一看作动态类型的类型信息。〔TB29 p9〕

本机 go1.26.4 源码中，`runtime/type.go:25` 声明 `type _type = abi.Type`，物理定义移到了 `$GOROOT/src/internal/abi/type.go:20-45`。字段顺序与语义和书中 Go 1.17 版本一致，但字段名改为导出形式：`Size_`、`PtrBytes`（即书中 `ptrdata`，注释是 "number of (prefix) bytes in the type that can contain pointers"）、`Hash`（注释 "hash of type; avoids computation in hash tables"）、`TFlag`、`Align_`、`FieldAlign_`、`Kind_`、`Equal`、`GCData`、`Str`（类型 `NameOff`）、`PtrToThis`（类型 `TypeOff`）。GCData 的注释还新增了一种按需模式：设置 `TFlagGCMaskOnDemand` 位时 GCData 是 `**byte`，位图按需构建。内存布局与书中描述一致。

### itab：非空接口的“实现档案”

`iface.tab` 指向的 `itab` 定义同样在 `$GOROOT/src/runtime/runtime2.go`：

```go
type itab struct {
	inter *interfacetype // 接口类型自身信息
	_type *_type         // 动态类型信息
	hash  uint32         // copy of _type.hash; used for type switches
	_     [4]byte
	fun   [1]uintptr     // variable sized；fun[0]==0 表示动态类型未实现该接口
}
```

〔TB29 p7〕四个关键部分：`inter` 指向 `interfacetype`，存储接口类型自身的信息；`_type` 存储该接口变量的动态类型信息；`hash` 是 `_type.hash` 的副本，用于 type switch 快速分派；`fun` 是动态类型已实现的接口方法的调用地址数组，方法调用直接走这里预存的地址，无需运行时查表，`fun[0]==0` 则表示未实现。〔TB29 p7〕

`interfacetype` 由三部分组成，定义在 `$GOROOT/src/runtime/type.go`：

```go
type interfacetype struct {
	typ     _type   // 类型信息
	pkgpath name    // 包路径名
	mhdr    []imethod // 接口方法集合切片
}
```

〔TB29 p7〕本机 go1.26.4 源码中，`runtime2.go:1129` 声明 `type itab = abi.ITab`，物理定义在 `$GOROOT/src/internal/abi/iface.go:14-19`：`ITab{Inter *InterfaceType; Type *Type; Hash uint32; Fun [1]uintptr}`。显式的 `_ [4]byte` 填充字段消失了，`Hash` 之后的 4 字节对齐填充改由 `Fun` 的 8 字节对齐隐式保证，内存布局不变；注释保留 `fun[0]==0 means Type does not implement Inter` 的语义，`Hash` 的注释是 "copy of Type.Hash. Used for type switches."。`interfacetype` 同样是别名：`runtime/type.go:403` 声明 `type interfacetype = abi.InterfaceType`，定义在 `internal/abi/type.go:447-451`：`{Type; PkgPath Name; Methods []Imethod}`，书中 `mhdr` 改名为 `Methods`。

### 两幅“指向图”

看两个例子，把上面的结构画成指向图。第一个是空接口：

```go
type T struct {
	n int
	s string
}

var ei interface{} = t
```

运行时用 `eface` 表示：`_type` 字段指向动态类型 T 的类型信息，`data` 指向一个 T 类型的实例值。〔TB29 p7-8〕第二个例子给 T 增加两个方法，并定义一个含这两个方法的接口：

```go
func (t T) M1() {}
func (t T) M2() {}

type NonEmptyInterface interface {
	M1()
	M2()
}

var i NonEmptyInterface = t
```

这次运行时用 `iface` 表示，结构比 `eface` 复杂：`tab` 指向的 `itab` 里同时存着接口自身的信息（`interfacetype`：类型信息、包路径、方法集合）、动态类型的类型信息（`_type`）、哈希，以及 `fun` 数组中动态类型所实现方法的地址；`data` 依旧指向 T 的实例值。〔TB29 p8-9〕

统一到记法上：每个接口变量的运行时表示由两部分组成，即 `eface(_type, data)` 与 `iface(tab, data)`。`data` 指向一块动态分配的内存空间，存储赋给接口变量的动态类型变量的值；未显式初始化的接口变量值为 nil，即 `_type`/`tab` 和 `data` 都为 nil。〔TB29 p9〕

理解了内部表示，回头看 `returnsError` 陷阱：`p` 是值为 nil 的 `*MyError` 指针，装入 `error` 接口后得到一个 `iface`，`tab` 指向 `*MyError` 的类型信息（非空），`data` 为 nil。所以 `err` 不是“两个字段都为空”的 nil 接口，与 nil 比较自然为 false。修复办法也不止一种：让 `returnsError` 直接返回 nil 接口，或把 `p` 的类型改为 `error`，删除 `p`，按需 `return &ErrBad` 或 nil。不要把一个 nil 的具体类型指针装进接口再返回。

## 五、接口值的比较和成本

内部表示清楚了，接口值的等值比较规则就顺理成章：两个接口值相等，要求类型信息一致，且 data 所指的数据内容一致。下面按原课的顺序过一遍四种等值比较情形。原课输出中的地址是它运行环境的具体值，在你机器上会不同；要看的是相同与不同的关系。〔TB29 p10-15〕顺带说明输出格式：运行时 `println` 一个接口变量，会打印它内部表示的两个字段。以本机 go1.26.4 源码为例，`$GOROOT/src/runtime/print.go:266-272` 中 `printeface` 打印 `(_type, data)`，`printiface` 打印 `(tab, data)`，原课示例输出 `(0x0,0x0)` 这样的二元组正来自这里。

**第一种：nil 接口变量。**未赋初值的接口类型变量值为 nil。〔TB29 p10-11〕

```go
var i interface{} // 空接口类型
var err error     // 非空接口类型

println(i)
println(err)
println("i = nil:", i == nil)
println("err = nil:", err == nil)
println("i = err:", i == err)
```

原课运行输出：〔TB29 p11〕

```text
(0x0,0x0)
(0x0,0x0)
i = nil: true
err = nil: true
i = err: true
```

无论是空接口还是非空接口变量，值为 nil 时内部表示均为 `(0x0,0x0)`：类型信息与数据信息均为空。所以 `i` 与 `err` 相等，也都等于字面量 nil；nil 状态下空接口与非空接口的内部表示没有区别。

**第二种：空接口类型变量之间。**〔TB29 p11-12〕

```go
func printEmptyInterface() {
	var eif1 interface{} // 空接口类型
	var eif2 interface{} // 空接口类型
	var n, m int = 17, 18
	eif1 = n
	eif2 = m
	println("eif1:", eif1)
	println("eif2:", eif2)
	println("eif1 = eif2:", eif1 == eif2) // false
	eif2 = 17
	println("eif1:", eif1)
	println("eif2:", eif2)
	println("eif1 = eif2:", eif1 == eif2) // true
	eif2 = int64(17)
	println("eif1:", eif1)
	println("eif2:", eif2)
	println("eif1 = eif2:", eif1 == eif2) // false
}
```

原课运行输出：〔TB29 p12〕

```text
eif1: (0x10ac580,0xc00007ef48)
eif2: (0x10ac580,0xc00007ef40)
eif1 = eif2: false
eif1: (0x10ac580,0xc00007ef48)
eif2: (0x10ac580,0x10eb3d0)
eif1 = eif2: true
eif1: (0x10ac580,0xc00007ef48)
eif2: (0x10ac640,0x10eb3d8)
eif1 = eif2: false
```

三步分析：

- 第一步：`eif1` 与 `eif2` 分别装入 int 的 17 与 18。两者的 `_type` 相同（都是 int 的类型信息 `0x10ac580`），但 `data` 所指内存块中存储的值不同（17 与 18），不相等。
- 第二步：`eif2` 重新装入 17。`_type` 仍相同，`data` 所指的值也相同（都是 17），相等。注意这一轮两个 `data` 指针并不相同（`0xc00007ef48` 与 `0x10eb3d0`），说明比较的是 `data` 所指的数据值，不是 `data` 指针本身的值。
- 第三步：`eif2` 装入 int64 类型的 17。动态类型变了（`_type` 变成 int64 的 `0x10ac640`），即便 `data` 所指的值仍是 17，两者也不相等。

结论：对空接口类型变量，只有 `_type` 与 `data` 所指数据内容都一致时才能划等号；另外 Go 创建 eface 时一般会为 data 重新分配内存空间，把动态类型变量的值复制进去，所以多数情况下看到的 data 指针值都不同。〔TB29 p12〕

**第三种：非空接口类型变量之间。**〔TB29 p13-14〕

```go
type T int

func (t T) Error() string {
	return "bad error"
}

func printNonEmptyInterface() {
	var err1 error // 非空接口类型
	var err2 error // 非空接口类型
	err1 = (*T)(nil)
	println("err1:", err1)
	println("err1 = nil:", err1 == nil)
	err1 = T(5)
	err2 = T(6)
	println("err1:", err1)
	println("err2:", err2)
	println("err1 = err2:", err1 == err2)
	err2 = fmt.Errorf("%d\n", 5)
	println("err1:", err1)
	println("err2:", err2)
	println("err1 = err2:", err1 == err2)
}
```

原课运行输出：〔TB29 p13〕

```text
err1: (0x10ed120,0x0)
err1 = nil: false
err1: (0x10ed1a0,0x10eb310)
err2: (0x10ed1a0,0x10eb318)
err1 = err2: false
err1: (0x10ed1a0,0x10eb310)
err2: (0x10ed0c0,0xc000010050)
err1 = err2: false
```

与空接口一样，只有 tab 与 data 所指数据内容都一致时，两个非空接口变量才能划等号。重点看 `err1 = (*T)(nil)` 这一步：println 输出 `(0x10ed120,0x0)`，类型信息非空、数据指针为空，因此它与 nil（`(0x0,0x0)`）不能划等号。这正是上一节 `returnsError` 陷阱的机制：`err1` 装的是一个“类型信息不为空、值为 nil”的具体指针。〔TB29 p14〕

`err1 = T(5)`、`err2 = T(6)` 一轮：tab 相同（`0x10ed1a0`，都是 T 的类型信息），data 所指的值不同（5 与 6），比较为 false；`err2` 换成 `fmt.Errorf` 构造的动态类型后，tab 与 data 所指数据都不同，仍为 false。

**第四种：空接口与非空接口变量之间的等值比较。**〔TB29 p14-15〕

```go
func printEmptyInterfaceAndNonEmptyInterface() {
	var eif interface{} = T(5)
	var err error = T(5)
	println("eif:", eif)
	println("err:", err)
	println("eif = err:", eif == err)
	err = T(6)
	println("eif:", eif)
	println("err:", err)
	println("eif = err:", eif == err)
}
```

原课运行输出：〔TB29 p14-15〕

```text
eif: (0x10b3b00,0x10eb4d0)
err: (0x10ed380,0x10eb4d8)
eif = err: true
eif: (0x10b3b00,0x10eb4d0)
err: (0x10ed380,0x10eb4e0)
eif = err: false
```

空接口与非空接口的内部表示结构不同（第一字段一个是 `_type`、一个是 tab），似乎永远不可能相等。但 Go 在进行等值比较时，类型比较使用的是 eface 的 `_type` 与 iface 的 `tab._type`：`eif` 与 `err` 都装入 `T(5)` 时，`_type` 与 `tab._type` 一致、data 所指数据一致，两者相等；`err` 改装 `T(6)` 后 data 所指数据不同，比较为 false。〔TB29 p15〕

四种情形合起来就是一条规则：接口值的等值比较先比类型信息（空接口看 `_type`，非空接口看 `tab._type`），再比 data 所指的数据内容；两者都一致才算相等。〔TB29 p9-13〕跨空/非空比较时，类型比较用的是 `_type` 对 `tab._type`，这条规则同样成立。〔TB29 p14-15〕顺带一提，`println` 打印接口变量内部表示的两个字段，具体函数是 `$GOROOT/src/runtime/print.go` 的 `printeface`/`printiface`（266-272 行，打印 `(_type, data)`/`(tab, data)` 二元组）；本节开头已交代过这一打印路径，此处不再展开。〔TB29 p10〕

比较还有一条边界要注意：如果两个接口的动态类型相同，但该类型不可比较（例如 slice），比较时会 panic；因此不能把任意接口值都当成可安全比较的值。

`returnsError` 陷阱的机制在第四节末尾已经拆过：nil 的 `*MyError` 指针装入接口后得到 `(tab, 0x0)`，`tab` 非空而 `data` 为 nil，与 nil（内部表示 `(0x0,0x0)`）比较自然为 false。〔TB29 p14〕原课的思考题有两问：这个 nil error 值 != nil 的坑你在日常编码中遇到过吗；本讲的 `returnsError` 例子如何修改才能按最初预期输出。正文只提出问题、未给答案，修改方案出自精选留言：Geralt of Rivia 给出两种改法，其一，把 `returnsError()` 里 `p` 的类型改为 `error`；其二，删除 `p`，直接 `return &ErrBad` 或 nil；i6u 用另一种表述点出问题本质：返回的是 `*MyError` 而不是 `error`，应返回接口类型。〔TB29 p24-25〕本机 go1.26.4 工具链复测与上述一致：`(*T)(nil)` 装接口后 `println` 输出 `(0x…,0x0)`，`err != nil` 为 true；`eface` 与 `iface` 同装 `T(5)` 时 `eif == err` 为 true。

**选读：输出接口内部表示的详尽信息。**`println` 只给两个指针值，有时过于简略：仅凭 `eif: (0x10b3b00,0x10eb4d0)` 与 `err: (0x10ed380,0x10eb4d8)`，想不到两个变量相等；若能输出 `tab._type` 一类的详细信息会事半功倍。〔TB29 p15〕eface、iface 与组成它们的 itab、`_type` 都是 runtime 包的非导出结构体，包外不能直接引用；但组成它们的基本数据类型可以复制，所以用“复制代码”的方式把结构定义拿到 runtime 包外，再借 unsafe.Pointer 强转读取内部数据，这就是原课的 dumpinterface.go。〔TB29 p15-16〕三个主要函数各管一段：`dumpEface` 输出空接口的 eface 与 `_type` 信息，data 部分按 type switch 只对 int、float64、T 三种类型读取；`dumpItabOfIface` 输出非空接口的 iface 与整个 itab，并展开 inter、`_type`，以及按接口方法数（mhdr 长度）逐个打印 fun 里的调用地址；`dumpDataOfIface` 输出非空接口的 data 所指内容，靠“eface 与 iface 的 data 部分相同”这一技巧，把参数统一按 eface 读。〔TB29 p16-18〕对同装 T(5) 的 eif 与 err，输出显示 eif 的 `_type`（0x10b38c0）与 err 的 tab._type（0x10b38c0）一致、data 所指内容同为 “bad error”，解释了 `eif == err` 为 true；原课同时说明 data 读取只实现了这三种类型，其余类型要自己补。〔TB29 p18-19〕版本边界：runtime 的 eface、iface 或其组成可能随 Go 版本变化，原课在引入时就说，基于 Go 1.17 复制的代码可能仅适用于 Go 1.17 编译〔TB29 p15〕，结尾又强调这个实现可能仅在 Go 1.17 版本上测试通过〔TB29 p19〕。本机复测中，照书复制的 Go 1.17 结构在 go1.26.4 编译运行成功，hash/kind/fun[0] 输出正确，eface._type.hash 与 iface.tab.hash 同为 0x9eab503f（与原课输出的 1156555957 不同属预期，不同版本哈希不同），但这不构成跨版本兼容性保证。

### 装箱：接口赋值的运行时过程

装箱（boxing）是编程语言领域的基础概念，一般指把一个值类型转换成引用类型；原课给出的类比是 Java 中把 int 变量转换成 Integer 对象，这也是原课全文唯一一处 Java 内容。〔TB29 p20〕在 Go 语言中，将任意类型赋值给接口类型变量同样是装箱操作；有了前面对内部表示的学习，这一过程实际就是创建一个 eface 或 iface。〔TB29 p20〕类比边界要说清：Java 的 Integer 是带对象头与身份语义的完整对象，Go 的装箱只是为接口值准备运行时所需的两份信息，即类型信息与 data 指针；两者只在“值类型转向引用类型”这个方向上相似，不要把 Java 自动装箱的细节直接搬进 Go。

用原课的例子看两次装箱：

```go
// interface_internal.go

type T struct {
	n int
	s string
}

func (T) M1() {}
func (T) M2() {}

type NonEmptyInterface interface {
	M1()
	M2()
}

func main() {
	var t = T{
		n: 17,
		s: "hello, interface",
	}
	var ei interface{}
	ei = t

	var i NonEmptyInterface
	i = t
	fmt.Println(ei)
	fmt.Println(i)
}
```

此例的 `T` 是带 `M1`/`M2` 方法的结构体，与比较、dump 例子里带 `Error` 方法的 `type T int` 不是同一个类型。`ei = t` 与 `i = t` 两个赋值都会触发装箱。〔TB29 p20〕

想知道 Go 在背后做了什么，把代码“下沉”一层看汇编。原课用 `go tool compile -S interface_internal.go` 输出汇编：`ei = t` 一行的关键指令是 `LEAQ type."".T(SB), AX`（把 T 的类型信息地址装入 AX）与 `CALL runtime.convT2E(SB)`；`i = t` 一行是 `LEAQ go.itab."".T,"".NonEmptyInterface…(SB)`（装入 itab 地址）与 `CALL runtime.convT2I(SB)`。〔TB29 p21〕两条语句各自先把常量 17、字符串地址与长度 MOVQ/LEAQ 进栈上的临时位置（autotmp_15），再把这个临时位置取地址作为第二参数传入：常量输入在编译期就被静态化到临时对象里。〔TB29 p21〕

convT2E/convT2I 是 Go 1.15 时代的 runtime 符号，现行工具链已移除：本机 go1.21.5、go1.26.4、go1.27.3 的 runtime 全树都搜不到这两个函数。这一符号归属来自对工具链源码的核对，不是原课内容；原课全文未标版本号，唯二的版本提法是 dump 工具处的 Go 1.17。现行 `iface.go`（行号取自本机 go1.26.4 源码）提供的是另一组函数：`convT`（334 行，`mallocgc` 加 `typedmemmove`）、`convTnoptr`（348 行，`mallocgc` 加 `memmove`，不含指针的类型免指针扫描）、`convT16`（365 行）、`convT32`（378 行）、`convT64`（400 行）、`convTstring`（419 行）、`convTslice`（438 行），都在 `$GOROOT/src/runtime/iface.go`。

原课贴出两个转换函数的实现（Go 1.17 时代源码），convT2E 用于把任意类型转成一个 eface，convT2I 用于转成一个 iface：

```go
// $GOROOT/src/runtime/iface.go

func convT2E(t *_type, elem unsafe.Pointer) (e eface) {
	if raceenabled {
		raceReadObjectPC(t, elem, getcallerpc(), funcPC(convT2E))
	}
	if msanenabled {
		msanread(elem, t.size)
	}
	x := mallocgc(t.size, t, true)
	typedmemmove(t, x, elem)
	e._type = t
	e.data = x
	return
}

func convT2I(tab *itab, elem unsafe.Pointer) (i iface) {
	t := tab._type
	if raceenabled {
		raceReadObjectPC(t, elem, getcallerpc(), funcPC(convT2I))
	}
	if msanenabled {
		msanread(elem, t.size)
	}
	x := mallocgc(t.size, t, true)
	typedmemmove(t, x, elem)
	i.tab = tab
	i.data = x
	return
}
```

按顺序拆 convT2E 的四个步骤：第一步 `mallocgc(t.size, t, true)` 按类型信息 t 分配一块大小为 t.size 的内存；第二步 `typedmemmove(t, x, elem)` 把 elem 指向的数据按类型 t 拷贝进这块新内存；第三步 `e._type = t` 填入 eface 的类型信息；第四步 `e.data = x` 让 data 指向新分配的内存。开头的 raceenabled/msanenabled 检查只在相应检测工具开启时读一次对象内存，不在装箱主路径上。〔TB29 p21-22〕convT2I 与它只有两点差别：类型信息不直接传入，而是先 `t := tab._type` 从 itab 里取；返回时填 `i.tab = tab` 与 `i.data = x`。分配与拷贝的思路完全一致。〔TB29 p22〕

装箱后，箱内数据存放在新分配的内存里，与原变量再无瓜葛。原课例子：

```go
func main() {
	var n int = 61
	var ei interface{} = n
	n = 62       // n的值已经改变
	fmt.Println("data in box:", ei) // 输出仍是61
}
```

`n = 62` 改的是 n 自己那块内存；`ei` 的 data 指向装箱时新分配的另一块内存，里面仍是 61，所以打印 `data in box: 61`。〔TB29 p22〕本机 go1.26.4 工具链复测：n 改为 62 后箱内仍是 61，与原课一致。

那么这些转换函数的类型信息从何而来？依赖编译器：编译器知道每个要转换为的接口类型（toType）与动态类型变量的类型（fromType）这一对类型，并根据它们选择适当的 convT2X 函数参与装箱操作。〔TB29 p23〕换句话说，具体调用哪个转换函数在编译期就已定好，运行时只负责执行。

### 装箱的成本与优化（选读）

Go 对装箱的优化主要体现在转换函数与静态数据上。本机复测（arm64，go1.26.4，`go build -gcflags=-S`）：int64 装箱走 `CALL runtime.convT64`，含指针的结构体走 `CALL runtime.convT`，无指针的数组走 `CALL runtime.convTnoptr`，uint16 走 `CALL runtime.convT16`，string 走 `CALL runtime.convTstring`；常量输入在编译期被静态化为 `.stmp_0`。这正是编译器按 toType/fromType 挑选转换函数的体现。〔TB29 p23〕

对常见类型如整型、字符串、切片等，runtime 提供系列快速转换函数，原课列出五个：`convT16`（val must be uint16-like）、`convT32`（val must be uint32-like）、`convT64`（val must be uint64-like）、`convTstring`（val must be a string）、`convTslice`（val must be a slice）；相比 convT2E/convT2I，它们去除了 typedmemmove 操作，增加了零值快速返回等特性。〔TB29 p23〕

零值与小整数是优化重点。原课引文里 staticuint64s 是字面量数组：`var staticuint64s = [...]uint64{0x00, 0x01, ...}`，注释 “staticuint64s is used to avoid allocating in convTx for small integer value”，作用是对 255 以内的小整数值装箱时不再分配新内存，直接利用这片区域的内存空间。〔TB29 p23〕这一字面量形态对应更早版本，如本机 go1.21.5 的 `iface.go:493-494` 仍是同样的字面量；现行形态已变：go1.26.4 的 `iface.go:696-697` 声明 `var staticuint64s [256]uint64`，注释保留原句并追加 “It is defined in assembler code so that it is read-only”，数据由 `runtime/ints.s` 的 DATA 指令填入，GLOBL 行把它标记为 RODATA 只读段。

以 convT64 为例看分支（go1.27.3 的 `iface.go:418-425`；go1.21.5 的对应实现在 379-386，分支行为一致）：val 小于 256（即 `len(staticuint64s)`）时，直接返回 `&staticuint64s[val]`，免分配完成装箱；否则走 `mallocgc(8, uint64Type, false)` 分配 8 字节再写入 val。convT16 与 convT32 同思路，大端平台上另有偏移调整（16 位加 6、32 位加 4）；convTstring 对空串、convTslice 对 nil 数组都直接返回 `&zeroVal[0]`，这就是零值快速返回。

最后核算成本：接口赋值可能发生复制或堆分配，具体取决于类型、使用方式与编译器优化，不能仅凭“用了接口”断定一定分配；装箱由 Go 编译器和运行时共同完成，有一定的性能开销，对性能敏感的系统应尽量避免或减少这类装箱操作，动手前先用基准测试和性能分析确认。〔TB29 p21-24〕

## 六、嵌入：复用方法，但不是继承

除了通过接口替换行为，程序还经常需要复用已有实现。Go 可以在结构体中**嵌入**另一个类型：写类型名而不另起字段名。被嵌入的值仍是一个字段；在没有名称冲突时，外层可以直接访问它的字段和方法。例如 `s.Read` 可以表示 `s.Reader.Read`。这种方法提升方便了访问，但不建立父子类型关系。〔TB26 p9-10〕

### 接口嵌入：基于已有接口构建新接口

原课把"在一个类型的定义中嵌入其他类型"统称为类型嵌入，并区分两种形式：接口类型的类型嵌入与结构体类型的类型嵌入。上一段说的是结构体一侧，这里先看两种里更简单的一种：接口嵌入接口；结构体一侧随后展开。〔TB26 p2〕

先用一对接口直观感受。`E` 声明两个方法：

```go
type E interface {
	M1()
	M2()
}
```

再定义一个方法集合为 M1、M2、M3 的接口 `I`。平铺的写法是逐个列出三个方法；由于其中的 M1、M2 与 `E` 的方法完全相同，也可以把这两个方法换成嵌入 `E`，写成 `type I interface { E; M3() }`。带嵌入的定义与平铺 M1、M2、M3 的定义等价：接口类型嵌入的语义，就是新接口把嵌入接口的方法集合并入自己的方法集合。〔TB26 p2-3〕

那为什么不在接口里平铺方法列表？按 Go 惯例，接口只包含少量方法，常常只有一个。方法一多，平铺的定义既难读，也无法复用已有接口；嵌入让新接口站在已有接口之上组合而成。这是 Go 基于已有接口类型构建新接口类型的惯用法，也是组合设计哲学的体现。〔TB26 p3-4〕

标准库里最典型的组合在 `io` 包。本章第二节已经用过 `io.Writer`，它就是只含一个方法的基本接口；`io` 包用 Reader、Writer、Closer 这三个单方法接口组合出一系列大接口：〔TB26 p4〕

```go
// $GOROOT/src/io/io.go

type Reader interface {
	Read(p []byte) (n int, err error)
}

type Writer interface {
	Write(p []byte) (n int, err error)
}

type Closer interface {
	Close() error
}

type ReadWriter interface {
	Reader
	Writer
}

type ReadCloser interface {
	Reader
	Closer
}

type WriteCloser interface {
	Writer
	Closer
}

type ReadWriteCloser interface {
	Reader
	Writer
	Closer
}
```

`io.ReadWriter` 由 Reader 和 Writer 嵌入而成，方法集合就是 Read 和 Write 两个方法；要一个同时支持读、写、关闭的接口，就照 `ReadWriteCloser` 的写法把三个基本接口都嵌入进去。

这种组合方式有一段历史约束要交代。Go 1.14 之前，新接口嵌入多个接口类型时有两条限制：各嵌入接口的方法集合不能有交集；嵌入接口方法集合里的方法名，也不能与新接口中其他方法同名。违反任意一条，编译器都报 duplicate method：〔TB26 p5〕

```go
type Interface1 interface {
	M1()
}

type Interface2 interface {
	M1()
	M2()
}

type Interface3 interface {
	Interface1
	Interface2 // Error: duplicate method M1
}

type Interface4 interface {
	Interface2
	M2() // Error: duplicate method M2
}

func main() {
}
```

这段代码在 Go 1.12.7 下无法编译。两处报错的原因不同：Interface3 是因为嵌入的 Interface1 与 Interface2 方法集合有交集，交集是 M1；Interface4 是因为它自己的方法 M2，与嵌入接口 Interface2 里的方法 M2 重名。自 Go 1.14 起，这些约束被去除了：原作者用 Go 1.17 运行同一示例不再得到编译错误；本机 go1.26.4（darwin/arm64）也实测复核过，Interface1 与 Interface2 这两个都含 M1 的接口嵌入后编译通过，没有 duplicate method 报错。约束解除放松的是编译检查，"方法集合并入"的语义本身没有变化。原课收束说，接口类型的类型嵌入比较简单，把握好"方法集合并入"这个语义即可。〔TB26 p5〕

关键差别要用代码看：

```go
package main

import "fmt"

type Base struct{ Name string }

func (b Base) Hello() string { return "hi " + b.Who() }
func (b Base) Who() string   { return b.Name }

type Child struct{ Base }

func (c Child) Who() string { return "child" }

// 想要"子类重写生效"：把可变部分作为接口参数传进去
type Whoer interface{ Who() string }

func Hello(w Whoer) string { return "hi " + w.Who() }

func main() {
	c := Child{Base{"base"}}
	fmt.Println(c.Hello(), "|", c.Who())
	fmt.Println(Hello(c))
}
```

```text
hi base | child
hi child
```

`c.Hello()` 实际调用 `Base.Hello`，接收者是 `c.Base`。因此，方法内部的 `b.Who()` 调用的是 `Base.Who`；外层定义的 `Child.Who` 不会改变它。相比之下，独立函数 `Hello` 接受 `Whoer` 接口，传入 `c` 后调用的是 `Child.Who`。接口调用可以实现多态，嵌入本身不提供继承式重写。

方法调用的绑定不受嵌入影响，方法集合却会因嵌入而扩充。嵌入对类型的要求两边不一样：接口类型只能嵌入接口类型，结构体则既可以嵌入自定义类型，也可以嵌入接口类型。用本章第三节定义过的 `dumpMethodSet` 看看结构体嵌入接口类型后会发生什么，其中 `T` 自身只声明了方法 M3：〔TB26 p10-11〕

```go
type I interface {
	M1()
	M2()
}

type T struct {
	I
}

func (T) M3() {}

func main() {
	var t T
	var p *T
	dumpMethodSet(t)
	dumpMethodSet(p)
}
```

```text
main.T's method set:
- M1
- M2
- M3

*main.T's method set:
- M1
- M2
- M3
```

`T` 原本只有 M3，嵌入 `I` 后，`I` 的方法集合并入了 `T` 的方法集合；又因为 `*T` 的方法集合包括 `T` 的方法集合（第三节刚得出的规则），所以 `T` 与 `*T` 的方法集合都包含 M1、M2、M3。结论：**结构体类型的方法集合，包含嵌入的接口类型的方法集合**。按第三节的判定条件，`T` 和 `*T` 便都实现了要求这三个方法的接口，尽管 `T` 对 M1、M2 一行实现代码都没写。本机 go1.26.4 实测输出与上面一致，两个类型各 3 个方法。〔TB26 p11〕那如果嵌入的多个接口方法集合出现交集、结构体自己又没实现交集里的方法，调用时会怎样？这个问题留到后面展开。

### 结构体嵌入：字段、提升与代理

接口嵌入把方法集合并入新接口，接口自身不需要任何实现；结构体嵌入则把一个既有类型整个装进结构体。与只能嵌入接口类型的接口嵌入不同，结构体可以嵌入任意自定义类型或接口类型〔TB26 p10〕，实现层面的复用正是从这里开始的。

**嵌入字段怎么声明？**把类型名、类型的指针类型名或接口类型名直接写进结构体定义，不另起字段名，这个标识符就既是字段名，也是字段类型，这样的字段叫嵌入字段（Embedded Field）〔TB26 p6-7〕：

```go
type T1 int

type t2 struct {
	n int
	m int
}

type I interface {
	M1()
}

type S1 struct {
	T1  // 字段名 T1，类型为自定义类型 T1
	*t2 // 字段名 t2，类型为 *t2
	I   // 字段名 I，类型为接口类型 I
	a int
	b string
}
```

`S1` 的前三个字段没有自己的名字：`T1` 表示字段名为 `T1`、类型为 `T1`；`*t2` 表示字段名为 `t2`、类型为 `*t2`；`I` 表示字段名为 `I`、类型为接口 `I`。后面的 `a`、`b` 是普通命名字段，放在一起作对照。现行语言规范把这两种合法写法固定成了规则：嵌入字段要么写成类型名 `T`，要么写成指向非接口类型名的指针 `*T`，且 `T` 自身不能是指针类型（本机 go1.26.4 工具链 `doc/go_spec.html:1082-1086`）。

嵌入类型来自其他包时，字段名取类型名本身、去掉包限定符：嵌入 `io.Reader` 的字段名是 `Reader` 而不是 `io.Reader`，它代表的类型才是 `io.Reader`；嵌入 `*strings.Reader` 的字段名同样是 `Reader`，本机实测用 `%T` 打印 `s.Reader`，输出 `*strings.Reader`〔TB26 p8〕。嵌入字段的可见性与类型的可见性一致：嵌入类型名字首字母大写，这个嵌入字段就是可导出的〔TB26 p8〕。

声明之后，嵌入字段在初始化和访问上与普通字段同法，马上用例子验证。不过先看两条硬约束，违反了编译器会直接报错〔TB26 p8〕：

- 嵌入字段类型的底层类型不能为指针类型。原课把它与 Go 方法 receiver 的基类型约束并提：`type P *T` 之后，`P` 不能写进结构体定义。本机 go1.26.4 实测报错 `embedded field type cannot be a pointer`，检查位于编译器源码 `src/cmd/compile/internal/types2/struct.go:160`。
- 嵌入字段的名字在结构体定义内必须唯一，因此两个名字相同的类型无法同时作为嵌入字段放进同一个结构体：`T1` 与 `*T1` 的嵌入字段名都是 `T1`，同时嵌入会报 `T1 redeclared`（实测报错位于 `src/cmd/compile/internal/types2/struct.go:204`）。

原课对这两条的定位是“了解一下就可以”：它们都是编译期检查的硬约束，一旦违反编译器会提示，不涉及运行期行为。

现在看完整的用法。`MyInt` 在 `*MyInt` 上声明了 `Add` 方法，结构体 `t` 有两个 `int` 字段，`S` 把它们连同 `io.Reader` 一起嵌入（为能独立运行，补全了 `package main` 与 import，原课文清单只展示了类型定义与 `main` 函数）：

```go
package main

import (
	"fmt"
	"io"
	"strings"
)

type MyInt int

func (n *MyInt) Add(m int) {
	*n = *n + MyInt(m)
}

type t struct {
	a int
	b int
}

type S struct {
	*MyInt
	t
	io.Reader
	s string
	n int
}

func main() {
	m := MyInt(17)
	r := strings.NewReader("hello, go")
	s := S{
		MyInt:  &m,
		t:      t{
			a: 1,
			b: 2,
		},
		Reader: r,
		s:      "demo",
	}

	var sl = make([]byte, len("hello, go"))
	s.Reader.Read(sl)
	fmt.Println(string(sl))
	s.MyInt.Add(5)
	fmt.Println(*(s.MyInt))
}
```

运行输出：

```text
hello, go
22
```

`S` 有三个嵌入字段 `MyInt`、`t`、`Reader` 和两个普通字段 `s`、`n`。定义里没有出现 `Reader` 这个名字，但复合字面量里却写了 `Reader: r`：这正是上面说的跨包命名规则，`io.Reader` 嵌入后的字段名就是 `Reader`。初始化直接用嵌入字段名作 field，与普通字段的 `s: "demo"` 写法完全一样；没有列出的 `n` 保持零值。访问也相同：`s.Reader`、`s.MyInt` 就是变量加选择符 `.` 加嵌入字段名。`s.Reader.Read(sl)` 把 `r` 里的字符串读进 `sl`，打印 `hello, go`；`s.MyInt.Add(5)` 之后 `*(s.MyInt)` 打印 `22`，`m` 的值从 17 变成了 22。注意 `Add` 声明在 `*MyInt` 上，所以必须经由嵌入的指针字段 `s.MyInt` 调用：`S` 内部持有指向 `m` 的指针，`Add` 修改的正是外部变量 `m` 所指的值〔TB26 p7-8〕。

到这里，嵌入字段的用法和普通字段看起来差别不大。真正的差别在调用上：把 `main` 里的显式调用换成直接通过 `s` 调用，只列变化的部分：

```go
var sl = make([]byte, len("hello, go"))
s.Read(sl)
fmt.Println(string(sl))
s.Add(5)
fmt.Println(*(s.MyInt))
```

类型 `S` 自己并没有声明 `Read` 和 `Add`，直觉上这段代码应该无法通过编译。实际恰恰相反：它不但没有编译错误，还能正常运行，输出与显式调用版本完全相同的 `hello, go` 和 `22`〔TB26 p8-9〕。

原因是编译器在背后做了一次转换。通过 `S` 的变量 `s` 调用 `Read` 时，Go 先发现类型 `S` 自身没有定义 `Read` 方法，于是查看 `S` 的嵌入字段对应的类型是否定义了 `Read`；找到 `Reader` 字段后，`s.Read` 的调用就被转换为 `s.Reader.Read`。`Add` 同理，`s.Add(5)` 等价于 `s.MyInt.Add(5)`。本机 go1.26.4（darwin/arm64）实测，`s.Read(sl)` 与 `s.Add(5)` 编译运行正常，输出与显式版本一致。这样一来，嵌入字段 `Reader` 的 `Read` 方法就被提升为 `S` 的方法，放进类型 `S` 的方法集合；`*MyInt` 的 `Add` 方法同理也被提升放进 `S` 的方法集合。〔TB26 p9〕从外部看，这种提升给人一种 `S` “继承”了 `io.Reader` 的 `Read` 实现、`*MyInt` 的 `Add` 实现的错觉。注意“继承”要加引号：这不是真正的继承，只是 Go 语言的一种“障眼法”〔TB26 p9〕。

这层障眼法的实质，是组合思想中的代理（delegate）模式：`S` 只是一个代理，对外提供它能代理的所有方法，比如例子里的 `Read` 和 `Add`；外界对 `S` 的 `Read` 方法发起调用后，`S` 把调用委派给它内部的 `Reader` 实例实际执行〔TB26 p10〕。嵌入字段的类型不同，`S` 能代理的方法就不同，这就引出一个更实用的问题：嵌入字段对结构体的方法集合究竟有哪些影响。

### 冲突、歧义与测试伪对象

接口嵌入小节已经实测过单个嵌入接口的情形：`T` 嵌入只声明 M1、M2 的接口 `I`，自身只定义值接收者的 M3，`dumpMethodSet` 显示 `T` 与 `*T` 的方法集合都包含 M1、M2、M3，结论是结构体类型的方法集合包含嵌入接口类型的方法集合。〔TB26 p11〕那如果嵌入的多个接口方法集合出现交集、结构体自身又没实现交集里的方法，调用会发生什么？这正是接口嵌入小节末尾留下的问题，现在兑现。回答它要用到刚讲完的代理机制：经提升的方法被调用时，Go 先查看结构体自身是否实现了该方法，实现了就优先使用自身实现；没有实现，才去查找各嵌入字段的方法集合。若多个嵌入字段的方法集合都包含这个方法，编译器无法确定究竟代理给谁，就会报 `ambiguous selector` 编译错误。〔TB26 p12〕原课的例子：`E1` 声明 M1、M2、M3，`E2` 声明 M1、M2、M4，`T` 同时嵌入两者：

```go
package main

type E1 interface {
	M1()
	M2()
	M3()
}

type E2 interface {
	M1()
	M2()
	M4()
}

type T struct {
	E1
	E2
}

func main() {
	t := T{}
	t.M1()
	t.M2()
}
```

```text
main.go:22:3: ambiguous selector t.M1
main.go:23:3: ambiguous selector t.M2
```

成因在于 E1 与 E2 的方法集合交集是 M1、M2，而 T 自身没有实现它们，编译器无法做出选择。报错行号取决于文件的排布，本机 go1.26.4（darwin/arm64）实测同样得到两行 `ambiguous selector`。修复有两种：一是消除交集，让 E1 与 E2 的方法集合不再重叠；二是为 T 自身增加 M1、M2 的实现。〔TB26 p13〕采用第二种，E1、E2 的定义沿用上一个例子：

```go
type T struct {
	E1
	E2
}

func (T) M1() {
	println("T's M1")
}

func (T) M2() {
	println("T's M2")
}

func main() {
	t := T{}
	t.M1() // T's M1
	t.M2() // T's M2
}
```

```text
T's M1
T's M2
```

自身实现命中了查找顺序的第一步，根本轮不到在嵌入字段之间做选择。本机实测还确认：自身实现写成指针接收者同样生效，值类型变量调用指针接收者的自身方法时，经自动取地址的语法糖照样成立，自身方法始终优先于嵌入代理。版本边界也要分清：前面接口嵌入小节提到的 Go 1.14 放宽，仅限于接口类型中嵌入接口类型；在结构体类型中嵌入方法集合有交集的接口类型，仍然会碰到上面的 `ambiguous selector`。〔TB26 p12-13〕

结构体嵌入接口类型在日常编码中还有一个妙用：简化单元测试的编写。原课的场景在 employee 包：Stmt 接口声明 Close、NumInput、Exec、Query 四个方法，MaleCount(s Stmt) 查询男性员工数，只用到其中的 Exec。如果每个测试伪对象都要实现这四个方法，工作量有些大；惯例是使用"伪对象（fake object）"来冒充真实的 Stmt 接口实现。〔TB26 p13-14〕

做法是让伪对象嵌入 Stmt 接口：嵌入之后，这个结构体的方法集合就包含了 Stmt 的全部四个方法，即便自身一个方法都不实现，编译期也算实现了 Stmt。因此伪对象只需覆写 MaleCount 用到的 Exec，返回 (Result{Count: 5}, nil) 即可满足测试。〔TB26 p14-15〕原例是 employee 包的测试 TestEmployeeMaleCount：构造 f := fakeStmtForMaleCount{}，调用 c, _ := MaleCount(f)，断言 c != 5 时调用 `t.Errorf("want: %d, actual: %d", 5, c)`。为能独立运行，下面放进 main 包，用 useStmt(s Stmt) int 承担 MaleCount 的角色，并补全原课在 gender=? 处被截断的 Exec 变参：

```go
package main

import "fmt"

type Result struct {
	Count int
}

func (r Result) Int() int { return r.Count }

type Rows []struct{}

type Stmt interface {
	Close()
	NumInput() int
	Exec(stmt string, args ...string) (Result, error)
	Query(args []string) (Rows, error)
}

// useStmt 承担原例 MaleCount 的角色：查询男性员工数，只用到 Exec。
func useStmt(s Stmt) int {
	result, err := s.Exec("select count(*) from employee_tab where gender=?", "M")
	if err != nil {
		return 0
	}
	return result.Int()
}

type fakeStmtForMaleCount struct {
	Stmt // 嵌入接口，方法集合即覆盖 Stmt 的全部四个方法
}

func (fakeStmtForMaleCount) Exec(stmt string, args ...string) (Result, error) {
	return Result{Count: 5}, nil
}

func main() {
	var f fakeStmtForMaleCount
	fmt.Println(useStmt(f)) // 5
}
```

运行输出 5，来自被覆写的 Exec；Close、NumInput、Query 一个都没写，照样通过编译，因为接口满足是编译期检查，看的是方法集合。但编译期满足不等于运行期一切可用：把 useStmt 改成调用未覆写的 s.Query，编译照样通过，运行即 panic，因为嵌入字段 Stmt 是接口类型，零值为 nil，代理调用没有真正的接收者可去。本机 go1.26.4（darwin/arm64）实测报 panic: runtime error: invalid memory address or nil pointer dereference。〔TB26 p13-15〕用伪对象的边界由此清楚：用不到的方法可以不写，可能被调到的方法一定要自己实现。后面还有一段"实用技巧"会从测试角度点到这个用法。

方法提升也影响接口实现，但要注意遮蔽和同层名称冲突。在不存在冲突时，结构体 `S` 嵌入 `T`：`S` 提升 `T` 的值接收者方法，`*S` 还提升 `*T` 的指针接收者方法；如果嵌入的是 `*T`，两者都提升 `T` 和 `*T` 的方法。〔TB26 p15-20〕

上一段给的是嵌入单个类型时的提升规则；嵌入多个类型时，方法集合就是各嵌入字段方法集合的合并。原课的例子让 T 同时嵌入 T1 与 *T2：T1 声明了值接收者的 T1M1 和指针接收者的 PT1M2，T2 同理有 T2M1 和 PT2M2。先列出前提：T1 的方法集合包含 T1M1，*T1 的方法集合包含 T1M1、PT1M2；T2 的方法集合包含 T2M1，*T2 的方法集合包含 T2M1、PT2M2。〔TB26 p15-16〕T1、T2 仍按本章惯例定义为 int 的命名类型，底层类型不影响方法集合的结论。用本章开头定义过的 dumpMethodSet 验证：

```go
type T1 int

func (T1) T1M1() {}
func (*T1) PT1M2() {}

type T2 int

func (T2) T2M1() {}
func (*T2) PT2M2() {}

type T struct {
	T1
	*T2
}

func main() {
	dumpMethodSet(T{})
	dumpMethodSet(&T{})
}
```

```text
main.T's method set:
- PT2M2
- T1M1
- T2M1

*main.T's method set:
- PT1M2
- PT2M2
- T1M1
- T2M1
```

输出与两条规则表达式一致：**类型 T 的方法集合 = T1 的方法集合 + `*T2` 的方法集合；类型 `*T` 的方法集合 = `*T1` 的方法集合 + `*T2` 的方法集合**。〔TB26 p17〕尤其要注意 *T 的方法集合：它包含的不是 T1 而是 `*T1` 的方法集合，PT1M2 就是这么进来的，道理和第三节"结构体指针类型的方法集合包含结构体类型的方法集合"一样：指针在手，值接收者与指针接收者的方法都够得着。调用面不受影响：无论是 T 类型变量实例还是 `*T` 类型变量实例，都可以调用所有提升上来的方法。〔TB26 p15-17〕本机 go1.26.4（darwin/arm64）实测输出与上面一致。

方法提升只认"裸类型名"，原课的一道思考题专门检验这一点。S1 就是本章前面介绍嵌入字段时用过的那个结构体：裸写 `T1`、`*t2`、`I`，另有普通字段 a、b；S2 把同样的类型一个一个写成普通命名字段，类型完全相同。T1 无方法，t2 只有 n、m 两个字段，I 只声明 M1()，定义都同前。原课正文没有给答案，请先自己分析：S1 与 `*S1`、S2 与 `*S2` 的方法集合各包含什么？两者等价吗？如不等价，区别在哪里？〔TB26 p21〕

```go
type S2 struct {
	T1 T1
	t2 *t2
	I  I
	a  int
	b  string
}

func main() {
	dumpMethodSet(S1{})
	dumpMethodSet(&S1{})
	dumpMethodSet(S2{})
	dumpMethodSet(&S2{})
}
```

```text
main.S1's method set:
- M1

*main.S1's method set:
- M1

main.S2's method set is empty!
*main.S2's method set is empty!
```

答案是两者不等价。本机 go1.26.4 实测与原课留言区的结论一致：S1 与 `*S1` 的方法集合都只有 M1 一项，S2 与 `*S2` 打印的都是空方法集合。〔TB26 p22-24〕原因是方法提升只发生在裸类型名写的嵌入字段上：S1 嵌入的接口 I 把 M1 并入了方法集合，*S1 也包含它；而 S2 的 T1、t2、I 只是恰好与类型同名的普通命名字段，命名字段不参与提升，所以 S2 与 `*S2` 的方法集合都是空的。再补一个同层冲突的实测：若让 S1 中的 `*t2` 与 `I` 同层都提供 M2，调用 s.M2 会报 `ambiguous selector`，机制与前面"冲突、歧义与测试伪对象"小节的交集歧义一致，编译器同样无法在两个同层候选之间做选择。〔TB26 p21-24〕它与下文 type S T 与别名的对照互为补充：那里看定义方式，这里看字段写法，结论都落在方法集合上。

另一个容易混淆的语法是 `type S T`：它定义新类型，而不是嵌入。对于普通非接口类型，新类型不会继承 `T` 的已声明方法；其底层结构若含嵌入字段，仍可能有提升的方法。`type S = T` 则是别名，与 `T` 是同一类型。

用原课的一道思考题把这个规则完整推一遍。先定义 `T`，给它声明两个方法，再基于 `T` 定义新类型 `S`：

```go
type T struct{}

func (T) M1()
func (T) M2()

type S T
```

两个方法只写声明、不写方法体，这是原课的示意写法，表示 `T` 拥有 `M1` 和 `M2`；动手验证时补上空方法体即可。问题是：`S` 类型包含哪些方法？`*S` 类型又包含哪些？原课把这道题当作嵌入话题的预习题：方法集合是重要且实用的概念，马上要用来理解具体问题。请先自行分析，再借助本章开头定义过的 `dumpMethodSet` 验证：〔TB25 p12〕

```go
dumpMethodSet(S{})
dumpMethodSet(&S{})
```

```text
main.S's method set is empty!
*main.S's method set is empty!
```

`S` 和 `*S` 的方法集合都是空的。原因在定义方式本身：方法要用接收者声明才能挂在类型上，`type S T` 造出的是全新类型，`S` 自己没有任何方法声明，`T` 的已声明方法也不会自动跟过来。本章前面说过，方法集合是判断接口实现的唯一手段，空方法集合意味着 `T` 能满足的接口不会自动算到 `S` 头上，需要的话只能为 `S` 重新声明方法；同样地，`S` 的值不能直接调用 `M1`，必须先显式转换，例如 `T(s).M1()`。〔TB25 p12-13〕

把定义换成别名，结论立刻反过来：`type S = T` 之后 `S` 与 `T` 是同一个类型，方法集合原样随 `T`，`S` 和 `*S` 都包含 `M1`、`M2` 两个方法。〔TB25 p13〕两种写法的本质区别是：`type S T` 定义新类型，方法集合从零开始；`type S = T` 只给既有类型换名字，类型的任何属性都不增不减。

以上结论经过实测（go1.26.4，darwin/arm64）：新类型 `S` 与 `*S` 打印的都是空方法集合；别名与原类型的方法集合完全相同，对别名值取 `reflect.TypeOf`，打印出的动态类型仍是原类型名（实测输出 `main.T0`），看不出别名痕迹。

还有一个实用技巧：测试里伪造依赖时，可以在结构体里嵌入接口，只覆写用到的方法，其余方法不实现也能编译；但未覆写的方法被调用时会 nil 解引用 panic，所以只适合你确定不会调到的场合。〔TB26 p13-15〕

## 七、水平组合三式：包装器、适配器、中间件

组合不一定要靠嵌入：一个结构体也可以持有接口字段，把工作交给另一个值。这让各部分只通过需要的行为协作。下面看三个常见用途：包装器在原有行为前后增加工作，适配器让一种形式满足另一种接口，中间件则常用包装器串联请求处理。〔TB30 p4-9〕

**1. 包装器**：`func Wrap(x I) I`，输入输出同一个接口，所以能链式叠加。标准库的 `io.LimitReader(r, n)` 就是。下面把它和一个自写的大写转换层叠在一起：

```go
package main

import (
	"fmt"
	"io"
	"strings"
)

type upper struct{ r io.Reader }

func (u upper) Read(p []byte) (int, error) {
	n, err := u.r.Read(p)
	for i := 0; i < n; i++ {
		if p[i] >= 'a' && p[i] <= 'z' {
			p[i] -= 'a' - 'A'
		}
	}
	return n, err
}

func main() {
	var r io.Reader = strings.NewReader("hello gopher")
	r = io.LimitReader(r, 8)
	r = upper{r}
	b, _ := io.ReadAll(r)
	fmt.Println(string(b))
}
```

```text
HELLO GO
```

注意变量 `r` 的类型一直是 `io.Reader`，每一层都是同接口进、同接口出，叠几层都行。

**2. 适配器**：把"满足某签名的普通函数"变成"实现某接口的值"。`http.HandlerFunc` 是范本：一个函数类型，再给它挂一个调用自己的方法。

**3. 中间件**：包装器加适配器。签名是 `func(http.Handler) http.Handler`，内部用 `http.HandlerFunc` 把匿名函数转型，函数体里先做自己的事（打日志、验签），再调用 `next.ServeHTTP(w, r)`。

```go
package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
)

func logMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("log:", r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

func authMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("auth check")
		if r.Header.Get("X-Token") == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func main() {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "hello")
	})
	srv := logMW(authMW(h))

	for _, tok := range []string{"", "t"} {
		req := httptest.NewRequest("GET", "/ping", nil)
		if tok != "" {
			req.Header.Set("X-Token", tok)
		}
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		fmt.Println(rec.Code, rec.Body.String())
	}
}
```

```text
log: /ping
auth check
401 unauthorized

log: /ping
auth check
200 hello
```

从输出里看顺序：最外层先跑，`logMW(authMW(h))` 是先日志后检查；检查失败时没有走到业务处理函数。这里仅演示控制流：它只判断请求头非空，**不是实际的令牌验证**，不能用于保护生产接口。Java 对应 Servlet Filter，Rust 对应 tower 的 Layer。后面的分层、日志、限流、熔断都会反复用到这三个词。

## 八、空接口与 any

`any` 是 `interface{}` 的别名，可以保存任何类型的值。但它没有可调用的业务方法；需要具体能力时，通常应使用相应的接口，或在合适的泛型场景中使用类型参数。格式化、处理未知类型的数据等场景仍可能需要 `any`；取出具体值时可以使用类型断言或 type switch。〔TB30 p16-17〕

### 思考题：如果 Go 中没有接口类型，会怎样？（选读）

原课在本讲结尾留下了两道思考题，第一道是"如果 Go 中没有接口类型，会发生什么，会对我们的代码设计产生怎样的影响？"〔TB28 p16〕原课正文只提出了问题，没有给出答案；下面这份参考答案由本手册综合本章正文整理，不是原课的逐条陈述，每个角度都注明正文依据，跳过不影响主线推导。

**第一，耦合会升高。**原课把接口的本质讲成契约：接口类型的背后，是通过把类型的行为抽象成契约，建立双方共同遵守的约定，这种契约将双方的耦合降到了最低的程度〔TB28 p8〕；小结再次强调：通过契约我们可以将代码双方的耦合降至最低〔TB28 p16〕。反过来，若没有接口，调用方只能依赖具体类型，想换一种发送方式就得改动调用方代码，所有调用点都要跟着改〔自·本手册第二节示例〕：把 Welcome 的参数写死成 Email 就是这个局面；改成 Sender 接口后，调用方只声明自己需要的能力，Email、SMS、内存版实现各自履约，替换或新增实现都不动调用方。

**第二，难以 mock 测试。**原课在小接口的优点里说：尤其是在单元测试环节，构建类型去实现只有少量方法的接口，要比实现拥有较多方法的接口付出的劳动少许多〔TB28 p12〕。测试替身正是建立在接口之上：第六节"冲突、歧义与测试伪对象"里，Stmt 接口声明 Close、NumInput、Exec、Query 四个方法，测试伪对象嵌入 Stmt 接口之后只需覆写真正用到的 Exec，就能冒充完整实现〔TB26 p13-15〕；14 节开头的 fakeStore 只实现一个 Get 方法，就替掉了真数据库〔自·本手册 14 节示例〕。若没有接口类型，函数与结构体的依赖只能写死为具体类型，这类替身连立足之地都没有〔自〕。

**第三，失去隐式组合的根基。**隐式的一面：接口类型与它的实现者之间的关系是隐式的，不需要像其他语言（比如 Java）那样要求实现者显式放置 implements 进行修饰，实现者只需实现接口方法集合中的全部方法，便算遵守了契约、立即生效〔TB28 p8〕。组合的一面：Go 推崇通过组合的方式构建程序，开发人员一般会尝试通过嵌入其他已有接口类型的方式来构建新接口类型，就像通过嵌入 io.Reader 和 io.Writer 构建 io.ReadWriter 那样〔TB28 p12-13〕。这两套机制都以接口类型为前提：没有接口，隐式满足无从谈起，接口嵌入接口构建新接口的手法随之消失，本章第六节的接口嵌入、第七节的包装器、适配器与中间件也一并失去基础〔自〕。

原课正文没有交代 any 这个名字的来历；第二节讲空接口时曾预告"本章第八节还会回到它"，这里把它补齐，并请注意出处层次：这一信息出自原课的精选留言，不是正文。读者 Calvin（2021-12-25）留言说："go1.18 增加了 any 关键字，用以替代现在的 interface{} 空接口类型：type any = interface{}，实际上是 interface{} 的别名。"〔TB28 p18〕泛型一章给出了更准确的表述：在 Go 1.18 中，any 是 interface{} 的别名，也是一个预定义标识符〔TBX05 p14〕。别名意味着两者是同一个类型，这与第六节讲 type S = T 时的结论一致：type S = T 之后 S 与 T 是同一个类型，类型的任何属性都不增不减〔TB25 p13〕。

## 小结

1. 方法的接收者按值传递；指针接收者可以修改所指向的原值。
2. 接口描述调用方需要的方法，类型通过方法集合隐式实现接口。
3. `T` 和 `*T` 的方法集合不同；可寻址值的自动取地址不改变接口实现关系。
4. 接口值有动态类型和动态值；包含 nil 指针的接口不一定是 nil。
5. 嵌入提升字段和方法，但不产生继承式重写；接口可以让行为替换。
6. 包装器、适配器和中间件展示了如何用接口连接不同的实现。

## 常见坑

- 返回 `error` 的函数里用了具体类型的变量（尤其是 nil 指针）：没有错误请直接 `return nil`。
- 同一类型混用值/指针接收者，导致值类型无法满足接口。
- 以为嵌入能重写被嵌入类型内部的方法调用。
- 用 `any` 代替实际需要的接口，丢失编译期的能力约束。

## 判断题与答案

先自己答，再看后面。

1. 为什么 `&c` 可以赋给 `Incrementer` 而 `c` 不可以，但 `c.Add(3)` 可以调用？
2. 对于没有嵌入字段的普通结构体，`type S T` 和 `type S = T` 的方法集合有什么差别？
3. `c.Hello()` 为什么打印 `hi base`？想要 `hi child` 该怎么改设计？
4. 一个函数返回 `error`，里面有 `var p *MyErr` 并 `return p`，调用方 `err != nil` 为什么是 true？

**答案**

1. 赋给接口时检查类型本身的方法集合；直接调用时，编译器可以对可寻址变量自动取地址。
2. `type S T` 是新类型，不继承 T 的已声明方法；`type S = T` 是别名，完全等同于 T。
3. 嵌入是转发，`Base.Hello` 里的 `b.Who()` 绑定到 `Base`，不知道外层存在。把可变部分作为接口参数传入（如上面的 `Hello(w Whoer)`）。
4. 接口等于 nil 需要类型槽和数据槽都空；这里类型槽是 `*MyErr`。

练习包 `exercises/p30/` 里可以直接跑对答案：`TestMethodSetMatrix`（方法集合矩阵）、`TestTypeAliasMethodSet`、`TestEmbedNotInherit`、`TestNilErrorTrap`、`TestCapReaderChain`（包装器链）、`TestMiddlewareCompose`（中间件）。

## 在项目里

- **隐式满足 + 适配器**：`dnsproxi-go/tcp/server.go` 里的 `streamConn` 没有声明实现 `net.PacketConn`，只是实现了它的方法，就能被传给原本为 UDP 写的 `handleQuery`。同文件的 `Handler` 是函数类型，签名与 `handleQuery` 一致，直接传方法值即可。
- **包装器**：`dnsproxi-go/logger.go` 的 `ctxHandler` 内部持有一个 `slog.Handler`，在 `Handle` 里补充请求 ID 再转发，是装饰器的真实用例。

## 想深入：正文之外的干货

### TB24 方法的本质

- 方法不是 Go 为了支持面向对象才加的：Go 从设计之初就放弃了类、对象、继承，保留方法语法是组合哲学在类型系统层面的需要，方法是纽带，不是类成员。〔TB24 p2〕
- 声明上方法只比函数多一个 receiver 段：`func (t T) Get() int` 其余六部分与函数声明完全一致；receiver 基类型不能是指针或接口类型（`type MyInt *int` 再挂方法直接编译错），且方法必须与基类型同包，所以不能给 int 等原生类型或别的包的类型加方法。〔TB24 p2-5〕
- receiver 名与形参名、具名返回值同名会报重复声明错误（原课文案 duplicate argument，现行 go1.26.4 输出 `t redeclared in this block` 并另起一行指向 receiver 声明处）；方法体不用 receiver 时可整个省略名字，写成 `func (T) M(t string)`，很少用。〔TB24 p3-4〕
- `*T` 实例能调 T-receiver 方法（编译器自动 `(*t2).M1()`），`T` 实例能调 `*T`-receiver 方法（可寻址时自动 `(&t1).M2()`），调用端两类互通，差异只在下一节的方法集合层面显现。〔TB25 p4-5〕
- 带坑的循环题：对 `[]field` 循环里 `go v.print()`（指针 receiver），编译器实际传的是 `&v`，而 for range 全程复用同一个变量 `v`，goroutine 真正跑时读到的都是最后一个元素，输出重复的末尾值；Go 1.22 起循环变量每轮新建，这个老坑在新语义下不复现。〔TB24 p9-11〕

### TB25 方法集合与 receiver 选型

- 选接收者时同时考虑修改语义、接口使用方式、复制成本和不可复制的字段；不要只按某一条机械决定。〔TB25 p5-11〕
- 指针接收者方法不属于值类型的方法集合，因此需要这些方法的接口通常由指针类型实现。〔TB25 p6-7〕
- `type S T` 不继承 T 的已声明方法；若底层结构包含嵌入字段，新类型仍可能具有提升的方法。别名 `type S = T` 则与 T 是同一类型。〔TB25 p12-13〕

### TB26 嵌入的精确语义

- 嵌入分两种：接口嵌入接口是把方法集合并入新接口，`io` 包的 ReadWriter/ReadCloser 就是单方法基本接口的组合产物；Go 1.14 前嵌入的多个接口方法不能有交集，1.14 起放开了。〔TB26 p2-5〕
- 结构体嵌入的约束：嵌入字段的底层类型不能是指针类型（`type P *T` 后嵌入 P 编译错），嵌入字段名（就是类型名）在结构体内必须唯一。〔TB26 p6-8〕
- 结构体嵌入接口类型时，外层的方法集合直接并入该接口的方法集合，`T` 和 `*T` 都算实现；但若嵌入的多个接口方法有交集且外层自己没实现交集方法，调用点报 ambiguous selector，解法是外层自己实现（自身实现优先于嵌入代理）。这条 1.14 的放宽不适用于结构体嵌入。〔TB26 p11-13〕
- `type S T` 定义的新类型不继承 T 的已声明方法；若底层结构含嵌入字段，仍可能有提升的方法。基于接口类型的 `type NI I` 则有相同的接口方法要求。〔TB26 p17-20〕
- 普通命名字段不产生方法提升：`struct { T1 T1 }` 的方法集合是空，和嵌入裸写 `T1` 完全不等价，这是容易混淆的点。〔TB26 p21-24〕

### TB28 接口即契约

- 同一接口不能声明同名方法，即使参数不同也不行；嵌入多个接口时，同名方法的签名必须一致。参数名不影响签名匹配。〔TB28 p2-4〕
- 接口方法集合可以含非导出方法（`context` 包的 `canceler` 就有 `cancel()`），这类接口通常本身非导出、只在包内用，实际编码极少遇到。〔TB28 p4-5〕
- 类型断言 `v, ok := i.(T)`：T 是具体类型时失败 ok=false、v 是 T 零值；T 是接口类型时语义变成"断言 i 装的值实现了 T"，成功后 v 的动态类型是原右值（`main.T` 而不是接口名）；单值形式 `v := i.(T)` 失败即 panic，处理不可控输入别用。〔TB28 p6-8〕
- 小接口有统计佐证：Go 标准库、Docker、Kubernetes 里 1 到 3 个方法的接口占绝大多数，标准库的 `error`、`io.Reader`、`http.Handler` 都是单方法。〔TB28 p9-10〕

### TB29 接口值与装箱的更多细节

- runtime 用两个结构体表示接口：`eface{_type, data}` 对应空接口，`iface{tab, data}` 对应有方法的接口；itab 里除了接口和动态类型的类型信息，还预存了方法地址数组 fun，接口方法调用零查表，hash 字段服务 type switch 快速分派。〔TB29 p5-7〕
- 课程中的汇编和运行时函数名对应特定版本，不应当作稳定 API。接口保存值的副本，但副本中的指针、slice 或 map 仍可能共享数据；是否发生堆分配取决于具体情况。〔TB29 p19-22〕
- 编译器对小数据有快速路径：convT16/convT32/convT64/convTstring/slice 各有专用函数，255 以内的小整数装箱走 staticuint64s 静态区免分配，和 Java 的 Integer 缓存池同思路。〔TB29 p22-23〕
- 四种等值比较情形：nil 空接口和 nil 非空接口都是 `(0x0,0x0)` 互等；两个非空接口比较要求 tab 一致且数据相等；`(*T)(nil)` 是 `(tab, 0x0)` 不等于 nil；空接口和非空接口比较时拿 eface 的类型信息对 itab 里的类型信息比，装同一个值也能相等。〔TB29 p10〕
- 课程里复制 runtime 结构体用 unsafe 读接口内部的 dump 工具，不跨版本兼容（原版在 Go 1.17 验证），结构体字段布局随版本演进，别直接搬。〔TB29 p15-19〕

### TB30 组合与三式的补充

- 前置原则：抽象有成本（装箱降效率、间接降可读性），`Add(int64, int64)` 一个函数够用就别包成 Adder 接口，先有具体实现，出现第二个变体或测试需求再抽象。〔TB30 p2〕
- 水平组合的接口参数模式天然满足三个设计原则：依赖倒置（依赖抽象）、里氏替换（无父类就没有强转问题）、接口分离（只依赖用到的能力），其他语言要刻意设计，Go 是隐式就位。〔TB30 p9〕
- 创建模式的标准库范本不止 log.New：`sync.NewCond(l Locker) *Cond`、`bufio.NewWriterSize(w io.Writer, size int) *Writer` 都是"参数收接口、返回给结构体"。〔TB30 p10-11〕
- `io.Reader` 可以同时返回 `n > 0` 和非 nil 错误；包装器应先处理这 `n` 个有效字节，再把错误传给调用方。〔TB30 p13〕
- 面对未知类型时，`any` 仍有用途；能通过具体接口表达所需能力时，优先选择具体接口。〔TB30 p17〕
- 组合哲学的出处是 Rob Pike 那句话:C++ 和 Java 是关于类型层次结构的语言,Go 是关于组合的语言。支撑组合的是三个正交语法元素:没有类型体系(没有父子类)、方法和类型正交(方法只是 receiver 当首参的函数)、接口与实现者无显式关联。正交的好处是改动互不牵连,数据库代码和 UI 代码可以各自换掉。〔TB30 p3〕


## 想深入去哪查

- 原材料：TB24（方法本质）、TB25（方法集合与选型）、TB26（嵌入）、TB28（接口即契约）、TB29（运行时表示）、TB30（组合与三式）。
- 旧长版：`notes/p2-lang-core.md`、`notes/p30-methods-interfaces.md`（含 itab 结构、装箱基准、反射）。
- 反射、`runtime` 结构体字段、`trace` 工具：不在这一节，遇到再查 `C:dachang/reflection`。