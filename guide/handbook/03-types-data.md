# 03 类型与数据：值语义、切片、map、字符串与结构体

> 详细程度：标准 · 约 25 分钟
> 材料：TB10 到 TB17、TB11、TB14 · 旧长版：`notes/p1_5-basics.md`
> 前置：01 Go 的世界观

## 这一节要建立的认知

Go 默认是**值语义**：赋值、传参、`range` 都拷贝。真正"共享"的只有那几个本身带指针的类型：切片、map、channel、函数、接口、指针。它们被拷贝的是一个小小的描述符，指向的数据仍然是同一份。

所以读任何 Go 代码，第一个问题永远是：**这里拷贝了什么，共享了什么**。下面每个坑都是这个问题的回答。

## 一、强类型与零值

类型分三类：基本数据类型、复合数据类型、接口类型。〔TB12 p1-2〕`int`、`uint`、`uintptr` 长度随平台，可移植的代码不要依赖它。〔TB12 p4-5〕整型溢出不报错，而是回绕：`int8` 的 127 加 1 得 -128。〔TB12 p5〕类型之间没有隐式转换（01 里"显式"那条的延伸）。

每个类型都有**零值**：数值 0，布尔 false，字符串空串，指针、切片、map、channel、函数、接口为 nil，结构体是各字段零值。好的 Go 类型设计让零值直接可用：`var mu sync.Mutex` 就能 `Lock`，`var buf bytes.Buffer` 就能 `Write`，对比 C 要先 `pthread_mutex_init`。〔TB17 p10-12〕

零值可用有两个例外要记牢，它们是最常见的运行时 panic 来源：

```go
var m map[string]int
fmt.Println(m["x"], len(m)) // 0 0   读 nil map 没问题
m["x"] = 1                  // panic: assignment to entry in nil map
var ns []int
ns = append(ns, 1)          // nil 切片 append 没问题
```

nil map 可读不可写，必须 `make` 或字面值初始化。〔TB16 p4-6〕

## 二、数组是值，切片是窗口

数组的长度是类型的一部分（`[5]int` 与 `[6]int` 是不同类型），变量代表整个数组，赋值和传参都是整块拷贝。切片是三元组：指向底层数组的指针、`len`、`cap`。〔TB15 p2-3〕〔TB15 p8-9〕

```go
a := [3]int{1, 2, 3}; b := a; b[0] = 9
s := []int{1, 2, 3};  t := s; t[0] = 9
fmt.Println(a, b, s, t) // [1 2 3] [9 2 3] [9 2 3] [9 2 3]
```

数组拷贝，切片共享。切片只拷贝三元组，所以传参开销恒定，但改元素会改到同一个底层数组。〔TB15 p9-10〕

`append` 的规则是两段式：容量够，写进同一个底层数组；容量不够，分配新数组、拷贝，之后与旧数组脱钩。

```go
base := make([]int, 3, 4)
sub := base[1:3]
sub = append(sub, 6) // 容量够，写进 base 的底层数组
fmt.Println(base[:4]) // [0 0 0 6]
sub = append(sub, 7)  // 超容量，新数组
sub[0] = 100
fmt.Println(base[:4], sub) // [0 0 0 6] [100 0 6 7]
```

扩容轨迹在本机 go1.26.4 上实测：`4 8 16 32 64 128 256 512 848 1280 1792 2560`，小的翻倍，到 256 之后增长放缓。**不要依赖具体数字**，只需要记住"可能换数组"，因此永远用 `s = append(s, ...)` 的返回值，并且别假定两个切片是否共享。

想让函数改调用方切片的内容，直接传切片；想让它增删元素，必须返回新切片（或传指针）。

## 三、map

- key 必须支持 `==`，切片、map、函数不能当 key。〔TB16 p3〕
- `v := m[k]` 查不到返回零值，区分"不存在"和"值恰为零"要用 `v, ok := m[k]`。〔TB16 p8-9〕
- 遍历顺序每次都不同，逻辑绝不能依赖遍历顺序；要稳定顺序，先取 key 排序。〔TB16 p9-12〕
- map 是引用类型，传参只传描述符，函数内的修改外面可见。〔TB16 p12〕
- map 不是并发安全的，并发读写要加锁或用 `sync.Map`（后面并发章详述）。

实现层面，课程讲的是哈希桶加 tophash 的版本：每个 bucket 默认 8 个槽，溢出挂链。〔TB16 p14-15〕**1.26 修正**：本机 go1.26.4 的 map 实现在 `internal/runtime/maps`，源码头部注释写明基于 Abseil 的 Swiss Table 设计，`grep -n "Swiss" $(go env GOROOT)/src/internal/runtime/maps/map.go` 可见。用法与语义不变，实现细节不要背课程里的桶结构。

## 四、字符串、字节与 rune

字符串不可变（`s[0] = 'k'` 编译错），可以整体重新赋值；没有结尾 `\0`，`len` 是 O(1)；源码和内存里都是 UTF-8。〔TB13 p3-6〕它是个描述符 `{Data, Len}`，传参只拷贝描述符。〔TB13 p10-12〕

最容易错的是"长度"有两种视角：

```go
str := "中国人"
fmt.Println(len(str), utf8.RuneCountInString(str), str[0]) // 9 3 228
for i, r := range str { fmt.Print(i, ":", string(r), " ") } // 0:中 3:国 6:人
```

`len` 是字节数，`s[i]` 是字节，`range` 才按 rune（即 Unicode 码点，`rune` 是 `int32` 的别名）迭代。〔TB13 p5-6〕〔TB13 p12〕处理中文、emoji 时别用下标切字符串。

## 五、结构体、定义类型与别名

- `type T S` 定义新类型，与 `S` 不同，只有底层类型相同才能显式转换；`type T = S` 是别名，完全等价，多用于渐进式重构。〔TB17 p2-5〕
- 初始化推荐 `T{Field: v}` 带字段名，顺序无关，新增字段不破坏旧代码；常写 `T{}` 而少用 `new(T)`。〔TB17 p13-14〕
- 空结构体 `struct{}` 占 0 字节，常作为 channel 里的"事件"信号。〔TB17 p6-7〕
- 结构体不能递归包含自身，除非通过指针、切片、map。〔TB17 p8-9〕
- 字段顺序影响大小：

```go
type Bad struct { a bool; b int64; c bool }  // Sizeof = 24
type Good struct { b int64; a bool; c bool } // Sizeof = 16
```

这是编译器为对齐插入填充的结果。〔TB17 p16-17〕〔TB17 p17-19〕对热路径上的大量小结构体，调整字段顺序是免费的内存优化。

## 六、常量与 iota

常量在编译期求值，并发访问不需要同步。〔TB14 p2〕Go 的创新是**无类型常量**：没写类型的常量可以按上下文隐式转成别的类型，转换发生在编译期，所以不破坏类型安全；转不了（如溢出 `int8`）就编译报错。〔TB14 p4-6〕没有 `enum` 关键字，枚举用 `const` 块加 `iota`：每行加 1，从 0 开始，后续行隐式重复前一行的表达式，每个块独立，例如 `Sunday Weekday = iota` 后面的 `Monday`、`Tuesday` 依次是 1、2。〔TB14 p8-12〕

## 七、作用域与遮蔽

作用域在编译期决定，外层声明的标识符对内层可见；`:=` 在内层块里会**新建**变量，遮蔽外层同名变量，这是最阴险的低级 bug。〔TB11 p1-2〕〔TB11 p4〕

```go
x := 1
var err error
if _, err := fmt.Sscan("12", &x); err != nil { /* ... */ } // 这个 err 是新变量
fmt.Println(x, err) // 12 <nil>，外层 err 没被这个 if 里的赋值影响
```

隐式代码块不止花括号：`if`、`for`、`switch` 自身各有一层，`switch` 的每个 `case` 又各有一层。〔TB11 p3〕预定义标识符（`new`、`len`、`true` 等）不是关键字，可以被遮蔽，`type new int` 之后 `new(int)` 就编译不过。〔TB11 p4-5〕实践上：不要用预定义名当变量名或类型名，函数保持短小。

Go 1.22 起，`for` 循环的变量**每次迭代新建一份**，闭包捕获不会再得到同一个变量：

```go
var fs []func()
for i := range 3 { fs = append(fs, func() { fmt.Print(i, " ") }) }
for _, f := range fs { f() } // 0 1 2
```

老教程里"循环变量被所有 goroutine 共享"的坑在 1.22 之后已不存在。

## 常见坑

1. 以为两个切片独立，其实共享底层数组；`append` 后是否脱钩取决于容量。
2. 用 `len(s)` 当字符个数，对中文得到字节数。〔TB13 p5-6〕
3. `if x, err := f(); ...` 里的 `err` 遮蔽外层 `err`。〔TB11 p1-2〕
4. 整型溢出悄悄回绕，循环结束条件用错类型时最容易触发。〔TB12 p5〕
5. nil map 写入 panic、遍历顺序当成稳定的：见第三节。

## 判断题（先答再看）

1. 函数 `func f(s []int) { s = append(s, 1) }`，调用方的切片变了吗？如果 `s` 的容量还有富余呢？
2. `var m map[string][]int; m["a"] = append(m["a"], 1)` 哪一步出问题？
3. 为什么 `x := 1; if true { x := 2 }` 之后外层 `x` 还是 1？
4. 结构体 `struct{a bool; b int64; c bool}` 为什么不是 10 字节？

练习包 `exercises/p1_5basics/` 里有切片共享、map 三坑、`rune` 预测题，可以直接跑对答案。

## 在项目里

- `dnsproxi-go/cache/cache.go` 里 `Cache` 用 `map[Key]*Entry`、`slot map[Key]int`、`access map[Key]uint64` 三张表，`Key` 是自定义结构体当 map key（可比较）。
- `dnsproxi-go/wire/wire.go` 处理 DNS 报文的字节切片，是 `[]byte` 视图与拷贝取舍的真实现场。
- `dnsproxi-go/main.go` 的 `Config` 把环境变量读成结构体，`loadConfig` 用 `envOr` 补默认地址，各功能开关为空或 0 即关闭。

## 想深入去哪查

- 类型与零值：TB12、TB17；字符串：TB13；常量与 iota：TB14；数组与切片：TB15；map：TB16；作用域：TB11；变量声明：TB10。
- 旧长版：`notes/p1_5-basics.md`（含 Rust 对照与练习预测题）。
- 切片与字符串的更底层结构（`StringHeader`、`unsafe` 窥视）：TB13 p10-12、TB15 p8-10。
