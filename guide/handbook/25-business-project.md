# 25 项目线二：业务实战主线与 minifeed 蓝图

> 详细程度：项目 · 约 30 分钟
> 材料：W02-1 到 W21-1 全部周次（W01-1 缺失，对应 Go 基础语法）、WX-sms · 旧长版：无
> 前置：手册 04 到 20 节至少读过一遍
> 说明：本节是设计蓝图，不依赖仓库里已有的任何实现；其中的接口骨架用 Go 1.26.4 编译通过，但没有实现

## 这一节要建立的认知

资料里的 21 周课程是同一个业务项目（一个叫"小微书"的内容社区，仓库名 webook）一路演进的过程：从单体的用户注册登录，加缓存，加消息队列，加可观测，拆微服务，做数据迁移，再接搜索、Feed、IM。**它真正的价值不是某个功能，而是"一个系统长大的顺序"和每一步的取舍**。

你不需要把 21 周全做一遍。速通的做法是：用这条主线理解"什么时候该引入什么、为什么"，再自己做一个缩小版（minifeed：用户、关注、发帖、Feed），每个阶段只实现最小的一刀，拿测试验收。

## 一、业务项目的整体形状

技术栈（按出现顺序）：Gin、GORM 与 MySQL、Redis（含 lua 脚本）、Kafka、Prometheus 与 Grafana、OpenTelemetry 与 Zipkin、etcd、gRPC 与 Protobuf、Elasticsearch、Canal、K8s，依赖注入用 wire，配置用 viper，日志用 zap。主线分层是 `Handler → Service → Repository → (DAO + Cache)`。〔W02-2 p14-17〕〔W21-1 p25〕

这条分层有个很实在的设计结论：Repository 负责"一切与存储有关的事"，包括缓存一致性、预加载、存储层降级；缓存与否、怎么缓存是技术问题，不是业务逻辑，所以放在 Repository 里而不是 Service 里。〔W21-1 p25〕

## 二、21 周与手册各节的对应

| 周 | 做什么 | 手册节 |
|---|---|---|
| W02 | Gin 入门、用户注册登录；GORM 与三层结构 | 10、11、12 |
| W03 上 | Session 与 JWT；注册登录的限流保护 | 16（Session 与 JWT 手册未展开，查 W03-1） |
| W03 下 | K8s 部署入门 | 23 |
| W04 | 登录性能与缓存；短信验证码登录；wire 依赖注入；单元与集成测试；第三方服务调用治理（限流、failover） | 13、10、14、16 |
| W05 | 微信扫码登录（OAuth2）；长短 token；配置模块；日志模块 | 10、15（OAuth2 与长短 token 手册未展开，查 W05-1、W05-2） |
| W06 | 发帖子（全程 TDD）；状态管理与 MongoDB 存储；查询与缓存 | 14、13（帖子状态机与 MongoDB 手册未展开，查 W06-1、W06-2） |
| W07 | 阅读、点赞、收藏；Kafka 改造 | 13 |
| W08 | Prometheus；OpenTelemetry | 15 |
| W09 | 热榜（小顶堆加定时计算）；分布式任务调度 | 13 |
| W10 | 微服务前期准备（DDD、gRPC、Protobuf）；拆分与微服务化 | 19、11 |
| W11 | 数据迁移（双写与校验） | 17 |
| W12 到 W13 | 服务注册与发现；负载均衡 | 20 |
| W14 | 熔断、限流、降级 | 16 |
| W15 | 支付与打赏 | 18、13 |
| W16 | 评论；用户关系（关注与计数） | 13、21（评论的树形表手册未展开，查 W16-1） |
| W17 到 W18 | 搜索与标签；ELK；Canal | 21、15、17 |
| W19 | Feed 流与压测 | 21 |
| W20 | IM 与 OpenIM | 21 |
| W21 | 总结：设计原则、设计模式、DDD、缓存策略、微服务 | 04、10、13、19 |

## 三、这个项目教会你的几条"演进规则"

1. **重构先补测试**：覆盖率 80% 以上，集成测试比单元测试更重要，因为它真的打到数据库和 Redis。重构必然引入 bug，要问的是"出 bug 了怎么办"。〔W10-2 p12-13〕
2. **拆微服务先选最容易的模块**：业务影响小、依赖最少、QPS 低。课程选的是点赞收藏。拆分用八步：选模块、补测试、拆代码、技术选型、改为微服务对外、本地与微服务双调（用开关控流，随时可回滚）、流量逐步切到 100%、删除本地调用。〔W10-2 p7〕〔W10-2 p12〕
3. **不停机迁移数据分四阶段**：读源、双写以源为准、双写以目标为准、只读写目标。难点是数据一直在变。〔W11-1 p9-10〕
4. **一切不在你控制范围内的调用都要容错**：先限流，再用装饰器无侵入地叠加（不改原有实现），再做 failover。〔W04-6 p3-5〕〔W04-6 p10〕〔W04-6 p21〕
5. **没有强制同步就用异步**：见 21 节「一定异步」。〔W19-1 p35〕
6. **缓存分层**：只缓存分页的第一页；启动时预加载；本地、Redis、DB 三级时，写顺序是 DB、本地、Redis（本地几乎不会失败）。〔W21-1 p29〕〔W21-1 p32〕

## 四、minifeed：自己动手做缩小版

目标：一个单进程的"用户、关注、发帖、Feed"服务。约束：标准库 加 `net/http` 路由 加 SQLite；Redis、Kafka、ES、etcd 全部用**内存实现的小接口**代替，真正的中间件只写"换成真的要改哪里"，测试要在没有网络和容器时也能跑。

### 建议的包划分

```text
minifeed/
  cmd/minifeed/        装配（手写 DI，不用 wire）
  internal/user/       注册、登录、鉴权
  internal/post/       发帖、作者分页
  internal/follow/     关注关系
  internal/feed/       推拉结合的 Feed
  internal/platform/   配置、日志、指标、事件总线、限流
```

### 接口骨架

我把核心接口写成了能编译的骨架，每个包只依赖接口，不依赖实现：

```go
type FollowRepo interface {
	Follow(ctx context.Context, follower, followee int64) error // 幂等：重复调用不报错
	Unfollow(ctx context.Context, follower, followee int64) error
	Followers(ctx context.Context, followee int64, limit int, afterID int64) ([]int64, error)
	Followees(ctx context.Context, follower int64) ([]int64, error)
}

type FeedStore interface {
	PushInbox(ctx context.Context, uid int64, p Post) error     // 写扩散
	PushOutbox(ctx context.Context, author int64, p Post) error // 读扩散
	Inbox(ctx context.Context, uid int64, limit int) ([]Post, error)
	Outbox(ctx context.Context, author int64, limit int) ([]Post, error)
}

// 进程内事件总线：先用 channel，后面换 Kafka 只改这一处
type Bus interface {
	Publish(ctx context.Context, topic string, v any) error
	Subscribe(topic string, h func(ctx context.Context, v any) error)
}
```

`FeedService.OnPostCreated` 的决策逻辑是 21 节推拉结合的直译：取粉丝列表时多取一个（`bigV+1`），超过阈值就只写发件箱，否则逐个写收件箱。

### 分阶段路线与验收

| 阶段 | 实现什么 | 验收标准（都能用 `go test -race` 自动判定） | 对应手册节 |
|---|---|---|---|
| 1 单体分层 | `user`：注册（bcrypt）、登录（JWT）；handler、service、repo、dao 四层 | service 用假 repo 做表格驱动测试；handler 用 `httptest` 测；repo 用内存 SQLite 做集成测试 | 10、11、12、14 |
| 2 发帖 | `post`：草稿与发布；按作者分页，`utime` 倒序 | 状态机非法转换返回错误；分页不重复不遗漏 | 12、14 |
| 3 关注 | `follow`：幂等关注与取关；关注数、粉丝数 | 重复关注结果不变；计数在并发下一致（`-race`）；唯一索引列顺序正确（用 `EXPLAIN` 看是否命中） | 12、13、21 |
| 4 Feed | `feed`：推拉结合，阈值可配 | 大 V 发帖只产生 1 次写；普通作者产生粉丝数次写；读取合并排序分页稳定 | 21 |
| 5 缓存与异步 | 作者第一页缓存；发帖事件走进程内总线 | 缓存命中时不访问 DB（用计数断言）；总线消费者异常不影响发帖成功 | 13 |
| 6 可观测 | `slog` 带请求 ID；自写计数器与直方图；`/metrics` | 同一请求的所有日志有同一 ID；标签只有有限枚举，没有用户 ID | 15 |
| 7 稳定性 | 登录限流；对"短信"这类外部依赖做超时、重试、熔断 | 超过阈值返回 429；外部依赖挂掉时快速失败而不是堆积 goroutine | 16 |
| 8 拆分与迁移 | 把 `feed` 拆成独立进程（先内部接口，再 HTTP）；做一次 SQLite 到另一库的双写迁移演练 | 开关切换流量可回滚；双写校验发现不一致能修复 | 17、19 |

每个阶段结束，项目都应该是**可运行**的，测试全绿。

### 做的时候要守的几条纪律

- 先写接口再写实现，包之间只依赖接口。
- 阶段 8 之前不要拆进程。

## 常见坑

1. 一开始就拆微服务，没有测试保护。
2. 把缓存逻辑写进 Service，业务代码被存储细节污染。
3. 关注关系的唯一索引列顺序写反，`WHERE follower=?` 全表扫。〔W16-2 p11〕
4. 大 V 全部写扩散。
5. 迁移数据时只做一次校验，忘了迁移期间数据还在变。

## 判断题

1. 为什么资料选"点赞收藏"作为第一个被拆出去的模块，而不是"用户"？
2. 三级缓存的写顺序为什么是 DB、本地、Redis？
3. 双写迁移的第二阶段和第三阶段，区别是什么？

## 在项目里

这里没有现成例子：`dnsproxi-go` 没有业务分层、数据库或关注关系。它与 minifeed 的互补关系在 24 节：dnsproxi-go 展示"一个小系统的内部机制"（缓存、逐出、成帧、背压），minifeed 展示"一个业务系统的外部演进"（分层、异步、治理、迁移）。

## 想深入去哪查

- 原材料：按上面的周次表，每一周对应 `Wxx-x` 单元；总览看 W21-1；短信服务面试话术见 WX-sms。
- 旧长版：无。
