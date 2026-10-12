---
title: P1.5 · 语法基础速通：12 个小节走完 Go 的日常语法
---

# P1.5 · 语法基础速通：12 个小节走完 Go 的日常语法


> 你已经会 Rust，这章不教编程，只把 Go 日常语法的地图铺开：每个小节
> = 一个语法点 + Rust 对照 + 一个可跑例子。粒度对标 Go by Example 和
> rust-for-frontend 的 Syntax 章：前者给节粒度，后者给对照密度。
> P2 讲"换直觉的四个迁移误判"（接口/嵌入/错误/零值），本章讲"语法
> 本身长什么样"，两章互补不重叠。
>
> 读者：读完 P1（工具链），还没系统看过 Go 语法。前置：无。
> 后续：P2 换语言直觉 → P2.5 惯用法 → P3 上并发。

## 这章学完你能干什么

不看文档写出 Go 日常代码：变量/常量/for 全形态/切片底层操作/map 三
坑/字符串 rune 处理/switch 变体/defer 语义/方法与多返回值。练习终点：
`exercises/p1_5basics/` 预测题全对、观察题全跑、改造题能修回。

12 个小节（依赖序，每节 2-3 分钟）：

1. 变量与多返回值
2. 常量与 iota
3. for 与 range 全形态
4. 数组、切片与底层结构
5. map：三个必须背下的坑
6. 字符串、rune 与 []byte
7. switch 变体
8. defer 的三条精确语义
9. panic 与 recover 的边界
10. 方法：值接收者 vs 指针接收者
11. 函数值、闭包与方法值
12. 值语义 vs 引用语义

## §1 变量与多返回值

Go 的声明类型在后，`:=` 是"声明并推断"的语法糖：

```go
var a int = 1        // 全量写法
var b = 1            // 类型推断
c := 1               // 短声明：函数内最常用；不能用于包级
var x, y = 1, "hi"   // 平行声明
```

Rust 对照：`let a: i32 = 1;` / `let b = 1;`。差异：Go 变量**声明不
用就是编译错**（Rust 只是 warning），未使用参数用 `_` 丢弃。

**多返回值是 Go 函数签名的常态**（Rust 要包 tuple）：

```go
func div(a, b int) (int, error) {   // 两个返回值，第二个是 error
    if b == 0 {
        return 0, errors.New("div by zero")
    }
    return a / b, nil
}
q, err := div(7, 2)   // 调用方两个都要接；不接的用 _
```

命名返回值（named return）把返回变量提前声明，配合 naked return：

```go
func split(sum int) (x, y int) {   // x, y 已在返回处声明
    x = sum * 4 / 9
    y = sum - x
    return                          // naked return：返回 x, y
}
```

惯例：短函数可以用 naked return；长函数禁用（可读性崩坏）。
error 在最后一位是全生态硬约定（P2 问题 3 展开）。

## §2 常量与 iota

Go 常量是**编译期**值，只能是数字/字符串/布尔（Rust const 同约束，
但 Go 没有 `static mut` 对应物，可变全局就是普通 `var`）：

```go
const Pi = 3.14159          // 无类型常量：精度无限，用时有默认类型
const (
    KB = 1 << 10
    MB = KB << 10            // 常量表达式可引用前一个
)
```

`iota` 是常量块里的行号计数器，**每行 +1**，枚举的官方写法：

```go
type Level int

const (
    Debug Level = iota      // 0
    Info                    // 1（iota 隐式重复上一行表达式）
    Warn                    // 2
    Error                   // 3
)

const (
    _  = iota               // 跳过 0
    KB = 1 << (10 * iota)   // 1<<10
    MB                      // 1<<20
    GB                      // 1<<30
)
```

Rust 对照：`enum Level { Debug, Info, ... }` 是 sum type，Go 的
"枚举"只是 `int + 常量`，**没有类型安全**（`Level(99)` 编译通过），
配合 stringer 工具生成 `String()`（见 P19 生态章）。

## §3 for 与 range 全形态

Go 只有一种循环 `for`，四种形态覆盖 Rust 的 loop/while/for：

```go
for i := 0; i < 10; i++ { }    // C 形态
for x < 100 { }                 // while（省前后段）
for { }                         // loop（死循环，配 break）
for i, v := range xs { }        // Rust for (i, &v) in xs.iter().enumerate()
```

`range` 的目标共 8 类（语言规范 range 小节，Go 1.26 仍是这 8 类），
返回值个数不同（这是高频坑源）：

| 目标 | 形态 | 等价 Rust |
|---|---|---|
| 切片/数组 | `for i, v := range s` | `.iter().enumerate()`，**v 是拷贝** |
| 只按下标 | `for i := range s` | `(0..len)` |
| map | `for k, v := range m` | 迭代**无序**（每次运行顺序可能不同） |
| 字符串 | `for i, r := range str` | i 是**字节下标**，r 是 rune（见 §6） |
| channel | `for v := range ch` | 迭代到 close（P3） |
| 整数（1.22） | `for i := range n`，不用下标写 `for range n` | `for i in 0..n` |
| 函数 1 值（1.23） | `for v := range seq`，seq 是 `iter.Seq[V]` | `impl Iterator<Item = V>` |
| 函数 2 值（1.23） | `for k, v := range seq2`，seq2 是 `iter.Seq2[K, V]` | `Iterator<Item = (K, V)>` |

函数形态还有 0 值版本（`func(yield func() bool)`），极少用。
函数迭代器是"推式"（迭代器调用循环体，循环体返回 false 即 break），
Rust 的 Iterator 是"拉式"（调用方 `next()`），所以 Go 里没有
`.map().filter()` 链，组合靠 `slices`/`maps` 包的函数（P8 迭代器节）。

Go 1.22 起 range 循环变量**每轮新建**，闭包捕获不再踩共享变量的坑
（1.22 前是全 notes 最经典的 bug 源）。

## §4 数组、切片与底层结构

**数组是值**：`[3]int` 长度是类型的一部分，赋值即整体拷贝（Rust 的
`[T; 3]` 一样）。日常几乎不用数组，用切片：

```go
s := []int{1, 2, 3}          // 切片字面量（注意有 []，没长度）
s := make([]int, 0, 8)       // len=0 cap=8：知道容量就预分配
s = append(s, 4)             // 追加；超过 cap 时扩容（通常翻倍）
n := len(s); c := cap(s)     // 长度 / 容量
sub := s[1:3]                // 子切片：**共享底层数组**
```

切片的底层是三元组 `(指针, len, cap)`，指向一块底层数组。Rust 对照：
`&[T]`（fat pointer），但 Rust 借用检查保证了不共享可变，Go 没有：

```go
a := []int{1, 2, 3, 4}
b := a[1:3]        // len=2 cap=3，和 a 共享底层数组
b[0] = 99          // **a[1] 也变成 99**！
b = append(b, 5)   // cap 够，仍写进 a 的底层数组：a[3] 变 5
```

**推论**：函数返回子切片时想清楚要不要 `copy`；`append` 后切片可能
指向新数组，"append 一定原地"是错的。防共享的标准动作：

```go
b := a[1:3:3]      // 三索引切片：cap 限到 3，append 必然另分配
dst := make([]int, len(a)); copy(dst, a)   // 或 slices.Clone(a)
```

**nil 切片是合法零值**：`var s []int` 可以直接 `len(s)`、`append`、
`range`，不用先 make（Rust 没有 None 切片这种状态，这是零值可用
的代表，P2 问题 4）。

## §5 map：三个必须背下的坑

```go
m := map[string]int{"a": 1}   // 字面量
m := make(map[string]int, 8)  // 预估容量
v := m["a"]                   // 取值，缺键得零值（不报错）
v, ok := m["a"]               // comma-ok：ok=false 表示键不存在
delete(m, "a")                // 删除（键不存在也不报错）
```

**坑 1：取缺键返回零值，不报错。** `m["missing"]` 得 `0`，逻辑 bug
静默通过。需要区分"没有"和"零值"时必须用 comma-ok。

**坑 2：nil map 能读不能写。** `var m map[string]int` 声明后读合法
（得零值）、写直接 panic。零值可用的边界案例（对照 nil 切片可
append）。

**坑 3：迭代无序 + 并发读写直接炸进程。** `for k := range m` 每次顺序可能
不同（runtime 故意随机化，防止依赖顺序）；goroutine 同时读写 map
大概率直接 fatal error（recover 接不住，进程必死；开 `-race` 才会
降级成 DATA RACE 报告），加 `sync.RWMutex` 或分片锁
（P3 决策树）。

键类型必须是可比较的（`comparable`）：slice/map/函数不能当键，
struct 可以（字段全可比较时）。Rust 对照：`HashMap<K, V>` 要
`K: Hash + Eq`，Go 用可比性近似。

## §6 字符串、rune 与 []byte

字符串是**只读字节序列**（UTF-8），不可变（Rust `&str` 对照，不是
`String`：Go 改字符串只能重建）。核心认知：**len 和索引按字节算，
不是字符**：

```go
s := "Go语言"
len(s)        // 8：Go(2字节) + 语言(各3字节)
s[2]          // byte(=uint8)，值 232："语"的第一个字节，不是'语'
for i, r := range s { }   // i 是字节下标，r 是 rune(int32)
[]rune(s)     // 转码点切片：len=4，按字符索引
[]byte(s)     // 转字节切片：可变副本，改完 string(b) 转回（发生拷贝）
```

rune = Unicode 码点（int32 别名）。**处理"第 n 个字符"必须先转
`[]rune`**；纯 ASCII 时不转也正确，所以这个坑在英文环境藏得住：

```go
for _, r := range []rune("Go语言") { }   // 4 次，每个是完整字符
utf8.RuneCountInString(s)                 // 字符数 4（不分配）
```

常用操作在 `strings` 包（对照 Rust 的 str 方法）：

```go
strings.Join(parts, ", ")     // parts.join(", ")
strings.Split(s, ",")         // s.split(',')
strings.Contains/HasPrefix/TrimSpace/ToUpper/ReplaceAll
strconv.Itoa(42) / strconv.Atoi("42")   // 数字↔字符串（fmt 慢，热路径用这个）
fmt.Sprintf("%s=%d", k, v)    // format! 对标
```

## §7 switch 变体

Go 的 switch 默认**每分支自动 break**（C 系的 fallthrough 才要显式
写）。四类形态：

```go
// 1. 常规：case 值可多个
switch day {
case "sat", "sun":
    fmt.Println("weekend")
default:
}

// 2. 无表达式 switch：＝ if-else 链的整洁版（最常用）
switch {
case score >= 90:
    grade = "A"
case score >= 60:
    grade = "B"
default:
    grade = "F"
}

// 3. 带初始化语句：变量作用域限于 switch
switch n := compute(); {
case n < 0:
}

// 4. type switch：接口的运行时收窄（P2 问题 1 的搭档）
switch x := v.(type) {
case string:
case *MyErr:
}
```

`fallthrough` 强制进入下一分支（几乎不用，见到先怀疑是 bug）。
Rust 对照：match 要求穷尽，switch 不要求 default；type switch 等价
`match downcast`。

## §8 defer 的三条精确语义

`defer` 把函数调用推迟到**当前函数返回前**执行（Rust 的 `Drop` 是
作用域结束，Go 的 defer 是函数边界：粒度不同）：

```go
f, err := os.Open(p)
if err != nil { return err }
defer f.Close()               // 函数任何出口都会执行（≈ guard + drop）
```

三条精确语义（每条都能出面试题）：

1. **LIFO**：多个 defer 后进先出（像栈）。开资源顺序关回来。
2. **参数立即求值**：defer 行的参数当时就算好，执行时只差函数体：

```go
i := 0
defer fmt.Println("defer:", i)   // 打印 0！参数 i 此刻已拷贝
i = 7
```

   要引用最新值就传指针或闭包：`defer func() { log(i) }()`。

3. **能改命名返回值**：defer 在 return 之后跑，摸得到命名返回值：

```go
func f() (err error) {
    defer func() { if r := recover(); r != nil { err = fmt.Errorf("recovered: %v", r) } }()
    panic("boom")            // 最终 err 非 nil，不崩
}
```

资源清理三件套：`defer mu.Unlock()`、`defer wg.Done()`、
`defer close(ch)`：**defer 紧跟资源获取**是全生态纪律（P14 的
gen 每个例子都是这么写的）。

## §9 panic 与 recover 的边界

panic ≈ Rust `panic!`：程序 bug（数组越界、nil 解引用、断言失败），
**不是错误处理机制**。error 是返回值，panic 是不可恢复异常：

```go
func mustParse(s string) int {
    n, err := strconv.Atoi(s)
    if err != nil {
        panic(fmt.Sprintf("config parse %q: %v", s, err))   // 程序员 bug
    }
    return n
}
```

recover 只在 **deferred 函数里**有效，把 panic 转成 error：

```go
defer func() {
    if r := recover(); r != nil {
        err = fmt.Errorf("panic: %v", r)   // 命名返回值（§8 语义 3）
    }
}()
```

边界：**库代码禁止 recover 吞 panic**（把 bug 藏起来）；服务边界
（HTTP handler）net/http 会 recover 兜底：进程不死，但该连接被关闭
（客户端看到 EOF，HTTP/2 发 RST_STREAM），不是稳稳转 500。
goroutine 的 panic 没有天然边界，必须每个 goroutine 入口自己
recover：P3 的 TestPanicKillsProcess 让你亲眼看进程死。

## §10 方法：值接收者 vs 指针接收者

方法是带接收者的函数，接收者就是第一个参数（Rust 的 `self`）：

```go
type Counter struct{ n int }

func (c Counter) Value() int { return c.n }     // 值接收者：拿到拷贝
func (c *Counter) Add(d int) { c.n += d }       // 指针接收者：能改原值
```

选型规则（背下来，全生态一致）：

- **要改接收者 → 指针接收者**。
- 结构体大（拷贝贵）或含锁/文件句柄 → 指针接收者（保持同一实例）。
- map/slice/chan 类型字段自带引用语义，值接收者也能"改到"内容。
- **一致性 > 单点最优**：一个类型的混用是坏味道，要么全值要么全
  指针（除非小只读类型如 `time.Time`）。

方法集规则（接口满足性由此决定，精确版在 P2 章末参考）：`*T` 的
方法集 = 值 + 指针方法；`T` 的方法集 = 只有值方法。所以：

```go
var c Counter
var _ Adder = &c     // *Counter 满足 Adder（有 Add）
var _ Adder = c      // 编译错：Counter 没有 Add 在方法集里
```

## §11 函数值、闭包与方法值

函数是一等公民（Rust `fn`/closure 对应物）：

```go
var op func(int, int) int = add      // 函数类型
op := func(a, b int) int { return a + b }   // 匿名函数字面量
```

闭包捕获**引用**（不是 Rust 的 move/borrow 语义，没有 Fn/FnMut
区分，可变捕获无痕）：

```go
func counter() func() int {
    n := 0
    return func() int { n++; return n }   // n 被闭包持有
}
```

方法值与方法表达式（易混淆的一对）：

```go
f := c.Add          // 方法值：绑定接收者 c（值类型变量自动取址），签名 func(int)
g := (*Counter).Add // 方法表达式：不绑定，签名 func(*Counter, int)：接收者变首参
// 写 Counter.Add 编译错：invalid method expression（Add 是指针方法）
```

Go 1.22+ 循环变量每轮新建，闭包捕获循环变量安全了（1.22 前
`for ... { go func(){ use(i) }() }` 全员共享最后一个 i，是史诗级
坑源，读老代码要警惕）。

## §12 值语义 vs 引用语义

Go 一切传参/赋值都是**拷贝**（没有 Rust 的 move 语义，也没有隐式
引用），但某些类型内部含指针，拷贝的是"头"：

| 类型 | 赋值/传参拷贝什么 | 修改会否影响原值 |
|---|---|---|
| int/float/bool/struct | 全部字节 | 否 |
| string | (ptr, len) 头 | 否（只读） |
| 切片 | (ptr, len, cap) 头 | **共享底层数组，改元素互相可见** |
| map | 指针 | **是** |
| channel/函数/接口 | 指针 | 是 |
| 数组 `[N]T` | 全部字节 | 否 |

一句话：**Go 的"引用类型"就是含指针的 struct 的语法糖**。跨函数改
slice 元素能生效，但 `append` 可能换底层数组而"改不到"调用方：
所以库函数改长度时返回新切片 `xs = append(xs, ...)`。

Rust 最大的观念差异：Go 没有 `&`/`mut` 标记，**谁共享可变状态在
签名上看不出来**，靠约定（指针接收者、slice/map 参数暗示共享）+
`-race` 兜底。写 Go 时把"这里是不是共享了"当成每次赋值的自检项。

## 动手：练习 p1_5basics（30-45 分钟）

```sh
go test ./exercises/p1_5basics/ -v          # 全绿 = 参考实现对
go test -run TestSliceShare -v ./exercises/p1_5basics/   # 单跑
```

**预测题**（先写答案再跑）：

1. `TestSliceShare`：`b := a[1:3]; b[0] = 99` 后 `a[1]` 是多少？
   `append(b, 5)` 后 `a[3]` 呢？改成三索引切片 `a[1:3:3]` 再 append，
   `a` 会变吗？（§4）
2. `TestMapZero`：`m["missing"]` 返回什么？怎么区分"键不存在"和
   "值就是零值"？向 nil map 写入会发生什么？（§5）
3. `TestDeferOrder`：三个 defer 的输出顺序？`defer fmt.Println(i)`
   在 `i = 7` 之后执行，打印几？闭包版本呢？（§8）
4. `TestRuneIndex`：`"Go语言"[2]` 的类型和值？`len()` 和
   `utf8.RuneCountInString()` 各返回几？（§6）

**观察题**：写个 10 行小程序：建 5 键 map，连跑几次 `for k := range m`
拼出键序并打印，多跑几次验证顺序真的会变（§5 坑 3 的实证；练习测试
里的 map 断言只看值，不打印顺序）。iota 的"输出"用 `go test -run
TestIota -v` 看断言：错一个值就 FAIL 并打印实际值。

**对照题**（断言即答案，读完对应小节再看）：`TestIota`（§2 枚举无类型
安全）、`TestGrade`（§7 无表达式 switch）、`TestValueSemantics`（§12
struct 拷贝 vs slice 共享）。

**改造题**：`TestPointerReceiver` 里把 `Add` 改成值接收者，看编译
错；再把接口断言改成 `&c`，想清楚为什么就绿了（§10）。

## Take-away

1. `:=` 声明推断、未使用即编译错、error 在最后一位、多返回值是常态。
2. 切片 = (ptr, len, cap) 头，子切片共享底层数组；要隔离用三索引
   切片或 Clone。nil 切片合法可用。
3. map 取缺键得零值（bug 隐身），nil map 写入 panic，迭代无序，
   并发读写炸进程（fatal error）：三个坑都背下来。
4. len/索引按字节，字符处理转 `[]rune`；字符串不可变，strings 包
   是日常工具箱。
5. defer 三语义：LIFO、参数立即求值、能改命名返回值。资源获取后
   紧跟 defer。
6. 接收者选型：要改/大/含锁用指针，一致性优先；方法集决定接口满足。
7. 一切赋值皆拷贝；切片/map 的"引用语义"是内部指针的语法糖，共享
   可变在签名上看不出来：这是没有 borrow checker 的代价。

---

## 章末参考

### 声明与初始化速查

| 写法 | 适用 | 备注 |
|---|---|---|
| `x := v` | 函数内 | 最常用；不能包级 |
| `var x T` | 包级/零值 | 零值直接可用 |
| `var x T = v` | 类型推断不出的场景 | 显式类型 |
| `new(T)` | 要指针 | `&T{}` 等价且更常用；1.26 起 new 可带值 |
| `make(T, args)` | slice/map/chan | 返回初始化（非零值）的 T |

### 运算符与比较的坑

- 结构体可比较 ⇔ 所有字段可比较；含 slice/map 字段的 struct 不可
  `==`（编译错），用 `slices.Equal`/`maps.Equal`。
- `slices.Equal(a, b)` ≈ Rust `a == b`（对 `&[T]`）。
- 整数除法向零截断（Rust 同），`%` 结果符号跟被除数。
- 自增 `i++` 是语句不是表达式，没有 `++i`，表达式里不能用。

### number 类型与转换

- `int` 平台相关（64 位机 = int64）；显式位宽用 `int64`/`uint32`。
- 隐式转换**不存在**：`int32` 赋给 `int64` 要显式 `int64(x)`
  （Rust 同款 `as`，但 Go 连 `as` 都要写全）。
- 浮点比较用 `math.Abs(a-b) < eps`；金额用整数分或
  `big.Rat`（别用 float64）。
- 常量是无类型的：`const Big = 1 << 62` 赋给 int64 不溢出，
  先算后定型的。

### 标签与 goto（知道即可）

`break`/`continue`/`goto` 可带标签跳出多层循环（Rust 的
`'outer: loop` 对应物）：

```go
outer:
    for i := range n {
        for j := range m {
            if bad { break outer }
        }
    }
```

### 与 Rust 的关键差异总表

| 主题 | Rust | Go |
|---|---|---|
| 声明 | `let x: T`（不可变默认） | `x := v`（可变默认） |
| 可变性 | `mut` 显式 | 全可变，无标记 |
| 所有权 | move/borrow | 拷贝（大 struct 记得传指针） |
| 枚举 | sum type + 数据 | int 常量 + iota（无类型安全） |
| 错误 | `Result<T, E>` + `?` | `(T, error)` + if err != nil |
| Option | `Option<T>` | 零值 + comma-ok（§5） |
| 泛型 | 全功能 | 接口约束（P8 问题 1） |
| 字符串 | `String` 可变 / `&str` 只读 | 统一 string 只读，改靠重建 |
| 迭代器 | 惰性链主流 | 显式 for 主流（iter 包是补充） |
