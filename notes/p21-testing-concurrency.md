# P21 · testing/synctest + fuzz：时间与输入的测试武器

> 这章学完你能干什么：给 Retry（指数退避）和 Debounce（防抖）写
> 毫秒级全绿的测试，不发一条真实 Sleep；给 CLI 解析器挂 fuzz 目标，
> 60 秒跑两千万个输入。读者：读过 P3、P14、P8。前置：会写表驱动
> 测试。
>
> 动手方式：`go test -race ./exercises/p21test/` 起步，fuzz 部分
> 真跑 60 秒拿自己的 execs 数。

## 体系背景：计时器是运行时的，不是语言

`time.Sleep` 不是一个循环加轮询。Go runtime 给每个 P 维护四叉堆
计时器，`Sleep`/`NewTimer`/`time.After` 全是往这个堆里塞事件，由
调度器统一触发。这个事实是 synctest 的可行性来源：**runtime 既然
统一管理计时器，就能在测试时把整套计时器换成假时钟**。

1.25 转正的 `testing/synctest` 就是干这个的：把测试扔进"气泡"
（bubble），假时钟瞬间走完。这和 Rust 侧 tokio 的
`start_paused`（`tokio::time::pause()`）是同一设计：测试时暂停
时间，idle 时自动跳到下一个 timer。差异在挂载点：tokio 挂在它的
异步运行时上（要求 current_thread），synctest 挂在 Go runtime 上，
普通同步代码也能享受。发起输入侧，Rust 有 proptest（结构化策略
+ shrinking）和 cargo-fuzz；Go 的 `go test -fuzz` 标准库自带。
对照全表在章末。

fuzz 则是另一面：CPU 随机数 + 覆盖率引导，几千万个输入扫不变量。
synctest 不需要注入：`time.NewTimer`/`AfterFunc`/`Sleep` 走的是
同一套 runtime 计时器，bubble 直接把它换成假时钟，函数自己毫不知情：

```go
synctest.Test(t, func(t *testing.T) {   // bubble 内
    start := time.Now()
    Retry(ctx, RetryOptions{Max: 3, Base: 100 * time.Millisecond},
        func() error { return errors.New("boom") })
    // 两次退避 100+200ms，虚拟时间恰好走到 300ms
    if time.Since(start) != 300*time.Millisecond { t.Fatal(...) }
})  // 真实耗时：毫秒级
```

关键性质：**虚拟时间与真实墙钟解耦**。假时钟起点是 2000-01-01 UTC
午夜（bubble 开头打印 `time.Now()` 可确认），虚拟时间走 300ms 还
是 100 小时，真实 CPU 时间只取决于代码执行量。

```sh
$ go test -race -run TestRetryVirtualTime -v ./exercises/p21test/
--- PASS: TestRetryVirtualTime (0.00s)   # 真实 0.00s，虚拟 300ms
```

**不注入的代价**：真时钟路径（工具 retryrun 的 main 函数）没法用
bubble 测，只能靠子进程冒烟（跑一次看退出码）。P21 选简单性，
理由是原语足够小。

**Sleep 版有多糟，实测说话**：等价逻辑用真时钟写（wait=2ms，
每轮 50 次 Call），真机压测：

| 形态 | 配置 | 结果 |
|---|---|---|
| 真时钟 Sleep 版 | `-count=150 -cpu 1` | 3 次 FAIL（timer 触发落在断言窗口外/旧 timer 抢先），150 轮耗时 2 分钟 |
| synctest 版 | 同逻辑 `-race -count=1` | 全绿，毫秒级；时间推进是确定性事件 |

压不出红的注意：这个 flake 是调度相关，负载重的机器更常见。
"偶尔 CI 红、重跑就好"的经典形态，synctest 不是缓解，是消灭。

## synctest.Wait 与 durably blocked

`synctest.Wait`：等 bubble 里**除当前 goroutine 外**的全部 goroutine
进入 durably blocked。"durably"是精髓：不是"阻塞着"就行，是
"**不需要外部事件就能继续**"的阻塞才算：

| 算 durably blocked | 不算 |
|---|---|
| bubble 内 channel 的收发 / select | `sync.Mutex` 的 Lock（等持锁者放行） |
| `sync.Cond.Wait` | 真实 I/O（文件、网络） |
| `WaitGroup.Wait`（Add 在 bubble 内） | 系统调用（cgo、fork） |
| `time.Sleep` / 计时器等待 | |

时间推进条件同源：**全部 goroutine 都 durably blocked 时，假时钟
直接跳到最近的未来事件**。有一个 goroutine 卡在锁或 I/O 上，时间
就不动，Wait 也不返回。

**"锁不算"的实测证明**：子 goroutine `mu.Lock()` 卡住（非
durable），主 goroutine `time.Sleep(1ms)` 后调 `Wait()`。真机
1.26.4 输出：

```text
fatal error: all goroutines are asleep - deadlock!

goroutine 1 [synctest.Run (durable), synctest bubble 1]:
goroutine 21 [sync.Mutex.Lock, synctest bubble 1]:
```

注意层次：不是 synctest 的 panic（recover 可捕获）、不是挂死，
是 runtime 的 fatal deadlock，退出码 2。官方文档没写清这条，是
实验抓出来的。锁不进 durable 清单，意味着"等锁"永远不能作为
bubble 的时间推进源。

## bubble 的三种死法

都在 `TestBubbleHangs`/`TestGoroutineLeakDetection` 以子进程隔离
演示（源码 retry_test.go 的 `deadlockProbe` 常量）：

| 场景 | 形态 | 真机输出 | 退出码 |
|---|---|---|---|
| 全部 durable 且无人唤醒 | synctest panic | `PANIC: deadlock: all goroutines in bubble are blocked` | 7（probe 自定） |
| 卡非 durable（锁） | runtime fatal | `fatal error: all goroutines are asleep` | 2 |
| 泄漏：有 goroutine 未退出 | 收尾 panic | `main bubble goroutine has exited but blocked goroutines remain` | 7 |

第三种最有用：**泄漏自动指认**。bubble 根函数返回时，任何还卡在
bubble 内 channel 上的 goroutine 都触发 panic，输出直接点出泄漏
位置。对比 P14 的 `NumGoroutine` 计数法（自己写回落断言），这是
零成本内置的。代价：泄漏必须阻塞在 bubble 内通道上，卡真实 I/O
的照旧测不到。

**为什么用子进程**：两种 panic 一种 fatal，都会带崩测试二进制，
包内跑不得。惯例：`t.TempDir()` 写 main.go → `go build` →
`exec.Command` 跑 → 断言 stderr 和退出码。

## fuzz：性质测试

fuzz 适合**纯函数 + 输入空间大 + 有不变量**的目标。P8 的示意
fuzz 函数是极简版，这章给真目标：P20 wcgo 的 `ParseArgs`
（`exercises/p21test/fuzz_test.go`）。三个性质，全是"任何输入都
必须满足"：

1. **不 panic**：坏输入只该产生错误（fuzz 框架隐式检查）。
2. **错误必然包装 `ErrUsage`**：出现其他错误类型就是破坏契约。
3. **`ExitCode` 只落 {0, 1, 2} 且与错误自洽**。

```go
func FuzzParseArgs(f *testing.F) {
    seeds := []string{"", "-v", "-v -q a.txt", "-x",
        "file1 file2 -v", "-verbose", "-v -v -v f",
        "文件-µ-🎉 -v", strings.Repeat("-a", 100)}
    for _, s := range seeds { f.Add(s) }
    f.Fuzz(func(t *testing.T, cmdline string) {
        args := strings.Fields(cmdline)   // 空格分隔编码 argv
        cfg, err := p20cli.ParseArgs(args)
        if err != nil && !errors.Is(err, p20cli.ErrUsage) { t.Fatal(...) }
        code := p20cli.ExitCode(err)
        if err == nil && code != 0 { t.Fatal(...) }
        if err != nil && code != 2 { t.Fatal(...) }
    })
}
```

种子质量决定起步：空输入、未知 flag、unicode、超长串，每条覆盖
一个分支。fuzz 参数只支持基本类型（`[]string` 不行），用空格分隔
串编码 argv 是权宜。

60 秒实测（go1.26.4，M 系列 10 workers）：

```text
fuzz: elapsed: 1m0s, execs: 28529755 (0/sec), new interesting: 188 (total: 199)
PASS
```

2850 万个输入、0 crash、覆盖语料从 11 个种子长到 199 个，缓存在
`testdata/fuzz/`，以后回归测试白拿。

**fuzz 能抓到什么，演示一次**：把 `ExitCode` 的 usage 分支
`return 2` 改成 `return 3`（植入 bug），种子语料直接命中：

```text
--- FAIL: FuzzParseArgs
    --- FAIL: FuzzParseArgs/seed#4
        fuzz_test.go:57: usage error maps to 3, want 2
```

例子只证明"-x 映 2"这一条路，性质证明"**所有** usage 错误都映 2"。

## 动手：练习 p21test（30-45 分钟，纯标准库）

```sh
cd /Users/akka/Dev/learning/go
go test -race ./exercises/p21test/
go run ./exercises/p21test/cmd/retryrun -n 2 -base 100ms -- sh -c 'exit 1'
```

**预测题**（跑之前写答案；猜错的地方才是认知边界）：

1. `TestRetryVirtualTime` 把 `Base` 改 100ms、`Max` 改 4，虚拟
   elapsed 是多少？封顶 `Cap: 300ms` 时呢？
2. `TestRetryCancelDuringBackoff` 在第 50ms 取消（第一次退避
   100ms 中点），返回 `context.Canceled` 还是 fn 的错误？虚拟
   elapsed 是 50ms 还是 100ms？
3. `TestDebounceSynctest` 里 3 次 `Call` 连发后 `Wait` 返回时
   `fired` 是多少？虚拟时间走满 200ms 后呢？
4. 把 `Retry` 的 `sleepCtx` 换成裸 `time.Sleep(wait)`，取消响应
   会怎样退化？

**观察验证**：

```sh
go test -race -run TestRetryVirtualTime -v ./exercises/p21test/
go test -race -run TestGoroutineLeakDetection -v ./exercises/p21test/
go test -fuzz=FuzzParseArgs -fuzztime=30s ./exercises/p21test/
```

预测题把上面机制各戳一遍：跑之前写答案，猜错的地方才是认知边界。

| 练习 | 检验什么 |
|---|---|
| TestBackoffTable | 退避序列 100→200→400→800→封顶 |
| TestRetryVirtualTime | 虚拟 300ms，真实毫秒级 |
| TestRetrySuccessSecond | 成功即停 |
| TestRetryCtxCancelImmediate | 预先取消：0 次执行 |
| TestRetryCancelDuringBackoff | 退避中取消：50ms 精确返回 |
| TestRetryOnRetryLog | OnRetry 回调三元组 |
| TestDebounceSynctest | 3 次 Call 合并 1 次触发 |
| TestDebounceStop | Stop 后永不触发 |
| TestGoroutineLeakDetection | 泄漏 panic 指认（子进程） |
| TestBubbleHangs | 三种挂死形态（子进程） |
| FuzzParseArgs | 3 条性质 × 千万级输入 |

**改造题**：给 `Retry` 加抖动：`wait = Backoff(...) + rand(0, wait/4)`。
bubble 里 rand 不确定会让虚拟账本对不上。提示：把 rand 抽象成
`func(n int64) int64` 注入固定值，或断言区间
`100ms ≤ elapsed ≤ 125ms`。

## Take-away

1. synctest 在 runtime 层换计时器，无需注入；虚拟时间与真实墙钟
   彻底解耦，测试毫秒级全绿且无 flake。
2. `Wait` 等的是 durably blocked：bubble 内 channel/Cond/WaitGroup/
   Sleep 算；锁、真实 I/O、系统调用不算。一个 goroutine 卡锁，
   全场时间静止（真机 fatal 实证）。
3. bubble 三种死法要认得：synctest panic / runtime fatal / 泄漏
   收尾 panic。演示一律子进程隔离。
4. fuzz 挂性质（不 panic/错误分类/映射域），种子覆盖全分支起步；
   性质抓全称命题，例子抓单点。
5. 真时钟路径（main）不在 bubble 射程内，子进程冒烟 + 退出码
   断言兜底。

## 章末参考

### 用刀对照

| 场景 | 用什么 | 理由 |
|---|---|---|
| 时间驱动原语测试 | `testing/synctest` | 假时钟零注入 |
| bubble 内泄漏 | synctest 收尾 panic | 零成本自动指认 |
| 真实 I/O 泄漏 | `NumGoroutine` 回落（P14） | synctest 看不见非 bubble 阻塞 |
| 纯函数输入空间大 | `go test -fuzz` | 千万级输入扫不变量 |
| 工具整条链 | 子进程冒烟 | 测试射程外 |

`go doc testing/synctest.Test` 摘要：新 bubble 中跑 f；等全部
goroutine 退出；死锁 fail；禁 T.Run/T.Parallel/T.Deadline。
常用 flag：`-fuzz=<正则>`、`-fuzztime=60s`、`-race` 与 fuzz 互斥。

### Rust 对照总表

| Go | Rust | 差异要点 |
|---|---|---|
| `synctest.Test` bubble | `#[tokio::main(start_paused = true)]` | tokio 要求 current_thread runtime；Go 在 runtime 层通用 |
| bubble 内 `time.Sleep` | `tokio::time::sleep`（paused） | Go 连 channel 阻塞一起管，tokio 只管 timer |
| `f.Fuzz` + `f.Add` | proptest strategies | proptest 有 shrinking；Go 语料缓存零配置 |
| `go test -fuzz` | cargo-fuzz（libFuzzer 后端） | cargo-fuzz 要独立 fuzz/ 目录 |
| `context.WithTimeout` | `tokio::time::timeout` | Rust 超时是组合子，Go 编码进 ctx |

选型直觉：Rust 异步测试工具绑定 tokio（能力强、耦合深），Go 的
synctest 在 runtime 层通用。proptest 的 shrinking 是真优势，Go 的
语料缓存是真省事。

P8 的示意 fuzz 函数是同一套 API 的极简版；本章 FuzzParseArgs 是
可跑全量版。
