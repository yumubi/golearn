---
title: 04 接口与组合：Go 用来代替继承的全套工具
---

# 04 接口与组合：Go 用来代替继承的全套工具


> 详细程度：深入 · 约 95 分钟
> 材料：TB24 到 TB30 · 旧长版：`notes/p2-lang-core.md`、`notes/p30-methods-interfaces.md`
> 前置：你写过 Java/Kotlin/Rust/TS 中任意一门的面向对象或 trait 代码

## 这节要解决什么问题

假设我们要写一段数据。最初，数据写入文件；后来，我们还想把它写到网络连接，或者在测试中写到内存里。

如果函数直接接受 `*os.File`，它只能写文件。我们真正需要的并不是“文件”，而是“能写入字节的东西”。Go 的接口就是用来表达这种需求的。它不要求这些类型有共同的父类，只要求它们提供约定的方法。

本节先从方法和接口讲起，再解释值与指针为什么影响接口实现。弄清这些基础后，我们会看接口值的 `nil`、结构体嵌入，以及包装器和中间件。〔TB24 p2-8〕〔TB30 p6-8〕

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

### 值接收者还是指针接收者？

先看行为：方法需要修改接收者时，用指针；结构体包含不应复制的锁，或复制成本较高时，也通常用指针。如果不需要修改，且复制是安全的，值接收者可以让方法作用于副本。

还要看调用方需要什么：只有值接收者方法的 `T` 和 `*T` 都可能满足接口；有指针接收者方法时，通常只有 `*T` 能满足需要该方法的接口。同一类型的方法一般保持接收者形式一致，但不是语言强制要求。〔TB25 p2-11〕

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

### 类型是什么时候实现接口的？

再看一遍 `Email`：我们没有在定义它的时候写“实现 `Sender`”，甚至可以先写好 `Email`，后来才定义 `Sender`。为什么它仍然能传进去？

因为 Go 检查的是方法。`Sender` 要求 `Send(string)`，而 `Email` 和 `SMS` 都有签名匹配的 `Send` 方法，所以它们满足 `Sender` 接口。这称为**隐式实现**。〔TB28 p2-8〕

签名必须匹配。例如，如果某个类型的方法是 `Send(message string) error`，它比接口要求多了一个返回值，就不能直接作为上面的 `Sender` 使用。编译器会在赋值或传参时检查，而不是等到调用时才发现。

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

定义自己的接口时，也可以从使用方实际需要的方法出发，不必预先为每个类型都创建接口。如果标准库已经有合适的接口，直接使用即可。接口应当完整表达使用方的需要，并不是方法越少越好。〔TB28 p10-15〕


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

### 修法：换接收者类型

课程给的修复方法只有一行：把 `print` 的 receiver 从 `*field` 改成 `field`，程序就输出 one 到 six。〔TB24 p11-12〕原因是：receiver 变成 `field` 后，方法表达式的第一个参数类型也是 `field`，两个循环传入的都是**元素的值拷贝**：`data1` 的循环是 `field.print(*v)`，`data2` 的循环是 `field.print(v)`，每个 goroutine 在 go 语句处就拿到了自己那一轮的值，不再依赖循环变量的地址〔TB24 p14〕；这正好绕开了旧语义的循环变量复用问题。原课程把这个修法的原理留作思考题，留言区给出了与分析一致的解释。

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

结合方法集合，可以得到接收者选型的第三条原则，它比前两条高一个层次：

**选择依据是 `T` 类型是否需要实现某个接口。**〔TB25 p10-11〕

- 如果 `T` 需要实现某个接口，就把接口要求的**所有方法**都用 `T` 作 receiver 声明，让值类型 `T` 的方法集合覆盖接口方法集合。混用接收者（比如 M1 用 `T`、M2 用 `*T`）会导致 `T` 无法实现接口，就像上面悖论里的类型。
- 如果 `T` 不需要实现接口，而 `*T` 需要，不必专门处理：`*T` 的方法集合包含 `T` 的方法集合，按原则一、二逐个方法选就行。

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

方法提升也影响接口实现，但要注意遮蔽和同层名称冲突。在不存在冲突时，结构体 `S` 嵌入 `T`：`S` 提升 `T` 的值接收者方法，`*S` 还提升 `*T` 的指针接收者方法；如果嵌入的是 `*T`，两者都提升 `T` 和 `*T` 的方法。〔TB26 p15-20〕

另一个容易混淆的语法是 `type S T`：它定义新类型，而不是嵌入。对于普通非接口类型，新类型不会继承 `T` 的已声明方法；其底层结构若含嵌入字段，仍可能有提升的方法。`type S = T` 则是别名，与 `T` 是同一类型。

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
- receiver 名与形参名、具名返回值同名会报 duplicate argument；方法体不用 receiver 时可整个省略名字，写成 `func (T) M(t string)`，很少用。〔TB24 p3-4〕
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
