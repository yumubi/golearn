# 10 分层、依赖注入与配置：服务代码怎么长成可替换的零件

> 详细程度：深入 · 约 60 分钟
> 材料：W04-3 W05-3 W04-6 W02-2 W04-1、C:go_frame/dependency_injection、旧长版 `notes/p5-deepsearch.md`
> 前置：04 接口与组合

## 先看一个奇怪的问题

下面这个 `UserService.Login` 的函数体里，没有任何一行连数据库、查缓存的代码，只有一句 `s.repo.FindByPhone`。可是它既能登录 MySQL 里的用户，也能登录内存里的用户，还能在测试里登录一个假仓库。先自己想想它到底是怎么知道"该问谁"的。

```go
package main

import "fmt"

type UserRepo interface {
	FindByPhone(phone string) (id int64, err error)
}

type MySQLRepo struct{}

func (MySQLRepo) FindByPhone(phone string) (int64, error) { return 42, nil }

type MemRepo struct{}

func (MemRepo) FindByPhone(phone string) (int64, error) {
	return 0, fmt.Errorf("memory repo: user %s not found", phone)
}

type UserService struct{ repo UserRepo }

func (s UserService) Login(phone string) (int64, error) {
	id, err := s.repo.FindByPhone(phone)
	if err != nil {
		return 0, fmt.Errorf("login: %w", err)
	}
	return id, nil
}

func main() {
	mysql := UserService{repo: MySQLRepo{}}
	mem := UserService{repo: MemRepo{}}

	id, err := mysql.Login("138")
	fmt.Println("mysql:", id, err)
	id, err = mem.Login("138")
	fmt.Println("mem:", id, err)
}
```

```text
mysql: 42 <nil>
mem: 0 login: memory repo: user 138 not found
```

同一个 `Login` 方法，一行没改，注入 `MySQLRepo` 就返回 42，注入 `MemRepo` 就返回"查不到"。答案只有两个字：**注入**。`Login` 不关心 `repo` 到底是谁，它只声明"我需要一个能按手机号查用户的东西"；具体是谁，由调用方在构造 `UserService` 的时候决定。

把"依赖"从"函数里自己 new"改成"从外面传进来"，就是 Go 社区做依赖注入的全部手法。没有运行期容器，没有注解扫描，依赖就是构造函数参数，装配就写在 `main` 里。这一节从这里往下讲五件事：分层、面向接口、依赖注入、装饰器、配置。合起来回答一个问题：一个后端服务怎么长成可替换的零件。

## 和你已知语言的差异

| 问题 | Java / Spring | Rust | TS / JS | Go |
|---|---|---|---|---|
| 依赖怎么进来 | `@Autowired`，运行期反射容器 | 手写构造 + 泛型或 `dyn Trait` | NestJS / InversifyJS 容器 | 构造函数参数；可选 wire 在编译期生成装配代码 |
| 谁来定义接口 | 常在提供方 | 常在提供方 | 使用方或提供方 | 惯例在使用方，见 04 节 |
| 配置来源合并 | `application.yml` + profile + `@ConfigurationProperties` | config-rs 多源合并 | dotenv + `process.env` | viper 多源合并，`UnmarshalKey` 整体绑定到结构体 |
| 横切逻辑（限流、重试） | AOP、`@Retryable` | tower `Layer` | NestJS 装饰器 / 中间件 | 装饰器：同接口包一层 |

表里最反直觉的是第一行：Java 程序员习惯了 `@Autowired` 这种"看不见的装配"，Go 里装配是**看得见的**，就在 `main` 函数里几十行，谁依赖谁一眼能读出来。wire 之类的工具只是帮你少写样板，不改变"显式装配"这个结构。

## 一、分层：每一层只管一件事

为什么不能把增删改查直接写在 Handler 里？回到 04 节的接口原则：Handler 只负责和 HTTP 有关的东西，数据库属于另一种职责。〔W02-2 p14〕把两者混在同一个函数里，改表结构就要改 HTTP 代码，换数据库也要改 HTTP 代码，两件事互相咬死。所以要引入一个"代表数据库抽象的东西"。

课程里的 webook 后端是 service、repository、dao 三层，外加一个代表业务对象的 domain，最外面再接 web 层，合起来可以看成四层。〔W02-2 p15〕

- **web**：只管 HTTP，接收请求、校验参数、返回响应。
- **service**：领域服务，代表一个业务的完整处理过程。
- **repository**：存储抽象，代表"数据存在哪"这件事本身。
- **dao**：数据库操作，具体和某一种数据库打交道。

其中 repository 和 dao 最容易混。区分的关键在 repository 的定位：它是**整体存储抽象**，下面可以是 MySQL、ElasticSearch 或 MongoDB；dao 只代表"数据库"这一种实现。〔W02-2 p16〕所以一个 repository 可能组合一个 dao，也可能组合一个搜索引擎客户端，dao 只是其中一种可能的底座。

调用链是固定的：web 调 service，service 调 repository，repository 调 dao。〔W02-2 p17〕课程没有明说禁止跨层，但分层的本意就是每一层只依赖紧挨着它的下一层：web 一旦直接碰 dao，换存储时 HTTP 代码又要跟着改，前面的分层就白做了。

下面是一个能跑的最小骨架。注意两个细节，一个在类型上，一个在缓存的位置上：

- **领域对象和数据库行分开定义**。`domain.User` 是业务概念，`dao.UserRow` 直接映射表行，两者在 repository 里互相转换。表结构变了，只会波及 dao 和 repository 的转换代码，不会一路烧到 service。〔W02-2 p20〕
- **缓存放在 repository 层**。缓存属于"如何存数据"的范畴，service 对它无感知，构造函数同时收 dao 和 cache 两个依赖。〔W04-1 p16〕

```go
package main

import "fmt"

// domain：业务概念，只有业务关心的字段
type User struct {
	ID    int64
	Phone string
}

// dao：直接映射表行，带存储细节
type UserRow struct {
	ID           int64
	Phone        string
	PasswordHash string
}

type UserDAO struct {
	rows map[string]UserRow
}

func NewUserDAO() *UserDAO {
	return &UserDAO{rows: map[string]UserRow{
		"138": {ID: 7, Phone: "138", PasswordHash: "sha256:abc"},
	}}
}

func (d *UserDAO) FindByPhone(phone string) (UserRow, error) {
	row, ok := d.rows[phone]
	if !ok {
		return UserRow{}, fmt.Errorf("dao: not found")
	}
	return row, nil
}

type UserCache struct {
	m map[string]User
}

func NewUserCache() *UserCache { return &UserCache{m: map[string]User{}} }

// repository：整体存储抽象，缓存属于这里，负责 dao.UserRow -> domain.User
type UserRepository struct {
	dao   *UserDAO
	cache *UserCache
}

func NewUserRepository(dao *UserDAO, cache *UserCache) *UserRepository {
	return &UserRepository{dao: dao, cache: cache}
}

func (r *UserRepository) FindByPhone(phone string) (User, error) {
	if u, ok := r.cache.m[phone]; ok {
		fmt.Println("repository: cache hit")
		return u, nil
	}
	fmt.Println("repository: cache miss, read dao")
	row, err := r.dao.FindByPhone(phone)
	if err != nil {
		return User{}, err
	}
	u := User{ID: row.ID, Phone: row.Phone} // 丢掉 PasswordHash
	r.cache.m[phone] = u
	return u, nil
}

// service：业务过程，对缓存、表结构无感知
type UserService struct{ repo *UserRepository }

func NewUserService(repo *UserRepository) *UserService { return &UserService{repo: repo} }

func (s *UserService) Login(phone string) (int64, error) {
	u, err := s.repo.FindByPhone(phone)
	if err != nil {
		return 0, fmt.Errorf("login: %w", err)
	}
	return u.ID, nil
}

// web：HTTP 层，这里用一个普通函数代替 handler
type Handler struct{ svc *UserService }

func NewHandler(svc *UserService) *Handler { return &Handler{svc: svc} }

func (h *Handler) HandleLogin(phone string) {
	id, err := h.svc.Login(phone)
	fmt.Println("login result:", id, err)
}

func main() {
	// 自底向上装配
	dao := NewUserDAO()
	cache := NewUserCache()
	repo := NewUserRepository(dao, cache)
	svc := NewUserService(repo)
	h := NewHandler(svc)

	h.HandleLogin("138") // 第一次：查 dao，回写缓存
	h.HandleLogin("138") // 第二次：命中缓存，不再碰 dao
}
```

```text
repository: cache miss, read dao
login result: 7 <nil>
repository: cache hit
login result: 7 <nil>
```

〔实测〕`go run .` 跑出上面的输出：第一次 `cache miss` 走 dao，第二次 `cache hit` 不再碰 dao，而 `Login` 和 `HandleLogin` 两层的代码对此毫无感知。注意 `dao.UserRow` 里的 `PasswordHash` 在转换时被丢掉了，`domain.User` 根本不带这个字段，这就是"表结构不影响业务"的具体含义。

装配永远是**自底向上**：先 new 最底层（连接、缓存），再 new repository，再 service，最后 handler 和路由。这就是课程说的"初始化七步链"。〔W04-3 p4〕上面 `main` 里的五行就是它的迷你版。这个顺序是硬约束：上面的层需要下面的实例，所以下面必须先生成。

## 二、面向接口：收益只有扩展性

分层解决"职责放哪"，接口解决"怎么替换"。课程给面向接口的定义很朴素：组件与组件之间的通信必须通过接口；再配合依赖注入，就是"编译时依赖接口，初始化时注入实现"。〔W04-3 p24〕

先看反面。直接依赖具体类型会发生什么：

```go fail
package main

import (
	"bytes"
	"os"
)

func Save(f *os.File, data []byte) error {
	_, err := f.Write(data)
	return err
}

func main() {
	var buf bytes.Buffer
	_ = Save(&buf, []byte("x"))
}
```

```text
cannot use &buf (value of type *bytes.Buffer) as *os.File value in argument to Save
```

`bytes.Buffer` 明明有 `Write` 方法，却传不进 `Save`，因为签名把参数锁死成 `*os.File`。这就是"依赖具体类型"的代价：只能写文件，不能写内存、不能写网络，测试也起不了真实文件。签名改成 `io.Writer`（04 节的写法）这些问题一次消失。

但接口不是免费的。课程把收益和代价都讲透了：**面向接口不提高性能，也不提高可靠性，收益只有扩展性**。〔W04-3 p27〕也就是说，它纯粹是为了将来能换实现。既然如此，就不能处处抽接口，要看准了再抽。课程给了四条判断：

1. **自底向上抽**。缓存和 DAO 位于最底部、不依赖别的类型，先抽它们。〔W04-3 p28-30〕
2. **看变化点**。非主流第三方（短信、对象存储）要考虑替换；MySQL、Redis 这种主流的可以忽略。〔W04-3 p36〕
3. **handler 不抽接口**。没有人调用它的方法，也看不到需要不同实现的地方，就先不留；注册路由虽然也算用了它，可以推迟到将来再抽。〔W04-3 p32〕
4. **超前设计最小化**。只留接口，不写多余实现；抽象时只考虑有限的几种情况，别想太长远。〔W04-3 p35〕

一个和 Go 惯例冲突的地方要专门记住：wire 场景里构造函数最好返回接口，这样 wire 可以直接按类型匹配，而 Go 社区的惯例是"接受接口，返回结构体"。课程原话是：初始化方法（形如 `NewUserRepo` 这样的构造函数）最好返回接口，Go 的推荐做法是返回具体类型，这和 wire 是冲突的。返回具体类型时，就得像下面课程仓库的片段那样再用 `wire.Bind` 补一条接口到实现的映射；不用 wire 就回到惯例。〔W04-3 p33〕

## 三、依赖注入：手写够用，wire 是省样板

依赖注入是控制反转（IoC）的一种实现，另一种是依赖查找。它们的共同点是：A 不自己初始化 B。区别在于 B 怎么到 A 手里：外面把实现传进来，叫注入；A 自己去找可用的 B，叫查找。〔W04-3 p10〕Go 里主流是前者，落地就是构造函数参数。

不注入的写法问题很大。要 `gorm.Open` 一个库、`redis.NewClient` 一个连接，Repository 就得知道 DAO 和 Cache 怎么初始化，于是：初始化过程和依赖深度耦合、要额外定义 Config 类型传配置、依赖变更跟着改、测试不友好、公共客户端难复用。〔W04-3 p5-6〕

### 手写装配

`main` 就是装配车间，一个服务通常几十行。资源关闭也要成对考虑：每个创建连接的函数同时返回一个 cleanup 闭包，`main` 里按相反顺序执行。〔C:go_frame/dependency_injection/main.go:22-32〕先创建的后关闭，保证没有连接被漏掉。

### wire：编译期生成装配代码

wire 的模型只有三个词：**Provider**（构造函数的集合）、**Bind**（接口到实现的映射）、**Injector**（要生成的入口函数）。你写一个带 `wireinject` 构建标签的文件，里面是 `wire.Build(...)`，运行 `wire` 命令后生成真正的 `wire_gen.go`。〔W04-3 p8-9〕Provider、Bind、Injector 这三个词是 wire 自己的术语，课程代码里的注释也用它们。下面是课程仓库里的真实片段：

〔C:go_frame/dependency_injection/wire/provider_injector.go:20-39〕

```go
DbSet = wire.NewSet(gd.GetMysqlDB, gd.NewGormNews, wire.Bind(new(database.INewsDB), new(*gd.GormNews)))
...
func InitHandler() (h *handler.GinHandler, cp func(), err error) {
...
wire.Build(service.SvrSet, handler.HdlSet, database.DbSet)
```

读这段要抓住三点：

- `wire.Bind(new(接口), new(*实现))` 补上"接口由谁实现"的信息，wire 不会自己推断。
- 返回值里的 `func()` 被识别为 cleanup，所有依赖的 cleanup 会**按顺序合并**进 injector 的第二个返回值。〔C:go_frame/dependency_injection/wire/provider_injector.go:36-39〕
- 生成物是普通 Go 代码，运行期零反射。代价是改了构造函数就要重新跑 `wire`，忘了就会编译失败或用旧装配。

wire 的缺陷课程也点名了：**不能按环境选不同实现**（要自己在函数体里写条件）、**找不到某接口的所有实现**、**找不到某类型的所有实例**（gin 中间件只能手写列表）。结论是"矮子里拔将军"：好处是清晰、可控、比手写装配快。〔W04-3 p17-19〕〔W04-3 p20-21〕wire 不支持单例，重复调用同一个初始化函数会得到多个实例，需要单例就在构造函数里自己用 `sync.Once`。〔W04-3 p9〕〔C:go_frame/dependency_injection/database/gorm/connection.go:14-31〕

另一条路是运行时容器：`notes/p5-deepsearch.md` 里的 `di` 包用 `reflect.TypeFor[T]()` 当 map key、`Invoke` 时惰性构造，`exercises/p5deep` 的 `TestProvideInvoke` 验证了它的单例语义。运行时容器灵活但出错晚（缺依赖在第一次 `Invoke` 才报），wire 严格但要多一步生成。两条路的取舍在"想深入去哪查"里可以继续追。

## 四、装饰器：横切逻辑不要侵入业务

短信服务要加限流。第一反应是直接改 `Send` 方法，课程明确反对：别的服务商也要限流、类似功能都得改，越改越难看；功能没错，可扩展性和非侵入性全错。〔W04-6 p9〕

正解是装饰器：不改原实现，在外面包一层同接口的对象，可以层层叠加，被装饰者作为依赖注入，装饰器只做一件事。〔W04-6 p10-12〕下面把限流和 failover 两个装饰器叠在一起：

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
)

type Sender interface {
	Send(ctx context.Context, to, msg string) error
}

type provider struct {
	name string
	err  error
}

func (p provider) Send(ctx context.Context, to, msg string) error {
	if p.err != nil {
		return p.err
	}
	fmt.Printf("%s -> %s: %s\n", p.name, to, msg)
	return nil
}

type Limiter interface {
	Limit(key string) bool
}

type fixedLimiter struct{ on bool }

func (l fixedLimiter) Limit(key string) bool { return l.on }

var errLimited = errors.New("limited")

type RateLimitSender struct {
	next    Sender
	limiter Limiter
}

func (r RateLimitSender) Send(ctx context.Context, to, msg string) error {
	if r.limiter.Limit("sms") {
		return errLimited
	}
	return r.next.Send(ctx, to, msg)
}

type Failover struct {
	svcs []Sender
	idx  atomic.Uint64
}

func (f *Failover) Send(ctx context.Context, to, msg string) error {
	start := f.idx.Add(1) - 1
	n := uint64(len(f.svcs))
	for i := uint64(0); i < n; i++ {
		err := f.svcs[(start+i)%n].Send(ctx, to, msg)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
			return err
		}
	}
	return errors.New("all providers failed")
}

func main() {
	a := provider{name: "A", err: errors.New("A down")}
	b := provider{name: "B"}

	send := func(s Sender, to, msg string) {
		fmt.Println(s.Send(context.Background(), to, msg))
	}

	// 场景1：A 挂了，failover 自动换 B
	send(&Failover{svcs: []Sender{a, b}}, "138", "code 111")

	// 场景2：超时直接返回，不再重试 B
	t := provider{name: "T", err: context.DeadlineExceeded}
	send(&Failover{svcs: []Sender{t, b}}, "138", "code 222")

	// 场景3：限流装饰器挡在前面，任何服务商都不碰
	send(RateLimitSender{next: &Failover{svcs: []Sender{a, b}}, limiter: fixedLimiter{on: true}}, "138", "code 333")
}
```

```text
B -> 138: code 111
<nil>
context deadline exceeded
limited
```

〔实测〕`go run .` 跑出上面的输出。场景 1：A 返回错误，failover 自动换到 B 成功。场景 2：第一家返回 `DeadlineExceeded`，failover **直接返回，不再重试** B。场景 3：限流装饰器挡在最前面，返回 `limited`，任何服务商都没被调用。组合起来就是 `RateLimitSender{next: &Failover{...}}`：先限流，再 failover，业务代码一行不改。

读这个例子有四个细节要抠：

1. 触发限流返回的 `errLimited` **保持非导出**。课程的原话是不到逼不得已不要做成公开的错误。〔W04-6 p12〕原因可以这样理解：错误一旦导出，调用方就会写 `errors.Is(err, ErrLimited)` 依赖它，它变成了对外契约，以后想改就难。
2. 装饰器里内嵌被装饰接口可以只实现部分方法，但调用方能直接访问内嵌字段绕开装饰器；非内嵌版必须实现全部方法，但绕不开。要拦截全部调用就选后者。〔W04-6 p15〕
3. failover 的起始下标用原子计数轮询（`idx.Add(1) - 1`），避免所有请求都砸在第一个服务商；这不是严格轮询，只有起始点在轮转，严格轮询要控制并发，代码更复杂。失败换下一个，全部失败才报错。其中一条重要纪律是**超时和取消直接返回，不再重试**：课程的理由是这两种错误都和用户体验密切相关，超时是调用方设的时限到了，取消是调用方主动放弃。〔W04-6 p25-26〕
4. 健康判定（连续 N 次超时就切换服务商）用原子 CAS 实现，并发下是近似的，只是可能切换得更频繁，换来无锁。〔W04-6 p28-30〕另一点是测试：并发安全没法靠 mock 验证，只能验证没有并发时结果符合预期，其余靠 code review。〔W04-6 p31〕

超时、重试、限流这类治理逻辑的位置就是装饰器这一层，或者 gRPC 的拦截器、HTTP 的中间件。它们都是同一个模式（见 04 节）。

## 五、配置：来源、优先级、整体绑定

配置有四类来源：启动参数、环境变量、配置文件、远程配置中心。〔W05-3 p4〕当多个来源同时给出同一个配置项，用哪个？这取决于你认为哪个来源更"权威"。课程给的是个人实践：命令行最权威，然后是环境变量、配置文件、远程中心。〔W05-3 p5-6〕课程也说不同公司、不同人偏好不同，没有行业标准，团队内一致即可。viper 自己的顺序是显式 Set、命令行参数、环境变量、配置文件、远程 key/value 存储、默认值，和课程的实践一致。

几条实用的纪律：

1. **整体绑定，不要单字段读**。不推荐到处 `viper.GetString("redis.addr")`，推荐定义内部结构体，用 `UnmarshalKey` 一次性绑定。〔W05-3 p11-12〕
2. **默认值留在业务代码里**：先填好结构体默认值再反序列化，配置缺失就保留默认。〔W05-3 p13〕
3. **配置只在初始化读**。配置操作限定在 IoC 和 `main`，换配置框架只改初始化；service 层监听配置变更会违背这条。〔W05-3 p28〕
4. **用统一 API 屏蔽来源差异**。借 viper 的 API 屏蔽文件、etcd、Nacos 的差别，不直接操作 etcd。〔W05-3 p27〕

用标准库演示"默认值 → 文件 → 环境变量 → 命令行"的覆盖链：

```go
package main

import (
	"encoding/json"
	"flag"
	"fmt"
)

type Config struct {
	Addr string
	DSN  string
}

func Load(args []string, getenv func(string) string, file []byte) (Config, error) {
	// 默认值写在业务代码里
	cfg := Config{Addr: ":8080", DSN: "root:root@tcp(localhost:3306)/webook"}
	if len(file) > 0 {
		if err := json.Unmarshal(file, &cfg); err != nil {
			return cfg, err
		}
	}
	if v := getenv("APP_DB_DSN"); v != "" {
		cfg.DSN = v
	}
	fs := flag.NewFlagSet("app", flag.ContinueOnError)
	addr := fs.String("addr", "", "listen addr")
	if err := fs.Parse(args); err != nil {
		return cfg, err
	}
	if *addr != "" {
		cfg.Addr = *addr
	}
	return cfg, nil
}

func getenv(val string) func(string) string {
	return func(k string) string {
		if k == "APP_DB_DSN" {
			return val
		}
		return ""
	}
}

func main() {
	file := []byte(`{"dsn":"file-dsn"}`)

	cfg, _ := Load([]string{"--addr=:9000"}, getenv("env-dsn"), file)
	fmt.Printf("%+v\n", cfg) // 命令行 > 环境变量 > 文件 > 默认

	cfg, _ = Load(nil, getenv(""), file)
	fmt.Printf("%+v\n", cfg) // 只有文件覆盖

	cfg, _ = Load(nil, getenv(""), nil)
	fmt.Printf("%+v\n", cfg) // 纯默认
}
```

```text
{Addr::9000 DSN:env-dsn}
{Addr::8080 DSN:file-dsn}
{Addr::8080 DSN:root:root@tcp(localhost:3306)/webook}
```

〔实测〕`go run .` 跑出上面三行：命令行和环境变量同时存在时，`Addr` 取命令行、`DSN` 取环境变量；只有文件时取文件；都没有时回到默认。把 `getenv` 作为参数传进来，是为了让它不依赖进程环境，可以直接单测。这是纯标准库版本；viper 做的事一样，只是把四种来源和 `UnmarshalKey` 都封装好了。

远程配置中心是"配置文件不够灵活"之后的选择：需要加解密、权限控制、实时更新。〔W05-3 p20〕最典型的用途是**功能开关**，某个功能出问题立刻改配置关掉。〔W05-3 p25〕开关有时延和误触风险，这是通用经验而非课程原话：关键开关要有权限与审批。

## 小结

1. 四层各自管一件事：web 只管 HTTP，service 管业务，repository 管存储抽象，dao 管数据库；调用链 web → service → repository → dao。
2. 领域对象和数据库行分开定义，在 repository 里转换；缓存属于 repository 层，service 无感知。
3. 面向接口的收益只有扩展性；自底向上抽、看变化点、handler 不抽、超前设计最小化。
4. 依赖注入就是构造函数参数，`main` 是装配车间，自底向上；wire 只省样板，三件套是 Provider、Bind、Injector。
5. 装饰器是横切逻辑的正解：同接口包一层，被装饰者注入，可层层叠加；failover 里超时和取消直接返回不重试。
6. 配置四来源，优先级命令行 > 环境变量 > 文件 > 远程；整体绑定、默认值留业务代码、只在初始化读。

## 常见坑

1. **viper 的 `WatchConfig` 要在设置文件路径之后调用**，放在前面监听不到。〔W05-3 p25〕
2. **匿名导入 `viper/remote`**，不导入的话 `AddRemoteProvider` 注册的 etcd3 provider 不生效，运行时才报错。〔W05-3 p23〕
3. **配置文件相对路径的基准是 working directory**，IDE 和容器里不一样，在 IDE 里要调整 working directory 的设置，否则配置文件找不到；上线用绝对路径或固定路径。〔W05-3 p10〕
4. **两次加载的组件要重建**：远程配置覆盖本地后，已经用旧配置初始化的组件（日志、连接池）不会自动生效。〔W05-3 p7〕
5. **缓存读不到就一律回落数据库**：只要缓存返回 error 就查库，Redis 一崩，所有读都落到数据库。〔W04-1 p17〕

## 判断题与答案

先自己答，再看后面。

1. 为什么 `NewUserRepository(dao, cache)` 把两个依赖都从参数传入，而不是在里面 `gorm.Open`？
2. 限流装饰器为什么要返回非导出的 `errLimited`？
3. failover 里 `context.DeadlineExceeded` 为什么不继续换下一家？
4. 配置里为什么把 `getenv` 做成参数？

**答案**

1. 在函数里 `gorm.Open` 会把 repository 和数据库的初始化过程、连接配置耦死，测试起不了真库，换数据库也要改这里；传参让调用方决定依赖是谁，是标准注入。
2. 课程的要求是不到逼不得已不公开。错误一导出就成了对外契约，调用方会依赖它，以后很难改；限流本身是内部治理信号，不属于业务错误。
3. 超时是调用方设的时限到了，取消是调用方主动放弃，两者都和用户体验直接相关；此时 ctx 已经失效，再换一家重试多半也立刻失败，所以直接返回。
4. 把 `getenv` 做成参数后，`Load` 不依赖进程环境，单测可以直接传一个假的读取函数，覆盖"环境变量存在"和"不存在"两种分支。

练习包 `exercises/p5deep/`：`TestTypeKey`（以类型为 key 的注册表）、`TestProvideInvoke`（迷你容器与单例语义）、`TestGracefulShutdown`（关闭顺序）。

## 在项目里

- **集中配置**：`dnsproxi-go/main.go` 的 `loadConfig` 把所有开关集中在启动时从环境变量读成一个 `Config`，`envIntClamp` 对越界值回退默认（避免错误值炸在后台任务里）。后续代码只拿 `Config`，不再碰环境。
- **依赖作为函数值传入**：`Proxy.Run` 调用 `tcp.Serve(ctx, ln, p.handleQuery, tcp.ServerCfg{})`，把业务处理函数以方法值传给 TCP 层，TCP 层不知道 `Proxy` 的存在。这是不用 DI 框架的依赖注入。

## 想深入去哪查

- 依赖注入与面向接口：W04-3 全文（wire 用法、缺陷、接口抽取顺序）；`C:go_frame/dependency_injection`（手写装配 vs wire 并排）。
- 装饰器与 failover：W04-6（限流装饰器、failover V1 V2、健康判定、tpl 安全治理）。
- 配置：W05-3（viper 初始化、整体绑定、etcd 远程配置、监听变更）。
- 旧长版：`notes/p5-deepsearch.md`（deepsearch 的 DI 骨架）。
