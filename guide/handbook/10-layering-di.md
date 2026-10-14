# 10 分层、依赖注入与配置：服务代码怎么长成可替换的零件

> 详细程度：深入 · 约 35 分钟
> 材料：W04-3 W05-3 W04-6 W02-2、C:go_frame/dependency_injection、旧长版 `notes/p5-deepsearch.md`
> 前置：04 接口与组合

## 这一节要建立的认知

一个后端服务能不能长期演进，看三件事：**每一层只依赖抽象**（能换实现）、**依赖从外面传进来**（能装配）、**配置只在启动时读一次**（能换来源）。

Go 社区在这三件事上的共识都偏"显式"：用接口隔离层，用构造函数传依赖，用 `main` 当装配车间。没有运行时容器，没有注解扫描。框架（wire、viper）只是帮你少写样板，不改变这个结构。

## 和你已知语言的差异

| 问题 | Java / Spring | Rust | Go |
|---|---|---|---|
| 依赖怎么进来 | `@Autowired`，运行期反射容器 | 手写构造 + 泛型或 `dyn Trait` | 构造函数参数；可选 wire 在编译期生成装配代码 |
| 谁来定义接口 | 常在提供方 | 常在提供方 | 惯例在使用方；课程把接口放在被调用的包里，两种都有人用 |
| 配置来源合并 | `application.yml` + profile + `@ConfigurationProperties` | config-rs 多源合并 | viper 多源合并，`UnmarshalKey` 整体绑定到结构体 |
| 横切逻辑（限流、重试） | AOP、`@Retryable` | tower `Layer` | 装饰器：同接口包一层 |

## 一、分层：每一层只管一件事

课程里的 webook 后端用四层：web（只管 HTTP）、service（业务逻辑）、repository（存储抽象）、dao（数据库操作）。repository 是整体存储抽象，下面可以是 MySQL、ES 或缓存，dao 只代表数据库。调用链是 web 调 service，service 调 repository，repository 调 dao。〔W02-2 p14-15〕〔W02-2 p16-17〕

两个容易被忽略的细节：

- **领域对象和数据库行分开定义**。`domain.User` 是业务概念，`dao.User` 直接映射表列（时间用 `int64` 毫秒），在 repository 里互相转换。表结构变了，不会一路波及到 service。〔W02-2 p18〕
- **缓存放在 repository 层**。缓存属于"如何存数据"，service 对它无感知。构造函数同时收 dao 和 cache 两个依赖。〔W04-1 p16〕

下面是一个能跑的最小骨架，分层靠接口，依赖靠参数：

```go
type UserRepo interface {
	FindByPhone(ctx context.Context, phone string) (User, error)
}

type UserService struct{ repo UserRepo }

func NewUserService(repo UserRepo) *UserService { return &UserService{repo: repo} }

func (s *UserService) Login(ctx context.Context, phone string) (int64, error) {
	u, err := s.repo.FindByPhone(ctx, phone)
	if err != nil {
		return 0, fmt.Errorf("login: %w", err)
	}
	return u.ID, nil
}
```

装配永远是自底向上：先 new 最底层（连接、缓存），再 new repository，再 service，最后 handler 和路由。这就是课程说的"初始化七步链"。〔W04-3 p4〕

## 二、面向接口：收益只有扩展性

课程的定义很朴素：组件之间通信必须通过接口，再配合依赖注入，就是"编译时依赖接口，初始化时注入实现"。〔W04-3 p24〕它也直说了代价和收益：**不提高性能，也不提高可靠性，收益只有扩展性**。〔W04-3 p27〕

所以不是处处抽接口，课程给的判断很实用：

1. **自底向上抽**：缓存和 DAO 在最底部、不依赖别人，先抽。〔W04-3 p28-30〕
2. **看变化点**：非主流第三方（短信、对象存储）要考虑替换；MySQL、Redis 这种主流的可以忽略。〔W04-3 p36〕
3. **handler 不抽接口**：接口在被调用方一侧才有意义，没有人 mock 它的调用者，就不留。〔W04-3 p31-32〕
4. **超前设计最小化**：只留接口，不写多余实现；抽象时只考虑有限的几种情况，别想太长远。〔W04-3 p35〕

一个和 Go 惯例冲突的地方要记住：wire 场景里构造函数最好返回接口（配合 `wire.Bind` 做类型匹配），而 Go 社区的惯例是"接受接口，返回结构体"。课程的态度是两者都有人用，按团队取舍。〔W04-3 p33〕

## 三、依赖注入：手写够用，wire 是省样板

依赖注入有两种形式：外部把实现传进来（注入），或 A 自己去找可用的 B（查找）。共同点是 A 不自己初始化 B。〔W04-3 p10〕Go 里主流是前者，即构造函数参数。

不注入的写法有哪些问题，课程列得很清楚：初始化过程和依赖深度耦合、要额外定义 Config 类型、依赖变更要跟着改、测试不友好、公共客户端难复用。〔W04-3 p5-6〕

### 手写装配

`main` 是装配车间，一个服务通常就是几十行。资源关闭也要成对考虑：每个创建连接的函数，同时返回一个 cleanup 闭包，`main` 里按相反顺序执行。〔C:go_frame/dependency_injection/main.go:22-32〕

### wire：编译期生成装配代码

wire 的模型只有三个词：**Provider**（构造函数的集合）、**Bind**（接口到实现的映射）、**Injector**（要生成的入口函数）。你写一个带 `wireinject` 构建标签的文件，里面是 `wire.Build(...)`，运行 `wire` 命令后生成真正的 `wire_gen.go`。〔W04-3 p7-8〕

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

wire 的缺陷课程也点名了：**不能按环境选不同实现**（要自己在函数体里写条件）、**找不到某接口的所有实现**、**找不到某类型的所有实例**（gin 中间件只能手写列表）。结论是"矮子里拔将军"：好处是清晰、可控、比手写装配快。〔W04-3 p17-19〕〔W04-3 p20-21〕wire 不支持单例，重复调用同一个初始化函数会得到多个实例，需要单例要在构造函数里自己用 `sync.Once`。〔W04-3 p9〕〔C:go_frame/dependency_injection/database/gorm/connection.go:14-31〕

另一条路是运行时容器。`notes/p5-deepsearch.md` 里读过的 deepsearch 项目用泛型加类型作 key 的注册表，练习包 `exercises/p5deep` 里的 `TestProvideInvoke` 带你写一个 20 行的迷你版。取舍很直接：运行时容器灵活但出错晚，wire 严格但要多一步生成。

## 四、装饰器：横切逻辑不要侵入业务

短信服务要加限流。第一反应是直接改 `Send`，课程明确反对：别的服务商也要限流、类似功能都得改，越改越难看；功能没错，可扩展性和非侵入性全错。〔W04-6 p9〕

正解是装饰器：不改原实现，在外面包一层同接口的对象，可以层层叠加，被装饰者作为依赖注入，装饰器只做一件事。〔W04-6 p10-12〕

```go
type RateLimitSender struct {
	next    Sender
	limiter Limiter
}

func (r RateLimitSender) Send(ctx context.Context, to, msg string) error {
	limited, err := r.limiter.Limit(ctx, "sms")
	if err != nil {
		return fmt.Errorf("limiter: %w", err)
	}
	if limited {
		return errLimited
	}
	return r.next.Send(ctx, to, msg)
}
```

三个细节：

1. 触发限流返回的 `errLimited` **保持非导出**。这是内部治理信号，暴露成公开错误会被上游当业务错误处理，到逼不得已再公开。〔W04-6 p12〕
2. 装饰器里内嵌被装饰接口可以只实现部分方法，但调用方能直接访问内嵌字段绕开装饰器；非内嵌版必须实现全部方法，但绕不开。要拦截全部调用就选后者。〔W04-6 p15〕
3. 接口都可 mock，所以装饰器很好测：正常发送、触发限流、限流器出错，三个用例覆盖全部分支。〔W04-6 p13-14〕

同样的手法做**容错**。failover 把多个服务商包成一个 `Sender`：起始下标用原子计数轮询（避免所有请求都砸在第一个），失败换下一个，全部失败才报错。其中有一条重要纪律：**超时和取消直接返回，不再重试**，继续重试只会浪费配额并拖垮体验。〔W04-6 p25-26〕

```go
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
```

组合起来是 `RateLimitSender{next: &Failover{...}}`：先限流，再 failover，业务代码一行不变。〔实测〕这几段在 `go1.26.4` 上一起编译运行，限流关闭时第一家服务商返回 `EOF` 后自动换到第二家成功，限流打开时直接返回 `limited`。

进一步的健康判定（连续 N 次超时就切换服务商）用原子 CAS 实现，课程承认并发下是近似的，换来无锁。〔W04-6 p28-30〕这也引出一条通则：**无并发控制的任何操作都不是并发安全的**，而 mock 测不出并发问题，只能验证无并发时的结果，剩下靠 code review。〔W04-6 p31-32〕

超时、重试、限流这类治理逻辑的位置就是装饰器这一层，或者 gRPC 的拦截器、HTTP 的中间件。它们都是同一个模式（见 04 节）。

## 五、配置：来源、优先级、整体绑定

配置有四类来源：启动参数、环境变量、配置文件、远程配置中心。课程给的是一套个人实践：命令行最权威，然后是环境变量、配置文件、远程中心。〔W05-3 p4〕〔W05-3 p5-6〕优先级没有行业标准，K8s 场景常把挂载的 ConfigMap 放得更高，团队内一致即可。

几条实用的纪律：

1. **整体绑定，不要单字段读**。不推荐到处 `viper.GetString("redis.addr")`，推荐定义内部结构体，用 `UnmarshalKey` 一次性绑定。〔W05-3 p11-12〕
2. **默认值留在业务代码里**：先填好结构体默认值再反序列化，配置缺失就保留默认。〔W05-3 p13〕
3. **配置只在初始化读**。配置操作限定在 IoC 和 `main`，换配置框架只改初始化；service 层监听配置变更会违背这条。〔W05-3 p28〕
4. **用统一 API 屏蔽来源差异**。借 viper 的 API 屏蔽文件、etcd、Nacos 的差别，不直接操作 etcd。〔W05-3 p27〕

用标准库就能演示"默认值 → 文件 → 环境变量 → 命令行"这条覆盖链：

```go
func Load(args []string, getenv func(string) string, file []byte) (Config, error) {
	cfg := Config{Addr: ":8080", DB: DB{DSN: "root:root@tcp(localhost:3306)/webook"}}
	if len(file) > 0 {
		if err := json.Unmarshal(file, &cfg); err != nil {
			return cfg, err
		}
	}
	if v := getenv("APP_DB_DSN"); v != "" {
		cfg.DB.DSN = v
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
```

〔实测〕传入文件 `{"db":{"dsn":"file-dsn"}}`、环境变量 `APP_DB_DSN=env-dsn` 和参数 `--addr=:9000`，输出 `{Addr::9000 DB:{DSN:env-dsn}}`；去掉环境变量和参数后输出 `{Addr::8080 DB:{DSN:file-dsn}}`。把 `getenv` 作为参数传进来，是为了让它不依赖进程环境，可以直接单测。

远程配置中心是"配置文件不够灵活"之后的选择：需要加解密、权限控制、实时更新。〔W05-3 p20〕最典型的用途是**功能开关**，某个功能出问题立刻改配置关掉；但开关有时延和误触风险，关键开关要有权限与审批。〔W05-3 p25〕

## 常见坑

1. **viper 的 `WatchConfig` 要在设置文件路径之后调用**，放在前面监听不到。〔W05-3 p25〕
2. **匿名导入 `viper/remote`**，不导入的话 `AddRemoteProvider` 注册的 etcd3 provider 不生效，运行时才报错。〔W05-3 p23〕
3. **配置文件相对路径的基准是 working directory**，IDE 和容器里不一样，直接 panic。上线用绝对路径或固定路径。〔W05-3 p10〕
4. **两次加载的组件要重建**：远程配置覆盖本地后，已经用旧配置初始化的组件（日志、连接池）不会自动生效。〔W05-3 p7〕
5. **wire 改了构造函数忘记重新生成**，要么编译失败，要么悄悄用旧装配。
6. **缓存读不到就一律回落数据库**：Redis 崩溃时所有读都会落到数据库。课程给了两种写法，只在"确认是未命中"时才查库，别的错误直接返回，数据库保住，其它业务不受连累。〔W04-1 p17〕

## 判断题

1. 为什么 `NewUserRepository(dao, cache)` 把两个依赖都从参数传入，而不是在里面 `gorm.Open`？
2. 限流装饰器为什么要返回非导出的 `errLimited`？
3. failover 里 `context.DeadlineExceeded` 为什么不继续换下一家？
4. 配置里为什么把 `getenv` 做成参数？

练习包 `exercises/p5deep/`：`TestTypeKey`（以类型为 key 的注册表）、`TestProvideInvoke`（迷你容器与单例语义）、`TestGracefulShutdown`（关闭顺序）。

## 在项目里

- **函数式配置**：`dnsproxi-go/main.go` 的 `loadConfig` 把所有开关集中在启动时从环境变量读成一个 `Config`，`envIntClamp` 对越界值回退默认（避免错误值炸在后台任务里）。后续代码只拿 `Config`，不再碰环境。
- **依赖作为函数值传入**：`Proxy.Run` 调用 `tcp.Serve(ctx, ln, p.handleQuery, tcp.ServerCfg{})`，把业务处理函数以方法值传给 TCP 层，TCP 层不知道 `Proxy` 的存在。这是不用 DI 框架的依赖注入。
- **资源与关闭**：`Proxy.Run` 里监听、缓存、后台回收任务共用同一个 `ctx` 取消和 `WaitGroup` 等待，退出路径只有一条。

## 想深入去哪查

- 依赖注入与面向接口：W04-3 全文（wire 用法、缺陷、接口抽取顺序）；`C:go_frame/dependency_injection`（手写装配 vs wire 并排）。
- 装饰器与 failover：W04-6（限流装饰器、failover V1 V2、健康判定、tpl 安全治理）。
- 配置：W05-3（viper 初始化、整体绑定、etcd 远程配置、监听变更）。
- 分层与三层职责：W02-2 p14-20。
- 旧长版：`notes/p5-deepsearch.md`（deepsearch 的 DI 骨架）。
