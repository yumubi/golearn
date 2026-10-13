---
title: P9 · 数据库：database/sql 心智模型 + pgx + 生态速查
---

# P9 · 数据库：database/sql 心智模型 + pgx + 生态速查


> 这章学完你能干什么：对同事说清 `*sql.DB` 为什么是连接池而不是连接，NULL 的三种读法各归谁，事务在 Go 里怎么靠纪律保证回滚，以及什么时候该绕过 `database/sql` 直接用 pgx。
> 读者：写过 sqlx、JDBC 或 node-postgres，第一次在 Go 里连数据库。前置：P3（ctx）、P2（接口和错误）。后续：P11 和 P12 把手写 SQL 换成 sqlc。
>
> 动手方式：开一个空目录，`go mod init demo`，`go get` 缺的包，每个例子存成 `main.go`，用 `go run main.go` 运行。片段省略了 `package main` 和 `import`。练习在 `exercises/p9db/`，用 sqlite 就能全绿，PG 部分用一个临时容器。

## 体系背景：每个连接一个进程

先起一个 PG，看它的进程：

```sh
$ docker run -d --rm --name p9pg -e POSTGRES_PASSWORD=p9 -e POSTGRES_DB=p9 \
    -p 127.0.0.1:15432:5432 postgres:17-alpine
$ docker exec p9pg ps -eo pid,args
PID   COMMAND
    1 postgres
   54 postgres: checkpointer
   55 postgres: background writer
   57 postgres: walwriter
   58 postgres: autovacuum launcher
   59 postgres: logical replication launcher
```

这些是后台进程。现在用 Go 开 3 条连接（`pgx.Connect` 三次，然后 sleep），再看一次：

```sh
$ docker exec p9pg ps -eo pid,args | grep "postgres:"
   54 postgres: checkpointer
   ...
   76 postgres: postgres p9 192.168.215.1(42048) idle
   77 postgres: postgres p9 192.168.215.1(42050) idle
   78 postgres: postgres p9 192.168.215.1(42062) idle
```

多出 3 个进程。PG 的模型是每条客户端连接 fork 一个服务进程，连接断开，进程退出。

进程不便宜：每个都有自己的内存，上下文切换也由内核承担。所以 PG 对连接数有硬上限：

```sh
$ docker exec p9pg psql -U postgres -d p9 -tAc "SHOW max_connections"
100
```

默认只给 100 条。一个 Web 服务每个请求开一条连接，几百个并发就打满了。所以连接池不是优化项，是必需品：几十个请求排队复用十来条连接。

Java 读者对此很熟，HikariCP 或 Druid 干的就是这件事。Node 的 `pg` 有 `Pool`。Go 把池直接做进了标准库，这就是 `*sql.DB`。

## 连接池

很多人第一次看到 `sql.Open` 会以为它连接了数据库。试试连一个不存在的端口：

```go
db, err := sql.Open("pgx", "postgres://postgres:p9@127.0.0.1:1/p9?sslmode=disable")
fmt.Println("Open 返回的 err:", err)
fmt.Println("Ping 返回的 err:", db.PingContext(ctx))
```

```sh
$ go run main.go
Open 返回的 err: <nil>
Ping 返回的 err: failed to connect to `user=postgres database=p9`: 127.0.0.1:1 (127.0.0.1): dial error: dial tcp 127.0.0.1:1: connect: connection refused
```

`Open` 只校验参数，不建连接。`*sql.DB` 是连接池，线程安全，全局一份，等价于 `sqlx::PgPool`、JDBC 的 `DataSource`、`pg.Pool`。连接错误要等到第一次查询或 `Ping` 才知道。

池有几个旋钮：

```go
db.SetMaxOpenConns(2)             // 同时最多几条连接
db.SetMaxIdleConns(2)             // 空闲时保留几条
db.SetConnMaxLifetime(time.Hour)  // 连接寿命，防被负载均衡悄悄掐断
```

把上限设成 2，同时发 6 个各 0.2 秒的查询，看池怎么排队：

```go
for range 6 {
	wg.Go(func() { db.ExecContext(ctx, "SELECT pg_sleep(0.2)") })
}
time.Sleep(100 * time.Millisecond)
s := db.Stats()
fmt.Printf("运行中: open=%d inUse=%d idle=%d wait=%d\n", s.OpenConnections, s.InUse, s.Idle, s.WaitCount)
```

```sh
$ go run main.go
运行中: open=2 inUse=2 idle=0 wait=4
结束后: open=2 inUse=0 idle=2 wait=4
6 个 0.2s 查询共耗时 0.6s
```

2 条连接，6 个查询，分三批跑完，共 0.6 秒。多出来的 4 个排队等连接，`wait=4` 记下了这件事。

这也是 goroutine 在这里不会出问题的原因。`QueryContext` 只是"能被 ctx 取消"，调用本身阻塞当前 goroutine。goroutine 便宜，一个请求一个 goroutine，池帮你限流。练习 `TestPoolConcurrency` 用 4 条连接跑 20 个并发查询。

## 查询与占位符

查一行用 `QueryRowContext`，再 `Scan`。占位符 PG 写 `$1`、`$2`，sqlite 的 `?` 也行，永远不要拼字符串：

```go
row := db.QueryRowContext(ctx, "SELECT name FROM users WHERE id=$1", 999)
fmt.Println("QueryRowContext 之后还没有错误，row 是:", row != nil)
var name string
err := row.Scan(&name)
fmt.Println("Scan 之后:", err, errors.Is(err, sql.ErrNoRows))
```

```sh
$ go run main.go
QueryRowContext 之后还没有错误，row 是: true
Scan 之后: sql: no rows in result set true
```

查不到数据时，错误要等到 `Scan` 才冒出来，`QueryRowContext` 本身永远不报。写惯 sqlx 的人会想在 QueryRow 后立刻判错，那里判不到。练习 `TestCRUD` 的断言就在 `Scan` 那一行。

占位符还有两个好处：防注入，以及服务端可以复用执行计划。Java 读者把它当 `PreparedStatement` 的 `?` 就行。

每个 API 都有带 `Context` 后缀的版本，永远用带 ctx 的那个。看看 ctx 超时对 PG 做了什么：

```go
ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
defer cancel()
_, err := db.ExecContext(ctx, "SELECT pg_sleep(5)")
fmt.Println("err:", err)
```

```sh
$ go run main.go
err: timeout: context deadline exceeded
耗时 < 1s: true
```

查询要 5 秒，50 毫秒就被取消了，驱动会通知服务端终止它。Java 的做法是 `Statement.setQueryTimeout`，Node 的 `pg` 有 `query_timeout` 配置，Go 把它统一成 ctx。练习 `TestContextCancel` 用 sqlite 的递归 CTE 造了一个长查询，同样 50 毫秒杀掉。

## NULL

数据库列可以是 NULL，Go 的 `string` 没有这个状态。直接读会怎样：

```go
var s string
err := db.QueryRowContext(ctx, "SELECT name FROM users WHERE id=1").Scan(&s)
```

```sh
sql: Scan error on column index 0, name "name": converting NULL to string is unsupported
```

Rust 有 `Option<String>`，Kotlin 有 `String?`，TS 有 `string | null`，Go 没有对应物。标准做法有三种，下面各读一次 NULL 行（id=1）和非 NULL 行（id=2）：

```go
var ns sql.NullString                      // 策略一：标准库的 Null 类型
db.QueryRowContext(ctx, q, id).Scan(&ns)   // 看 ns.Valid

var p *string                              // 策略二：指针
db.QueryRowContext(ctx, q, id).Scan(&p)    // NULL 得到 nil

var s string                               // 策略三：COALESCE 在 SQL 里消灭 NULL
db.QueryRowContext(ctx, "SELECT COALESCE(name, '') FROM users WHERE id=$1", id).Scan(&s)
```

```sh
$ go run main.go
id=1 NullString: Valid=false String=""
id=1 指针: nil=true
id=1 COALESCE: ""
id=1 Null[string]: Valid=false
id=2 NullString: Valid=true String="bob"
id=2 指针: nil=false
id=2 COALESCE: "bob"
id=2 Null[string]: Valid=true
```

最后一行是 Go 1.22 加的泛型版 `sql.Null[string]`，不用再区分 `NullString` 和 `NullInt64`。

怎么选？一次性脚本和报表用 COALESCE，最快。表结构里有语义的可空字段，比如"删除时间"，用指针。不想引入指针的 struct 字段用 `sql.NullString`（或 Go 1.22+ 的 `sql.Null[string]`）。练习 `TestNullStrategies` 把三种各写一遍。

pgx 原生接口直接支持指针，还有 `pgtype.Text` 这类类型，表达力比 `database/sql` 的 Null 类型强。P12 的 timestamptz 取舍会回到这条分界。

## 事务

事务要保证：中途出错，前面改的全部撤销。先看没有任何保护的写法为什么危险：

```go
tx, err := db.BeginTx(ctx, nil)
if err != nil { return err }
tx.ExecContext(ctx, "UPDATE accounts SET bal = bal - 30 WHERE id = 1")
// 这里出错返回了，忘了 Rollback，事务一直挂着
```

事务没结束，连接就一直被占着。池里的连接用光，整个服务卡死。Rust 靠 `Drop` 自动回滚，Kotlin 的 `transaction {}` 和 Spring 的 `@Transactional` 靠块或注解。Go 没有这些，要靠纪律。

纪律浓缩成一个 10 行的助手函数：

```go
func WithTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() // Commit 之后再 Rollback 只会返回 ErrTxDone，无害
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
```

让 `fn` 先扣 30 再报错，看余额：

```sh
$ go run main.go
返回 error 版: boom 余额: 100
recover 接到: boom-panic 余额: 100
成功版: <nil> 余额: 70
```

第一行 `fn` 返回 error，第二行 `fn` 直接 panic，余额都是 100，说明回滚了。`defer tx.Rollback()` 在 panic 展开时照样会跑，panic 本身没有被 `WithTx` 吞掉，仍然会继续上抛。要不要在 `WithTx` 里 `recover` 是设计取舍，库代码通常不吞。

第三行成功提交，余额变 70。`defer` 里的 `Rollback` 此时返回 `ErrTxDone`，什么也不会发生。练习 `TestTxAtomicity` 让 `fn` 中途返回 `boom`，断言没有半提交。

## 隔离级别

`BeginTx` 的第二个参数可以指定隔离级别：

```go
for _, o := range []*sql.TxOptions{nil, {Isolation: sql.LevelSerializable}} {
	tx, _ := db.BeginTx(ctx, o)
	tx.QueryRowContext(ctx, "SHOW transaction_isolation").Scan(&lvl)
	fmt.Println(lvl)
	tx.Rollback()
}
```

```sh
$ go run main.go
read committed
serializable
```

不传选项，PG 默认是 `read committed`。MySQL InnoDB 默认是 `repeatable read`，跨库迁移时要显式写出来。Java 对应 `Connection.setTransactionIsolation`。

## pgx 原生接口

`database/sql` 是一层通用抽象。锁定 PG 的话，可以直接用 pgx 的原生接口，少一层装箱，还能用 PG 特有的功能。

```go
pool, err := pgxpool.New(ctx, dsn)
rows, err := pool.Query(ctx, "SELECT id, name, nick FROM demo_users WHERE id < 3 ORDER BY id")
defer rows.Close()
for rows.Next() {
	var nick *string                     // NULL 直接进指针
	if err := rows.Scan(&id, &name, &nick); err != nil { return err }
}
if err := rows.Err(); err != nil { return err }   // 迭代中的错误在这里冒出
```

```sh
$ go run main.go
0 u0 true
1 u1 true
2 u2 true
```

`rows.Err()` 是常见的漏检点：迭代中途出的错误，要循环结束后才看得到。

原生接口还有 batch。同样插 200 行，先逐条等响应，再用 `pgx.Batch` 一次发完：

```sh
$ go run main.go
循环 200 条: 927ms
批量 200 条: 20ms
```

这是本机容器上的实测，走回环网卡，仍有这个差距。循环里每条都是一次网络往返加一次独立提交，batch 一次往返发完，服务端按一个隐式事务执行。数字随机器变化，量级的结论不变。

练习里的 `p9pgx` 把这些做成了一个冒烟程序，设置 `P9_PG_DSN` 才会连库：

```sh
$ P9_PG_DSN="postgres://postgres:p9@127.0.0.1:15432/p9?sslmode=disable" \
    go run ./exercises/p9db/p9pgx/
pgx 冒烟 OK：5 行 batch 写入 + 指针 NULL 读取全部正确
$ go run ./exercises/p9db/p9pgx/
P9_PG_DSN 未设置，跳过 pgx 冒烟（这是设计行为）
```

选哪个？锁定 PG 且要用数组、jsonb、COPY、LISTEN 这类特有能力，就上原生。要保留换库的自由，比如 sqlite 跑测试、PG 跑生产，就用 `database/sql`。pgx README 的原话是 "The pgx interface is faster"，没有给具体百分比。

## sqlite 内存库的坑

练习用 modernc.org/sqlite，纯 Go 实现，`go test` 不需要 CGO 工具链。对照 Rust 的 rusqlite 要系统装 libsqlite3。代价是比 mattn/go-sqlite3 慢约 2 倍（社区经验数字），测试和单机场景无所谓。

但 `:memory:` 有个坑。池里开两条连接试试：

```go
db, _ := sql.Open("sqlite", ":memory:")
db.SetMaxOpenConns(2)
c1, _ := db.Conn(ctx)
c2, _ := db.Conn(ctx)
_, err := c1.ExecContext(ctx, "CREATE TABLE t(x)")
fmt.Println("连接 1 建表:", err)
_, err = c2.ExecContext(ctx, "INSERT INTO t VALUES (1)")
fmt.Println("连接 2 插入:", err)
```

```sh
$ go run main.go
连接 1 建表: <nil>
连接 2 插入: SQL logic error: no such table: t (1)
```

每条连接各有一份独立的空库。正确姿势是 `file:x?mode=memory&cache=shared` 加 `SetMaxOpenConns(1)`，`openTestDB` 就是这么写的。

## 小结

1. PG 每条连接一个进程，默认上限 100，所以连接池是必需品。
2. `*sql.DB` 是池不是连接，`Open` 不建连接，错误要查过才知道。
3. `QueryRow` 的 `ErrNoRows` 延迟到 `Scan` 才冒出，永远用带 ctx 的版本。
4. NULL 三种读法：COALESCE、指针、`sql.NullString`/`sql.Null[string]`，按场景选。
5. 事务靠纪律：`WithTx` 加 `defer tx.Rollback()`，panic 也会回滚。
6. 锁定 PG 用 pgx 原生，要换库用 `database/sql`。

## 练习：p9db

先跑一遍，sqlite 零环境依赖：

```sh
$ go test -race -v ./exercises/p9db/...
--- PASS: TestCRUD (0.01s)
--- PASS: TestNullStrategies (0.00s)
--- PASS: TestTxAtomicity (0.01s)
--- PASS: TestContextCancel (0.05s)
--- PASS: TestPoolConcurrency (0.01s)
ok  	golearn/exercises/p9db	1.405s
```

五个测试各考一件事：

- `TestCRUD`：占位符，`ErrNoRows` 在 `Scan` 冒出。
- `TestNullStrategies`：三种 NULL 读法各写一遍。
- `TestTxAtomicity`：`WithTx` 中途 boom，回滚坐实。
- `TestContextCancel`：50 毫秒超时杀长查询。
- `TestPoolConcurrency`：池加并发 goroutine。

**预测**，先写答案再跑：

1. `db.QueryRow("... WHERE id=$1", 999)` 返回的 row 本身带错误吗？`ErrNoRows` 从哪一行冒出？
2. 表列是 `TEXT NULL`，三种策略读进 `name` 后，各怎么判断"是 NULL"？
3. `WithTx` 里 `fn` 返回错误后不写 `tx.Rollback()`，只靠 `defer`，事务会回滚吗？Commit 成功之后再走 `defer` 呢？
4. sqlite 的 `:memory:` 加上默认连接池会发生什么？

**观察**：

```sh
go test -race -v ./exercises/p9db/    # 看 TestContextCancel 的 50ms 超时
```

PG 部分用上面的 `docker run` 起容器，跑 `p9pgx`，用完执行 `docker stop p9pg`。

**改造**：

1. 给 `WithTx` 写一个 panic 版测试：`fn` 里先 INSERT 再 `panic("boom")`，用 `recover` 接住后查行数。预测：事务回滚吗？panic 还会继续上抛吗？上面的实测是回滚，panic 不被吞。要不要在 `WithTx` 里 `recover` 是取舍，写下你的理由。
2. 在 `TestNullStrategies` 里加第四种读法 `sql.Null[string]`，读 `SELECT NULL`，断言 `Valid == false`。

## 章末参考

### 生态速查

```sh
sqlc generate   # SQL 生成类型安全的 Go 代码，对应 sqlx 的编译期校验
goose up        # 迁移工具，对应 diesel migration run
```

sqlc 和 goose 的用法见 P11 和 P12。

### 与 sqlx 的对照

| database/sql | sqlx（Rust） |
|---|---|
| `sql.Open`（不连） | `PgPool::connect` |
| `QueryRowContext` 加 `Scan` | `query` 加 `fetch_one`，ErrNoRows 即时 |
| NULL 三策略 | `Option<T>` |
| 手写 10 行 `WithTx` | `pool.begin()`，`Drop` 保证 |
| `BeginTx(ctx, &sql.TxOptions{...})` | `.isolation_level(...)` |

### Java/Kotlin 与 TS/JS 类比

| 概念 | Java / Kotlin | TS / JS |
|---|---|---|
| `*sql.DB` | `DataSource`，HikariCP | `pg.Pool` |
| 占位符 `$1` | `PreparedStatement` 的 `?` | `pool.query(sql, [args])` |
| NULL 指针 | `Integer`、Kotlin `String?` | `string \| null` |
| `WithTx` | Spring `@Transactional`，Exposed `transaction {}` | Prisma `$transaction` |
| ctx 超时 | `setQueryTimeout` | `query_timeout` 配置 |
| 隔离级别 | `setTransactionIsolation` | `BEGIN ISOLATION LEVEL ...` |
