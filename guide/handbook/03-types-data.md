# 03 类型与数据：值语义、切片、map、字符串与结构体

> 详细程度：标准 · 约 25 分钟
> 材料：TB10 到 TB17、TB11、TB14 · 旧长版：`notes/p1_5-basics.md`
> 前置：01 Go 的世界观

## 先看一个长得像、行为相反的现象

下面两段代码做的事几乎一模一样：声明一个集合，赋给另一个变量，改新变量的第一个元素。唯一区别是类型，一个数组一个切片。先猜猜两行输出，再往下读。

```go
package main

import "fmt"

func main() {
	a := [3]int{1, 2, 3}
	b := a
	b[0] = 9
	fmt.Println("array:", a, b)

	s := []int{1, 2, 3}
	t := s
	t[0] = 9
	fmt.Println("slice:", s, t)
}
```

```text
array: [1 2 3] [9 2 3]
slice: [9 2 3] [9 2 3]
```

数组 `a` 原封不动，切片 `s` 却被"隔空"改了。`b := a` 和 `t := s` 写起来一模一样，行为完全相反，根源在于 Go 默认是**值语义**：赋值、传参、`range` 都拷贝。数组是"整块值"，拷贝它就是把每个元素复制一遍；切片却是一个小小的描述符，拷贝描述符并不拷贝它指向的那块数据。

所以读任何 Go 代码，第一个问题永远是：**这里拷贝了什么，共享了什么**。真正"共享"的只有那几个本身带指针的类型：切片、map、channel、函数、接口、指针，它们被拷贝的是一个小描述符，指向的数据仍是同一份。这一节后面每个坑，都是这个问题的回答。

## 和你已知语言的差异

| 问题 | Java / Kotlin | Rust | TS | Go |
|---|---|---|---|---|
| 集合变量赋值的语义 | 引用（共享同一对象） | `[T; N]` 是值（T: Copy 时复制） | 引用（共享同一对象） | 数组是值，切片共享底层数组 |
| 默认可变性 | 对象可变，String 不可变 | 默认不可变 | 可变 | 字符串不可变，切片/map 可变 |
| 字符与字符串 | `char` 是 UTF-16 单元 | `char` 是 Unicode 标量 | `string` 是 UTF-16 | `rune` 是码点，`string` 是 UTF-8 |
| 隐式类型转换 | 有隐式拓宽 | 无隐式转换 | 宽松 | 无隐式转换（无类型常量除外） |
| 枚举 | `enum` | `enum` | `enum`/联合 | 没有 enum，用 `const` + `iota` |

表里最反直觉的是第一行和第三行：Go 同时有"值"的数组和"引用"的切片，两个长得像的集合语义相反；而 Go 的"字符"不是 `char`，字符串内部是 UTF-8 编码的字节序列，这决定了取长度和切片的很多坑。下面按值语义、切片、map、字符串、结构体的顺序逐个讲。

## 一、值语义与零值

先建立最底层的认知：Go 的类型分三类，基本数据类型（数值、字符串、布尔）、复合数据类型（数组、切片、map、结构体等）、接口类型。〔TB12 p1-2〕其中数值类型又分平台无关的和平台相关的，`int`、`uint`、`uintptr` 长度随平台变化，可移植的代码不要依赖它的长度。〔TB12 p4-5〕

类型之间要求"一模一样"，没有隐式转换，这是 01 节"显式"那条的延伸。更隐蔽的是整型溢出：结果超出边界就悄悄回绕，不报错也不 panic。`int8` 的 127 加 1 得 -128，`uint8` 的 1 减 2 得 255。〔TB12 p5〕这类 bug 最常出现在循环结束条件的类型选错时。

每个类型都有一个**零值**，也就是不初始化时自动得到的默认值：数值 0，布尔 false，字符串空串，指针、切片、map、channel、函数、接口为 nil，结构体是各字段零值的组合。Go 的设计主张"零值可用"：`var mu sync.Mutex` 就能直接 `Lock`，`var buf bytes.Buffer` 就能直接 `Write`，不用像 C 那样先调 `pthread_mutex_init`。〔TB17 p10-12〕

但零值可用有两个例外要记牢，它们是最常见的运行时 panic 来源。先看 nil map 和 nil 切片：

```go
package main

import "fmt"

func main() {
	var m map[string]int
	fmt.Println(m["x"], len(m)) // 读 nil map 没问题

	var ns []int
	ns = append(ns, 1) // nil 切片 append 没问题
	fmt.Println(ns)
}
```

```text
0 0
[1]
```

nil map 读和取长度都正常，nil 切片 append 也正常。但 nil map **不能写**：

```go
var m map[string]int
m["x"] = 1 // panic: assignment to entry in nil map
```

为什么读没事写就 panic？因为 map 的底层哈希表在 nil 时还没分配，读一个不存在的 key 可以直接返回零值，而写入必须往哈希表里插槽，没表可插，运行时只能 panic。map 必须先 `make` 或字面值初始化才能写。〔TB16 p4-6〕切片则不同，append 发现底层数组是 nil 时自己会去分配，所以零值切片可直接用。

## 二、数组是值，切片是窗口

数组的长度是类型的一部分，`[5]int` 与 `[6]int` 是两个不同类型，不能互传；变量代表整个数组，赋值和传参都是整块拷贝。〔TB15 p2-3〕切片是三元组：指向底层数组的指针、`len`、`cap`。〔TB15 p8-9〕拷贝切片只拷贝这个三元组（三个字大小），所以传参开销恒定，但改元素会改到同一个底层数组。〔TB15 p9-10〕开头那个现象就是这两条规则的直接后果。

`append` 的行为是两段式：容量够，就写进同一个底层数组；容量不够，就分配新数组、把旧数据拷过去，之后新旧切片脱钩。用代码看最清楚：

```go
package main

import "fmt"

func main() {
	base := make([]int, 3, 4)
	sub := base[1:3]
	sub = append(sub, 6) // 容量够，写进 base 的底层数组
	fmt.Println(base[:4])
	sub = append(sub, 7) // 超容量，换新数组
	sub[0] = 100
	fmt.Println(base[:4], sub)
}
```

```text
[0 0 0 6]
[0 0 0 6] [100 0 6 7]
```

`sub` 从 `base` 切出，cap 是 3（从下标 1 到数组末尾还有 3 个位置）。第一次 append 只写进第 4 个位置，`base` 被"顺带"改了；第二次 append 超出容量，`sub` 换了新数组，之后改 `sub[0]` 不再影响 `base`。所以"切片是否共享"取决于容量，不是谁都能一眼看穿。这也是为什么永远要写 `s = append(s, ...)`：append 可能换数组，返回值必须接住，别假定两个切片还共享。

扩容轨迹本机 go1.26.4 实测是 `1 2 4 8 16 32 64 128 256 512 848 1280 1792 2560`，小容量翻倍，256 之后增长放缓。**不要依赖这些具体数字**，只需要记住"可能换数组"这一件事。

想让函数改调用方切片的内容，直接传切片（共享底层数组）；想让函数增删元素（可能换数组），必须返回新切片，或传指向切片的指针。

## 三、map

map 是无序的键值对，用 key 唯一关联到一个 value。四条规则直接记住：

- key 必须支持 `==` 比较，切片、map、函数不能当 key。〔TB16 p3〕
- `v := m[k]` 查不到返回零值；要区分"不存在"和"值恰为零"，用 `v, ok := m[k]`。〔TB16 p8-9〕
- 遍历顺序每次都不同，逻辑绝不能依赖遍历顺序；要稳定顺序，先把 key 取出来排序。〔TB16 p9-12〕
- map 是引用类型，传参只传描述符，函数内的修改调用方可见。〔TB16 p12〕

另外 map 不是并发安全的：多个 goroutine 同时读写会 panic，要加锁或用 `sync.Map`（并发章详述）。

关于 map 的底层实现，老课程讲的是"哈希桶加 tophash、每个 bucket 8 个槽、溢出挂链"的版本。〔TB16 p14-15〕但本机 go1.26.4 的实现已经换成了基于 Abseil Swiss Table 的设计：源码头部注释明写 `The map design is based on Abseil's "Swiss Table" map design`，`grep -n "Swiss" $(go env GOROOT)/src/internal/runtime/maps/map.go` 就能看到。用法与语义不变，桶结构那套细节不用背。

## 四、字符串、字节与 rune

Go 原生支持 string 类型，和 C 的"以 `\0` 结尾的字符数组"划清界限。字符串不可变，`s[0] = 'k'` 编译不过，但可以整体重新赋值；没有结尾 `\0`，`len` 是 O(1)；源码和内存里都是 UTF-8 编码。〔TB13 p3-6〕它在内存里是一个 `{Data, Len}` 描述符，传参只拷贝描述符，不拷贝内容。〔TB13 p10-12〕

最容易错的是"长度"有两种视角：

```go
package main

import (
	"fmt"
	"unicode/utf8"
)

func main() {
	str := "中国人"
	fmt.Println(len(str), utf8.RuneCountInString(str), str[0])
	for i, r := range str {
		fmt.Print(i, ":", string(r), " ")
	}
	fmt.Println()
}
```

```text
9 3 228
0:中 3:国 6:人
```

`len` 是字节数（"中国人"三个汉字各 3 字节），`s[i]` 取到的是单个字节（228 是"中"的第一个字节），`range` 才按 rune 迭代，索引是字节偏移 0、3、6。〔TB13 p5-6〕〔TB13 p12〕`rune` 是 `int32` 的别名，代表一个 Unicode 码点。所以处理中文、emoji 时别用下标切字符串，会从多字节字符中间切断。

## 五、结构体、定义类型与别名

结构体把多个字段聚合成一个实体。在定义结构体之前，先分清自定义类型的两种写法：`type T S` 定义一个新类型，与 `S` 是两个类型，只有底层类型相同才能显式转换；`type T = S` 是别名，完全等价，多用在大规模渐进式重构时给类型改名。〔TB17 p2-5〕比如 `type MyInt int` 和 `int` 底层类型相同，能 `int(x)` 互转，但 `type MyInt string` 和 `int` 底层类型不同，`int(x)` 直接编译不过。

初始化结构体推荐带字段名 `T{Field: v}`：顺序无关，以后新增字段不会破坏旧代码；常写 `T{}` 而少用 `new(T)`。〔TB17 p13-14〕空结构体 `struct{}` 占 0 字节，常拿来做 channel 里的"事件"信号，只传信号不传数据。〔TB17 p6-7〕结构体不能递归包含自身，除非通过指针、切片、map 间接包含。〔TB17 p8-9〕

最后是字段顺序影响大小这件事，很多人第一次听说时会愣一下：

```go
package main

import (
	"fmt"
	"unsafe"
)

type Bad struct {
	a bool
	b int64
	c bool
}

type Good struct {
	b int64
	a bool
	c bool
}

func main() {
	fmt.Println(unsafe.Sizeof(Bad{}), unsafe.Sizeof(Good{}))
}
```

```text
24 16
```

两个结构体字段完全一样，只是顺序不同，`Bad` 占 24 字节，`Good` 占 16。原因是编译器为了对齐，会在字段间插入填充：`int64` 必须放在 8 的倍数地址上，`Bad` 里 `bool` 后面要补 7 字节再放 `int64`，末尾再补 7 字节凑成 8 的倍数。〔TB17 p16-17〕〔TB17 p17-19〕对热路径上的大量小结构体，调一下字段顺序是免费的内存优化。

## 六、常量与 iota

常量在编译期求值，运行时不会变，并发访问不需要同步。〔TB14 p2〕Go 在常量上有两个"创新"。一是**无类型常量**：不写类型的常量可以按上下文隐式转成别的类型，转换发生在编译期，所以不破坏类型安全；转不了（如溢出 `int8`）就编译报错。〔TB14 p4-6〕这让 `const n = 13` 能和 `int8`、`int64` 等混用而不用写显式转型，但换成会溢出的常量值编译器立刻拦下。

二是用常量实现枚举。Go 没有 `enum` 关键字，枚举用 `const` 块加 `iota`：

```go
package main

import "fmt"

type Weekday int

const (
	Sunday Weekday = iota
	Monday
	Tuesday
	Wednesday
	Thursday
	Friday
	Saturday
)

func main() {
	fmt.Println(Sunday, Monday, Tuesday, Saturday)
}
```

```text
0 1 2 6
```

`iota` 在每个 `const` 块里从 0 开始，每行加 1，后续行隐式重复前一行的表达式，所以 `Monday`、`Tuesday`、`Saturday` 依次是 1、2、6；每个 `const` 块独立计数。〔TB14 p8-12〕

## 七、作用域与遮蔽

作用域在编译期决定，外层声明的标识符对内层可见。真正咬人的是 `:=`：它在内层块里会**新建**变量，遮蔽外层同名变量，这是最阴险的低级 bug。〔TB11 p1-2〕〔TB11 p4〕

```go
package main

import "fmt"

func main() {
	x := 1
	var err error
	if _, err := fmt.Sscan("12", &x); err != nil {
		// 这里的 err 是 if 语句里的新变量
	}
	fmt.Println(x, err)
}
```

```text
12 <nil>
```

`x` 被改成了 12（`Sscan` 真的写进了 `&x`），但外层 `err` 还是 nil，因为 `if` 里的 `:=` 声明了一个**新的** `err`，遮蔽了外层的，错误被静默丢弃。这类 bug 难查就在于代码看起来"确实处理了 err"，其实处理的是另一个变量。

隐式代码块不止花括号：`if`、`for`、`switch` 自身各有一层，`switch` 的每个 `case` 又各有一层。〔TB11 p3〕预定义标识符（`new`、`len`、`true` 等）不是关键字，可以被遮蔽：`type new int` 之后 `new(int)` 就编译不过。〔TB11 p4-5〕实践上两条：不要用预定义名当变量或类型名；函数保持短小，缩短遮蔽的查找范围。

Go 1.22 起 `for` 循环的变量**每次迭代新建一份**，闭包捕获不会再拿到同一个变量：

```go
package main

import "fmt"

func main() {
	var fs []func()
	for i := range 3 {
		fs = append(fs, func() { fmt.Print(i, " ") })
	}
	for _, f := range fs {
		f()
	}
	fmt.Println()
}
```

```text
0 1 2
```

老教程里"循环变量被所有 goroutine 共享、闭包都打印最后一个值"的坑，在 1.22 之后已不存在。

## 小结

1. Go 默认值语义，赋值传参都拷贝；真正共享的只有带指针的类型：切片、map、channel、函数、接口、指针。
2. 数组是值（整块拷贝），切片是三元组（指针、len、cap），拷贝描述符不拷贝数据。
3. append 可能换数组，永远接住返回值；切片是否共享取决于容量。
4. map：key 要可比较，读不到返零值要用 `v, ok` 区分，遍历无序，不是并发安全。
5. 字符串不可变、UTF-8 字节序列，`len` 数字节，`range` 数 rune。
6. `type T S` 是新类型，`type T = S` 是别名；结构体字段顺序影响内存占用。
7. 无类型常量按上下文隐式转型，枚举用 const + iota。
8. `:=` 在内层块会新建变量遮蔽外层；1.22 起 for 循环变量每次迭代独立。

## 常见坑

1. 以为两个切片独立，其实共享底层数组；append 后是否脱钩取决于容量。
2. 用 `len(s)` 当字符个数，对中文得到的是字节数。〔TB13 p5-6〕
3. `if x, err := f(); ...` 里的 `err` 遮蔽外层 `err`，错误被静默丢弃。〔TB11 p1-2〕
4. 整型溢出悄悄回绕，循环结束条件类型选错时最容易触发。〔TB12 p5〕
5. nil map 写入 panic、把遍历顺序当成稳定的：见第三节。

## 判断题与答案

先自己答，再看后面。

1. 函数 `func f(s []int) { s = append(s, 1) }`，调用方的切片变了吗？如果 `s` 的容量还有富余呢？
2. `var m map[string][]int; m["a"] = append(m["a"], 1)` 哪一步出问题？
3. 为什么 `x := 1; if true { x := 2 }` 之后外层 `x` 还是 1？
4. 结构体 `struct{ a bool; b int64; c bool }` 为什么不是 10 字节？

答案：

1. 调用方的切片值没变，因为 `append` 的返回值只赋给了 `f` 里的局部变量 `s`。容量有富余时，新元素确实写进了共享的底层数组，但调用方的 `len` 没变，看不到；容量不足时换新数组，调用方完全无感。
2. 读 `m["a"]` 和 `append` 都没问题（nil map 可读，nil 切片可 append），出问题的是最后的赋值 `m["a"] = ...`：它要向 nil map 写入，nil map 不可写，直接 panic `assignment to entry in nil map`。
3. 内层 `x := 2` 用 `:=` 在内层块里新建了一个同名变量，遮蔽了外层 `x`；赋值只发生在内层变量上，外层不受影响。
4. 编译器为对齐插入填充：`int64` 要对齐到 8 的倍数，`bool` 后面补 7 字节，末尾 `bool` 后再补 7 字节凑成 8 的倍数，实际是 24 字节，不是 1+8+1=10。

## 在项目里

- `dnsproxi-go/cache/cache.go` 里 `Cache` 用 `map[Key]*Entry`、`slot map[Key]int`、`access map[Key]uint64` 三张表，`Key` 是自定义结构体当 map key（可比较）。
- `dnsproxi-go/wire/wire.go` 处理 DNS 报文的字节切片，是 `[]byte` 视图与拷贝取舍的真实现场。
- `dnsproxi-go/main.go` 的 `Config` 把环境变量读成结构体，`loadConfig` 用 `envOr` 补默认地址，各功能开关为空或 0 即关闭。

## 想深入去哪查

- 类型与零值：TB12、TB17；字符串：TB13；常量与 iota：TB14；数组与切片：TB15；map：TB16；作用域：TB11；变量声明：TB10。
- 旧长版：`notes/p1_5-basics.md`（含 Rust 对照与练习预测题）。
- 切片与字符串的更底层结构（`StringHeader`、`unsafe` 窥视）：TB13 p10-12、TB15 p8-10。
