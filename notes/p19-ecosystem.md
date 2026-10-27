# P19 · 生态与常用库：选型地图

> 这章学完你能干什么：拿到新需求（配置/日志/HTTP/队列）30 秒说出
> 社区默认选型和替代品；看懂 go.mod 里每个第三方库在干什么；知道
> "标准库优先"铁律的适用边界。读者：读完 P8 和 P2.5。前置：无硬
> 依赖。后续：P9-P12、P13、P15 是本图四个格子的深潜版。
>
> 动手方式：本章命令都短，开终端跟着跑 `go list -m all`。

## 体系背景：去中心化的分发模型

Go 生态的气质和 Rust 最大的不同，根源在**分发模型**：module path
即地址（`github.com/jackc/pgx` 直接就是导入路径），没有中心注册
表。crates.io 是中心化市场，Go 是"GitHub + proxy.golang.org 兜底
缓存"。

这个模型推出来三件事：

1. **先看标准库能否解决问题**：例如简单 HTTP 服务可以先用 `net/http`，再根据路由、中间件和团队需求评估框架。这是减少依赖的选型策略，不是分发模型保证的比例。
2. **`golang.org/x/...` 是 Go 项目的扩展库**：例如 errgroup、singleflight 和 crypto 补充包。它们不属于标准库，升级时仍要检查版本、兼容性和维护状态。
3. **读传递依赖名是免费课程**：`go mod graph` 挑三个查 README，
   比读十篇选型文有用。

## 从标准库开始评估

先看需求能否由标准库满足；遇到缺失功能、维护成本或团队协作问题，再比较第三方方案：

| 领域 | 标准库答案 | 说明 |
|---|---|---|
| HTTP 服务 | `net/http` | 1.22+ 路由模式匹配（`r.PathValue`），不用框架 |
| HTTP 客户端 | `net/http` | `http.Client` 连接池内置，配 Timeout |
| JSON | `encoding/json` | serde 对标（弱些但够用） |
| 日志 | `log/slog`（1.21+） | P15/P16 深潜 |
| 时间 | `time` | 不引 dayjs 类库 |
| 并发 | `sync`/`channel` | 锁、Once、WaitGroup 全在 |
| 并发模式 | `golang.org/x/sync` | errgroup/singleflight/semaphore |
| 测试 | `testing` | 不用断言库（P2.5 讲过） |
| CSV | `encoding/csv` | Excel 才引 excelize |
| 定时 | `time.Ticker` | cron 语义才引 robfig/cron |

**`encoding/json/v2`（实验性）**：默认关，`GOEXPERIMENT=jsonv2`
才启用，默认构建直接编译失败（实测 "build constraints exclude all
Go files"）。1.26 实测（本机 M4，100000x bench）：同一 struct
Unmarshal v1 296.8ns/6 allocs vs v2 147.1ns/1 alloc（2 倍速、
1/6 分配），Marshal 两者相近（v1 71ns vs v2 136ns，v2 反而慢）。
API 定型前别上生产。

**什么时候考虑第三方**：标准库没有的领域，如数据库驱动（`database/sql` 只是接口）、gRPC、指标和分布式追踪；标准库已有基础能力但项目需要更丰富功能时，也可以按成本选库。

## 默认选型地图（2026）

### 配置

| 库 | 地位 | Rust 对标 |
|---|---|---|
| 标准库 + env | 小服务默认 | 无对标 |
| **spf13/viper** | 大配置（多格式/热更新）；能不用就不用 | config |
| caarlos0/env | env 结构体绑定，轻量惯用 | envy |

本课程 dnsproxi-go 用 flag + env（不到 30 行），是"先标准库"的示范。

### 日志与追踪

| 库 | 地位 | Rust 对标 |
|---|---|---|
| **log/slog** | 官方答案（P15/P16 深潜） | tracing |
| open-telemetry/opentelemetry-go | OTel 官方 SDK | tracing-opentelemetry |
| zap/logrus | 历史：logrus 官宣维护模式，zap 快但先于 slog | 无现役对标 |

纪律：**新项目 slog，老项目别为了换日志而重构**。

### Web 框架

| 库 | 地位 | Rust 对标 |
|---|---|---|
| **net/http** | 默认（1.22 路由后更无借口） | axum |
| gin-gonic/gin | 老牌，新项目已可不必 | actix-web |
| go-chi/chi | 轻路由，要中间件树时用 | 无直接对标 |

框架热的降温是这五年最明显的生态转向：官方教程 RESTful 篇虽仍是
Gin 示范，但 `r.PathValue` 纳入 ServeMux 后社区风向明确"标准库
先试"。

### 数据库（P9/P11/P12 深潜）

| 库 | 地位 | Rust 对标 |
|---|---|---|
| **database/sql** | 标准接口层 | sqlx 的 facade |
| jackc/pgx/v5 | PG 首选驱动（原生类型/批水线） | tokio-postgres |
| sqlc | SQL 先行，生成类型安全代码 | 最接近的是 SeaORM 反向 |
| pressly/goose | 迁移 | sqlx-cli / refinery |
| sqlx（jmoiron） | 老牌扫描库，新项目多被 sqlc 替代 | |

### gRPC 与可观测性

| 库 | 地位 | Rust 对标 |
|---|---|---|
| **google.golang.org/grpc** | 官方（P10 深潜） | tonic |
| **prometheus/client_golang** | metrics 事实标准（P13 深潜） | prometheus crate |

### 任务队列

| 库 | 地位 | Rust 对标 |
|---|---|---|
| channel + errgroup | 进程内队列默认 | mpsc |
| hibiken/asynq | Redis 背景任务（事实标准） | apalis |
| riverqueue/river | PG 背景任务（新锐，PG 栈首选） | pgmq 类 |

### 工具箱杂项

| 需求 | 默认 | 备注 |
|---|---|---|
| UUID | google/uuid | 事实标准 |
| 重试 | 手写 select+timer | 别引全家桶 |
| 断路器 | sony/gobreaker | circuit breaker 官方参考实现 |
| 校验 | go-playground/validator | struct tag 驱动，gin 生态标配 |
| CLI | spf13/cobra | deepsearch 的 cmd/ 就用它（≈ clap） |

## 本仓库依赖逐个点名

`go.mod` 里 31 个依赖，主干 7 个：

| 依赖 | 用在哪 | 对应地图格子 |
|---|---|---|
| jackc/pgx/v5 | P9/P12 PG 驱动 | 数据库 |
| pressly/goose/v3 | P11 迁移 | 数据库 |
| modernc.org/sqlite | P9 纯 Go SQLite | 数据库（**mattn/go-sqlite3 要 CGO**，CI 选 modernc 的原因） |
| prometheus/client_golang | P13/P17 指标 | 可观测性 |
| google.golang.org/grpc + protobuf | P10 | gRPC |
| golang.org/x/sync | P14 errgroup | 半官方特区 |

传递依赖抽样：`cespare/xxhash`（Prometheus 的哈希）、
`beorn7/perks`（直方图分位数）、`puddle`（pgx 的连接池）。

## 新库评估四信号

重要性递减：

1. **谁维护**：看维护责任、发布节奏、问题处理和安全响应，不仅看组织或个人身份。
2. **生态契合**：涉及取消的操作是否支持 `context.Context`；错误是否按需支持 `errors.Is`/`errors.As`；日志能否接入项目现有体系。
3. **替换成本**：检查实际使用的 API、数据模型和数据库方言。pgx 的 `database/sql` 驱动接口与原生 pgx API 是不同的接入方式，接口相同也不意味着 SQL 可无代价换库。
4. **衰退信号**：README 写 maintenance mode、长期未处理的重要 issue 等都值得调查；稳定的小工具提交少并不自动意味着不可用。

对照：crates.io 的 download 数 ≈ pkg.go.dev 的 import 数
（`go.dev/pkg?importers` 可查）。

## 动手：练习 p19eco（20-30 分钟）

```sh
go test ./exercises/p19eco/ -v
go list -m all | head -20     # 看依赖树
```

**观察题**：

1. `go list -m all` 找出 `prometheus/client_golang` 的传递依赖，
   对照本章表格认领。
2. `go generate ./exercises/p19eco/` 跑 stringer，看生成的
   `level_string.go`（P1 generate 节 + P2.5 惯例的合流）。

**预测题**（跑之前写答案）：

1. `TestPingCtx` 里用已取消的 ctx 调 `Ping`，会等一小时超时吗？
   返回什么错误？
2. `TestStringer` 的 `Warn.String()` 返回什么？为什么是小写
   （`-linecomment`）？

**改造题**（在练习包里）：

1. `TestSlogHandler`：给只打 `level>=warn` 的自定义 Handler 补全
   `Enabled` 方法：体验"生态库全长在标准库接口上"。
2. `TestFunctionalOptions`：把 `NewServer(addr)` 补成 functional
   options（`WithTimeout`/`WithLogger`）。

## 小结

1. 标准库是选型起点，不是禁止依赖的铁律；`golang.org/x/...` 是独立版本的 Go 项目扩展库。
2. 默认选型：slog（日志）、net/http（服务）、pgx+sqlc+goose
   （PG 栈）、client_golang（指标）、cobra（CLI）、asynq/river
   （队列）。
3. 本仓库 7 个主干依赖每个都能对上地图的格子。
4. 评估新库：维护者 → 生态契合 → 替换成本 → 衰退信号。
5. 选型偏离默认要有理由，理由写在 go.mod 旁注释或 README。

## 章末参考

### functional options（对照 Rust builder）

```go
type Server struct {
    addr    string
    timeout time.Duration
}

type Option func(*Server)

func WithTimeout(d time.Duration) Option { return func(s *Server) { s.timeout = d } }

func NewServer(addr string, opts ...Option) *Server {
    s := &Server{addr: addr, timeout: 5 * time.Second}   // 默认值
    for _, o := range opts { o(s) }
    return s
}

srv := NewServer(":8080", WithTimeout(10*time.Second))
```

进阶形态：`Option` 改成 `interface{ apply(*Server) }` 支持错误返回
（grpc-go 的风格）。标准库不用这招（`http.Server` 直接公开字段），
**内部库才讲究，写业务代码别过度设计**。

### stringer 全流程

```go
//go:generate stringer -type=Level -linecomment
type Level int

const (
    Debug Level = iota
    Info
)
```

```sh
go install golang.org/x/tools/cmd/stringer@latest
go generate ./...      # 产出 level_string.go
```

产物**提交进 git**（对照 Rust derive：编译期自动，Go 是生成期手动）。
CI 里 `go generate ./... && git diff --exit-code` 验证生成物新鲜。

### 值得知道的两个库

- **singleflight**（golang.org/x/sync/singleflight）：同 key 并发
  请求合并成一次调用，缓存击穿的官方解。
- **uber-go/goleak**：测试检测 goroutine 泄漏，P14 手写
  NumGoroutine 的库化版本（`defer goleak.VerifyNone(t)`）。

### Rust 对照总表

| 主题 | Go | Rust | 差异要点 |
|---|---|---|---|
| 分发 | module path 即地址 + proxy | crates.io 中心化 | Go 去中心，URL 即依赖 |
| 默认栈 | 标准库起步 | tokio+serde+anyhow | Go 零依赖是常态 |
| builder | functional options | builder pattern | Go 用闭包，Rust 用链式 |
| 派生 | stringer（生成物提交） | derive 宏（编译期） | Go 无编译期元编程 |
| 老化判断 | import 数 / maintenance mode | download 数 / is-it-maintained | 直觉通用 |
