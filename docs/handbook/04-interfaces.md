---
title: 04 接口与组合：Go 用来代替继承的全套工具
---

# 04 接口与组合：Go 用来代替继承的全套工具


> 详细程度：深入 · 约 50 分钟
> 材料：TB24 到 TB30 · 旧长版：`notes/p2-lang-core.md`、`notes/p30-methods-interfaces.md`
> 前置：你写过 Java/Kotlin/Rust/TS 中任意一门的面向对象或 trait 代码

## 先看一个让人愣住的现象

下面这段代码，`mayFail(false)` 明明"没出错"，返回值却不等于 `nil`。先自己猜一下输出，再往下读。

```go
package main

import "fmt"

type MyErr struct{}

func (*MyErr) Error() string { return "my err" }

func mayFail(bad bool) error {
	var p *MyErr // 类型 *MyErr，值 nil
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

```text
false
*main.MyErr true
true
```

第一行 `false` 就是 Go 最著名的坑。要真正看懂它，不能只背"不要返回具体类型的 nil 指针"，需要把接口在内存里的样子想清楚。而要想清楚接口，又得先明白 Go 怎么把"面向对象"拆成了三个互相独立的零件：

1. **类型加方法**：方法只是第一个参数叫 receiver 的函数，和类型之间没有层级。
2. **接口**：一组方法签名，**由使用方声明，实现方不用知道它存在**。
3. **嵌入**：编译器替你生成转发代码，不产生任何父子关系。

这三者互相正交，Go 管这叫"组合"。后面的小接口、装饰器、中间件、依赖注入，都是这三个零件的用法。这一节按这个顺序走，走到第五部分回头解释开头的现象。

## 和你已知语言的差异

| 问题 | Java / Kotlin | Rust | TS | Go |
|---|---|---|---|---|
| 类型怎么声明自己实现了接口 | `implements` 显式 | `impl Trait for T` 显式 | `implements` 可选，结构化 | 不声明，方法集合覆盖就算 |
| 接口谁来定义 | 通常实现方一侧 | trait 通常与类型同 crate | 使用方或提供方 | 惯例：**使用方** |
| 复用实现 | 继承、默认方法 | trait 默认方法、Deref | 继承、mixin | 嵌入（转发，不是继承） |
| 值还是引用决定能力 | 否 | `self`/`&self`/`&mut self` | 否 | 是：值类型和指针类型的方法集合不同 |
| 运行时多态 | 虚表 | `dyn Trait` 胖指针 | 原型链 | 接口值是（类型，数据）二元组 |

表里最陌生的是第四行。Java 里你从不关心调用方法时拿的是对象本身还是引用；Go 里"值"和"指向值的指针"是两个类型，能做的事不一样，下面第二、三部分专门讲。

## 一、方法就是首参数为 receiver 的函数

写 `t.Get()` 时，编译器把它翻译成 `T.Get(t)`：方法只是把 receiver 写在了函数名前面的普通函数。看穿这一点，"值还是指针"就退化成一个你早就熟悉的问题：传参时拷贝还是传地址。〔TB24 p6-8〕

```go
package main

import "fmt"

type T struct{ n int }

func (t T) Get() int   { return t.n }
func (t *T) Set(a int) { t.n = a }

func main() {
	set := (*T).Set // 方法表达式：receiver 变成第一个参数
	get := T.Get
	fmt.Printf("%T\n%T\n", set, get)

	var t T
	set(&t, 3)
	fmt.Println(get(t), t.n)

	t2 := T{1}
	(T).Get(t2)
	fmt.Println(t2.n)
}
```

```text
func(*main.T, int)
func(main.T) int
3 3
1
```

打印出的两个函数类型把真相摆在眼前：`Set` 的第一个参数是 `*main.T`，`Get` 的第一个参数是 `main.T`。值接收者拿到的是副本，所以在方法里改字段，外面看不到；指针接收者拿到地址，能改原实例。

Java 程序员可以这样对照：Go 里"静态方法"和"实例方法"没有区别，实例方法只是第一个参数提前写在了前面。

## 二、选 receiver：先看接口，再看修改，最后看拷贝

三条原则有优先级，不要按出现顺序用。〔TB25 p2-6〕〔TB25 p10-11〕

1. **类型要实现某个接口**：这个接口需要的方法统一用同一种接收者，否则值类型会被排除在外（见下一节）。
2. **方法要改原实例**：指针接收者。
3. 以上都不是：默认值接收者，缩小外部修改内部状态的"接触面"；类型很大、拷贝贵时换指针接收者。

同一类型不要混用两种接收者。混用的后果在下一节会看到：拿值类型的人会"少了一半方法"，而且光看方法列表很难一眼看出来。

## 三、方法集合：接口满足的唯一判据

这是整节最值得背下来的规则。〔TB25 p7-10〕

- 值类型 `T` 的方法集合只有**值接收者**的方法。
- 指针类型 `*T` 的方法集合是**两种接收者**的方法之和。
- 一个类型的方法集合是接口方法集合的超集或相等，它就实现了该接口。

先别急着记，看它怎么在代码里咬人：

```go fail
package main

type Iface interface {
	M1()
	M2()
}

type Impl struct{}

func (Impl) M1()  {}
func (*Impl) M2() {}

func main() {
	var _ Iface = &Impl{} // 通过
	var _ Iface = Impl{}  // 编译错
}
```

```text
Impl does not implement Iface (method M2 has pointer receiver)
```

这里有个常见的疑惑：既然 `Impl{}.M2()` 在变量可寻址时是能写的，为什么赋给接口就不行？区别在于两个场景编译器做的事不同：

- **直接调用**：编译器看到 `t.M2()`，而 `t` 是可寻址变量，就自动改写成 `(&t).M2()`。这是语法糖。
- **赋给接口**：要看类型本身的方法集合，没有自动取地址这回事。

标准库里惯用的编译期断言就利用了这个检查：`var _ json.Marshaler = (*RawMessage)(nil)`，意思是"如果有一天 `*RawMessage` 不再满足这个接口，让编译立刻失败"。

## 四、接口由使用方定义，而且要小

Java 里你习惯先写接口，再让实现类 `implements`。Go 的隐式满足把这个顺序倒了过来：**接口应该写在用它的地方**，而不是写在实现它的地方。函数只声明自己用到的那一点能力，其他都不管。〔TB30 p6-8〕

反例是直接依赖具体类型：

```go
func Save(f *os.File, data []byte) error // 只能写文件，难测试，签名锁死
```

正解是只依赖唯一用到的能力：

```go
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
)

func Save(w io.Writer, data []byte) error {
	_, err := w.Write(data)
	return err
}

func main() {
	_ = Save(os.Stdout, []byte("to stdout\n"))

	var buf bytes.Buffer // 测试里用内存代替文件
	_ = Save(&buf, []byte("to buffer"))
	fmt.Printf("%q\n", buf.String())
}
```

```text
to stdout
"to buffer"
```

同一个函数，调用方自己决定落点：磁盘传 `*os.File`，单元测试传 `bytes.Buffer`，网络传自定义 Writer。可测性、接口隔离、可扩展三件事靠同一次修改同时拿到。〔TB30 p7-8〕

**为什么小接口好**，可以给一个数得出来的理由：接口方法越少，能满足它的类型就越多，抽象程度越高。极限是空接口，万物皆可装，但信息量为零。〔TB28 p10-13〕落地时按三步走：先不管大小，把接口抽象出来；再按使用场合把大接口拆成小接口；最后看能不能拆到单方法。标准库的 `io.Reader` 也是后期才抽出来的，越偏业务的抽象越难一次抽对，不要指望第一版就对。〔TB28 p13-15〕

配套的创建原则：**接受接口，返回结构体**。〔TB30 p10〕`log.New(out io.Writer, ...) *Logger` 就是范本：参数放宽，让调用方有选择；返回值给具体类型，让调用方拿到全部能力。

## 五、接口值在内存里是什么

Go 的接口是这门语言里唯一"动静兼备"的东西。〔TB29 p1-2〕

- **静态**：接口变量有静态类型。`var err error = 1` 编译不过，编译器在赋值时检查右值的类型是否覆盖了接口的全部方法。
- **动态**：接口变量在运行时还记着右值的真实类型，叫动态类型。`fmt.Printf("%T", err)` 能打印出它。

这两面的好处也是两面：静态让你在编译期就知道有没有实现接口，动态让同一个变量先后装不同的类型，获得动态语言的"鸭子类型"灵活性，而不用声明继承关系。〔TB29 p2-4〕

要支撑这两面，接口变量里必须同时存两样东西，所以它在内存里是一个**二元组**（类型信息，数据指针）。有方法的接口是 `iface{tab, data}`，`tab` 指向一张"接口 x 动态类型"的绑定表；空接口是更简单的 `eface{_type, data}`。〔TB29 p1-9〕你不需要背 runtime 的结构体字段，只需要记住"两个槽"。

有了这个模型，三件事自然就解释清楚了。

**第一，相等。** 两个接口值相等，要类型信息相同，**且**数据所指的值相同。`int(17)` 和 `int64(17)` 装进接口后不相等，因为类型槽不同：〔TB29 p9-10〕

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

倒数第二行也值得留意：从未赋值的 `error` 接口，两个槽都是空，`%T` 打印 `<nil>`。

**第二，装箱有成本。** 任何类型赋给接口，都可能要分配一块内存并拷贝一份数据，因为数据槽只存一个指针。性能敏感的路径要少装箱。〔TB29 p21-23〕

**第三，开头那个现象。** 回到 `mayFail`：函数里的 `p` 是 `*MyErr` 类型的 nil 指针，`return p` 把它装进 `error` 接口。装完之后的接口是：

- 类型槽：`*MyErr`（非空）
- 数据槽：nil

而**接口等于 `nil`，要求两个槽都是空**。类型槽有值，所以 `err == nil` 为 false。第二行输出 `*main.MyErr true` 把这个事实拆开给你看：类型是 `*main.MyErr`，数据槽 nil，所以和 `(*MyErr)(nil)` 比较为 true。

修法很直接：函数返回类型是 `error`、没有错误时，就在那个位置写 `return nil`，让两个槽都是空，不要经过一个具体类型的变量（`mayFailOK` 就是这样）。对比一下其他语言：Rust 的 `Option<Box<dyn Error>>` 把"没有"放在类型里，所以没有这个坑；Java 的 null 引用语义单一，也不会"装箱后非 null"。Go 的这个坑是由"接口是二元组"加"指针可以为 nil"同时造成的。〔TB29 p3-5〕〔TB29 p23-25〕

## 六、嵌入：编译器生成的转发，不是继承

在结构体里只写类型名、不写字段名，就是嵌入。`s.Read` 被编译器改写成 `s.Reader.Read`，被嵌入类型的方法"提升"进外层的方法集合，造成继承的错觉。本质是组合里的代理模式。〔TB26 p9-10〕

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

`c.Hello()` 里调的仍然是 `Base.Who`：**没有虚调用**，被嵌入的类型根本不知道外层存在。Java 里子类重写后，父类方法里的 `this.who()` 会走到子类，Go 不提供这种行为。想要这种效果，只能像最后一个 `Hello` 函数那样，把可变的部分作为接口参数传进去。这条路也是 Go 的惯用解法：用接口做多态，不靠继承链。

嵌入后的方法集合有两条精确规则，常被拿来考：〔TB26 p15-20〕

- 外层结构体 `S` 嵌入 `T1` 和 `*T2`：`S` 的方法集合是 `T1` 的加 `*T2` 的；`*S` 的方法集合是 `*T1` 的加 `*T2` 的。
- `type S T` 是新类型，**不继承** `T` 的任何方法，方法集合为空；`type S = T` 是别名，与 `T` 完全相同。

还有一个实用技巧：测试里伪造依赖时，可以在结构体里嵌入接口，只覆写用到的方法，其余方法不实现也能编译；但未覆写的方法被调用时会 nil 解引用 panic，所以只适合你确定不会调到的场合。〔TB26 p13-15〕

## 七、水平组合三式：包装器、适配器、中间件

嵌入是"器官"，接口参数是"关节"。水平组合的基本形态只有一种：函数参数写接口类型。〔TB30 p9-16〕在此之上有三个衍生模式，复杂度递增。

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

从输出里看顺序：最外层先跑，`logMW(authMW(h))` 是先日志后鉴权；鉴权失败时没有走到业务处理函数。Java 对应 Servlet Filter，Rust 对应 tower 的 Layer。后面的分层、日志、限流、熔断都会反复用到这三个词。

## 八、空接口与 any

Rob Pike 的说法：空接口不提供任何信息，传 `any` 等于告诉编译器"别检查了"，错误被推迟到运行时。标准库里合理使用的只有容器算法（`sort`、`container/heap`）和格式化日志（`fmt`、`log`）。〔TB30 p16-17〕Go 1.18 起 `any` 是 `interface{}` 的别名，泛型能做的事优先用泛型（第 9 节）。

## 小结

1. 方法是首参数为 receiver 的函数，值接收者拿副本，指针接收者拿地址。
2. 选 receiver 的顺序：接口要求，其次是否修改，最后才是拷贝成本；同一类型不混用。
3. 方法集合决定接口是否满足：`T` 只含值接收者方法，`*T` 含两者。
4. 接口写在使用方，越小越好；接受接口，返回结构体。
5. 接口值是（类型，数据）二元组；两个槽都空才等于 nil。
6. 嵌入是转发不是继承，没有虚调用；要多态就传接口参数。
7. 包装器、适配器、中间件是同一种组合方式的三个层次。

## 常见坑

- 返回 `error` 的函数里用了具体类型的变量（尤其是 nil 指针）：没有错误请直接 `return nil`。
- 同一类型混用值/指针接收者，导致值类型无法满足接口。
- 以为嵌入能"重写"：父类方法里调用的永远是父类自己的版本。
- 到处用 `any`：编译器不再帮你查类型。
- 接口放在实现方：消费者被迫依赖整套实现，难 mock。

## 判断题与答案

先自己答，再看后面。

1. 为什么 `var _ Iface = &Impl{}` 过而 `Impl{}` 不过，但 `Impl{}.M2()` 在变量可寻址时能调用？
2. `type S T` 和 `type S = T` 的方法集合差别是什么？
3. `c.Hello()` 为什么打印 `hi base`？想要 `hi child` 该怎么改设计？
4. 一个函数返回 `error`，里面有 `var p *MyErr` 并 `return p`，调用方 `err != nil` 为什么是 true？

**答案**

1. 赋给接口看类型本身的方法集合（`Impl` 不含 `M2`）；直接调用时编译器对可寻址变量自动取地址。
2. `type S T` 是新类型，方法集合为空；`type S = T` 是别名，完全等同于 `T`。
3. 嵌入是转发，`Base.Hello` 里的 `b.Who()` 绑定到 `Base`，不知道外层存在。把可变部分作为接口参数传入（如上面的 `Hello(w Whoer)`）。
4. 接口等于 nil 需要类型槽和数据槽都空；这里类型槽是 `*MyErr`。

练习包 `exercises/p30/` 里可以直接跑对答案：`TestMethodSetMatrix`（方法集合矩阵）、`TestTypeAliasMethodSet`、`TestEmbedNotInherit`、`TestNilErrorTrap`、`TestCapReaderChain`（包装器链）、`TestMiddlewareCompose`（中间件）。

## 在项目里

- **隐式满足 + 适配器**：`dnsproxi-go/tcp/server.go` 里的 `streamConn` 没有声明实现 `net.PacketConn`，只是实现了它的方法，就能被传给原本为 UDP 写的 `handleQuery`。同文件的 `Handler` 是函数类型，签名与 `handleQuery` 一致，直接传方法值即可。
- **包装器**：`dnsproxi-go/logger.go` 的 `ctxHandler` 内部持有一个 `slog.Handler`，在 `Handle` 里补充请求 ID 再转发，是装饰器的真实用例。

## 想深入去哪查

- 原材料：TB24（方法本质）、TB25（方法集合与选型）、TB26（嵌入）、TB28（接口即契约）、TB29（运行时表示）、TB30（组合与三式）。
- 旧长版：`notes/p2-lang-core.md`、`notes/p30-methods-interfaces.md`（含 itab 结构、装箱基准、反射）。
- 反射、`runtime` 结构体字段、`trace` 工具：不在这一节，遇到再查 `C:dachang/reflection`。
