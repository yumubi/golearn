# 14 测试策略：可测的设计，加上三件专用武器

> 详细程度：深入 · 约 70 分钟
> 材料：W04-3、W04-4、W04-5、W04-6、W06-1 · 旧长版：`notes/p21-testing-concurrency.md`、`notes/p2_5-idiom.md`（表驱动）、`notes/p8-fast-track.md`（测试部分）
> 前置：04 接口与组合、05 错误与资源、06 到 07 并发

## 先看一个让人愣住的现象

下面这个 `Service` 十分普通：从某个存储里查用户，拼一句问候。它**没有任何测试上的特殊代码**，却能被打上 `-race` 标记、跑出覆盖率、注入假仓库、做完虚拟时间实验。先看代码：

```go
package main

import (
	"context"
	"errors"
	"fmt"
)

var ErrNotFound = errors.New("user not found")

type User struct{ Name string }

// Store 是 Service 对存储层的全部要求。
type Store interface {
	Get(ctx context.Context, id string) (User, error)
}

type Service struct{ store Store }

func NewService(s Store) *Service { return &Service{store: s} }

func (s *Service) Greet(ctx context.Context, id string) (string, error) {
	u, err := s.store.Get(ctx, id)
	if err != nil {
		return "", err
	}
	return "hello, " + u.Name, nil
}

// fakeStore 是手写的假仓库：能工作，但没有数据库。
type fakeStore struct{ users map[string]User }

func (f fakeStore) Get(_ context.Context, id string) (User, error) {
	u, ok := f.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	return u, nil
}

func main() {
	ctx := context.Background()
	s := NewService(fakeStore{users: map[string]User{"1": {Name: "ann"}}})
	g, err := s.Greet(ctx, "1")
	fmt.Println(g, err)
	_, err = s.Greet(ctx, "9")
	fmt.Println(errors.Is(err, ErrNotFound), err)
}
```

```text
hello, ann <nil>
true user not found
```

`Service` 依赖的是 `Store` 接口，`main` 里塞的是 `fakeStore`：一个用 map 假装数据库的"能工作的简化实现"。正因为有了这个接口，测试可以不打真数据库地跑全部业务逻辑。反过来，如果 `NewService` 的参数是 `*sql.DB`，上面每一行测试都得先起一个 MySQL。

所以这一节要建立的核心认知是：**Go 的测试工具链是语言自带的（`go test` 一个命令覆盖发现、运行、竞态检测、覆盖率、基准和模糊测试），测试质量的差距不在工具，而在被测代码是否可测。**〔自〕课程对应的做法是：单测先行、不碰任何第三方组件〔W04-4 p5-6〕。可测性怎么来、测试怎么写、三层问题各用什么武器，按这个顺序讲。

## 和你已知语言的差异

| 问题 | Java | Rust | Go |
|---|---|---|---|
| 约定 | JUnit 注解 | `#[test]` | 文件以 `_test.go` 结尾，函数 `TestXxx(t *testing.T)` |
| 用例组织 | 参数化测试 | 手写循环 | **表驱动加 `t.Run`**，是惯用法 |
| 替身 | Mockito 为主 | `mockall`、手写 | 优先手写 fake；接口大或要验证调用次序时用 `uber-go/mock` |
| 竞态检测 | 无内建 | 编译期排除 | `go test -race` |
| 时间相关 | 注入时钟 | `tokio::time::pause` | `testing/synctest` 虚拟时钟 |
| 性质测试 | jqwik 等第三方 | `proptest` 第三方 | 标准库内建 fuzz |

〔W04-4 p7〕表里值得展开的是第一行和最后一行。第一行：Go 的全部约定就是三条（文件名、函数前缀、参数类型），编译器靠命名把测试挑出来，同一套机制既写单测也写集成、冒烟、回归测试，没有第二套框架要学。最后一行：Rust 和 Java 的性质测试要引第三方，Go 的 fuzz 是标准库内建的，`go test -fuzz` 一个开关。

## 一、命令：几个值得记住的开关

```text
go test ./...            # 当前目录及子目录，CI 里用这个
go test -run TestGreet/存在   # 只跑某个子用例
go test -count=1 ./...   # 关掉结果缓存，排查时必加
go test -race ./...      # 竞态检测，CI 默认开
go test -cover ./...     # 覆盖率
```

五个开关里 `-run` 和 `-count` 是日常排查双件套：`-run` 后面是正则，`TestGreet/存在` 表示只跑 `TestGreet` 里名字叫"存在"的子用例（子用例名来自 `t.Run`，所以表驱动不是装饰，它决定了你能不能单跑一个）；`-count=1` 强制绕过结果缓存，否则代码没改时 `go test` 直接回上次的 ok，你新加的打印根本不会出现。

`go test your_test.go` 这种写法要带上用到的所有文件，漏一个就报 `undefined: NewUserHandler` 这类编译错，问题根本不在测试逻辑，一般不用。〔W04-4 p11〕

## 二、可测性来自设计：自底向上抽接口

回到开头的现象：为什么 `Service` 能被轻松塞进假仓库？因为依赖是一个接口，而且是从 `main` 一路传进来的。这条链有名字，叫**依赖注入**：A 要调用 B，A 初始化时就要求外面传一个构建好的 B，而不是自己 `gorm.Open` 一个。〔W04-3 p4〕课程里的初始化链是：DB 和 Redis 到 DAO 和 Cache，到 Repository，到 Service，到 Handler，再到路由，层层组装。〔W04-3 p4〕给已有代码补可测性时，改造顺序是**自底向上**：最底层的缓存和 DAO 不依赖别人，先抽它们的接口，再往上改 Repository、Service。〔W04-3 p28-30〕为什么自底向上？因为抽接口要改的是"使用方"的签名，底层类型的使用方少，先改它们代价最小；等上层改完，正好接住下层已经就位的接口。

但有一个必须先说破的问题：**面向接口的好处是扩展性，为什么它成了可测性的前提？** 面向接口本身不提高性能，也不提高可靠性，它的收益只有"将来可以换实现"。〔W04-3 p27〕可测性是同一枚硬币的另一面：**替身也是一种"另一种实现"**。没有接口，测试里的 fake 没有位置可放；有了接口，fake 就是众多实现里的一个。所以结论是：扩展性是主收益，可测性是它免费附送的。抽象的度要克制：只考虑有限的几种变化，不想太长远；非主流第三方（短信、对象存储）考虑可替换，MySQL、Redis 这种主流依赖不必为替换而抽象。〔W04-3 p35-36〕

## 三、表驱动：Go 测试的基本句型

句型是三段式：测试用例的定义（匿名结构体）、具体用例列表、遍历执行。用例名放进 `t.Run`，失败时输出里能看到 `TestGreet/不存在` 这样的路径，能精确定位，也能用 `-run TestGreet/不存在` 单独重跑一个。〔W04-4 p12-15〕把用例定义看成表头、每个用例看成一行数据，Table Driven 这个名字就直白了。字段最全有六个：名字、预期输入、预期输出、mock、数据准备（before）、数据清理（after）；单测常用前四个，集成测试常用后两个。〔W04-4 p14〕

把开头那个 `Service` 完整测一遍，单测形态是这样的：

```go
// 测试文件 test_greet.go（片段）：`package main` 加上上面的类型定义后即可 `go test`
func TestGreet(t *testing.T) {
	tests := []struct {
		name    string
		users   map[string]User
		id      string
		want    string
		wantErr error
	}{
		{"存在", map[string]User{"1": {Name: "ann"}}, "1", "hello, ann", nil},
		{"不存在", nil, "9", "", ErrNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := NewService(fakeStore{users: tc.users}).Greet(context.Background(), tc.id)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
```

```text
=== RUN   TestGreet
=== PAUSE TestGreet/存在
=== PAUSE TestGreet/不存在
=== CONT  TestGreet/存在
=== CONT  TestGreet/不存在
--- PASS: TestGreet (0.00s)
    --- PASS: TestGreet/存在 (0.00s)
    --- PASS: TestGreet/不存在 (0.00s)
PASS
```

〔实测〕在 go1.26.4 上 `go test -race -v -count=1` 输出如上，全绿。

〔实测〕这段代码里有一个 Java 程序员会皱眉的点：闭包直接捕获了循环变量 `tc`，每个子测试还 `t.Parallel()` 挂起再恢复，为什么不会互相串值？因为模块的 `go.mod` 声明 `go 1.22` 及以上时，循环变量每轮迭代都是新变量，闭包捕获的各是各的；声明低于 1.22 的老模块仍是旧语义，这里必须写 `tc := tc`。〔自〕这是少数"新版 Go 把旧坑删掉了"的例子。

断言为什么用 `errors.Is` 而不是 `==`：`==` 只匹配同一个错误值，`errors.Is` 沿 `%w` 链找，fake 换成包装过的错误时测试不用改。这也是 05 节讲过的哨兵错误配对写法。用例设计的顺序：先写正常流程，再基于它复制改出各个异常流程。覆盖标准：单测至少做到分支覆盖，常见目标是 80% 以上；集成测试覆盖主要的正常和异常流程。〔W04-4 p16〕那些基本无法触发的分支（比如密码哈希库的超时错误分支）可以不测，但没测到的代码必须认真 review。〔W04-4 p17〕

## 四、替身：fake 优先，mock 补位

**fake** 是一个能真正工作的简化实现，开头的 `fakeStore` 就是：map 就是它的"存储引擎"。接口小，fake 就小；再进一步，用函数字段能让每个用例自己决定行为：

```go
// 测试文件 test_fake.go（片段）：`package main` 加上 `boom` 与上面的类型定义后即可 `go test`
type boom struct{}

func (boom) Error() string { return "db down" }

// fakeStore2 用函数字段：每个用例自己决定行为。
type fakeStore2 struct {
	get func(ctx context.Context, id string) (User, error)
}

func (f fakeStore2) Get(ctx context.Context, id string) (User, error) {
	return f.get(ctx, id)
}

func TestFakeFuncField(t *testing.T) {
	tests := []struct {
		name    string
		get     func(ctx context.Context, id string) (User, error)
		id      string
		want    string
		wantErr error
	}{
		{"存在", func(_ context.Context, _ string) (User, error) {
			return User{Name: "ann"}, nil
		}, "1", "hello, ann", nil},
		{"底层故障", func(_ context.Context, _ string) (User, error) {
			return User{}, boom{}
		}, "1", "", boom{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewService(fakeStore2{get: tc.get})
			got, err := s.Greet(context.Background(), tc.id)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
```

```text
=== RUN   TestFakeFuncField
=== RUN   TestFakeFuncField/存在
=== RUN   TestFakeFuncField/底层故障
--- PASS: TestFakeFuncField (0.00s)
    --- PASS: TestFakeFuncField/存在 (0.00s)
    --- PASS: TestFakeFuncField/底层故障 (0.00s)
PASS
```

〔实测〕在 go1.26.4 上 `go test -race -v -count=1` 输出如上。和 map 版相比，函数字段的 fake 把"行为"也做成了用例数据：第二行的 fake 返回 `boom{}`，错误注入就这样覆盖了"存储层炸了"的分支。接口只有一个方法时，这种写法读起来最直白，不需要任何工具。

Handler 初始化时需要 `UserService` 和 `CodeService`，单测就给它们注入 mock。这样在这一层切断真实依赖，不用一路构造到 DAO，也不用往数据库准备数据；被测对象仍然是实际的 Handler。〔W04-4 p24〕

**mock** 是带期望的替身：它断言某个方法被以什么参数、调用了几次、什么顺序。课程用的是 `uber-go/mock`（官方的 `golang/mock` 已停止维护），用 `mockgen` 从接口生成代码。〔W04-4 p25-27〕使用铁律：先 `EXPECT()`，再写同名方法与参数条件（不想匹配具体值就用 `gomock.Any()`），再 `Return()`；**设计了几个模拟调用，就要全部用上，且次序对得上**，多了、少了、乱了都会失败。〔W04-4 p28〕排错口诀：检查多了没、少了没、漏了没、条件对上了没。

什么时候用哪个：接口只有一两个方法，或只关心返回值：手写 fake，零依赖；接口大，或要验证"调用了几次、顺序如何"：mock，代价是测试与实现细节耦合，重构时容易整片红；第三方客户端没有暴露接口（比如 GORM）：生成不了 mock，课程用 `sqlmock` 编排 `ExpectBegin`、`ExpectExec`、`ExpectCommit`，最后 `ExpectationsWereMet()`，GORM 初始化要关掉版本探测、ping 和默认事务，避免多出 sqlmock 没编排的调用〔W04-4 p66-69〕。注意 sqlmock 只验证 SQL 字符串的收发，不验证 SQL 真的有效；另一派做法是 DAO 层直接用真实数据库（容器或内存库），按团队取舍。

## 五、各层怎么测：金字塔在服务里的落地

单测不碰任何第三方组件：不连 MySQL，不连 Redis，因为单测讲究快速启动、快速发现、快速修复，起了真依赖就快不起来。〔W04-4 p5-6〕课程把一个服务从上到下逐层测了一遍，每层只替换它的直接依赖：

| 层 | 被测对象 | 替身 | 要点 |
|---|---|---|---|
| Handler | HTTP 入口 | mock 或 fake 的 Service | 构造请求、`httptest.NewRecorder`、`ServeHTTP`、断言状态码和响应体〔W04-4 p29-31〕 |
| Service | 业务逻辑 | mock 的 Repository | 每个分支一个用例，如用户不存在与密码错误返回同一个错误〔W04-4 p45-49〕 |
| Repository | 缓存加库 | mock 的 DAO 和 Cache | 缓存未命中要模拟三次调用：`Get` 未命中、DAO 查询、`Set` 回写〔W04-4 p52-55〕 |
| Cache | Redis 封装 | mock 的 `redis.Cmdable` | 对第三方接口用反射模式生成 mock〔W04-4 p61-63〕 |
| DAO | SQL | `sqlmock` 或真库 | 增删改关注 `lastInsertId` 和 `rowsAffected`，冲突用 MySQL 错误码 1062 模拟〔W04-4 p66-71〕 |

这张表自上而下读，就是"被测层换掉下一层"的固定套路，Handler 层值得展开，因为它的工具链最特别：

```go
package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
)

var errNoSuchUser = errors.New("no such user")

// GreetHandler 是 HTTP 入口。greet 是函数字段（最小的"接口"），
// 测试里塞一个假的进去，就能不打数据库地测路由和编码。
type GreetHandler struct {
	greet func(id string) (string, error)
}

func (h GreetHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	msg, err := h.greet(r.PathValue("id"))
	if err != nil {
		http.Error(w, "no such user", http.StatusNotFound)
		return
	}
	fmt.Fprintln(w, msg)
}

func main() {
	h := GreetHandler{greet: func(id string) (string, error) {
		if id == "1" {
			return "hello, ann", nil
		}
		return "", errNoSuchUser
	}}
	mux := http.NewServeMux()
	mux.Handle("GET /greet/{id}", h)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/greet/2", nil))
	fmt.Println(rec.Code, rec.Body.String())

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/greet/1", nil))
	fmt.Println(rec.Code, rec.Body.String())
}
```

```text
404 no such user

200 hello, ann

```

〔实测〕go1.26.4 运行输出如上。关键在 `mux.ServeHTTP(rec, req)` 这一步：它等价于服务器真的从网络收到了一个请求，走完路由、handler、写响应的全程，但没有端口、没有网络栈，所以快且稳定。〔W04-4 p29〕`httptest.NewRequest` 造请求，`httptest.NewRecorder` 造一个"能把响应存下来"的 `ResponseWriter`，事后从 `rec.Code` 和 `rec.Body` 读结果，HTTP 层测试的全部秘密就是这两个东西。〔W04-4 p21-23〕

一个小细节：课程里创建时间在库里存的是毫秒数，纳秒部分在落库时就丢了，Repository 读回来的 `time.Time` 自然没有纳秒。所以断言前要先用 `time.UnixMilli(time.Now().UnixMilli())` 把期望值截断到毫秒再重建，否则含纳秒的期望值永远和读回来的不相等。〔W04-4 p58〕换了存微秒或纳秒精度的列，这条就要按列精度改。〔自〕

## 六、集成测试：用真依赖验证业务流程

单测保证每个组件单独没问题，组合起来有没有问题靠集成测试。两者用例设计的依据不同：单测以**实现逻辑**为依据（看代码找分支），集成测试以**业务流程**为依据（用户会走什么路）。由此推出两条边界：程序员编码造成的系统错误用户触发不了，集成测试几乎测不到；依赖并发的异常流程也难自动化，靠手工。〔W04-5 p3-4〕用例结构升级成六件套：名字、`before`（准备数据）、`after`（验证并清理数据）、输入、预期状态码、预期结果。执行流程：`before`、构造请求、`Recorder`、`ServeHTTP`、比较响应码和 JSON 反序列化后的结构、`after`。比较响应时直接比较结构体（反序列化回 `web.Result` 再比），避免比较 JSON 字符串，字段顺序变了字符串比就误报。〔W04-5 p6-7〕

和单测相比，集成测试多出来的是数据责任。三条铁律：〔W04-5 p12〕

1. 直接操作的第三方（Redis、MySQL）要**验证状态**：`after` 里查数据是否写进去了、TTL 对不对，然后清理。
2. 调用别人的接口，只验证返回响应，管不了人家内部状态。
3. **永远不要让用例互相依赖**：比如"发送太频繁"这个用例完全可以依赖"发送成功"先跑过，但绝对不要这么写。用例间有依赖，重构一动就整片崩，而且单跑一个用例永远跑不起来。

第 1 条展开一个真实形状。测试"发送验证码成功"时，`after` 里做三件事：从 Redis 取出 key 确认写入了、断言 TTL 符合预期、`Del` 清理。〔W04-5 p8〕TTL 怎么断言是自己的取舍：比如写入时设 10 分钟，就断言剩余大于 9 分钟，给执行耗时留余量（这个数字是举例，课程页面没给）。〔自〕第 2 条和第 1 条的区别，用"发短信"最清楚：验证码发没发到用户手机上，自动化无法确认，所以自动化部分用内存短信服务验证流程走通，另由测试人员手动发一次真短信，双层验证。〔W04-5 p5〕集成测试之前还有一层灰色地带：单独验证和某个第三方的复杂交互，比如验证码存储依赖的 Redis Lua 脚本。它不算正规集成测试，但比 mock 真、比全链路小，值得单独跑。〔W04-5 p15〕

### TDD：测试先行的完整走法

前文的流程都是"写完实现再补测试"。TDD 把顺序反过来：先写一个必然失败的测试，再写刚好让测试通过的实现，最后重构。课程用一个完整的发帖模块演示了一遍：**编辑接口用集成测试驱动**：六件套测试先写好（`before` 建数据、断言 200、`after` 验库并清理），跑一遍看它失败，然后实现 handler，再实现 service、repository、dao。链路是被测试"逼"出来的，不是先设计好再贴测试。**发表接口用单元测试驱动**（gomock），核心难点在制作库/线上库双写。〔W06-1 p2〕〔W06-1 p17〕〔W06-1 p20-22〕〔W06-1 p38〕

双写要在哪个层次同步，有三种选法：web 层调两个 service（将来微服务拆分时直接照搬）、service 层调两个 repository（同进程默认选法）、repository 层同时操作两个 DAO。取舍标准一句话：**需要本地事务，就必然同库不同表，下沉到 repository 或 DAO 处理；不需要事务，放哪都行，建议 service**。部分失败加重试兜底。〔W06-1 p47-48〕〔W06-1 p72〕

## 七、三件专用武器

### 并发：`-race`

先说清楚为什么前面所有手段都管不了并发 bug：fake 和 mock 验证的是"无并发时的结果"，逻辑正确性；并发 bug 是时序问题，两次读之间插进了别人的写，任何固定的调用序列都复现不了。mock 测不出并发安全，并发 bug 要靠 `-race` 和 code review。〔W04-6 p31-32〕`-race` 是动态分析：运行时用向量时钟监控每次内存访问，只抓**实际发生**的竞争路径。这有两层含义：没触发的竞争抓不到，所以要多跑几遍（`-count`）、配不同调度；抓到的竞争是真的，不是启发式猜测。06 节看过 `WARNING: DATA RACE` 的输出形态，退出码非 0，CI 直接红。CI 默认开。

### 时间：`testing/synctest`

第二个专用武器解决"测试里的时间"。直接看现象：同一个 `Retry`（失败退避重试，`time.Sleep`/`time.After` 直接用标准库计时，没有注入任何时钟），在真实时间和在 `synctest` 气泡里各跑一次：

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"
)

// Retry 以指数退避重试 fn：次数用尽返回最后一次错误。
// 计时直接用标准库 time，没有注入任何时钟。
func Retry(ctx context.Context, max int, base time.Duration, fn func() error) error {
	wait := base
	for i := 1; i <= max; i++ {
		err := fn()
		if err == nil {
			return nil
		}
		if i == max {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		wait *= 2
	}
	return nil
}

func main() {
	start := time.Now()
	err := Retry(context.Background(), 3, 100*time.Millisecond, func() error {
		return errors.New("fail")
	})
	fmt.Println(err, time.Since(start) >= 300*time.Millisecond)

	synctest.Test(new(testing.T), func(t *testing.T) {
		start := time.Now()
		err := Retry(context.Background(), 3, 100*time.Millisecond, func() error {
			return errors.New("fail")
		})
		fmt.Println(err, time.Since(start))
	})
}
```

```text
fail true
fail 300ms
```

〔实测〕go1.26.4 上两次运行输出如上。第一行是真实时间：至少等了 300 毫秒（`>=` 只能证到这么多，因为真实时钟只能往前走）。第二行在气泡里：`time.Since` 精确返回 300ms，真实耗时接近零。**同一个函数，一次代码都没改。**机制要说透。`synctest.Test` 把里面的代码放进一个隔离的"气泡"，并给气泡一个**虚拟时钟**：runtime 换掉了计时器实现，气泡里所有 goroutine 的 `time.Sleep`、`time.After`、`time.NewTimer` 都由这个假时钟驱动。快进条件只有一条：气泡内**全部** goroutine 都处于 durably blocked（"不需要外部事件、气泡内就能继续"的阻塞）时，假时钟直接跳到最近的未来事件。〔自〕所以第二行里，Retry 卡在 `time.After` 上，这正是 durable 阻塞，时钟跳 100ms、触发、再跳 200ms，账本精确。

反面例子先动手试：把 `Retry` 写错成"最后一次失败后还多等一次退避"，`max=3`、`base=100ms` 时虚拟账本变成 100+200+400=700ms：

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"
)

// 错误写法：最后一次失败后还多等一次退避。
func RetryWrong(ctx context.Context, max int, base time.Duration, fn func() error) error {
	wait := base
	for i := 1; i <= max; i++ {
		err := fn()
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		wait *= 2
	}
	return nil
}

func main() {
	synctest.Test(new(testing.T), func(t *testing.T) {
		start := time.Now()
		err := RetryWrong(context.Background(), 3, 100*time.Millisecond, func() error {
			return errors.New("fail")
		})
		fmt.Println(err, time.Since(start))
	})
}
```

```text
<nil> 700ms
```

〔实测〕go1.26.4 上 `go run .` 输出如上。正确的 `Retry` 在同一个气泡里是精确的 `300ms`（上一个示例），这个错误版本多等了一次 400ms 退避，账本变成 700ms。写成测试时，断言 `time.Since(start) != 300*time.Millisecond` 就会把这类问题直接拦下：`--- FAIL: TestRetryWrongVirtualTime ... elapsed = 700ms`。durably blocked 的清单要背下来：〔自〕

| 算 durably blocked | 不算 |
|---|---|
| bubble 内 channel 的收发 / select | `sync.Mutex` 的 Lock |
| `sync.Cond.Wait` | 真实 I/O（文件、网络） |
| `WaitGroup.Wait`（Add 在 bubble 内） | 系统调用（cgo、fork） |
| `time.Sleep` / 计时器等待 | |

不算的那一列决定了两件事。第一，等锁不能推进时间：一个 goroutine 卡在 `mu.Lock()` 上，其他 goroutine 全部睡完，假时钟也不动，`synctest.Wait` 永不返回，最后 runtime 报 `fatal error: all goroutines are asleep - deadlock!` 退出，这不是 synctest 能 recover 的 panic，是 fatal。第二，气泡根函数返回时若仍有 goroutine 卡在气泡内的 channel 上，会自动 panic 并指认泄漏位置（`main bubble goroutine has exited but blocked goroutines remain`），相当于零成本内置的 goroutine 泄漏检测；代价是泄漏必须阻塞在 bubble 内通道上才测得到，卡真实 I/O 的照旧测不到。〔自〕

### 输入空间：fuzz

前两种武器测的是"给定输入下结果对不对"，fuzz 测的是**性质**：给一批种子，引擎变异出成千上万输入，断言对**任何**输入都成立的规律。fuzz 目标长这样（一个解析 `key=value` 的函数，性质是"解析成功则能还原"）：

```go
import (
	"fmt"
	"testing"
)

// ParseKV 把 "key=value" 切成两半；没有等号就报错。
func ParseKV(s string) (string, string, error) {
	for i := 0; i < len(s); i++ {
		if s[i] == '=' {
			return s[:i], s[i+1:], nil
		}
	}
	return "", "", fmt.Errorf("no = in %q", s)
}

func FuzzParseKV(f *testing.F) {
	f.Add("a=b")
	f.Add("=x")
	f.Add("")
	f.Fuzz(func(t *testing.T, s string) {
		k, v, err := ParseKV(s)
		if err != nil {
			return
		}
		if k+"="+v != s {
			t.Fatalf("roundtrip: %q -> %q %q", s, k, v)
		}
	})
}
```

```text
=== RUN   FuzzParseKV
=== RUN   FuzzParseKV/seed#0
=== RUN   FuzzParseKV/seed#1
=== RUN   FuzzParseKV/seed#2
--- PASS: FuzzParseKV (0.00s)
PASS
```

〔实测〕普通的 `go test -v` 只跑种子语料，输出如上，不开引擎。要开引擎得显式给 `-fuzz` 和 `-fuzztime`：〔实测〕`go test -run '^$' -fuzz FuzzParseKV -fuzztime 3s` 在本机跑了约 125 万次执行无失败（`fuzz: elapsed: 3s, execs: 1255255 (418408/sec)`）。

找到的失败输入会存进 `testdata/fuzz`，成为以后每次普通 `go test` 都重放的回归用例。适合解析器、编解码、协议处理这类"外部可控输入"的代码。

## 八、基准与覆盖率

基准用 `b.Loop()`（Go 1.24 起）取代 `for i := 0; i < b.N; i++`：框架自动处理计时起点，还会阻止编译器把被测调用优化掉（老写法里 `ParseKV` 的结果没人用，内联加死代码消除可能把整个调用删光，量出来的时间不是你以为的时间）。〔自〕

```go
import "testing"

func BenchmarkParseKV(b *testing.B) {
	for b.Loop() {
		ParseKV("key=value")
	}
}
```

```text
goos: darwin
goarch: arm64
cpu: Apple M4
BenchmarkParseKV-10    	525833678	         2.268 ns/op	       0 B/op	       0 allocs/op
PASS
```

〔实测〕`go test -run '^$' -bench ParseKV -benchmem -count=1` 在 Apple M4 的输出如上（`ParseKV` 就是上一节的那个）。绝对数字随机器浮动，看两个东西：量级（这个解析是纳秒级）和 `allocs/op`（0 意味着热路径没有分配，05 节说过错误构造的分配成本，这里因为成功路径不构造错误）。`b.Loop()` 和 `-benchmem` 是基准的最低配置。

覆盖率是诊断工具，不是目标：低覆盖率说明有路径没人管；高覆盖率不说明测试有意义：Go 的覆盖率按语句统计，只说明这些语句被执行过，一个没有任何断言的测试也能把行数盖满。单测 80% 以上、集成测试盖住主流程，是课程给出的落点。〔W04-4 p16〕

## 小结

1. 测试工具链全在 `go test` 一个命令里；质量差距在**被测代码是否可测**，而可测性靠接口和依赖注入，fake 就是"另一种实现"。
2. 表驱动加 `t.Run` 是基本句型：名字、输入、输出、mock 四件套起步，`t.Run` 让子用例可定位、可单跑。
3. 替身选择：一两个方法的接口手写 fake（函数字段把行为做成用例数据）；要验证调用次数顺序用 `uber-go/mock`；GORM 这类没有接口的用 `sqlmock`。
4. 分层测试每层只换直接依赖：Handler 用 `httptest` 记录器，Service 到 DAO 逐层往下；数据库时间列毫秒截断是必踩的坑。
5. 集成测试以业务流程为依据：`before`/`after` 六件套，直接操作的第三方要验证状态并清理，用例之间绝不互相依赖。
6. 三件武器各管一类问题：并发安全靠 `-race`（动态分析，只抓实际发生的竞争）；时间相关用 `testing/synctest`（虚拟时钟，durably blocked 才快进）；外部输入用 fuzz（性质，不是期望值）。

## 常见坑

1. 用例之间共享可变状态或依赖执行顺序：重构时整片红，单跑一个还起不来。
2. 用 `time.Sleep` 等异步结果：慢且不稳；改成 channel 同步、`synctest`，或带超时的轮询。
3. 直接读 `time.Now()`，测试无法控制时间；放进 `synctest` 气泡，时钟立刻可控。
4. mock 的期望次数、顺序与实现细节绑定，重构一动就红；只关心返回值就别用 mock。
5. 只测 happy path；错误分支用函数字段 fake 注入错误来覆盖。
6. 忘了 `-count=1`，看到的是缓存结果，新加的日志一行没出。
7. 集成测试不清理数据，下一次运行互相污染；`after` 里验证加 `Del`。
8. 用单测去"证明"并发安全：mock 和 fake 都做不到，并发 bug 靠 `-race` 加 code review。
9. 气泡里等待锁（`mu.Lock()`）或真实 I/O：不算 durably blocked，时间不动，最后 fatal deadlock；把共享状态交给气泡内的 channel 或 timer。

## 判断题与答案

先自己答，再看后面。

1. 为什么说面向接口的收益是扩展性，却又说它是可测性的前提？
2. 同一个仓库里，什么时候该手写 fake，什么时候该用 mock？
3. 为什么集成测试里 `after` 既要验证数据又要清理？
4. `synctest` 气泡里一个 goroutine 卡在 `mu.Lock()` 上，虚拟时间会前进吗？
5. fuzz 测的"性质"和普通测试里的"期望值"差别在哪？

**答案**

1. 替身（fake/mock）本身就是接口的"另一种实现"；为换供应商留的接口恰好也是测试注入的位置，一次抽象两份回报，所以它虽不为测试而生，却是测试能成立的前提。
2. 接口只有一两个方法、只关心返回值：fake，零依赖零生成代码；接口大、要断言调用次数和顺序：mock，代价是和实现细节耦合。
3. 验证是断言的一部分（业务副作用不只是返回值，如 Redis 的 TTL）；清理是隔离的一部分（数据留着，下一次运行就变成"依赖上一个用例"）。
4. 不前进。等锁不算 durably blocked，快进条件是全部 goroutine durable，卡锁的这一个让全场时间静止，直到 runtime 判定 deadlock。
5. 期望值断言"这个输入得到那个输出"，一个用例一个点；性质断言"任何输入都满足这个规律"，一条性质覆盖整个输入空间，由引擎变异出千万个输入去试。

练习包 `exercises/p21test/` 里可以直接跑：`TestBackoffTable`（退避序列）、`TestRetryVirtualTime`（虚拟时钟）、`TestRetryCtxCancelImmediate`（退避中取消）、`TestDebounceSynctest`（防抖）、`TestGoroutineLeakDetection`（泄漏 panic 指认）、`FuzzParseArgs`（三条性质）；`exercises/p2_5idiom/` 的 `TestTableDriven`、`TestFake`。

## 在项目里

- **小型集成测试**：`dnsproxi-go/main_test.go` 的 `TestD1Forward` 起一个 fake 上游和真实的代理（`startProxy`），用真实 UDP 回环走完"客户端、代理、上游、回包"四跳，断言回包的 ID 与 `QR` 位。上游是假的，网络是真的，正好对应"自己操作的组件验证状态、外部依赖只验响应"的分层。
- **性质测试**：`dnsproxi-go/tcp/fuzz_test.go` 的 `FuzzDecodeFrame` 种子覆盖空输入、半截前缀、前缀大于正文、零长度帧、粘包，断言错误路径不返回缓冲区，且错误只能来自 EOF 族。
- **带并发的测试**：`dnsproxi-go/cache/cache_test.go` 的 `TestConcurrentHitMiss` 配合 `-race`；`dnsproxi-go/tcp/server_test.go` 的 `TestMaxConns` 与 `TestMaxInFlight` 验证并发上限。
- **指标行为测试**：`dnsproxi-go/metrics_test.go` 的 `TestMetricsDeadUpstream` 在死上游场景断言指标计数与直方图行为。

## 想深入：正文之外的干货

### W04-3 wire 与接口抽取的补充细节

- 初始化七步链的完整顺序：DB/Redis、DAO/Cache、Repository、Service、Handler、Gin 注册路由、结束；每一层都是"A 依赖 B 就在初始化时传入构建好的 B"，这条链本身就是标准的手写依赖注入。〔W04-3 p4〕
- 反面教材是非依赖注入的 `NewUserRepositoryV1(dbCfg DBConfig, cCfg CacheConfig)`：函数体内自己完成 `gorm.Open(mysql.Open(dbCfg.DSN))`、`dao.NewUserDAO(db)`、`redis.NewClient(&redis.Options{Addr: c.Addr})`、`cache.NewUserCache(...)` 再组装返回；非依赖注入写法必须自己初始化依赖，即 Repository 需要知道如何初始化 DAO 和 Cache，对照的注入版 `NewUserRepository(d *dao.UserDAO, c *cache.UserCache)` 不关心依赖是如何构造的。课程由此总结六条缺点：深度耦合依赖的初始化过程；往往需要定义额外的 Config 类型来传递依赖所需的配置信息；一旦依赖增加新配置或更改了初始化过程都要跟着修改；缺乏扩展性；测试不友好；难以复用 DB、Redis 之类的公共组件/客户端。〔W04-3 p5-6〕
- 引入 wire 的动机：依赖注入的缺点是要在系统初始化过程（main 函数）里完成复杂冗长的构造链表，所以借助中间件完成这些复杂又没有多少技术含量的事情，课程推荐 wire。wire 分成两部分：项目中使用的依赖和命令行工具，安装命令 `go install github.com/google/wire/cmd/wire@latest`；页面边注强调要确保 `GOPATH/bin` 在环境变量 PATH 里，否则就是 command not found。使用有两个基本步骤：第一步往 wire 里注册各种初始化方法（相当于告诉 wire 我有什么东西、每个东西怎么初始化），`wire.go` 文件头带 `go:build wireinject` 编译标签，`InitUserRepository() *repository.UserRepository` 函数体内 `wire.Build(repository.NewUserRepository, dao.NewUserDAO, InitDB)`，末尾随便 return 一个值（`return &repository.UserRepository{}`）凑齐返回值；第二步运行 `wire` 命令让 wire 完成组装。〔W04-3 p7-8〕
- wire 生成 `wire_gen.go` 靠的是代码生成技术：根据你传入的各种构造方法分析其中的依赖，生成一个真正的初始化方法，展开就是普通 Go 代码 `db := InitDB()` → `userDAO := dao.NewUserDAO(db)` → `userRepository := repository.NewUserRepository(userDAO)` → `return userRepository`。wire 本身并不支持单例模式：假如你多次调用 InitDB，那么就会生成多个 gorm.DB，课程注明后面会解决这个问题（常见落法是在构造函数里用 `sync.Once` 固化单例〔自〕）。〔W04-3 p9〕
- 依赖注入是控制反转（IoC）的一种实现形式，另一种是依赖发现/依赖查找：A 调用 B，A 自己去找一个可用的 B。反例：如果 A 用 B 接口的时候需要自己创建一个，比如 UserHandler 里面自己创建一个 UserService 的实现，那么 UserHandler 就和 UserService 的实现耦合在一起了。控制反转的意思是 UserHandler 不会去初始化 UserService：要么外面传入 UserService 给 UserHandler（依赖注入），要么 UserHandler 自己去找一个 UserService（依赖查找）；无论哪种，A 都不初始化 B，从而避免与 B 的具体实现耦合。"面向接口加依赖注入"的定义：写代码时 A 调用 B，B 必须是一个接口，初始化时注入一个实现了 B 接口的实例，也就是编译时依赖接口、初始化时注入实现。〔W04-3 p10〕〔W04-3 p24〕
- 用 wire 改造整套 Web 服务时，DAO、Repository、Service 和 Handler 都已经有构造方法了，此外还要补三类新方法：一个用于准备接入的 Gin HandlerFunc，一个初始化对应的 sms.Service，一个集成所有 Handler、Gin HandlerFunc 完成路由注册；而后在 main 函数里面直接启动就可以。预期中的 main 什么事情也不干，就是直接启动：`server := InitWebServer(...)`，注册一个 `/hello` 路由，`server.Run(":8080")`。`InitWebServer(funcs []gin.HandlerFunc, userHdl *web.UserHandler) *gin.Engine` 的内部组装顺序：`server := gin.Default()`，`server.Use(funcs...)` 挂上中间件，`userHdl.RegisterRoutes(server)` 注册路由，返回 server。〔W04-3 p13-14〕〔W04-3 p18〕
- 构造函数/初始化方法返回接口类型时（如 `InitRedis` 声明返回 `redis.Cmdable`，实际创建 `*redis.Client`），必须显式告诉 wire 实现是谁，wire 不会自己推断：在 ioc 包里加绑定声明 `wire.Bind(new(redis.Cmdable), new(*redis.Client))`，第一个参数指接口，第二个参数指实现。这个 ioc 包用于封装提供依赖的逻辑：`InitRedis` 提供一个 Redis 的客户端，`InitDB` 提供一个 gorm.DB；基础部分之后按层注册构造函数：`dao.NewUserDAO`，`cache.NewUserCache` 与 `cache.NewCodeCache`，`repository.NewUserRepository` 与 `repository.NewCodeRepository`。〔W04-3 p15〕
- wire 的三条硬缺陷：无法按环境指定不同实现（课程在 `InitSmsService` 里写死 tencent/memory，注释切换）、无法找出某接口的所有实现、无法找出某类型的所有实例（gin middleware 列表只能手写）；另外每次改构造函数都要重新跑 wire 命令，忘了跑就会编译错或用上旧组装。仍用它是因为清晰可控、比手写快，矮子里拔将军。〔W04-3 p17-19〕〔W04-3 p9〕〔W04-3 p20-21〕
- 构造函数返回接口还是具体类型：wire 场景最好返回接口（配合 `wire.Bind` 做类型匹配），Go 社区惯例是"接受接口，返回结构体"，两者冲突；返回接口还牺牲了具体类型新增方法的可见性，只在确有多实现需求时用。〔W04-3 p33〕
- 接口定义在提供方还是消费方：课程把接口写在 service/repository 包内（提供方一侧），Go 社区惯例是消费方定义小接口，两种都有人用。〔W04-3 p29-31〕
- `UserHandler` 不抽接口不是偷懒：接口在被调用方一侧才有意义，只有当有人要 mock 它时才值得抽；没人调用它的方法，注册路由的用途可以推迟。〔W04-3 p32〕
- `UserService` 抽接口的当下理由就是测试：将来才可能按版本或 VIP 分实现，现在先为单测铺路；实现类用小写 `userService` 与接口区分。〔W04-3 p31-32〕

### W04-4 单测细部的坑与口诀

- mock 工具换代要记牢：官方 `golang/mock` 已归档弃维护，现在装的是 `go.uber.org/mock/mockgen`；三参数 `-source`（接口所在文件）、`-destination`（生成路径）、`-package`（生成包名），在项目根目录跑，Makefile 封装成 `make mock`。〔W04-4 p25-27〕
- gomock 新手第一坑是字面量类型不匹配：断言 `int64(12)` 必须显式写 `int64(12)`，裸写 `12` 是 int，参数对不上时报的是"期望的调用没发生"，错误信息和真实原因相距甚远。〔W04-4 p55-57〕
- 每个测试用例独立一个 mock 控制器（在 `t.Run` 里面 `gomock.NewController(t)`）：控制器跨用例复用，EXPECT 会互相串扰。〔W04-4 p31〕
- Handler 单测的用例从哪来：先写正常流程（输入合法、Signup 返回 nil、断言 200），异常用例全部由正常用例复制粘贴改出来：Bind 错 400 空体、邮箱格式不对、两次密码不同、邮箱冲突返回 `ErrUserDuplicateEmail`、系统错误。〔W04-4 p32-36〕
- 两个没法 mock 的角落要心里有数：校验出错时不会调 UserService，mock 直接 `Return(nil, nil)`；正则表达式没抽接口，`MatchString` 的 error 分支测不了，只能测格式不对的情形。〔W04-4 p36-37〕
- Service 层测试的 mock 自由度：`FindByEmail` 可以不用 `gomock.Any()` 而传具体参数（如 `"123@qq.com"`），参数越具体测试越能暴露实现细节的耦合，这是取舍不是规范。〔W04-4 p45-49〕
- DAO 测试不用 sqlite 内存库的理由：真用数据库就得建表、清数据，单测快不起来；sqlmock 靠 SQL 字符串的正则匹配加返回值注入，零代码生成。代价是 SQL 写法一变 `ExpectExec` 的匹配就挂。〔W04-4 p66-67〕
- `SkipDefaultTransaction` 关掉后单语句不再包 BEGIN/COMMIT，sqlmock 的期望序列随之简化；生产配置要保持默认开启事务，两边的期望不能混用。〔W04-4 p69〕

### W04-5 集成测试的补充形状

- 集成测试谁来做：日常由测试团队做，研发可以提前集成自己维护的模块，比如负责 A、B 两个模块就先测 A+B 的组合。〔W04-5 p3〕
- 两个专用用例的形状：发送太频繁用例在 `before` 里预置一个 TTL 9 分钟以上的验证码，`after` 断言值还是原来的 123456（没产生新码）再清理；未知错误用例在 `before` 里预置一个没设过期时间的 key 模拟误操作，`after` 断言数据原样保留。〔W04-5 p10〕〔W04-5 p11〕
- 测试 key 与 cache 实现强耦合（手写 `"phone_code:Login:..."` 这种字符串）课程承认是"测试，没办法"；理想做法是导出 `C.key()` 复用，避免 key 格式漂移后测试集体失效。〔W04-5 p10〕
- Lua 脚本的集成用例与 HTTP 用例同构：同样 before/after/输入/wantErr 的六件套，只是直接调 `CodeCache` 方法而不走 HTTP，断言 TTL 大于 9 分钟。〔W04-5 p16-19〕

### W04-6 第三方调用治理：装饰器、failover 与并发测法

- 治理动机一句话：一切不在你控制范围内的调用都要容错。腾讯短信 API 阈值 3000/s，超了直接被拒，所以客户端在本地限流，不等服务商返回限流响应。〔W04-6 p3-5〕
- 限流下沉成通用抽象：沿用 Web 限流的 Redis 加 Lua 滑动窗口方案，抽成 `Limiter` 接口 `Limit(ctx, key) (bool, error)`，比 Gin 插件版少一个 prefix 字段，任何第三方调用都能套。〔W04-6 p6-7〕
- 反面教材是直接改 `Send` 加限流：功能没错，但别的服务商也要限流、类似功能都得改，越改越难看；正解是装饰器，不改原实现，同接口包一层，可层层叠加，装饰器只做一件事。〔W04-6 p9-12〕
- 限流装饰器触发限流返回的 `errLimited` 保持非导出：它是内部治理信号，一旦导出就成了对外契约，上游会拿它做 `errors.Is` 判断，以后想改就难。〔W04-6 p12〕
- 装饰器的单测是"接口小就全 mock"的标准样例：Limiter 和 Service 都是接口可 mock，输入对结果无影响随便写，三个用例：正常放行断言 `Send` 被调、触发限流断言没调、限流器本身报错。〔W04-6 p13-14〕
- 侵入式修改降低可读性、可测试性、可扩展性且强耦合，"侵入式=垃圾代码"基本成立；开闭原则、非侵入式、装饰器说的是同一件事。〔W04-6 p16〕
- 怎么发现服务商快挂了：频繁超时、EOF/UnexpectedEOF、响应时间变长是间接信号；关系紧密可以问特定错误码，多数时候只能猜。〔W04-6 p20〕
- failover V1 顺序轮询的缺点是负载不均（多数请求在 `svcs[0]` 就成功）和服务商多时慢；V2 用 `atomic.AddUint64(&idx, 1)` 做起始下标轮转，`uint64` 不用担心溢出。〔W04-6 p22〕〔W04-6 p25-26〕
- `DeadlineExceeded` 和 `Canceled` 不重试直接返回：一个是调用方的时限到了，一个是调用方主动放弃，都与用户体验直接相关，重试只会浪费配额。〔W04-6 p25-26〕
- 健康判据有两类可选：错误率（连续 N 个超时、错误率超 10%）和响应时间增长率（100ms 突变 1s）；课程实现选的是连续超时计数。〔W04-6 p28-30〕
- 健康判定的 CAS 细节：CAS 失败说明别人已经切换过，无需检测返回值；成功要重置计数，超时才加一，其他错误保持不动；并发下这是近似的，可能切换得更频繁，但换来无锁。〔W04-6 p30〕
- atomic 四件套的语义一句话版：Load 原子读（读不到改一半的数据）、Store 原子写（跨核立即可见）、Add 原子自增返回新值、CAS 比较并交换；配套原则是无并发控制的任何操作都不是并发安全的。〔W04-6 p27〕
- 组合版装饰器（struct 内嵌 Service 匿名字段）能省掉转发代码、只实现部分方法，代价是调用方能绕开装饰器直接访问内嵌实现；要拦截全部调用就用非组合版。〔W04-6 p15〕
- 并发代码的单测形态：mock 只能验证无并发时结果符合预期，真实场景模拟靠在单测里直接给私有字段（如 `svc.idx`、`svc.cnt`）赋值，然后断言 `wantIdx`、`wantCnt`。〔W04-6 p31-32〕

### W06-1 发帖模块的其余细节

- 需求拆解先分角色：创作者对帖子是增删改查，读者只有一个查；发表之前读者一律不可见，可见性是这个模块要回答的第一个问题。〔W06-1 p4-5〕
- 帖子状态机按库分两套：制作库走新建（未发表）、已发表、被删除，另有隐藏与重新发表；线上库只有已发表和被删除，草稿态对读者永远不存在。〔W06-1 p6-9〕
- “一边修改一边可看”不靠锁：修改期间读者看到的仍是上一次发表的版本，靠制作库与线上库分开存储实现。〔W06-1 p7〕
- 删除做成两个功能：撤销发表（对外不可见、仅自己可见）和真删除，对应“争议太大先隐藏”和“彻底删掉”两种诉求，两个都提供。〔W06-1 p8〕
- 发表只有三种情况：全新直接发表是制作库 INSERT 加线上库 INSERT；编辑后首次发表是 UPDATE 加 INSERT；改已发表的再发表是 UPDATE 加 UPDATE。〔W06-1 p12-14〕
- TDD 核心循环三步：初步定义接口（必然不合适，别怕）、按接口把测试框架搭出来、进入“加用例、改实现、跑测试”的循环；课程先演示集成测试出发的 TDD，再演示单元测试出发的。〔W06-1 p18〕
- 新建帖子的 `after` 要逐字段断言入库数据：Title、Content、AuthorId，加 Ctime 与 Utime 大于 0；TDD 下一切都在迭代，不必担心表结构或接口变更。〔W06-1 p23〕
- 帖子领域没有“用户”概念，只有创作者和读者，`domain.Article` 内嵌 `Author{Id}`；用 DDD 的话说，一个人在用户域是实体，在帖子域只是值对象。〔W06-1 p24-25〕
- 编辑接口新建与修改共用 `/edit`：Id 大于 0 走更新，否则新建；`Save` 因此返回 `(int64, error)`，靠返回的 ID 分流，签名是被用例逼出来的，不是过度设计。〔W06-1 p28-29〕
- 防篡改的用例先于实现写出来：文章 ID 从前端传入就是攻击面，“更新别人的帖子”这个用例是 TDD 价值最大处。〔W06-1 p30〕
- 防篡改两个方案：先查后判多一次查询；直接 UPDATE 带 `WHERE id=? AND author_id=?` 省一次查询，代价是分不清 ID 不存在还是作者不对，但这两种都不是普通用户场景，不值得细查，课程选直接更新。〔W06-1 p31-33〕
- 带 author_id 条件后 `RowsAffected == 0` 有歧义：ID 不存在与非本人更新返回同一个“更新数据失败”，不给攻击者探测信息，普通用户也无感；测试预期是 200 加 Code 5，而不是 403。〔W06-1 p33〕〔W06-1 p34〕
- 发表接口单测的结构体四件套：mock 字段（用 gomock.Controller 换出 service.ArticleService）、reqBody、wantCode、wantRes；gin 登录态用中间件 `ctx.Set("user", UserClaims{Id: 789})` 模拟。〔W06-1 p38-40〕
- 为“将来可能加的逻辑”提前写防御性用例、固化现有行为，大多数是杞人忧天，但成本几乎为零。〔W06-1 p42〕
- service 的 `Publish` 实现：Id==0 走 `authorRepo.Create`，否则 `Update`；随后 `art.Id=id` 再 `readerRepo.Save`，保持同一篇文章在两库 ID 一致；部分失败打 ERROR 日志。〔W06-1 p50-52〕
- 双写测试用例不必穷举：制作库失败、线上库失败再叠新建与修改共四种，追求分支覆盖时让其中一个用例用修改语义即可。〔W06-1 p51〕
- 部分失败影响可控：创作者收到失败响应可以自己重试，读者要么看不到、要么看到旧版；实践是先上线观察失败频率，再决定要不要加重试。〔W06-1 p54〕
- 重试的形状：for 循环最多 3 次调用 `readerRepo.Save`，成功就 break；耗尽后 ERROR 日志加 `return 0, err`。〔W06-1 p54〕
- 重试不着急加的另一半理由：幂等没有保证时，重试有放大写压力与重复写的风险，宁可先观察再上。〔W06-1 p54〕
- 线上库的 ReaderDAO 提供 Upsert：`clause.OnConflict` 指定 Columns id 加 DoUpdates，实现 INSERT OR UPDATE；制作库不能用 Upsert，原因在下面一条。〔W06-1 p56〕
- 制作库不能 Upsert 的根因：防篡改要求 SQL 形如 `ON DUPLICATE KEY UPDATE WHERE author_id=?`，MySQL 不支持把权限条件塞进 upsert，只能 `UpdateById` 带上 author_id 条件。〔W06-1 p73〕
- repository 层同步有两版：SyncV1 非事务，适合两个库真分开；SyncV2 事务版用 `repo.db.WithContext(ctx).Begin()` 开事务，拿 tx 新建两个 DAO，`defer tx.Rollback()`，Commit 之后再 Rollback 只会拿到一个无害的 error。〔W06-1 p60-62〕
- SyncV2 的代价：repository 持有 `*gorm.DB`，等于强依赖了 DAO 的依赖，违背面向接口；哪天换 ORM，repository 跟着受牵连。〔W06-1 p63〕
- 把同步下沉到 DAO 层就没有这个耦合：`db.Transaction` 闭包里 txDAO 先 `Create`/`UpdateById` 再 Upsert `PublishedArticle`，事务生命周期交给 GORM，这是单体时代的设计。〔W06-1 p66-67〕
- 同库不同表要两个模型：`Article` 对应创作者的表，`PublishedArticle` 对应读者的表，先组合 `Article` 建模；业务分化后字段增减，组合就不够了，各自独立定义。〔W06-1 p66-67〕
- 测试用例先于实现定义，但实现定型后 mock 要跟着调：新建发表成功的用例最后改成断言 readerRepo 收到制作库返回的 ID。〔W06-1 p49〕
- 面试口径：TDD 一句话是测试先行；TDD 与 DDD 无关，DDD 管战略规划，TDD 落地具体功能；好处是理清思路、查漏补缺、方便重构、测试完善。〔W06-1 p78〕

省略未吸收：W04-6 的 tpl 安全治理与 JWT token 设计（K17/K18，讲的是模板资源的审批、配额与“认 token 不认人”，是第三方调用的安全治理而非测试话题）；W04-3 的 wire 四类改造方法（K6）与 CodeCache 反例（K11），都是纯 wire 操作细节；W06-1 只省略代码清单与练习题设计（E1 到 E5、X1 到 X4），知识点条目已全部融入正文与本块。W04-4 p2 是单元概览页，内容由正文与本块覆盖。其余必读条目已融入正文或本块。

## 想深入去哪查

- 原材料：W04-3（面向接口与依赖注入，含 wire 的取舍）、W04-4（单测全套：Handler、Service、Repository、Cache、DAO，含 mock 与 sqlmock 的具体用法）、W04-5（集成测试的用例结构、验证码流程、铁律）、W04-6（第三方服务调用治理：限流装饰器、failover 的测试方式，稳定性一节会再用到）。
- 旧长版：`notes/p21-testing-concurrency.md`（synctest 的 durably blocked 清单、气泡三种死法、fuzz 与 race 的开销数据）、`notes/p2_5-idiom.md`（表驱动与 fake 的最小例子）、`notes/p8-fast-track.md`（测试部分）。
- 不在这一节：`mockgen` 反射模式的生成细节〔W04-4 p61-63〕、`sqlmock` 的完整编排写法〔W04-4 p66-71〕，写 DAO 测试时再查。
