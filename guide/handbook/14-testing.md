# 14 测试策略：可测的设计，加上三件专用武器

> 详细程度：深入 · 约 35 分钟
> 材料：W04-3、W04-4、W04-5、W04-6 · 旧长版：`notes/p21-testing-concurrency.md`、`notes/p2_5-idiom.md`（表驱动）、`notes/p8-fast-track.md`（测试部分）
> 前置：04 接口与组合、05 错误与资源、06 到 07 并发

## 这一节要建立的认知

Go 的测试工具链是语言自带的，几乎不需要框架：`go test` 负责发现、运行、竞态检测、覆盖率、基准和模糊测试。所以测试质量的差距不在工具，而在**被测代码是否可测**。

1. **可测性是设计出来的**。面向接口加依赖注入，让每一层都能把下一层替换成假的。小接口让假对象也很小。
2. **测试分层有分工**。单测不碰任何第三方组件，追求快；集成测试验证组件组合，用真依赖；更上层的交给测试团队或手工。〔W04-4 p5-6〕
3. **三类特殊问题各有专用武器**：并发用 `-race`，时间用 `testing/synctest`，输入空间用 `fuzz`。别用 `time.Sleep` 去猜。

## 和你已知语言的差异

| 问题 | Java | Rust | Go |
|---|---|---|---|
| 约定 | JUnit 注解 | `#[test]` | 文件以 `_test.go` 结尾，函数 `TestXxx(t *testing.T)` |
| 用例组织 | 参数化测试 | 手写循环 | **表驱动加 `t.Run`**，是惯用法 |
| 替身 | Mockito 为主 | `mockall`、手写 | 优先手写 fake；接口大或要验证调用次序时用 `uber-go/mock` |
| 竞态检测 | 无内建 | 编译期排除 | `go test -race` |
| 时间相关 | 注入时钟 | `tokio::time::pause` | `testing/synctest` 虚拟时钟 |
| 性质测试 | jqwik 等第三方 | `proptest` 第三方 | 标准库内建 fuzz |

〔W04-4 p7〕测试文件、函数名前缀、参数类型是仅有的三条规则，同一套机制也用来写集成、冒烟和回归测试。

## 一、命令：几个值得记住的开关

```text
go test ./...            # 当前目录及子目录，CI 里用这个
go test -run TestGreet/存在   # 只跑某个子用例
go test -count=1 ./...   # 关掉结果缓存，排查时必加
go test -race ./...      # 竞态检测，CI 默认开
go test -cover ./...     # 覆盖率
```

`go test your_test.go` 这种写法要带上用到的所有文件，容易漏报 `undefined`，一般不用。〔W04-4 p11〕

## 二、可测性来自设计：自底向上抽接口

初始化链是 DB 和 Redis，到 DAO 和 Cache，到 Repository，到 Service，到 Handler，再到路由，层层组装就是标准的依赖注入（细节见 10 节）。〔W04-3 p4〕改造顺序**自底向上**：最底层的缓存和 DAO 不依赖别人，先抽接口，再往上。〔W04-3 p28-30〕面向接口的收益只有扩展性，但它是可测性的前提：有了接口，测试里才能注入替身。〔W04-3 p27〕

抽象的度要克制：只考虑有限的几种变化，不想太长远。非主流第三方（短信、对象存储）考虑可替换，MySQL、Redis 这种主流依赖不必为替换而抽象。〔W04-3 p35〕〔W04-3 p36〕

## 三、表驱动：Go 测试的基本句型

三段式：测试用例的定义（匿名结构体）、具体用例列表、遍历执行。用例名放进 `t.Run`，失败时能精确定位，也能单独重跑一个。〔W04-4 p12-15〕

课程总结的用例字段最全有六个：名字、预期输入、预期输出、mock、数据准备、数据清理。单测常用前四个，集成测试常用后两个。简单的方法用不上全部。〔W04-4 p14〕

```go
tests := []struct {
	name    string
	store   fakeStore
	id      string
	want    string
	wantErr error
}{
	{"存在", fakeStore{...}, "1", "hello, ann", nil},
	{"不存在", fakeStore{...}, "9", "", ErrNotFound},
	{"底层故障", fakeStore{...}, "1", "", boom},
}
for _, tc := range tests {
	t.Run(tc.name, func(t *testing.T) {
		t.Parallel()
		got, err := NewService(tc.store).Greet(context.Background(), tc.id)
		if !errors.Is(err, tc.wantErr) { t.Fatalf("err = %v, want %v", err, tc.wantErr) }
		if got != tc.want { t.Fatalf("got %q, want %q", got, tc.want) }
	})
}
```

〔实测〕完整示例在 go1.26.4 上用 `go test -race -v -count=1` 全绿，三个子用例并行执行。Go 1.22 起循环变量每轮独立，`t.Parallel()` 配合闭包不会再串用例变量。

用例设计的顺序：先写正常流程，再基于它复制改出各个异常流程。覆盖标准：单测至少做到分支覆盖，常见目标是 80% 以上；集成测试覆盖主要的正常和异常流程。〔W04-4 p16〕那些基本无法触发的分支（比如密码哈希库的错误分支），可以不测，但没测到的代码必须认真 review。〔W04-4 p17〕

## 四、替身：fake 优先，mock 补位

**fake** 是一个能真正工作的简化实现。接口小，fake 就小，用函数字段还能让每个用例自己决定行为：

```go
type fakeStore struct {
	get func(ctx context.Context, id string) (User, error)
}
func (f fakeStore) Get(ctx context.Context, id string) (User, error) { return f.get(ctx, id) }
```

**mock** 是带期望的替身：断言某个方法被以什么参数、调用了几次、什么顺序。课程用的是 `uber-go/mock`（官方的 `golang/mock` 已停止维护），用 `mockgen` 从接口生成。〔W04-4 p25-27〕使用铁律：先 `EXPECT()`，再写同名方法与参数条件，再 `Return()`；**设计了几个模拟调用，就要全部用上，且次序对得上**，多了、少了、乱了都会失败。〔W04-4 p28〕

选择原则：

- 接口只有一两个方法，或者只关心返回值：手写 fake，读起来最直白。
- 接口大，或者你确实要验证"调用了几次、顺序如何"：mock。代价是测试与实现细节耦合，重构时容易整片红。
- 第三方客户端没有暴露接口（比如 GORM）：课程用 `sqlmock` 编排 `ExpectBegin`、`ExpectExec`、`ExpectCommit`，最后 `ExpectationsWereMet()`；GORM 初始化要关掉版本探测、ping 和默认事务，避免多出调用。〔W04-4 p66-69〕另一派做法是 DAO 层直接用真实数据库（容器或内存库），`sqlmock` 只验证 SQL 字符串的收发，不验证 SQL 真的有效，这是需要你按团队取舍的地方。

## 五、各层怎么测：金字塔在服务里的落地

课程把一个服务从上到下逐层测了一遍，每层只替换它的直接依赖：

| 层 | 被测对象 | 替身 | 要点 |
|---|---|---|---|
| Handler | HTTP 入口 | mock 或 fake 的 Service | 构造请求、`httptest.NewRecorder`、`ServeHTTP`、断言状态码和响应体〔W04-4 p29-31〕 |
| Service | 业务逻辑 | mock 的 Repository | 每个分支一个用例，如用户不存在与密码错误返回同一个错误〔W04-4 p45-49〕 |
| Repository | 缓存加库 | mock 的 DAO 和 Cache | 缓存未命中要模拟三次调用：`Get` 未命中、DAO 查询、`Set` 回写〔W04-4 p52-55〕 |
| Cache | Redis 封装 | mock 的 `redis.Cmdable` | 对第三方接口用反射模式生成 mock〔W04-4 p61-63〕 |
| DAO | SQL | `sqlmock` 或真库 | 增删改关注 `lastInsertId` 和 `rowsAffected`，冲突用 MySQL 错误码 1062 模拟〔W04-4 p66-71〕 |

Handler 测试的核心，是把 Go 1.22 的路由、`httptest` 和 fake 拼起来：

```go
rec := httptest.NewRecorder()
h.ServeHTTP(rec, httptest.NewRequest("GET", "/greet/2", nil))
// rec.Code == 404，rec.Body.String() == "no such user\n"
```

〔实测〕该用例在 go1.26.4 上通过。`ServeHTTP` 等价于服务器真的收到了一个网络请求，但没有端口、没有网络，所以快且稳定。〔W04-4 p29〕

一个小细节：数据库只存到毫秒，断言时间字段前要先把期望值截断到毫秒再重建，否则含纳秒的 `time.Time` 永远不等。〔W04-4 p58〕

## 六、集成测试：用真依赖验证业务流程

单测保证组件单独没问题，组合是否有问题靠集成测试。用例设计以**业务流程**为依据：程序员编码造成的系统错误用户触发不了，几乎测不到；依赖并发的异常流程也难自动化，靠手工。〔W04-5 p3-4〕

用例结构是六件套：名字、`before`（准备数据）、`after`（验证并清理数据）、输入、预期状态码、预期结果。执行流程：`before`、构造请求、`Recorder`、`ServeHTTP`、比较响应码和 JSON 反序列化后的结构、`after`。〔W04-5 p6-7〕比较响应时直接比较结构体，避免比较 JSON 字符串。

三条铁律：〔W04-5 p12〕

1. 直接依赖的第三方（Redis、MySQL）要**验证状态**：`after` 里查数据是否写进去了、TTL 对不对，然后清理。
2. 调用别人的接口，只验证返回响应。
3. **永远不要让用例互相依赖**，否则重构一动就整片崩。

怎么测"发送短信"这类无法自动确认的外部效果：自动化部分用内存短信服务验证流程，另由测试人员手动发一次真短信做双层验证。〔W04-5 p5〕

## 七、三件专用武器

### 并发：`-race`

mock 测不出并发安全问题，它只能验证无并发时的结果；并发 bug 要靠 `-race` 和 code review。〔W04-6 p31-32〕`-race` 做的是动态分析，只抓**实际发生**的竞争，所以要多跑几遍、不同调度。CI 默认开。06 讲过它的输出形态。

### 时间：`testing/synctest`

用 `time.Sleep` 等重试退避、超时、定时器，测试又慢又不稳。`synctest.Test` 在一个隔离的"气泡"里给出**虚拟时钟**：初始时间固定，只有当气泡内所有 goroutine 都处于 durably blocked（只能被气泡内别的 goroutine 唤醒）时，时间才快进到下一个事件。

```go
synctest.Test(t, func(t *testing.T) {
	start := time.Now()
	err := Retry(context.Background(), 3, 100*time.Millisecond, func() error {
		return errors.New("fail")
	})
	// 3 次失败，中间两次退避 100ms + 200ms；虚拟时钟恰好走了 300ms，真实耗时接近 0
	if got := time.Since(start); got != 300*time.Millisecond { t.Fatalf("elapsed = %v", got) }
})
```

〔实测〕`go test -race -run TestRetryVirtualTime -v` 通过。如果 `Retry` 写成最后一次失败后还多等一次退避（常见写法错误），同一个断言得到的是 700ms 而不是 300ms，这类问题 `Sleep` 式测试几乎看不出来。

要点：等锁、真实 I/O、系统调用都**不算** durably blocked，卡在这些上面时间不会快进；气泡根函数返回时若仍有 goroutine 卡在气泡内的 channel 上，会自动 panic 并指认泄漏位置，相当于内置的泄漏检测。见旧长版 `notes/p21-testing-concurrency.md` 的 durably blocked 对照表和三种死法。

### 输入空间：fuzz

fuzz 测的是**性质**，不是具体输入：给一批种子，由引擎变异出成千上万输入，断言对任何输入都成立的规律。

```go
func FuzzParseKV(f *testing.F) {
	f.Add("a=b"); f.Add("=x"); f.Add("")
	f.Fuzz(func(t *testing.T, s string) {
		k, v, err := ParseKV(s)
		if err != nil { return }
		if k+"="+v != s { t.Fatalf("roundtrip: %q -> %q %q", s, k, v) } // 成功则能还原
	})
}
```

〔实测〕`go test -run '^$' -fuzz FuzzParseKV -fuzztime 3s` 在本机跑了约 71 万次执行无失败；平时 `go test` 只跑种子，不开引擎。常用的性质：往返一致、对任何输入不 panic、错误路径返回的资源是 nil。找到的失败输入会存进 `testdata/fuzz`，成为以后每次都跑的回归用例。适合解析器、编解码、协议处理这类"外部可控输入"的代码。

## 八、基准与覆盖率

基准用 `b.Loop()`（Go 1.24 起）取代 `for i := 0; i < b.N; i++`，它会阻止编译器把被测调用优化掉。〔实测〕

```go
func BenchmarkParseKV(b *testing.B) {
	for b.Loop() {
		ParseKV("key=value")
	}
}
// go test -bench ParseKV -benchmem   →  约 3.3 ns/op，0 B/op，0 allocs/op（Apple M4）
```

数字随机器浮动，看量级和 `allocs/op`。覆盖率是诊断工具，不是目标：低覆盖率说明有路径没人管，高覆盖率不说明测试有意义。

## 常见坑

1. 用例之间共享可变状态或依赖执行顺序，重构时整片红。
2. 用 `time.Sleep` 等异步结果，慢且不稳；改成 channel 同步、`synctest`，或带超时的轮询。
3. 直接读 `time.Now()`，测试无法控制时间；注入时钟或放进 `synctest`。
4. mock 的期望次数、顺序与实现细节绑定，重构一动就红。
5. 只测 happy path；错误分支用 fake 注入错误来覆盖。
6. 忘了 `-count=1`，看到的是缓存结果。
7. 集成测试不清理数据，下一次运行互相污染。
8. 用单测去"证明"并发安全，mock 做不到，要靠 `-race`。

## 判断题（先答再看）

1. 为什么说面向接口的收益是扩展性，却又说它是可测性的前提？
2. 同一个仓库里，什么时候该手写 fake，什么时候该用 mock？
3. 为什么集成测试里 `after` 既要验证数据又要清理？
4. `synctest` 里一个 goroutine 卡在 `mu.Lock()` 上，虚拟时间会前进吗？
5. fuzz 测的"性质"和普通测试里的"期望值"差别在哪？

练习包 `exercises/p21test/`：`TestBackoffTable`、`TestRetryVirtualTime`、`TestRetryCtxCancelImmediate`、`TestDebounceSynctest`、`TestGoroutineLeakDetection`、`FuzzParseArgs`；`exercises/p2_5idiom/` 的 `TestTableDriven`、`TestFake`。

## 在项目里

- **小型集成测试**：`dnsproxi-go/main_test.go` 的 `TestD1Forward` 起一个 fake 上游和真实的代理（`startProxy`），用真实 UDP 回环走完"客户端、代理、上游、回包"四跳，断言回包的 ID 与 `QR` 位。上游是假的，网络是真的。
- **性质测试**：`dnsproxi-go/tcp/fuzz_test.go` 的 `FuzzDecodeFrame` 种子覆盖空输入、半截前缀、前缀大于正文、零长度帧、粘包，断言错误路径不返回缓冲区，且错误只能来自 EOF 族。
- **带并发的测试**：`dnsproxi-go/cache/cache_test.go` 的 `TestConcurrentHitMiss` 配合 `-race`；`dnsproxi-go/tcp/server_test.go` 的 `TestMaxConns` 与 `TestMaxInFlight` 验证并发上限。
- **指标行为测试**：`dnsproxi-go/metrics_test.go` 的 `TestMetricsDeadUpstream` 等，在死上游场景断言指标计数。

## 想深入去哪查

- 原材料：W04-3（面向接口与依赖注入，含 wire 的取舍）、W04-4（单测全套：Handler、Service、Repository、Cache、DAO，含 mock 与 sqlmock 的具体用法）、W04-5（集成测试的用例结构、验证码流程、铁律）、W04-6（第三方服务调用治理：限流装饰器、failover 的测试方式，稳定性一节会再用到）。
- 旧长版：`notes/p21-testing-concurrency.md`（synctest 的 durably blocked 清单、气泡三种死法、fuzz 与 race 的开销数据）、`notes/p2_5-idiom.md`（表驱动与 fake 的最小例子）、`notes/p8-fast-track.md`（测试部分）。
- 不在这一节：`mockgen` 的反射模式生成细节〔W04-4 p61-63〕、`sqlmock` 的完整编排写法〔W04-4 p66-71〕，写 DAO 测试时再查。
