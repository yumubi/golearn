# 18 分布式一致性：部分失败、补偿与共识

> 详细程度：深入 · 约 60 分钟
> 材料：W15-1、W15-2、C:dachang/distributed_transaction、C:dachang/raft、C:dachang/etcd · 旧长版：无整章对应
> 前置：04 接口与组合、13 缓存与幂等、16 稳定性（重试、超时）、17 迁移（双写与校验）

## 先看一笔永远对不上的账

用户打赏作者 1 块钱，系统里有三个环节要动：打赏模块记一笔打赏、支付服务记一笔支付、账号服务给作者加钱（资料里是读者发起、webook 记录并调第三方、第三方完成支付、创作者最终提现这条链，〔W15-1 p4〕〔W15-2 p5〕）。跑一天下来对账，三种对不上都有：

- 打赏记录显示成功，支付服务里查不到这笔钱：创建支付订单的请求超时了，钱可能付了也可能没付。
- 支付成功，作者账户没加钱：支付到记账之间那条消息丢了。
- 账户加了两遍钱：同一条"支付成功"的消息被投递了两次。

单机的数据库事务帮不上忙，因为这三个环节不在一个事务边界里。〔W15-2 p28〕本节起点就是：**一次业务操作跨越了多个独立的失败域**（多个服务、多个库、库加消息队列、本系统加第三方），任何两步之间都可能失败，失败之后你不知道对方是成功了还是没成功。所有一致性技术都在回答同一串问题：

1. 这一步失败（超时、重复）之后，系统处于什么状态？
2. 这个状态能不能被检测出来？
3. 检测出来之后，靠什么把它修回去？

还有三个"捣乱者"，缩写 NPC：网络延迟（TCP 保序不丢，但消除不了延迟）、进程暂停（GC 或云迁移可能停几分钟）、时钟漂移（石英钟本身有精度偏差、还受温度影响，NTP 对齐又会让本机时间前后跳）。分布式事务不依赖时间戳，真正天天咬人的是延迟和暂停。〔C:dachang/distributed_transaction/exception/npc.md:1-9〕后面的机制几乎都在和"响应还没回来"以及"请求其实还活着"作斗争。

工程上的总原则只有三条，优先级从高到低：

1. **尽量不跨**：按事务边界划分服务和库。同一个库里"更新余额加记流水"就是一个本地事务，别把它拆成两个服务。〔W15-2 p24〕
2. **跨了就接受最终一致**：用补偿、重试、对账让两边收敛，不追求每一刻都一致。
3. **必须强一致才上协议**：TCC、XA，付出吞吐与复杂度的代价。

## 和你已知语言的差异

| 问题 | Java / Kotlin | Rust | Go 生态 |
|---|---|---|---|
| 分布式事务框架 | Seata（AT/TCC/SAGA） | 少有成熟方案，多靠手写 | dtm（SAGA、TCC、二阶段消息、XA）〔C:dachang/distributed_transaction/分布式事务模式.md:55-56〕 |
| 消息表/发件箱 | 有框架内建（如事务性发件箱） | 手写 outbox 模式 | 手写 outbox 常见，dtm 的 msg 二阶段消息是现成品 |
| 两阶段提交 | JTA/XA，容器托管 | 无标准方案，靠数据库的 XA 命令或手写 | `database/sql` 层面少用，XA 靠数据库命令 |
| 共识库 | JRaft（SOFAJRaft） | openraft、raft-rs | hashicorp/raft、etcd（内置 raft） |
| 金额类型 | `BigDecimal` | `rust_decimal` | 没有 decimal 内建，惯用 `int64` 最小面额〔W15-1 p25〕 |

## 一、四种模式一张表：先知道有什么

先给地图，再逐个拆机制。资料把分布式事务分成四种模式：〔C:dachang/distributed_transaction/分布式事务模式.md:1-25〕

| 模式 | 做法 | 隔离性 | 适用 |
|---|---|---|---|
| SAGA | 长事务拆成短事务，失败时逆序补偿 | 无，中间状态对外可见 | 最简单、应用最广，需要回滚的场景 |
| 二阶段消息 | 本地事务与"待发消息"原子绑定，异步投递 | 无 | 不需要回滚，下游必然成功 |
| TCC | Try 预留资源、Confirm 使用预留、Cancel 释放 | 预留资源提供一定隔离 | 一致性要求高、不接受"先改后补偿" |
| XA | 两阶段提交，数据库原生支持 | 强，prepare 期间持锁 | 并发不高、无热点争抢 |

**TCC 的短事务限制与失败重试**：TCC 只适合短事务，分支的执行顺序要自己写代码控制，AP 重启后全局事务只能回滚、不能继续；Confirm/Cancel 失败时 dtm 会不断重试到成功，建议监控，重试超过 3 次就报警并人工介入。〔C:dachang/distributed_transaction/分布式事务模式.md:23-27〕〔C:dachang/distributed_transaction/分布式事务模式.md:9-13〕

**XA 的两阶段与恢复**：XA 是数据库原生两阶段提交，prepare 阶段所有参与者锁住资源并报 ready，commit/rollback 由 TM 裁决；主流数据库都支持，而且不需要 Barrier。xa end 之后、prepare 之前同一连接读写会报 XAE07；锁超时是 ERROR 1205，可用 innodb_lock_wait_timeout 调整。崩溃恢复分三种：已经 commit 的没事，还没 prepare 的回滚，prepare 过的要等 xa recover 裁决。代价是 prepare 期间锁不释放、伤害吞吐，commit 结果对客户端未知；适用条件是并发不高、没有热点争抢。〔C:dachang/distributed_transaction/分布式事务模式.md:29-53〕

选型口诀（DTM 官方建议）：**不需要回滚用二阶段消息，需要回滚用 SAGA，一致性要求高用 TCC，并发不高且没有热点用 XA**。〔C:dachang/distributed_transaction/分布式事务模式.md:54-56〕三个典型场景帮你把口诀落到地上：

- **秒杀**：创建订单和扣库存不是原子操作。用二阶段消息：Prepare 阶段去 Redis 扣库存（大概率失败，秒杀成功是小概率事件），Commit 阶段向 MySQL 写订单。〔C:dachang/distributed_transaction/分布式事务模式.md:57〕
- **订单系统**：下单涉及建单、扣库存、扣券、建支付单，任一步 crash 就不一致。用 SAGA，写好每一步的正向操作和补偿即可。〔C:dachang/distributed_transaction/分布式事务模式.md:58〕
- **缓存一致性**：Prepare 阶段改数据库，Commit 阶段删缓存。〔C:dachang/distributed_transaction/分布式事务模式.md:59〕

## 二、三大异常与 barrier：补偿机制的地基

SAGA 的补偿动作会遇到三类异常，这也是一切补偿方案的公共考点：〔C:dachang/distributed_transaction/exception/npc.md:11-15〕

- **空补偿**：补偿比分支操作先到（操作还没执行就收到了补偿），此时补偿应该什么都不做，直接返回成功。
- **悬挂**：分支操作在补偿之后才到达，此时分支操作应该拒绝执行，否则资源永远悬着。
- **幂等**：任何请求都可能被网络重试，所有分支操作必须幂等。

前两个异常来自 NPC：网络延迟让两个请求以任意顺序到达；进程暂停让"先发"的请求后执行。

DTM 的解法是**子事务屏障**（barrier）：在本地数据库建一张表，唯一键是 `gid-branchid-op`（全局事务 ID、分支 ID、操作类型 action 或 compensate），用 `insert ignore` 判断是否第一次到达。四步：〔C:dachang/distributed_transaction/exception/npc.md:17-29〕

1. 开启本地事务。
2. 对当前操作 `op`，`insert ignore` 一条 `gid-branchid-op`；影响 0 行说明是重复请求，直接提交返回成功。这一步管**幂等**。
3. 如果当前是 compensate，再 `insert ignore` 一条 `gid-branchid-action`；插入成功说明 action 从没执行过，这是空补偿，直接返回成功，占位记录留着，迟到的 action 会在第 2 步被拦。这一步同时管**空补偿**和**防悬挂**。
4. 执行业务，成功提交，失败回滚（barrier 记录一起撤销，下次重试可重新执行）。

```go
package main

import (
	"errors"
	"fmt"
)

// Barrier 子事务屏障的内存版：rows 模拟带唯一键 gid-branchid-op
// 的表，insert 冲突返回 false（生产里用 insert ignore 加 RowsAffected）。
type Barrier struct{ rows map[string]bool }

func (b *Barrier) insert(key string) bool {
	if b.rows[key] {
		return false
	}
	b.rows[key] = true
	return true
}

// Call 资料里的四步：判重（幂等）、空补偿占位（防悬挂）、执行业务。
func (b *Barrier) Call(gid, branch, op string, biz func() error) error {
	if !b.insert(gid + "-" + branch + "-" + op) { // 第 2 步：重复请求
		fmt.Println("  幂等拦截，直接成功")
		return nil
	}
	if op == "compensate" && b.insert(gid+"-"+branch+"-action") { // 第 3 步：空补偿占位
		fmt.Println("  空补偿，占位防悬挂")
		return nil
	}
	if err := biz(); err != nil { // 第 4 步：业务失败，记录一起回滚
		delete(b.rows, gid+"-"+branch+"-"+op)
		return err
	}
	return nil
}

func main() {
	balance := 100
	take := func() error { balance -= 10; fmt.Println("  action 执行，扣 10"); return nil }
	give := func() error { balance += 10; fmt.Println("  compensate 执行，加 10"); return nil }

	// 幂等：同一笔分支重复到达，第二次被拦。
	b := &Barrier{map[string]bool{}}
	_ = b.Call("g1", "out", "action", take)
	_ = b.Call("g1", "out", "action", take)
	fmt.Println("幂等后余额:", balance)

	// 业务失败：barrier 记录回滚，之后重试可以真正执行。
	bb := &Barrier{map[string]bool{}}
	err := bb.Call("g3", "out", "action", func() error {
		fmt.Println("  action 第一次执行，业务失败")
		return errors.New("余额不足")
	})
	_ = bb.Call("g3", "out", "action", take)
	fmt.Println("失败重试:", err, "余额:", balance)

	// 悬挂：补偿先到，迟到的 action 被占位记录拦住。
	b2 := &Barrier{map[string]bool{}}
	_ = b2.Call("g2", "out", "compensate", give)
	_ = b2.Call("g2", "out", "action", take)
	fmt.Println("悬挂后余额:", balance)
}
```

```text
  action 执行，扣 10
  幂等拦截，直接成功
幂等后余额: 90
  action 第一次执行，业务失败
  action 执行，扣 10
失败重试: 余额不足 余额: 80
  空补偿，占位防悬挂
  幂等拦截，直接成功
悬挂后余额: 80
```

### SAGA 串起来：失败后逆序补偿，失败的步骤本身也要补偿

有了 barrier，SAGA 就是一层很薄的编排：顺序执行各分支，第 i 步失败时从第 i 步往回逐步补偿。两个细节：**补偿的起点是失败的那一步本身**（它可能已经部分执行）；**补偿本身也要过 barrier**（补偿也会重复、也会迟到）。

```go
package main

import (
	"errors"
	"fmt"
	"strings"
)

// RunSaga 顺序执行各分支；第 i 步失败时，从第 i 步往回逐步补偿。
// call 就是上一节的 barrier.Call；补偿的起点是失败的那一步本身。
func RunSaga(call func(branch, op string, biz func() error) error, steps []Step) error {
	for i, s := range steps {
		if err := call(s.Name, "action", s.Action); err != nil {
			for j := i; j >= 0; j-- {
				for call(steps[j].Name, "compensate", steps[j].Compensate) != nil {
					// 真实系统里：重试，超阈值告警转人工
				}
			}
			return err
		}
	}
	return nil
}

// Step 一条分支：正向操作加它自己的补偿。
type Step struct {
	Name       string
	Action     func() error
	Compensate func() error
}

func main() {
	acct := map[string]int{"A": 100, "B": 0, "C": 0}
	var history []string
	move := func(name string, d int, tag string) {
		acct[name] += d
		history = append(history, fmt.Sprintf("%s%+d%s", name, d, tag))
	}
	step := func(name string, d int, fail bool) Step {
		return Step{name,
			func() error {
				if fail {
					return errors.New(name + " 转入失败")
				}
				move(name, d, "")
				return nil
			},
			func() error { move(name, -d, "(补偿)"); return nil }}
	}
	call := func(branch, op string, biz func() error) error { return biz() } // 幂等见上一节 barrier
	fmt.Println("成功路径:", RunSaga(call, []Step{step("A", -30, false), step("B", 30, false)}))
	fmt.Printf("  账本 A=%d B=%d\n", acct["A"], acct["B"])
	fmt.Println("失败路径:", RunSaga(call, []Step{step("A", -30, false), step("B", 30, false), step("C", 30, true)}))
	fmt.Printf("  账本 A=%d B=%d C=%d\n", acct["A"], acct["B"], acct["C"])
	fmt.Println("  操作序列:", strings.Join(history, "; "))
}
```

```text
成功路径: <nil>
  账本 A=70 B=30
失败路径: C 转入失败
  账本 A=70 B=30 C=-30
  操作序列: A-30; B+30; A-30; B+30; C-30(补偿); B-30(补偿); A+30(补偿)
```

读失败路径的操作序列：A 扣 30、B 加 30，C 失败。补偿从 C 开始逆序执行，B、A 依次退回，A、B 回到失败路径开始前的 70 和 30，中间那段 A=40、B=60 的状态对外是可见的，这就是 SAGA"无隔离性"的含义。注意最后的 `C=-30`：这个示例里的 `call` 直接执行 `biz`，没有套上一节的 barrier，C 本来就没转入成功，它的补偿却照样执行，把 C 扣成了负数，这正是**空补偿**要拦的情形。
换成 barrier 后，C 的 action 失败时事务回滚、不留记录，补偿到来时 barrier 发现 action 从没执行过，直接返回成功，C 保持 0。资料里完整的 SAGA 把调度交给 dtm：`NewSaga(...).Add(action, compensate, req).Submit()`。〔C:dachang/distributed_transaction/saga/ap/main.go:140-145〕

最后一个工程细节：分支里的"余额不能为负"要靠 SQL 原子条件，如 `update ... set balance=balance+? where user_id=? and balance+?>=0`，影响 0 行即余额不足或用户不存在；先 `select` 再 `update` 有并发窗口，两个请求会双双扣成负数。〔C:dachang/distributed_transaction/dao/bank_account.go:36-48〕

## 三、本地消息表：不需要回滚时的首选

二阶段消息最常用的自建形态是 **outbox**（本地消息表）：业务数据和"待发送消息"在**同一个本地事务**里写入，再由中继程序异步投递。先看错误答案：**先发消息再写库**，写库失败，下游拿着不存在的订单；**先写库再发消息**，进程 crash，消息永远没发出去。outbox 让"写业务数据"和"记待发消息"原子化，发消息变成可重试的后台动作：

```go
package main

import "fmt"

// Relay 中继程序：不断把未发送的消息投出去，成功才标记。
// 崩溃在"投递成功、标记之前"，重启后会再投一遍：至少一次。
func relay(outbox []string, sent []bool, publish func(string) error) {
	for i := range outbox {
		if sent[i] {
			continue
		}
		if err := publish(outbox[i]); err != nil {
			continue // broker 挂着：留着，下轮再发
		}
		sent[i] = true
	}
}

func main() {
	outbox := []string{"order_created:42"}
	sent := []bool{false}
	consumer := map[string]int{} // 消费端按消息 ID 去重
	handle := func(id string) {
		if _, ok := consumer[id]; ok {
			consumer[id]++ // 重复投递：只计数，不重复入账
			return
		}
		consumer[id] = 1
		fmt.Println("  消费:", id)
	}

	relay(outbox, sent, func(string) error { return fmt.Errorf("连接拒绝") }) // broker 挂着
	fmt.Println("broker 挂着: 1 已发送:", sent[0])

	relay(outbox, sent, func(id string) error { handle(id); return nil })
	fmt.Println("恢复后:", outbox[0], "已发送:", sent[0])

	sent[0] = false // 模拟"投递成功但标记前崩溃"：重启后再投一遍
	relay(outbox, sent, func(id string) error { handle(id); return nil })
	fmt.Println("重复投递后消费计数:", outbox[0], "=", consumer[outbox[0]])
}
```

```text
broker 挂着: 1 已发送: false
  消费: order_created:42
恢复后: order_created:42 已发送: true
重复投递后消费计数: order_created:42 = 2
```

三段输出走全了 outbox 的生命周期：broker 挂着时消息留在表里，恢复后补发；"投递成功但标记前崩溃"的时序导致重复投递，消费计数变成 2。完整契约两句话：**发送方至少一次，消费端必须幂等**（第 13 节的幂等四种做法在这里是最大应用场景）。另三条消息经验：**发送失败**就重试加监控加告警，再配异步补偿；〔W15-2 p12〕此时本地数据库已经记录了结果，是否先把用户请求成功返回，看业务取舍。〔自〕**有序性**靠生产者配哈希分区器，同一业务 key 进同一分区；〔W15-2 p13〕**消费幂等**上，消费端按业务交易号反解订单只做状态更新。〔W15-2 p14〕

## 四、支付与对账：和第三方打交道的两种未知结果

前面的模式都假设"对方是你的服务"。第三方支付不一样：**真正的支付发生在第三方平台上**。〔W15-1 p5〕

**谁参与，支付在哪完成**：打赏支付涉及四个关键方，读者负责发起支付；webook 作为枢纽，负责记录打赏并调用第三方接口发起支付；第三方支付是真正完成支付的地方；创作者最终收到打赏金额。〔W15-1 p4〕用户扫码时绕开 webook 直接和第三方支付打交道，类似于微信扫码登录，第三方再通过回调通知 webook；所以 webook 靠回调得知支付结果，拿到结果后做两件事：记录结果并更新系统状态。〔W15-1 p5〕正常完成路径共六步：读者点击打赏；webook 创建一个支付订单；webook 跳转到第三方支付或者展示一个二维码；读者扫码支付；第三方支付发送回调给 webook；webook 得知支付结果，记录结果并更新系统状态。〔W15-1 p5〕

**明面一个功能，内部两个模块**：不加思考容易把整个打赏做成一个模块，但它类似于验证码登录，明面是一个功能，内部可进一步拆分。〔W15-1 p6〕拆出两个模块：支付本身，负责和不同的第三方支付打交道，有些会成为支付网关；打赏模块，利用支付模块来实现。大型应用里可能还有一个账号模块，负责记录每个账号里的金额变动，以及个人用户的支付信息、收款信息；实践中还可能有反洗钱、税务等安全审计模块。课程只引入核心的支付和打赏两个模块，并做成独立的两个微服务。〔W15-1 p6〕

**渠道不抽象**：国内第三方支付最主要的是微信支付和支付宝，还有华为支付、银联支付、某些银行自己的支付接口，很多公司把这些不同的第三方支付称为"渠道"。〔W15-1 p7〕要不要定义一个统一接口，由支付宝、微信各自实现，每多接入一种渠道就多一种实现？答案是不需要。〔W15-1 p8〕理由：支付渠道和短信服务商不同的地方在于，所有支付场景都明确要求用户选择一个支付方式；用户在前端选了支付方式后，后续所有环节都知道用户选择了什么，因此不构建统一抽象也没有什么问题。〔W15-1 p8〕短信服务商没有"用户必选"这个环节，可以在服务端轮换，才需要抽象接口，差别就在选择方在谁手里。〔W15-1 p7-8〕把这个判断抽成一条可复用的规则：要不要为某类外部依赖抽统一接口，先看"用哪个"由谁决定、这个决定如何传到后续环节。选择方在用户前端、选择结果随请求全链路传递的（支付），抽象几乎没有收益；选择方在服务端内部、还要在服务商之间轮换的（短信），才值得用接口把切换藏起来。抽象本身有代价：每多接入一种渠道就多一种实现，统一接口还要求各渠道字段能对齐，在"用户已经选好渠道"的前提下，这些代价换不来对应收益。〔W15-1 p7-8〕〔自〕

**native 扫码支付：与微信的调用点共三处**：微信支付里有很多种支付方式，课程采用 native 方式，直观来说就是扫码支付，实现简单，原理清晰。〔W15-1 p12〕对照微信提供的标准流程序列图，需要调用微信的地方有三处：步骤 2，请求下单接口创建订单，也就是说我们说的支付实际上是在微信那边创建了一个订单；步骤 10，微信会发过来一个回调，需要处理这个回调；步骤 11 是可选的，可以考虑主动调用查询接口判定是否支付成功。〔W15-1 p12〕换句话说，我们自己的系统只负责创建订单、接收回调、必要时主动查询。〔W15-1 p5〕〔W15-1 p12〕后文按主交互口径说与微信支付的交互"主要有两个"，指的就是步骤 2 与步骤 10 这两处，每个都对应一种"未知结果"；这与三处不矛盾，第三处是可选动作，不是每次支付都发生的主交互：〔W15-1 p29〕

1. **Prepay 超时**：成败未知。你不知道订单在微信那边建了没有。
2. **回调处理失败**：支付与否未知。微信通知过你，但你的处理失败了。

原课对步骤 11 只说明它可选、目的是判定是否支付成功；把它接到前两种未知结果上（回调迟迟不到或处理失败时，拿交易号主动查一次微信，把"未知"变成"已知"）是本节的教学衔接，后文对账兜底用的正是这个查询。

**接入微信支付的材料**：先以企业名义创建一个小程序（公众号也可以），再以企业名义创建一个商户号并申请开通微信支付，最后把商户号和小程序关联起来；材料里的示意图是企业下多个 app 与商户号的关系。〔W15-1 p11〕

**微信那一侧的预下单**：整个支付是"我们在微信里创建一个订单，而后用户支付了这个订单"，所以第一步就是创建订单，对应微信 API `https://api.mch.weixin.qq.com/v3/pay/transactions/native`。请求有四个必传的关键参数：`mchid`（商户号 ID）、`out_trade_no`（接入方自己的交易号/订单号）、`appid`（小程序或公众号 ID，必须已绑定到该商户号）、`notify_url`（回调地址，微信把支付结果通过这个接口通知你）。调用成功后响应里含 `code_url` 字段，即二维码对应的地址（形如 `weixin://wxpay/bizpayurl/...`），正常由前端把 `code_url` 转成二维码展示在页面上，用户扫码即可支付；支付完成后，下单时传过去的 notify_url 就会收到微信的回调。〔W15-1 p13-14〕

**Prepay 先落一条支付记录，目的就是本地去重**：NativePaymentService 的 Prepay 一上来先初始化一条支付记录，在本地挡住重复请求；即便省掉本地去重，BizTradeNO 也会透传给微信作为 out_trade_no，微信同样按它去重。这套极简设计的前提是当前只支持打赏，几乎没有商品信息和结算信息，能传给微信的数据很少。〔W15-1 p18〕

**Prepay 的幂等键是 out_trade_no，重试是"把同一笔再问一遍"**：原课讨论的重试全部是调用方（webook/payment 服务这一侧）自己重发 Prepay 请求，微信不会替你重发下单〔自〕。微信按请求里的 `out_trade_no` 认单，同一个交易号在微信侧只对应一笔单：即便我们不去重，微信侧也会按它去重。〔W15-1 p17-18〕所以重试不是"再建一笔"，而是"把同一笔再问一遍"：第一次没到微信，这次就是全新创建；第一次已到微信，这次是重复请求，应答规则见下一段。反方向的约定同样成立：每个业务动作要生成新的交易号（打赏侧用 `fmt.Sprintf("reward-%d", rid)` 拼），对同一个业务方，要支付两次就必须想办法生成两次 BizTradeNO，否则第二次业务动作创建不出第二笔支付单。〔W15-1 p17〕

**notify_url 与回调分发：本地调试的前提**：用户扫码支付成功后，微信会把回调发到下单时传入的 notify_url；这个地址通常来说是线上地址，开发环境或测试环境收不到这个 HTTP 请求，所以要考虑转发请求。〔W15-1 p20〕最简单的做法有两种：一是配置多个回调域名，特定域名打到特定的环境；二是在回调路径上做特殊标记，比如以 `/test` 开头就打到测试环境。〔W15-1 p20〕分发可以利用 nginx，也可以自己写一个轻量级 HTTP 接口，自写时可以设计比较灵活的转发机制，课程采用借助 nginx 转发。〔W15-1 p20-21〕三个内部域名的分工：test.yourcompany.com 转发到测试环境；dev.yourcompany.com 转发到某一个地方，这个地方要能把这个请求进一步转发到你的电脑上；live.yourcompany.com 转发到生产环境。〔W15-1 p21〕dev 这层二次转发就是本地调试支付功能的前提：notify_url 在下单时就定死了，如果只把回调域名指向线上环境、没有 dev 域名这层能转回本机的链路，本地就收不到微信的回调 HTTP 请求，回调之后的验签、状态更新、事件通知全部无从调试；原课的结论是"不然你本地调试会非常麻烦"。〔W15-1 p20-21〕

**支付服务对外暴露的 Prepay 是另一层接口，采用最小化设计**：既然目前只支持微信，就根据微信接口只设计必要的字段。逐一分析微信的必传参数后可以发现：`appid`、`mchid` 是"微信的东西"，调用者当然不知道，不该让它传（唯一例外是公司有很多 app 和商户号时，才可能需要调用方传递 `appid` 或 `mchid`）；`notify_url` 本身是支付（服务）的东西，同样不应由调用者传递。最终结论：支付服务的 Prepay 只让外部传 `description`、`out_trade_no`、`amount` 三个字段。将来接支付宝若需要更多字段，再定义 WePayment/AliPayment，共同字段组合进 basePayment 这类公共结构体；返回值暂不通用化，等真要接入别的第三方再调整。〔W15-1 p15-16〕

**Prepay 超时，重试是唯一出路**，重试由调用方发起，能成立靠的是微信接口的幂等设计：〔W15-1 p30〕第一次没建成（微信根本没收到请求），重试就是一次新创建，成功并拿到最需要的 `code_url`；第一次已建成，重试拿到的是代表 `OutTradeNO` 重复的错误，但响应里 `code_url` 照样返回。为什么重复了还能拿到 `code_url`：重复请求命中"这笔单已经建过"，微信把第一次调用已经产生的结果再返回一次。原文的结论句是：**"在所有的幂等接口里面，都应该尽量返回之前调用的结果。"**〔W15-1 p30〕对自研接口，这就是要复刻的语义：重复请求不应该以报错收场，而是把第一次的结果还回去，拿到重复错误的调用方业务流程照样继续。〔W15-1 p30〕

```go
package main

import (
	"errors"
	"fmt"
	"strings"
)

// prepay 微信侧的内存版：outTradeNo 全局唯一，重复调用返回第一次的结果。
func prepay(orders map[string]string, outTradeNo string) (string, error) {
	if url, ok := orders[outTradeNo]; ok {
		return url, errors.New("OUT_TRADE_NO_EXISTS") // 订单已存在，但 code_url 照给
	}
	orders[outTradeNo] = "weixin://wxpay/bizpayurl?pr=42"
	return orders[outTradeNo], nil
}

func main() {
	orders := map[string]string{}
	_, err := func() (string, error) { return "", errors.New("context deadline exceeded") }() // 第一次调用超时：成败未知
	fmt.Println("第一次:", err)

	url, err := prepay(orders, "reward-1001") // 幂等重试：即使第一次已建成，也拿到同一地址
	fmt.Println("重试:", err, url)

	url2, err2 := prepay(orders, "reward-1001") // 再来一次：订单号重复
	fmt.Println("再试:", err2, strings.HasPrefix(url2, "weixin://"))
}
```

```text
第一次: context deadline exceeded
重试: <nil> weixin://wxpay/bizpayurl?pr=42
再试: OUT_TRADE_NO_EXISTS true
```

还有一个配套设计：用户点了打赏又放弃，再点一次时要**复用缓存的二维码或生成新的交易号**，否则同一个业务动作会创建多笔支付单。〔W15-1 p17〕两种做法的根据都在交易号本身：`out_trade_no` 是业务方透传给微信的唯一交易号，Payment 结构体里对应的 `BizTradeNO` 是建了唯一索引、表达"这一次支付"的唯一凭证，靠它做去重；对一个业务方来说，要支付两次就必须想办法生成两次 BizTradeNO。NativePaymentService.Prepay 上来就初始化一条支付记录用于去重；即便不去重也没问题，BizTradeNO 透传给了微信，微信侧也会按它去重。〔W15-1 p17-18〕打赏服务两条路都备了：PreReward 先查缓存的二维码，命中说明用户上次点了打赏又放弃，直接复用，省一次支付服务调用（这步是纯优化，不缓存也行）；未命中就创建打赏记录（Status=Init），调 NativePrePay 时 BizTradeNo 用 `fmt.Sprintf("reward-%d", rid)` 拼出来，即每次新建打赏记录都得到一个新的唯一交易号，最后缓存返回的二维码。〔W15-2 p8-9〕

**回调到达后怎么处理**：手动验证签名、手动解密回调数据有一大堆坑，处理支付通知借助微信的 NotifyHandler 之后非常简单，验签解密交给 SDK。资料在 Web 这一层就把微信的支付状态转成内部状态，作者自评这"只能算不太坏的做法"，下沉到 NativeService 里做也可以；一个让设计更顺的做法是把 WechatHandler 改名为 WechatNativeHandler，那么在这一层转换微信状态就名正言顺。HandleCallback 里面做的事情就是记录微信返回的 TxnID 和 Status 两个字段。〔W15-1 p22〕环境上有个前提：回调只会打到 notify_url 这个（通常是线上的）地址，开发/测试环境收不到，需要 nginx 按域名（test/dev/live.yourcompany.com）或 `/test` 路径前缀分发转发，dev 环境还要经一个能再转发到你本机的跳板。〔W15-1 p20-21〕

**支付模块处理完回调，还要通知业务方**：业务方也需要知道支付结果，所以引入 Kafka，Payment 处理完微信回调后生产一条支付事件，各业务方调用支付之后自行选择监听 Kafka 里的回调事件。updateByTxn 的步骤顺序是：先把微信 TradeState 映射为内部状态（未知状态返回错误）→ repo.UpdatePayment 更新本地支付记录 → 再 producer.ProducePaymentEvent 发事件（带 BizTradeNO 和 Status）。〔W15-2 p11-12〕关键点是"处理结果成功了但发送消息失败了怎么办"：发送失败分支只打日志（"发送支付事件失败"，含 biz_trade_no）然后 return nil。取舍核心是先让事实落库，updateByTxn 已把支付终态写入本地 DB，Kafka 消息只是对外的通知手段，不是事实记录本身；若因通知失败而报错，微信会因非 200 响应重试整个回调流程。〔W15-2 p12〕〔W15-1 p31〕遗漏的消息靠两类手段补救：重试+监控+告警发现问题，再加异步补偿:扫描 DB 里已到终态但消息未确认发出的记录补发，异步补偿是资料列为后续作业的设计点。这个设计成立还依赖一个前提：消费方 Reward 按 BizTradeNO 反解出 rid 只做状态更新，更新是幂等的，所以补偿导致的消息重复不会造成数据不一致。〔W15-2 p14〕

**用户怎么最终得知支付结果：前端轮询**。用户支付的时候只跟微信打交道，微信再异步地给系统发送支付通知，所以用户查询结果时结果未必已就绪，查询那一刻只可能处于三种情况之一：没收到回调；收到了回调但处理失败了；收到了回调且处理成功。方案是让用户在支付成功后轮询查询支付结果，由前端控制：查询失败了就立刻开始下一次查询；如果轮询了一定的时间或者一定的次数都还没查到最终结果，就返回错误（原课未规定具体数值）。〔W15-2 p10〕后端为轮询暴露查询打赏结果接口，Web 层把 uid 一起传给 Reward 服务，让 Reward 校验 rid 归属，打赏的人和查询的人必须是同一个人，防止交易号被枚举后越权看别人的打赏记录。〔W15-2 p15〕服务实现分快慢路径：快路径按 rid 查本地 reward，已完结（Completed()）直接返回；本地还是未知支付结果时（Reward 的消费者还没收到 Kafka 消息、或正在处理中）走慢路径，调 Payment 服务 GetPayment（用 bizTradeNo 查），把 PaymentStatus（Failed/Init/Success/Refund）翻译成对应 RewardStatus，repo.UpdateStatus 回填本地后返回；慢路径里 GetPayment 失败或回填 UpdateStatus 失败都只打日志（含 rid）不向上抛错，直接返回本地 DB 查到的数据；触发降级时按降级理论在这个位置不去查询 Payment，即关掉慢路径，保住 Reward 服务本身。〔W15-2 p16〕

**回调失败，靠微信重试加对账兜底**。微信考虑到我们可能有处理回调失败的情况，因此回调本身就会重试，规则是两条：处理回调时没有返回 200 的响应，微信就会再次调用你；达到重试次数之后，微信就不会再调用了。〔W15-1 p31〕但资料明确说，仅仅依赖这个机制来保证处理回调一定会成功是不靠谱的：微信一停止重试，就再也不会主动通知你这笔单的结果，本地永远停在处理失败当时的状态，没有自动机制会把结果补进来〔自〕。兜底是**对账**：利用 `BizTradeNO` 主动查询微信上面你的订单状态，而后更新本地的信息。〔W15-1 p31〕

**把重试的层次摆齐，再看对账的两层兜底**：第一层，Prepay 超时由调用方（webook/payment 服务）重试，幂等键是 `out_trade_no`；第二层，回调处理失败由微信重试，触发条件是非 200，达到次数就停，不可全信；第三层是对账，由我们自己发起，是最终兜底，分定时与及时两层。〔W15-1 p29-33〕微信的自动重试是"别人替你重试"，有次数上限；对账是"你主动去查"，不依赖对方的心情。

- **定时对账**：借助 cronjob 触发对账定时任务，逻辑是找到超时的订单、同步订单状态。这里的超时是指 Prepay 的时候设置了订单 30 分钟超时，因此可以预期扫出来的就是没走完的超时单；担忧时间精度和并发问题的话，可以找 31 分钟之前的订单（一分钟偏移是原课给的做法，用意是避开 30 分钟边界上的误判〔自〕）。也可以进一步借助分布式锁让这个任务全局唯一，但原课判断没特别大的必要。〔W15-1 p32〕
- **及时对账**：定时对账最坏要等一个扫描周期，更及时的做法是处理回调失败的时候立刻触发一次对账，并且只比对出故障的这一条；发起方式两种都可以：直接调用一个对账接口，或者发消息到 Kafka，后续再去对账。〔W15-1 p33〕

**cronjob 对账示例（静态示意，未在本机实测）**：这个例子的逻辑就是扫出 Prepay 超时（30 分钟）的订单，逐条调对账同步订单状态；如果担心时间精度和并发问题，把查询条件改成 31 分钟之前的订单。代码例子只做静态阅读，没有运行输出。〔W15-1 p32〕

**支付系统的面试问法**：把这一节压成五个问题：一是微信支付流程，重点是 Prepay 和处理回调；二是支付回调怎么处理，最关键的是怎么分发/区别不同环境收到的回调；三是金额怎么存，能讲三种方案、各自优缺点和自己的偏好；四是怎么防止重复支付，核心是业务方提供唯一凭证并用它去重；五是微信支付回调处理失败怎么办。〔W15-1 p35〕

两层对账的动作是同一个：拿 `BizTradeNO` 查微信订单状态，按查询结果更新本地。下面的教学示例把三种查询结果都演了：第三方无此单（用户没付过，保持现状）、状态一致（不动）、有差异（修成微信的状态）。对账接口的具体 URL、扫描周期的具体数值原课未规定，只给了上述触发条件。〔W15-1 p31〕

```go
package main

import "fmt"

// reconcile 对账：拿业务交易号去第三方查询，按查询结果修本地状态。
// 兜底的是回调丢失、回调处理失败、预下单超时这三种未知结果。
func reconcile(no, status, txn *string, query func(string) (string, string)) string {
	t, s := query(*no)
	if t == "" {
		return "第三方无此单，保持 " + *status
	}
	if *status == s && *txn == t {
		return "一致，不动"
	}
	*status, *txn = s, t
	return "修复为 " + s
}

func main() {
	third := map[string][2]string{
		"reward-1001": {"tx-888", "success"}, // 用户付了，回调丢了
	}
	query := func(no string) (string, string) {
		r := third[no]
		return r[0], r[1]
	}
	no1, st1, txn1 := "reward-1001", "init", ""
	fmt.Println("超时单:", reconcile(&no1, &st1, &txn1, query), fmt.Sprintf("txn=%s", txn1))

	no2, st2, txn2 := "reward-1002", "init", "" // 从没付过
	fmt.Println("未付单:", reconcile(&no2, &st2, &txn2, query))

	fmt.Println("已付单:", reconcile(&no1, &st1, &txn1, query))
}
```

```text
超时单: 修复为 success txn=tx-888
未付单: 第三方无此单，保持 init
已付单: 一致，不动
```

### 金额怎么存：三种选法，float 不在列

资料列了三种存法，各有适用面：〔W15-1 p25〕

- `int64`：存最小面额（分），运算最直接；银行类场景要记到元后四位小数，也可以用 `int64` 表达。
- `decimal`：精确十进制数，要对金额做复杂数学运算时考虑；Go 没有内建，常用 `shopspring/decimal`。
- `string`：最不容易出错，但一运算就要转换，只适合纯存储和传输。资料的结论是三种在实践里都行，各有优缺点，本节取 `int64` 作默认。

**选型落点：默认 `int64`、单位为分**（本节取 int64 作默认是对"三种皆可"的归纳取舍，原课原文没有钦定唯一答案）。金额全程用最小面额的整数计数，不存在二进制舍入，不会出现下面 float 实测的对不上账；"元"只是展示层除以 100 的格式化结果。银行类机构要记到元后四位小数，依旧可以用 `int64` 表达，做法是把最小面额取到万分之一元（原课只说"依旧可以用 int64 来表达"，没有为这个场景另立单位名〔自〕）。什么时候才升 `decimal`：出现要求精度、要对金额做多步运算的场景，原课点名的触发条件是"对金额进行复杂的数学运算"（教学举例：跨币种换算、按比例分摊）。什么时候考虑 `string`：这份数据只做网络传输或存储、不参与运算；代价是一旦参与数学运算就必须先转成其他类型，多一道转换就多一个出错点。原课的收尾：三种方式在实践中用起来都没问题、各有优缺点，公司里没决定权就听老板的，有决定权随便选一个效果都差不多。〔W15-1 p25〕

**float64 不在任何一列里**，实测：

```go
package main

import "fmt"

func main() {
	a, b, c := 0.1, 0.2, 0.3
	fmt.Println(a+b == c)
	fmt.Printf("%.20f %.20f %.20f\n", a, b, c)
	fmt.Printf("a+b-c = %g\n", a+b-c)
}
```

```text
false
0.10000000000000000555 0.20000000000000001110 0.29999999999999998890
a+b-c = 5.551115123125783e-17
```

`0.1 + 0.2 == 0.3` 是 `false`：三个数在二进制里都是无限循环小数，各自已经被舍入过；`a+b` 的结果比字面量 `0.3` 对应的 double 大一个最小精度单位，差 `5.6e-17`。Java 有 `BigDecimal` 救场，Go 的答案是把金额变成整数分，从类型上消灭小数。

### 部分失败清单：打赏流程逐环节过一遍

开头那笔"永远对不上的账"是打赏 1 块钱的链路：打赏模块记打赏、支付服务记支付、账号服务给作者加钱，三个环节三个失败域。把这条流程走全，每个环节都标注"失败后系统处于什么状态、怎么发现、怎么修"：〔W15-2 p28〕

| 环节 | 失败状态 | 发现 | 修复 |
|---|---|---|---|
| Prepay 超时 | 微信侧可能已建单 | 调用方拿到超时错误 | 幂等重试，重复建单返回原结果〔W15-1 p30〕 |
| 支付回调丢失 | 本地永远 init | 定时对账任务扫超时单 | 用交易号查微信补状态〔W15-1 p31-32〕 |
| 记账消息丢失 | 支付成功账户没加钱 | 发送侧重试加告警〔W15-2 p12〕 | outbox 或异步补偿 |
| 记账消息重复 | 账户加两遍 | 消费端无感知，靠设计 | 幂等：按交易号去重〔W15-2 p14〕 |
| 记账与流水分离 | 余额和流水对不上 | 无法单事务时对账 | 同库用本地事务〔W15-2 p24〕；分库后事务失效，靠补偿加对账〔自〕 |
| 三方各自对 | 两两一致但业务错 | 三者对账 | 用业务交易号串起打赏、支付、账号流水，写差异检测与修复〔W15-2 p32〕 |

表里前五行对应前面讲的机制；最后一行是三者对账，"谁来分账"资料认为没有显著优劣，选一个保持一致即可。〔W15-2 p20〕

## 五、共识：Raft 的概念地图

前面四部分都在"业务层"补偿，框架替你重试。更底层有一类问题没法靠业务补偿：**多个副本对同一份数据的顺序达成一致**，这就是共识，etcd、注册中心、很多分布式存储都建立在 Raft 之上。Raft 的武器是**任期**（term，单调递增的逻辑时钟）加**多数派**（quorum，过半节点），把问题拆成三件事：〔C:dachang/raft/util.go:10-17〕〔C:dachang/raft/server.go:163〕

1. **选主**：三种角色 Follower、Candidate、Leader。Follower 在随机超时内没收到心跳就变成 Candidate 发起投票；超时随机化（1 到 2 倍范围）防止多个节点同时发起选举，随机超时让每个节点"抢跑"的机会均等：

```go
package main

import (
	"fmt"
	"math/rand/v2"
	"time"
)

// randomTimeout 资料里的写法：timeout 到 2 倍 timeout 之间的随机值。
func randomTimeout(timeout time.Duration) <-chan time.Time {
	delta := time.Duration(rand.Int64N(int64(timeout)))
	return time.After(timeout + delta)
}

func main() {
	base := 150 * time.Millisecond
	wins := make([]int, 5) // 10000 轮虚拟选举：超时短者抢跑获胜
	for range 10000 {
		best, idx := time.Duration(1<<62-1), 0
		for i := range 5 {
			if d := base + time.Duration(rand.Int64N(int64(base))); d < best {
				best, idx = d, i
			}
		}
		wins[idx]++
	}
	mean := 10000 / 5
	ok := true
	for _, w := range wins {
		if abs(w-mean) > mean/5 { // 每个节点都该在均值正负 20% 内
			ok = false
		}
	}
	fmt.Println("5 个节点各赢约", mean, "次，全部落在均值正负 20% 内:", ok)
}

func abs(x int) int { if x < 0 { return -x }; return x }
```

```text
5 个节点各赢约 2000 次，全部落在均值正负 20% 内: true
```

2. **日志复制**：Leader 把日志发给 Follower；Follower 按前一条日志的下标和任期检查，对得上才追加，冲突的尾部截断；**已提交的日志不允许被覆盖**。〔C:dachang/raft/log.go:138-166〕这一条加上下面的提交规则，保证一旦日志被提交，就不会因为换主而丢失。

3. **提交**：Leader 发现某条日志已被**过半**节点复制，就提交并应用到状态机。这是简化说法：论文里还有一条限制，Leader 只靠"数副本"来提交**当前任期**的日志，旧任期的日志随新日志一起被间接提交，否则已复制到多数派的旧日志仍可能被覆盖（论文 Figure 8）。资料的实现没有做这个区分。〔自，待核〕

**为什么必须是多数派**：任意两个多数派至少有一个共同节点，所以投票时拒绝日志比自己旧的候选人，新 Leader 就必然见过所有已提交日志，这是 Raft 选主安全性的核心。资料实现里 `peers` 不含自己，`QuorumSize()` 返回 `(len(peers)+1)/2 + 1`，即总节点数的一半加一。〔C:dachang/raft/server.go:65-67〕

资料实现里有一个很巧也很险的细节：提交点不用逐个数，而是把所有节点已复制的最大日志下标升序排列，取"从高往低第 quorum 个"的位置。资料代码写的是 `indices[QuorumSize()-1]`，**奇数节点恰好正确，偶数节点会取高一位**，把还没过半的日志提前提交；正确写法是升序后取 `s[len(s)-quorum]`。〔C:dachang/raft/server.go:378-392〕用 4 节点和 7 节点各验一次：

```go
package main

import (
	"fmt"
	"slices"
)

// CommitIndex 计算已提交下标：所有节点已复制的最大下标升序排好，
// 取从高往低第 quorum 个（含）都到齐的位置；quorum = len(s)/2 + 1。
func CommitIndex(lastIdx []int) int {
	s := slices.Clone(lastIdx)
	slices.Sort(s)
	quorum := len(s)/2 + 1
	return s[len(s)-quorum]
}

func main() {
	// 7 节点进度 [5 3 5 4 2 3 5]：排序 [2 3 3 4 5 5 5]，quorum=4，提交点 4。
	fmt.Println(CommitIndex([]int{5, 3, 5, 4, 2, 3, 5}))

	// 资料写法 indices[QuorumSize()-1] 在偶数节点时取高一位：
	// 4 节点进度 [5 4 3 1]，quorum=3，正确提交点 3，取到 4。
	s := slices.Clone([]int{5, 4, 3, 1})
	slices.Sort(s)
	fmt.Println("正确:", CommitIndex([]int{5, 4, 3, 1}), "资料写法:", s[len(s)/2])
}
```

```text
4
正确: 3 资料写法: 4
```

参数上有一条硬约束：**心跳必须显著小于选举超时**。资料实现里心跳 20ms、选举超时 150ms，启动时检查 `2 * 心跳 >= 选举超时` 就直接 panic。〔C:dachang/raft/config.go:7-24〕违反的后果是 Follower 偶尔漏收一两次心跳就超时、频繁换主、任期飙升、集群空转。〔自〕2 倍只是资料的下限检查，Raft 论文建议广播耗时比选举超时小一个数量级。另一个实现选择是所有 RPC 投进同一个带缓冲 channel（容量 100），在角色循环里串行处理，避免两个 RPC 交错出中间状态。〔C:dachang/raft/rpc.go:159-164〕〔C:dachang/raft/server.go:171〕

对使用者最实用的推论：**强一致要付性能代价**。作者的基准（Windows 单机，Redis 3.0.504 对 etcd 3.5.9）里 Redis 读写都在 30 微秒量级，etcd 写 3.4 毫秒、读 186 微秒；写慢主要是落盘加共识流程，集群下还要加上多数派确认。〔C:dachang/etcd/etcd_redis_test.go:104-123〕所以 etcd 适合配置、元数据、服务发现这类低写高读的数据，不适合当业务数据库。选存储前先问：这份数据需要"多数派确认才算成功"吗？不需要，就别付这个钱。

## 小结

1. 一致性的根源是一次业务操作跨了多个失败域，每一步都要问：失败、超时、重复之后系统处于什么状态。〔W15-2 p28〕
2. 模式按需选：不回滚用二阶段消息，要回滚用 SAGA，高一致用 TCC，低并发用 XA；所有模式共享"最终成功"：重试到成功，超阈值告警。〔C:dachang/distributed_transaction/分布式事务模式.md:54-56〕〔C:dachang/distributed_transaction/exception/npc.md:15-20〕
3. 空补偿、悬挂、幂等三个异常用一张 barrier 表统一解决，核心是唯一键加 `insert ignore` 加 `RowsAffected`。〔C:dachang/distributed_transaction/exception/npc.md:22-28〕
4. SAGA 补偿从失败的步骤本身开始逆序执行，补偿也要幂等、也要防空防悬。〔C:dachang/distributed_transaction/exception/npc.md:9-13〕
5. 本地消息表让"写业务"和"记消息"原子化，发送方至少一次，消费端必须幂等；消息有序靠哈希分区。
6. 第三方交互只有两种未知结果：预下单超时（幂等重试解决）和回调失败（微信重试加对账兜底）；金额用 `int64` 最小面额，永远不用 float。〔W15-1 p30〕〔W15-1 p31〕〔W15-1 p25〕
7. 共识靠任期加多数派：随机选举超时防活锁，已提交日志不可覆盖，多数派交集保证新 Leader 见过全部已提交数据；强一致的性能代价是实打实的。

## 常见坑

1. **只写正常路径**：每一步都要问"失败、超时、重复"后是什么状态，答案落成检测和修复代码。〔W15-2 p28〕
2. **补偿不幂等、不重试**：补偿自己也会失败也会重复，必须重试到成功并配告警，否则等于没补偿。〔C:dachang/distributed_transaction/exception/npc.md:15-20〕
3. **没处理空补偿和悬挂**：网络乱序下会多扣或永久悬着；用 barrier 或同类机制，别裸写补偿。
4. **消息先发后写库，或先写库后发消息**：前者可能下游拿到不存在的数据，后者可能 crash 丢消息；用 outbox 让两者原子。
5. **完全信任第三方回调**：微信的重试到次数就停，必须用交易号主动对账兜底。〔W15-1 p31〕
6. **用 float 存金额**：`0.1+0.2 != 0.3`，本节示例实测 `false`；用 `int64` 最小面额。
7. **在选举超时附近设心跳**：Raft 会反复换主、任期飙升；保持心跳显著小于选举超时（资料 panic 检查的阈值是 2 倍）。〔C:dachang/raft/config.go:19-21〕
8. **先 select 再 update 做防负**：并发窗口里两个请求同时通过检查；防负要写进 SQL 条件，看影响行数。〔C:dachang/distributed_transaction/dao/bank_account.go:36-48〕

## 判断题与答案

1. 补偿先到、操作后到（悬挂），barrier 是怎么让后到的操作失效的？2. SAGA 失败后，为什么要补偿"失败的那一步"本身？3. outbox 为什么只能保证"至少一次"？消费端要做什么？4. 七节点的 Raft 集群最多容忍几个节点失败，为什么不是更多？

**答案**

1. 空补偿到达时先插入了 `gid-branchid-action` 占位记录；后到的 action 在判重步骤撞上这条记录，被当作重复请求直接返回，业务不会执行。〔C:dachang/distributed_transaction/exception/npc.md:26-28〕
2. 失败的那一步可能已经部分执行（本地已提交、响应没回来），不补偿它资源就悬着；补偿必须能处理空补偿。〔C:dachang/distributed_transaction/exception/npc.md:11〕
3. "投递成功"和"标记已发送"是两个动作，中间崩溃就无法区分，重试后消息会再来一遍；消费端按业务幂等键去重（唯一索引、状态机等），让重复无害。〔W15-2 p14〕
4. 3 个。quorum 是 `7/2+1=4`，挂 3 个还剩 4 个，正好凑齐多数派；挂 4 个就选不出主、也提交不了。"不是更多"来自多数派交集：两个 quorum（各 4 个）至少共享一个节点，所以不会同时存在两个各自独立提交的多数派，新 Leader 必然见过全部已提交日志。〔C:dachang/raft/server.go:65-67〕

## 在项目里

这里没有现成例子：`dnsproxi-go` 没有跨资源事务，也没有多副本状态。勉强能类比的只有 `dnsproxi-go/tcp/server.go` 里 `serveConn` 的"错误一律关连接"：`DecodeFrame` 出错就 `return`，把连接关掉让客户端重试，这是"失败后把重试交给对方"的最小形态，谈不上一致性协议。练习：把 `Barrier`、`Relay`（outbox）、`CommitIndex` 抄进 `exercises/` 新建的包，各写一个失败注入测试。

## 想深入：正文之外的干货

### W15-1 支付系统：对接第三方支付

- 模块划分：支付本身是对接第三方的支付网关，打赏是用支付实现的业务模块；大型应用再加账号模块管金额变动与收款信息，实践里还有反洗钱和税务审计。课程把打赏拆成 payment 加 reward 两个微服务。〔W15-1 p6〕
- 渠道要不要抽象：支付渠道（微信、支付宝、银联）不需要统一接口抽象，因为支付方式是用户在前面必选的，全链路都知道选了谁；短信服务商可以在服务端轮换，才需要抽象接口。这是"选择方在谁手里"的经典设计题。〔W15-1 p7-8〕
- 接口最小化：`mchid`、`appid`、`notify_url` 都是支付服务自己知道的配置，不该出现在 API 参数里；外部只传 description、out_trade_no、amount。提前为支付宝做通用化是过早抽象，将来字段变多再拆实现类组合复用。〔W15-1 p15-16〕
- native 支付只涉及三处代码：下单接口创建订单、处理回调、可选的主动查询；Prepay 参数里 `out_trade_no` 是我方业务交易号，`notify_url` 是回调地址，响应的 `code_url` 前端转成二维码。〔W15-1 p12-14〕
- 处理回调不要手动验签解密，坑多，直接用微信 SDK 的 NotifyHandler；Web 层负责把微信支付状态翻译成内部状态。〔W15-1 p22〕
- PreReward 的调用链：用户在文章页点打赏，Web 先按文章 ID 查文章信息，拿到作者和标题，再调 Reward 服务的 PreReward；Reward 内部完成建打赏记录与调 Payment 的 NativePrePay，返回 codeURL 和 rid，前端展示二维码。接口挂在 `ArticleHandler` 下，因为它就是“打赏文章”的接口。PreReward 的入参来自打赏这一业务动作：Biz（article）、BizId（文章 ID）、BizName（文章标题）、TargetUid（文章作者）、Uid（当前登录用户）、Amt（打赏金额）。返回体里 codeURL 给前端转二维码，rid 是打赏记录 ID，后续查打赏支付结果要用。〔W15-2 p6〕
- 课程只支持微信扫码一种支付方式，是刻意的假设；只支持一种方式，gRPC 接口和内部业务实现都能保持简单。多方式时用户选金额的同时选方式，Web 按方式调不同的 reward 接口，前面的幂等与对账设计不受影响（这句是本节推导）。〔W15-2 p7〕
- 账号服务的 Credit 接口一次请求带多笔账：`CreditRequest{biz, bizId, items[]}`，每条 `CreditItem{account, account_type, amt, currency, uid}`；一个业务拆几笔账要在接口里定义清楚，账号服务是对账、统计、报表的核心。〔W15-2 p21〕
- 本地调试收不到回调：notify_url 必须是公网地址，标准解法是 nginx 多域名分发，test/dev/live 各一个域名，dev 转发到能回到本机的跳板机。〔W15-1 p20-21〕
- PreReward 四步：查缓存二维码、建打赏记录（Status=Init，拿到 rid）、调 NativePrePay（BizTradeNo 用 `reward-` 拼 rid，Description 拼上被打赏文章标题）、缓存返回的二维码。缓存命中说明用户上次点了打赏又放弃，直接复用，少调一次支付服务；未命中才走创建记录和调支付这两步。最后缓存的是含 rid 与 URL 的 CodeURL 对象；这步是纯优化，不缓存也行，去掉缓存最少保留创建打赏记录、获得二维码两步。〔W15-2 p8-9〕
- 支付结果用 Kafka 广播：Payment 自己处理微信回调，业务方也要拿到支付结果，于是引入一个 Kafka；微信把结果通知 Payment，Payment 处理完回调、更新本地之后往 Kafka 发事件，不同业务方按需订阅。〔W15-2 p11〕Reward 消费后只更新本地 DB：`UpdateReward` 收到 BizTradeNO 和 status，先用 `toRid` 反解出 rid，再调 `repo.UpdateStatus(ctx, rid, status)` 更新状态；这个更新本身幂等，前提是发送方消息顺序没问题，重复消费最多重复更新。〔W15-2 p14〕
- 要不要统一支付接口：设计系统通常倾向于屏蔽不同实现方式之间的差异，但原文提醒要把握尺度，过犹不及。支付系统可以分成两部分：和具体支付方式有关的，以及所有支付方式都具有的。分辨方法是：一开始就明确知道具体支付方式的（如只支持微信扫码），属于和具体支付有关，不需要抽象，按方式直接写；不清楚具体支付方式的，才要抽象出来（例如查询支付结果）。审视现有代码时按这个尺度抽共性和个性。〔W15-2 p27〕
- 表设计从简：Payment 表只存必要字段，Id 主键自增，Amt、Currency、Description、BizTradeNO，以及 TxnID（`sql.NullString`，第三方支付平台的事务 ID，唯一）；业务方传来的数据业务方自己存了就别重复存，存了后面大概率也用不上，不到逼不得已不要多存数据。如果因为字段太少缺乏安全感，可以多存详细商品信息、结算信息，或者提供一个巨大的 BLOB 字段（如 `ExtraData`）存支付相关的其它字段。〔W15-1 p19〕
- 回调进 MQ 可行但不提早：收到微信回调、解析内容后转发到 Kafka 的设计是可行的。常见缘由有四个：别的部门也关心回调时可以去订阅；重试机制掌握在自己手里；DEBUG 容易，payment-web 只做轻量解密，遇到问题可以绕开它在 Kafka 重发或模拟消息；削峰只能算聊胜于无，回调 QPS 与正向支付一致，正向支付撑得住的流量回调一般也撑得住，秒杀之类场景才可能更好。作者的结论是不要提早引入消息队列。〔W15-1 p26〕

### W15-2 打赏功能：消息、查询与记账

- 用户怎么知道支付结果：支付时用户只跟微信交互，结果通知是异步的；前端轮询查询结果，失败立刻再查，超时或超次才报错。查询结果有三种可能：没收到回调、回调处理失败、成功。〔W15-2 p10〕
- 查询走快慢两条路：快路径查本地 reward 表，已完结直接返回；本地状态未知时走慢路径去查 Payment 服务，把状态转换后更新本地打赏状态，再返回数据库里的数据。慢路径查 Payment 失败只记日志，直接返回数据库里的数据；按降级理论，触发降级时也可以选择不去查 Payment，或者关掉慢路径保住 Reward 服务本身。〔W15-2 p16〕
- 查询必须校验归属：后端只暴露一个查询打赏结果的接口，`RewardHandler.GetReward` 的请求带 `Rid=req.Rid`、`Uid=claims.Id`；Reward 服务核对 rid 是不是当前登录用户创建的，打赏的人和查询的人必须是同一个，否则交易号可被枚举，越权看别人的打赏记录（这句是本节推导）。返回 Data 暂时只需要状态（resp.Status 的字符串形式）。〔W15-2 p15〕
- 有序性只对有顺序语义的事件必要：打赏从 Init 到 Success 的纯状态更新天然容忍乱序（重复更新无害），支付、退款这类有顺序要求的事件才需要控制；Payment 初始化生产者时把分区器换成 sarama 的一致性 CRC 哈希分区器（`sarama.NewConsistentCRCHashPartitioner`），并设置 `Return.Successes=true`，再用 viper 读 Kafka 配置创建客户端。〔W15-2 p13〕
- 发消息失败可以返回 nil：本地 DB 已经记下终态，Kafka 只是通知；因通知失败让微信回调整个流程重试反而危险。代价是必须有监控告警加异步补偿作业兜底。〔W15-2 p12〕
- 记账归属三选一：打赏分，因为打赏业务才知道具体分成比例；支付分，因为分账可以看作支付流程的一部分；账号分，因为分成可以看作记账业务的一部分。打赏分还有一个变体：打赏只计算分成，由支付来调用记账服务，具体做法是打赏调支付接口时提前告诉支付怎么记账，后续支付再调账号服务。作者认为三种没有明显优劣，课程选打赏分，因为他更倾向于让具体业务处理记账。〔W15-2 p20〕
- 账号服务 Credit 接口一次带多笔账：平台抽成 10% 加作者 90% 两条 item；抽成为 0 也要记账（课程注释原话「虽然可能为 0，但是也要记录出来」）。课程只说了"要记录",理由是教学推导:省掉零金额记录不影响金额合计,但流水少了一行,无法完整还原"这笔业务事件分了两笔账"的事实,排查和审计时对不上条数。〔W15-2 p25〕
- CreditRequest 与 CreditItem 的字段全景：CreditRequest 只有三个字段，`biz`（string，注释「唯一标识业务」）、`biz_id`（int64，业务 ID）、`items`（repeated CreditItem，注释「不同账号金额变动」）；每条 CreditItem 有 `account`（int64，账号）、`account_type`（AccountType，注释「账号类型」）、`amt`（int64，注释「金额」）、`currency`（string，货币）、`uid`（int64，用户）五个字段。两条注释别混：`account` 的原注释是"在一些复杂的系统里面，用户可能有多个"，说的是复杂系统里一个用户可以有多个账号（此时用 account 指定记到哪个账号）；"系统账号这个字段可能会没有"是 `uid` 的注释（平台侧抽成账没有自然人 uid，这是结合 p25 的分账代码得出的结论〔W15-2 p25〕）。接口的关键是把同一个业务拆成多笔账、在接口里定义清楚，账号服务是对账、统计、报表的核心。〔W15-2 p21〕
- Reward 侧分账与幂等遗留：先算平台抽成 `weAmt = int64(float64(r.Amt) * 0.1)`，再一次 `Credit` 调用 `Biz="reward"`、`BizId=rid`，Items 带两笔：`Amt=weAmt`（平台抽成，注释「虽然可能为 0，但是也要记录出来」）与 `Amt=r.Amt-weAmt`（作者得九成），`Currency` 均为 "CNY"。同一笔打赏消息重复消费会重复记账，作者明说"这里我没有处理一个问题：幂等。这是你作业中的一个选项"，Credit 调用没有处理幂等，作者把它留成教学留白；落地时至少用 Biz 加 BizId 做幂等键。作业要求设计一个方案，保证同一个 biz+bizId 不会重复记账，还要考虑进一步优化性能；参考思路包括唯一索引、Redis 去重、布隆过滤器等。〔W15-2 p25〕〔W15-2 p32〕
- 流水表带 Biz 和 BizId 两个字段是 DEBUG 神技：对照设计是只用一个单独的 txn_id 来标记一笔变动，课程选择额外带这对业务 ID，注释原话「加上这些业务 ID，DEBUG 的时候贼好用」；打赏、支付、记账三个服务之间的对账和排查全靠它们串联。〔W15-2 p23〕
- 事务边界划在"更新余额加记流水"上：同一库就是一个本地事务；金融级的分库分表之后一笔账跨库，本地事务失效，业务层要靠补偿与对账兜底（补偿与对账的具体机制见本章前四节，这句是本节推导）。例外是钞能力：买 Oracle、买服务器、买操作系统，事实上仍可以继续用本地事务，外企和银行就是这么干的（微信、支付宝的细节作者不清楚）。〔W15-2 p24〕
- Account 表的拆分尺度：账号服务实现分两件事，更新余额加记流水，至少两张表：Account 存余额，AccountActivity 记变动。这两步要么同成功、要么同失败，任何一步单独落库都会让余额和流水对不上，所以必须放进同一个本地事务。Account 表字段设计要提前考虑出海的币种问题但不必提前写多币种代码，要为审计、会计、对账预留空间；字段真多起来再拆 AccountAudit（审计）、AccountTax（税务）这类伴生表，加起来才二三十个字段就不着急拆。〔W15-2 p22〕〔W15-2 p24〕
- AccountActivity 流水表比 Account 简单，同样要考虑币种。字段与索引：Id 主键自增；Uid、Account、AccountType 三个字段都带 `gorm:"index:account_uid"` 复合索引；Amount int64、Currency、Ctime、Utime。金额增减有两种设计：直接用正负数表达增大减小，或不引入负数、另加方向字段标记增减；课程选正负数，原注释「暂时我们还不需要」。〔W15-2 p22-23〕
- 支付单号直接用业务方的还是自己生成：课程实现直接用业务方传来的 BizTradeNO 做微信的 out_trade_no；大型系统会在 Payment 里自建 PaymentSN（UUID，或编入商品 ID、商品类型等信息），但无论哪种，去重都必须依赖业务方的 BizTradeNO，不能用自己生成的序列号去重。〔W15-1 p23〕
- 回调接口放 BFF 还是 Payment 自启：把回调当普通 HTTP 接口塞进 BFF 是形态一，接入快，小公司初期可以先这么接；形态二是 Payment 自己启 Web 接口，作者一般推荐这一种，因为能在回调路径上做专门治理（分发到多环境、验签、幂等这些前面讲过的机制都挂在这条路径上），课程也选形态二。〔W15-1 p24〕

### C:dachang/distributed_transaction：DTM 的细节与坑

- 所有模式共享"最终成功"兜底：二阶段消息的分支、SAGA 的补偿、TCC 的 Confirm/Cancel 都必须重试到成功，失败超阈值报警转人工。〔C:dachang/distributed_transaction/分布式事务模式.md:9-13〕
- barrier 的并发语义靠数据库唯一键：事务未提交时，另一事务写相同 `gid-branchid-op` 会被唯一键锁阻塞；`insert ignore` 撞键不报错只给 warning，必须用 `RowsAffected>0` 判断是否真的插进去了；不违反唯一键的 insert ignore 不会阻塞。唯一键漏建，三个控制全部失效。〔C:dachang/distributed_transaction/exception/ap/barrier.go:9-23〕
- 二阶段消息的 DoAndSubmitDB 把本地事务和 Submit 原子绑定：本地事务成功才插入 barrier 记录并 Submit；返回 error 时插 rollback 记录，dtm 把全局事务标为失败。进程没来得及返回就宕机，dtm 回查 QueryPrepared，它一上来先插 rollback 记录，所以宕机默认按失败处理，业务方必须接受"宁可少转不可多转"；二阶段消息的分支 trans_in 由 dtm 异步调用，默认必成功。〔C:dachang/distributed_transaction/msg/ap/main.go:73-103〕
- TCC 的代价是业务侵入：RM 要实现 Try/Confirm/Cancel 三接口加资源冻结字段；Try 全成才 Confirm，一失败全 Cancel；返回时 Try 已完成但 Confirm 通常没完，最终成功由 dtm 无限重试保证。TCC 只适合短事务：AP 重启后全局事务只能回滚不能继续，这点和 SAGA 不同。〔C:dachang/distributed_transaction/tcc/ap/main.go:60-194〕
- XA 的坑：prepare 期间锁不释放，高并发热点必死；xa end 之后 prepare 之前同连接再读写报 XAE07；AP 在 prepare 后宕机，锁悬着，要人工 `xa recover` 裁决。RM 的 handler 必须用 ShouldBindJSON 而不是 Bind，XA 分支会被多次调用且有时没有请求体，Bind 会 400 中止让 dtm 误判失败。〔C:dachang/distributed_transaction/xa/rm/main.go:47-49〕
- 手写补偿的教程代码是 for 死循环加 Sleep，没有重试上限，生产必须加次数阈值、报警、人工介入。〔C:dachang/distributed_transaction/exception/ap/main.go:104-113〕
- dtm 客户端怎么用：分支 handler 从请求 query 里取 gid、branch_id、op、trans_type 构造 Barrier（`dtmcli.BarrierFromQuery`），再用 `CallWithDB` 把业务包进 barrier 事务，框架在内部完成幂等、空补偿、防悬挂三项控制；分支返回值三态，nil 成功、ErrOngoing 框架重试、ErrFailure 触发补偿。〔C:dachang/distributed_transaction/saga/ap/main.go:59-126〕
- TCC 在 RM 侧的样子：Try 调资源接口把金额挪进 `trading_balance` 冻结字段（带防负条件），Confirm 还原进真实余额，Cancel 反向解冻；AP 里 TccGlobalTransaction 串行 CallBranch。〔C:dachang/distributed_transaction/tcc/ap/main.go:60-194〕
- XA 的代码形状：RM 用 `XaLocalTransaction` 包本地 SQL（xaID 是 gid 加 branchID），数据库自动两阶段；AP 用 XaGlobalTransaction 串行调各 RM，Prepare 成功就返回，commit 大概率还没完成。〔C:dachang/distributed_transaction/xa/rm/main.go:46-62〕
- 手写 SAGA 的编排：AP 起全局 gid，分支先过 Barrier(action)（拦住说明已执行，直接成功）再调 RM 转账，本地事务成功 Commit 失败 Rollback；任一分支失败触发全部补偿，补偿先过 Barrier(compensate) 幂等，再查 action 占位判断空补偿，然后循环重试到成功（Sleep 5s，超过阈值报警）。〔C:dachang/distributed_transaction/exception/ap/main.go:64-170〕

### C:dachang/raft：教学版实现的工程细节

- 投票规则三个拒绝一个升级：同任期已投别人拒绝、候选人任期更小拒绝、候选人日志不如自己新拒绝；对方任期更大则无条件升级任期、清空投票记录、降回 Follower 再投。任何 RPC 里发现对方任期更大，都走这条降级链。〔C:dachang/raft/server.go:403-422〕
- Follower 拒绝投票时不重置选举计时器，否则真正合适的候选人永远选不上。〔C:dachang/raft/server.go:185-189〕
- 新 Leader 当选后要立刻发一轮 AppendEntries（空日志也行），让其他 Candidate 尽快降级，不等心跳周期。〔C:dachang/raft/server.go:306-312〕
- 日志按 term 加 index 双升序组织，查找用二分；AppendEntries 按 prevLogIndex 定位，term 匹配则截掉冲突尾部再追加，term 不一致连第 i 条一起截断并返回 false 让 Leader 回退重试。〔C:dachang/raft/log.go:127-166〕
- Leader 为每个 Follower 记一个 prevLogIndex，新 Leader 乐观地初始化成自己的末尾，一轮响应后修正为对方实际位置；每次心跳最多带 100 条日志，追不上就分多轮。〔C:dachang/raft/server.go:255-295〕
- 教学版日志只在内存，真实 Raft 必须先落盘再响应，否则宕机丢已提交日志。〔C:dachang/raft/log.go:20-21〕
- 提交点的三道闸：日志条目找得到、Leader 只能提交本任期的 index、commitIndex 单调不回退；然后逐条应用到状态机。〔C:dachang/raft/server.go:378-400〕
- 所有 RPC 投进同一个带缓冲 channel 在角色循环里串行处理，把并发问题变成顺序问题：Raft 状态迁移必须原子，简单正确优先于吞吐。Stop 时 handler 与主循环竞争 channel，发送前必须二次检查状态，否则 send on closed channel 会 panic。〔C:dachang/raft/rpc.go:128-133〕
- 这个教学版验证过什么：7 节点集群随机停一台，等选主，发一次心跳发不完的 300 条日志，再重启宕机节点，5 秒后所有节点日志下标追平 300。注意磁盘持久化是注释掉的，真实实现必须先落盘再响应。〔C:dachang/raft/test/raft_test.go:29-72〕

### C:dachang/etcd：租约与锁

- 租约两式：KeepAlive 返回通道自动续约，长驻服务用它；KeepAliveOnce 手动续一次且剩余寿命清零，适合自己控制节奏的心跳场景。两者都必须赶在到期前调用，过期了只能重新 Grant 加 Put。〔C:dachang/etcd/etcd.go:59-161〕
- 服务注册：key 按 `/service/grpc/<服务名>/<endpoint>` 设计只存 key 不存 value；注册时 Grant 租约加 Put，续约失败返回 ErrLeaseNotFound 就重注册；进程退出不主动反注册，最晚一个心跳周期后被发现宕机。〔C:dachang/etcd/service_registration_center/server/service_hub.go:54-96〕
- 并发锁三式：TryLock 立即返回（他人持锁返回 ErrLocked）、Lock 阻塞到获得、带超时的 Lock 到期返回 DeadlineExceeded 放弃。Session 底层建租约，进程崩溃锁随会话过期自动释放，比 MySQL 唯一键锁可靠，代价是写延迟 3.4ms 级。〔C:dachang/etcd/etcd.go:163-265〕
- Get 不存在的 key 不报错，用 `len(response.Kvs)==0` 判断；租约到期的键自动消失；`WithRange` 是前闭后开区间。〔C:dachang/etcd/etcd.go:36-37〕
- 配置中心模式：启动时按 key 拉初始值进全局配置，再用 Watch 前缀监听事件通道热更新，只处理 PUT 事件；服务发现客户端用 Watch 刷新端点缓存，随机挑一个实例并按服务复用 grpc 连接。〔C:dachang/etcd/etcd_config.go:22-61〕〔C:dachang/etcd/service_registration_center/client/client.go:19-46〕

注：正文已经展开而这里不再重复的：W15-1 的打赏参与方与全流程（开头已收）与幂等重试返回旧结果、对账双层兜底、金额三存法、二维码复用（第 4 节），W15-2 的 PreReward 参数与单一支付方式、Credit 接口结构（本节收）、部分失败清单与三者对账（第 4 节），barrier 四步与 NPC（第 2 节），Raft 的选主、随机超时、多数派与提交点计算、心跳参数约束、etcd 对 Redis 的性能差（第 5 节）。〔W15-1 p4〕〔W15-1 p25〕〔W15-1 p29-33〕〔W15-2 p5-7〕〔W15-2 p21〕〔W15-2 p28〕〔C:dachang/raft/config.go:7-24〕〔C:dachang/etcd/etcd_redis_test.go:104-123〕

## 想深入去哪查

- 原材料：W15-1（支付流程、两种未知结果、对账、金额）、W15-2（打赏全流程、消息有序与幂等、记账与分账、部分失败识别）、C:dachang/distributed_transaction（四种模式、barrier 四步与并发细节）、C:dachang/raft（500 行实现，选主与日志复制）、C:dachang/etcd（etcd 对 Redis 基准）。
- 本章可运行代码是 DTM barrier、outbox、Raft 提交点与随机超时的内存版；真实组件里对应数据库事务里的 `dtm_barrier` 表、消息队列加中继程序、Raft 的 `AppendEntries` 响应处理。
- Raft 正式定义见论文 In Search of an Understandable Consensus Algorithm；dtm 的模式与 barrier 文档在 `https://dtm.pub`。
