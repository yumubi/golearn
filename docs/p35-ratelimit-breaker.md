---
title: P35 · 限流、熔断与容错
---

# P35 · 限流、熔断与容错


> 这章学完你能干什么：说清服务治理三件套（限流、熔断、降级）各自解决什么问题、阈值从哪来，并用 dnsproxi-go/guard/ 的真实代码走完一条"理论 → 教学实现 → 生产替换"的链路：Clock 接口注入时间、令牌桶的浮点补 token 与欠账语义、三态熔断的窗口统计与半开试探、两道闸在 main.go 的默认关闭接入。前置：P13（RED 指标）、P17（dnsproxi 观测）、P30（方法值）。

## 1. 为什么治理：故障四阶段与雪崩

故障处理的主线是四阶段：预防（限流不管有没有故障，到阈值就限）→ 检测（静态=压测+经验设阈值；动态=硬件+服务指标）→ 处理（同步转异步 / 执行特殊代码 / 请求转发）→ 恢复（试探+逐步放量，防抖动）。〔W14-1 p5〕〔W14-1 p6〕〔W14-1 p7〕

为什么需要专门一章讲"拒绝请求"？因为流量会放大：单请求在微服务里产生多次下游调用，上游一抖下游全抖，流量翻倍负载不止翻倍，这就是雪崩。〔W14-1 p10〕熔断专防级联：A 崩了尽快不再打它，B/C/D 才能活。〔W14-1 p12-13〕

三个名词的分工，教材 D1 的总结最清楚：熔断、限流、降级是同一个故障处理模型的三个参数化，处理动作（拒绝/默认值/转发）与触发条件不同。〔W14-1 p7-8〕选用口诀：保可用=降级，快恢复=熔断，保部分请求正确=限流；使用频率限流 > 降级 > 熔断（熔断破坏性强，全拒）。〔W14-1 p28〕

W03-1 把这个落到真实业务：注册/登录是天然漏洞，任何人可刷、都查库，限流是最常见的保护手段。〔W03-1 p35-36〕〔W03-1 p38〕

在 dnsproxi 里看故障路径：上游黑洞时每个查询都等 2s deadline 超时（main.go 的 SetDeadline），goroutine 全堵在读上。没有 guard 的版本只会上游越来越忙、本进程 goroutine 越积越多。限流的判定时机：系统无法处理更多请求时执行，多为静态阈值，超阈值拒绝。〔W14-1 p17〕

## 2. 限流：三种算法对比与 guard/limiter.go 的令牌桶

### 2.1 算法对比

限流对象先分清维度：单机 vs 集群；整个应用 vs 某接口；业务对象（用户、IP）。〔W14-1 p19〕W04-6 把治理动机放在客户端：尽量不搞崩第三方，第三方崩了你还得稳定运行；腾讯短信 API 阈值 3000/s，超了被拒，与其等服务商拒绝不如本地先限。〔W04-6 p3-4〕〔W04-6 p5〕算法清单：计数器、固定窗口、滑动窗口、令牌桶、漏桶。〔W14-1 p20〕

并发计数器限制在途请求数（进入 +1、退出 -1），时间窗口计数器限制一段时间内的请求次数；“计数器”本身并不只限并发。〔W14-1 p21〕〔W14-1 p24〕固定窗口的坑是边界突刺：窗口 1 末与窗口 2 初各用满额度，短时间内可放过接近两个窗口的额度。Redis 的 INCR+EXPIRE 可用于固定窗口，但初始化计数和设置过期要考虑原子性。〔W14-1 p24〕滑动窗口能缓解窗口边界突刺，具体精度取决于实现。

令牌桶：定速发令牌入桶，请求拿令牌，拿到处理，没拿到拒绝或等。允许积压是它胜出的原因：桶里攒的令牌扛突发流量；漏桶只在对下游有严格匀速要求时必要。〔W14-1 p25〕〔W14-1 p27〕

### 2.2 教学实现：tokenbucket.go

参考 dachang 课程的令牌桶教学版，guard/ 写了并发安全 + 时钟注入的版本。教学版的形状：tokens float64 可为负；按流逝时间补 token；非并发安全。〔C:dachang/algorithm/rate_limit.go:6-46〕guard 的 Allow/WaitN 两出口就是这版思路的并发安全化（WaitN 的欠账语义直接继承）。

第一处设计：Clock 接口。

```go
type Clock interface { Now() time.Time }
```

（tokenbucket.go L13）单方法接口，生产 realClock，测试 fakeClock 手动推进。为什么值得一个接口：限流和熔断的逻辑全挂在"现在几点"上，测试要验证"0.2s 补 2 枚""冷却期过了没有"，如果 Now 写死 time.Now()，测试只能真等。注入后测试零等待。


第二处：补 token 公式。advance 把 last 推到 now，tokens += elapsed*rate，封顶 cap（L57-64）。不 Sleep 阻塞式补 token，而是"下次来取时按流逝时间一次算清"。无 tick 限流器的标准写法：没有后台 goroutine，没有 timer 泄漏，空闲时零开销。

〔实测〕基准：`go test -bench . -benchmem -run xxx ./guard/`（M4，bench_test.go）：

```
BenchmarkTokenBucketAllow-10    8118202   150.1 ns/op   0 B/op   0 allocs/op
BenchmarkBreakerAllowClosed-10 16972405    76.5 ns/op   0 B/op   0 allocs/op
BenchmarkTokenBucketWaitN-10    1000000   9000 ns/op   219 B/op   2 allocs/op
```

Allow 一次一把锁 + 浮点乘加，150ns；WaitN 慢是因为 timer 分配（2 allocs），阻塞路径不在乎。

### 2.3 接入：入口门

main.go 的 initGuard 解析 DNS_RATE_LIMIT（"rate"或"rate,burst"，空串默认关，L144-153），handleQuery 入口第一个判定（L350-354）：

```go
if p.limiter != nil && !p.limiter.Allow(1) {
    QueriesTotal.WithLabelValues("OTHER", "rate_limited").Inc()
    return
}
```

label 只有 outcome，不记客户端 IP：IP 高基数，会撑爆 Prometheus（P13 的纪律）。限流对象用 IP 是 Web 端最好选择，但那是判定维度，不是 label 维度，两者不矛盾，章内明确。〔W03-1 p38〕
〔实测〕黑洞上游 + `DNS_RATE_LIMIT="5,5"`，40 连 `dig -p 5398 @127.0.0.1 example.com A +time=1 +tries=1`。计数：13 个 rate_limited，2 个 forwarded，其余因上游错误计 upstream_error。限流阈值 5/s 与拒绝计数对得上（40 查询 8s 窗口，配额 40+突发 5，上游 2s deadline 吞吐瓶颈下实际过门 29）。

生产替换：x/time/rate 是标准答案（带 reserve 记账、内存 limit、等待节流）。教学版价值在把"补 token、欠账、封顶"三件事写到看得见。

## 3. 熔断：三态机与 guard/breaker.go

### 3.1 三态语义

闭合（closed）=全放行；开放（open）=全拒绝；半开（half-open）=恢复中，放一小部分试探。〔W14-1 p13〕Java Resilience4j 的 CircuitBreaker 三态同构（CLOSED/OPEN/HALF_OPEN），状态机不是 Go 特有发明。

与限流的分工：限流防"请求太多"，熔断防"上游病了还打"。W04-6 的间接信号清单：频繁超时、EOF、响应时间变长；无法准确知道上游状态，只能从错误猜。〔W04-6 p20〕

### 3.2 breaker.go 逐段

状态与转移规则在文件头注释里写死（L19-35）：closed → open 要求窗口内请求 ≥MinSamples 且失败率 ≥FailThreshold；open → halfOpen 要求冷却期 CoolOff 过后；halfOpen 试探成功回 closed、失败回 open 重新冷却。

窗口统计用失败位环形缓冲（ring，L64-92）：固定 bool 数组 + next 指针，add 覆盖最老样本，counts 扫一遍数失败。为什么不用 channel 或时间分桶：滑动窗口只需"最近 N 次里失败多少"，环形缓冲 O(1) 写 O(N) 读（N=100 扫描是 100 个 bool，够快），且 reset（半开成功后清窗）就是把指针和计数归零。

Allow() 是唯一放行口（L131-153）：

```go
case stateOpen:
    if b.clock.Now().Sub(b.openedAt) >= b.cfg.CoolOff {
        b.st = stateHalfOpen
        b.probing = false
    } else {
        return ErrOpen
    }
    fallthrough
case stateHalfOpen:
    if b.probing { return ErrOpen }
    b.probing = true
    return nil
```

open 态每请求都要进来判断"冷却期过了没"，所以 open 不是短路返回，而是先检查再拒绝。fallthrough 进半开：第一个到达的请求成为试探（probing=true 独占），其余全拒。Go 的 fallthrough 在这里刚好表达"开态结束时顺势做半开判定"。

MarkSuccess/MarkFailure 是调用方必须回填的账（L156-189）：半开成功 → closed + 清窗；半开失败 → 重开；closed 下失败进窗口，够样本且失败率超阈值即 trip。漏回填一个，窗口就失真，所以 main.go 用 sent 标志保证"门过了才有账"。

〔实测〕单测覆盖五个转移：`go test -race -count=3 ./guard/`（guard_test.go）：2 成功 2 失败 trip；MinSamples 不足不 trip；冷却后放试探回 closed；试探失败重开且重新计时；8 goroutine 并发 Allow+Mark 的 -race 干净。冒烟：黑洞上游 30 连超时（MinSamples=20、阈值 50%）→ open，之后 5 个查询全部 breaker_open 计数，上游一个包都没收到。

### 3.3 接入：转发门

main.go 转发段的门（L412-424）：breaker.Allow() 非 nil 直接 breaker_open 计数返回，不碰上游。之后每个结局回填：deadline 设不进去、write 错、read 错都 markBreaker(false)，读成功 markBreaker(true)（L437-460）。sent 标志在 DialUDP 错误检查后置位（L444）：门没过不计样本，门过了漏回填才是 bug。

例如阈值 30（`DNS_BREAKER_THRESHOLD=30`）按百分数除以 100 后是 0.3；环境变量中不需要写小数点。

## 4. 接入形态：装饰器、interceptor、方法值直连与 failover

### 4.1 三种包装形态

W04-6 的教训先看反面：把限流直接改在 Send 方法里，每个新功能都改业务方法，越改越屎山。〔W04-6 p9〕装饰器的定义：不改变原有实现而增加新特性，可层层叠加；保持面向接口+依赖注入，业务逻辑最终转交被装饰者。〔W04-6 p10-12〕RateLimitSMSService{svc Service, limiter} 的 Send 先 Limit 再转发。熔断的库形态 aegis 的契约：Allow 返回 error 也要 MarkFailed。〔W14-2 p12〕

W14-2 的 gRPC 形态：Interceptor 即 middleware（gRPC 自带的一层包装，服务端收到请求先过链），ChainUnaryInterceptor 把多个串成链。〔W14-2 p4-5〕〔W14-2 p6〕

dnsproxi 用的第三种：方法值直连。tcp.Serve 的 Handler 签名刻意与 handleQuery 一致（P34 已写），guard 不包 Handler，而是在 handleQuery 体内放两道闸。三形态对比：装饰器最通用（换实现零改动），interceptor 是框架给的等价物，方法值直连最少抽象（没有接口层级，代价是 guard 和业务耦合在一个函数里）。dnsproxi 选直连的理由：只有一个业务入口，装饰器层的灵活性没有买主。方法值本身在 p30 §receiver 是第一个参数 讲过：T.Get 把方法当函数用。

### 4.2 failover：熔断的兄弟

W04-6 的 SMS 场景有多个服务商，挂了换下一个：failover V1 顺序试，V2 用 atomic.AddUint64 轮询起始服务商，健康判定"连续 N 个超时就切换"，切换用 CAS（失败说明别人切了，无需检测返回值）。〔W04-6 p22〕〔W04-6 p25-26〕〔W04-6 p28-30〕

guard 的 breaker 和 failover 的 CAS 切换是同一个决策点：原子地在"还信它"和"不信它"之间翻转。差别在恢复：failover 靠下一轮健康判定切回来，breaker 靠半开试探。dnsproxi 单上游，failover 无处安放，breaker 就是全部。

## 5. 阈值从哪来：压测、观察与集群化

静态阈值的来源是压测：找到系统能扛的 QPS 上限，配上安全系数。〔W14-2 p14-15〕W14-2 的无压测三招：按上游配额倒推、按容量模型估算、抄同行的公开数字。dnsproxi 的 rate 面板与压测方法在 p17 §压一轮，本章不重复。

W03-1 的多实例问题：限流计数在单进程内存里，如果每个副本都配置同样额度，总允许量可能随副本数增加。要严格执行全局配额，可用 Redis 等共享存储集中计数；也可按副本分配额度，但要处理扩缩容和流量不均。〔W03-1 p42〕guard 是进程内的，作用域单机；集群版是练习方向（X1：INCR+EXPIRE 固定窗口 + 两窗拼接模拟滑动）。

链路超时与熔断的配合：W14-2 讲 gRPC deadline 沿链路传递，服务端收到请求时 ctx 内已是剩余预算，上游超时约束下游；整条链路（A→B→C）同一超时控制，B 的超时不能大于 A 给它的余量。〔W14-2 p19〕〔W14-2 p20〕dnsproxi 的形态更简单：每查询 2s deadline 写死在 main.go，读超时即失败样本进窗口。超时预算不传递时，熔断窗口会替你发现"下游全在超时"。

## 6. 小结与边界

| 机制 | 触发 | 动作 | guard 对应 |
|---|---|---|---|
| 限流 | 速率/并发超阈值 | 拒绝（或排队） | TokenBucket.Allow/WaitN |
| 熔断 | 失败率超阈值 | 全拒+试探恢复 | Breaker.Allow/Mark |
| 降级 | 资源不足或触发降级开关 | 默认值/快路径 | 本章未做（降级=尽力给响应，预配默认响应或快路径，与熔断的全拒相对）〔W14-1 p14〕；P37 缓存降级呼应 |

在 dnsproxi 里看这一章：guard/ 两文件共 300 行，main.go 接入 40 行。env 两个开关：DNS_RATE_LIMIT 与 DNS_BREAKER_THRESHOLD。默认零值全关，不配行为逐位不变。指标 outcome 新增两个枚举，label 无 IP。行数明细：tokenbucket.go 111 行、breaker.go 189 行。

教学与生产的边界，逐条列清：限流换 x/time/rate（reserve 记账/内存 limit/等待节流）；熔断换 aegis 或 sony/gobreaker（半开放量策略更细，1%→10%→100% 灰度）；集群化换 Redis INCR（W03-1#K21）；降级需要业务语义（默认 DNS 应答没有意义），dnsproxi 不做。W14-1 的动态判定综合方案（硬件+服务指标实时调阈值）实践中很少用，宣讲才吹。〔W14-1 p29〕

## Take-away

1. 熔限降是同一模型三参数化：处理动作与触发条件不同（W14-1#K4）。
2. 区分并发计数器与时间窗口计数器，先问限的是在途数还是速率（W14-1#K21）。
3. 令牌桶胜在积压扛突发；固定窗口有 2 倍突刺（W14-1#K24）。
4. 时钟注入是限流熔断可测的前提：Now() 单方法接口换掉 time.Now() 直呼。
5. 熔断三态机：窗口统计 trip、冷却期后单试探、回填账不能漏（W14-1#K9）。
6. 包装三形态按需选：装饰器通用、interceptor 框架化、方法值直连最少抽象。
7. 阈值优先由压测和容量约束确定；全局配额需要共享计数或明确的副本配额分配。
8. 限流对象可以是 IP，但将 IP 放进指标 label 往往带来过高基数，应谨慎设计。

## 章末参考

- 命令速查：`DNS_RATE_LIMIT="5,5"` / `DNS_BREAKER_THRESHOLD=50`（默认关）；`go test -race -count=3 ./guard/`；`go test -bench . -benchmem ./guard/`
- 源码（本机 go1.26.4）：tokenbucket.go 的 Clock L13、advance L57、Allow L67、WaitN L85；breaker.go 的 ring L64、Allow L131
- main.go 的 initGuard L144、入口门 L350、转发门 L412；breaker.go 的 Mark L156 与 L172（回填账）
- 原材料单元：W14-1（治理理论 25 讲）、W14-2（gRPC interceptor 落地）、W04-6（客户端限流/failover/装饰器）、W03-1（注册登录限流与 Redis 集群化）
- 代码素材：C:dachang/algorithm/rate_limit.go（教学令牌桶，本章 guard 的直接参照）；C:go大厂冲刺--代码/radic/index_service/hub_proxy.go（x/time/rate 的 Allow 用法一例）
- 延伸：golang.org/x/time/rate（生产限流）；go-kratos/aegis/circuitbreaker（生产熔断）；Resilience4j CircuitBreaker 文档（三态参数对照）
- 深读：x/time/rate 源码 limiter.go 的 reserveN（教学版与生产版的差距清单）；aegis 的半开放量策略
- 旧章交叉：p13 §13.1 RED（rate 面板读法）、p17 §RED 在 DNS 里长什么样 与 §压一轮（观测与压测方法）、p30 §receiver 是第一个参数（方法值直连的前题）、p32 §pprof 四级优化（pprof 方法论，本章未用）、p3 §goroutine 有多便宜（每查询一 goroutine 的堵读问题）
- 明确不引：p6（全章实装复盘，无治理内容）；p21（无 bench 内容）

## 练习：p35guard

先跑全绿：`go test -race ./exercises/p35guard/`

1. 预测：rate=10、cap=5 的桶，静置 1s 后最多连续放几个请求？答案在注释里（5：封顶）。
2. 预测：MinSamples=20、阈值 50%，前 19 个全失败会 open 吗？答案：不会，样本不足。
3. 观察：fakeClock 拨 200ms 与真实 Sleep 200ms 的测试时长差，打印两版耗时。
4. 改造：给 TokenBucket 加 `Take(ctx) error`：阻塞直到拿到 1 枚（WaitN(1) 的糖）。
5. 改造：给 Breaker 加 `OnStateChange(fn func(from, to string))`，测试半开回闭合时回调参数。
6. 基准：BenchmarkBreakerMarkFailure 并发回填的 ns/op，与 Allow 对比锁开销。
7. 实验：DNS_BREAKER_THRESHOLD=100 开 100 副本场景（单进程多 goroutine 模拟），观察阈值翻倍对 trip 时机的影响。
