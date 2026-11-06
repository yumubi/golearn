# 01 Go 的世界观与差异总表

> 详细程度：标准 · 约 115 分钟
> 材料：TB00、TB01、TB02、TBX01、TB19、TB20、TB21、TBX08 · 旧长版：`notes/p1-toolchain.md`、`notes/p2-lang-core.md`
> 前置：任意一门主流语言

## 先看一个让人皱眉的现象

下面这段代码，写过 C 或 Java 的人都会觉得毫无问题，但它就是编译不过。先别往下翻，猜一下报错写的是什么。

```go fail
package main

import "fmt"

func main() {
	var a int16 = 5
	var b int = 8
	fmt.Println(a + b)
}
```

```text
invalid operation: a + b (mismatched types int16 and int)
```

在 C 里，`a + b` 会把 `int16` 悄悄提升成 `int` 再相加，程序员不用表态；Go 却让你必须先把两个数变成同一类型。第一次碰到的人多半会骂"这语言怎么这么轴"。但它不是没能力做自动转换，而是故意不做。这条线贯穿了 Go 的整个设计，源头是五条设计哲学：**简单、显式、组合、并发、面向工程**。〔TB02 p2〕

这五条是后面每一章的判断尺子。以后你遇到任何"为什么 Go 不提供 X"的问题，先回到这里问一句：X 是不是违反了其中一条。绝大多数时候答案是"是"。

## 和你已知语言的差异

先把 Go 放在你熟悉的语言旁边，看一张总表。表里每一行都会在本节或后面的章节展开，现在只需要扫一遍，感受差异的密度。

| 维度 | Java / Kotlin | Rust | TS / JS | Go |
|---|---|---|---|---|
| 类型检查 | 静态强类型 | 静态强类型 | 静态（擦除），运行时动态 | 静态强类型，变量类型不可变〔TBX01 p2-4〕 |
| 复用手段 | 继承、默认方法 | trait、泛型 | 继承、mixin | 嵌入加接口，无继承 |
| 错误 | 异常（checked 与 unchecked） | `Result` 与 `?` | `throw` 与 `try` | 返回 `error` 值，panic 只留给"程序即将崩溃"〔TB02 p14〕 |
| 并发 | 线程、虚拟线程 | 线程、async 运行时 | 事件循环 | goroutine 加 channel，运行时调度 |
| 内存 | 分代 GC | 所有权，无 GC | GC | 并发 GC，无分代 |
| 包管理 | Maven、Gradle | Cargo，取最新兼容版本 | npm，取最新匹配版本 | module，最小版本选择〔TB06 p12-14〕 |
| 泛型 | 擦除 | 单态化 | 擦除 | 1.18 起支持〔TBX01 p11〕 |
| 工具链 | 多个工具拼接 | `cargo` 一体 | tsc、eslint、prettier、jest 拼接 | 一个 `go` 命令覆盖构建、测试、格式、剖析〔TB02 p10〕 |
| 可见性 | `public`/`private` | `pub` | `export` | 标识符首字母大写即导出〔TB04 p7〕 |
| 默认参数、重载 | Java 有重载无默认参数，Kotlin 两者都有 | 两者都没有（泛型和宏另说） | 默认参数有，重载只有签名声明 | 两者都没有，默认参数是故意不做的〔TB02 p9〕 |

表里最扎眼的是最后一行：默认参数和函数重载，Go 一个都不给。这不是"还没做"，是作者在 API 设计上划的一条线，第五节会讲为什么。

## 一、五条哲学从哪来

Go 不是凭空"想简单一点"，它是被 Google 内部的 C++ 逼出来的。2007 年，Rob Pike 等三人在一次 C++ 编译要等近一个小时的间隙里，聊出了这门新语言的雏形：C++ 太复杂、编译太慢、并发支持又弱，他们想要一门写起来快、构建快、能天然利用多核的语言。〔TB01 p2-3〕第二天三人在会议室又细聊了一轮，会后格瑞史莫发出了一封题为 "prog lang discussion" 的邮件，对这门语言的功能特性做了第一轮归纳，成了它的第一版设计稿。〔TB01 p3〕

学语法之前先谈哲学，不是作者的铺垫癖好。原课在开讲前先给初学者泼了盆冷水，因为这将决定学习结果是"从入门到继续"还是"从入门到放弃"：很多人最初兴致勃勃，学着学着就发现很多"别扭"的地方，按叙述顺序表现为五类：想要的语言特性缺失；语法风格冷僻，与主流语言差异较大；语言的不同版本间无法兼容；语法特性过多导致学习曲线陡峭；工具链支持较差。〔TB02 p1-2〕这些问题的病根，原课归结为设计哲学。所谓编程语言的设计哲学，就是决定这门语言演化进程的高级原则和依据。作者打的比方是：设计哲学之于编程语言，就好比一个人的价值观之于这个人的行为；你不认同一个人的价值观，就很难与之持续交往下去，即所谓道不同不相为谋。同样，不认同一门语言的设计哲学，大概率会在后续学习中遇到上面这些问题，甚至失去继续学习的精神动力。〔TB02 p2〕等学完这一讲，你就能更深刻地认识到自己学习 Go 的原因了。〔TB02 p2〕

原课把 Go 的设计哲学总结为五点：简单、显式、组合、并发、面向工程。〔TB02 p2〕先看第一条。知名 Go 开发者戴维·切尼（Dave Cheney）说过："大多数编程语言创建伊始都致力于成为一门简单的语言，但最终都只是满足于做一个强大的编程语言"。〔TB02 p2〕而 Go 是个例外：设计者在语言设计之初就拒绝了走语言特性融合的道路，选择做减法，因此不会像 C++、Java 那样把其他语言的新特性兼蓄并收。〔TB02 p2〕做减法是有代价的，代价就是你在 Go 里看不到传统的面向对象的类、构造函数与继承，看不到结构化的异常处理，也看不到本属于函数编程范式的语法元素。〔TB02 p2〕

还有一个容易误读的地方：Go 并没有它看起来那么简单，它的自身实现起来并不容易，只是这些复杂性被设计者们"隐藏"了，语法层面才呈现出极简状态。〔TB02 p2〕"特性少"也不是偷懒。Tony Bai 在答疑里说，保持一门小语言，让语言更容易学习与理解，同时每个特性都经过精心打磨与实现，不能再少了；他还提到 Rob Pike 在近期一期 talk 里自评"Go 语言中变量声明的方式有些多了"，如果重新来过，会把变量声明方式再减少一种。特性少是深思熟虑的取舍，不是没做过特性。〔TB02 p13〕

复杂性被隐藏之后，语法层面呈现的状态，就是"简单"的证据。原课列了八项：〔TB02 p2-3〕

- 仅有 25 个关键字，主流编程语言最少；
- 内置垃圾收集，降低内存管理的心智负担；
- 首字母大小写决定可见性，无需额外关键字修饰；
- 变量初始为类型零值，避免以随机值作为初值；
- 内置数组边界检查，极大减少越界访问带来的安全隐患；
- 内置并发支持，以 go 关键字实现，简化并发程序设计；
- 内置接口类型，为组合的设计哲学奠定基础；
- 原生提供完善的工具链，开箱即用。

这种简单不是无中生有，而是站在巨人肩膀上：去除或优化以往语言中已被开发者证明体验不好或难以驾驭的语法元素和机制，再提出自己的创新设计。首字母大小写决定可见性、变量初始为零值、内置以 go 关键字实现的并发支持，都被原课点名为创新而非删减。〔TB02 p3〕其中两条值得单独看一眼，因为它们各自避免了一类老问题。可见性：标识符首字符是 Unicode 大写字母，且声明在包块、或者它本身就是字段名或方法名，即导出，其他一律不导出；省掉 public/private 这类修饰符之后，"谁能用"在定义处一眼可见。〔TB02 p2-3〕〔实测：/Users/akka/Toolchain/go/current/doc/go_spec.html:2294-2309〕零值：变量分配存储后若没有显式初始化，bool 为 false、数值为 0、字符串为 ""、指针/函数/接口/切片/通道/map 为 nil，并且逐字段递归初始化；变量永远从一个可预期的值开始，而不是上一位使用者留在内存里的随机值（`go doc builtin` 起步，零值规则见规范 "The zero value" 节）。〔TB02 p3〕〔实测：/Users/akka/Toolchain/go/current/doc/go_spec.html:8127-8148〕

收益链也是一环扣一环：简单意味着用更少的代码实现相同功能；更少代码意味着更好的可读性；可读性好通常意味着更好的可维护性与可靠性；在软件工程化的今天，这些意味着对生产效率的极大促进。原课的结论是：简单的设计哲学是 Go 生产力的源泉。〔TB02 p3〕"25 个关键字"这个数字也值得钉在现行规范上：当前工具链自带的规范保留字表逐项核对仍是 25 个，无增减（`grep -c "^keyword" $GOROOT/doc/go_spec.html` 不适用时可直接数 Keywords 节，`go version` 为 go1.26.4）。〔实测：/Users/akka/Toolchain/go/current/doc/go_spec.html:255-266〕

所以这五条哲学不是口号，是对一组真实痛点的回答。把每条落在语言里的证据列出来：

| 哲学 | 含义 | 语言里的证据 |
|---|---|---|
| 简单 | 做减法，少而足够，保持生产力 | 25 个关键字；没有类、构造函数、继承、结构化异常〔TB02 p2-3〕 |
| 显式 | 没有暗箱操作，开发者要知道每个操作 | `int16` 与 `int` 不能直接相加；错误经返回值上抛〔TB02 p3-5〕 |
| 组合 | 降低耦合，靠组合而非层级扩展 | 类型之间没有父子关系；接口隐式满足；包独立〔TB02 p5-8〕 |
| 并发 | 面向多核，并发是结构设计方法 | goroutine、channel、select 内建；栈初始 2KB（起点值，细节见下文并发一节）〔TB02 p8〕 |
| 面向工程 | 把构建慢、依赖乱、代码难读这类问题纳入语言设计 | 未使用的导入无法编译；禁止包循环依赖；故意没有默认函数参数；gofmt〔TB02 p9-10〕 |

注意"简单"和"显式"的分工：简单管"东西少"，显式管"剩下的东西必须看得见"。两者合起来，就是你开头遇到的 `int16 + int` 报错。

## 循环：做一件事只有一种方法

第一节那张"哲学、含义、证据"表里，"简单"一行的证据还都停在特性清单上：25 个关键字，删掉的类、继承和异常。这一节把"简单"往控制结构上再落一步，落点是循环。绝大多数主流语言，包括 C、C++、Java 和 Rust，甚至连动态语言 Python，都提供了不止一种的循环语句；Go 却只有一种，就是 for 语句，原课称它为循环结构里的"独苗"。〔TB19 p1-2〕原课的讲题《控制结构：Go的for循环，仅此一种》把结论直接写进了标题。把"只保留一种循环"算进"简单"哲学，定性出处是原课小结：这是践行"做一件事仅有一种方法"理念的作法，恰是 Go 语言崇尚"简单"的设计哲学的具体体现，本节末尾会回来收这笔账。〔TB19 p23〕原课的讲课顺序也有信息量：分支结构还剩 switch-case 没讲，但原课先跳过它，直接挑战控制结构里最复杂的循环；理由是很多时候用 if 可以替代 switch-case，往后放没关系，而让你更早动手写带循环结构的 Go 代码更要紧。〔TB19 p1〕

认识 for 语句，原课从 C 落笔：C 语言是很多现代编程语言的"祖先"，它的 for 经典使用形式被后继语言延承了下来，Go 也不例外。先看原课的 C 版本，求 0 到 9 的累加和（片段）：

```c
int i;
int sum = 0;
for (i = 0; i < 10; i++) {
	sum += i;
}
printf("%d\n", sum);
```

Go 里的等价形式（原文片段补上 package main 和 main 函数后即可独立运行）：

```go
package main

func main() {
	var sum int
	for i := 0; i < 10; i++ {
		sum += i
	}
	println(sum)
}
```

两版 for 头部都是三段：初始化、条件、后置更新，用分号分隔；循环体都是把 i 累加进 sum，i 取 0 到 9。真正的差别在循环变量 i 的声明位置：C 把 i 声明在 for 语句之外，for 头部第一步是对已有变量赋值；Go 用短声明 i := 0 直接把声明写进 for 头部。〔TB19 p2〕两版运行结果都是 45，即 0 加到 9；这是从代码推出的教学推导，原文没有展示运行输出。

上面这段 for 是 Go 循环的第一种形式：经典形式。原课本配有流程图讲解各组成部分与执行顺序，文本版未保留该图，下面按紧随其后的文字逐个拆解。经典 for 语句有四个组成部分：〔TB19 p3-4〕

- **①循环前置语句**：执行于循环体之前，在整个 for 语句中仅被执行一次。通常在这里声明循环体或循环控制条件要用的自用变量，也称循环变量或迭代变量，例子里就是整型变量 i。与 if 语句的自用变量一样，循环变量采用短变量声明形式，作用域仅限于 for 语句隐式代码块范围内（控制语句隐式代码块的完整规则见 03 章第七节）。〔TB19 p3〕
- **②循环判断表达式**：决定循环是否继续进行。与 if 一致，必须是布尔表达式；有多个判断条件时可用逻辑操作符连接。求值为 true 进入循环体，为 false 时循环直接结束，循环体③与组成部分④都不会被执行。〔TB19 p3〕
- **③循环体**：条件为 true 时执行一次，这样的一次执行称为一次迭代（Iteration）。示例中的动作是把本轮迭代中变量 i 的值累加到 sum。〔TB19 p3-4〕
- **④循环后置语句**：在每次循环体迭代之后执行，通常用于更新①中声明的循环变量，示例中是对 i 加 1。〔TB19 p4〕

四个组成部分里，除了循环体③，其余三个都是可选的，原课把三种省法各给了一个例子。省略后置语句④，把对循环变量的更新挪进循环体：

```go
for i := 0; i < 10; {
	i++
}
```

省略前置语句①，不再短声明新变量，直接使用外层已声明的变量充当循环变量：

```go
i := 0
for ; i < 10; i++ {
	println(i)
}
```

前置与后置都省略：

```go
i := 0
for ; i < 10; {
	println(i)
	i++
}
```

注意上面三个例子虽然省了前置或后置，经典形式中的分号依然被保留着：原课特别提醒，这是 Go 语法的要求。〔TB19 p4-5〕

唯一的例外是：当前置与后置语句都省略、仅保留循环判断条件表达式时，分号可以一并省去。上面的例子就写成：

```go
i := 0
for i < 10 {
	println(i)
	i++
}
```

这是日常 Go 编码中经常使用的 for 第二种形式：除了循环体，只保留循环判断条件表达式。沿这条线再推一步，当条件表达式的求值结果始终为 true 时，条件本身也可以省略，得到 for { }，这就是通常所说的"无限循环"，形式上等价于 for true { } 与 for ; ; { }；原课建议日常使用最简形式 for { }。无限循环是不是就被困在循环体里出不来了？原课在这里卖了个关子，答案就是下面要讲的 break 语句。〔TB19 p5-6〕

写到这里你大概已经感到经典形式的啰嗦：遍历切片要先短声明下标（var sl = []int{1, 2, 3, 4, 5}）、每轮手动 i++、再用 sl[i] 取元素。针对切片这类复合类型和 Go 原生的 string 类型，原课给出了更方便的"语法糖"：for range。同样是打印切片的每个元素（sl 即上句声明的切片，片段）：

```go
for i, v := range sl {
	fmt.Printf("sl[%d] = %d\n", i, v)
}
```

形式上除了循环体，其余组成部分都"不见"了：它们被融合进 for range 的语义。i 和 v 对应经典形式前置语句中的循环变量；隐含的循环条件是"是否已遍历完 sl 的所有元素"，等价于 i < len(sl)；每次迭代后，for range 取出下一个元素的下标和值分别赋给 i、v，这与经典形式后置语句执行的逻辑相同。〔TB19 p6-7〕

for range 用在 string 上时，有一处语义必须专门讲。原课的例子（原文片段补上 package 与 import 后可独立运行）：

```go
package main

import "fmt"

func main() {
	var s = "中国人"
	for i, v := range s {
		fmt.Printf("%d %s 0x%x\n", i, string(v), v)
	}
}
```

```text
0 中 0x4e2d
3 国 0x56fd
6 人 0x4eba
```

每次循环得到的 v 是一个 Unicode 字符码点，也就是 rune 类型值，而不是一个字节；返回的第一个值 i 是该码点的 UTF-8 编码的第一个字节在字符串内存序列中的位置。"中国人"三个汉字各占 3 字节，所以下标是 0、3、6，而不是 0、1、2：下标不按 1 递增，而是按上一个码点的 UTF-8 编码长度跳跃。原课再次提醒：使用 for 经典形式与使用 for range 形式对 string 进行循环操作的语义是不同的，细节在第 13 讲；"经典形式按下标逐字节、range 按 rune 且下标为字节偏移"这组对照，在 03 章"字符串、字节与 rune"一节有完整展开。〔TB19 p8-9〕

最后是离场规则：循环执行到一半怎么提前离开？原课把场景分成两类。一类是中断当前迭代、忽略循环体剩余代码，回到条件判断尝试下一次迭代，用 continue，无论带不带 label，continue 的本质都是继续循环语句的执行；另一类是不仅中断当前迭代，还要彻底跳出循环、终结整个循环语句的执行，continue 不再适用，Go 用 break 解决这个问题。〔TB19 p11-13〕

最简单的用法是"找到就走"。在切片里找第一个偶数，找到 6 之后的 3、8、12 就没有必要再检查了（原文片段，补上 package main 后可独立运行）：

```go
func main() {
	var sl = []int{5, 19, 6, 3, 8, 12}
	var firstEven int = -1

	// 找出整型切片sl中的第一个偶数
	for i := 0; i < len(sl); i++ {
		if sl[i]%2 == 0 {
			firstEven = sl[i]
			break
		}
	}

	println(firstEven) // 6
}
```

break 在这里终结整个 for 语句，println 打出 6。〔TB19 p14〕

嵌套循环里这条规则要升级：不带 label 的 break 仅能跳出其所在的最内层循环，要跳出外层循环必须给 break 加上 label。原课的例子是在二维切片中找 gold = 38（原文片段补上 package 与 import 后可独立运行）：

```go
package main

import "fmt"

var gold = 38

func main() {
	var sl = [][]int{
		{1, 34, 26, 35, 78},
		{3, 45, 13, 24, 99},
		{101, 13, 38, 7, 127},
		{54, 27, 40, 83, 81},
	}

outerloop:
	for i := 0; i < len(sl); i++ {
		for j := 0; j < len(sl[i]); j++ {
			if sl[i][j] == gold {
				fmt.Printf("found gold at [%d, %d]\n", i, j)
				break outerloop
			}
		}
	}
}
```

label outerloop 标记的是外层 for：内层发现 38 后，break outerloop 直接终结外层循环，从多层嵌套中一次跳出。原文给这条规则的动机是算力：整个二维切片中至多有一个值为 38 的元素，找到之后剩余迭代全是浪费，直接跳出来避免不必要的算力资源浪费。原文没有展示这段程序的运行输出；按数据推导，38 位于 sl[2][2]，程序会打出 found gold at [2, 2]，这是教学推导。〔TB19 p14-15〕

for range 的语法糖还埋着一个 2021 年课程重点讲解的坑：循环变量的重用。看原课的例子（原文片段未含 package 与 import，补齐后可独立运行）：

```go
package main

import (
	"fmt"
	"time"
)

func main() {
	var m = []int{1, 2, 3, 4, 5}

	for i, v := range m {
		go func() {
			time.Sleep(time.Second * 3)
			fmt.Println(i, v)
		}()
	}

	time.Sleep(time.Second * 10)
}
```

这段程序遍历切片，每轮迭代启动一个 goroutine，睡 3 秒后打印本轮的下标与元素值，主 goroutine 最后睡 10 秒等它们跑完。初学者的预期输出是五行 0 1 到 4 5，各打各的；实际运行，五行全部是：

```text
4 5
4 5
4 5
4 5
4 5
```

原课的解释是：我们最初的"预期"本身就是错的。初学者容易被 for range 的短声明形式"迷惑"，以为每次迭代都会重新声明两个新的变量 i 和 v；事实上，这些循环变量在 for range 语句中仅会被声明一次，且在每次迭代中都会被重用。想直观看到"重用"，可以按第 11 讲隐式代码块的规则，把这条 for range 做一个等价转换（片段，与上面程序的循环部分等价，其余不变）：

```go
{
	i, v := 0, 0
	for i, v = range m {
		go func() {
			time.Sleep(time.Second * 3)
			fmt.Println(i, v)
		}()
	}
}
```

短声明被提到 for 语句之外，循环里只剩赋值：i 和 v 整个循环只有一份。goroutine 执行的闭包引用了外层函数的变量 i、v，于是 i、v 在主 goroutine 和各新启动的 goroutine 之间实现了共享，且整个循环过程中值是重用的、仅有一份；for range 循环结束后 i = 4、v = 5，各 goroutine 醒来时打印的自然都是最终值。修正办法是给闭包加参数，创建 goroutine 时把 i、v 的当时值绑定为参数（原文片段补齐后可独立运行）：

```go
package main

import (
	"fmt"
	"time"
)

func main() {
	var m = []int{1, 2, 3, 4, 5}

	for i, v := range m {
		go func(i, v int) {
			time.Sleep(time.Second * 3)
			fmt.Println(i, v)
		}(i, v)
	}

	time.Sleep(time.Second * 10)
}
```

输出恢复为五行 0 1 到 4 5；注意行序可能与书中不同，这由 goroutine 的调度决定。〔TB19 p15-18〕

这个坑有一个版本边界要单独交代，它是材料整理标注的版本事实，不是 2021 年原文的内容：自 Go 1.22 起，for 用 := 声明的循环变量改为每次迭代创建新实例（前提是该文件采用 Go 1.22 或更高语言版本），第一个程序在 1.22 及以后会直接输出预期值，不再需要参数绑定修正；等价转换伪代码描述的是 Go 1.21 及以前的语义。1.22 语义的完整说明落在 03 章第七节末尾与 06 章 goroutine 一节；新代码里你大概率踩不到这个坑，但读老代码要认得它。

最后把循环挂回本节的五条哲学。第一节哲学表里，"简单"一行的证据（25 个关键字、删掉的类与继承、没有结构化异常）都还停在特性清单上；现在多了一条你能亲手写出来的：循环只有一种写法，写之前不用挑形式，读任何 Go 代码里的循环，认得这一种就够了。原课讲完全部 for 语法后还留了一句现成的注脚：到这里，关于 Go 语言中 for 语句的相关语法，我们已经全部讲完了。〔TB19 p15〕一门语言的循环语法一节课就讲得完，这正是"做减法"换来的东西。

## 二、显式：回到开头的那个报错

C 编译器看到 `a + b` 会自动做整数提升；Go 对不同数值类型的变量不做这种隐式转换。无类型常量则可以在可表示的前提下按上下文取得类型。〔TB02 p4-5〕

把场景放回原课的例子。C 里下面这段代码可以正常编译并输出正确结果：〔TB02 p3-4〕

```c
short int a = 5;
int b = 8;
long c = 0;
c = a + b;          /* 编译器自动把 a、b 先转换为 long，再相加 */
printf("%ld\n", c); /* 输出 13 */
```

变量 a、b、c 类型各不相同，但 C 编译器在编译 `c = a + b` 这行时，会自动把短整型 a 和整型 b 先转换为 long 类型再相加，结果存入 long 变量 c，程序员全程不用表态，这就是隐式自动类型提升。换成 Go（本章开头那段程序）就不行了：编译得到 `invalid operation: a + b (mismatched types int16 and int)`。Go 不允许不同类型的整型变量混合计算，也不会做隐式的自动转换。〔TB02 p4〕

修复办法是显式转型，把参与计算的变量统一到同一类型。原课的写法：〔TB02 p4〕

```go
var a int16 = 5
var b int = 8
var c int64
c = int64(a) + int64(b)
fmt.Println(c) // 13
```

多写一个转换，读者就能看到选择的结果类型。Rust 同样要求显式数值转换；Go 使用 `T(x)`。

为什么 Go 要卡这一道？原课给的理由是：Go 希望开发人员明确知道自己在做什么，这与 C 语言"信任程序员"的原则完全不同，因此必须以显式方式通过转型统一参与计算的各个变量的类型。〔TB02 p5〕转型本身的语义要分两条记，它们在现行规范里是两套规则，混在一起会得出错误结论：

- 变量之间的数值转换不做范围校验。整型间转换先把值按符号扩展（有符号类型）或零扩展（无符号类型）放到隐式无限精度上，再截断到目标类型的宽度，规范原话是"转换总是产生有效值，没有溢出指示"，自带的例子是 `uint32(int8(uint16(0x10F0))) == 0xFFFFFFF0`。〔1.26 修正〕〔实测：/Users/akka/Toolchain/go/current/doc/go_spec.html:5547-5560〕所以 `int16(40000)` 不会报错，而是静默截断成 -25536。这也解释了修复写法的取舍：`int64(a) + int64(b)` 或 `int(a) + b` 都是把小类型往大转，不会丢值；而 `a + int16(b)` 只有在 b 的值恰好落在 int16 范围内才安全，超范围时结果会悄悄变成另一个数。
- 常量转换是另一条规则：常量必须能被目标类型准确表示，否则编译错误。规范反例：`uint(-1)` 和 `int(3.14)` 都无法通过编译。〔1.26 修正〕〔实测：/Users/akka/Toolchain/go/current/doc/go_spec.html:5803-5808〕

一句话区分：编译器报错的是"不同类型变量的混合计算"，静默截断的是"超范围的变量转换"；常量超出目标类型的表示范围，则在编译期直接报错。

与显式一脉相承的是 Go 的静态强类型：一个变量声明成什么类型，就不能再改。`var a int = 1` 之后再 `a = "hello"` 编译不过，因为类型在编译期就定死了。〔TBX01 p2-4〕Python 的变量可以先存整数再存字符串，Go 不提供这个自由度。

显式哲学的第二落点是错误处理，它直接决定了 Go 错误处理方案的形态。这个方案有三个要素：错误在函数/方法中通过 return 语句显式地返回；方案基于值比较，错误就是一个普通的返回值，不是隐藏的控制流；调用方通常不能忽略对返回的错误的处理。〔TB02 p5〕错误摆在你眼前，处理不处理、在哪处理都是明面上的决定，没有 throw 那种隐式向上传播的控制流。这种有悖于主流语言潮流的机制一度被开发者诟病，社区提出过多个新错误处理方案，但或多或少都包含隐式成分，被 Go 开发团队一一否决，否决与显式的设计哲学不无关系。〔TB02 p5〕错误处理的具体写法，见 05 节。

### switch：把穿透做成显式动作

显式哲学在控制流上的另一处落地，是 switch 的穿透语义。写惯 C 的人有一段肌肉记忆：匹配到某个 case，执行完它的分支代码，如果没写 `break`，就顺势滑进下一个 case 接着跑。原课把这种隐式语义点名为 C 的一个缺陷：它不符合日常算法的常规逻辑，所以 C 程序员的补救办法是给每个 case 都补上 `break`，原课前面用 C 写的 check_work_day 示例就是这样做的。〔TB20 p9〕Go 把这条默认语义整个取消：每个 case 对应的分支代码执行完后，switch 语句就结束了。多数时候这正是想要的行为，"一个 case 一件事"，少写一个 `break` 就少一类事故。确实需要延续到下一个 case 时，Go 提供显式关键字 `fallthrough`：要不要穿透，写出来让读代码的人看见，原课把这一改动归到"显式"设计哲学名下。〔TB20 p9〕

`fallthrough` 到底做了什么，直接看原课的完整程序：

```go
package main

func case1() int {
	println("eval case1 expr")
	return 1
}

func case2() int {
	println("eval case2 expr")
	return 2
}

func switchexpr() int {
	println("eval switch expr")
	return 1
}

func main() {
	switch switchexpr() {
	case case1():
		println("exec case1")
		fallthrough
	case case2():
		println("exec case2")
		fallthrough
	default:
		println("exec default")
	}
}
```

```text
eval switch expr
eval case1 expr
exec case1
exec case2
exec default
```

三个函数各打印一行求值日志再返回一个值：switchexpr 作 switch 表达式，case1、case2 作 case 表达式。逐行读输出：先求值 switch 表达式，得到 eval switch expr；再按书写顺序求值 case1 的表达式并匹配成功，执行 case1 分支，得到 exec case1。关键在下一行：case1 写了 `fallthrough`，执行流并不离开 switch 语句，而是直接落到下一个 case 的分支代码，打出 exec case2。输出里没有 eval case2 expr，因为 `fallthrough` 不对下一个 case 的表达式求值，直接执行它的分支代码。case2 分支里同样写了 `fallthrough`，于是最后一个分支 default 也被执行，打出 exec default：`fallthrough` 可以连续穿透，一直穿到 default。〔TB20 p9-10〕

还有一条边界：如果某个 case 已经是 switch 中最后一个 case，且它后面没有 default 分支，这个 case 里就不能再写 `fallthrough`，否则编译器报错。〔TB20 p9-10〕

Go 的 switch 还支持一种专属写法：type switch，用类型信息当分支条件，等价于一组类型断言的紧凑写法，代价是只能用于接口变量（断言本身的规则在 04 节讲）：

```go
package main

func main() {
	var x interface{} = 13
	switch x.(type) {
	case nil:
		println("x is nil")
	case int:
		println("the type of x is int")
	case string:
		println("the type of x is string")
	case bool:
		println("the type of x is string")
	default:
		println("don't support the type")
	}
}
```

```text
the type of x is int
```

switch 后面的 `x.(type)` 是 switch 语句专有的表达式形式，离开 switch 语句就用不了；x 必须是接口类型的变量，表达式的求值结果是这个接口变量当前的动态类型。动态类型可以先借这行声明建立直觉：x 的静态类型是 interface{}，把整型值 13 赋给它之后，x 的动态类型就是 int。接口值在内部怎么同时装下静态类型与动态类型，04 节有完整展开，这里先记住分支怎么写。〔TB20 p10-11〕case 后面接的不再是普通表达式，而是一个个具体的类型：Go 拿 x 的动态类型与各 case 的类型匹配，命中哪支就执行哪支，之后的逻辑与普通 switch 一致。Go 中所有类型都实现了 interface{}，所以当 switch 变量是 interface{} 类型时，case 后面可以写任意类型。〔TB20 p11-12〕

原文这个示例里埋了一处笔误，正好当练习：case bool 分支的 println 文案写成了 the type of x is string，应为 bool。因为 bool 没被匹配，这行文案不执行，运行结果不受影响。〔TB20 p11〕

只拿类型还不够，多数场景还想要值。把 `x.(type)` 换成 `v := x.(type)`：

```go
package main

func main() {
	var x interface{} = 13
	switch v := x.(type) {
	case nil:
		println("v is nil")
	case int:
		println("the type of v is int, v =", v)
	case string:
		println("the type of v is string, v =", v)
	case bool:
		println("the type of v is bool, v =", v)
	default:
		println("don't support the type")
	}
}
```

```text
the type of v is int, v = 13
```

千万不要认为变量 v 存储的是类型信息：v 存的是 x 的动态类型对应的值信息，这样 case 执行路径里就可以直接使用 v。x 为 13 时命中的是 case int，v 的类型是 int、值是 13。顺带一个形式辨析：`v := x.(type)` 看似带初始化语句的 switch 写法（`switch initStmt; expr` 那种），但它后面没有分号，这种形式叫 type guard。〔TB20 p12〕

type switch 对 case 还有一层静态约束。switch 变量是 interface{} 时 case 随便写；换成某个特定接口类型 I，case 后面就只能写实现了 I 的类型：

```go fail
package main

type I interface {
	M()
}

type T struct{}

func (T) M() {}

func main() {
	var t T
	var i I = t
	switch i.(type) {
	case T:
		println("it is type T")
	case int:
		println("it is type int")
	case string:
		println("it is type string")
	}
}
```

```text
19:2: impossible type switch case: i (type I) cannot have dynamic type int
21:2: impossible type switch case: i (type I) cannot have dynamic type string
```

这段代码编译不过。例子中只有自定义类型 T 实现了接口 I（它提供了 M 方法），Go 原生类型 int 与 string 都没有实现 I。编译器的两条报错都叫 impossible type switch case，直译是"不可能的 type switch 分支"：静态类型为 I 的变量 i，其动态类型必须实现 I，所以 case int 和 case string 从类型系统上就站不住，这层约束把 type switch 的合法分支收紧到接口的能力范围内。（报错行号对应原课程序的行位置，代码贴进自己的文件时行号会随代码位置变化。）〔TB20 p13〕

最后一个坑来自 `break` 和 switch 的组合。本章"循环"一节给过规则：嵌套循环里不带 label 的 `break` 只跳出最内层循环，跳外层要带 label。把找第一个偶数的例子从 if 换成 switch，这条规则就换了个埋伏位置：

```go
package main

func main() {
	var sl = []int{5, 19, 6, 3, 8, 12}
	var firstEven int = -1

	// 找出整型切片sl中的第一个偶数
	for i := 0; i < len(sl); i++ {
		switch sl[i] % 2 {
		case 0:
			firstEven = sl[i]
			break
		case 1:
			// do nothing
		}
	}

	println(firstEven)
}
```

切片里第一个偶数是 6，程序却输出最后一个偶数 12。原因在 Go 语言规范的定义：不带 label 的 `break` 语句中断执行并跳出的，是同一函数内 break 所在的最内层的 for、switch 或 select。上面代码里的 `break` 坐在 switch 里，它只跳出 switch，外层 for 根本没停。于是循环继续跑：下标走到 2 时 firstEven 先被赋成 6，随后被 8 覆盖，最后被 12 覆盖，循环结束后 println 打出 12。修正用带 label 的 `break`：

```go
package main

func main() {
	var sl = []int{5, 19, 6, 3, 8, 12}
	var firstEven int = -1

loop:
	for i := 0; i < len(sl); i++ {
		switch sl[i] % 2 {
		case 0:
			firstEven = sl[i]
			break loop
		case 1:
			// do nothing
		}
	}

	println(firstEven) // 6
}
```

label loop 写在 for 语句前面，指代这个 for 循环的执行。代码执行到 `break loop` 时，程序停止 loop 所指代的 for 循环，firstEven 停在第一个赋进来的 6，println 输出 6。〔TB20 p14-15〕

select 语句和 switch 一样会阻拦不带 label 的 `break` 跳出，具体例子留到 06 节讲并发时再展开。〔TB20 p15〕

## 三、组合：没有继承，靠什么扩展

Java/Kotlin 程序员建模的第一反应是定义基类，然后继承。Go 里没有 `extends`，找不到经典的面向对象语法元素、类型体系和继承机制，类型之间没有父子关系，每个类型都是一座孤岛。〔TB02 p5-6〕扩展能力的办法是把这些孤岛"拼"起来：结构体里嵌入别的类型做垂直扩展，函数参数写成接口类型做水平扩展。〔TB02 p6-7〕

在讲怎么"拼"之前，先看 Go 在语法元素设计上为组合奠定的四条正交性，原课逐条列出：〔TB02 p5-6〕

- Go 无类型层次体系，各类型之间相互独立，没有子类型的概念；
- 每个类型都可以有自己的方法集合，类型定义与方法实现是正交独立的；
- 实现某个接口时，无需像 Java 那样采用特定关键字修饰；
- 包之间是相对独立的，没有子包的概念。

这四条共同把 Go 程序呈现成一幅图景：无论包、接口还是一个个具体的类型定义，都像一座座没有关联的"孤岛"，但每个岛内都很精彩。摆在面前的工作，是在这些孤岛之间以最适当的方式建立关联、形成一个整体，Go 选择的主要方式就是组合。〔TB02 p6〕

垂直方向靠类型嵌入（Type Embedding）：把已经实现的功能嵌入到新类型中，快速满足新类型的功能需求。在结构体里只写类型名、不写字段名，就把那个类型已有的方法"借"进新类型。这种方式类似经典面向对象语言的继承机制，但原理上与继承完全不同，是 Go 设计者精心设计的语法糖。〔TB02 p6〕与继承划清三条边界：被嵌入的类型和新类型两者之间没有任何关系，甚至相互完全不知道对方的存在；没有经典面向对象那种父类、子类关系，也没有向上、向下转型（Type Casting）；通过新类型实例调用方法时，方法的匹配主要取决于方法名字，而不是类型。〔TB02 p6〕这种组合方式叫垂直组合：通过类型嵌入，让新类型复用其他类型已经实现的能力、实现功能的垂直扩展；垂直组合本质是一种"能力继承"，新类型继承了嵌入类型的能力。〔TB02 p6-7〕

原课给的标准库例子是 $GOROOT/src/sync/pool.go 里的 poolLocal：结构体中嵌入类型 Mutex，就使 poolLocal 这个类型具有了互斥同步能力，poolLocal 类型的变量可以直接调用 Mutex 的 Lock 或 Unlock。〔TB02 p6〕这个例子有版本差异：专栏贴的是早期版本，有读者留言指出 go1.16.4 中 poolLocal 的实现已经和正文不一样，作者回复"无伤大雅，文中的目的就是为了说明类型嵌入的组合方式"，并决定不改正文。〔TB02 p13〕现行 go1.26 源码同样如此（`go doc sync.poolLocal` 可自行对照）：poolLocal 嵌入的是 poolLocalInternal（private any 加 shared poolChain）和 pad 字节填充，Mutex 不再被 poolLocal 直接嵌入，只作为独立变量 allPoolsMu 存在；嵌入结构体的教学意图没变，但引用例子要用现行实现。〔实测：/Users/akka/Toolchain/go/current/src/sync/pool.go:67-78、:284〕

用一段最小代码看方法和字段是怎么"提升"进新类型的：

```go
package main

import "fmt"

type Base struct{ ID int }

func (b Base) Describe() string { return fmt.Sprintf("base %d", b.ID) }

type Server struct {
	Base
	Addr string
}

func main() {
	s := Server{Base{7}, ":8080"}
	fmt.Println(s.Describe()) // Describe 来自 Base，按名字提升
	fmt.Println(s.ID)         // 字段 ID 同样提升
}
```

```text
base 7
7
```

`Server` 自己没写 `Describe`，`s.Describe()` 依然成立，直观上就像编译器替你生成了一层转发；规范层面的机制是方法提升，规则很精确：深度指从 T 出发跨越的嵌入字段层数，T 自己声明的字段或方法深度为 0；选择器 `x.f` 取最浅深度处唯一的 `f`，最浅深度上若不唯一，选择器非法。`S` 嵌入 `T` 时，`S` 与 `*S` 的方法集都包含 receiver 为 `T` 的提升方法，`*S` 的方法集还额外包含 receiver 为 `*T` 的提升方法。〔1.26 修正〕〔实测：/Users/akka/Toolchain/go/current/doc/go_spec.html:1127-1144、:3459-3487〕

水平方向靠接口。和垂直组合的能力继承不同，水平组合是一种能力委托（Delegate），通常使用接口类型来实现。〔TB02 p7〕Go 的接口是一个创新设计：它只是方法集合，与实现者之间的关系无需通过显式关键字修饰；它让程序内部各部分之间的耦合降至最低，也是连接程序各部分之间的"纽带"。〔TB02 p7〕

那就剩一个问题：类型没有 `implements` 之类的关键字，编译器依据什么判定它满足接口？答案是方法集合，而不是任何声明关键字。现行规范规定：类型 `T` 实现接口 `I`，当且仅当 `T` 不是接口且 `T` 属于 `I` 的 type set；或者 `T` 本身是接口且 `T` 的 type set 是 `I` 的 type set 的子集。basic interface 的 type set 就是实现了其全部方法的那些类型的集合，接口的 method set 恰由接口声明的方法组成，整个判定过程没有任何"声明了才算数"的参与项。〔1.26 修正〕〔实测：/Users/akka/Toolchain/go/current/doc/go_spec.html:1600-1623、:1288-1296、:2086-2106〕

最直观的一点是：Go 的类型实现接口不需要写任何关键字，方法集合覆盖了就算。看下面这段：

```go
package main

import "fmt"

type Greeter interface {
	Greet() string
}

type Cat struct{ Name string }

func (c Cat) Greet() string { return "meow, I'm " + c.Name }

type Dog struct{ Name string }

func (d Dog) Greet() string { return "woof, I'm " + d.Name }

func greetAll(gs []Greeter) {
	for _, g := range gs {
		fmt.Println(g.Greet())
	}
}

func main() {
	greetAll([]Greeter{Cat{"Tom"}, Dog{"Rex"}})
}
```

```text
meow, I'm Tom
woof, I'm Rex
```

注意 `Cat` 和 `Dog` 从头到尾没有声明自己"实现"了 `Greeter`，它们只是恰好有 `Greet() string` 这个方法；`greetAll` 要的也只是"能 `Greet`"这个能力，不管传进来的是什么。对比 Java：你得先定义 `interface Greeter`，再让 `Cat implements Greeter`，实现方必须知道接口的存在；Go 把"声明实现"这一步整个删掉了。〔TB02 p5〕

这套"只认方法集合"的判定，撑起了标准库里最常见的水平组合模式：通过接受接口类型参数的普通函数进行组合。原课给了两个签名：$GOROOT/src/io/ioutil/ioutil.go 的 `func ReadAll(r io.Reader) ([]byte, error)`，和 $GOROOT/src/io/io.go 的 `func Copy(dst Writer, src Reader) (written int64, err error)`。〔TB02 p7〕参数写成接口类型而不是具体类型，函数就只认能力、不认类型：ReadAll 通过 io.Reader 这个接口，把 io.Reader 的任意实现与 ReadAll 所在的包低耦合地水平组合在一起，达到从任意实现 io.Reader 的数据源读取所有数据的目的；Copy 同理，接受任意 Writer 与任意 Reader。〔TB02 p7〕

这两个签名在现行源码里有一处要更正：io/ioutil 自 Go 1.16 起废弃，现行 src/io/ioutil/ioutil.go 里 ReadAll 的文档标注 `Deprecated: As of Go 1.16, this function simply calls [io.ReadAll]`，函数体也只剩一行 `return io.ReadAll(r)`；ReadAll 本体已迁入 io 包（src/io/io.go，内部以 512 字节容量起步、按指数扩容读取，读完再拼成精确大小的切片返回），io.Copy 的签名未变。教学引用应写 io.ReadAll。〔1.26 修正〕〔实测：/Users/akka/Toolchain/go/current/src/io/ioutil/ioutil.go:26-31、/Users/akka/Toolchain/go/current/src/io/io.go:709-712、:387-389〕

水平组合的模式不止这一种，原课还点名了点缀器、中间件等，声明留到接口类型章节详细展开；此外，Go 内置的并发能力也能拿来组合，通过 goroutine+channel 可以实现类似 Unix Pipe 的能力。〔TB02 p7〕

### 函数是一等公民：从值到方法

"组合"至今拿出的证据都在数据侧：类型嵌入把已有能力搬进新类型，接口参数让函数只认能力不认类型。这条哲学还有行为侧的证据：在 Go 里，一段行为本身也能当成值来对待。本小节从"把一段行为存进变量"出发，先讲清函数的类型从哪来、传参时拷贝什么，再走进标准库里最著名的一个函数类型适配器 http.HandlerFunc，把这条证据链走完。

**从把一段行为存进变量说起。** 原课的例子（原文片段补上 package 与 import 后可独立运行）：

```go
package main

import (
	"fmt"
	"io"
	"os"
)

var (
	myFprintf = func(w io.Writer, format string, a ...interface{}) (int, error) {
		return fmt.Fprintf(w, format, a...)
	}
)

func main() {
	fmt.Printf("%T\n", myFprintf) // func(io.Writer, string, ...interface {}) (int, error)
	myFprintf(os.Stdout, "%s\n", "Hello, Go") // 输出 Hello, Go
}
```

var 块里没有函数名：等号右边是一个新创建的匿名函数，整个被赋给了变量 myFprintf。行为存进变量之后，这个变量就能像函数一样调用：第二行传入 os.Stdout、格式串和实参 "Hello, Go"，屏幕上打出 Hello, Go。第一行用 %T 打印 myFprintf 的类型，与预期的函数类型相符：func(io.Writer, string, ...interface {}) (int, error)。原课该行注释在原文里右缘截断到 ...interface {})，返回值部分按 digest K14 记录的完整类型 func(io.Writer, string, ...interface{}) (int, error) 补全，两者是同一个类型，差别只是类型字符串里 interface{} 显示为带空格的 interface {}。先存、用 %T 验证类型、再按函数类型调用，三步就是"函数可以存储在变量中"的最小演示。〔TB21 p9-10〕

一处现行源码对照：标准库现行签名是 `func Fprintf(w io.Writer, format string, a ...any) (n int, err error)`，any 是 interface{} 的别名（"equivalent to interface{} in all ways"），所以 ...any 与 ...interface{} 是同一个类型，例子在今天照样成立。〔实测：/Users/akka/Toolchain/go/current/src/fmt/print.go:222、/Users/akka/Toolchain/go/current/src/builtin/builtin.go:96-97〕

**把函数声明读成变量声明。** myFprintf 的写法为什么合法？回到普通函数声明。一个 Go 函数声明由五部分组成：func 关键字、函数名、参数列表、返回值列表、函数体。原课做了一次等价变换，把函数声明改写成完全合乎语法的变量声明：函数名就是变量名，func 关键字、参数列表和返回值列表共同构成函数类型。原课的变换图在文本版没有保留，用最小例子复原这个对照（教学复原，非原课代码）：

```go
// 写法一：传统函数声明
func Add(a, b int) int { return a + b }
```

```go
// 写法二：等价的变量声明形式，函数名的位置换成变量名，右边整个是函数字面值
var add = func(a, b int) int { return a + b }
```

原课对这次变换的评语是："这不就是在声明一个类型为函数类型的变量吗"。参数列表与返回值列表的组合有一个专门的名字：函数签名，它是决定两个函数类型是否相同的决定因素，函数类型可以看成由 func 关键字与函数签名组合而成。表述函数类型时通常省略签名里的参数名和返回值变量名，比如 fmt.Fprintf 的类型写作 func(io.Writer, string, ...interface{}) (int, error)。〔TB21 p4〕

签名判定有一个直接推论：func(a int, b string) (results []string, err error) 与 func(c int, d string) (sl []string, err error) 的参数名和返回值名都不同，但省去名字后都是 func(int, string) ([]string, error)，所以它们是同一个函数类型。〔TB21 p4-5〕由此得到原课的结论：每个函数声明所定义的函数，仅仅是对应函数类型的一个实例，就像 var a int = 13 里 a 是 int 类型的一个实例。原课还把两种形式并排放进最简对照：s := T{} 是用复合类型字面值初始化结构体变量，f := func(){} 里同样位置的 func(){} 叫函数字面值，由函数类型与函数体组成，像一个没有函数名的函数声明，所以也叫匿名函数。〔TB21 p5〕

有一条边界要划清：等价变换只为理解函数声明，日常定义函数仍用传统函数声明，原课的原话是"万万不可"改用变量声明形式。〔TB21 p5〕

**存进变量的行为，传参时怎么拷贝。** 行为能存进变量，就逃不开传参时的拷贝规则。Go 的参数传递恒为值传递：把实际参数在内存中的表示逐位拷贝（Bitwise Copy）到形式参数。整型、数组、结构体的内存表示就是自身的数据内容，值传递拷贝的就是它们自身，开销与自身大小成正比。string、切片、map 不同：它们的内存表示是数据内容的"描述符"，值传递只拷贝描述符、不含数据内容本身，开销固定、与数据内容大小无关，这种只拷描述符的过程称为浅拷贝。Go 用统一的值传递换简单的心智模型，代价是大结构体的拷贝开销，需要时用显式传指针优化；还有一条推论要记牢（digest 易错点，03 章"值语义"一节有完整展开）：描述符的副本与原描述符指向同一份底层数据，函数内经描述符修改底层数据，对调用者可见。〔TB21 p6〕

两条例外由编译器介入，触发条件是"形参为接口类型"或"形参为变长参数"，此时简单的值传递不能满足要求：形参为接口类型时，编译器把传递的实参赋值给对应的接口类型形参（作者在留言区把这步补称为"装箱"，由编译器植入的代码完成）；形参为变长参数时，编译器把零个或多个实参按一定形式转换为对应的变长形参，"零个"也在转换范围内，原课 myAppend(sl) 的无实参调用就是零实参的例子，转换的目标形式是切片（变长参数 elems 的 %T 打出来是 []int）。〔TB21 p6-7〕〔TB21 p20〕回看 myFprintf 的调用：os.Stdout 要先赋值给接口形参 io.Writer，实参 "Hello, Go" 被打包成切片传给变长形参 a，一条调用语句把两条例外都凑齐了。

**判定标准：什么算一等公民。** "把一段行为存进变量"这件事有正式的名字。业界和教科书都没有给出"一等公民"的精准定义，原课引用 wiki 发明人、C2 站点作者沃德·坎宁安（Ward Cunningham）的解释作为工作定义：如果一门编程语言对某种语言元素的创建和使用没有限制，我们可以像对待值（value）一样对待这种语法元素，那么我们就称这种语法元素是这门编程语言的"一等公民"；拥有"一等公民"待遇的语法元素可以存储在变量中，可以作为参数传递给函数，可以在函数内部创建并可以作为返回值从函数返回。〔TB21 p9〕

Go 函数这四条全部满足，原课按特征一到特征四逐一展开：特征一"存储在变量中"就是开头的 myFprintf；特征二"在函数内创建并通过返回值返回"，例证是 setup(task) 返回一个做上下文拆除的匿名函数，这种引用了包裹函数局部变量的匿名函数就是闭包（defer 与闭包的求值规则见 05 章，闭包捕获循环变量的坑，本章"循环"一节刚用 for range 讲过）；特征三"作为参数传入函数"，例证是 time.AfterFunc(time.Second*2, func() { println("timer fired") })，定时器到点执行传入的函数。〔TB21 p9-12〕特征二、三本小节不展开，落点是特征四：函数拥有自己的类型，它是把"函数"和"组合"连起来的那根线。

**特征四：函数拥有自己的类型。** 推理链原课给得很直白：作为一等公民的整型值拥有自己的类型 int，且只是 int 的一个实例，字符串值、布尔值同理；前面刚得到过"每个函数声明只是对应函数类型的一个实例"，所以每个函数都和整型值一样拥有自己的类型，也就是函数类型。〔TB21 p11〕再进一步：可以像基于整型、字符串类型自定义类型那样，基于函数类型自定义类型，标准库里现成的两例：

```go
// $GOROOT/src/net/http/server.go
type HandlerFunc func(ResponseWriter, *Request)

// $GOROOT/src/sort/genzfunc.go（2021 年课程引用时的版本）
type visitFunc func(ast.Node) ast.Visitor
```

〔TB21 p12〕版本边界要交代：HandlerFunc 在现行 go1.26.4 源码中原样可见，定义未变，注释为 godoc 链接格式加了方括号；visitFunc 出自当年版本的 sort 包，现行源码里已没有 genzfunc.go 这个文件，全树也搜不到 type visitFunc。今天要找同结构的样本，go/ast 包的 inspector 是一个：type inspector func(Node) bool，配一个 Visit 方法，同样是"函数类型加方法"的组合。〔实测：/Users/akka/Toolchain/go/current/src/net/http/server.go:2278-2287、/Users/akka/Toolchain/go/current/src/go/ast/walk.go:357-363〕

最后一块拼图：整型、字符串等所有类型都能做的操作，比如显式转型，同样可以用在函数类型上，函数可以被显式转型，这个能力在特定领域有典型应用。〔TB21 p12〕

**应用：http.HandlerFunc(greeting)。** 最典型的应用就是标准库 http 包的 HandlerFunc，先看原课的完整例程：

```go
func greeting(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "Welcome, Gopher!\n")
}

func main() {
	http.ListenAndServe(":8080", http.HandlerFunc(greeting))
}
```

角色分工：greeting 是业务函数，往 ResponseWriter 写一行欢迎语；ListenAndServe 是服务入口，监听 8080 端口；http.HandlerFunc(greeting) 是两者之间的适配层。运行后用户通过浏览器或类似 curl 的命令行工具访问 8080 端口，会收到 "Welcome, Gopher!" 的文字应答，原课说这是日常最常见的用 Go 构建 Web Server 的例子。原课 09 讲只讲了怎么写、没讲原理，这一讲把原理补上；手册里最小服务的对应落点在 11 章。〔TB21 p12-13〕

为什么不能直接写 http.ListenAndServe(":8080", greeting)？ListenAndServe 会把来自客户端的 http 请求交给第二个参数 handler 处理，而 handler 参数的类型 http.Handler 是一个接口类型，只有一个方法：〔TB21 p13〕

```go
// $GOROOT/src/net/http/server.go
type Handler interface {
	ServeHTTP(ResponseWriter, *Request)
}
```

〔实测：/Users/akka/Toolchain/go/current/src/net/http/server.go:88-90〕greeting 的函数类型 `func(http.ResponseWriter, *http.Request)` 与 ServeHTTP 方法的签名完全一致，但直接传编译不过：原课记录的报错是 `func(http.ResponseWriter, *http.Request) does not implement http.Handler`，原文在括号处右缘截断，现行工具链会补全括号里的细节 (missing method ServeHTTP)，这是编译器缺方法报错的固定模板。原因在接口的判定方式：接口认的是方法集合。greeting 是函数值，它的类型是未命名的函数类型，没有任何方法，方法集合为空，所以"签名一致"不等于"实现了接口"，它不能赋给 Handler 类型的参数。〔TB21 p13〕〔实测：/Users/akka/Toolchain/go/current/src/go/types/lookup.go:483〕

再看转型后的类型靠什么满足接口，直接看 $GOROOT/src/net/http/server.go 的现行定义（go1.26.4，2282 行是类型定义，2285-2287 行是方法）：

```go
// The HandlerFunc type is an adapter to allow the use of
// ordinary functions as HTTP handlers. If f is a function
// with the appropriate signature, HandlerFunc(f) is a
// [Handler] that calls f.
type HandlerFunc func(ResponseWriter, *Request)

// ServeHTTP calls f(w, r).
func (f HandlerFunc) ServeHTTP(w ResponseWriter, r *Request) {
	f(w, r)
}
```

〔TB21 p14〕〔实测：/Users/akka/Toolchain/go/current/src/net/http/server.go:2278-2287〕HandlerFunc 是基于函数类型定义的新类型，底层类型就是 func(ResponseWriter, *Request)；它带一个 ServeHTTP 方法，方法体只有一行 f(w, r)，把请求原样转交给被包装的函数。有了这个方法，HandlerFunc 的方法集合覆盖了 Handler 接口的全部要求，从而实现该接口。所以 http.HandlerFunc(greeting) 的真正含义是：把 greeting 显式转型为 HandlerFunc 类型，后者实现了 Handler 接口，满足 ListenAndServe 第二个参数的要求。请求到达时的执行路径也就清楚了：ListenAndServe 把请求交给 handler 参数，实际执行的是 HandlerFunc 的 ServeHTTP，它转手调用 greeting。〔TB21 p13-14〕

这行转型为什么能通过编译器检查？因为 HandlerFunc 的底层类型 func(ResponseWriter, *Request) 与 greeting 的函数类型一致，底层类型相同的两个类型可以显式互转，原理和 type MyInt int 之后 y := MyInt(x)（x 为 int）完全一样，底层类型与显式互转的完整规则在 03 章第五节。签名对不上时转型同样在编译期报错，不会把失败拖到运行期。〔TB21 p14〕

选择条件也由此而来：手头已有的就是一个签名正确的普通函数、要的只是一个动作时，HandlerFunc 转型是最省事的适配，源码注释 "If f is a function with the appropriate signature" 说的就是这个前提；一个角色要暴露多个方法时，应该定义自己的类型去实现接口，这是从"接口只认方法集合"直接推出的教学推导。〔TB21 p14〕

原课的例程要起真服务器、开浏览器或 curl 才能观察应答。想不起服务器就验证这条链，可以借 04 章和 14 章都用过的 httptest：造一个请求和一个会记录写入的 ResponseWriter，直接调用转型后值的 ServeHTTP 方法（用 httptest 直调是本手册的写作落点，原课没有这一步）：

```go
package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
)

func greeting(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "Welcome, Gopher!\n")
}

func main() {
	var h http.Handler = http.HandlerFunc(greeting)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	fmt.Println(rec.Code, rec.Body.String())
}
```

第一行赋值本身就是一次编译期验证：h 的静态类型是 http.Handler，转型结果能赋给它，证明 HandlerFunc 实现了接口。ServeHTTP 被直接调用，方法体那一行 f(w, r) 把请求转交给 greeting，greeting 通过 fmt.Fprintf 把 "Welcome, Gopher!\n" 写进 recorder。输出按 httptest 源码推导：NewRecorder 初始 Code 为 200，第一次 Write 会把状态码定为 200 并把字节存进 Body，所以 rec.Code 是 200、rec.Body 是 "Welcome, Gopher!\n"，Println 打出 200 Welcome, Gopher! 并多带一个空行（这是按源码推导的教学推导，未实跑；14 章对同型代码记录过 go1.26.4 的实测输出）。〔TB21 p12-14〕〔实测：/Users/akka/Toolchain/go/current/src/net/http/httptest/recorder.go:51-57、:102-112〕

一个现成的练习（digest 的改造点 X2）：把例程里的 http.HandlerFunc(greeting) 改回直接传 greeting，观察编译报错；再恢复转型，给转型结果加一行 %T 打印，打出来是 http.HandlerFunc。〔TB21 p12-14〕

把这条证据链收回"组合"哲学：数据侧，嵌入与接口把能力和契约拼起来；行为侧，一等公民让一段行为可以存进变量、作为参数和返回值流转，再配上"函数类型能自定义、能挂方法"，普通函数就能被包装成满足接口的类型。HandlerFunc 正是"函数加方法等于适配器"的范本，本小节开头提到的中间件模式，在 04 章"中间件：包装处理器，并决定是否继续"一节有完整展开，那里的包装函数内层就是 http.HandlerFunc 转型。〔TB21 p12-14〕〔TB02 p7〕

## 四、并发：语言内建，不是库

2007 年前后 CPU 主频涨不动了，性能竞争转向多核。Go 从设计第一天就把"面向多核"当原则，所以 goroutine、channel、select 是语言的一部分，不是某个库。〔TB02 p8〕

```go
package main

import "fmt"

func main() {
	ch := make(chan string)
	go func() { ch <- "hello from goroutine" }()
	fmt.Println(<-ch)
}
```

```text
hello from goroutine
```

`go` 是关键字，`chan` 是内建类型，`<-` 是运算符，全都不用 import。goroutine 是用户态轻量线程，运行时默认给每个 goroutine 只分配 2KB 栈，切换也不陷入内核，所以一个程序里开成千上万个 goroutine 很常见。〔TB02 p8〕（补一句本机验证过的细节：自 Go 1.19 起运行时会按历史平均栈用量调整新 goroutine 的初始栈大小，`runtime/stack.go` 里有 `adaptivestackstart`，所以 2KB 是起点和下限，不是永远固定的值。）而且所有 Go 代码都在 goroutine 里执行，连运行时的代码也不例外，goroutine 不是"另开的线程"，而是代码运行的基本载体。这个设计和 Rust、JS 都不同：Rust 要自己选 async 运行时（tokio、async-std 之类），JS 是单线程加事件循环，Go 则是语言和运行时一体化提供并发，用户不用挑。

这里要提醒一句：goroutine 便宜，不等于免费。每个 goroutine 都要有退出路径，否则就是泄漏，第 06 节会展开。并发也不只是语法问题，它是一种程序结构设计方法：把独立计算拆开，再用 channel 和 select 把它们连接起来。〔TB02 p8〕

## 五、面向工程：把工程问题写进语言

很多语言把"构建慢、依赖乱、代码难读"当成使用层面的事，Go 的设计者不这么看，他们把这些工程问题直接写进了语言和工具的设计里。〔TB02 p9〕

**未使用的导入是编译错误，不是警告。** 下面这个程序直接编译失败：

```go fail
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println("x")
}
```

```text
"os" imported and not used
```

为什么这么严？因为 Go 要保证任何程序的依赖树是精确的：你 import 了就一定用了，构建时就不会把没用到的包也编译进来，大工程的增量构建才快得起来。〔TB02 p9〕

**故意没有默认函数参数。** 理由是一条工程观察：很多开发者会靠默认参数往函数上堆越来越多的形参，来掩盖 API 设计本身的缺陷，结果函数参数越堆越多，清晰度下降。一刀切掉这个机制，逼你把 API 设计得参数更少、更直接。〔TB02 p9〕

配套的是工具链：`gofmt` 统一代码风格，别人在争论缩进和换行的时候，Go 团队已经把答案定死了；`go build`、`go test`、`go doc`、性能剖析都收在同一个 `go` 命令下，不用像 JS 那样自己拼一堆工具。〔TB02 p10〕

## 六、几个值得多想一步的取舍

世界观这一节的价值，不在背下五条口号，而在理解几个具体取舍。它们平时不太显眼，但会在你写 Go 的第一周反复出现。

**没有异常，不等于没有 panic。** panic 通常用于程序无法按当前约定继续执行的情况，不应替代常规错误返回。库内部可以在明确的边界恢复自己主动触发的 panic 并转换为 `error`；不要一概捕获所有 panic，以免隐藏程序缺陷。〔TB02 p14〕

**运行时随二进制一起交付。** 垃圾回收、并发调度、栈管理都在 runtime 里，runtime 会和你的源码一起编译并链接进目标程序，哪怕一个 hello world 也带着它。〔TB01 p12-13〕代价是二进制体积更大；收益是部署简单，纯 Go 程序（不用 cgo）默认静态链接，拷一个文件过去就能跑。

**有兼容性承诺，也自带电池。** Go 1.0（2012）起，官方承诺符合 Go1 规范的源码向后兼容，十年前的代码今天还能编译；配合简单哲学，这十年新增的语言特性也屈指可数。〔TB01 p7〕〔TB00 p3〕标准库方面，`net/http`、`crypto`、`encoding` 能直接撑起生产级服务，第三方依赖可以少很多。〔TB02 p9-10〕

**标准库是学写法的范本。** `http.HandlerFunc` 是 Russ Cox 利用"函数是一等公民且能挂方法"这个特性设计的；`io.Reader`/`io.Writer` 奠定了 Go 的 I/O 结构。想学地道的 Go 写法，多读标准库。〔TB01 p5〕〔TB00 p12〕

**它的自我定位是服务器端。** Go 创建的最初目的，就是构建流行的、高性能的服务器端编程语言，用来替代当时主流的 Java 和 C++；在带 GC 和运行时的语言里，它的性能名列前茅。〔TB00 p4〕这个定位决定了后面所有章节的语境：你在写的是后端服务，不是脚本。

**简单哲学的回报是极低的门槛。** Tony Bai 回忆 2011 年他用 Rob Pike 的教程，一天就学完了 Go 的全部语法，一周就能写简单实用的小程序。〔TB00 p3〕这不是吹嘘，而是"简单"这条哲学的直接结果：关键字少、特性克制，换来的就是入门快，代价则是很多基础设施要自己搭。

**语言叫 Go，不叫 golang。** 初学者常把它写成 golang，其实 "golang" 只是官网域名的拼法（当时 go.com 被占用），语言本身就叫 Go。〔TB01 p4〕

## 七、迁移者最容易犯的七个错误

Tony Bai 把"用别的语言的思维写 Go"叫作"形神皆丧"，并指出另一个更根本的问题是没建立"设计意识"：能不能选对语言元素，搭出程序的骨架。尤其要弄清不同语言元素所在的层次，否则只能一直停留在 hello world 的世界里。〔TB00 p6〕

七个高频错误，逐个对号入座：

1. **用类和继承建模**：改成小结构体、小接口、嵌入，见 04 节。
2. **用 panic 当异常**：错误是常态，走返回值，见 05 节。
3. **把 goroutine 当免费的**：创建便宜，但每个都要有退出路径，见 06 节。
4. **提供方先写接口**：Go 的接口由使用方定义，见 04 节。
5. **给每个字段写 getter 和 setter**：Go 习惯直接访问字段，只在需要不变量时加方法。〔TBX04 p11-12〕
6. **想要最新版依赖**：Go 取满足所有依赖方要求的最小版本，构建可重现，见 02 节。
7. **先找大而全的框架**：先看标准库够不够。

给新语言做体检也有个快办法，不必从头读语法书：先问它强类型吗？静态还是动态？内建类型有哪些？有结构体、字典、泛型吗？〔TBX01 p1-2〕回答完这些再读规范，比从头啃快得多。Go 的答案是：静态强类型，原生复合类型（数组、切片、map、结构体、channel），泛型有但克制。

## 小结

1. Go 的每个"缺失"都是刻意取舍，判断标准是五条哲学：简单、显式、组合、并发、面向工程。
2. 显式：不隐式转换类型，错误经返回值上抛，没有看不见的控制流。
3. 组合：没有继承，靠类型嵌入（垂直）和接口（水平）扩展。
4. 并发：goroutine 加 channel 是语言内建，栈初始 2KB，但不是免费的。
5. 面向工程：未使用导入是编译错误，没有默认参数，gofmt 统一风格，一个 `go` 命令包办工具链。
6. panic 留给"程序即将崩溃"，不是异常机制；运行时随二进制交付；Go 1.0 起有兼容性承诺。
7. 迁移的头号敌人是"用别的语言的思维写 Go"。

## 常见坑

- 以为 `int16` 和 `int` 能直接相加：必须显式转型。
- 把 panic 当异常用：错误走返回值，panic 表示程序要崩了。
- 以为没有继承就没法做多态：组合加接口完全够，见 04 节。
- 一上来就找全家桶框架：先看标准库能不能覆盖。
- 想给每个字段加 getter/setter：Go 直接访问字段是常态。
- 以为导入未使用的包只是警告：它是编译错误，会挡住构建。

## 判断题与答案

先自己答，再看后面的答案。

1. `var x int32 = 1; var y int = 2; _ = x + y` 能编译吗？为什么 Go 这样设计？
2. 为什么 Go 没有默认函数参数？（提示：想想 API 设计缺陷会被什么掩盖。）〔TB02 p9〕
3. 一个库大量用 panic 表达常规失败，这个 API 设计应该怎么改？
4. Go 程序的二进制里，为什么连 hello world 都带着垃圾回收器？

**答案**

1. 不能，`int32` 和 `int` 类型不同，Go 不做隐式数值转换。这样设计是为了显式：任何类型转换都得自己写清楚，避免"编译器偷偷提升"带来的意外。
2. 默认参数会被用来给函数不断加形参，掩盖 API 设计缺陷，让函数参数越堆越多、可读性下降，所以一刀切掉了。
3. 库应把可预期的失败设计成 `error` 返回，而不是要求调用方到处 recover。对内部主动触发的 panic，可在明确边界转换为错误；不要无差别吞掉程序缺陷。〔TB02 p14〕
4. 因为 Go 的 runtime 随二进制一起交付，垃圾回收、并发调度、栈管理都在里面，hello world 也带着它，换来单文件部署的便利。

## 在项目里

`dnsproxi-go/` 把这节讲的东西落到了实处（module 与目录布局见 02 节）。入口 `main.go` 的 `main` 先 `loadConfig()` 读环境变量，再 `newLogger(...)` 建日志，最后 `p.Run(ctx)` 启动代理；`Run` 出错时 `logger.Error("run failed", ...)` 然后 `os.Exit(1)`，全程没有异常，也没有隐式初始化。

`cache/cache.go` 能看到「显式」落到数据上的样子：`Cache` 结构体里直接放一个 `mu sync.RWMutex` 字段，零值就能用，不用单独初始化；而 `m`、`slot`、`access` 三张 map 必须由 `NewCap` 显式 `map[Key]...{}` 建好，因为 nil map 不能写。这里没有嵌入或接口组合的现成例子，那部分看 04 节。

## 想深入：正文之外的干货

### TB00 学习路线与设计意识

- 弯路共四条：入错行盲目投入、只看不练、用其他语言的思维写 Go（作者叫它"形神皆丧"）、没建立"设计意识"。后两条是思维问题，不是语法问题，背多少关键字都没用。〔TB00 p6〕
- "设计意识"指选对语言元素、搭出程序骨架的能力，前提是分清各语言元素所在的层次；分不清，就一直停在 hello world 的世界里。〔TB00 p6〕
- 作者的五阶段路线对应三诀窍：心定（认同设计哲学，就是本节）、手勤（环境、程序结构、项目布局、构建）、脑勤（数据与算法加小项目，然后接口与 goroutine）。〔TB00 p6-8〕
- 接口和 goroutine 之所以被单独划进"核心篇"，是因为它们是仅有的两个能影响程序结构设计的语法元素：goroutine 管动态的并发骨架，接口管静态的组合骨架。〔TB00 p7〕
- 作者从 C 转过来，2011 年看 Rob Pike 的教程一天学完全部语法、一周写出可用小程序；这是"简单"哲学换来的入门速度。〔TB00 p3〕

### TB01 历史细节

- 三位创始人个个有来头：Ken Thompson 是图灵奖得主、Unix 之父、C 语法的联合发明人；Rob Pike 领导过 Plan 9、是 UTF-8 的最初设计者；Robert Griesemer 参与设计过 HotSpot 虚拟机和 V8 引擎。〔TB01 p2〕
- 初版设计思路是在 C 的基础上修缺陷、删诟病特性、加缺失功能：用 import 替代 include，去掉宏，加上垃圾回收和接口。〔TB01 p3〕
- 2008 年 Ian Lance Taylor 独立写了 gcc 前端，成为第二个能编译 Go 的编译器，证明了这门语言定义得足够严谨，他本人也因此成为团队第四人。〔TB01 p4-5〕
- Russ Cox 是第五位成员，后来长期负责核心技术团队；`http.HandlerFunc` 和 `io.Reader`/`io.Writer` 都出自他手。〔TB01 p5〕
- 编译器前端指词法分析到中间代码生成，后端指优化和目标代码生成；所谓 gcc 前端，就是读 Go 源码、产出 gcc 后端能识别的中间代码。〔TB01 p12〕
- 命名小史：语言叫 Go 不叫 golang，"golang"只是官网域名（go.com 被占了）；早期按后缀拆过 goc、goa、gol 三个工具，后来撤销，只留统一的 `go` 命令。〔TB01 p3-4〕
- 2009 年开源当年就拿下 TIOBE 年度语言；2012 年 3 月 Go 1.0 发布，同时给出 Go1 兼容性承诺：符合规范的源码保证向后兼容，十年前的代码今天仍能编译。〔TB01 p7-8〕

### TB02 哲学的更多证据

- 类型嵌入是"能力继承"而非数据继承：方法按名字匹配，被嵌入类型与新类型没有父子关系、没有上下转型。标准库里 `sync.Pool` 内部的 `poolLocalInternal` 就是靠嵌入 `Mutex` 白拿锁能力（本机 go1.26 源码里嵌入仍在，字段有变化）。〔TB02 p6〕
- 接口自己也能嵌接口：`io.ReadWriter` 就是把 `Reader` 和 `Writer` 嵌进来，凑出"又能读又能写"；这是标准库的惯用法。〔TB02 p6-7〕
- 水平组合的接口参数你天天在用：`io.ReadAll(r io.Reader)`、`io.Copy(dst Writer, src Reader)` 只认能力不认类型；注意旧教程里的 `ioutil.ReadAll` 从 Go 1.16 起废弃，改用 `io.ReadAll`。〔TB02 p7〕
- 关键字 25 个是主流语言里最少的，还有配套的减法：变量初始为零值、数组自带边界检查、首字母大小写决定可见性。〔TB02 p2-3〕
- 包的规则里藏了两个"工程向"决定：包路径必须唯一但包名不必唯一，所以不同仓库可以有同名包；类型别名是为大规模重构准备的，改名不动调用方。〔TB02 p9〕
- 作者答"性能敏感场景怎么办"：GC 开销小、延迟低，但不是没有；逐帧计算这类场景直接上 C/C++/Rust，面向服务的架构天然允许各服务各选语言。〔TB02 p12〕
- Rob Pike 自己也检讨：Go 的变量声明方式还是多了，重来一次会再砍掉一种。"简单"是深思熟虑的取舍，不是没做过特性。〔TB02 p13〕

### TBX01 泛型的两条实现路线

- 学新语言先问七个类型问题：强类型吗？动态还是静态？内建类型有哪些？有结构体吗？有字典吗？有泛型吗？按这张清单读规范，比从头啃快得多。〔TBX01 p1-2〕
- 别把两个维度搅在一起：Python 是强类型但动态（`int + str` 抛 TypeError，但变量可以先存 int 再存 str），JS 是弱类型且动态（整数加字符串会被转成拼接），Go 是强类型且静态。〔TBX01 p2-3〕
- 动态类型的代价不只有难读：编译期没有类型信息，对象的内存布局就定不下来，解释执行性能也差；所以小脚本用 Python、大型多人项目用 Java 这类静态语言，是按场景选，不是谁更好。〔TBX01 p4〕
- 泛型的动机是消灭重复：int 栈写完，float 栈除了元素类型全一样，C++ 用 `template <typename T>` 让核心逻辑只写一遍；最常见的用武之地是容器。〔TBX01 p5-7〕
- 两条实现路线决定了泛型的行为：C++ 按类型参数生成新类型（实例化），`Stack<int>` 和 `Stack<string>` 在机器码里是两个真的不同的类型；Java 为了兼容旧字节码选择擦除，`ArrayList<Integer>` 和 `ArrayList<String>` 运行时是同一个类，想靠泛型参数重载方法只会报 name clash。〔TBX01 p7-11〕
- 类型级函数是看泛型的另一个视角：`template <typename T> class vector` 是输入类型、输出类型的函数，`vector` 裸名不是合法类型；C++ 还允许值当类型参数（`template <int n>`），于是能写出编译期算斐波那契的模板元编程，递归忘了写特化基例就直接把编译器撑爆。〔TBX01 p7-9〕
- Go 1.18 的类型参数走的是"产生新类型"一侧，约束用 interface 写，对标 Rust 的 trait；标准库从 1.21 起自带泛型的 `slices`、`maps` 包。〔TBX01 p11-13〕

### TB99 怎么继续走

- 进阶路线是 T 字形：一竖是语言深度（reflect、cgo、unsafe、编译器、调度、GC 的实现），一横是工程宽度（标准库加工具链）。Kelsey Hightower 的话：不知道何时不该用一种工具，就还没掌握它。〔TB99 p3-4〕
- 检验学会了的办法是公开输出（费曼学习法）：写笔记、博客、做分享，把理解暴露给别人评判；起步阶段不怕错、不怕没人看。〔TB99 p4-5〕
- 选 Go 的理由可以落到数字上：可汗学院 2019 年把在线平台从 Python 迁到 Go，代码变多了，但性能提升近 10 倍。〔TB00 p4〕
- 结束语里"泛型是 Go 最后一次大的语法更新"的预言已被打破：1.23 的 range over func、1.24 的泛型类型别名还在往前走，别拿任何"已定论"当终点。〔TB99 p7-8〕

### TB19 循环：只有一种 for

- C/C++/Java/Rust/Python 都给了不止一种循环语句，Go 只保留 for 一种，这是"做一件事仅有一种方法"的践行，也是"简单"哲学落到控制结构上的样子。〔TB19 p1-2〕
- 经典形式四部分：`for preStmt; condition; postStmt {}`，前置语句只执行一次、循环变量用短声明（作用域限 for 隐式代码块），条件必须是布尔表达式，后置语句每轮迭代后更新循环变量。〔TB19 p2-4〕
- 除循环体外三部分都可省，但省前置或后置时分号必须留着，这是语法要求；只留条件时可省分号，`for i < 10 {}` 是日常第二常用形式；条件恒真就整体省掉，`for {}` 等价 `for true {}`，用最简形式。〔TB19 p4-6〕
- for range 是把四部分折叠进语义的语法糖：i、v 就是循环变量，隐含条件等价 `i < len(sl)`，隐含后置是"取下一个元素的下标和值赋给 i、v"。〔TB19 p6-7〕
- 只要下标写 `for i := range sl`；只要值用 `for _, v := range sl`，`_` 不能省，否则两种写法无法区分；两个都不要就写 `for range sl`（Go 1.4 起支持，等价 `for _, _ =`）。〔TB19 p7-8〕
- string 的 for range 每次给的是 Unicode 码点（rune）而不是字节，下标 i 是该码点 UTF-8 编码首字节的字节偏移（"中国人"输出 0/3/6）；经典形式按下标循环则逐字节，两种写法语义不同。〔TB19 p8-9〕
- map 只能用 for range 遍历（经典形式不支持），map 没有下标概念；channel 也用 for range 读，无数据时阻塞在读上，channel 关闭时循环结束，这是隐含的循环条件。〔TB19 p9-11〕
- continue 中断本轮迭代回到条件判断；Go 给 continue/break 加了 label 支持：嵌套循环里 `continue outerloop` 直接结束内层循环、回到外层继续下一迭代，等价于在外层放一个不带 label 的 continue。〔TB19 p11-13〕
- 别用 goto 替代带 label 的 continue：goto 会把内外层循环全部终结，再从 label 处重新开始整个嵌套循环，语义完全不同。〔TB19 p13〕
- break 彻底终结当前循环语句；不带 label 只跳出最内层，跳外层要 `break outerloop`。〔TB19 p13-15〕
- 坑一（Go 1.22 前）循环变量重用：for range 的 i、v 整个循环只声明一次、逐轮重用，goroutine 闭包捕获的是同一个变量，循环结束后输出全是最终值（5 个 goroutine 全打 4 5）；修法是给闭包加参数按值绑定。〔TB19 p15-17〕Go 1.22 起改成每轮迭代新建变量，这个坑在新代码里消失了，但读老代码要认得它。〔TB19 p17〕
- 坑二：参与循环的是 range 表达式的副本。数组被 range 时拷贝整个数组，首轮改 a[1]/a[2]，后面取到的仍是旧值 2、3；切片副本的结构体里 array 指针还指向原底层数组，所以改动可见。数组想看到修改，用 `range a[:]`、`range &a` 或循环内 `a[i]` 直取。〔TB19 p18-21〕
- 坑三：range map 的副本与原变量指向同一个 map，遍历中删改会影响后续迭代且结果随机；新增的键值对是否被遍历取决于它落在哪个 hash bucket、是否排在遍历位置之后。边遍历边改 map 的代码要格外小心。〔TB19 p21-23〕

### TB20 switch：修掉 C 的三处老毛病

- 分支多时 switch 比 if/else if 链简洁紧凑：扩展名分发的例子里 if 链写成 switch 后每个 case 一行，可读性一眼见高下。〔TB20 p1-3〕
- 一般形式 `switch initStmt; expr { case ... }`：initStmt 可短声明 switch 自用的临时变量，作用域限 switch 隐式代码块，就近声明把变量作用域压到最小。〔TB20 p3-4〕
- 求值次序固定：先求 switch 表达式，再按 case 书写顺序自上而下逐一求值比较，表达式列表从左到右；一旦匹配，后面的 case 表达式不再求值（哪怕也能匹配）。default 与书写位置无关，只在所有 case 都没匹配时执行。〔TB20 p4-5〕
- 配合求值次序有个小优化：把匹配概率高的 case 排前面，列表里概率最高的放最左。〔TB20 p5〕
- C 的 switch 表达式只能是 int 或枚举；Go 放宽到"类型支持比较操作"即可：整型、布尔、字符串、复数、元素可比较的数组、字段可比较的结构体都行。〔TB20 p5-7〕
- switch 表达式是恒为 true 的布尔值时可整体省略，直接 `switch { case cond1: ... }` 当 if 链用；带 initStmt 时分号不能省，因为 initStmt 是语句。〔TB20 p7〕
- case 支持逗号分隔的表达式列表 `case 1, 2, 3, 4, 5:`，一个分支多值匹配；C 要串联多个空 case 共享逻辑，Go 一行搞定。〔TB20 p7-9〕
- Go 取消了 C 的隐式穿透：每个 case 执行完就结束 switch，"一个 case 一件事"是安全默认；真要穿透就显式写 fallthrough，这是显式哲学的又一处落地。〔TB20 p9〕
- fallthrough 的细节：它直接执行下一个 case 的分支代码而不对下一个 case 的表达式求值；可以连续穿透直到 default；如果已经是最后一个 case 且后面没有 default，再写 fallthrough 直接编译报错。〔TB20 p9-10〕
- type switch 是 switch 专属语法：`switch x.(type)` 里的 x 必须是接口类型变量，求值结果是它的动态类型，case 后面跟具体类型；用 `switch v := x.(type)` 还能拿到动态类型对应的值（v 是值，不是类型）。〔TB20 p10-12〕
- 限定接口后 case 也要守规矩：switch 后用具体接口类型 I 时，case 只能写实现了 I 的类型，写没实现的类型（如 int、string）编译器报 `impossible type switch case`。〔TB20 p13〕
- 坑：不带 label 的 break 跳出的是"所在最内层的 for、switch 或 select"。for 里嵌 switch 找第一个偶数，break 只出了 switch 不出 for，结果是最后一个偶数 12 而不是 6；修法是给 for 打 label 再 `break loop`，select 同样会拦截 break。〔TB20 p14-16〕

### TB21 函数是一等公民

- 函数声明五部分：func、函数名（同包唯一、遵守首字母大小写导出规则）、参数列表（参数名在前类型在后）、返回值列表、函数体；函数体都可省（那种声明在 Go 之外用汇编实现，靠链接器按函数名接上）。〔TB21 p1-3〕
- 看懂函数声明的钥匙是把它等价成变量声明：函数名就是变量名，func 加参数列表加返回值列表构成函数类型；参数列表与返回值列表的组合叫函数签名，是判断两个函数类型是否相同的唯一依据，表述时省去参数名，如 `func(io.Writer, string, ...interface{}) (int, error)`。〔TB21 p3-5〕
- 每个函数声明只是对应函数类型的一个实例，和 `var a int = 13` 里 a 是 int 的实例一个道理；匿名函数 `func(){}` 之于函数变量，如同 `T{}` 之于结构体变量。但日常定义函数仍用传统声明，别改用变量声明形式。〔TB21 p5〕
- Go 参数恒为值传递：实参的内存表示逐位拷贝给形参。整型、数组、结构体拷贝自身，开销与自身大小成正比；string、切片、map 的内存表示是数据内容的"描述符"，只拷描述符不拷数据，开销固定，这就是"浅拷贝"，所以往函数里传大切片不贵。〔TB21 p6〕
- 值传递两个例外由编译器介入：形参是接口类型时实参先装箱赋给接口；形参是变长参数时零个或多个实参被打包转换。〔TB21 p6〕
- 变长参数本质是切片：`elems ...int` 的 `%T` 打出来就是 `[]int`，函数体内 len、append、`elems...` 展开全能用。〔TB21 p6-7〕
- 多返回值是 Go 特有（C/C++/Java 没有），三个形式：无返回值、单值不加括号、两个及以上加括号；Go 的错误处理机制整个建立在多返回值之上。〔TB21 p7-8〕
- 具名返回值是特例不是默认：defer 里修改外层函数返回值时更优雅，返回值多时省去 return 后的长串（标准库 time 的 parseNanoseconds 用裸 return）。〔TB21 p8-9〕
- Ward Cunningham 对一等公民的解释：创建和使用无限制、可以像值一样对待，能存进变量、能作参数、能在函数内创建、能作返回值。Go 的函数四条全满足。〔TB21 p9〕
- 特征落到语法上：匿名函数可赋值给变量（`%T` 打出完整函数类型）；函数内可创建函数并返回，引用了包裹函数局部变量的匿名函数就是闭包，闭包与包裹函数共享同一变量，变量生命周期随闭包延长；函数能作参数（`time.AfterFunc(d, func(){...})`）；函数还有自己的类型，标准库基于函数类型定义了 `net/http.HandlerFunc`、`sort` 的 visitFunc。〔TB21 p9-12〕
- 应用一（转型实现接口）：`greeting` 函数的类型与 `http.Handler` 接口的 ServeHTTP 方法签名一致，但函数本身不能直接传（不实现接口）；`http.HandlerFunc(greeting)` 把函数显式转型为带 ServeHTTP 方法的 HandlerFunc 类型，原理与 `MyInt(x)` 依赖底层类型 int 相同。这就是 01 正文"函数是一等公民且能挂方法"的完整机制。〔TB21 p12-14〕
- 应用二（闭包固定高频参数）：`partialTimes(x)` 返回 `func(y int) int { return times(x, y) }`，把高频乘数固定住生成专用函数（timesTwo、timesThree）；留言区点破这就是柯里化，函数参数越多（5 个以上）收益越大。〔TB21 p14-18〕

### TBX08 他山之石：怎么继续找资料

- 国内 Go 书少有三个原因：语言年轻（2021 年才 12 岁，比最年轻的 Java 还小 14 岁）；还没当上哪个领域的"品类代名词"（Web 是 Java、深度学习是 Python、前端是 JS，Go 在云原生、微服务占优但不算代名词）；翻译有时滞。国内流行节点是 2015 年第一届 GopherChina（Go 1.4）之后，同年 Go 1.5 自举加 GC 延迟大降才彻底铺开。〔TBX08 p2-3〕
- 书单（按作者推荐度从低到高）：《The Way To Go》是 Go 百科全书，成书于 Go 1.0 时代（2012.3），无中文版，无闻在 GitHub 发起过中译项目；《Go 101》（老貘）相当于 Go 的"标准解读"，开源电子书与付费版并行，更新与 Go 演化同步。〔TBX08 p3-6〕
- 再往上三本：《Go 语言学习笔记》（雨痕）精华在源码剖析，基于 1.5 讲内存分配、GC、调度、channel、defer 的实现；《Go 语言实战》薄而直击 Go 与其他语言的不同（类型系统、并发、标准库、测试），适合先建立框架；《Go 程序设计语言》（tgpl，Kernighan 参与）是人手一本的圣经，对标 K&R。〔TBX08 p7-11〕
- 读书教训比书单更值钱：有人《Go 语言实战》读一遍全忘、《Go 程序设计语言》读到第 9 页的 map 就懵，只看不练无效，边看边练最有效；先吃透一门课再计划读书（一本中文一本英文加官方博客），资料不在多而在精。〔TBX08 p16-17〕
- 最权威的资料是官方文档，必读六件：语言规范、module 参考文档、命令参考手册、Effective Go、标准库包参考手册、常见问答；主线开发中文档在 tip.golang.org。〔TBX08 p12-13〕
- 信息流够用就好：博客盯 Go 官博、Russ Cox、Rob Pike、Dave Cheney 几个源头；播客一个 Go Time 就够（200 多期）；演讲按时间顺序过一遍 Go 官方演讲归档和 GopherCon 系；国内订阅 Go 语言爱好者周刊和 Gopher 日报两个邮件列表足矣。〔TBX08 p13-15〕
- 两个最容易被忽视的宝藏：Go 项目的官方 issue 列表，能实时看演进状态、挖某个特性的来龙去脉，对深入理解特性极有帮助；Go 代码 review 站点，读核心团队的评审过程和意见，学的就是 Go 的编码原则与地道惯用法，也是理解设计哲学的入口。〔TBX08 p15〕

（TB99 是全课的结束语，其余内容是课程回顾与团队动态，此处只取上面两条仍站得住的判断。TB01 里"Go 1.5 自举""TIOBE 年度语言"等叙述为当时事实，不代表当前状态；TB00 的五阶段学习清单对应原课程目录，本手册的 26 节结构已另行组织，不再照搬。）

## 想深入去哪查

- 设计哲学全文：TB02 全讲；历史与生态位：TB01、TB99。
- 学习路线与"设计意识"：TB00 p6-8。
- 类型系统与泛型的比较视角：TBX01。
- 旧长版：`notes/p2-lang-core.md`（Rust 经验换直觉）、`notes/p1-toolchain.md`（cargo 到 go 的命令映射）。
