---
title: 16 稳定性：限流、熔断、降级、重试与超时
---

# 16 稳定性：限流、熔断、降级、重试与超时


> 详细程度：深入 · 约 65 分钟
> 材料：W14-1、W14-2、W04-6、WX-sms、C:dachang/algorithm（令牌桶）· 旧长版：无整章对应，相关片段在 `notes/p10-grpc.md`（拦截器）、`notes/p14-pipelines.md`（并发上限）
> 前置：04 接口与组合（装饰器、中间件）、07 并发二（context）

## 先看一个压垮服务的现象

一个接口平时 50ms 返回，某天下游数据库抖了一下，响应变成 2s。上游的并发数没变，但每个请求占着连接和 goroutine 的时间翻了 40 倍，连接池瞬间耗尽，上游也跟着挂。一个节点的故障沿着调用链放大成全站雪崩。要拦住它，需要在链路的不同位置放不同的阀门：入口限流、调用下游加超时、下游持续失败就熔断、撑不住时降级返回。〔W14-1 p5〕〔W14-1 p10〕

第一个阀门可以短到什么程度？Go 里限并发只要一个带容量的 channel。下面 10 行是完整的限流器，先跑起来看看它的判定节奏：

```go
package main

import "fmt"

// Wall 计数器限流：进一个请求占一个坑（+1），出一个腾一个坑（-1）。
// Acquire 非阻塞占坑：占到了放行，占不到就是超限。
type Wall chan struct{}

func (w Wall) Acquire() bool {
	select {
	case w <- struct{}{}:
		return true
	default:
		return false
	}
}

// Release 腾出一个坑。谁 Acquire 成功，谁在请求结束时负责调用。
func (w Wall) Release() { <-w }

func main() {
	w := Wall(make(chan struct{}, 2))
	fmt.Println(w.Acquire(), w.Acquire(), w.Acquire())
	w.Release()
	fmt.Println(w.Acquire())
}
```

```text
true true false
true
```

第三个 `false` 就是限流生效的瞬间。容量 2，前两个请求占满了坑，第三个被拒；`Release` 腾出一个坑后，下一个请求又能进来。这一节把这类阀门讲全：限流（限谁、限什么、算法怎么选）、熔断（三态怎么转）、降级（怎么给兜底响应）、超时与重试（预算怎么传递、重试怎么不放大故障），最后落到接入点：中间件、拦截器、装饰器。

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
2. **限什么**：并发数，还是速率。两者不一样：开头那个 `Wall`（计数器）限的是**并发**，请求进来加一、响应出去减一。阈值 100、平均耗时 50ms，实际能放行约 2000 QPS，因为每个请求只占 50ms，一个坑一秒能周转 20 次。〔W14-1 p21〕并发限制还有个"自适应"的好处：下游变慢时，同样的坑位能放行的 QPS 自动下降；速率阈值则完全不看负载，下游已经半死还按固定速率放请求进去。作者认为计数器这种最简单的限流，效果不比后面几种算法差。〔W14-1 p21〕
3. **被限的请求去哪**：直接拒绝、转异步、返回默认值，或者通知客户端换节点重试。〔W14-1 p18〕

阈值从哪来？有压测就找三个点：A 点性能最好、资源利用率高；B 点是将崩的临界，能扛更高并发但响应已变慢；C 点吞吐最高。选哪个看目标，无论选哪点都要留余量。〔W14-2 p14〕没有压测就看线上观测数据，或者手算：一次调用要两次 DB 查询各 10ms，共 20ms，4 核机器上 `1000/20*4 = 200 QPS`，打八折留余量就是 160，这个数字就是你的初始限流阈值。〔W14-2 p15〕

**集群限流**：单机阈值乘以节点数只是总量估计，节点数变化时会漂移；要精确就把计数放进 Redis：`INCR` 加 `EXPIRE` 能做固定窗口，要滑动窗口就得用 Lua 脚本保证多步操作的原子性。〔自〕W04-6 把限流抽成通用 `Limiter` 接口 `Limit(ctx, key) (bool, error)`，后端是 Redis 加 Lua 的滑动窗口，限整个系统不超过 3000/s，多一次网络往返换来全局准确。〔W04-6 p6-7〕

## 二、速率限流：四种算法的真实差别

资料列出的清单是：计数器、固定窗口、滑动窗口、令牌桶、漏桶，结论是"效果差不多，随便选"。〔W14-1 p20〕但边界行为差别值得看一眼，因为它决定你的突发流量怎么被放行。

### 固定窗口 vs 滑动窗口：边界突刺

固定窗口把时间切片，每片内计数，槽号变了就清零。问题在边界：上一片末尾和下一片开头各放满，短时间内放过的请求可达阈值的两倍。〔W14-1 p24〕滑动窗口的窗口始终是"当前时刻往前回溯一个窗口大小"，用时间戳队列实现，没有这个问题，代价是要存时间戳。〔W14-1 p23〕下面的实验：阈值 100/秒，第 0.9 秒打 100 个，第 1.1 秒再打 100 个，中间间隔 0.2 秒：

```go
package main

import "fmt"

// FixedWindow 固定窗口：按秒切片，槽号变了就清零。
type FixedWindow struct {
	limit int
	slot  int64
	count int
}

func (w *FixedWindow) Allow(now int64) bool {
	slot := now / 1_000_000_000 // 一秒一个窗口
	if slot != w.slot {
		w.slot, w.count = slot, 0 // 窗口切换，清零重来
	}
	if w.count >= w.limit {
		return false
	}
	w.count++
	return true
}

// SlidingWindow 滑动窗口：窗口是"当前时刻往前回溯 1 秒"，
// 用时间戳队列实现：先丢掉滑出窗口的，再数还剩多少。
type SlidingWindow struct {
	limit int
	ts    []int64
}

func (w *SlidingWindow) Allow(now int64) bool {
	cut := now - 1_000_000_000 // 窗口左边界：1 秒前
	keep := w.ts[:0]           // 复用底层数组，原地压缩
	for _, t := range w.ts {
		if t > cut {
			keep = append(keep, t)
		}
	}
	w.ts = keep
	if len(w.ts) < w.limit {
		w.ts = append(w.ts, now)
		return true
	}
	return false
}

func probe(name string, w interface{ Allow(int64) bool }) {
	granted := 0
	for range 100 {
		if w.Allow(900_000_000) { // 第 0.9 秒：把窗口打满
			granted++
		}
	}
	for range 100 {
		if w.Allow(1_100_000_000) { // 第 1.1 秒：紧挨着再来一批
			granted++
		}
	}
	fmt.Printf("%s granted in 0.2s: %d\n", name, granted)
}

func main() {
	probe("fixed  ", &FixedWindow{limit: 100})
	probe("sliding", &SlidingWindow{limit: 100})
}
```

```text
fixed   granted in 0.2s: 200
sliding granted in 0.2s: 100
```

固定窗口在 0.2 秒里放行了 200 个，等于阈值的两倍，这就是突刺；滑动窗口始终只认"过去 1 秒"，第二个请求撞在同一个窗口里，只能拒绝。注意 `SlidingWindow` 里 `keep := w.ts[:0]` 的写法：在原底层数组上原地压缩，避免每次分配新切片。

### 令牌桶：允许攒出突发

令牌桶定速往桶里放令牌，请求取令牌，取不到就拒绝或等待。桶满后多余令牌丢弃，所以空闲一段时间能攒出一批突发额度。〔W14-1 p25〕下面的实现把时钟作为参数注入，`now` 由调用方传入，测试不需要真的等待：

```go
package main

import (
	"fmt"
	"sync"
)

// TokenBucket 令牌桶（内存版，时钟可注入）。
type TokenBucket struct {
	mu     sync.Mutex
	tokens float64
	cap    float64
	rate   float64
	last   int64
}

// Allow 取一枚令牌。now 传入当前 UnixNano，时钟由调用方注入。
func (b *TokenBucket) Allow(now int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	// 按流逝时间补令牌，封顶在桶容量：桶满后多余的丢弃
	b.tokens = min(b.tokens+float64(now-b.last)/1e9*b.rate, b.cap)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func main() {
	b := &TokenBucket{tokens: 5, cap: 5, rate: 10} // 速率 10/秒，容量 5，初始满桶
	granted := 0
	for range 10 {
		if b.Allow(0) {
			granted++ // t=0 的突发：只放行桶里攒下的 5 枚
		}
	}
	fmt.Println("burst at t=0:", granted)
	granted = 0
	for range 10 {
		if b.Allow(300_000_000) { // 300ms 后又来 10 个
			granted++
		}
	}
	fmt.Println("after 300ms:", granted)
}
```

```text
burst at t=0: 5
after 300ms: 3
```

一开始连续 10 个请求只放过 5 个（桶里攒的），300ms 补 3 枚所以又放 3 个。补令牌那行里的 `min(..., b.cap)` 是封顶：没有它，空闲一小时会攒出 3600 枚，突发额度完全失控。资料里的手写版把令牌数做成可以为负、先补后扣，思路相同，但**不支持并发**，生产用 `x/time/rate`。〔C:dachang/algorithm/rate_limit.go:6-46〕

**漏桶：把输出削成匀速**。漏桶的本质是**整形**：请求排队，以恒定间隔流出，不允许积压突发。实现不需要真的开队列：`next` 记录下一个出水时刻，每个请求的等待时间是 `next - now`，放行后 `next` 加一个间隔，等待超过 `maxWait` 就拒绝。间隔 100ms、最长等 250ms、请求每 30ms 来一个时，等待依次是 0、70、140、210ms，第 5 个直接被拒：队伍越排越长，超上限就快速失败。

**怎么选**：令牌桶允许积压（攒令牌），能扛突发；漏桶没有积压，放出去的速率绝对匀速；资料的结论是大多数时候无脑选令牌桶。〔W14-1 p27〕需要保护下游、要求匀速调用第三方接口时才选漏桶。

## 三、熔断：三态状态机

熔断解决的是**级联故障**：下游崩了，调用它的服务线程和连接被占满，也跟着崩，一路传上去。要点是检测、处理、恢复三件事。〔W14-1 p12-13〕三个状态：闭合（全部放行）、开放（全部拒绝）、半开放（放一部分探针，看是否恢复），半开放本质上是恢复过程中的状态。〔W14-1 p13〕

`Allow` 里冷却期满时从开放态进入半开态，并限制同时在飞的探针数不超过 `probes`。`Done(err)` 负责记录结果：闭合态下连续失败达阈值就跳闸；半开态下探针全部成功回到闭合，任何一次失败立刻重新跳闸。参数 `maxFails=3`、冷却 5 秒、探针 2 个：

```go
package main

import (
	"fmt"
	"sync"
	"time"
)

type state int

const (
	stateClosed state = iota
	stateOpen
	stateHalfOpen
)

var stateName = [...]string{"closed", "open", "half-open"}

type Breaker struct {
	mu       sync.Mutex
	st       state
	maxFails int // 闭合态：连续失败多少次跳闸
	fails    int
	coolOff  time.Duration // 开放态的冷却时间
	openedAt time.Time
	probes   int // 半开态允许的在飞探针数
	inFlight int
	probeOK  int
}

// Allow 决定放不放行。now 由调用方注入，测试不用 Sleep。
func (b *Breaker) Allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.st == stateOpen && now.Sub(b.openedAt) >= b.coolOff {
		b.st = stateHalfOpen // 冷却期满，试探性恢复
		b.probeOK, b.inFlight = 0, 0
	}
	switch b.st {
	case stateOpen:
		return false
	case stateHalfOpen:
		if b.inFlight >= b.probes {
			return false
		}
		b.inFlight++
	}
	return true
}

// Done 回填一次调用的结局，err 为 nil 表示成功。
func (b *Breaker) Done(now time.Time, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.st {
	case stateClosed:
		if err == nil {
			b.fails = 0
			return
		}
		if b.fails++; b.fails >= b.maxFails {
			b.st = stateOpen // 跳闸
			b.openedAt = now
		}
	case stateHalfOpen:
		b.inFlight--
		if err == nil {
			if b.probeOK++; b.probeOK >= b.probes {
				b.st = stateClosed // 探针全部成功，恢复
				b.fails = 0
			}
		} else {
			b.st = stateOpen // 任何一次失败，立刻回到开放态
			b.openedAt = now
		}
	}
}

func main() {
	base := time.Now()
	b := &Breaker{maxFails: 3, coolOff: 5 * time.Second, probes: 2}
	row := func(label string, allow bool) {
		fmt.Printf("%-21s allow=%-5v state=%s\n", label, allow, stateName[b.st])
	}
	boom := fmt.Errorf("boom")
	t6, t11 := base.Add(6*time.Second), base.Add(11*time.Second)

	// 连续失败 3 次，跳闸。
	for i := 1; i <= 3; i++ {
		b.Done(base, boom)
		label := fmt.Sprintf("fail %d", i)
		if i == 3 {
			label = "fail 3 -> open"
		}
		row(label, b.Allow(base))
	}
	// 冷却期内一律拒绝；冷却期满，进半开态放探针。
	row("during cooldown", b.Allow(base.Add(2*time.Second)))
	row("after cooldown p1", b.Allow(t6))
	b.Done(t6, nil)
	row("probe 2 -> closed", b.Allow(t6))
	b.Done(t6, nil)
	// 探针失败，立刻回到开放态。
	for range 3 {
		b.Done(base, boom)
	}
	row("fail 3 -> open", b.Allow(base))
	row("after cooldown p1", b.Allow(t11))
	b.Done(t11, fmt.Errorf("probe fail"))
	row("probe fails -> reopen", b.Allow(t11))
}
```

```text
fail 1                allow=true  state=closed
fail 2                allow=true  state=closed
fail 3 -> open        allow=false state=open
during cooldown       allow=false state=open
after cooldown p1     allow=true  state=half-open
probe 2 -> closed     allow=true  state=half-open
fail 3 -> open        allow=false state=open
after cooldown p1     allow=true  state=half-open
probe fails -> reopen allow=false state=open
```

读输出的顺序是先看 `Done` 再看 `Allow`：第 3 行 `fail 3 -> open` 时 `allow=false`，因为 `Done` 里第三次失败已经把状态翻成 open，随后的 `Allow` 自然拒绝；`probe 2 -> closed` 那行 state 还显示 half-open，因为闭合的翻转发生在第二个探针的 `Done` 里，之后的 `Allow` 已是正常放行。

- **恢复不能一把放开**。固定时间等待后直接全量放行会抖动，通用做法是试探：间隔放一个请求，正常就加大流量，失败就退回，直到 100%。〔W14-1 p8-9〕上面的示例为了短小做了简化：探针全部成功就直接闭合，没有逐步放量这一步；生产实现要补上。
- **判据**：这里用的是连续失败次数。更常见的是错误率、响应时间增长，静态阈值靠压测和经验，动态阈值靠硬件和服务指标。〔W14-1 p6〕资料也坦白：很复杂的动态判定在实践里很少用，更多是宣讲和面试时用。〔W14-1 p29〕
- **熔断器返回的错误也要被调用方当成失败**：接入 aegis 时，`Allow` 返回错误也要 `MarkFailed`，否则统计失真。〔W14-2 p12〕也不要让本地的 `ErrOpen` 被上层的重试逻辑当成普通失败再重试，否则等于对熔断器发起风暴。

## 四、降级：尽力给响应

限流、熔断、降级的"处理"本质上是同一件事：执行一段特殊代码，边界并不分明；熔断和限流直接返回错误，降级则是**尽力给响应**，另外还可以同步转异步，或让客户端换节点，做法是预先配好默认响应，或者只走资源少、计算快的"快路径"。〔W14-1 p7〕〔W14-1 p18〕〔W14-1 p14〕

最典型的是缓存降级：正常时先查 Redis，没有再查 DB 并回写；降级时只查 Redis，查不到直接返回错误或默认值。好处有三个：应用负载骤降，并发上限由 Redis 决定，数据库被保住（少数几个 DB 查询就会严重拖累并发）。〔W14-1 p15〕降级难以通用，因为"降不降、怎么降、降到哪"都和业务强相关。资料的做法是：由限流器或熔断器判定，打一个标记，业务代码据此走特殊逻辑。〔W14-2 p13〕Go 里这个标记最自然的载体是 context：

```go
package main

import (
	"context"
	"fmt"
)

type ctxKey struct{}

// WithDegraded 把"已判定降级"的标记放进 ctx；Degraded 读标记。
func WithDegraded(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKey{}, true)
}

func Degraded(ctx context.Context) bool {
	v, _ := ctx.Value(ctxKey{}).(bool)
	return v
}

// load 模拟"先缓存后数据库"：降级时只允许走缓存。
func load(ctx context.Context, cache map[int]string, db func(int) string, id int) string {
	if v, ok := cache[id]; ok {
		return "hit:" + v
	}
	if Degraded(ctx) {
		return "degraded: cache miss for " + fmt.Sprint(id)
	}
	v := db(id) // 慢路径：查库并回写缓存
	cache[id] = v
	return "from-db:" + v
}

func main() {
	cache := map[int]string{}
	db := func(id int) string { return fmt.Sprint(id * 10) }
	fmt.Println(load(context.Background(), cache, db, 2))               // 正常：走慢路径
	fmt.Println(load(WithDegraded(context.Background()), cache, db, 3)) // 降级：只走缓存
	fmt.Println(load(WithDegraded(context.Background()), cache, db, 2)) // 降级但缓存命中
}
```

```text
from-db:20
degraded: cache miss for 3
hit:20
```

第一行正常路径查库回写；第二行降级上下文里缓存未命中，直接返回兜底；第三行虽然也降级，但缓存命中所以照常返回。跨服务的降级是同一个思路放大：资源不足时停增删改保查询，更高级的是集群层停边缘业务保核心、读写分组停写保读。〔W14-1 p16〕

## 五、超时：deadline 随 context 传递

单次调用超时用 `context.WithTimeout`。链路超时的关键是：整条链路共用一个总预算，下游拿到的是**剩余时间**。〔W14-2 p19〕Go 里不需要手动算，context 的 deadline 取父子中较小的，想给下游"放宽"是不可能的：

```go
package main

import (
	"context"
	"fmt"
	"time"
)

func main() {
	// callC 模拟下游 C 收到请求时看到的 deadline。
	callC := func(ctx context.Context, label string) {
		d, _ := ctx.Deadline()
		fmt.Println(label, "remaining:", time.Until(d).Round(10*time.Millisecond))
	}

	// A 给 B 的预算是 1 秒。
	ctxB, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	time.Sleep(100 * time.Millisecond) // B 自己用掉 100ms

	// B 调 C：直接把 ctx 传下去，C 拿到的就是剩余时间。
	callC(ctxB, "pass ctx down:")

	// 再包一层 WithTimeout(2s)，想放宽也没用。
	ctxC, cancel2 := context.WithTimeout(ctxB, 2*time.Second)
	defer cancel2()
	callC(ctxC, "wrap with 2s:")
}
```

```text
pass ctx down: remaining: 900ms
wrap with 2s: remaining: 900ms
```

两行都是 900ms：B 用掉 100ms 后，C 拿到的就是剩余的 900ms；再包一层 2 秒的 `WithTimeout` 也没用，deadline 取的是较小的那个。gRPC 会把 deadline 随请求带到服务端，服务端 ctx 里已经是剩余时间，零配置。〔W14-2 p20〕超时值怎么定：理论上由用户体验决定，实操上看接口 999 线或手算；首页应该在 100ms 内，1 秒是用户明显感到卡顿的关键节点。〔W14-2 p21〕

## 六、重试：退避加抖动，并且要幂等

重试看似简单，三件事做错一件都会放大故障。先看骨架，退避间隔指数增长、封顶，抖动系数可注入：

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Retry 带退避与抖动的重试。sleep 与 jitter 可注入，测试不用真睡；
// ctx 类错误（超时、取消）不重试，直接返回。
func Retry(ctx context.Context, attempts int, base, maxDelay time.Duration,
	jitter func() float64, sleep func(context.Context, time.Duration) error,
	fn func(context.Context) error) error {
	var err error
	for i := 0; i < attempts; i++ {
		if err = fn(ctx); err == nil {
			return nil
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return err // 用户的预算用完了，重试只会更糟
		}
		if i < attempts-1 {
			d := min(maxDelay, base<<i) // 指数退避，封顶
			if serr := sleep(ctx, time.Duration(float64(d)*jitter())); serr != nil {
				return serr
			}
		}
	}
	return err
}

func main() {
	var delays []time.Duration
	attempts := 0
	err := Retry(context.Background(), 5, 100*time.Millisecond, 300*time.Millisecond,
		func() float64 { return 1 }, // 抖动固定为 1，好观察退避序列
		func(_ context.Context, d time.Duration) error {
			delays = append(delays, d)
			return nil
		},
		func(context.Context) error {
			attempts++
			return fmt.Errorf("fail %d", attempts)
		})
	fmt.Println("tries:", attempts, "delays:", delays, "err:", err)
}
```

```text
tries: 5 delays: [100ms 200ms 300ms 300ms] err: fail 5
```

指数增长到上限 300ms 封顶。生产里 `jitter` 取 0 到 1 的随机数，让一批同时失败的客户端不会同时重试。三个必备件：**退避**，立刻重试只会让已经过载的下游更过载；**抖动**，没有随机性，所有客户端会在同一时刻再次撞上去；**幂等前提**，只有幂等的调用才能无脑重试，幂等接口应尽量返回上一次的结果：支付预下单超时重试，如果第一次已经创建，第二次会得到"重复"错误但仍能拿到二维码地址。〔W15-1 p30〕

还有两条边界。用户体验相关的错误（`DeadlineExceeded`、`Canceled`）不要重试，直接返回，上面的 `Retry` 已经带了这道闸；重试会放大流量：每一层各自重试，最坏的放大倍数是各层相乘（每层最多 3 次尝试、三层串联，最坏 27 倍）。〔自〕它叠在微服务本来就有的流量放大上：一个请求会扇出多个下游调用，请求量翻倍时整个系统的负载不止翻倍。〔W14-1 p10〕这是雪崩的来源。不重试用户体验类错误的做法见课程的 failover 实现。〔W04-6 p25-26〕gRPC 内置的 `retryPolicy` 可以在 service config 里配置最大尝试次数、退避和可重试状态码，字段都是手写 JSON，要仔细。〔W13-2 p29-30〕

### 一个把本章凑齐的业务：短信服务平台

短信服务是稳定性三件套（重试、降级、failover）的集大成业务案例。架构两头：对内收业务方的请求（同步调用或直接丢 MQ 异步），对外连第三方服务商（腾讯、阿里）直发。〔WX-sms p1〕它的第一设计决策是**入库先行**：请求先落库再尝试发送，失败交给定时扫描未成功记录的补偿任务补发；反过来直接调第三方，失败即丢，崩溃后无从追讨。代价是可能重复发送加部分非实时，这两点都能接受，丢短信不能。〔WX-sms p2-3〕

重复请求靠**去重加幂等回放**：请求带 `biz_type+biz_id`，短信服务建唯一索引；重复请求来了不报错，查同一个键之前是否发送成功，成功就返回成功，失败就返回失败。注意去重键必须用业务语义而不是手机号：同一个人两条不同业务的短信都要发。〔WX-sms p3〕

限流在这里分两层，别混：**平台自保护**的限流和降级（高负载时同步转异步，给业务方"已受理"响应加查询或订阅通道，而不是直接报错把压力变事故）保护平台自己的入口；**客户端限流**保护出口的第三方服务商，防触发对方的限流。〔WX-sms p4〕服务商容错三档递进：轮询 failover（超时或异常就切下一个，无状态）、健康判定 failover（连续不健康就临时移出，带记忆）、试探法恢复（间隔试探，成功恢复、失败延长不健康时间，避免永久拉黑）；只轮询会把流量反复打到坏节点上。〔WX-sms p4〕

最值得记住的是**回查机制**，它把本章和 13 节的事务消息连起来：业务方先发一条"准备短信"（不真发），本地事务成功后再确认发送；失败则删除；超时无提示就回查业务方（HTTP 回调或丢 MQ 让业务方自查后回执）。这就是事务消息的"半消息加回查"在业务侧的翻版。它实现复杂且要业务方配合改造，面试提一句即可，别主动展开。〔WX-sms p4-5〕

## 七、接入点：中间件、拦截器、装饰器


同一个判定逻辑，换一层皮就能接入不同协议。gRPC 服务端拦截器是一个函数套一层函数：先调 `Limiter.Allow()`，不过门就短路返回，否则调 handler。真实的 gRPC 拦截器通过 `grpc.ChainUnaryInterceptor` 链式注册，返回 `status.Error(codes.ResourceExhausted, "限流")` 让客户端拿到明确的限流错误码。〔W14-2 p6〕〔W14-2 p8〕限流粒度分三层：应用级（key 固定，整个服务一起限）、服务级（按 `info.FullMethod` 前缀）、业务级（在拦截器里对 req 做类型断言取 key，所有请求都要付断言成本）。〔W14-2 p9-10〕更干净的是只重写要限流方法的装饰器，〔W14-2 p11〕即 04 讲过的包装器（那章有完整可运行版本），也是 W04-6 反对在 `Send` 里直接加限流的理由：侵入式修改让每增加一个服务商都要再改一遍。〔W04-6 p9-12〕

客户端侧的容错同理。failover 把请求发给一个起始下游，连续超时就换下一个；起始位置用原子计数轮询保证负载大致均匀；判定下游是否健康，用"连续超过 N 个超时就切换"的近似算法，并发下略有误差但不影响效果。〔W04-6 p25-26〕健康判据除了连续超时，还有错误率（连续 N 个超时、错误率超 10%）和响应时间增长率（100ms 突然变成 1s），按服务商特点选。〔W04-6 p28-30〕雪崩的另一个对策是**舱壁**：每个下游一个独立的并发计数器（上面的 `Wall` 就行），满了快速失败，不影响别的下游。〔自〕课程的面试整套方案里提到，用了分组和隔离措施会更好。〔W14-2 p30〕

## 小结

1. 限流先答三问：限谁、限什么、被限的去哪。计数器限的是并发不是速率，阈值定多少要看压测或手算，别拍脑袋。〔W14-1 p19〕〔W14-1 p21〕〔W14-2 p14〕
2. 速率算法按突发需求选：固定窗口有边界突刺，滑动窗口均匀但存时间戳，令牌桶攒突发，漏桶绝对匀速；大多数场景无脑令牌桶。〔W14-1 p24〕〔W14-1 p27〕
3. 熔断是三态机：闭合全放、开放全拒、半开放探针。恢复必须试探加逐步放量，一把放开会抖动。〔W14-1 p13〕〔W14-1 p8-9〕
4. 降级是尽力给响应：预配默认值或只走快路径，判定结果用 context 标记传给业务代码，跨服务降级就是停边缘保核心。〔W14-1 p14〕〔W14-1 p16〕
5. 超时是链路预算：deadline 随 context 走，下游只拿剩余时间，包再多层也放宽不了。〔W14-2 p19〕重试三件套：退避、抖动、幂等；用户体验类错误（`DeadlineExceeded`、`Canceled`）不重试，否则流量自我放大。〔W04-6 p25-26〕
6. 接入点统一为函数包装：HTTP 中间件、gRPC 拦截器、装饰器是同一种组合。服务端放限流熔断保护自己，客户端放超时重试 failover 保护自己不被下游拖死。〔W04-6 p9-12〕

## 常见坑

1. **阈值拍脑袋**：没有压测也没有手算，上线后不是限错了就是形同虚设。
2. **熔断恢复一把放开**：冷却结束直接全量放行，下游刚恢复又被打回去，来回抖动。
3. **重试没有退避、没有抖动，或者重试非幂等写**：故障时流量自我放大，写操作重复执行。
4. **限流器自己出错时没想好默认行为**：课程的 gRPC 限流拦截器在 `Limit` 自己返回错误时选保守处理，直接把错误返回；〔W14-2 p8〕选放行还是选拒绝要根据下游承受能力决定，并且要有指标，否则出问题时看不到。

## 判断题与答案

1. 令牌桶 `Allow` 里的封顶（`min(b.cap, ...)`）如果去掉，空闲一小时后会发生什么？
2. 一个调用同时有重试和熔断，熔断器包在重试的外层还是内层各有什么取舍？本地的 `ErrOpen` 该被重试逻辑怎么对待？
3. 集群限流用"单机阈值乘以节点数"，在自动扩缩容时会怎样漂移？改用 Redis 计数又引入了什么新的故障点？
4. 链路上 A 给 B 的超时是 1s，B 已用 100ms 再调 C，C 的 ctx deadline 大约是多少？如果 B 对 C 又包了一个 `WithTimeout(2s)`，会怎样？

**答案**

1. 令牌数涨到 3600，一次突发全部放行，限流形同虚设。封顶那行就是给突发额度设上限。
2. 两种包法都有人用，要说清取舍。熔断器在外层：整段重试只记一次结局，`ErrOpen` 只会出现在重试开始之前，不会进重试循环；代价是熔断器看不到每次尝试的失败。熔断器在内层（重试包着它）：每次尝试都被统计，但重试逻辑必须把 `ErrOpen` 识别为"不要重试"，否则会把它当普通失败反复撞门，等于对熔断器发起风暴。
3. 节点数变化时总阈值跟着漂，扩容瞬间放行量突增；Redis 计数把准确换来了新的单点：Redis 挂了限流器不可用，得想好这时是放行还是拒绝。
4. 约 900ms，deadline 取父子较小的那个。再包 `WithTimeout(2s)` 也没用，不会比父级的 900ms 更晚。

## 在项目里

`dnsproxi-go/tcp/server.go` 的 `ServerCfg` 里有两个上限，是这一节"背压"的最小形态：`MaxConns` 连接数达上限时，新连接 `Accept` 之后直接关闭，不进处理队列，这是**并发数限制**；`MaxInFlight` 每个连接上同时处理的查询数超限就丢帧，连接不断，这是**舱壁加快速失败**，压力决策还给客户端重试。两者都是开头 `Wall` 的思路，没有速率算法。带时间状态的组件在 `dnsproxi-go/guard/`：`TokenBucket`（`Allow(n)` 拒绝式、`WaitN` 欠账式，时钟注入）和三态 `Breaker`。`main.go` 的 `initGuard` 按 `DNS_RATE_LIMIT`、`DNS_BREAKER_THRESHOLD` 建立，默认全关。`handleQuery` 入口令牌不够就直接丢包（UDP 不回包，由客户端重试），转发上游前过熔断门，开放态快速失败，结局用 `MarkSuccess`/`MarkFailure` 回填。与本节示例的差别：它按最近 100 次请求的失败率判定（样本不少于 20），半开态一次只放一个探针，而不是连续失败计数。

## 想深入:正文之外的干货

原材料里还有一批正文没讲透的东西,按单元收在这里,读完可以不用再翻 PDF。

### W14-1 故障理论里正文没展开的部分

- 检测分静态和动态两条路:静态是压测结果加经验定阈值,动态是硬件指标(CPU、内存、IO)加服务指标(响应时间、超时比率);正文只讲了静态的压测半边,动态判定胜在能发现压测覆盖不了的新故障。〔W14-1 p6〕
- 恢复是试探加放量的循环:间隔一段时间试探一个请求,正常就加大流量,失败就减回去或回到默认值,直到 100% 流量正常;放开节奏可以复用限流里"随机数加阈值"的思路,按处理结果实时调阈值。〔W14-1 p8-9〕
- 降级走快慢路径:快路径资源少计算快,慢路径资源多计算慢,降级后只走快路径。〔W14-1 p14〕
- 缓存降级方案(面试高频):正常查 Redis 再查 DB 再回写,降级期只查 Redis,查不到直接给默认值;负载骤降时反而能撑极高并发,因为瓶颈从 DB 换成了 Redis。隐藏代价是恢复时 DB 要承接积压的回写加新查询,必须配合放量。〔W14-1 p15〕
- 限流熔断降级是同一个故障处理模型的三个参数化,触发条件与处理动作不同;选用口径:保可用选降级,要快恢复选熔断,保部分请求正确选限流。使用频率是限流大于降级大于熔断,熔断破坏性强(全拒),需要客户端配合重试或降级才闭环。〔W14-1 p7-8〕〔W14-1 p28〕

### W14-2 gRPC 接入治理的工程决策

- 拦截器是 gRPC 版 middleware:UnaryServerInterceptor 四参数 (ctx, req, info, handler),info 里拿 FullMethod 与服务实现;熔断限流降级多在服务端拦截器,多个用 grpc.ChainUnaryInterceptor 链式注册。〔W14-2 p4-7〕
- 限流分三级:应用级 key 固定(普通和 VIP 一起限)、服务级用 HasPrefix 匹配 FullMethod、业务级类型断言 req 取业务 key;业务级的缺陷是所有请求都进断言但只有部分被限,白费 CPU。通用规则用拦截器断言,单方法专有限流用装饰器组合 Server 只重写一个方法,不污染别的业务。〔W14-2 p8-11〕
- 被限流的响应必须返回 ResourceExhausted 语义码:客户端负载均衡靠它区分"被限流"与"真失败"(降权的依据),统一返回 Internal 会误导容错逻辑。〔W14-2 p8〕〔W14-2 p32〕
- 限流器自己出错时 fail-close 还是 fail-open:下单类保守直接拒,展示类放行;课程选 fail-close。〔W14-2 p8〕
- 熔断不用手搓:go-kratos/aegis 子项目独立可用(限流 BBR 加熔断),封装成拦截器;Allow 返回 error 时也要 MarkFailed。〔W14-2 p12〕
- 降级没法做成通用组件:降不降、怎么降、降到哪都业务强相关;可行方案是限流器或熔断器判定后打标记位,业务看标记走特殊路径(触发限流后缓存未命中直接返回,快速清空在途请求)。〔W14-2 p13〕
- 链路超时是自动的但有坑:gRPC 把 deadline 随 ctx 传播,服务端收到请求时 ctx 里已是剩余时间,ctx.Deadline() 减 time.Now() 即得,零配置;但每层再 WithTimeout 会截断剩余时间,中途放大无效(deadline 单调减)。〔W14-2 p19-20〕
- 单次调用的超时值:理论由产品按用户体验定,实操取 999 线(1 秒是用户明显卡顿的节点)或手动计算;首页要压进 100ms。〔W14-2 p21〕
- 客户端容错升级:负载均衡里处理限流错误(降权)与熔断错误(把节点挪出可用列表,goroutine 定时健康检查,正常后挪回并控制流量),这是熔断反馈到客户端的完整闭环。〔W14-2 p32〕

### W04-6 客户端侧的完整工具箱

- 客户端也要限流:腾讯短信 API 阈值 3000/s,超了直接被拒,与其等服务商返回限流响应,不如本地就限住;Limiter 接口后端是 Redis 加 Lua 的滑动窗口,限整个系统不超过 3000/s,代价是多一次网络往返。〔W04-6 p5-7〕
- 客户端治理的姿势是装饰器:直接改 Send 加限流是反面教材,每个服务商都要改一遍,越改越难看;同接口包一层可层层叠加超时、重试、限流,触发限流返回的 errLimited 保持非导出,导出就成了对外契约。〔W04-6 p9-12〕
- failover V1 是朴素轮询:按顺序把服务商试一遍,全挂才报错(更可能是你自己的网络崩了,这时要打日志);缺点是负载不均,多数请求在 svcs[0] 就成功。〔W04-6 p22〕
- 起始下标用原子计数轮转,避免所有请求都砸在第一个服务商;这不是严格轮询,只有起始点在轮转,严格轮询要控制并发,代码更复杂。失败换下一个,全部失败才报错。〔W04-6 p25〕
- 超时和取消直接返回,不再重试:超时是调用方设的时限到了,取消是调用方主动放弃,这两种错误都和用户体验直接相关,重试只会浪费配额。〔W04-6 p25-26〕
- 健康判定用连续超时计数,切换服务商靠 CAS 实现:CAS 失败说明别人已经切换过,返回值不用检测;成功时重置计数,超时才累加,其他错误不动。判据除了连续超时还有错误率(超 10%)和响应时间突变(100ms 变 1s),按服务商特点选。〔W04-6 p28-30〕
- 怎么发现服务商快挂了:无法准确知道,只能猜,信号是频繁超时、EOF 或 UnexpectedEOF、响应时间变长;关系够铁可以问有没有特定错误码。〔W04-6 p20〕
- tpl 是要治理的资源:审批流程申请、特定组才能用、月 10 万条容量上限控成本;内部调用用静态 token,认 token 不认人;对外或跨部门用 appId/appSecret 换临时 token 控时效。token 替换 tpl 参数把"指定模板"变成"授权发送",模板 ID 服务端从 token 解出,业务方没法越权。〔W04-6 p34-37〕

### W13-2 重试与负载均衡的配合

- 客户端 failover 的两个齿轮缺一不可:只有重试会反复撞死节点,只有 LB 剔除则单次失败就丢请求;最简落地是 round_robin 加 retryPolicy,配 maxAttempts、initialBackoff/maxBackoff/backoffMultiplier(指数退避)与 retryableStatusCodes(必须含 UNAVAILABLE),才会真正换节点重试。〔W13-2 p29-30〕
- retryPolicy 的 name 要去 proto 编译产物里找 service 名;它同样是手写 JSON,字段名错一个就静默失效。〔W13-2 p29-30〕
- 一次调用的最坏时延可以自己算:maxAttempts=4、节点宕机时,是 4 次尝试加指数退避叠加,这就是 maxAttempts 别配太大的原因。〔W13-2 p29-30〕

### C:dachang/algorithm 令牌桶手写版的三个细节

- 手写版令牌桶 tokens 是 float64 且可以为负(欠账):WaitN 先按流逝时间补 token(封顶容量)再扣 n,不足时 Sleep 补足时长;last 字段必须及时更新,否则补 token 的计算全错。〔C:dachang/algorithm/rate_limit.go:6-46〕
- 它非并发安全,生产用 golang.org/x/time/rate(正文提过的生产级实现);手写版的价值是看清"补 token、扣 token、睡到够"这三步骨架,与正文漏桶的"记下次出水时刻"对照着记。〔C:dachang/algorithm/rate_limit.go:6-46〕

以下有意省略:W04-6 的面试项目包装话术(p38-40)、atomic 四件套语义与装饰器单测细节(p13-14、p27,14 节已收);W14-2 的 log/trace/prometheus 拦截器字段级清单(p24-26,15 节正篇已讲)与面试话术(p28-30);W14-1 的动态综合判定(p29,宣讲向)、跨服务降级分层(p16,19 节已收);W13-2 的负载均衡本体(pick_first、WRR、P2C、哈希,由 11 节与 19 节收),这里只收与稳定性相关的重试与熔断联动;C 单元其余条目是缓存、TopK、Trie、DP 等与稳定性无关的算法。

## 想深入去哪查

- 原材料：W14-1（故障理论、熔断三态、降级、限流算法清单）、W14-2（gRPC 拦截器接入、压测定阈值、链路超时）、W04-6（装饰器限流、failover、健康判定）、C:dachang/algorithm（令牌桶手写版）、W13-2（`retryPolicy`）。
- 旧章：`notes/p10-grpc.md`（拦截器基础）、`notes/p14-pipelines.md`（并发上限 `SetLimit`）。
- 生产级实现：`golang.org/x/time/rate` 的令牌桶支持等待与取消；`sony/gobreaker` 与 aegis 的熔断器支持错误率判据与事件回调。
