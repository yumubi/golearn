# 16 稳定性：限流、熔断、降级、重试与超时

> 详细程度：深入 · 约 40 分钟
> 材料：W14-1、W14-2、W04-6、C:dachang/algorithm（令牌桶）· 旧长版：无整章对应，相关片段在 `notes/p10-grpc.md`（拦截器）、`notes/p14-pipelines.md`（并发上限）
> 前置：04 接口与组合（装饰器、中间件）、07 并发二（context）

## 这一节要建立的认知

稳定性治理可以压缩成一条故障时间线：**预防、检测、处理、恢复**。限流是预防，不管有没有故障，到阈值就限；熔断、降级是检测到下游或自己撑不住之后的处理；恢复是退出故障机制、回到正常流程。〔W14-1 p5〕

选型的经验顺序：**保可用靠降级，快恢复靠熔断，保部分请求正确靠限流**；实际使用频率是限流大于降级大于熔断，因为熔断破坏性也很强（资料作者的原话是"也很强，所以比较少用"）。〔W14-1 p28〕

另一半认知是"放在哪"：**服务端**放限流和熔断，保护自己不被打死；**客户端**放超时、重试、熔断、failover，保护自己不被下游拖死。两端都要有，缺一端，故障就会沿调用链放大。

## 和你已知语言的差异

| 能力 | Java | Rust | Go 生态 |
|---|---|---|---|
| 限流 | Guava RateLimiter、Sentinel | tower 的 `RateLimit`、`ConcurrencyLimit` 层 | `golang.org/x/time/rate`（令牌桶），或自己写 |
| 熔断 | Resilience4j、Sentinel | 第三方库 | `sony/gobreaker`、go-kratos 的 aegis 子项目〔W14-2 p12〕 |
| 超时 | 各框架配置 | tower 的 `Timeout` 层 | `context.WithTimeout`，gRPC 自动传播 deadline |
| 接入点 | Filter、Interceptor | tower `Layer` | HTTP 中间件、gRPC 拦截器、装饰器 |

Go 的特点是**没有统一框架**：算法几十行就能写完，难点在接入位置和参数怎么定。下面的代码都是自己写的内存版，时钟可注入，所以测试里不需要 `Sleep`。

## 一、限流先问三个问题

1. **限谁**：整个应用、某个服务、某个接口，还是某个业务对象（用户、IP；VIP 可以不限）。单机限还是集群限。〔W14-1 p19〕
2. **限什么**：并发数，还是速率。两者不一样：计数器限流（请求进来加一、响应出去减一）限的是**并发**。阈值 100、平均耗时 50ms，实际能放行约 2000 QPS，因为每个请求只占 50ms。〔W14-1 p21〕
3. **被限的请求去哪**：直接拒绝、转异步、返回默认值，或者通知客户端换节点重试。〔W14-1 p18〕

计数器在 Go 里一个带容量的 channel 就够了：`Acquire` 用 `select` 加 `default` 非阻塞地往 `chan struct{}` 里塞一个值，塞不进去就是超限；`Release` 取出一个值。容量 2 时连续三次 `Acquire` 得到 `true true false`，`Release` 之后再来一次又是 `true`。作者认为这种最简单的限流效果不比后面几种差。〔W14-1 p21〕

## 二、速率限流的四种算法

资料列出的清单是：计数器、固定窗口、滑动窗口、令牌桶、漏桶，"效果差不多，随便选"。〔W14-1 p20〕但它们的行为差别值得看一眼。

### 固定窗口的临界突刺

固定窗口把时间切片，每片内计数。问题在边界：上一片末尾和下一片开头各放满，短时间内放过的请求可达阈值的两倍。〔W14-1 p24〕

阈值 100/秒，第 0.9 秒打 100 个，第 1.1 秒再打 100 个：

```text
fixed:   100 + 100 = 200 in 0.2s
sliding: 100 + 0 = 100 in 0.2s
```

固定窗口的实现只有一行关键逻辑：用 `UnixNano()/窗口大小` 当槽号，槽号变了就清零计数。滑动窗口的窗口始终是"当前时刻往前回溯一个窗口大小"，用时间戳队列实现，没有这个问题，代价是要存时间戳。〔W14-1 p23〕

### 令牌桶：允许攒出突发

令牌桶定速往桶里放令牌，请求取令牌，取不到就拒绝或等待。桶满后多余令牌丢弃，所以空闲一段时间能攒出一批突发额度。〔W14-1 p25〕

```go
func (b *TokenBucket) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.clk.Now()
	b.tokens = min(b.cap, b.tokens+now.Sub(b.last).Seconds()*b.rate)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
```

速率 10/秒、容量 5、初始满桶：

```text
burst at t=0: 5
after 300ms: 3
```

一开始连续 10 个请求只放过 5 个（桶里攒的），过 300ms 后补了 3 个令牌所以放过 3 个。资料里的手写版把令牌数做成可以为负、先补后扣，思路相同，但**不是并发安全的**，生产用 `x/time/rate`。〔C:dachang/algorithm/rate_limit.go:6-46〕

### 漏桶：把输出削成匀速

漏桶的本质是**整形**：请求排队，以恒定间隔流出，不允许积压突发。实现上不需要真的开队列，给每个请求算一个"可以放行的时刻"即可：`next` 记录下一个出水时刻，每个请求的等待时间是 `next - now`，放行后 `next` 加一个间隔，等待超过 `maxWait` 就拒绝。间隔 100ms、最长等 250ms，连续来 5 个请求：

```text
req0 wait=0s ok=true
req1 wait=100ms ok=true
req2 wait=200ms ok=true
req3 wait=0s ok=false
req4 wait=0s ok=false
```

**怎么选**：令牌桶允许积压、能扛突发；漏桶绝对匀速、不允许突发；资料的结论是大多数时候无脑选令牌桶。〔W14-1 p27〕需要保护下游、要求匀速调用第三方接口时才选漏桶。

**阈值怎么定**：

- 有压测：找三个点。A 点性能最好、资源利用率高；B 点是将崩的临界；C 点吞吐最高。选 A 性能最好，选 B 能扛更高并发但响应已变慢，选 C 吞吐最高，按目标取舍；无论选哪点都要留余量。〔W14-2 p14〕〔W14-2 p15〕
- 没有压测：看线上观测数据，或者手算。例如一次调用要两次 DB 查询各 10ms，共 20ms，4 核机器上 `1000/20×4 = 200 QPS`，估值上调并留余量。〔W14-2 p15〕

**集群限流**：单机限流要乘以节点数才是集群总量，节点数变化时会漂移；要精确就把计数放进 Redis（`INCR` 加 `EXPIRE` 实现固定窗口，滑动窗口要 Lua 脚本）。W04-6 把限流抽成通用 `Limiter` 接口 `Limit(ctx, key) (bool, error)`，后端用 Redis 限 3000/s，多一次网络往返换来全局准确。〔W04-6 p6-7〕

## 三、熔断：三态机

熔断解决的是**级联故障**：下游崩了，调用它的服务线程和连接被占满，也跟着崩，一路传上去。要点是检测、处理、恢复三件事。〔W14-1 p12-13〕

三个状态：闭合（全部放行）、开放（全部拒绝）、半开放（放一部分探针，看是否恢复）。〔W14-1 p13〕

`Allow` 里冷却期满时从开放态进入半开态，并限制同时在飞的探针数不超过 `probes`。`Done(err)` 负责记录结果：闭合态下连续失败达阈值就跳闸；半开态下探针全部成功回到闭合，任何一次失败立刻重新跳闸。参数 `maxFails=3`、冷却 5 秒、探针 2 个时的行为（略去一行普通放行）：

```text
fail 1                 allow=true  state=closed
fail 2                 allow=true  state=closed
fail 3 -> open         allow=true  state=open
during cooldown        allow=false state=open
after cooldown probe 1 allow=true  state=half-open
probe 2 -> closed      allow=true  state=closed
fail again             allow=true  state=closed
fail again             allow=true  state=closed
fail again             allow=true  state=open
probe fails -> reopen  allow=true  state=open
```

几个设计取舍：

- **恢复不能一把放开**。固定时间等待后直接全量放行会抖动，通用做法是试探：间隔放一个请求，正常就加大流量，失败就退回，直到 100%。〔W14-1 p8-9〕
- **判据**：这里用的是连续失败次数。更常见的是错误率、响应时间增长，静态阈值靠压测和经验，动态阈值靠硬件和服务指标。〔W14-1 p6〕资料也坦白：很复杂的动态判定在实践里很少用，更多是宣讲和面试时用。〔W14-1 p29〕
- **熔断器返回的错误也要被调用方当成失败**：接入 aegis 时，`Allow` 返回错误也要 `MarkFailed`，否则统计失真。〔W14-2 p12〕
- 不要让本地的 `ErrOpen` 被上层的重试逻辑当成普通失败再重试，否则等于对熔断器发起风暴。

## 四、降级：尽力给响应

限流、熔断、降级的"处理"本质上是同一件事：执行一段特殊代码，边界并不分明；熔断和限流直接返回错误，降级则是**尽力给响应**，另外还可以同步转异步，或让客户端换节点。〔W14-1 p7〕〔W14-1 p18〕降级的做法是预先配好默认响应，或者只走资源少、计算快的"快路径"。〔W14-1 p14〕

最典型的是缓存降级：正常时先查 Redis，没有再查 DB 并回写；降级时只查 Redis，查不到直接返回错误或默认值。好处有三个：应用负载骤降，并发上限由 Redis 决定，数据库被保住（少数几个 DB 查询就会严重拖累并发）。〔W14-1 p15〕

降级难以通用，因为"降不降、怎么降、降到哪"都和业务强相关。资料的做法是：由限流器或熔断器判定，打一个标记，业务代码据此走特殊逻辑；下面用 context 携带这个标记。〔W14-2 p13〕

```go
type degradedKey struct{}

func WithDegraded(ctx context.Context) context.Context { return context.WithValue(ctx, degradedKey{}, true) }
func Degraded(ctx context.Context) bool                { v, _ := ctx.Value(degradedKey{}).(bool); return v }
```

`load` 里缓存未命中时：正常上下文走慢路径得到 `from-db:2`，降级上下文直接返回 `degraded: cache miss for 2`；缓存命中时两者都返回 `hit`。跨服务的降级是同一个思路放大：资源不足时停增删改保查询，更高级的是集群层停边缘业务保核心、读写分组停写保读。〔W14-1 p16〕

## 五、超时与重试

### 超时：deadline 随 context 传递

单次调用超时用 `context.WithTimeout`。链路超时的关键是：整条链路共用一个总预算，下游拿到的是**剩余时间**。〔W14-2 p19〕Go 里不需要手动算，context 的 deadline 取父子中较小的：

```go
parent, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
defer cancel()
time.Sleep(50 * time.Millisecond)
d1, _ := parent.Deadline()
fmt.Println("remaining ~", time.Until(d1).Round(10*time.Millisecond))
child, cancel2 := context.WithTimeout(parent, 2*time.Second) // 想放宽，没用
defer cancel2()
d2, _ := child.Deadline()
fmt.Println("child deadline == parent deadline:", d1.Equal(d2))
```

```text
remaining ~ 150ms
child deadline == parent deadline: true
```

gRPC 会把 deadline 随请求带到服务端，服务端 ctx 里已经是剩余时间，零配置。〔W14-2 p20〕超时值怎么定：理论上由用户体验决定，实操上看接口 999 线或手算；首页应该在 100ms 内，1 秒是用户明显感到卡顿的关键节点。〔W14-2 p21〕

### 重试：退避加抖动，并且要幂等

重试看似简单，三件事做错一件都会放大故障：

```go
func Retry(ctx context.Context, attempts int, base, maxDelay time.Duration,
	jitter func() float64, sleep func(context.Context, time.Duration) error,
	fn func(context.Context) error) error {
	var err error
	for i := 0; i < attempts; i++ {
		if err = fn(ctx); err == nil {
			return nil
		}
		if i == attempts-1 {
			break
		}
		d := min(maxDelay, base<<i)
		d = time.Duration(float64(d) * jitter())
		if serr := sleep(ctx, d); serr != nil {
			return serr
		}
	}
	return err
}
```

`jitter` 固定返回 1 时，5 次尝试得到的退避序列是：

```text
tries: 5 delays: [100ms 200ms 300ms 300ms] err: fail
```

指数增长到上限 300ms 封顶。生产里 `jitter` 取 0 到 1 的随机数，让一批同时失败的客户端不会同时重试。

1. **退避**：立刻重试只会让已经过载的下游更过载。
2. **抖动**：没有随机性，所有客户端会在同一时刻再次撞上去。
3. **幂等前提**：只有幂等的调用才能无脑重试。幂等接口应尽量返回上一次的结果：支付预下单超时重试，如果第一次已经创建，第二次会得到"重复"错误但仍能拿到二维码地址。〔W15-1 p30〕

还有两条边界：用户体验相关的错误（`DeadlineExceeded`、`Canceled`）不要重试，直接返回；〔W04-6 p25-26〕重试会放大流量，一个请求在链路上产生多次调用时，流量翻倍系统负载不止翻倍，这是雪崩的来源。〔W14-1 p10〕gRPC 内置的 `retryPolicy` 可以在 service config 里配置最大尝试次数、退避和可重试状态码，字段都是手写 JSON，要仔细。〔W13-2 p29-30〕

## 六、隔离：别让一个下游吃掉全部资源

雪崩的根因是共享资源被一个慢下游耗尽，对策是**舱壁**：每个下游一个计数器，满了快速失败，不影响别的下游；资料的面试整套方案也列了"分组隔离"。〔W14-2 p30〕

## 七、接入点：中间件、拦截器、装饰器

同一个限流器，换一层皮就能接入不同协议。gRPC 拦截器如下，用 `go vet` 编译过（依赖仓库已有的 `grpc v1.84.0`）；HTTP 中间件同构，`Allow` 失败时返回 `http.StatusTooManyRequests`，写法见 04 的中间件：

```go
// gRPC 服务端拦截器：被限流返回 ResourceExhausted
func LimitInterceptor(l Limiter) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
		if !l.Allow() {
			return nil, status.Error(codes.ResourceExhausted, "rate limited")
		}
		return h(ctx, req)
	}
}
```

拦截器通过 `grpc.ChainUnaryInterceptor` 链式注册。〔W14-2 p6〕〔W14-2 p8〕限流粒度分应用级、服务级（`info.FullMethod` 前缀）、业务级三层，业务级在拦截器里做类型断言取 key，所有请求都要付断言成本；〔W14-2 p9-10〕更干净的是只重写要限流方法的装饰器，〔W14-2 p11〕即 04 讲过的包装器，也是 W04-6 反对在 `Send` 里直接加限流的理由：侵入式修改让每增加一个服务商都要再改一遍。〔W04-6 p9-12〕

客户端侧的容错同理：failover 把请求轮流发给多个下游，超时或错误就换下一个。起始下游用原子计数轮询，保证负载大致均匀；判定下游是否健康，用"连续超过 N 个超时就切换"的近似算法，并发下略有误差但不影响效果。〔W04-6 p22-26〕〔W04-6 p28-30〕

## 常见坑

1. **阈值拍脑袋**：没有压测也没有手算，上线后不是限错了就是形同虚设。
2. **熔断恢复一把放开**：冷却结束直接全量放行，下游刚恢复又被打回去，来回抖动。
3. **重试没有退避、没有抖动，或者重试非幂等写**：故障时流量自我放大，写操作重复执行。
4. **限流器自己出错时没想好默认行为**：资料里的做法是保守处理直接报错；〔W14-2 p8〕选放行还是选拒绝要根据下游承受能力决定，并且要有指标，否则出问题时看不到。

## 判断题（先答再看）

1. 令牌桶 `Allow` 里的 `min(b.cap, ...)` 如果去掉，空闲一小时后会发生什么？
2. 一个调用同时有重试和熔断，熔断器应该包在重试的外层还是内层？本地的 `ErrOpen` 会被重试逻辑怎么对待？
3. 集群限流用"单机阈值乘以节点数"，在自动扩缩容时会怎样漂移？改用 Redis 计数又引入了什么新的故障点？
4. 链路上 A 给 B 的超时是 1s，B 已用 100ms 再调 C，C 的 ctx deadline 大约是多少？如果 B 对 C 又包了一个 `WithTimeout(2s)`，会怎样？

## 在项目里

`dnsproxi-go/tcp/server.go` 的 `ServerCfg` 里有两个上限，是这一节"背压"的最小形态：

- `MaxConns`：连接数达到上限时，新连接 `Accept` 之后直接关闭，不进处理队列，防止连接占坑；这是**并发数限制**。
- `MaxInFlight`：每个连接上同时处理的查询数超限就丢帧，连接不断。这是**舱壁加快速失败**，压力决策还给客户端重试。

两者都是上面的计数器思路，没有速率算法。带时间状态的组件在 `dnsproxi-go/guard/`：`TokenBucket`（`Allow(n)` 拒绝式、`WaitN` 欠账式，时钟注入）和三态 `Breaker`。`main.go` 的 `initGuard` 按 `DNS_RATE_LIMIT`、`DNS_BREAKER_THRESHOLD` 建立，默认全关。`handleQuery` 入口令牌不够就直接丢包（UDP 不回包，由客户端重试），转发上游前过熔断门，开放态快速失败，结局用 `MarkSuccess`/`MarkFailure` 回填。与本节示例的差别：它按最近 100 次请求的失败率判定（样本不少于 20），半开态一次只放一个探针，而不是连续失败计数。

## 想深入去哪查

- 原材料：W14-1（故障理论、熔断三态、降级、限流算法清单）、W14-2（gRPC 拦截器接入、压测定阈值、链路超时）、W04-6（装饰器限流、failover、健康判定）、C:dachang/algorithm（令牌桶手写版）、W13-2（`retryPolicy`）。
- 旧章：`notes/p10-grpc.md`（拦截器基础）、`notes/p14-pipelines.md`（并发上限 `SetLimit`）。
- 本节所有代码在 `/tmp` 里的独立模块中用 `go test -race` 跑过；如果要做成练习，可以把 `Counter`、`TokenBucket`、`Breaker` 抄进 `exercises/` 下新建的包。
