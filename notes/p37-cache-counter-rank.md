# P37 · 缓存、计数与榜单

> 这章学完你能干什么：说清互动系统三块（对象缓存、互动计数、热帖榜单）各自的竞态在哪、缓存一致性五模式的取舍（先 DB 后缓存、先更新 DB 再删缓存、三级读写序、只缓存第一页、启动预加载），并用 minifeed 新增的 post/、counter/、rank/ 三个包走完"素材模式 → 单机等价物 → 竞态分析"的链路：锁内自增对 HIncrBy、ticker 批量刷库对 Kafka 凑批、TryLock 单飞对 SetNX 分布式锁。素材来自 webook 互动线（Redis + Kafka + GORM），本章项目锚点仍是标准库 minifeed，差异章内明说。前置：P36（Store 接口与双实现）、P3（channel）、P21（-race）、P35（治理边界）。

## 1. 为什么缓存：瓶颈与三层结构

webook 压测后瓶颈定位两处：加密算法耗 CPU（注册/登录都有）、数据库查询。优化思路是 Redis 先查、未命中再回落数据库并回写缓存。〔W04-1 p12〕登录低频（好几天一次）不缓存，只优化 Profile 这种高频读。〔W04-1 p19〕

不直接用 Redis 而引入业务专属 UserCache（redis.Cmdable 接口 + expiration 15min）：调用方不接触 Redis 细节。〔W04-1 p13〕

UserCache 屏蔽三件事：过期时间设置（调用方不关心）、key 结构（Key(id) = fmt.Sprintf("user:info:%d")）、序列化协议（json.Marshal 与 Unmarshal 互转）。〔W04-1 p14-15〕缓存集成位置属于"如何存储数据"，放 Repository 层，Service 无感知，NewUserRepository(dao, cache) 双依赖注入。〔W04-1 p16〕

三级缓存结构：本地缓存-Redis-DB。读序=本地→Redis→DB；写序=DB→本地→Redis，先本地后 Redis 的理由是本地操作几乎不会失败。〔W21-1 p32〕

Redis 层的两种形态：对象缓存模式（STU_+Id 前缀防 key 冲突，json.Marshal 后 string 存，读时 err==redis.Nil 视为不存在）〔C:go_frame/database/redis/redis.go:41-62〕；hash 结构（一个 key 一个 map 存多计数）。〔W07-1 p9〕

minifeed 没有独立缓存层（单进程 map 本身就是"本地层"），这章的等价物是接口形状。

counter.Counter 接口给 Like/Unlike/Count 三方法，biz+bizId 标识任意资源，即 W07-1 K4 的通用性设计。（counter/counter.go L13）post.PostStore 接口给 Create/GetByID/List。（post/post.go L27）

UserCache 屏蔽三件事在 minifeed 的对应物是 Store 接口：换实现只动 openStore/openPostStore 一行（SQLite 路径在 MINIFEED_DB 环境变量）。（server.go L63-L76）

〔实测〕三层结构的单机版可达性：`cd minifeed && go test -race -count=1 ./counter/ ./post/`，两包全 ok。计数与帖子各持一个接口，Service 组装两者（rankSource 持 posts 与 cnt 双依赖），与 NewUserRepository(dao, cache) 的注入形状同构。

## 2. 对象缓存一致性：先 DB 后缓存

缓存 error 就查 DAO 的默认写法：cache.Get 有 error（含未命中）就查 DAO，查到后回写 cache.Set 且忽略回写错误。隐患是 Redis 崩溃时查询全部落到数据库。〔W04-1 p17〕

替换性写法 FindByIdV1：switch err，case nil 直接返回、case ErrKeyNotExist 才查 DAO、default 直接返回错误；Redis 崩溃则业务不可用但数据库保住。〔W04-1 p18〕

这是可用性优先与隔离优先的取舍：课程推荐前者（缓存是优化不是数据源），D2 的补丁是生产要给回写失败打点监控，不能完全忽略。〔W04-1 p17-18〕

一致性策略的核心一条：先更新 DB 再删缓存。它缓解非解决（极端交叠仍不一致：读旧值回写慢于删除后写），但极端少见（读 DB 回写缓存比写 DB 删 key 快），用户缓存用此策略。〔W21-1 p28〕W09-1 的可用性复盘补上另外两格：MySQL 崩=榜单不更新（定时任务失败）；Redis 崩+本地缓存失效=接口失败；对策=Redis 崩溃时回退本地缓存且不检查过期。〔W09-1 p36〕Redis 永不过期（只有定时任务更新缓存）规避 DB 故障传导。〔W09-1 p38〕

minifeed 单进程内没有 Redis 可崩，一致性竞态留给 §3 的计数与 §4 的榜单；这节的教学物是"降级语义写进代码"的形状。rank 包把 K19/K21 两条直接固化：Refresh 刷新失败保留旧榜继续服务（课程"刷新失败继续用过期数据"的亮点方案）（rank/rank.go L43-L54）；读侧 Top 是 atomic.Value.Load，零锁。（rank/rank.go L56）

【实测】降级语义的可验证形状：`cd minifeed && go test -race -count=1 -run TestRefreshTTL ./rank/`，TTL 内二次 Refresh 幂等跳过（calls==1）。

## 3. 互动计数：先 DB 后缓存与原子自增

互动三件套形态差异：阅读只加不减（打开即+1）；点赞/收藏可取消；收藏另有收藏夹分类。设计上要做成给任意资源用（评论、照片、视频都行），biz+bizId 标识业务。〔W07-1 p2-3〕课程选合并三套相似代码（中小公司主流），不按服务拆分。〔W07-1 p4〕

IncrReadCnt 顺序=先 DB 后缓存；部分失败引发的不一致不解决（阅读数不准没关系）。〔W07-1 p6〕DB 侧的硬约束：不能"读出加一写回"（并发丢更新），必须 read_cnt=read_cnt+1（MySQL 行级锁保证线程安全）。〔W07-1 p7〕Interactive 表含 BizId+Biz+ReadCnt/LikeCnt/CollectCnt，uniqueIndex:biz_type_id 联合唯一索引是关键。〔W07-1 p8〕

缓存侧的 Redis hash 结构：一个 key（biz:bizId）一个 map 存三个计数。〔W07-1 p9〕两种缺数据情形：key 不存在（只有 DB 有）、key 存在但 field 不存在。HIncrBy 天然兜底后者：不存在先置 0 再自增。〔W07-1 p10〕

lua 脚本防并发：EXISTS 检查+HINCRBY 是"检查-做某事"场景，lua 保证原子；参数 cntKey+delta。〔W07-1 p11〕

点赞两件事：记录"谁赞过"（UserLikeBiz 表，可取消）+总数统计（Interactive）。〔W07-1 p12〕

软删除 vs 硬删除：点赞-取消-再点赞场景，硬删除=反复插删；软删除加 status 字段=只更新一列（性能更好）+保留数据（公司偏好）。〔W07-1 p15-16〕软删除下的 Upsert：OnConflict DoUpdates status=1+utime（点赞）与 LikeCnt=gorm.Expr("like_cnt+1")+utime（计数）两个 Upsert，放同一事务。〔W07-1 p17〕

〔实测〕降级语义的可验证形状：`cd minifeed && go test -race -count=1 -run TestRefreshTTL ./rank/`，TTL 内二次 Refresh 幂等跳过（calls==1）。

〔实测〕原子自增的并发验证：`cd minifeed && go test -race -count=1 -run TestConcurrentLike ./counter/`，50 goroutine 并发 Like 同一帖，计数恰 50。-race 下丢更新会直接翻车，这是 P21 §-race：动态分析武器 的方法论在这章的复用。

## 4. 榜单：TopN 计算与缓存装填

榜单查询接口高并发高可用（每用户打开 APP 都调）。本地缓存两种组装思路：缓存层装饰器（DoubleCache）vs Repository 直接组合两实现（简单快，课程选此）。〔W09-1 p32-33〕本地缓存实现：数据只有一份不需要 key-value，atomicx.Value[T] 泛型原子类（Store/Load）+3 分钟 ttl 判失效。〔W09-1 p34〕Repository 组装：Set 先本地（必成功）再 Redis；Get 先本地→Redis→回写本地。〔W09-1 p35〕

生产侧的算分流程：分批 ListPub 拉 batchSize 篇→GetByIds 查点赞→算 score→小顶堆（cap=N）满队时出堆比较、新 score 大才替换→全部批次结束堆里即 TopN→装 Redis。〔W09-1 p18-19〕七天优化：ddl=now-7d，七天前数据不可能进前 100，批次空/不满/最后一条 utime 早于 ddl 就中断循环。〔W09-1 p23〕缓存字段纪律：查询接口所需字段一个不少，Content 换 Abstract 不缓存全文。〔W09-1 p24〕

minifeed 的 rank 包：Source 接口给候选集。生产是分批+小顶堆，minifeed 候选集小直接排序。（rank/rank.go L20）rankSource 组装分数：分数=计数，标题来自帖子表，List(100,0) 取前 100 候选。（server.go L78-L95）

装填与读取：data atomic.Value 存 rankCache{items, at}，Top(n) 返回快照。（rank/rank.go L30-L61）

榜单 TTL 语义上做了与课程一致的取舍：未过期跳过（启动预加载后的重复触发）；空榜不算有效缓存（允许重装，避免"启动空榜被 TTL 锁 60 秒"）。

〔实测〕榜单排序装填：`MINIFEED_ADDR=:8090 MINIFEED_DB=/tmp/mf.db /tmp/minifeed-smoke` 起服务，curl 建两帖给帖 1 点 3 赞，`curl -s localhost:8090/rank` 得 `[{"PostID":1,"Title":"t1","Score":3},{"PostID":2,"Title":"t2","Score":0}]`；帖 1（3 分）排在帖 2（0 分）前。

## 5. 异步计数：领域事件与批量消费

改造动机：HTTP GET 里调 IncrReadCnt=读请求里夹一次 DB 写，读多写放大压垮 DB；引入 Kafka 后 ArticleService 只发"某用户读了某文章"事件，订阅方自治。〔W07-2 p37-38〕领域事件是 DDD 概念：ReadEvent{Aid,Uid} 定义在 internal/domain/events；articleService 聚合 ProduceReadEvent（go func 异步发，失败仅记日志）。〔W07-2 p39-40〕

批量消费动机：消费者无权要求生产者把单条改批量，所以在消费端凑批（BatchHandler[T]{Fn func(msgs []*ConsumerMessage, ts []T)}）+超时控制+一次事务调用 repo.BatchIncrReadCnt。〔W07-2 p42-44〕批量 DB：事务内 for 循环 incrReadCnt(tx,...)；批量一次提交比逐条提交快一个数量级。〔W07-2 p45〕松耦合的验证：新增阅读记录 HistoryConsumer 消费同一 ReadEvent，GetPub 接口零改动。〔W07-2 p47〕

minifeed 的单机等价物：MemoryCounter 的 ticker 刷库。单条请求只动内存，handleLike 里计数只调 s.counters.Like，不碰 DB。ticker 到点把脏 key 批量交给注入的 flushFn，落库失败保留 dirty 下轮重试。（counter/counter.go L111-L127）这就是 W07-2 K25 的凑批语义在调度侧的形状：凑批的意义在"到点才落"，不在单条 SQL 数。

channel 语法（谁创建谁关、close 后读恒零值）P3 §close 与 range 已讲。Kafka 分区有序与 ack 语义是素材栈细节，本章不展开（W07-2#K15-K21 是 P31/P41 的线）。

〔实测〕批量刷库：`cd minifeed && go test -race -count=1 -run TestFlush ./counter/`，ticker 10ms，Like 两次+一次后 flushed map 得 post:1=2、post:2=1。flushFn 注入使测试不碰真 DB。

## 6. 分布式锁与抢占调度

榜单定时计算在多副本下会重复算：解法是节点先抢锁，抢到才执行，保证分布式环境只有一个 goroutine 拿到锁。〔W09-2 p6〕

锁放在 Job 还是 Service？Service 是业务逻辑要求全局唯一，Job 是调度本身唯一，课程选 Job 层让 Service 保持纯净。〔W09-2 p8〕

计算期间持锁版的三个约定：锁过期时间=r.timeout（任务必然在 timeout 内完成）；解锁失败不重试，最多 timeout 自动释放；抢锁失败不 return error，相信别的节点会拿锁。〔W09-2 p9〕升级路径：Preempt 接口抢到任务时启动 ticker（refreshInterval 10s）goroutine 持续 refresh，CancelFunc=close(ch) 停止续约并 Release。〔W09-2 p19〕抢占 SQL=乐观锁：UPDATE ... SET utime=now,version=version+1,status=running WHERE id=? AND version=?（CAS），RowsAffected==1 才算抢占成功。〔W09-2 p20〕

Redis 侧的教学实现：SetNX（key 不存在才写并带过期）实现 TryLock；过期时间兜底崩溃不悬挂；Del 释放；教学版无 owner 校验（可能误删他人锁，生产应 Lua 原子比对）。〔C:go_frame/database/redis/lock.go:10-23〕

minifeed 的单机等价物：rank.Refresh 用 sync.Mutex 单飞。并发触发 Refresh 只有 1 个 goroutine 真算，其余等锁后直接命中"未过期跳过"。（rank/rank.go L43-L47）注意语义差：SetNX 抢不到就放弃（TryLock 语义），sync.Mutex.Lock 会排队等。minifeed 里等到的结果也是跳过，所以行为等价；要"抢不到立刻返回"得换 TryLock 方法，练习位。多副本下这份代码的失效模式（每节点各持一把内存锁，榜单各算各的）就是 K3 要解决的问题本身。

〔实测〕单飞验证：`cd minifeed && go test -race -count=1 -run TestConcurrentRefresh ./rank/`，10 goroutine 并发 Refresh 不 panic、结果一致；TestRefreshTTL 断言真算次数为 1。

## 7. ORM 生产开关与分页缓存

gorm 关键初始化开关：PrepareStmt 缓存预编译语句、SkipDefaultTransaction 关掉写操作默认事务（约 30%+ 性能提升）、NamingStrategy{SingularTable: true} 表名不复数、DryRun 只生成 SQL 不执行、DisableNestedTransaction 关 SavePoint 嵌套事务。〔C:go_frame/orm/gorm/connection.go:39-51〕

读写分离用 dbresolver：Register(Config{Replicas, Policy: RoundRobinPolicy}) 后 db.Use(resolver)，主库写从库读自动路由。〔C:go_frame/orm/gorm/connection.go:63-87〕

Create 必须传指针（要回填主键与 CreatedAt）；CreateInBatches 分批插入且各批次放同一个事务。〔C:go_frame/orm/gorm/create.go:16-48〕

Save 保存所有字段（含零值）；Updates(map) 精确指定列；Updates(struct) 只更新非 0 值。〔C:go_frame/orm/gorm/update.go:10-33〕

事务模板是 db.Begin() 起手、业务在中间、Rollback/Commit 收尾；recover 里也要 Rollback。〔C:go_frame/orm/gorm/transaction.go:11-54〕

minifeed 用 database/sql 没有"开关"可开（无 gorm 层），对应的纪律在裸 SQL 里显式做。

Create 的 Exec 结果对取 LastInsertId 回填 ID（C:go_basic/database#K2 语义）；rows 必须 defer Close，for rows.Next() 内 Scan（C:go_basic/database#K4）。

连接池参数 SetMaxOpenConns 是 go_frame/orm#K1 连接池纪律的裸版。minifeed 的 List 用 LIMIT ? OFFSET ?。（post/post.go L134-L150）

分页缓存难做的根因：筛选/排序/偏移量/页大小任一变化缓存即失效；折中=只缓存第一页（用户大多只看第一页，分页接口难缓存，第一页高频访问效果明显）。〔W06-3 p7〕〔W21-1 p29〕第一页缓存判定很严格：offset==0 && limit==100 才走缓存（可放宽到 Limit<=100）；先查缓存，miss 走慢路径查 DB 再 SetFirstPage。〔W06-3 p8〕预加载方案两分：业务相关预加载（预期用户马上访问下一接口，提前装其数据）与启动预加载（更常用；本地缓存=每节点各自加载，Redis 缓存=只需一次）。〔W06-3 p12-13〕〔W21-1 p31〕预加载实现纪律：让调用者决定是否异步（go func 包 preCache），不要自己偷偷异步。〔W06-3 p14〕

Redis 侧还有一个容易被跳过的细节：Scan 游标遍历五律。cursor=0 开启迭代、返回 0 结束（cursor 是槽位值非递增）；COUNT 批大小（建议 10000，大则总耗时短单次阻塞长）；结果可能重复须客户端去重；遍历中修改可见性不确定；单批空≠结束看 cursor。〔C:go_frame/database/redis/redis.go:188-212〕minifeed 单机 map 用不到 Scan，换 RedisCounter（练习 3）后遍历 dirty key 集就要照这五条来。

minifeed 的取舍：列表不缓存（参数四元组任一变化即失效，收益薄），只缓存"单帖详情+榜单"两个稳定热点。预加载的坑在这章踩过真的：handleGetPost 曾在详情读里 go s.top.Refresh()，把空榜锁进 60 秒 TTL。W06-3 K10 的预加载是"预测用户下一个动作提前装数据"，详情读的下一步不定是榜单，偷偷异步反而制造 stale；修正后榜单刷新责任在 /rank 自己。

〔实测〕分页语义：`curl -s "localhost:8090/posts?n=1&offset=1"` 得 `[{"id":2,"title":"t2"}]`；`offset=50`（越界）得 `[]`，空页不是错误。

## 8. 小结与边界

三级读写序复盘：读=本地→Redis→DB；写=DB→本地→Redis。〔W21-1 p32〕本地缓存作备份是轻易不用的策略（命中率低+占内存）：正常走 Redis→DB，Redis 崩溃后走本地→DB，恢复切回。〔W21-1 p33〕面试亮点两条：本地缓存预加载（启动时/快过期提前）+容错（刷新失败继续用"过期"数据，或永不过期+异步刷新）。〔W09-1 p40〕

这章的模式与 minifeed 落点对照：对象缓存（UserCache 屏蔽三件事）对应 Store 接口；原子自增（HIncrBy/lua）对应锁内自增；批量消费（Kafka BatchHandler）对应 ticker 刷库；分布式锁（SetNX）对应 TryLock 单飞；分页缓存（只缓存第一页）对应"不缓存列表"。Redis/Kafka 接入都是练习位（counter 加 RedisCounter、MemoryCounter 的 flushFn 换事务落库），不引依赖是 P36 定下的教学边界。

与素材的差距清单：榜单没有 cron 定时器（robfig/cron/v3）与"刷新失败重试"；计数没有 UserLikeBiz 明细表与软删除 Upsert；锁没有续约（Preempt 的 ticker goroutine）与 owner token。这些是生产替换位，模式在，接入点是练习。

## Take-away

1. 瓶颈定位先于缓存：加密 CPU 与 DB 查询是压测出来的，登录低频不缓存（W04-1#K5/K11）。
2. 缓存层屏蔽三件事：TTL、key 结构、序列化协议，收在一个类型里（W04-1#K6/K7）。
3. 先更新 DB 再删缓存是缓解非解决；刷新失败降级旧榜、永不过期+异步刷新是容错亮点（W21-1#K22、W09-1#K21/K22）。
4. 计数必须原子自增：read_cnt=read_cnt+1，不能读出加一写回；单机锁内自增即 HIncrBy 等价物（W07-1#K6）。
5. "检查-做某事"多副本下必须 lua；单进程锁内查重即等价物（W07-1#K10）。
6. 榜单读侧零锁：atomic.Value 装载快照，刷新单飞由锁保证（W09-1#K17）。
7. 写放大解法是异步化+消费端凑批：事件只发不改单条为批量，凑批在消费侧（W07-2#K22/K25）。
8. 分页缓存只缓存第一页，参数任一变化即失效；预加载不要偷偷异步（W06-3#K5/K11）。
9. 空榜不算有效缓存：启动空数据被 TTL 锁住是预加载语义写错的信号。
10. 分布式锁的多副本失效模式（每节点一把内存锁各算各的）就是引入 SetNX 的理由本身（W09-2#K3）。

## 章末参考

- 命令速查：`MINIFEED_ADDR=:8090 MINIFEED_DB=/tmp/mf.db /tmp/minifeed-smoke`；`curl -s localhost:8090/rank`（榜单）；`curl -s "localhost:8090/posts?n=1&offset=1"`（分页）；`go test -race -count=3 ./...`（minifeed 六包）
- 源码（本机 go1.26.4）：server.go 的 openStore L63、openPostStore L70、rankSource L78、newMux L113、handleCreatePost L225、handleGetPost L249、handleRank L313
- 源码续：post/post.go 的 PostStore L27、MemoryStore.List L63、SQLiteStore.List L134
- 源码续二：counter/counter.go 的 Counter L13、NewMemoryCounter L30、Like L71、flush L111
- 源码续三：rank/rank.go 的 Source L20、Rank L30、Refresh L43、Top L56
- 原材料单元：W04-1（UserCache 与降级两写法）、W06-3（分页缓存与预加载）、W07-1（互动计数与软删除 Upsert）、W07-2（领域事件与批量消费）、W09-1（榜单与本地缓存）、W09-2（分布式锁与抢占调度）、W21-1（缓存模式五点）
- 代码素材：C:go_frame/database/redis/redis.go:41-62（对象缓存模式）、C:go_frame/database/redis/lock.go:10-23（SetNX 教学锁）、C:go_frame/orm/gorm/connection.go:39-51（五开关）
- 代码素材续：C:go_frame/orm/gorm/connection.go:63-87（dbresolver）、C:go_frame/orm/gorm/create.go:16-48（Create 传指针）、C:go_frame/orm/gorm/update.go:10-33（Save vs Updates）、C:go_frame/orm/gorm/transaction.go:11-54（事务模板）
- 延伸：robfig/cron/v3（定时调度）；go-redis v9（生产 Redis）；redsync（带 owner token 的分布式锁）；go-kafka/kafka-go（真 Kafka）
- 旧章交叉：p36 §1 分层：从 handler 到 Store 接口（Store 接口与双实现是本章三包的同构底座）、p36 §5 组装：wire、DI 与 Singleton 实测（组装语义）
- 旧章交叉续：p3 §close 与 range（channel 语法）、p13 §13.1 RED：先摆哪几个指标（写放大的观测位）、p21 §-race：动态分析武器（并发验证方法论）
- 旧章交叉三：p32 §pprof 四级优化：从 72070 到 9625 ns/op（TopN 计算的 CPU 侧）、p35 §2 限流：三种算法对比与 guard/limiter.go 的令牌桶（单飞与 TryLock 的并发结构对照）
- 明确不引：p9 §（database/sql 基础与 pgx，本章只引 C:go_basic/database 的 K 点代码行号）；W07-2#K15-K21 channel 之外的 Kafka 细节归 P31/P41 线

## 练习：p37minifeed

先跑全绿：`cd minifeed && go test -race ./...`

1. 预测：50 goroutine 并发 Unlike 同一帖（初值 0）后计数是多少？先写答案再跑 `go test -race -count=1 -run TestLikeUnlikeCount ./counter/` 验证（Unlike 有下界 0）。
2. 观察：`curl -s "localhost:8090/posts?n=10&offset=50"` 越界返回什么？改成返回 400 该动哪一层？
3. 改造：给 counter 加 RedisCounter（go-redis 依赖，HIncrBy/HGET），与 MemoryCounter 同接口；flushFn 注入位换成事务落库，对照 W07-2#K26 批量一次提交。
4. 测试：给 rank 换 TryLock 语义（抢不到立刻返回不排队），写并发测试断言真算次数仍为 1。
5. 修 bug：MemoryCounter 的 flush 在 flushFn 报错时保留 dirty 下轮重试，但如果 flushFn 永远失败，dirty 会无限增长。加一个 dirty 上限并写测试。
6. 模式迁移：把 §2 的"先更新 DB 再删缓存"落到 post 包（GetByID 加一层 map 缓存，Create/Update 后失效），写竞态测试验证 -race 下不翻车。

（答案与提示：1 为 0，下界保护；5 参考丢最旧 key；6 注意 Create 后的缓存失效顺序即 W21-1#K22。）
