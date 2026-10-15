# 13 缓存、消息队列与幂等：读写放大之后的三件套

> 详细程度：深入 · 约 45 分钟
> 材料：W04-1 W04-2 W06-3 W07-1 W07-2 W09-1 W09-2、C:go_frame/mq C:go_frame/lottery、旧长版 `notes/p6-capstone-dnsproxi.md`（dnsproxi 缓存部分）
> 前置：10 分层与依赖注入，12 数据库

## 先看一个让人心疼的现象
一个内容平台，读文章是最基本操作。排查接口慢的原因，发现读文章的 GET 请求里藏着一次数据库写：每次有人打开文章，`IncrReadCnt` 就去更新阅读数。〔W07-2 p37〕原本的接口全部是数据库的读操作，或者直接命中缓存，唯独这里额外多了一个写操作，高并发下数据库被这个"顺手的写"压垮。〔W07-2 p37〕

工程师的直觉反应是三步：读扛不住，加缓存，让读尽量不碰数据库；写和耦合扛不住，加消息队列，让写异步、慢点落库；这两样一加，新的问题立刻出现：**同一条数据被处理两遍成了常态**（缓存会重复回源，队列会重复投递），所以每条写路径都要补一句：做两遍会怎样？这三步合起来就是本章的三件套：**缓存用一致性换读性能，队列用延迟换解耦和削峰，幂等给前两者的重复兜底**。它们不是三个独立知识点，是"读写放大之后"依次长出来的三层防御。这一节按缓存、计数、榜单、锁、队列、幂等的顺序走，每一层都回答"上一层的代价怎么付"。

## 和你已知语言的差异
| 问题 | Java / Kotlin | Rust | TS | Go |
|---|---|---|---|---|
| 并发回源合并 | Guava `LoadingCache` 内建 | moka 的 `get_with` 内建，或手写 | 手写 promise 去重 | `golang.org/x/sync/singleflight`，几行 |
| 计数原子自增 | `AtomicLong` 或 SQL 表达式 | `AtomicI64` | 主线程单线程无竞态 | SQL `cnt = cnt + 1` / `atomic.Int64` |
| 定时任务 | `ScheduledExecutorService` | `tokio::time::interval` | `setInterval` | `time.Ticker` + `select` |
| 分布式锁释放 | Redisson 看门狗+脚本 | redlock 类 crate | 少见 | Lua 脚本"比对 token 再删" |
| 消息客户端 | kafka-clients 一统 | rdkafka（librdkafka 绑定）| kafkajs | Sarama / kafka-go / confluent 三家 |

表里最值得展开的是第一行。Java 生态把"同一个 key 的并发回源合并成一次"做进了缓存库；Go 标准库没有缓存，但把这个能力拆成了独立的小库 `singleflight`，任何函数都能包进去，不止是缓存。下面第三部分专门用它。

## 一、缓存：先定位瓶颈，再决定放哪
### 缓解什么：压测出来的两个瓶颈
先别急着加缓存。课程的做法是先用 wrk 压测三个接口（注册、登录、Profile），再定位瓶颈，结论是两处：加密算法耗 CPU，数据库查询。〔W04-1 p12〕缓存只解决第二个，思路是先查 Redis，未命中再查数据库，查到后回写缓存。〔W04-1 p12〕这个顺序不能反：没有压测数据的缓存设计是猜。

### 放在哪：repository 层，业务无感知
第二个决策是缓存代码放哪。不引入通用 Cache，而是为业务写专门的 UserCache，因为它要屏蔽三件事：过期时间的设置、key 的结构、序列化协议，调用方一律不用关心。〔W04-1 p13〕〔W04-1 p14〕集成位置是 repository 层：缓存属于"如何存储数据"的范畴，service 对它无感知，构造函数多注入一个依赖就行，`NewUserRepository(dao, cache)`。〔W04-1 p16〕这正是第 10 节分层与依赖注入的直接应用：业务逻辑（service）不应该知道自己读的数据是数据库给的还是缓存给的。写入路径要清缓存：新写文章、编辑文章、发表三个写路径都要清除相关缓存。〔W06-3 p10〕失败时只打日志、不回滚业务，是这类缓存常见的取舍（课程页面没有明说）。

### 两种回源写法：崩了之后数据库死不死
先看默认写法，读缓存出错（包括未命中）就直接查数据库：

```go
func (ur *UserRepository) FindById(ctx context.Context, id int64) (domain.User, error) {
	u, err := ur.cache.Get(ctx, id)
	if err == nil {
		return u, err
	}
	// 缓存里没有（或者缓存出错），就查数据库，回写时忽略错误
	ue, err := ur.dao.FindById(ctx, id)
	if err != nil {
		return domain.User{}, err
	}
	u = domain.User{ /* ... */ }
	_ = ur.cache.Set(ctx, u)
	return u, nil
}
```

这个写法的特点是"只要缓存返回了 error，就直接去数据库查询；回写缓存的时候，忽略掉了错误"。〔W04-1 p17〕隐患也随之而来：万一 Redis 本身崩溃了，那么所有查询都会落到数据库上。〔W04-1 p17〕替换性写法区分"未命中"和"出错了"：

```go
func (ur *UserRepository) FindByIdV1(ctx context.Context, id int64) (domain.User, error) {
	u, err := ur.cache.Get(ctx, id)
	switch err {
	case nil:
		return u, err
	case cache.ErrKeyNotExist:
		// 只有确认未命中才回源
		ue, err := ur.dao.FindById(ctx, id)
		if err != nil {
			return domain.User{}, err
		}
		u = domain.User{ /* ... */ }
		_ = ur.cache.Set(ctx, u)
		return u, nil
	default:
		return domain.User{}, err
	}
}
```

代价是 Redis 崩溃之后业务不可用，但是数据库保住了，而别的使用数据库的业务不会受连累。〔W04-1 p18〕课程推荐第一种：缓存是优化不是数据源，可用性优先。两条路都合法，选哪条取决于"数据库能不能扛住全量读"，这要用数据回答，不用立场回答。

## 二、缓存什么：命中率、一致性成本、内存，按这个顺序想
没有通用规则，判定什么要缓存基于对业务的理解。〔W07-1 p23〕课程里有四个对照鲜明的决策，放在一起看才看得出规律：

| 场景 | 决策 | 理由 |
|---|---|---|
| 个人资料（高频读） | 缓存 | 登录态之后几乎每个请求都要 |
| 登录的"邮箱到用户"映射 | 不缓存 | 登录低频，好几天才一次，缓存了也没用〔W04-1 p19〕 |
| 分页列表 | 只缓存第一页的摘要 | 筛选、排序、偏移任一变化缓存就失效；用户大多只看第一页〔W06-3 p7〕〔W06-3 p9〕 |
| 个人是否点赞、收藏 | 不缓存，只缓存总数 | 重复访问同一篇是小概率，命中率低〔W07-1 p23〕 |

规律是：**先看命中率，再看一致性成本，最后看内存**。邮箱映射输在命中率；分页列表输在一致性成本（四元组任一变化即失效），只缓存第一页是折中；"是否点赞"输在命中率；列表页只展示摘要，所以大文档（课程里的阈值是 1MB）不值得缓存，内存也要权衡，但课程强调这不等于所有情况都不缓存大对象。〔W06-3 p15〕分页第一页的判定写得很严格：只有 `offset == 0 && limit == 100` 才走缓存，先查缓存，未命中再查数据库并回写。〔W06-3 p8〕列表页只展示摘要，所以不需要把整个 Content 缓存下来。〔W06-3 p9〕

### 两个"提前写缓存"的套路
缓存不一定是读的时候才写，还可以预测性地提前写，套路有两个：
- **业务预加载**：认为创作者拿到列表后大概率点开第一条，返回列表后把第一篇放进缓存。预测性质，所以过期时间设得很短（1 分钟），规避预测失败带来的内存开销。〔W06-3 p12〕〔W06-3 p13〕实现上有一条纪律：预加载要不要异步，让**调用者决定**，别自己偷偷摸摸开 goroutine，把选择权交给调用者，可读性和可维护性都更强。〔W06-3 p14〕
- **发布时提前写**：新帖发布后预期立刻被访问，Publish 成功就直接写缓存。凡是有"制作库、线上库"区分的场景都适用；提前设置缓存失败不需要特别关心，多半是超时或偶发抖动。〔W06-3 p17〕〔W06-3 p18〕TTL 还可以按创作者是否大 V 区别对待，大 V 更长。〔W06-3 p20〕

过期时间的本质是让"接下来对该资源的访问都命中"：长 TTL 高命中低一致性，短 TTL 反之。〔W06-3 p20〕每处的 TTL 都该这样推出来，不是拍脑袋。

## 三、穿透、击穿、雪崩与 singleflight
缓存失效有三类经典事故，现象不同，对策也不同：

| 事故 | 现象 | 常用对策 |
|---|---|---|
| 穿透 | 查不存在的数据，每次都落库 | 缓存空值（短 TTL），或布隆过滤器 |
| 击穿 | 热点 key 过期，大量并发同时回源 | 合并回源（singleflight）或加互斥锁 |
| 雪崩 | 大批 key 同时过期，或缓存整体不可用 | TTL 加随机抖动，缓存失效降级 |

击穿值得跑一遍。热点 key 刚过期，100 个并发读同时回源，回源耗时 50ms：不合并时数据库挨 100 次打；`singleflight` 把"同一个 key、回源还没返回期间"的并发调用合并成一次。示例跑起来：

```go
package main

import (
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

func main() {
	for _, useSF := range []bool{false, true} {
		var mu sync.Mutex
		loads, results := 0, make([]string, 100)
		var sf singleflight.Group
		start, wg := make(chan struct{}), sync.WaitGroup{}
		load := func() (any, error) {
			mu.Lock()
			loads++
			mu.Unlock()
			time.Sleep(50 * time.Millisecond) // 模拟回源耗时
			return "value", nil
		}
		for i := range 100 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start // 100 个 goroutine 同时起跑
				var v string
				if useSF {
					val, _, _ := sf.Do("hot", load) // 同一 key 的并发回源合并成一次
					v = val.(string)
				} else {
					r, _ := load()
					v = r.(string)
				}
				mu.Lock()
				results[i] = v
				mu.Unlock()
			}()
		}
		close(start)
		wg.Wait()
		fmt.Printf("singleflight=%v 回源次数=%d 结果一致=%v\n", useSF, loads, results[0] == results[99])
	}
}
```

```text
singleflight=false 回源次数=100 结果一致=true
singleflight=true 回源次数=1 结果一致=true
```

〔实测〕热点 key 刚过期，100 个并发读，回源耗时 50ms：不合并时回源 100 次，`singleflight` 合并后回源 1 次，连跑三次结果相同，用 `go run` 里的 `start` 通道让 100 个 goroutine 同时起跑。合并的前提是这些调用在同一次回源还没返回时到达；回源很快或请求错开时，合并次数会多于 1。

另外两个对策一句话说清：缓存空值是对不存在的 key 也写一个短 TTL 的空占位，连查几次只回源一次；TTL 抖动是写入时加 `rand` 出来的一小段，让同一批 key 不会在同一时刻过期。对照 Java：Guava 的 `LoadingCache` 把"合并回源"内建在 `get(key, loader)` 里；Go 把它抽成 `singleflight.Group.Do`，能包任何函数，不止缓存，比如"同一篇文章只允许一个 goroutine 去算榜单"。

## 四、计数：一条不能破的纪律
阅读、点赞、收藏是三个典型计数。形态有差异：阅读数每次打开就加 1，关掉也不减；点赞和收藏可以取消；收藏还有收藏夹。〔W07-1 p2〕设计上它们要做成通用的：用 `biz + bizId` 标识任意资源（文章、评论、照片都行），类似的设计你在很多地方能见到。〔W07-1 p3〕〔W07-1 p6〕课程选择三套合并成一个 Interactive 服务（中小公司主流），不按服务拆分。〔W07-1 p4〕

### 数据库侧：必须原子自增
关键纪律：更新计数不能先读出来、加一、再写回，因为有并发问题；必须写 `read_cnt = read_cnt + 1`，MySQL 行级锁保证安全。〔W07-1 p7〕这句话值得跑一遍看它怎么咬人。两个请求同时给阅读数 41 加一，按"读加写回"交错执行：

```go
package main

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	// 复现"读出来加一再写回"的丢更新：两个请求同时读到 41
	cnt := int64(41)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v := cnt                          // 1. 读：两个 goroutine 都读到 41
			time.Sleep(10 * time.Millisecond) // 模拟业务耗时，放大交错窗口
			cnt = v + 1                       // 2. 写回：都写 42，一次更新被覆盖丢失
		}()
	}
	wg.Wait()
	fmt.Println("读加写回:", cnt)
	// 数据库里的正解：UPDATE SET cnt = cnt + 1，行级锁保证原子
	var good atomic.Int64
	good.Store(41)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			good.Add(1)
		}()
	}
	wg.Wait()
	fmt.Println("原子自增:", good.Load())
}
```

```text
读加写回: 42
原子自增: 43
```

两次自增，正确结果是 43，"读加写回"得到 42：第二个 goroutine 的写回把第一个的覆盖了，这就是丢更新（示例里的 `cnt` 故意不加同步，它本身是个数据竞争，只为复现交错）。放到数据库里，`read_cnt = read_cnt + 1` 是一条语句，在行级锁保护下"读和写"不会被别的事务插进来；"先读后写"是两个语句，中间的窗口谁都能进。Java 的 `AtomicLong.incrementAndGet`、Rust 的 `fetch_add` 是同一层语义，Go 单进程里对应 `atomic.Int64.Add`，跨进程对应数据库的自增表达式或 Redis 的原子命令。新帖没有记录时用 Upsert：不存在则插入，存在则自增，表上 `biz` 加 `bizId` 的联合唯一索引是关键，没有它 Upsert 判不了冲突。〔W07-1 p8〕

### 缓存侧：hash、HINCRBY 与一段 Lua
缓存侧用 Redis 的 hash，一个 key 存三个计数（read_cnt、like_cnt、collect_cnt）。〔W07-1 p9〕缺数据有两种情形：key 不存在（数据只在数据库），或者 key 存在而 field 不存在。后者不用管，`HINCRBY` 遇到 field 不存在会先当 0 再自增。〔W07-1 p10〕要处理的是前者：自增前先检查 key 是否存在，存在才自增，不存在就跳过、等后续查询回写。检查加自增是典型的"检查-做某事"场景，这类场景在并发下都不安全（检查完到动手之间，别的请求可能已经改了状态），所以用 Lua 脚本保证原子：〔W07-1 p11〕

```text
local key    = KEYS[1]
local cntKey = ARGV[1]
local delta  = tonumber(ARGV[2])
local exists = redis.call("EXISTS", key)
if exists == 1 then
  redis.call("HINCRBY", key, cntKey, delta)
  return 1
end
return 0
```

为什么放到 Redis 里就安全了：Redis 是单线程执行命令和脚本，Lua 脚本把"检查并且做某事"封装成一个整体操作，中间插不进别的命令。〔W04-2 p31〕同样的"检查-做某事"思想在验证码发送的 Lua 里还出现了一次（查 TTL 判断发送频率），可以对照着看。〔W04-2 p32〕这个设计的代价也要说清：按 DDD 的理论，这段逻辑本该放 CodeService 这类业务服务里，但 service 层解决不了并发问题，只好下沉到 Redis；缺点是换一个缓存（比如 memcached）就得重写一遍。〔W04-2 p37〕

### 一致性分析：算得清的账，可以不算
缓存有数据时，所有写请求走 Lua，都是对的。问题出在缓存没数据时：读请求回写缓存和写请求更新数据库并发交错，可能把旧值回写覆盖新值，也就是你把时序图上的箭头挪一下顺序就能看出的那几种场景。〔W07-1 p24〕〔W07-1 p25〕课程的立场是：**不需要解决**。〔W07-1 p26〕理由有两层：阅读数不准没关系，不会对用户产生坏影响；而且只有高并发的文章才会撞上这个交错，高并发文章基数本来就大，少一点点无所谓，低并发文章极小概率撞上。〔W07-1 p26〕这就是"基于业务理解的取舍"：金额不能这样，计数可以；反过来，需要彻底解决一致性的场景，本来就不该用缓存。〔W07-1 p27〕点赞是计数里的一个变体，要拆成两件事：记录"谁赞过"（可取消，一张 UserLikeBiz 表），和总数统计（Interactive 表）。取消再点赞用软删除，只更新一列 status，比删除加插入性能好，还保留数据。〔W07-1 p12〕〔W07-1 p15〕〔W07-1 p16〕

## 五、榜单：异步定时算，别实时扫
### 为什么不能实时算
热榜是首页高频接口，性能和可用性都要求极高。〔W09-1 p3〕直接实时算的难点是全表扫描加全局排序：扫描全表找点赞数和发表时间，逐条算分，再全局排序取前 50；千万级数据下磁盘、内存、带宽都扛不住。〔W09-1 p12〕方案是**异步定时计算**：每隔一段时间算一次，间隔可控，间隔越短实时性越好；计算时间可以长，但不能长到好几个小时。〔W09-1 p13〕再叠缓存设计（查询性能）和可用性设计（任何情况下都能拿到榜单）。〔W09-1 p13〕

### 算什么分：HackerNews 模型
课程选了最简单的 HackerNews 模型：`Score = (P-1) / (T+2)^G`，P 是点赞数，T 是发表以来的小时数，G 取 1.5；得分越高热度越高。〔W09-1 p5〕〔W09-1 p8〕Reddit 模型考虑赞成反对票差，微博只给了宽泛描述没有公式，国内平台大多如此。〔W09-1 p6〕〔W09-1 p7〕

### 怎么算：分批加小顶堆
计算流程：分批拉文章（batchSize）找到点赞数算 score，用一个数据结构维持前 100，比已有前 100 还大的进来、淘汰最小的，全部批次结束后堆里就是 TopN，最后装进 Redis。〔W09-1 p18〕维持 TopN 用**小顶堆**：父节点比子节点小，堆顶是当前第 N 名；堆满后新分数比堆顶大才替换。〔W09-1 p19〕

```go
package main

import (
	"container/heap"
	"fmt"
	"math"
	"time"
)

// score 就是课程里的 HackerNews 公式：(P-1) / (T+2)^1.5
func score(likes int64, postedAt, now time.Time) float64 {
	hours := now.Sub(postedAt).Hours()
	if hours < 0 {
		hours = 0
	}
	return float64(likes-1) / math.Pow(hours+2, 1.5)
}

type item struct {
	id    int64
	score float64
}

// topN 是一个小顶堆：堆顶是当前第 N 名
type topN []item

func (h topN) Len() int           { return len(h) }
func (h topN) Less(i, j int) bool { return h[i].score < h[j].score }
func (h topN) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *topN) Push(x any)        { *h = append(*h, x.(item)) }
func (h *topN) Pop() any          { old := *h; it := old[len(old)-1]; *h = old[:len(old)-1]; return it }

func main() {
	now := time.Now()
	// 按公式手算验证时间衰减：{点赞数, 多少分钟前}
	for _, tc := range []struct {
		likes  int64
		minAgo float64
	}{{80, 30}, {100, 60}, {1000, 7 * 24 * 60}} {
		s := score(tc.likes, now.Add(-time.Duration(tc.minAgo*float64(time.Minute))), now)
		fmt.Printf("%d赞 %v前: score=%.2f\n", tc.likes, dur(tc.minAgo), s)
	}
	// 三批数据，用小顶堆维持 Top3；堆满后比堆顶大才替换
	n, h := 3, &topN{}
	batches := [][]item{{{1, 5}, {2, 20}, {3, 8}, {4, 30}}, {{5, 25}, {6, 2}, {7, 18}}, {{8, 40}, {9, 1}}}
	for _, b := range batches {
		for _, it := range b {
			if h.Len() < n {
				heap.Push(h, it)
			} else if it.score > (*h)[0].score {
				heap.Pop(h)
				heap.Push(h, it)
			}
		}
	}
	fmt.Print("Top3:") // 出堆是堆顶先出，即分数从低到高
	for h.Len() > 0 {
		fmt.Printf(" %d(%.0f)", (*h)[0].id, (*h)[0].score)
		heap.Pop(h)
	}
	fmt.Println()
}

func dur(min float64) string {
	if min >= 24*60 {
		return fmt.Sprintf("%d天", int(min/1440))
	}
	if min >= 60 {
		return fmt.Sprintf("%d小时", int(min/60))
	}
	return fmt.Sprintf("%d分钟", int(min))
}
```

```text
80赞 30分钟前: score=19.99
100赞 1小时前: score=19.05
1000赞 7天前: score=0.45
Top3: 5(25) 4(30) 8(40)
```

〔实测〕按 HackerNews 公式手算并运行验证（把上方示例存成 `main.go` 后 `go run main.go`）：30 分钟前的 80 赞约 19.99，1 小时前的 100 赞约 19.05，而 7 天前的 1000 赞只有约 0.45；老帖点赞再多也排不进前面，这就是时间衰减。Top3 部分演示了小顶堆的替换逻辑：8(40) 进堆时挤掉的是堆顶的最小者，不是最大的。三个工程细节：**七天优化**是认为七天之前的数据不用再算，课程的判断是它们必然进不了前 100〔W09-1 p23〕；按这个思路，如果批次按发表时间从新到旧拉取（课程页面没明说），批次里最后一条早于截止时间就可以中断循环。这是业务假设，点赞极多的老帖会被漏掉；**写 Redis 的 TTL 要比计算间隔长**，留够重试时间，缓存字段要满足查询接口、一个不少，但用摘要不存全文〔W09-1 p24〕；**定时调度**用 `time.Ticker` 或 cron 表达式（`github.com/robfig/cron/v3`），〔W09-1 p14〕〔W09-1 p15〕另外 `select` 里的 `break` 只退出 `select`、退不出外层 `for`，要用标签或 `return` 跳出。

### 读侧：本地缓存加 Redis，读序与容错
查询侧追求极致性能要引入本地缓存。组装有两种思路：缓存层提供 Redis 实现加本地实现再配一个装饰器同时操作两者；或者直接在 Repository 上组合两个实现，逻辑不复杂时选后者，开发快。〔W09-1 p33〕榜单数据只有一份，本地缓存不需要 key-value 结构，用泛型原子类 `atomicx.Value[T]` 存就够了。〔W09-1 p34〕读写都优先操作本地缓存：读先本地，没有再读 Redis 并回写本地；写先写本地，再写 Redis。理由是本地缓存本质是 CPU 运算，基本不可能出错。榜单结果本身不存数据库。〔W09-1 p24〕〔W09-1 p35〕可用性上有两招值得记：Redis 崩溃时回退本地缓存，且不再检查它是否过期，宁可给旧榜单也不给错误页；Redis 缓存可以设成永不过期（或极大值），只有定时任务成功时才更新它，这样数据库故障不会传导成榜单故障。〔W09-1 p36〕〔W09-1 p38〕再往上是 failover：前端拿不到榜单就重发请求，只要不是所有节点都没有本地数据且 Redis 全崩，总能出一个榜单。〔W09-1 p37〕

## 六、分布式锁：谁抢到谁执行，且要有续约
### 问题：多实例都在算榜单
榜单任务部署多实例后会同时跑。结果相同问题不大，〔W09-2 p4〕但仍要解决：任一时刻只有一个节点运行。方案是节点先抢分布式锁，抢到才执行。〔W09-2 p5〕〔W09-2 p6〕锁放在 **Job 层**而不是 Service 层："全局只能有一个人计算热榜"不是业务逻辑，"Job 调度唯一"才是；Service 保持业务纯净。〔W09-2 p8〕`Lock` 返回的 error 要处理。〔W09-2 p7〕按这个库的设计，错误大致分三类（课程页面没有展开，以下是库的约定，未逐项核对）：整体超时、加没加上无人知道；超过重试次数没抢到，肯定没成功；Redis 通信问题。课程给了两个版本，递进关系要看清：

- **执行期间持锁**：锁的过期时间等于任务超时，没有开续约，解锁失败也不重试（最多超时自动释放）。缺陷是只管"同一时刻"，一个实例算完释放后，别的实例立刻抢到又算一遍。〔W09-2 p9〕〔W09-2 p10〕
- **启动就拿锁、不释放**，加自动续约：本地记录的锁为 nil 就尝试抢，抢到则开启自动续约，没抢到说明大概率别的节点在算。〔W09-2 p11〕〔W09-2 p12〕〔W09-2 p13〕续约失败时应当放弃持锁身份、清空本地记录并回去重新抢，这是常见做法，课程页面没有写。

### 最容易漏的一条：释放必须验 token
锁的语义里最容易漏的是**释放必须验 token**。场景：A 拿到锁后业务超过了 TTL，锁已过期并被 B 拿走；A 恢复后迟到的释放，如果只是 `DEL key`，删掉的是 B 的锁，C 又会趁虚而入，两个节点同时执行。正解是"检查 token 再删除"，在 Redis 里是一段 Lua，保证比对和删除原子完成。用内存版把这个事故复现出来：

```go
package main

import (
	"fmt"
	"sync"
	"time"
)

// Store 是一个带过期时间的分布式锁的内存教学版，接口与 Redis 版一致
type Store struct {
	mu  sync.Mutex
	m   map[string]lockEntry
	now func() time.Time
}

func NewStore() *Store { return &Store{m: map[string]lockEntry{}, now: time.Now} }

type lockEntry struct {
	tok   string
	until time.Time
}

// TryLock 抢锁：SET NX EX，key 不存在才写入
func (s *Store) TryLock(key, tok string, ttl time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.m[key]; ok && e.until.After(s.now()) {
		return false // 已被人持有且未过期
	}
	s.m[key] = lockEntry{tok: tok, until: s.now().Add(ttl)}
	return true
}

// UnlockSafe 释放必须验 token：A 的锁过期被 B 拿走后，A 迟到的释放不能删掉 B 的锁。
// Redis 版就是一段 Lua：if get(key)==tok then del(key)
func (s *Store) UnlockSafe(key, tok string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.m[key]; ok && e.tok == tok {
		delete(s.m, key)
		return true
	}
	return false
}

func main() {
	s := NewStore()
	fmt.Println("A TryLock:", s.TryLock("ranking", "tokA", 100*time.Millisecond))
	fmt.Println("B TryLock:", s.TryLock("ranking", "tokB", 100*time.Millisecond))
	time.Sleep(150 * time.Millisecond) // A 业务超时：锁过期
	fmt.Println("过期后 B TryLock:", s.TryLock("ranking", "tokB", time.Second))
	fmt.Println("A 迟到释放:", s.UnlockSafe("ranking", "tokA")) // token 对不上
	fmt.Println("B 正常释放:", s.UnlockSafe("ranking", "tokB"))
}
```

```text
A TryLock: true
B TryLock: false
过期后 B TryLock: true
A 迟到释放: false
B 正常释放: true
```

〔实测〕用内存版演示锁的全生命周期：A 抢到锁、B 抢不到；A 超时后 B 抢到；A 迟到的释放返回 false，B 的释放返回 true，连跑三次输出相同，命令在示例里 `go run` 即可复现。内存版的 `mu` 只模拟"比对加删除"的原子性，分布式语义靠 token 和 TTL 表达。

还有一条纪律：抢锁失败不要当作错误上报，抢不到说明别的节点在做，是正常情况，代码里直接 `return nil`。〔W09-2 p12〕另一条思路是用 MySQL 做抢占式调度：任务表加状态和版本号，乐观锁抢占（`UPDATE ... WHERE id=? AND version=?`，`RowsAffected==1` 才算抢到），抢到的实例不断更新时间戳证明活着（续约），崩溃后无人续约，别人才能接手。〔W09-2 p17〕〔W09-2 p18〕〔W09-2 p20〕K8s 的 CronJob 也是同一问题的另一种解：学会写配置文件就行。〔W09-2 p26〕

## 七、消息队列：解耦、削峰和它带来的重复
### 为什么引入：把读请求里的写挪走
回到开头那个现象。改造思路：用户读文章时，`ArticleService` 发一条"某用户读了某文章"的领域事件（`ReadEvent{Aid, Uid}`），发完立刻返回；统计阅读数的 InteractiveService 作为消费者订阅这个消息、批量落库，别的业务方（比如阅读历史）也可以订阅同一条消息，对原接口零影响。〔W07-2 p38〕〔W07-2 p39〕〔W07-2 p40〕〔W07-2 p47〕发送失败只记日志、不影响读文章，是统计类场景常见的取舍（课程页面没有明说）。这就是解耦加削峰：读路径上只剩一次发消息，数据库写挪到消费者侧慢慢做。

### Kafka 五个概念与四条语义
Kafka 的基本概念：producer、consumer、broker（一个 broker 就是一个消息队列进程）、topic 与分区、消费者组。一个业务一个 topic，一个 topic 多个分区。〔W07-2 p4〕〔W07-2 p5〕要背的语义，每条都决定了你系统的行为：

1. **分区内有序，全局无序**。要全局有序就单分区；要"同一业务有序"就让同一业务的消息进同一分区，即按 key 哈希选分区。〔W07-2 p9〕〔W07-2 p8〕
2. **消费者组内，一个分区最多一个消费者**。N 个分区最多 N 个消费者有效，多出来的闲着；消息积压时不能靠无限加消费者提速，这是面试热点。〔W07-2 p10〕〔W07-2 p11〕
3. **`acks` 三档**：0 是发了不管（TCP 确认即可），1 是写入主分区，-1 是同步所有 ISR；性能递减、可靠性递增，要性能选 0，要消息不丢失选 -1。〔W07-2 p22〕〔W07-2 p23〕
4. **主从分区分散在不同 broker**，主分区之间不在一起、主从也不在一起，目标单 broker 崩溃影响最小。ISR（In Sync Replicas）就是跟上了节奏的从分区。〔W07-2 p7〕〔W07-2 p24〕

Go 客户端三家：Sarama 用户最多（现在在 IBM 名下），segmentio/kafka-go 没大缺点，confluent 版需要 cgo、跨平台差。课程选 Sarama，因为你出去工作遇到它的概率最大。〔W07-2 p17〕积压了怎么办：增加分区现实中基本不可能；异步消费（goroutine 并发消费最后合并提交）；批量消费（凑一批一次处理，下面第八部分的阅读数就是这么改的）。〔W07-2 p54〕

### RabbitMQ：另一种模型
RabbitMQ 是交换机把消息路由到队列、消费者确认的模型，不是 Kafka 那种分区日志，上面的分区与 `acks` 语义不适用。资料代码里值得记三条：

- 消息持久化需要**队列 durable 加消息 persistent 两个条件同时满足**，即使如此也不保证立即落盘（中间有缓冲，broker 突然挂掉缓冲里的数据会丢）。〔C:go_frame/mq/rabbitmq/producer/direct.go:33-36〕
- `autoAck=false` 时消费后必须显式 `Ack`；没 ack 的消息在 channel 关闭后会重新放回队列尾部，所以**消费者要能容忍重复**。〔C:go_frame/mq/rabbitmq/consumer/direct.go:40〕
- `Qos(prefetch)` 限制单个消费者未 ack 的消息数，值越小负载越均衡，但单消费者吞吐越低。〔C:go_frame/mq/rabbitmq/consumer/direct.go:61-65〕

## 八、幂等：重复无害
### 为什么必须有
重复的来源数得出来：Kafka 消费端如果按"先处理、后提交位点"的常规做法，语义就是至少一次，处理完还没提交位点就崩溃，重启后同一条消息再来一遍（生产端重试也可能重复发）；RabbitMQ 漏 ack 的消息 channel 关闭后重新投递；网络重试、前端重复点击同理。所以消费逻辑的基本假设是：**同一条消息会到达两次**。通用做法：给每个操作一个**业务幂等键**（事件 ID，不是 offset），处理前先查"是否处理过"，处理和记录放在**同一个数据库事务**里（前提是处理的结果也落在这个库），崩溃在事务边界两侧都不会造成"处理了但没记录"。

```go
package main

import (
	"fmt"
	"hash/fnv"
	"sync"
)

type Msg struct {
	ID  string // 事件 ID，业务幂等键
	Key string // 业务键，按它哈希进分区
}
type Consumer struct {
	mu   sync.Mutex
	seen map[string]bool // 生产里是一张带唯一索引的表
	cnt  map[string]int  // 记录每个业务键被成功处理几次
}

// Handle 模拟"至少投递一次"语义下的消费者：同一条消息可能来两次
func (c *Consumer) Handle(m Msg) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seen[m.ID] { // 处理前先查"是否处理过"
		fmt.Println("  跳过重复", m.ID)
		return
	}
	c.seen[m.ID] = true // 处理与记录在同一处（生产里是同一事务）
	c.cnt[m.Key]++
}

// partition 模拟 Kafka 按 key 哈希选分区
func partition(key string, n int) int {
	h := fnv.New32a()
	h.Write([]byte(key))
	return int(h.Sum32()) % n
}

func main() {
	// 3 条属于同一实体的消息，按 key 进同一分区，分区内保持发送顺序
	msgs := []Msg{{"e1", "order:42"}, {"e2", "order:42"}, {"e3", "order:42"}}
	partitions := make([][]Msg, 2)
	for _, m := range msgs {
		p := partition(m.Key, 2)
		partitions[p] = append(partitions[p], m)
		fmt.Printf("%s -> 分区%d\n", m.ID, p)
	}
	c := &Consumer{seen: map[string]bool{}, cnt: map[string]int{}}
	for _, ps := range partitions {
		for _, m := range ps { // 分区内逐条消费
			c.Handle(m)
		}
	}
	c.Handle(Msg{"e3", "order:42"}) // 模拟位点未提交：e3 被重新投递一次
	fmt.Println("order:42 处理次数:", c.cnt["order:42"])
}
```

```text
e1 -> 分区1
e2 -> 分区1
e3 -> 分区1
  跳过重复 e3
order:42 处理次数: 3
```

〔实测〕3 条属于同一实体的消息按 key 哈希进同一分区（FNV 哈希稳定，连跑多次分区号不变），保持发送顺序；模拟位点未提交导致 e3 重新投递，被 seen 表跳过，实体的处理次数仍是 3，用 `go run` 直接复现。真实系统里 `seen` 是一张带唯一索引的处理记录表，插入冲突即重复。

### 四种实现，按成本排序
**天然幂等**（设置为某值比累加安全，执行多少遍结果一样）；**唯一索引去重**（处理记录表加唯一约束，重复插入失败就是重复消息，是幂等键方案的落点）；**状态机**（只允许合法的状态迁移，重复的迁移被拒绝）；**乐观锁加版本号**（更新时带版本条件，第六部分 MySQL 抢占调度用的就是它）〔W09-2 p20〕。第四部分计数的"原子自增"不在这四条里：它保证的是并发下不丢更新，但同一条消息来两遍还是会加两。**去重（幂等）和原子（不丢）是两个正交的要求，都要满足**。

### 同步改异步，值不值
把同步写改成发消息，换来解耦和削峰，代价是：延迟变大、要处理重复和乱序、要监控积压、失败要有补偿。判断标准回到业务：阅读数、点赞数这类可以接受不准的写，适合异步（先 Kafka，消费者批量落库，一次事务提交一批比逐条提交快一个数量级）；〔W07-2 p45〕金额、库存这类强一致的写，别轻易异步。

抽奖项目给了强一致场景的另一种答案：实时库存放 Redis 维护（Redis 承受的 QPS 比 MySQL 高一到两个数量级），扣库存用单线程下的原子 `DECR`，减完发现负数就是没库存，本次抽奖作废重试；**只有减库存成功才把结果返回前端**，和秒杀是一个道理。〔C:go_frame/lottery/readme.md:57-58〕这里的原子性不靠数据库行锁也不靠 Lua，靠 Redis 单线程本身，和第四部分 Lua 脚本的安全性是同一个来源。〔C:go_frame/lottery/handler/gift.go:33-58〕

## 小结
1. 缓存定位在 repository 层，屏蔽 TTL、key 结构、序列化三件事；service 无感知，多注入一个依赖。
2. 回源有两种写法：出错也回源（可用性优先，课程推荐）与只有确认未命中才回源（保数据库）。
3. 缓存什么按"命中率、一致性成本、内存"排序想；只缓存第一页、预加载短 TTL、发布提前写是三个可复用模式。
4. 击穿用 singleflight 合并回源；穿透缓存空值；雪崩 TTL 加抖动。
5. 计数必须原子自增 `cnt = cnt + 1`，不能读出加一再写回；"检查-做某事"用 Lua 保证原子。一致性账可以不算：高并发文章基数大，低并发撞不上，业务允许不准就不解决。
6. 榜单异步定时算：分批加小顶堆维持 TopN，七天优化砍计算，读侧本地缓存加 Redis 双层，Redis 崩溃回退旧榜。
7. 分布式锁三个要点：锁在 Job 层、启动拿锁加续约、释放必须验 token。
8. Kafka 分区内有序、一分区至多一消费者、acks 三档、主从分散；RabbitMQ 是交换机路由模型，语义不通用。幂等键加事务是重复投递的兜底，去重和原子是两个正交要求。

## 常见坑
1. 缓存空值不设短 TTL，数据后来有了却一直读到空。
2. 双写路径写缓存失败后不清理，读到旧值；优先"写库后删缓存"，失败打日志不回滚。
3. 预加载在 repository 里偷偷 `go func`，调用方失去选择权，测试也难写；异步留给调用者。
4. 分布式锁释放不验 token，A 的迟到释放误删 B 的锁；锁没有续约，任务超时后两个节点同时执行。
5. 抢锁失败当作错误上报、触发告警；抢不到是正常情况。
6. 消费者没有幂等，重复投递导致重复入账；用 offset 当幂等键，位点重置时就乱套，用业务事件 ID。
7. 分区数小于消费者数，多出来的闲着；以为加消费者能解决积压。
8. `time.Ticker` 不 `Stop`：Go 1.23 之前定时器永远不会被回收，之后才可被 GC；循环退出时 `defer t.Stop()` 仍是好习惯。〔W09-1 p14〕

## 判断题与答案
1. 为什么计数更新要用 `cnt = cnt + 1` 而不是读出来加一再写？2. 缓存"只缓存第一页"的理由是什么？
3. A 的任务超过锁的 TTL 后，A 迟到的释放为什么必须验 token？4. 一个 topic 有 3 个分区、5 个消费者（同一消费者组），实际有几个在工作？

**答案**

1. 读加写回是"读-改-写"三步，中间能插进别的写，后写的把先写的覆盖，更新丢失；`cnt = cnt + 1` 在行级锁内一条语句完成，没有插队的窗口。第四部分示例里 42 对 43 就是这个差别。2. 分页结果的筛选、排序、偏移、页大小任一变化缓存就失效，整表缓存命中率为零；用户大多只看第一页，只缓存第一页（且只存摘要）是命中率和失效成本的平衡。
3. 锁已过期被 B 持有，不验 token 的释放会删掉 B 的锁，第三个节点就能趁虚而入，两个节点同时执行；验 token 让释放只对自己名下的锁生效，Redis 里用 Lua 保证比对加删除原子。4. 3 个。一分区至多一消费者，3 个分区最多 3 个消费者有效，多出来的 2 个闲着；积压时加消费者没用；课程给的办法是异步消费和批量消费，加分区在现实中基本做不到。

## 在项目里
- `dnsproxi-go/cache/` 是一个真正的缓存实现：`cache.Cache` 的 `Get` 与 `Put` 读写、`cache.ExpireRound` 与 `cache.RunExpirer` 的主动过期（每轮抽样回收，和 Redis 的主动过期同思路）、`cache.FreqSketch` 做 TinyLFU 准入（count-min sketch 估频率，准入不过就拒之门外）。它和业务缓存的区别是容量有限，要关心"淘汰谁"和"准入谁"；业务缓存通常容量按富余设计，只需要关心"存什么"和"TTL 多长"。`reply.go` 的 `BufPool` 是 `sync.Pool` 复用 4KB 应答缓冲，属于进程内的对象缓存，"复用昂贵资源"的动机相通。
- minifeed 的 `rank.Rank.Refresh` 是"算分加读侧零锁"的单机版：`rank.Source` 给候选集，排序后装进 `atomic.Value`，`Top(n)` 读快照不加锁，对应第五部分榜单读侧的形状；数据未过期时再次调用 `Refresh` 直接跳过，并发调用由 `mu` 合并成一次。注意 `Source.Posts` 不返回 error，所以代码里并没有"刷新失败保留旧榜"的分支，那只是注释里的设想。`counter` 包的 ticker 批量刷库对应第八部分"消费者凑批落库"。消息队列和分布式锁在这两个项目里没有对应物。

## 想深入去哪查
- 缓存：W06-3（分页缓存、预加载、提前写、淘汰策略）、W04-1（缓存集成位置、两种回源写法）、W04-2（验证码场景的"检查-做某事"Lua）、W07-1（计数的 Lua 脚本与一致性分析）。榜单与调度：W09-1（HackerNews 模型、小顶堆、本地缓存、可用性复盘）、W09-2（分布式锁、续约、MySQL 抢占、K8s CronJob）。
- Kafka 与 RabbitMQ：W07-2（五概念、分区、acks、Sarama、批量消费）、`C:go_frame/mq`（RabbitMQ 持久化、Ack、Qos、Exchange 类型、Kafka 写入）。抽奖与并发扣库存：`C:go_frame/lottery`（原子 DECR、减库存成功才返回）。
- 旧长版：`notes/p6-capstone-dnsproxi.md`（dnsproxi 的缓存逐出、TinyLFU、主动老化）；`notes/p37-cache-counter-rank.md`（本章素材的单机等价物与练习位）。
