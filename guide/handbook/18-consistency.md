# 18 分布式一致性：部分失败、补偿与共识

> 详细程度：深入 · 约 60 分钟
> 材料：W15-1、W15-2、C:dachang/distributed_transaction、C:dachang/raft、C:dachang/etcd · 旧长版：无整章对应
> 前置：04 接口与组合、13 缓存与幂等、16 稳定性（重试、超时）、17 迁移（双写与校验）

## 先看一笔永远对不上的账

用户打赏作者 1 块钱，系统里有三个环节要动：打赏模块记一笔打赏、支付服务记一笔支付、账号服务给作者加钱。跑一天下来对账，三种对不上都有：

- 打赏记录显示成功，支付服务里查不到这笔钱：创建支付订单的请求超时了，钱可能付了也可能没付。
- 支付成功，作者账户没加钱：支付到记账之间那条消息丢了。
- 账户加了两遍钱：同一条"支付成功"的消息被投递了两次。

单机的数据库事务帮不上忙，因为这三个环节不在一个事务边界里。〔W15-2 p28〕本节起点就是：**一次业务操作跨越了多个独立的失败域**（多个服务、多个库、库加消息队列、本系统加第三方），任何两步之间都可能失败，失败之后你不知道对方是成功了还是没成功。所有一致性技术都在回答同一串问题：

1. 这一步失败（超时、重复）之后，系统处于什么状态？
2. 这个状态能不能被检测出来？
3. 检测出来之后，靠什么把它修回去？

还有三个"捣乱者"，缩写 NPC：网络延迟（TCP 保序不丢，但消除不了延迟）、进程暂停（GC 或云迁移可能停几分钟）、时钟漂移（NTP 同步会让本机时间前后跳）。分布式事务不依赖时间戳，真正天天咬人的是延迟和暂停。〔C:dachang/distributed_transaction/exception/npc.md:1-7〕后面的机制几乎都在和"响应还没回来"以及"请求其实还活着"作斗争。

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

选型口诀（DTM 官方建议）：**不需要回滚用二阶段消息，需要回滚用 SAGA，一致性要求高用 TCC，并发不高且没有热点用 XA**。〔C:dachang/distributed_transaction/分布式事务模式.md:54-56〕三个典型场景帮你把口诀落到地上：

- **秒杀**：创建订单和扣库存不是原子操作。用二阶段消息：Prepare 阶段去 Redis 扣库存（大概率失败，秒杀成功是小概率事件），Commit 阶段向 MySQL 写订单。〔C:dachang/distributed_transaction/分布式事务模式.md:57〕
- **订单系统**：下单涉及建单、扣库存、扣券、建支付单，任一步 crash 就不一致。用 SAGA，写好每一步的正向操作和补偿即可。〔C:dachang/distributed_transaction/分布式事务模式.md:58〕
- **缓存一致性**：Prepare 阶段改数据库，Commit 阶段删缓存。〔C:dachang/distributed_transaction/分布式事务模式.md:59〕

## 二、三大异常与 barrier：补偿机制的地基

SAGA 的补偿动作会遇到三类异常，这也是一切补偿方案的公共考点：〔C:dachang/distributed_transaction/exception/npc.md:9-13〕

- **空补偿**：补偿比分支操作先到（操作还没执行就收到了补偿），此时补偿应该什么都不做，直接返回成功。
- **悬挂**：分支操作在补偿之后才到达，此时分支操作应该拒绝执行，否则资源永远悬着。
- **幂等**：任何请求都可能被网络重试，所有分支操作必须幂等。

前两个异常来自 NPC：网络延迟让两个请求以任意顺序到达；进程暂停让"先发"的请求后执行。

DTM 的解法是**子事务屏障**（barrier）：在本地数据库建一张表，唯一键是 `gid-branchid-op`（全局事务 ID、分支 ID、操作类型 action 或 compensate），用 `insert ignore` 判断是否第一次到达。四步：〔C:dachang/distributed_transaction/exception/npc.md:22-28〕

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

读失败路径的操作序列：A 扣 30、B 加 30，C 失败。补偿从 C 开始逆序执行，B、A 依次退回，A、B 回到失败路径开始前的 70 和 30，中间那段 A=40、B=60 的状态对外是可见的，这就是 SAGA"无隔离性"的含义。注意最后的 `C=-30`：这个示例里的 `call` 直接执行 `biz`，没有套上一节的 barrier，C 本来就没转入成功，它的补偿却照样执行，把 C 扣成了负数，这正是**空补偿**要拦的情形。换成 barrier 后，C 的 action 失败时事务回滚、不留记录，补偿到来时 barrier 发现 action 从没执行过，直接返回成功，C 保持 0。资料里完整的 SAGA 把调度交给 dtm：`NewSaga(...).Add(action, compensate, req).Submit()`。〔C:dachang/distributed_transaction/saga/ap/main.go:140-145〕

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

前面的模式都假设"对方是你的服务"。第三方支付不一样：**真正的支付发生在第三方平台上**，你只能创建订单、接收回调、必要时主动查询。〔W15-1 p5〕〔W15-1 p12〕与微信支付的交互只有两个，每个都对应一种"未知结果"：〔W15-1 p29〕

1. **Prepay 超时**：成败未知。你不知道订单在微信那边建了没有。
2. **回调处理失败**：支付与否未知。微信通知过你，但你的处理失败了。

**Prepay 超时，重试是唯一出路**，重试能成立靠的是微信接口的幂等设计：第一次没建成，重试建成并拿到 `code_url`；第一次已建成，重试得到订单号重复的错误，但 `code_url` 照样返回。所以官方原则是：**幂等接口应尽量返回之前调用的结果**。〔W15-1 p30〕

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

还有一个配套设计：用户点了打赏又放弃，再点一次时要**复用缓存的二维码或生成新的交易号**，否则同一个业务动作会创建多笔支付单。〔W15-1 p17〕

**回调失败，靠微信重试加对账兜底**。你处理回调没返回 200，微信就再调，到次数为止，但资料明确说"仅仅依赖这个机制是不靠谱的"；兜底是**对账**：用业务交易号主动查询微信的订单状态，更新本地。〔W15-1 p31〕最小形态是定时任务扫 30 分钟超时单；更及时的做法是回调处理失败时立刻触发单条对账。〔W15-1 p32-33〕

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

把开头的打赏流程走全，每个环节都标注"失败后系统处于什么状态、怎么发现、怎么修"：〔W15-2 p28〕

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

## 想深入去哪查

- 原材料：W15-1（支付流程、两种未知结果、对账、金额）、W15-2（打赏全流程、消息有序与幂等、记账与分账、部分失败识别）、C:dachang/distributed_transaction（四种模式、barrier 四步与并发细节）、C:dachang/raft（500 行实现，选主与日志复制）、C:dachang/etcd（etcd 对 Redis 基准）。
- 本章可运行代码是 DTM barrier、outbox、Raft 提交点与随机超时的内存版；真实组件里对应数据库事务里的 `dtm_barrier` 表、消息队列加中继程序、Raft 的 `AppendEntries` 响应处理。
- Raft 正式定义见论文 In Search of an Understandable Consensus Algorithm；dtm 的模式与 barrier 文档在 `https://dtm.pub`。
