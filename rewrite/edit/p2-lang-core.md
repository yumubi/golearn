---
chapter: p2-lang-core
title: 方法、接口与组合
stage: 1
absorbs: p30-methods-interfaces
keeps_separate: p2_5-idiom
minutes: 45
prereq: p1_5-basics
---

points:
| 1 | 方法就是首参数为 receiver 的函数 | 〔TB24 p6-8〕 | 记住：t.M() 只是 M(t) 的另一种写法，所以"值还是指针"的所有问题，都是函数传参问题。T.Get 这种方法表达式是这条本质的直接证据。 | 体系
| 2 | 选 receiver：先看接口，再看修改，最后看拷贝 | 〔TB25 p2-6〕〔TB25 p10-11〕 | 记住：要改原实例用指针接收者；需要实现某个接口时，整个类型的方法要统一；其余默认值接收者，类型很大再换指针接收者。同一类型别混用两种。 | 思维
| 3 | 方法集合不对称：值类型只有值接收者的方法，指针类型有全部方法 | 〔TB25 p7-10〕 | 记住：接口实现看方法集合。指针类型能赋给接口而值类型不能，就是因为这条。 | 体系
| 4 | 接口是隐式契约，由使用方定义 | 〔TB28 p8〕〔TB30 p3〕〔TB30 p9〕 | 记住：实现者不需要知道接口存在；接口放在用它的地方，而不是实现它的地方。这是和 Java 最大的分野。 | 思维
| 5 | 小接口：为什么好，怎么落地 | 〔TB28 p10-15〕 | 记住：接口越大抽象越弱。先抽象，再按使用场合拆，最后看能否拆到单方法。 | 思维
| 6 | 接口值在内存里是一个二元组 | 〔TB29 p1-4〕〔TB29 p7-9〕 | 记住：接口变量存的是（类型信息，数据指针）。静态上编译器查方法集合，动态上运行时记真实类型，这就是"动静兼备"。 | 体系
| 7 | 装箱与相等：接口不是免费的 | 〔TB29 p9-10〕〔TB29 p21-23〕 | 记住：赋给接口会分配并拷贝；两个接口相等要类型和数据都相等。性能敏感处少装箱。只讲到概念地图深度，不展开 runtime 结构体。 | 体系
| 8 | 类型嵌入是编译器生成的转发，不是继承 | 〔TB26 p9-10〕 | 记住：s.Read 被改写成 s.Reader.Read；被嵌入的类型不知道外层存在，没有虚调用。 | 体系
| 9 | 嵌入后的方法集合；defined 类型与别名 | 〔TB26 p15-20〕 | 记住：嵌入 T1 和指针 T2 时，指向外层结构体的指针拿到的是指针 T1 的方法；type S T 方法集合为空，type S = T 与 T 完全相同。 | 体系
| 10 | 垂直组合与水平组合：Go 用组合代替继承 | 〔TB30 p3-6〕〔TB30 p9〕 | 记住：嵌入是"器官"，接口参数是"关节"。Go 的类型、方法、接口三者互相正交。 | 思维
| 11 | 水平组合三式：包装器、适配器、中间件 | 〔TB30 p10-16〕 | 记住：包装器（同接口进同接口出）可链式；适配器把函数变成接口值（HandlerFunc）；中间件 = 包装器 + 适配器。 | 思维
| 12 | 空接口与 any：什么时候可以用 | 〔TB30 p16-17〕〔TB28 p5-6〕 | 记住：空接口不提供信息；标准库里只有容器算法和格式化日志合理。Go 1.18 起 any 是 interface{} 的别名，泛型能做的优先泛型。 | 思维

pitfalls:
| 1 | nil 指针装进 error 接口后 err != nil | 〔TB29 p3-5〕〔TB29 p23-25〕
| 2 | 混用值接收者与指针接收者：值接收者改不了原实例；一半方法是指针接收者时值类型丢掉接口资格 | 〔TB25 p2-4〕〔TB25 p6-9〕
| 3 | 把嵌入当继承：外层同名方法不虚调；嵌入两个有交集的接口且自己没实现时报 ambiguous selector | 〔TB26 p9-10〕〔TB26 p12-13〕

project: 去 dnsproxi-go 里找并读这些真实代码再写，找不到就写"这里没有现成例子"。候选：dnsproxi-go/tcp/server.go 的 streamConn（适配成 net.PacketConn，隐式满足接口）；dnsproxi-go/logger.go 的 ctxHandler（包装 slog.Handler，包装器模式）；dnsproxi-go/tcp/server.go 的 Handler 函数类型（适配器思想）。

exercises: 保留 exercises/p2lang 与 exercises/p30 两个练习包不动。正文只挑 5 到 6 个测试，要覆盖预测、观察、改造各至少 1 个；测试名必须在这两个包里真实存在。候选：TestMethodSetMatrix、TestEmbedNotInherit、TestNilErrorTrap、TestBoxingCost、TestCapReaderChain、TestMiddlewareCompose、TestTypeAliasMethodSet。

deep: TB24 TB25 TB26 TB28 TB29 TB30。旧长版 notes/p2-lang-core.md 与 notes/p30-methods-interfaces.md 在 git 里的位置（提交哈希用 git log 查）

lookup:
- 方法声明六部分与 receiver 三条约束（基类型不能是指针或接口、同包、只能一个）：编译器会报错，遇到再查 〔TB24 p2-5〕
- 接口声明细则、非导出方法接口、嵌入接口的历史约束（Go 1.14 前）：〔TB28 p3-5〕〔TB26 p4-5〕
- io 包接口组合清单：〔TB26 p4〕
- 反射 API（Type/Value/CanAddr/Call）：补全对运行时类型系统的理解，但短期少用，遇到再查 〔C:dachang/reflection〕

dropped:
- TB24 K3 到 K7 声明细则：编译器会拦，对体系和思维无增量，只留 lookup 一行
- TB27 trace 工具（AST 注入、gid）：只对调试有用，对体系无帮助，删
- TB29 K5/K10/K13（_type 结构、dump 工具、装箱快速路径）：实现细节且随版本变，删；装箱只讲概念
- TB30 K15、TB28 K13 思考题：删
- TB24 迭代变量 go v.print() 陷阱：Go 1.22 起已修复，只在 pitfalls 之外一句话带过，不占名额
- 旧 p2 的"零值"小节归 p1_5；"错误"小节归函数与错误章；类型断言并入要点 4 的代码
