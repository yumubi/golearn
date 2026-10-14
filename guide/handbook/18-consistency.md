# 18 分布式一致性：部分失败、补偿与共识

> 详细程度：深入 · 约 40 分钟
> 材料：W15-1、W15-2、C:dachang/distributed_transaction、C:dachang/raft、C:dachang/etcd · 旧长版：无整章对应
> 前置：04 接口与组合、16 稳定性（重试、幂等）、17 迁移

## 这一节要建立的认知

一致性问题的根源只有一个：**一次业务操作跨越了多个独立的失败域**（多个服务、多个库、库加消息队列、本系统加第三方）。任何两步之间都可能失败，失败之后你不知道对方是成功了还是没成功。〔W15-2 p28〕

三条工程原则，优先级从高到低：

1. **尽量不跨**：按事务边界划分服务和库。同一个库里"更新余额加记流水"是一个本地事务，别把它拆开。〔W15-2 p24〕
2. **跨了就接受最终一致**：用补偿、重试、对账让两边收敛，而不是追求每一刻都一致。
3. **必须强一致才上协议**：TCC、XA，付出吞吐与复杂度的代价。

识别不一致的办法是一条纪律：**对每个调用点，问"这一步失败（或超时、或重复）之后，系统处于什么状态"**，然后补上比对和修复机制。〔W15-2 p28〕

先认清分布式环境里的三个"捣乱者"（NPC）：网络延迟、进程暂停、时钟漂移。时钟不可靠，所以分布式事务靠不上时间戳，真正的困扰来自延迟（TCP 保序不丢，但消除不了延迟）和暂停（GC 或云迁移可能停数分钟）。〔C:dachang/distributed_transaction/exception/npc.md:1-9〕

## 四种模式一张表

| 模式 | 做法 | 隔离性 | 适用 |
|---|---|---|---|
| SAGA | 长事务拆成短事务，失败时逆序补偿 | 无，中间状态对外可见 | 最简单、应用最广，需要回滚的场景 |
| 二阶段消息（DTM 的 msg；自建时常用本地消息表） | 本地事务与"待发消息"原子绑定，异步投递 | 无 | 不需要回滚，下游必然成功 |
| TCC | Try 预留资源、Confirm 使用预留、Cancel 释放 | 预留资源提供一定隔离 | 一致性要求高、不接受"先改后补偿" |
| XA | 两阶段提交，数据库原生支持 | 强，prepare 期间持锁 | 并发不高、无热点争抢 |

〔C:dachang/distributed_transaction/分布式事务模式.md:1-3〕〔C:dachang/distributed_transaction/分布式事务模式.md:5-6〕〔C:dachang/distributed_transaction/分布式事务模式.md:8-12〕〔C:dachang/distributed_transaction/分布式事务模式.md:21-25〕〔C:dachang/distributed_transaction/分布式事务模式.md:54-55〕

选型口诀（DTM 官方建议）：不需要回滚用二阶段消息，需要回滚用 SAGA，一致性要求高用 TCC，并发不高且没有热点用 XA。典型应用：秒杀（Redis 扣库存做 Prepare、MySQL 写订单做 Commit）、订单系统用 SAGA 编排建单、扣库存、扣券、建支付单、缓存一致性（改库加删缓存）。XA 的缺点是 prepare 期锁不释放伤吞吐，而且 commit 结果客户端未知。〔C:dachang/distributed_transaction/分布式事务模式.md:50-59〕

**共同要求：最终成功**。SAGA 补偿、TCC 的 Confirm 和 Cancel、二阶段消息的分支，必须不断重试直到成功；失败超阈值就报警并人工介入。〔C:dachang/distributed_transaction/exception/npc.md:15-20〕

## 一、三大异常与 barrier

以 SAGA 为例，补偿动作会遇到三类异常，资料里叫得很准：〔C:dachang/distributed_transaction/exception/npc.md:9-13〕

- **空补偿**：补偿比分支操作先到（操作还没执行就收到补偿），应直接返回成功。
- **悬挂**：分支操作在补偿之后才到达，应直接拒绝执行，否则资源永远悬着。
- **幂等**：任何请求都可能被网络重试，所有分支操作必须幂等。

DTM 的 barrier 用一张表同时解决三个问题：唯一键 `gid-branchid-op`，用 `insert ignore` 判断是否第一次到达。四步：〔C:dachang/distributed_transaction/exception/npc.md:22-28〕

1. 开本地事务。
2. `insert ignore` 当前 `op` 的记录；没插入成功就是重复请求，直接提交并返回成功（**幂等**）。
3. 如果当前是 compensate，再 `insert ignore` 一条 `action` 记录；插入成功说明 action 从没执行过，直接返回（**空补偿**）。这条记录留着，后来迟到的 action 在第 2 步就会被拦住（**防悬挂**）。
4. 执行业务，成功提交，失败回滚（barrier 记录一起撤销）。

用内存版把四步写出来（真实实现里全部在同一个数据库事务里）：

```go
func (b *Barrier) Call(gid, branch, op string, biz func() error) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.insert(gid, branch, op) { // 2. 重复请求：直接成功
		return nil
	}
	if op == "compensate" && b.insert(gid, branch, "action") { // 3. 空补偿，并占位防悬挂
		return nil
	}
	if err := biz(); err != nil { // 4. 业务失败：回滚，barrier 记录一起撤销
		delete(b.rows, gid+"-"+branch+"-"+op)
		return err
	}
	return nil
}
```

两个异常的实测输出。两个场景各用一个全新的账户 A（余额 100）和全新的 barrier，`action` 扣 10，`compensate` 加 10：

```text
   out action: 执行
   out action: 重复，幂等返回
幂等: 余额 90
   out compensate: 空补偿
   out action: 重复，幂等返回
悬挂: 余额 100
```

第一组连调两次 `action`，只扣了一次，余额 90。第二组先到补偿，被识别成空补偿并占位，迟到的 `action` 因为占位记录被拦截，没有执行，余额仍是 100；如果没有占位，这笔扣款会在补偿之后落地，余额变成 90，对应的资源永远悬着。

barrier 里有一个并发细节：事务没提交时，另一个事务写入相同的 `gid-branchid-op` 会在唯一键上阻塞，`insert ignore` 违反唯一键不报错只给警告，所以要用 `RowsAffected > 0` 判断是否真的插入了。〔C:dachang/distributed_transaction/exception/ap/barrier.go:9-23〕

### 用 SAGA 串起来：失败后的补偿

两步转账：A 转出、B 转入。B 转入失败时，补偿要逆序执行，而且**失败的那一步本身也要补偿**，因为它可能已经部分执行（所以补偿必须能处理空补偿）：

```go
func RunSaga(b *Barrier, gid string, steps []Step) error {
	for i, s := range steps {
		if err := b.Call(gid, s.Name, "action", s.Action); err != nil {
			for j := i; j >= 0; j-- {
				for b.Call(gid, steps[j].Name, "compensate", steps[j].Compensate) != nil {
					// 真实系统里这里 Sleep 加告警，直到补偿成功
				}
			}
			return err
		}
	}
	return nil
}
```

```text
ok: <nil> map[A:70 B:30]
fail: B 已冻结 map[A:70 B:30]
   out action: 执行
   in action: 业务失败，回滚
   in compensate: 空补偿
   out compensate: 执行
```

第二笔转账 A 先扣了 50，B 转入失败；补偿时先补偿 `in`（它的 action 失败回滚了，所以是空补偿），再补偿 `out`（真的把 50 加回去），余额回到 70 与 30。

最后，分支里的"防负"要靠 SQL 自身的原子条件，例如 `WHERE balance + ? >= 0`，`RowsAffected == 0` 就是余额不足或用户不存在；先更新再查询检查会有并发窗口。〔C:dachang/distributed_transaction/dao/bank_account.go:25-56〕

## 二、本地消息表：最常用的最终一致

二阶段消息里最实用的是 **outbox**（本地消息表）：业务数据和"待发送消息"在**同一个本地事务**里写入，再由中继程序异步投递。投递成功后才标记已发送，所以是**至少一次**，消费端必须幂等。

```go
func (d *DB) Relay(publish func(Msg) error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := range d.Outbox {
		if d.Outbox[i].Sent {
			continue
		}
		if err := publish(d.Outbox[i]); err != nil {
			continue
		}
		d.Outbox[i].Sent = true
	}
}
```

```text
broker 挂着: 0 已发送: false
恢复后: [order_created:book] 已发送: true
重复投递: [order_created:book dup 1]
```

broker 挂着时消息留在表里；恢复后补发；模拟"投递成功但标记前崩溃"后重复投递，消费端按消息 ID 去重，只记了一个 `dup`。

支付系统的消息有两条经验：

- **发送失败**：重试加监控告警加异步补偿。〔W15-2 p12〕"数据库已记录结果，所以可以先返回成功、不阻塞回调的响应"是推论，页面没写。
- **有序性**：支付、退款这类有先后顺序的事件，生产者用哈希分区（同一个业务 key 进同一分区）。〔W15-2 p13〕
- **消费幂等**：按业务交易号反解出订单，只做状态更新，重复更新不会造成不一致，前提是发送方顺序正确。〔W15-2 p14〕

## 三、支付与对账：和第三方交互的两种异常

支付的特殊性在于**真正的支付发生在第三方**，你只能创建订单、接收回调、必要时主动查询。〔W15-1 p5〕〔W15-1 p12〕与第三方交互有两种未知结果：〔W15-1 p29〕

1. **预下单超时**：成败未知。重试时，如果第一次已经创建，会得到"订单号重复"的错误但同样能拿到二维码地址；设计幂等接口应尽量返回之前的调用结果。〔W15-1 p30〕
2. **回调处理失败**：支付与否未知。微信会自动重试（非 200 就再调，到次数停止），但不可全信。**兜底是对账**：用业务交易号主动查询订单状态更新本地。〔W15-1 p31〕

对账的最小形态是一个定时任务：预下单设 30 分钟超时，任务扫描超时订单去同步状态。更进一步，回调处理失败时立刻触发单条对账。〔W15-1 p32-33〕

### 金额怎么存

资料列了三种存法：`int64`（分，或四位小数，最小面额，运算最直接）、`decimal`（精确，复杂运算用）、`string`（最不易错，运算需转换，只存储传输时可用）。〔W15-1 p25〕三者里 `int64` 的最小货币单位最省事；不要用 `float`，`0.1+0.2 == 0.3` 为 `false`，实际得 `0.30000000000000004`。

### 部分失败的典型场景：打赏、支付、记账

打赏要经过打赏模块、支付服务、账号服务三个环节，任意一环失败都会不一致。这里的几个设计取舍：

- **分账归属**有打赏分、支付分、账号分三种选择，没有显著优劣。〔W15-2 p20〕
- **记账**：更新余额和记流水必须在同一本地事务；到了金融或支付宝级别，数据库撑不住、分库分表后一笔账跨库，就没有本地事务了。〔W15-2 p24〕
- **三者对账**：用业务交易号把打赏、支付、账号流水串起来，写差异检测和修复。〔W15-2 p32〕

## 四、共识：Raft 的概念地图

前面都是"业务层的补偿"。更底层有一类问题：**多个副本对同一份数据达成一致**，这就是共识。etcd、注册中心、很多分布式存储都建立在 Raft 之上。

Raft 把问题拆成三件事：

1. **选主**：节点有 Follower、Candidate、Leader 三种角色，状态变化切换角色。Follower 在随机超时内没收到心跳，就变成 Candidate 发起投票；超时时间随机化（取 1 到 2 倍的范围）是为了防止多个节点同时发起选举造成活锁。〔C:dachang/raft/util.go:10-17〕〔C:dachang/raft/server.go:163〕
2. **日志复制**：Leader 把日志发给 Follower；Follower 按前一条日志的下标和任期检查，冲突的尾部截断后再追加；已经提交的日志不允许被覆盖。〔C:dachang/raft/log.go:127-166〕
3. **提交**：Leader 发现某条日志已被**过半**节点复制，就提交。

**为什么必须是多数派**：任意两个多数派至少有一个共同节点，所以只要投票时拒绝日志比自己旧的候选人，新选出的 Leader 就一定见过所有已提交的日志。这是 Raft 论文的核心论证，资料代码里的 `QuorumSize()` 就是这个意思。

资料的实现里有一个很巧的细节：提交点不用逐个数，而是把所有节点已复制的最大日志下标升序排好，取其中"有 quorum 个节点不低于它"的那一位，这个位置就是过半节点都已复制到的下标。资料代码写的是 `indices[QuorumSize()-1]`，节点数为奇数时恰好正确，偶数时会取高，把还没过半的日志提前提交（4 个节点复制进度 `[5 4 3 1]`，quorum 是 3，该取 3，`s[quorum-1]` 取到 4）。正确写法是升序后取 `s[len(s)-quorum]`。〔C:dachang/raft/server.go:373-392〕

```go
func CommitIndex(lastIdx []int) int {
	s := slices.Clone(lastIdx)
	slices.Sort(s)
	quorum := len(s)/2 + 1
	return s[len(s)-quorum]
}
```

七个节点复制进度为 `[5 3 5 4 2 3 5]`，排序后 `[2 3 3 4 5 5 5]`，quorum 是 4，取下标 `7-4=3` 得到 `4`。确实有 4 个节点（4、5、5、5）已经复制了第 4 条，是多数派。

参数上的一个硬约束：**心跳必须显著小于选举超时**（资料的实现里心跳 20ms、选举超时 150ms，启动时检查 `2 * 心跳 < 选举超时`，否则直接 panic）。〔C:dachang/raft/config.go:5-24〕所有 RPC 被包装后投进同一个带缓冲的 channel，在角色循环里串行处理，防止中间状态错乱。〔C:dachang/raft/rpc.go:159-164〕〔C:dachang/raft/server.go:171〕

对使用者最实用的推论是：**强一致要付性能代价**。作者的基准里 Redis 读写都在 30 微秒量级，etcd 写要 3.4 毫秒、读 186 微秒（Windows 单机版 etcd 3.5.9，没有多数派开销，写慢主要是落盘与共识流程；集群下还要加上多数派确认）。所以 etcd 适合存配置、元数据、服务发现这类低写高读的数据，不适合当业务数据库。〔C:dachang/etcd/etcd_redis_test.go:104-123〕

## 常见坑

1. **只写正常路径**：每一步都要问"失败、超时、重复"后是什么状态。
2. **补偿不幂等、不重试**：补偿本身也会失败也会重复，必须重试到成功并配告警。
3. **没处理空补偿和悬挂**：网络乱序下会多扣或永久悬着；用 barrier 或同类机制。
4. **消息先发后写库或先写库后发**：任一顺序都可能丢或多；用 outbox。
5. **完全信任第三方回调**：必须有主动对账兜底。
6. **用 float 存金额**。
7. **在选举超时附近设心跳**：Raft 会反复换主，任期飙升。（资料作业的方向，待核）

## 判断题（先答再看）

1. 补偿先到、操作后到（悬挂），barrier 是怎么让后到的操作失效的？
2. SAGA 失败后，为什么要补偿"失败的那一步"本身？
3. outbox 为什么只能保证"至少一次"？消费端要做什么？
4. 七个节点里同时只能容忍几个节点失败？为什么不是更多？

## 在项目里

这里没有现成例子：`dnsproxi-go` 没有跨资源事务。接近的概念是 `tcp/server.go` 里 `serveConn` 的"每连接一个循环、错误一律关连接"，它选择了"失败即断开，由客户端重试"的最简策略，但那是通信层，不是数据一致性。本节没有对应的项目练习，可以把文中的 `Barrier`、`Relay`、`CommitIndex` 抄进 `exercises/` 新建的包里跑测试。

## 想深入去哪查

- 原材料：W15-1（支付流程、对账、金额）、W15-2（打赏、消息、记账、部分失败）、C:dachang/distributed_transaction（五种模式、barrier 与实测）、C:dachang/raft（500 行实现）、C:dachang/etcd（etcd 基准）。
- 旧章：无整章对应。
- 本节所有代码在 `/tmp` 的独立模块里用 `go test -race` 跑过，是 DTM barrier、outbox 和 Raft 提交点的内存版，真实组件里它们对应数据库事务里的 `dtm_barrier` 表、消息队列加中继程序、以及 Raft 的 `AppendEntries` 响应处理。
