---
title: 第 12 章 · sqlc + pgx/v5：PG 原生类型，一条配置换血
---

# 第 12 章 · sqlc + pgx/v5：PG 原生类型，一条配置换血


> 这章学完你能干什么：P9-P11 的 `.sql` 工作流原样搬过来，加一行 `sql_package: "pgx/v5"`，生成代码就换成 pgx 方言。`text[]` 进出是 Go 切片，jsonb 直接落到 `[]byte`，`pgx.Batch` 一次网络往返写 20 条。
> 读者：写过 Java/Kotlin 或 TS 的后端，会一点 Rust。前置：P11。后续：P13 起的可观测性五连。
>
> 动手方式：本章要一个 PG。按"练习"一节的 `docker run` 起容器，先用 `docker exec` 里的 `psql` 看服务端。Go 例子放在仓库根目录下新建的 `scratch/main.go`（用完删掉），用 `go run ./scratch` 运行，片段省略 `package main` 和 `import`，`dsn` 是下面的连接串。

```sh
dsn="postgres://postgres:p9@127.0.0.1:15433/p12?sslmode=disable"
```

## 体系背景：PG 的连接是一个进程

先看服务端。开三个会话，各自执行 `select pg_sleep(8)`，再看容器里的进程：

```sh
$ docker exec p12pg ps | grep "postgres:"
   26 postgres  0:00 postgres: checkpointer
   27 postgres  0:00 postgres: background writer
   29 postgres  0:00 postgres: walwriter
   30 postgres  0:00 postgres: autovacuum launcher
   31 postgres  0:00 postgres: logical replication launcher
   56 postgres  0:00 postgres: postgres liao12 [local] SELECT
   62 postgres  0:00 postgres: postgres liao12 [local] SELECT
   68 postgres  0:00 postgres: postgres liao12 [local] SELECT
```

后三行就是三个连接。PG 的进程模型是一个连接一个操作系统进程，由主进程 `fork` 出来。进程有独立的内存，建立和销毁都不便宜，上限由 `max_connections` 决定：

```sh
$ docker exec p12pg psql -U postgres -c "show max_connections"
 max_connections
-----------------
 100
(1 row)
```

连接池通常是控制连接数和复用连接的重要手段。规划时还要给管理连接和其他应用留余量，否则应用实例一多就可能撞到服务端上限。

这和 Java 的 HikariCP、Node 的 `pg.Pool` 是同一个理由。区别是 Go 里 goroutine 太便宜，一个请求一个 goroutine 很容易把连接用光，池的上限要自己想清楚。

再看一条语句怎么走协议。pgx 默认把每条带参数的语句做成预备语句缓存在连接上：

```go
conn, _ := pool.Acquire(ctx)
defer conn.Release()
var x int
for i := range 3 {
	conn.QueryRow(ctx, "select $1::int + 1", i).Scan(&x)
	fmt.Println("结果", x)
}
rows, _ := conn.Query(ctx, "select name, statement from pg_prepared_statements")
```

```sh
$ go run ./scratch
结果 1
结果 2
结果 3
stmtcache_f50f3a6a5b1611e8241bf46ca6e5ec7724a11d788d678d90 => select name, statement from pg_prepared_statements
stmtcache_9d95881a9af31aec478a7fc8614da285b1f0ae39340541a2 => select $1::int + 1
```

这是 PG 的扩展查询协议：先 Parse 出一个命名的语句，再 Bind 参数并 Execute。同一连接上缓存命中时可以复用已解析语句。`$1` 将值作为参数传给服务端，避免把值拼进 SQL；但动态拼接表名、排序表达式等仍需单独校验，参数化不能保护这些部分。

预备语句属于单个连接。PgBouncer 事务池模式下，客户端事务可能使用不同的服务端连接；兼容性取决于 PgBouncer 版本和配置。部署时应核对其 prepared statement 支持及 pgx 查询模式，不要假定所有事务池都必须关闭预备语句。

最后一个事实是 MVCC，也就是多版本并发控制。PG 的 `UPDATE` 不改原行，而是写一个新版本。在一张只有一行的表上试试：

```sql
create table t(id int primary key, n int);
insert into t values (1, 10);
select xmin, ctid, * from t;
update t set n = 11 where id = 1;
select xmin, ctid, * from t;
begin;
select txid_current();
update t set n = 12 where id = 1;
select xmin, ctid, * from t;
rollback;
select xmin, ctid, * from t;
```

```sh
$ docker exec -i p12pg psql -U postgres -d liao12 < mvcc.sql
 xmin | ctid  | id | n
------+-------+----+----
  777 | (0,1) |  1 | 10
(1 row)

UPDATE 1
 xmin | ctid  | id | n
------+-------+----+----
  778 | (0,2) |  1 | 11
(1 row)

BEGIN
 txid_current
--------------
          779
(1 row)

UPDATE 1
 xmin | ctid  | id | n
------+-------+----+----
  779 | (0,3) |  1 | 12
(1 row)

ROLLBACK
 xmin | ctid  | id | n
------+-------+----+----
  778 | (0,2) |  1 | 11
(1 row)
```

`xmin` 是创建这个版本的事务号，`ctid` 是它在表里的物理位置。更新之后事务号从 777 变成 778，位置从 `(0,1)` 变成 `(0,2)`。事务 779 写出的版本在回滚后消失，读到的又是 778 那一版。

后面讲事务的收尾时会用到这个事实。事务没提交，服务端就当它没发生过。

## 换血

P11 用 `database/sql` 承接 sqlc 的生成代码，换库自由。这章锁定 PostgreSQL，目标是让 PG 特有的类型原生进出。

`sqlc.yaml` 里 PG 引擎这一段只多了一行：

```yaml
- engine: postgresql
  schema: exercises/p12pgx/migrations
  queries: exercises/p12pgx/queries
  gen:
    go:
      package: db
      out: exercises/p12pgx/db
      sql_package: pgx/v5 # 这一行换血
```

`sqlc.yaml` 里两套引擎并存，各自有 `out` 目录。生成出来的 `exercises/p12pgx/db/db.go` 和 P11 的版本对比：

```sh
$ diff exercises/p11sqlm/db/db.go exercises/p12pgx/db/db.go
9c9,11
< 	"database/sql"
---
> 	"github.com/jackc/pgx/v5"
> 	"github.com/jackc/pgx/v5/pgconn"
13,16c15,17
< 	ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
< 	PrepareContext(context.Context, string) (*sql.Stmt, error)
< 	QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error)
< 	QueryRowContext(context.Context, string, ...interface{}) *sql.Row
---
> 	Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error)
> 	Query(context.Context, string, ...interface{}) (pgx.Rows, error)
> 	QueryRow(context.Context, string, ...interface{}) pgx.Row
27c28
< func (q *Queries) WithTx(tx *sql.Tx) *Queries {
---
> func (q *Queries) WithTx(tx pgx.Tx) *Queries {
```

只有三处变化：`DBTX` 接口的方法集、`WithTx` 的参数类型、连接池。事务收尾也变了：`tx.Rollback()` 现在要传 ctx，下面"事务"一节会讲这个坑。

心智模型是：pgx 是 PG 的原生客户端，说的是协议。`database/sql` 是通用抽象层。sqlc 生成的代码本来就没绑定哪一层，`sql_package` 就是绑定开关。

## 连接池

pgx 的池是 `pgxpool`，参数在配置对象里调：

```go
cfg, err := pgxpool.ParseConfig(dsn)
pool, err := pgxpool.NewWithConfig(ctx, cfg)
defer pool.Close()
```

对照 P9 的 `db.SetMaxOpenConns`，这里是 `cfg.MaxConns`、`cfg.MinConns` 和 `cfg.MaxConnLifetime`。先看默认值，再把上限压到 3，同时发 10 条各睡 200 毫秒的查询：

```go
cfg, _ := pgxpool.ParseConfig(dsn)
fmt.Println("默认 MaxConns:", cfg.MaxConns)

cfg.MaxConns = 3
pool, _ := pgxpool.NewWithConfig(ctx, cfg)
for range 10 {
	wg.Go(func() { pool.Exec(ctx, "select pg_sleep(0.2)") })
}
// 另开一条监控连接，数 application_name 为 liao 的会话
```

```sh
$ go run ./scratch
默认 MaxConns: 10
服务端同时在跑的连接数: 3
10 条各 200ms 的查询，总耗时 800ms
```

默认值是 `max(4, NumCPU)`，本机 10 核所以是 10。上限压到 3 以后，服务端始终只有 3 个会话，10 条查询分四轮排队，总耗时 800 毫秒。

排队发生在客户端的池里，服务端不受影响。`database/sql` 的默认不限制，在 PG 上会直接撞 `max_connections`。生产环境要按"PG 的 `max_connections` 除以应用实例数"来规划 `MaxConns`。

## PG 类型

表设计就冲着 `database/sql` 的盲区去，见 `exercises/p12pgx/migrations/0001_articles.sql`：

```sql
CREATE TABLE articles (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    title      TEXT NOT NULL,
    tags       TEXT[] NOT NULL DEFAULT '{}',
    meta       JSONB NOT NULL DEFAULT '{}',
    views      BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

数组、jsonb 和带时区的时间，在 `database/sql` 里都要写胶水代码。用 pgx 版生成的代码试试：

```go
raw, _ := json.Marshal(map[string]any{"author": "alice", "words": 1200})
a, err := q.CreateArticle(ctx, db.CreateArticleParams{
	Title: "hello pgx",
	Tags:  []string{"go", "db"},
	Meta:  raw,
})
got, _ := q.GetArticle(ctx, a.ID)
fmt.Printf("Tags: %v (%T)\n", got.Tags, got.Tags)
fmt.Printf("Meta: %s (%T)\n", got.Meta, got.Meta)
var back map[string]any
json.Unmarshal(got.Meta, &back)
fmt.Println("author:", back["author"])
fmt.Printf("CreatedAt type: %T\n", got.CreatedAt)
```

```sh
$ go run ./scratch
Tags: [go db] ([]string)
Meta: {"words": 1200, "author": "alice"} ([]uint8)
author: alice
CreatedAt type: pgtype.Timestamptz
```

`Tags` 就是 `[]string`，Go 切片进，Go 切片出，没有一个字节的胶水。`Meta` 回来是 `[]byte`，再 `Unmarshal` 到你要的结构。注意 jsonb 会重排键，输出里 `words` 排在了 `author` 前面。

数组也能直接查。`@>` 是"包含"操作符，参数传 `[]string{"db"}`：

```go
tagged, _ := q.ListByTag(ctx, []string{"db"})
none, _ := q.ListByTag(ctx, []string{"rust"})
fmt.Println("ListByTag(db):", len(tagged))
fmt.Println("ListByTag(rust):", len(none))
```

```sh
ListByTag(db): 1
ListByTag(rust): 0
```

`@>` 能走 GIN 索引，这是"数组列也能走索引"的 PG 惯用法。练习 `TestPGTypes` 把这些都断言了一遍，`TestExecRowsAndTop` 考 `:execrows` 返回的影响行数和排序分页。

时间字段有两个选择。默认是 `pgtype.Timestamptz`，显式带 `Valid`，NULL 语义清楚。也可以在 `sqlc.yaml` 里加 override，映射成 `time.Time`，省掉 `Valid` 判断，代价是 NULL 变成零值。

生产建议保留 `pgtype`，NULL 显式比隐式好，这是 P9 的 NULL 三策略在这里的延伸。审读时实测过一个坑：`db_type: pg_catalog.timestamptz` 的写法在 sqlc v1.31.1 不生效，生成物仍是 `pgtype`。要真换成 `time.Time`，得用 column 级写法 `column: articles.created_at`，实测生效。

## 事务

P11 的 `WithTx` 纪律在 pgx 上是这样的形态：

```go
func withTxPgx(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)      // 注意：带 ctx
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
```

`Commit` 之后再调用 `Rollback` 不会撤销已提交的事务；pgx 会返回事务已关闭的错误，这里的 defer 有意忽略它。练习 `TestWithTxPgx` 先让 `fn` 返回错误，断言一行都没写进去，再让它成功，断言两行都在。

新坑来了：`Rollback(ctx)` 和 `Commit(ctx)` 都收 ctx。如果请求超时正好取消了 ctx，收尾本身会怎样？用两个事务对比：

```go
ctx, cancel := context.WithCancel(root)
tx, _ := pool.Begin(ctx)
_, err := tx.Exec(ctx, "insert into cancel_t values (1)")
cancel()
fmt.Println("Rollback(已取消的 ctx):", tx.Rollback(ctx))

ctx2, cancel2 := context.WithCancel(root)
tx2, _ := pool.Begin(ctx2)
tx2.Exec(ctx2, "insert into cancel_t values (2)")
cancel2()
fmt.Println("Rollback(WithoutCancel):", tx2.Rollback(context.WithoutCancel(ctx2)))
```

```sh
$ go run ./scratch
insert: <nil>
Rollback(已取消的 ctx): timeout: context already done: context canceled
直传 ctx 之后 服务端连接数 0
Rollback(WithoutCancel): <nil>
WithoutCancel 之后 服务端连接数 1
cancel_t 行数: 0
```

直传已取消的 ctx，`Rollback` 返回错误，pgx 随后把这条连接直接丢掉，服务端连接数变成 0。丢掉连接不会留下脏数据：体系背景里讲过，连接断开时服务端把没提交的事务回滚，最后 `cancel_t` 是 0 行。

代价是白白销毁了一条连接，下次要重新建，而 PG 的连接是一个进程。取消集中发生时，池要不断重连。

惯例是收尾不受取消影响，用 Go 1.21 的 `context.WithoutCancel`：

```go
defer tx.Rollback(context.WithoutCancel(ctx))
```

练习里为了对照 P11 保持了直传 ctx，能跑是因为测试的 ctx 活着。生产代码把 `WithoutCancel` 写上。`database/sql` 的收尾不带 ctx，所以没法传错。pgx 的收尾收 ctx，所以能传错。

## 批量写入

P9 的 `database/sql` 版只是顺序复用连接，每条语句仍是一次往返。`pgx.Batch` 是网络级流水线：N 条命令打包，一次发出去。

```go
batch := &pgx.Batch{}
for _, title := range titles {
	batch.Queue(`INSERT INTO articles (title, tags, meta) VALUES ($1, $2, $3)`,
		title, []string{"batch"}, []byte(`{}`))
}
br := pool.SendBatch(ctx, batch)
defer br.Close()
for range titles {
	if _, err := br.Exec(); err != nil {
		return err
	}
}
```

同一张表写 20 条，对比循环单发和 Batch。这是三次独立运行的第 2 轮，也就是连接预热之后的数字：

```sh
$ go run ./scratch
第 2 轮: 循环 20 次 91.8ms，Batch 2.51ms
第 2 轮: 循环 20 次 35.75ms，Batch 1.44ms
第 2 轮: 循环 20 次 42.82ms，Batch 2.04ms
```

循环 36-92 毫秒，Batch 1.4-2.5 毫秒。机器上同时在跑别的程序，循环的数字抖动很大，Batch 稳定得多。审读台账里的实测也一致：预热后 20 条 INSERT 约 2 毫秒（冷启动 4.04 毫秒，预热后 1.78 毫秒）。

数据量上万换 `COPY FROM`，也就是连接上的 `CopyFrom` 方法，快一个数量级，但只支持 INSERT，没有 RETURNING。

sqlc 生成的查询和 Batch 有个接缝：生成的 SQL 常量 `createArticle` 是小写未导出，跨包拿不到。要么像练习的 `batchInsert` 那样直接写位置参数 SQL，要么给查询加 `:copyfrom` 或 `batchexec` 注解，让 sqlc 直接生成批量方法。

## 三种 PG 工作流怎么选

`database/sql` 加任一驱动，适合要换库自由的场景，比如测试用 sqlite。代价是抽象层开销，PG 类型要写胶水。

pgx 原生手写，适合 PG 专属逻辑多或原型期。代价是手写 `Scan`，类型安全靠测试。

sqlc 加 pgx/v5 就是本章：锁定 PG 又要类型安全，代价只是多一个生成步骤，CI 里一行命令。`migrations/`、`queries/*.sql` 和 `sqlc.yaml` 三件套，`sqlc generate` 一次到位，`WithTx` 纪律跨 P11 和 P12 通用。

## 小结

1. PG 一个连接一个进程，连接池是必需品，`MaxConns` 要按 `max_connections` 规划。
2. `sql_package: "pgx/v5"` 一行换血，数组、jsonb、timestamptz 原生进出，胶水归零。
3. `pgx.Batch` 是真流水线，一次往返 N 条，对照 P9 的顺序复用连接。
4. pgx 的收尾带 ctx。传已取消的 ctx，收尾失败且连接被丢，用 `context.WithoutCancel`。
5. P9 手写、P11 生成加 sqlite、P12 生成加 PG 原生，同一套 `withTx` 纪律，实现层三级跳。

## 练习：p12pgx

起一个 PG（一次性容器），跑全绿，再看离线也绿：

```sh
docker run -d --rm --name p12pg -e POSTGRES_PASSWORD=p9 -e POSTGRES_DB=p12 \
  -p 127.0.0.1:15433:5432 postgres:17-alpine
```

```sh
$ P12_PG_DSN="postgres://postgres:p9@127.0.0.1:15433/p12?sslmode=disable" go test -race -count=1 -v ./exercises/p12pgx/
=== RUN   TestPGTypes
--- PASS: TestPGTypes (0.10s)
=== RUN   TestExecRowsAndTop
--- PASS: TestExecRowsAndTop (0.04s)
=== RUN   TestBatchInsert
--- PASS: TestBatchInsert (0.02s)
=== RUN   TestWithTxPgx
--- PASS: TestWithTxPgx (0.03s)
PASS
ok  	golearn/exercises/p12pgx	1.435s
```

不设 DSN 时，四个测试全部 SKIP，整体仍是 PASS：

```sh
$ go test ./exercises/p12pgx/
=== RUN   TestPGTypes
    p12pgx_test.go:78: P12_PG_DSN 未设置，跳过（go test 默认离线安全）
--- SKIP: TestPGTypes (0.00s)
...
PASS
```

这个门控模式值得抄到任何真库测试里：没有环境变量就跳过，离线 CI 照样绿。练习做完以后 `docker stop p12pg`。

四个测试各考一件事：

- `TestPGTypes`：`[]string` 数组、jsonb、timestamptz 和 `@>` 查询。
- `TestExecRowsAndTop`：`:execrows` 的影响行数加排序分页。
- `TestBatchInsert`：20 条 INSERT 一次往返。
- `TestWithTxPgx`：回滚坐实，加 `q.WithTx(tx)`。

**预测**，跑之前先写答案：

1. 不设 `P12_PG_DSN` 跑 `go test ./exercises/p12pgx/`，结果是 PASS、FAIL 还是 skip？为什么这么设计？
2. `CreateArticle` 传 `Tags: []string{"go", "db"}`，PG 侧存的是什么？读回来的 `got.Tags[0]` 是什么类型？
3. 请求 ctx 在 `fn(tx)` 执行中途超时取消，`defer tx.Rollback(ctx)` 会发生什么？改成哪种写法能免疫？

**观察**：把 `TestBatchInsert` 里的 `batchInsert` 换成循环单发，用 `-v` 对比两个版本的耗时。想看服务端日志，先 `ALTER SYSTEM SET log_statement = 'all'` 再 `select pg_reload_conf()`，然后 `docker logs -f p12pg`。

**改造**：给 `articles` 加一条 `GIN INDEX ON tags` 的迁移（0002），用 `EXPLAIN` 对比加索引前后 `ListByTag` 的执行计划。这是 PG 侧知识，工作流是 Go 的。

## 章末参考

### Rust 对照

| Rust | Go | 差异 |
|---|---|---|
| tokio-postgres 加 deadpool/bb8 | pgx 加 `pgxpool` | 池是库自带的一个类型 |
| sqlx 的宏做编译期检查 | sqlc 生成代码，CI 里检查 | 检查在生成步骤，不在编译器 |
| diesel 里数组操作要写 `sql("tags @> ")` | `ListByTag` 传 `[]string` | 查询在 `.sql` 文件里，类型随生成 |
| 事务在 `Drop` 时回滚 | 手写 `defer tx.Rollback(ctx)` | Go 靠纪律，不靠析构 |

### Java/Kotlin 与 TS/JS 类比

| 概念 | Java / Kotlin | TS / JS |
|---|---|---|
| `pgxpool` | HikariCP，默认 `maximumPoolSize` 10 | `pg.Pool`，默认 `max` 10 |
| 预备语句缓存 | `PreparedStatement` | `pg` 的命名查询 `name` |
| sqlc 生成代码 | jOOQ、MyBatis Generator | pgtyped |
| `text[]` 进出 | JDBC 要 `createArrayOf` | `pg` 自动解析数组 |
| `pgx.Batch` | JDBC `addBatch` 加 `executeBatch` | `pg` 无内建批量 |
| `Rollback(ctx)` | `Connection.rollback()` 无 ctx | `client.query('ROLLBACK')` |
| `WithTx` | Kotlin Exposed 的 `transaction {}` | Prisma 的 `$transaction` |

### 速查

- 离线门控：`P12_PG_DSN` 没设置就 `t.Skip`。
- 默认 `MaxConns` 是 `max(4, NumCPU)`，`database/sql` 默认不限制。
- 类型映射：`TEXT[]` 是 `[]string`，`JSONB` 是 `[]byte`，`TIMESTAMPTZ` 是 `pgtype.Timestamptz`，`BIGINT` 是 `int64`。
- 变异测试 `p12-rollback-commit` 与 `p12-top-order` 必须带 `P12_PG_DSN` 运行，否则测试 skip 会被误判为存活。
