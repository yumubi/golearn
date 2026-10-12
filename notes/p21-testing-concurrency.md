# 第 21 章 · testing/synctest + fuzz：时间与输入的测试武器

> 并发代码的测试有个老难题：Sleep 写长了吧（100ms、1s），测试套件
> 慢得没法用还偶发红；写短了吧，竞态压不出来。1.25 转正的
> `testing/synctest` 给了答案：把测试扔进"气泡"（bubble），假时钟
> 瞬间走完。fuzz 则是给解析器这类纯函数上的性质测试：不用想具体
> 用例，说清楚"任何输入都必须满足什么"，让机器生成几千万个输入。
>
> 读者：读过 P3（goroutine/channel）、P14（errgroup）、P8（test
> 基础 + bench）。前置：会写表驱动测试。Rust 对照（tokio
> `start_paused`、proptest、cargo-fuzz）下沉到章末参考。

## 这章学完你能干什么

给 Retry（指数退避）和 Debounce（防抖）这两个并发原语写毫秒级
全绿的测试，不发一条真实 Sleep；给 CLI 参数解析器挂一个 fuzz 目标，
60 秒跑两千万个输入。终点证据：

```sh
go test -race ./exercises/p21test/
go test -fuzz=FuzzParseArgs -fuzztime=60s ./exercises/p21test/
```

10 个测试 -race 全绿；fuzz 60s 实测 28,529,755 次 execs、0 crash。
四个问题依次是：

1. 假时钟怎么不注入就生效？
2. synctest.Wait 到底在等什么（durably blocked 的精确清单）？
3. bubble 挂死和泄漏长什么样（真机输出的三种形态）？
4. fuzz 对什么、怎么说清"性质"？

## 问题 1：假时钟怎么不注入就生效？

老方案是把 `sleep func(time.Duration)` 当依赖注入进构造器，测试传
假实现。P21 的 Retry/Debounce 没有 Clock 注入（Clock 接口写过又删了）：
`time.NewTimer`/`time.AfterFunc`/`time.Sleep` 在 runtime 层面走的是
同一套计时器，synctest 的 bubble 直接把整套计时器换成了假时钟，
函数自己毫不知情。

```go
synctest.Test(t, func(t *testing.T) {   // bubble 内
    start := time.Now()
    Retry(ctx, RetryOptions{Max: 3, Base: 100 * time.Millisecond},
        func() error { return errors.New("boom") })
    // 两次退避 100+200ms，虚拟时间恰好走到 300ms：
    if time.Since(start) != 300*time.Millisecond { t.Fatal(...) }
})  // 真实耗时：毫秒级
```

关键性质：**虚拟时间与真实墙钟解耦**。假时钟起点是 2000-01-01 UTC
午夜（真机实测 `time.Now()` 在 bubble 开头打印确认），无论虚拟时间
走 300ms 还是 100 小时，真实 CPU 时间只取决于代码执行量。

**不注入的代价**：真时钟路径（工具 retryrun 的 main 函数）没法用
bubble 测，只能靠子进程冒烟（跑一次看退出码）。注入换来可控性，
不注入换来简单性。P21 选简单性，理由是原语足够小，组合逻辑
（sleepCtx 的 timer/select 竞速）已经抽成了可 bubble 测的形态。

**Sleep 版有多糟，实测说话**：等价逻辑用真时钟写（wait=2ms，
断言余量收紧到 1ms，每轮 50 次 Call），真机压测：

| 形态 | 配置 | 结果 |
|---|---|---|
| 真时钟 Sleep 版 | `-count=150 -cpu 1` | 3 次 FAIL（`fired = 0` 一次：timer 触发落在断言窗口外；`fired = 2` 两次：重置窗口里旧 timer 抢先触发），150 轮耗时 2 分钟 |
| synctest 版 | 同等逻辑 `go test -race -count=1` | 全绿，毫秒级；时间推进是确定性事件，`fired` 不可能既不是 0 也不是 1 |

`-cpu 1,4` 的 500 轮配置跑满 300s 还没完（真 Sleep 的墙钟成本），
降到 150 轮单 cpu 才在可忍时间内拿到 3 次实锤。压不出红的同学注意：
这个 flake 是调度相关，负载重的机器更常见。真机上它就是"偶尔
CI 红、重跑就好"的经典形态，synctest 直接消灭这一类问题（不是
缓解，是消灭：时间推进变成确定性事件）。

## 问题 2：synctest.Wait 到底在等什么？

`synctest.Wait`：等 bubble 里**除当前 goroutine 外**的全部 goroutine
进入 durably blocked。这个"durably"是精髓：不是"阻塞着"就行，
是"**不需要外部事件就能继续**"的阻塞才算：

| 算 durably blocked | 不算 |
|---|---|
| bubble 内 channel 的收发 / select（全部通道都在 bubble 内） | `sync.Mutex`/`RWMutex` 的 Lock（等的是持锁者放行） |
| `sync.Cond.Wait` | 真实 I/O（文件、网络、终端） |
| `WaitGroup.Wait`（Add 也发生在 bubble 内） | 系统调用（cgo、fork 等） |
| `time.Sleep` / 计时器到期等待 | |

时间推进条件同源：**全部 goroutine 都 durably blocked 时，假时钟
直接跳到最近的未来事件**（timer 到期、WaitGroup 归零……）。
有一个 goroutine 卡在锁或 I/O 上，时间就不动，Wait 也不返回。

**"锁不算"的实测证明**（`TestBubbleHangs` 的 mutex 模式，子进程跑）：
子 goroutine `mu.Lock()` 卡住（非 durable），主 goroutine `time.Sleep(1ms)`
（durable）后调 `synctest.Wait()`。直觉上"主在睡、时间该走 1ms"，
真机 1.26.4 的输出：

```text
fatal error: all goroutines are asleep - deadlock!

goroutine 1 [synctest.Run (durable), synctest bubble 1]:
...
goroutine 21 [sync.Mutex.Lock, synctest bubble 1]:
...   # 子卡在锁上，非 durable → 时间不推进、Wait 不返回 → 进程级 fatal
```

注意三个层次：不是 synctest 的 panic（那个走 recover 可捕获）、
不是挂死（进程直接退出）、是 runtime 的 fatal deadlock。退出码 2。
这条真机行为官方文档没写清（文档只说 Wait 会"block"），是实验
抓出来的。

**"锁不算"的混合场景**（`TestBubbleHangs` 的 mutex 模式里还有一层）：
锁等待本身不驱动时间，时间由 durable 的睡眠驱动。想让 bubble 里的
主 goroutine 等锁解锁，必须有人在锁外提供 durable 事件（比如另一个
goroutine 先 `time.Sleep` 再解锁）；纯"锁 + 无事件"就是上面那种
runtime fatal。锁不进 durably blocked 清单，意味着"等锁"永远不能
作为 bubble 的时间推进源。

## 问题 3：bubble 挂死和泄漏长什么样？

三种真实形态（都在 `TestBubbleHangs`/`TestGoroutineLeakDetection`
以子进程隔离演示，源码见 retry_test.go 的 `deadlockProbe` 常量）：

| 场景 | 形态 | 真机输出（1.26.4） | 退出码 |
|---|---|---|---|
| 全部 goroutine durable 且无人唤醒 | synctest panic | `PANIC: deadlock: all goroutines in bubble are blocked` | 7（probe 自定） |
| 有 goroutine 卡非 durable（锁） | runtime fatal | `fatal error: all goroutines are asleep - deadlock!` | 2 |
| 泄漏：bubble 有 goroutine 未退出 | Test 收尾 panic | `PANIC: deadlock: main bubble goroutine has exited but blocked goroutines remain` | 7 |

第三种最有用：**泄漏自动指认**。bubble 的根函数返回时，任何还卡在
bubble 内 channel 上的 goroutine 都会触发 panic，输出直接点出泄漏
的 goroutine 和它卡在哪。对比 P14 的 `NumGoroutine` 计数法（要自己
写回落断言），synctest 是零成本内置的。代价：泄漏 goroutine 必须
阻塞在 bubble 内通道上，卡真实 I/O 的泄漏照旧测不到。

**为什么用子进程**：这三种形态两种 panic 一种 fatal，panic 会带崩
整个测试二进制（`fatal` 更是直接杀进程），包内跑不得。惯例：
`t.TempDir()` 写 main.go → `go build` → `exec.Command` 跑模式 →
断言 stderr 文本和退出码。注意 `&testing.T{}` 零值能当 synctest.Test
的 t 用（probe 里为了简洁），但拿不到 t.Log 输出，仅适合这种
独立探针。

## 问题 4：fuzz 对什么、怎么说清"性质"？

fuzz 适合**纯函数 + 输入空间大 + 有不变量**的目标。P8:181 的示意
fuzz 函数是极简版，这章给真目标：P20 wcgo 的 `ParseArgs`
（`exercises/p21test/fuzz_test.go`）。

三个性质，全是"任何输入都必须满足"：

1. **不 panic**：解析器是纯函数，坏输入只该产生错误（fuzz 框架
   隐式检查：crash 即失败）。
2. **错误必然包装 `ErrUsage`**：解析器只可能报这一类错，出现
   其他错误类型就是破坏了错误分类契约。
3. **`ExitCode` 只落 {0, 1, 2} 且与错误自洽**：nil 错误映 0，
   ErrUsage 映 2（shell 惯例）。

```go
func FuzzParseArgs(f *testing.F) {
    seeds := []string{"", "-v", "-v -q a.txt", "-x",
        "file1 file2 -v", "-verbose", "-v -v -v f",
        "文件-µ-🎉 -v", strings.Repeat("-a", 100)}
    for _, s := range seeds { f.Add(s) }
    f.Fuzz(func(t *testing.T, cmdline string) {
        args := strings.Fields(cmdline)   // 空格分隔编码 argv
        cfg, err := p20cli.ParseArgs(args)
        if err == nil && cfg == nil { t.Fatal(...) }
        if err != nil && !errors.Is(err, p20cli.ErrUsage) { t.Fatal(...) }
        code := p20cli.ExitCode(err)
        if err == nil && code != 0 { t.Fatal(...) }
        if err != nil && code != 2 { t.Fatal(...) }
    })
}
```

**种子质量决定起步**：空输入、未知 flag、重复 flag、unicode、超长
串，每条覆盖一个分支。fuzz 参数只支持基本类型（`[]string` 不行，
`string`/`[]byte` 可以），用空格分隔串编码 argv 是权宜，文件名含
空格的场景留给单元测试。

60 秒实测（go1.26.4，M 系列 10 workers）：

```text
fuzz: elapsed: 1m0s, execs: 28529755 (0/sec), new interesting: 188 (total: 199)
PASS
```

2850 万个输入、0 crash、覆盖语料从 11 个种子长到 199 个。这 199 个
"interesting"输入会被缓存（`testdata/fuzz/`），以后回归测试白拿。

**fuzz 能抓到什么，演示一次**：把 `ExitCode` 的 usage 分支从
`return 2` 改成 `return 3`（植入 bug），不用等 fuzz 循环：种子
语料直接命中：

```text
--- FAIL: FuzzParseArgs
    --- FAIL: FuzzParseArgs/seed#4
        fuzz_test.go:57: usage error unknown flag "-x": usage: wcgo [-v] [-q] [files...] maps to 3, want 2
FAIL
```

退出码契约被性质 3 抓住。这就是性质测试和例子的差别：例子只证明
"-x 映 2"这一条路，性质证明"**所有** usage 错误都映 2"。

## 动手：练习 p21test（30-45 分钟，纯标准库）

```sh
cd /Users/akka/Dev/learning/go
go test -race ./exercises/p21test/
go run ./exercises/p21test/cmd/retryrun -n 2 -base 100ms -- sh -c 'exit 1'
```

**预测题**（跑之前写答案；预测的意义在 P5 讲过：猜错的地方才是
你认知的边界）：

1. `TestRetryVirtualTime` 把 `Base` 改成 100ms、`Max` 改 4，虚拟
   elapsed 应该是多少？封顶 `Cap: 300ms` 时又是多少？（§问题 1，
   `Backoff` 的 2^attempt 和封顶逻辑）
2. `TestRetryCancelDuringBackoff` 在第 50ms 取消（第一次退避
   100ms 的中点），返回值是 `context.Canceled` 还是 fn 的错误？
   虚拟 elapsed 是 50ms 还是 100ms？（§问题 2，sleepCtx 的
   timer/ctx 竞速）
3. `TestDebounceSynctest` 里 3 次 `Call` 连发后 `synctest.Wait`
   返回时 `fired` 是多少？虚拟时间走满 200ms 后又是多少？
   （§问题 2，Wait 不等计时器：计时器未到不算"事件完成"）
4. 把 `Retry` 的 `sleepCtx` 换成裸 `time.Sleep(wait)`（不 select
   ctx），`TestRetryCancelDuringBackoff` 会怎样？真实墙钟呢？
   （§问题 1，取消响应从"立即"退化到"睡完才发现"）

**观察验证**：

```sh
go test -race -run TestRetryVirtualTime -v ./exercises/p21test/   # 300ms 虚拟 / 毫秒级真实
go test -race -run TestGoroutineLeakDetection -v ./exercises/p21test/   # 泄漏 panic 真实输出
go test -fuzz=FuzzParseArgs -fuzztime=30s ./exercises/p21test/   # 自己跑 30s 看 execs
```

| 练习 | 对应小节 | 检验什么 |
|---|---|---|
| TestBackoffTable | §问题 1 | 退避序列 100→200→400→800→封顶 |
| TestRetryVirtualTime | §问题 1 | 虚拟时间恰好 300ms，真实毫秒级 |
| TestRetrySuccessSecond | §问题 1 | 成功即停，不再走后续退避 |
| TestRetryCtxCancelImmediate | §问题 2 | 预先取消：0 次执行、elapsed 0 |
| TestRetryCancelDuringBackoff | §问题 2 | 退避中取消：50ms 处精确返回 |
| TestRetryOnRetryLog | §问题 1 | OnRetry 回调三元组（attempt/next/err） |
| TestDebounceSynctest | §问题 2 | 3 次 Call 合并 1 次触发 |
| TestDebounceStop | §问题 2 | Stop 后永不触发 |
| TestGoroutineLeakDetection | §问题 3 | 泄漏 panic 指认（子进程） |
| TestBubbleHangs | §问题 2、3 | 三种挂死形态真机输出（子进程） |
| FuzzParseArgs | §问题 4 | 3 条性质 × 千万级输入 |

**改造题**：给 `Retry` 加抖动（jitter）：`wait = Backoff(...) +
rand(0, wait/4)`。bubble 里 rand 不确定会让虚拟账本对不上。想想
怎么测试（提示：把 rand 抽象成 `func(n int64) int64` 注入固定值，
或断言区间 `100ms ≤ elapsed ≤ 125ms`）。

## Take-away

1. synctest 的假时钟在 runtime 层换计时器，**无需注入**就能让
   `time.Sleep`/`NewTimer`/`AfterFunc` 走虚拟时间；虚拟时间与真实
   墙钟彻底解耦，测试毫秒级全绿且无 flake。
2. `synctest.Wait` 等的是 durably blocked：bubble 内 channel、
   Cond、WaitGroup、Sleep 算；**锁、真实 I/O、系统调用不算**。
   时间推进同条件：一个 goroutine 卡锁，全场时间静止（真机
   fatal deadlock 实证）。
3. bubble 的三种死法要认得：全 durable 无人唤醒 → synctest panic；
   卡非 durable → runtime fatal；泄漏 → 收尾 panic 指认。演示一律
   子进程隔离，panic/fatal 会带崩测试二进制。
4. fuzz 对纯函数挂性质（不 panic / 错误分类 / 映射域），种子覆盖
   全分支起步；性质测试抓的是"所有 usage 错误映 2"这类全称命题，
   例子只能抓单点。
5. 工具面（retryrun）的真时钟路径不在 bubble 测试射程内，用子进程
   冒烟 + 退出码断言兜底。测试射程的边界要心里有数。

---

## 章末参考

### 什么时候用哪把刀（P3 + P14 + P21 对照）

| 场景 | 用什么 | 理由 |
|---|---|---|
| 时间驱动的并发原语测试 | `testing/synctest` | 假时钟零注入，毫秒级全绿 |
| 检测 bubble 内泄漏 | synctest 收尾 panic | 零成本，自动指认 |
| 检测真实 I/O 泄漏 | `runtime.NumGoroutine` 回落（P14） | synctest 看不见非 bubble 阻塞 |
| 纯函数输入空间大 | `go test -fuzz` | 千万级输入扫不变量 |
| 单点回归 | 表驱动单元测试 | 快、可读、可定位 |
| 工具整条链（main + 退出码） | 子进程冒烟 | 测试射程外 |

`go doc testing/synctest.Test` 摘要：Test 在新 bubble 中跑 f；等
bubble 全部 goroutine 退出；死锁时 fail；Cleanup 在 bubble 内跑；
T.Context 关联 bubble 取消；禁 T.Run/T.Parallel/T.Deadline。
`go doc testing/synctest.Wait`：等其它全部 goroutine durably
blocked。常用 flag：`-fuzz=<正则>`、`-fuzztime=60s`（另有
`-fuzzminimizetime`）、`-race` 与 fuzz 互斥（fuzzing 不开 race）。

### Rust 对照：tokio pause、proptest、cargo-fuzz

| Go | Rust | 差异要点 |
|---|---|---|
| `synctest.Test` bubble | `#[tokio::main(start_paused = true)]` 或 `tokio::time::pause()` | tokio 要求 current_thread runtime；`advance(dur)` 手动推，idle 时自动跳到下一个 timer |
| bubble 内 `time.Sleep` | `tokio::time::sleep`（paused 下） | 同样瞬间完成；Go 的 bubble 连普通 channel 阻塞一起管，tokio 只管 timer |
| `f.Fuzz` + `f.Add` | proptest 的 `proptest!` + strategies | proptest 支持 `String`/`Vec` 等结构化策略 + shrinking（失败用例自动缩小）；Go fuzz 只有基本类型，无自动缩小 |
| `go test -fuzz`（libFuzzer 风格语料） | cargo-fuzz（libFuzzer 后端） | cargo-fuzz 要独立 `fuzz/` 目录 + `cargo fuzz run`；`go test -fuzz` 就地跑，语料缓存在 `testdata/fuzz/` |
| `context.WithTimeout` | `tokio::time::timeout(dur, fut)` | Rust 把超时当组合子返回 `Result`；Go 把它编码进 ctx 层层传递 |
| debounce 手写 | `tokio::time::sleep` 复位模式 / `futures::stream` 的 `throttle` | Rust 生态有现成 throttle（间隔而非防抖），Go 手写十几行（本章练习） |

选型直觉：Rust 的异步测试工具绑定在 tokio 运行时上（能力更强但
耦合深），Go 的 synctest 在 runtime 层通用（不管你用什么并发形态）。
proptest 的 shrinking 和结构化策略是真优势，Go fuzz 的语料缓存和
零配置是真省事。

P8:181 的示意 fuzz 函数是同一套 API 的极简版（`f.Add([]byte{...})`
+ `f.Fuzz(func(t *testing.T, data []byte))`，命令 `go test -fuzz <名字>
-fuzztime 30s` 两种写法都合法）；本章 FuzzParseArgs 是可跑全量版。
