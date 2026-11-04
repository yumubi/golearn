# 04 接口与组合：Go 用来代替继承的全套工具

> 详细程度：深入 · 约 190 分钟
> 材料：TB24 到 TB30 · 旧长版：`notes/p2-lang-core.md`、`notes/p30-methods-interfaces.md`
> 前置：你写过 Java/Kotlin/Rust/TS 中任意一门的面向对象或 trait 代码

## 这节要解决什么问题

假设我们要写一段数据。最初，数据写入文件；后来，我们还想把它写到网络连接，或者在测试中写到内存里。

如果函数直接接受 `*os.File`，它只能写文件。我们真正需要的并不是“文件”，而是“能写入字节的东西”。Go 的接口就是用来表达这种需求的。它不要求这些类型有共同的父类，只要求它们提供约定的方法。

接口要求的是方法，方法又必须挂在类型上，所以本章从“给类型添加方法”讲起。而要回答“Go 为什么会有方法”，得先回到语言的设计起点：Go 语言从设计伊始就不支持类、对象、继承这些经典面向对象语法元素，但语言里仍保留了名为“方法”（method）的语法元素；而且，Go 的方法和面向对象中的方法并不是一样的。引入方法，不是为了支持面向对象编程范式，而是 Go 践行组合设计哲学的一种实现层面的需要。〔TB24 p2〕

两者的定位差别在哪里？在面向对象语言里，方法写在类的定义里，是类的一员；子类继承类，方法也跟着到手，还可以改写，方法从属整棵继承树。这是本书的推导，用来对照第三节对方法本质的讲解。Go 没有继承树，方法靠 receiver 绑定到类型上；一个类型能否被当作某种“能力”使用，看的是它提供了哪些方法，而不是它继承自谁。01 节“三、组合：没有继承，靠什么扩展”已经给过这个判断：类型嵌入做垂直扩展，接口参数做水平扩展。

本节是方法三讲的开头：先解答“Go 方法究竟是什么”，后续两讲依次是 receiver 的类型选择、方法集合，以及如何实现方法的“继承”。〔TB24 p1-2〕原课说，函数是 Go 代码的基本功能逻辑单元，程序执行流在函数调用栈中从一个函数流向另一个函数；有预习的读者会反驳：Go 还有方法这种语法元素，也能承载代码逻辑。原课的回答是：别急，等讲完方法的本质，再评判这个说法也不迟。〔TB24 p1〕“方法承载逻辑”为什么能成立，要等第三节把方法还原成函数才见分晓；本节先解决更基础的问题：方法怎么声明，它和函数差在哪。

本章按这条线讲四件事：第一，给类型添加方法：方法怎么声明，receiver 是什么，Go 对它有哪些硬性限制（本节）；第二，值接收者还是指针接收者：同一对 T/`*T`，行为和接口实现为什么不同；第三，接口的定义与实现：从“能写入字节的东西”这样的需求出发，看接口怎么被定义、被满足、被使用，以及为什么指针能实现接口而值不行；第四，接口值和 nil、结构体嵌入、包装器与中间件：接口值的内部表示、嵌入为什么不是继承、水平组合的三种写法。反射和 `trace` 工具不在这一节，章末“想深入去哪查”指了去处。〔TB24 p2-8〕〔TB30 p6-8〕

## 一、先给类型添加方法

先看一个普通的计数器。它保存一个数字，提供读取和增加两个操作：

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

`func` 和方法名之间的 `(c Counter)` 称为**接收者**（receiver）。它说明 `Value` 是 `Counter` 的方法；`(c *Counter)` 则说明 `Add` 使用指向 `Counter` 的指针作为接收者。调用时，把接收者放在方法名前面：`c.Value()`、`c.Add(3)`。

为什么 `Add` 用指针？因为 Go 调用函数时按值传递参数。值接收者得到的是 `Counter` 的副本；如果在副本上增加数字，调用者的计数器不会改变。指针接收者得到的是指针的副本，但它仍指向原来的计数器，所以可以修改原值。注意，结构体副本里的 slice、map 等字段仍可能共享底层数据，值接收者并不保证所有数据都被隔离。

方法也可以用**方法表达式**调用：`Counter.Value(c)` 等价于这里的 `c.Value()`，`(*Counter).Add(&c, 3)` 对应 `c.Add(3)`。这有助于理解接收者如何传递；日常代码通常直接使用点号调用。〔TB24 p6-8〕

### 方法声明：和函数只差一个 receiver 段

标准库的例子比计数器更正式。`net/http` 包里 `*Server` 类型的 `ListenAndServeTLS` 是这样声明的（本机 go1.26.4 工具链源码 `net/http/server.go:3703`；原课图示中 receiver 参数名为 `srv`，这里照现行源码写作 `s`）：

```go
func (s *Server) ListenAndServeTLS(certFile, keyFile string) error
```

先数一数：函数声明由五个部分组成，Go 方法的声明有六个组成部分，多出的一个就是 receiver 部分，在 receiver 部分声明的参数称为 receiver 参数。〔TB24 p3〕同样以 `func` 关键字修饰，也包含方法名（对应函数名）、参数列表、返回值列表与方法体（对应函数体）。〔TB24 p2〕方法名的首字母大小写决定该方法是否为导出方法；参数列表支持变长参数；返回值列表支持具名返回值。〔TB24 p2〕这六个部分是：接收者段（receiver）、方法名、参数列表、返回值列表、方法体，再加上开头修饰的 `func` 关键字。除 receiver 外，其余各部分与函数声明中对应的部分在形式与语义上都一致。

receiver 参数是方法与类型之间的纽带，也是方法与函数最大的不同：Go 的方法必须归属于一个类型，receiver 参数的类型就是这个方法归属的类型。〔TB24 p3〕`ListenAndServeTLS` 的 receiver 参数类型是 `*Server`，所以它是 **`*Server` 类型的方法**，不能说成“`Server` 类型的方法”，原课特意用“注意！”强调了这一点，具体原因留到后面课程细讲，落点在本手册第三节的方法集合部分。〔TB24 p3〕

把上面的声明换成一般形式：

```go
func (t *T或T) MethodName(参数列表) (返回值列表) {
	// 方法体
}
```

无论 receiver 参数的类型是 `*T` 还是 `T`，一般形式中的 `T` 都叫做 receiver 参数 `t` 的基类型：`t` 的类型为 `T`，这个方法就是类型 `T` 的方法；`t` 的类型为 `*T`，这个方法就是类型 `*T` 的方法。〔TB24 p3〕拿开头的计数器对号入座（这段是我的推导）：`(c Counter)` 的基类型是 `Counter`，`Value` 是 `Counter` 的方法；`(c *Counter)` 的基类型同样是 `Counter`，`Add` 是 `*Counter` 的方法。

数量上，每个方法只能有一个 receiver 参数：Go 不支持在 receiver 部分放置包含多个 receiver 参数的参数列表，也不支持变长 receiver 参数〔TB24 p3〕；语言规范同样写明 receiver 参数段“必须声明单个非变长参数”（本机 go1.26.4 工具链 `doc/go_spec.html:2985-2986`）。

### receiver 的基类型：不能是指针或接口类型

数量之外，Go 对 receiver 参数的基类型本身还有一条约束：**基类型不能是指针类型，也不能是接口类型**〔TB24 p4〕。规范原句是 `A receiver base type cannot be a pointer or interface type`（receiver 基类型不能为指针或接口类型，本机 go1.26.4 工具链 `doc/go_spec.html:2990-2991`）。同一句规范还规定了 receiver 参数类型的两种合法形式：一个定义类型 `T`，或指向定义类型 `T` 的指针 `*T`（`doc/go_spec.html:2988-2989`）；无论写成哪种，`T` 都是基类型，这条约束检查的是 `T` 自己，而不是 receiver 参数写成 `T` 还是 `*T`。〔TB24 p3-4〕

先看原课的两个反例，它们演示了基类型分别为指针类型和接口类型时编译器的反应〔TB24 p4〕：

```go
type MyInt *int

func (r MyInt) String() string { // 编译器报错：invalid receiver
	// ...
}

type MyReader io.Reader

func (r MyReader) Read(p []byte) (int, error) { // 编译器报错：invalid receiver
	// ...
}
```

两个例子都有意不能编译。`MyInt` 和 `MyReader` 这两个类型定义本身是合法的：`MyInt` 的底层类型是 `*int`，`MyReader` 的底层类型是 `io.Reader`。触发报错的是把它们用作基类型：编译器检查的是基类型的**底层类型**，一个落在指针类型上，一个落在接口类型上，方法声明就被拒绝。本机 go1.26.4 编译器按同样的次序实现这个检查：先剥掉 receiver 参数类型中的指针得到基类型（`src/cmd/compile/internal/types2/signature.go:452-456`），再检查基类型的底层类型是否为指针或接口（`signature.go:468-476`）；对 `MyInt`，现行编译器在该分支给出的信息是 `invalid receiver type MyInt (pointer or interface type)`，比原课标注的 `invalid receiver` 多出类型名和原因（`signature.go:475-483`）。

最容易出错的是约束的落点：它约束基类型，不约束 receiver 参数的写法。拿开头的计数器对号入座（这段对号入座是我的推导，检查次序引自上述编译器源码）：`(c *Counter)` 的 receiver 参数类型是指针 `*Counter`，剥掉这一层指针后，剩下的基类型 `Counter` 是结构体，既不是指针也不是接口，所以完全合法；开头的 `Add` 用的正是这种写法。原课有读者问“`*int` 算指针类型，但是 `*T` 不算指针类型吗”〔TB24 p16〕，答案就是这层区别：`*` 出现在 receiver 参数类型上没有问题，出现在基类型自己的定义里（`type MyInt *int`）才违反约束。原课小结把“基类型不能是指针类型或接口类型”与方法声明的位置约束并列为两条独立约束〔TB24 p12〕，接下来看第二条。

### 方法声明的位置：必须与基类型同包

原课给出的最后一条约束与位置有关：方法声明必须与 receiver 参数的基类型的声明放在同一个包内〔TB24 p5〕。它就是上一小节那句规范原文的后半分句：`and it must be defined in the same package as the method`（并且基类型必须与方法声明在同一个包中，`doc/go_spec.html:2990-2991`）。规范另有一个正向说法：方法被“绑定”（bound）到它的基类型上（`doc/go_spec.html:2992`）。绑定意味着方法从声明那一刻起就归属于某个包里声明的类型，不存在“站在包外给类型挂方法”的写法。

基于这条约束，原课给出两个推论〔TB24 p5〕。推论一：不能为原生类型（诸如 `int`、`float64`、`map` 等）添加方法。下面的代码试图为原生类型 `int` 增加新方法 `Foo`，编译器报错：

```go
func (i int) Foo() string { // 编译器报错：cannot define new methods on non-local type
	// ...
}
```

推论二：不能跨越 Go 包为其他包的类型声明新方法。`http.Server` 声明在标准库 `net/http` 包里，在你的包里为它加方法同样被拒绝：

```go
import "net/http"

func (s http.Server) Foo() { // 编译器报错：cannot define new methods on non-local type
	// ...
}
```

两个推论是同一条约束的两种表现：`int` 和 `http.Server` 都不是本包内声明的基类型，编译器把这类基类型按“非本包类型”拒绝，两个例子的报错信息相同：`cannot define new methods on non-local type`（不能在非本包类型上定义新方法）。原课文把这行报错截断为 `non-loca`，完整形式以现行编译器源码为准：跨包定义的类型走 `cmd/compile/internal/types2/signature.go:464-466` 的分支，原生类型走同一函数的 `*Basic` 分支（`signature.go:485-486`），两处打印同一条信息。

这条约束的收益和代价来自同一处（取舍为教学推导）：一个类型的方法集合被封闭在声明它的包内，读类型的声明就能确定它有哪些方法，第三方无法在别处悄悄扩充它；代价是不能扩展原生类型和别的包的类型。需要这类扩展时，惯用做法是定义一个自己的类型把值包装起来，再在新类型上声明方法，落地方式见第六节（嵌入）与第七节（包装器）。对照其他语言（以下为我的推导，非原课内容）：Rust 的 `impl` 必须与类型在同一个 crate 内，跨 crate 不能给外部类型实现 trait，例外只有 newtype 包装，与 Go 的同包约束同构；Java 没有对应约束，继承或装饰器都能扩展既有类型，方法归属最自由。原课小结也复述了这条位置约束：方法声明必须与 receiver 参数的基类型在同一个包中〔TB24 p12〕。

### receiver 的名字：作用域与省略

receiver 参数也有作用域，规则来自第 11 讲、本讲复习：receiver 参数、函数/方法的参数、返回值变量，对应的作用域范围都是函数/方法体对应的显式代码块。〔TB24 p3-4〕（这条规则在 03 节“七、作用域与遮蔽”的“函数边界上的标识符”下有完整展开。）由此推出：receiver 部分的参数名不能与方法参数列表中的形参名、具名返回值中的变量名冲突，必须在该方法的作用域中唯一。〔TB24 p3-4〕

```go
type T struct{}

func (t T) M(t string) { // 编译器报错：duplicate argument t（重复声明参数 t；原课 Go 版本的报错，go1.26.4 实测为 `t redeclared in this block`，另起一行 `other declaration of t` 指向 receiver 声明处）
	// ...
}
```

编译器在方法体的作用域里检查标识符唯一性：同一个作用域出现了两个 `t`。原课文案是报 `duplicate argument t`；现行 go1.26.4 编译器输出为 `t redeclared in this block`（t 在此代码块中重复声明），另起一行 `other declaration of t` 指向 receiver 声明处。想编译通过，把 receiver 参数或形参改成不同的名字即可。规范原文是：非空 receiver 标识符在方法签名中必须唯一（`doc/go_spec.html:2998-3002`）。〔TB24 p4〕

如果方法体的实现不需要用到 receiver 参数，参数名可以整个省略：

```go
type T struct{}

func (T) M(t string) {
	// ...
}
```

规范允许在方法体内未引用 receiver 的值时省略它的标识符（`doc/go_spec.html:3000-3002`）。〔TB24 p4〕不过仅当方法体中的实现不需要 receiver 参与时才这样写，这种情况很少使用，了解即可。〔TB24 p4〕

### 方法怎么调用：值和指针都能调

声明约束到此讲完，原课转入方法的使用：如果 receiver 参数的基类型为 T，就说 receiver 参数绑定在 T 上；绑定在 T 上的方法，既可以通过 T 类型的变量实例调用，也可以通过 *T 类型的变量实例调用。〔TB24 p5〕

原课用下面的例子演示这两种调用形式：

```go
type T struct{}

func (t T) M(n int) {
}

func main() {
	var t T
	t.M(1) // 通过类型T的变量实例调用方法M

	p := &T{}
	p.M(2) // 通过类型*T的变量实例调用方法M
}
```

方法 M 的 receiver 写的是值类型 `t T`，按声明它是类型 T 的方法；`main` 里第一个实例 `var t T` 用 `t.M(1)` 调用，注释标明“通过类型T的变量实例调用方法M”；第二个实例 `p := &T{}` 用 `p.M(2)` 调用，注释标明“通过类型*T的变量实例调用方法M”。两种调用针对的是同一个绑定在 T 上的方法 M，都能编译执行。〔TB24 p5-6〕（原课网页版有一处排版勘误：留言区读者西红柿牛腩泡饼指出个别位置的星号被排成了斜体，文中还有几处同类错误，编辑回复“感谢反馈，已整体复核修改了”；p5 到 p6 的代码块以注释文字“类型*T的变量实例”为准。〔TB24 p15-17〕）

看到这里你可能会问出原课预判的问题：方法 M 是类型 T 的方法，那为什么通过 *T 类型变量也可以调用 M 方法呢？原课的处理是把原因留到下一讲：“关于这个问题，我会在下一讲中告诉你原因，这里你先了解方法的调用方式就好了。”〔TB24 p6〕本节因此只要求掌握调用方式，不给出任何机制解释：既没有提到方法集合，也没有提到自动取地址。至于“下一讲”是哪一讲，本章开头列过方法三讲的安排，下一讲正是方法集合所在的讲次。〔TB24 p1-2〕

这里只补一句指向，不提前展开：为什么 *T 能调 T 的方法的完整答案在第三节的方法集合；同一节“调用不等于实现”一段（对应 TB25 p4-5）讲的是方法调用处的自动取地址语法糖，它不改变方法集合。本节先记住调用形式：绑定在 `T` 上的方法，`T` 或 `*T` 的实例都能调。

## 二、接口：让同一段代码使用不同的类型

假设我们要给用户发送通知。先写一个 `Email` 类型，用它的 `Send` 方法发送消息：

```go
type Email struct{}

func (Email) Send(message string) {
	fmt.Println("发送邮件：", message)
}
```

这里为了看清调用过程，`Send` 只打印消息，并没有真的发送邮件。现在有一段业务代码：用户注册成功后，发送一条欢迎消息。

```go
func Welcome(sender Email) {
	sender.Send("欢迎注册")
}
```

调用 `Welcome(Email{})`，就会输出：

```text
发送邮件： 欢迎注册
```

### 如果要换成短信呢？

过了一段时间，我们又实现了一个 `SMS` 类型。它也有一个 `Send` 方法，只是发送方式不同：

```go
type SMS struct{}

func (SMS) Send(message string) {
	fmt.Println("发送短信：", message)
}
```

现在试着调用 `Welcome(SMS{})`。虽然 `SMS` 也有 `Send` 方法，代码还是不能编译：

```go
Welcome(SMS{}) // 编译错误：不能把 SMS 用作 Email
```

原因不在 `SMS.Send` 的写法，而在 `Welcome` 的参数：它要求的类型是 `Email`。`SMS` 和 `Email` 是不同的类型，不能因为它们碰巧有相同的方法，就把一个当成另一个。

我们当然可以再写一个 `WelcomeSMS(sender SMS)`，在里面调用 `sender.Send("欢迎注册")`。但是，欢迎消息的业务逻辑并没有变，只是发送方式变了。如果以后再增加站内信，还要继续复制这段逻辑吗？

仔细看 `Welcome`：它并不需要知道邮件地址，也不关心如何连接邮件服务器。它只需要一个可以调用 `Send(string)` 的值。我们能不能把这个要求写在参数里，而不是指定 `Email`？

### 定义接口

Go 的接口可以描述这样的要求：

```go
type Sender interface {
	Send(message string)
}
```

`Sender` 里面没有 `Send` 的函数体，只有方法签名。它的意思是：如果一个类型要作为 `Sender` 使用，就必须提供一个接受 `string`、没有返回值的 `Send` 方法。

把 `Welcome` 的参数从 `Email` 改成 `Sender`，其余代码不变：

```go
func Welcome(sender Sender) {
	sender.Send("欢迎注册")
}
```

现在，把前面的代码放在一起运行：

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
	Welcome(Email{})
	Welcome(SMS{})
}
```

输出：

```text
发送邮件： 欢迎注册
发送短信： 欢迎注册
```

两次调用执行的是同一个 `Welcome`，为什么输出不同？

第一次传入的是 `Email{}`，`sender.Send(...)` 调用 `Email.Send`；第二次传入的是 `SMS{}`，同一行代码调用 `SMS.Send`。`Welcome` 只负责决定发送什么，不负责决定具体怎样发送。

如果以后增加一个站内信类型，只要它提供符合要求的 `Send(string)` 方法，就可以传给现有的 `Welcome`，不必修改 `Welcome` 本身。这就是接口在这里带来的好处：**调用方依赖需要的行为，而不是绑定某一种实现。**

回看 `Sender` 的声明形式。接口类型由 `type` 和 `interface` 关键字定义的一组方法集合构成，方法集合唯一确定这个接口类型所表示的接口；集合里的每个方法，形式上是不带 `func` 关键字的“函数名 + 函数签名（参数列表 + 返回值列表）”的组合。之所以称它们为“方法”，是从接口实现者的角度说的。〔TB28 p3〕声明方法时，参数列表不需要写出形参名字，返回值列表同样如此：形参名字与返回值列表中的具名返回值，都不作为区分两个方法的凭据。对 `Sender` 来说，`Send(message string)`、`Send(s string)` 与 `Send(string)` 描述的是同一个方法。原课用一个含两个方法的接口演示了这条规则：

```go
type MyInterface interface {
	M1(int) error
	M2(io.Writer, ...string)
}
```

`M1(int) error`、`M1(a int) error`、`M1(n int) error` 是同一个方法；`M2(io.Writer, ...string)`、`M2(w io.Writer, strs ...string)`、`M2(w io.Writer, args ...string)` 同理。这几种写法定义的接口彼此等价。〔TB28 p3〕

### 类型是什么时候实现接口的？

再看一遍 `Email`：我们没有在定义它的时候写“实现 `Sender`”，甚至可以先写好 `Email`，后来才定义 `Sender`。为什么它仍然能传进去？

因为 Go 检查的是方法。`Sender` 要求 `Send(string)`，而 `Email` 和 `SMS` 都有签名匹配的 `Send` 方法，所以它们满足 `Sender` 接口。这称为**隐式实现**。〔TB28 p2-5〕

签名必须匹配。例如，如果某个类型的方法是 `Send(message string) error`，它比接口要求多了一个返回值，就不能直接作为上面的 `Sender` 使用。编译器会在赋值或传参时检查，而不是等到调用时才发现。

这个判定可以写得更精确。Go 规定：如果一个类型 `T` 的方法集合是某接口类型 `I` 方法集合的等价集合或超集，就说类型 `T` 实现了接口类型 `I`，`T` 的变量就可以作为合法右值赋给 `I` 的变量。〔TB28 p5〕方法集合怎么求，特别是 `T` 与 `*T` 的差别，是下一节的主题；在那里，这条规则会被用来解释为什么指针能实现接口而值不行。

### 方法不能重名：判定依据是名字加签名

接口方法还有两条硬性规则：方法必须具名，并且方法名在这个接口类型的方法集合中是唯一的。〔TB28 p3-4〕把 `M1(int) error` 和 `M1(a int) error` 同时写进一个接口，形参名不同也救不了它：对显式声明的重复方法，编译器直接报 `duplicate method`（$GOROOT/src/cmd/compile/internal/types2/typeset.go:227-230）。

同名冲突更常见的触发场景是接口嵌入。Go 1.14 版本以后，允许嵌入的不同接口类型的方法集合存在交集，但前提是交集中的方法不仅名字要一样，函数签名部分也要保持一致，也就是参数列表与返回值列表都要相同，否则编译器照样报错。〔TB28 p3-4〕看一个会报错的例子（裸类型名是接口嵌入，语义是把被嵌接口的方法集合并入自己，第六节展开）：

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
	Interface2 // 编译器报错：duplicate method M1
	M3()
}
```

Interface3 同时嵌入 Interface1 和 Interface2，并声明了自己的 M3()。报错由 Interface1 的 M1() 与 Interface2 的 M1(string) 冲突引发：两者名字相同，但参数列表不同（一个无参，一个接受 string），签名不一致。判定冲突的依据不是“名字重复”本身：名字相同、签名相同，交集合法；名字相同、签名不同，才报 duplicate method。Interface3 自己声明的 M3() 与这场冲突无关。现行编译器在类型集计算中做这项检查：Go 1.14 语义下，嵌入的方法签名不一致（不是 Identical）才报 duplicate method（$GOROOT/src/cmd/compile/internal/types2/typeset.go:240-242）。〔TB28 p3-4〕

第六节还会出现一组同名的 Interface1 到 Interface4，那里的 Interface2 声明的是 M1() 而不是 M1(string)，与本例正好互补：签名相同的交集自 Go 1.14 起合法，签名不同的交集始终非法。

上面例子里的方法都是首字母大写的导出方法。接口方法集合中放入首字母小写的非导出方法也是合法的，标准库里能找到这样的例子，比如 context 包的 canceler 接口，它是可以被直接取消的 context 类型，实现者是 `*cancelCtx` 与 `*timerCtx`：〔TB28 p4〕

```go
// $GOROOT/src/context/context.go
// A canceler is a context type that can be canceled directly. The
// implementations are *cancelCtx and *timerCtx.
type canceler interface {
	cancel(removeFromParent bool, err, cause error)
	Done() <-chan struct{}
}
```

原课引用的版本里 cancel 只有两个参数（cancel(removeFromParent bool, err error)），现行源码多了一个 cause 参数。〔TB28 p4〕〔$GOROOT/src/context/context.go:417-420〕这样的例子在标准库中并不多；从中可以总结：如果接口类型的方法集合包含非导出方法，该接口类型自身通常也是非导出的，应用范围局限于包内，日常实际编码中极少使用，了解即可。〔TB28 p4-5〕

### 空接口：方法集合为空的接口

把判定规则推到极端：如果一个接口类型一个方法都没有，它的方法集合就是空集合：

```go
type EmptyInterface interface {
}
```

方法集合为空的接口类型被称为空接口类型。任何类型的方法集合都是空集合的超集，按判定规则，任何类型都实现了空接口。〔TB28 p5-6〕通常不需要显式定义空接口类型，直接使用 `interface{}` 这个类型字面值作为所有空接口类型的代表：

```go
var i interface{} = 15 // ok
i = "hello, golang"    // ok

type T struct{}
var t T
i = t  // ok，结构体值
i = &t // ok，指针
```

同一个变量 `i` 先后接受了 int、字符串、结构体值和指针，全部合法。〔TB28 p6〕空接口可接受任意类型值作为右值，这使它成为 Go 加入泛型语法之前唯一一种具有“泛型”能力的语法元素：包括标准库在内的一些通用数据结构与算法，都用 `interface{}` 作为数据元素的类型，无需为每种元素类型单独复制一份代码。〔TB28 p6〕本章第八节还会回到它：`any` 就是 `interface{}` 的别名。

### 接口类型变量：默认值 nil

接口类型一旦定义，就和其他 Go 类型一样可以用于声明变量：

```go
var err error    // err 是一个 error 接口类型的实例变量
var r io.Reader  // r 是一个 io.Reader 接口类型的实例变量
```

类型为接口类型的变量被称为接口类型变量；没有被显式赋予初值时，接口类型变量的默认值为 nil。〔TB28 p5〕要为接口类型变量显式赋予初值，就必须选择合法的右值：按实现判定规则，右值类型的方法集合必须是接口方法集合的等价集合或超集，即右值类型实现了该接口。〔TB28 p5-6〕`Email{}` 与 `SMS{}` 能传给 `Welcome`，而没有 `Send` 方法的类型不能，依据正是这条规则。

### 类型断言：赋值的逆操作

接口变量赋值有一个“逆操作”：通过接口类型变量还原它的右值的类型与值信息，这个过程称为类型断言（Type Assertion）。类型断言通常使用下面的语法形式，其中 `i` 是某个接口类型变量：〔TB28 p6〕

```go
v, ok := i.(T)
```

当 `T` 是非接口类型且 `T` 是想要还原的类型时，这句代码的含义是：断言存储在接口变量 `i` 中的值的类型为 `T`。〔TB28 p6-7〕这种写法通常叫 comma-ok 形式，结果有两种：如果 `i` 之前被赋予的值确为 `T` 类型，执行后 `ok` 为 `true`，`v` 的类型为 `T`，值是 `i` 之前的右值；如果不是 `T` 类型，`ok` 为 `false`，`v` 的类型仍然是要还原的 `T`，值则是 `T` 的零值。〔TB28 p7〕类型断言也支持单值形式 `v := i.(T)`：一旦 `i` 之前被赋予的值不是 `T` 类型，这条语句将抛出 panic；成功时与 comma-ok 形式一致。由于可能出现 panic，原课并不推荐这种形式。〔TB28 p7〕

把 int64 的 13 装进空接口变量，四种写法的结局一目了然（片段放在 main 函数里、import "fmt" 后即可运行）：〔TB28 p7〕

```go
var a int64 = 13
var i interface{} = a

v1, ok := i.(int64)
fmt.Printf("v1=%d, the type of v1 is %T, ok=%t\n", v1, v1, ok) // v1=13, the type of v1 is int64, ok=true
v2, ok := i.(string)
fmt.Printf("v2=%s, the type of v2 is %T, ok=%t\n", v2, v2, ok) // v2=, the type of v2 is string, ok=false
v3 := i.(int64)
fmt.Printf("v3=%d, the type of v3 is %T\n", v3, v3) // v3=13, the type of v3 is int64
v4 := i.([]int) // panic: interface conversion: interface {} is int64, not []int
fmt.Printf("the type of v4 is %T\n", v4)
```

`v2` 那行值得多看一眼：断言失败，`ok` 为 `false`，`v2` 的类型仍是 `string`，值是 string 的零值（空字符串），程序继续往下走；`v4` 用单值形式断言一个从未装入过的 `[]int`，直接 panic，最后的 `Printf` 不会执行。

当 `T` 本身是接口类型时，断言语义随之改变：不再是“值的类型恰为 `T`”，而是断言 `i` 的值实现了接口类型 `T`。断言成功时，变量 `v` 的类型为 `i` 的值的类型（也就是第四节的动态类型），而并非接口类型 `T`：〔TB28 p7〕

```go
package main

import "fmt"

type MyInterface interface {
	M1()
}

type T int

func (T) M1() {
	println("T's M1")
}

func main() {
	var t T
	var i interface{} = t

	v1, ok := i.(MyInterface)
	if !ok {
		panic("the value of i is not MyInterface")
	}
	v1.M1()
	fmt.Printf("the type of v1 is %T\n", v1) // the type of v1 is main.T
}
```

程序先输出 T's M1（`v1.M1()` 的结果），再输出 the type of v1 is main.T：`%T` 打印的是 `main.T`，不是 `MyInterface`。断言成功只说明 `i` 中的值实现了 `MyInterface`，`v1` 拿到的是那个值本身，它的类型是装进去时的 `T`，所以还能直接调用 `v1.M1()`。〔TB28 p7-8〕

断言失败又是另一番景象。在上面 `main` 的末尾接着写：

```go
i = int64(13)
v2, ok := i.(MyInterface)
fmt.Printf("the type of v2 is %T\n", v2) // the type of v2 is <nil>
// v2 = 13 // 去掉注释后无法通过编译：cannot use ... as type MyInterface in assignment
```

失败时，`v2` 的类型信息为接口类型 `T`（这里是 `MyInterface`），值为 nil；但 `%T` 打印出来的是 `<nil>`，单看这行输出看不出 `v2` 的类型。把注释行 `v2 = 13` 打开，编译器的赋值检查会拒绝它，报错信息里带着 `v2` 的静态类型 `MyInterface`，类型信息由此暴露（引文出自原课文案，现行编译器的具体报错文字可能略有出入）。〔TB28 p8〕`%T` 帮不上忙的原因：它打印的是动态类型，断言失败的 `v2` 还没有动态类型，静态类型只存在于编译器的检查里。

接口类型的类型断言还有一个变种 type switch，按动态类型分发，01 节“TB20 switch”小节已经讲过，可以回去复习。〔TB28 p8〕

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

## 四、接口值和 nil

接口变量可以先后保存不同的具体类型。为了理解它的行为，可以把非 nil 接口值想成一对信息：**动态类型和动态值**。这个模型描述语言语义，不要求依赖某个 Go 版本的运行时内存布局。

下面看一个实际会遇到的问题：函数声明返回 `error`，没有错误时却返回了一个 nil 指针。

```go
package main

import "fmt"

type MyErr struct{}

func (*MyErr) Error() string { return "my err" }

func mayFail(bad bool) error {
	var p *MyErr
	if bad {
		p = &MyErr{}
	}
	return p
}

func mayFailOK(bad bool) error {
	if bad {
		return &MyErr{}
	}
	return nil
}

func main() {
	err := mayFail(false)
	fmt.Println(err == nil)
	fmt.Printf("%T %v\n", err, err == (*MyErr)(nil))
	fmt.Println(mayFailOK(false) == nil)
}
```

输出：

```text
false
*main.MyErr true
true
```

`p` 的值确实是 nil，但 `return p` 把它赋给了 `error` 接口。返回的接口值具有动态类型 `*MyErr`，动态值则是 nil 指针；接口本身因此不是 nil。`err == (*MyErr)(nil)` 为 true，是因为两边转换成接口后动态类型相同，动态指针值也相同。

没有错误时直接 `return nil`，返回的才是 nil 接口。不要把一个 nil 的具体指针作为 `error` 返回。〔TB29 p3-5〕

## 五、接口值的比较和成本

前面用动态类型和动态值解释了 `nil`。同一个模型也能帮助我们理解接口值之间的比较：两个非 nil 接口值要相等，动态类型必须相同，动态值也必须相等。动态类型不同，即使数值看起来一样，结果也不相等。〔TB29 p9-10〕

```go
package main

import "fmt"

func main() {
	var a, b any = int(17), int64(17)
	fmt.Println(a == b)
	fmt.Printf("%T %T\n", a, b)

	var c any = int(17)
	fmt.Println(a == c)

	var err error
	fmt.Printf("%T\n", err)
	err = fmt.Errorf("x")
	fmt.Printf("%T\n", err)
}
```

```text
false
int int64
true
<nil>
*errors.errorString
```

没有赋值的 `error` 是 nil 接口，`%T` 打印 `<nil>`。如果两个接口的动态类型相同，但该类型不可比较（例如 slice），比较时会 panic；因此不能把任意接口值都当成可安全比较的值。

把值赋给接口时可能发生复制或堆分配，具体取决于类型、使用方式和编译器优化；不能仅凭“使用接口”断定一定分配。性能敏感时应先用基准测试和性能分析确认，再考虑优化。〔TB29 p21-23〕

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