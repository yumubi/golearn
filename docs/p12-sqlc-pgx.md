---
title: 第 12 章 · sqlc + pgx/v5：PG 原生类型，一条配置换血
---


> 教程章。P11 用 `database/sql` 承接生成代码（换库自由）；这章锁定 PostgreSQL，
> `sql_package: "pgx/v5"` 一行换血：生成代码直接说 pgx 方言，PG 特有类型
> 原生进出。Rust 对照：tokio-postgres + 生成器（如 sqlx 的 postgres 特化）。

## 这章学完你能干什么

P9-P11 的一套 `.sql` 工作流原样搬过来，加一行 `sql_package: "pgx/v5"`，
生成代码就从 database/sql 方言换成 pgx 方言：`text[]` 进出是 Go 切片、
jsonb 直接落到 `[]byte`、`pgx.Batch` 一次网络往返写 20 条。读完你能回答
四个问题：

1. 同一套 sqlc 工作流，pgx/v5 版生成代码差在哪几处？
2. `text[]`/jsonb/timestamptz 这些 PG 类型怎么原生进出？
3. P11 的 `WithTx` 纪律到 pgx 里多了什么坑？
4. database/sql、pgx 手写、sqlc+pgx 三种工作流怎么选？

```sh
sqlc generate                        # 同一套 .sql 工作流，产出 pgx 版代码
P12_PG_DSN="postgres://..." go test -race ./exercises/p12pgx/
```

终点：真连 PG17，四个测试全绿：`text[]` 数组原生读写、jsonb 进出、
`pgx.Batch` 一次往返 20 条 INSERT、事务回滚。**DSN 没设置就 skip**，
离线 CI 照样绿（这个门控模式值得抄到任何真库测试里）。

## 12.1 换血：database/sql 版 vs pgx/v5 版生成代码

P11 的 sqlc.yaml 加一段（`sqlc.yaml` 里两套并存，各自 out 目录）：

```yaml
- engine: postgresql # 引擎换 postgresql
  schema: exercises/p12pgx/migrations
  queries: exercises/p12pgx/queries
  gen:
    go:
      package: db
      out: exercises/p12pgx/db
      sql_package: pgx/v5 # ← 这一行换血
```

生成代码的三处变化（`exercises/p12pgx/db/db.go` 对照 P11 版）：

| | P11（database/sql） | P12（pgx/v5） |
|---|---|---|
| 接收者接口 | `DBTX`（*sql.DB/*sql.Tx） | `DBTX`（pgx 连接/事务方法集） |
| 事务 | `WithTx(tx *sql.Tx)` | `WithTx(tx pgx.Tx)` |
| 连接池 | `sql.Open` + `db.SetMaxOpenConns` | `pgxpool.NewWithConfig`（池参数在 config 里） |
| 收尾调用 | `tx.Rollback()` 不带 ctx | `tx.Rollback(ctx)` **带 ctx**（见 12.4 的坑） |

**心智模型**：pgx 是 PG 的原生客户端（协议级），database/sql 是通用抽象层。
sqlc 生成代码本来就没绑定哪一层，`sql_package` 就是绑定开关。

## 12.2 连接：pgxpool 的姿势

```go
cfg, err := pgxpool.ParseConfig(dsn)             // 解析 + 校验
pool, err := pgxpool.NewWithConfig(ctx, cfg)
defer pool.Close()
```

池参数在 config 里调（对照 P9 的 `db.SetMaxOpenConns`）：
`cfg.MaxConns`、`cfg.MinConns`、`cfg.MaxConnLifetime`。
**默认 MaxConns = max(4, NumCPU)**：生产要按 PG 的 max_connections 规划，
不像 database/sql 默认无限（无限在 PG 侧会撞 `max_connections`）。

## 12.3 PG 类型原生映射（这章的核心价值）

表设计就冲着 database/sql 的盲区去（`0001_articles.sql`）：

| PG 类型 | database/sql 版 | pgx/v5 版（生成代码实测） |
|---|---|---|
| `TEXT[]` | 手动 parse（string + split 或 pq.Array） | **`[]string` 直接进出** |
| `JSONB` | `[]byte` 手动 parse（一样） | `[]byte`（unmarshal 到 map/struct） |
| `TIMESTAMPTZ` | `time.Time`（驱动猜 layout） | `pgtype.Timestamptz`（`.Valid` + `.Time`） |
| `BIGINT` | `int64` | `int64` |

练习坐实（`TestPGTypes`）：

```go
a, _ := q.CreateArticle(ctx, db.CreateArticleParams{
    Title: "hello pgx",
    Tags:  []string{"go", "db"},     // Go 切片 → PG 数组，一个字节的胶水都没有
    Meta:  raw,                       // json.Marshal 的 []byte → jsonb
})
got, _ := q.GetArticle(ctx, a.ID)
got.Tags[0] == "go"                   // 直接可用
json.Unmarshal(got.Meta, &back)       // jsonb 回来是 []byte，unmarshal 验内容
```

数组查询也原生：`WHERE tags @> $1` 传 `[]string{"db"}`：
`@>` 包含操作符走 GIN 索引，这是"数组列也能走索引"的 PG 惯用法
（对照 diesel 里要写 `sql("tags @> ")` 的窘迫）。

**timestamptz 的两个选择**：
默认 `pgtype.Timestamptz`（显式，带 Valid）；
在 `sqlc.yaml` 加 override 映射成 `time.Time`（省 Valid 判断，NULL 时零值）。
教程 yaml 里给了 override 示例但注释掉了：**生产建议保留 pgtype**，
NULL 语义显式比隐式好（P9 NULL 三策略在这里的延伸）。

## 12.4 事务：P11 纪律的 pgx 形态 + 一个新坑

```go
func withTxPgx(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
    tx, err := pool.Begin(ctx)
    if err != nil { return err }
    defer tx.Rollback(ctx)          // ← ctx 参数！
    if err := fn(tx); err != nil { return err }
    return tx.Commit(ctx)
}
```

**新坑（database/sql 版没有）**：pgx 的 `Rollback(ctx)`/`Commit(ctx)` 都收
ctx。如果你传的 ctx 已被取消（比如请求超时正好触发），**收尾本身会失败**：
事务被服务器按 idle timeout 回收，行为说不清。惯例：

```go
defer tx.Rollback(context.WithoutCancel(ctx))   // 收尾不受取消影响（Go 1.21+）
```

练习里为了对照 P11 保持了直传 ctx（能跑，因为测试 ctx 活着）；
生产代码把 `WithoutCancel` 写上。**Take-away：database/sql 的收尾没 ctx
是"没法传错"；pgx 的收尾收 ctx 是"能传错"。能力越大坑越大。**

## 12.5 batch：真流水线（P9 的 database/sql 版只是顺序复用）

```go
batch := &pgx.Batch{}
for _, title := range titles {
    batch.Queue(`INSERT INTO articles (title, tags, meta) VALUES ($1,$2,$3)`,
        title, []string{"batch"}, []byte(`{}`))
}
br := pool.SendBatch(ctx, batch)
defer br.Close()
for range titles {
    if _, err := br.Exec(); err != nil { return err }
}
```

**pgx.Batch 是网络级流水线**：N 条命令打包一次往返（pipelining），
不是"循环复用连接"。20 条 INSERT 实测 ~2ms（P9 那版要 20 次往返）。
数据量上万换 `COPY FROM`（`pgx.CopyFrom`，快一个数量级，但只支持
INSERT 无 RETURNING）。

**sqlc 与 batch 的接缝**：生成的 `:one` 方法走 `QueryRow` 单发，
batch 场景要么直接用位置参数 SQL（练习里的做法。注意生成常量
`createArticle` 是**小写未导出**，跨包拿不到），要么给查询加
`:copyfrom`/`batchexec` 注解让 sqlc 直接生成 batch 方法。

## 12.6 三种 PG 工作流怎么选（P9-P12 的总结）

| 组合 | 何时用 | 代价 |
|---|---|---|
| database/sql + 任一驱动 | 要换库自由（sqlite 测试） | 抽象层开销、PG 类型走胶水 |
| pgx 原生手写 | PG 专属逻辑多、原型期 | 手写 Scan，类型安全靠测试 |
| **sqlc + pgx/v5**（本章） | 锁定 PG + 要类型安全 | 生成步骤（CI 一行） |

本章练习 = 第三种的完整闭环：`migrations/` + `queries/*.sql` + `sqlc.yaml`
三件套，`sqlc generate` 一次到位，`WithTx` 纪律跨 P11/P12 通用。

## 12.7 动手：练习 p12pgx（30-45 分钟，需 PG）

```sh
# 起 PG（一次性容器）
docker run -d --rm --name p12pg -e POSTGRES_PASSWORD=p9 -e POSTGRES_DB=p12 \
  -p 127.0.0.1:15433:5432 postgres:17-alpine

# 全绿（-race）
P12_PG_DSN="postgres://postgres:p9@127.0.0.1:15433/p12?sslmode=disable" \
  go test -race ./exercises/p12pgx/

# 离线也要绿：不设 DSN 全部 SKIP
go test ./exercises/p12pgx/

docker stop p12pg
```

**预测题**（跑之前写答案）：

1. 不设 `P12_PG_DSN` 跑 `go test ./exercises/p12pgx/`，结果是 PASS、
   FAIL 还是 skip？为什么这么设计？（12.0 门控）
2. `CreateArticle` 传 `Tags: []string{"go", "db"}`，PG 侧存的是什么？
   读回来的 `got.Tags[0]` 是什么类型？（12.3）
3. 请求 ctx 在 `fn(tx)` 执行中途超时取消，`defer tx.Rollback(ctx)`
   会发生什么？改成哪种写法能免疫？（12.4）

**观察验证**：跑测试时开 PG 的日志（`docker logs -f p12pg`），
对比 `TestBatchInsert`（20 条 INSERT 一次往返）和单发版本的往返次数。

| 练习 | 对应小节 | 检验什么 |
|---|---|---|
| TestPGTypes | 12.3 | `[]string` 数组、jsonb、timestamptz、`@>` 查询 |
| TestExecRowsAndTop | 12.3 | execrows 存在性 + 排序分页 |
| TestBatchInsert | 12.5 | 20 条 INSERT 一次往返 |
| TestWithTxPgx | 12.4 | 回滚坐实 + q.WithTx(tx) |

**改造题**：给 articles 加 `GIN INDEX ON tags` 迁移（0002），`EXPLAIN`
对比加索引前后 `ListByTag` 的计划：PG 侧知识，但工作流是 Go 的。

## 12.8 Take-away

1. `sql_package: "pgx/v5"` 一行换血：生成代码从 database/sql 方言变 pgx 方言，
   PG 类型（数组/jsonb/timestamptz）原生进出，胶水代码归零。
2. `pgx.Batch` 是真流水线（一次往返 N 条），对照 P9 的"顺序复用连接"。
3. pgx 收尾调用带 ctx：传被取消的 ctx 收尾会失败，用 `context.WithoutCancel`。
4. P9（手写）→ P11（生成 + sqlite）→ P12（生成 + PG 原生）：同一套
   withTx 纪律、同一套 NULL 语义，实现层三级跳。
