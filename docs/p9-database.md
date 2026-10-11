---
title: P9 · 数据库：database/sql 心智模型 + pgx + 生态速查
---

# P9 · 数据库：database/sql 心智模型 + pgx + 生态速查


> 后端第二支柱。Rust 对照：diesel/sqlx/tokeneer，sqlx 最像
> （编译期校验 SQL 的是 sqlx-cli，Go 里对应 sqlc 代码生成，P11/P12 展开）。
> 练习：`exercises/p9db/`（sqlite 零依赖跑通全部模式，pgx 冒烟门控 `P9_PG_DSN`）。

## 这章学完你能干什么

读完你能对同事说清：为什么 `*sql.DB` 不是连接而是连接池、一个可空字段
为什么有三种NULL写法且各有归属、事务在 Go 里为什么靠纪律不靠类型系统、
什么时候该放弃 database/sql 直接上 pgx。终点证据：`exercises/p9db/`
5 个测试全绿（sqlite 零环境依赖），其中 `TestNullStrategies` 把三策略
各写一遍，`TestTxAtomicity` 用中途 panic 坐实回滚。四个问题依次是：

1. `*sql.DB` 为什么不是连接？
2. NULL 三策略怎么选？
3. 事务的 Begin/Commit/Rollback 纪律长什么样？
4. 什么时候绕过 database/sql 直接上 pgx？

## 问题 1：*sql.DB 为什么不是连接？

三个最常见的理解错误都源于把 `*sql.DB` 当成一根连接：

```go
db, err := sql.Open("pgx", dsn)  // ⚠️ Open 不建连接！只是配置结构体（≈ url::Url 不是 TcpStream）
db.SetMaxOpenConns(10)           // 连接池参数：diesel 没有，Go 显式管
db.SetMaxIdleConns(5)
db.SetConnMaxLifetime(time.Hour) // 连接寿命，防被 LB 悄悄掐死

row := db.QueryRowContext(ctx, "SELECT name, age FROM users WHERE id=$1", id)
// QueryRow 的坑：err 延迟到 Scan 才报，ErrNoRows 在 Scan 里出现
err = row.Scan(&name, &age)      // sql.ErrNoRows = 查空；≈ sqlx::query 的 Option<Row>
```

**心智模型三句话**：

1. `*sql.DB` 不是连接，是**连接池**：线程安全，全局一份（≈ sqlx::PgPool）。
   Open 只存配置，第一次查询才真建连接，所以连接错误要查过才知道。
2. 占位符 `$1, $2`（pg）/ `?`（sqlite）永远比字符串拼接好：
   注入防护 + 缓存执行计划。
3. 没有 async：QueryContext 只是"能被 ctx 取消"，调用本身阻塞
   goroutine。但 goroutine 便宜，per-request 一个 goroutine 天然并发
   （你 P3 懂的）。

QueryRow 的延迟坑值得单独记：`QueryRowContext` 本身不返回 ErrNoRows，
错误攒到 `Scan` 才冒出。写惯 sqlx 的人会想在 QueryRow 后面立刻判错，
那里判不到空结果。练习 `TestCRUD` 的断言就在 Scan 那行。

**context 谱系**：每个 API 都有 `Context` 后缀版本（`QueryContext`）。
**永远用带 ctx 的版本**：请求取消时数据库查询自动终止（≈ sqlx 的
`.fetch_optional(&mut *tx)` 带上 cancellation，但 Go 的粒度是整个查询）。
练习 `TestContextCancel` 用 50ms 超时杀长查询验证这件事。

## 问题 2：NULL 三策略怎么选？

Rust 的 `Option<String>` 在 Go 没有对应物，这是 Go 数据库代码最痛的
点。三条路，练习 `TestNullStrategies` 各写一遍：

```go
// 策略一：sql.NullXxx 类型（标准库原味）
var name sql.NullString
row.Scan(&name)
if name.Valid { use(name.String) } else { /* NULL */ }

// 策略二：指针（pgx 推荐）
var name *string
row.Scan(&name)  // NULL → nil；非 NULL → &"x"

// 策略三：COALESCE 把 NULL 消灭在 SQL 里（务实派首选）
row.Scan(&name)  // SELECT COALESCE(name, '') AS name ...
```

**选型**：

| 场景 | 用哪条 |
|---|---|
| 一次性脚本、报表查询 | COALESCE 最快 |
| 表结构里语义化的可空字段（如"删除时间"） | 指针 |
| 不想引指针语义的 struct 字段 | sql.NullXxx |

pgx 原生 API 直接支持 Go 指针和 `pgtype` 包（`pgtype.Text` 等），
比 database/sql 的 Null 类型表达力强（有 `Set/Assign` 语义）。P12 的
timestamptz override 选择会再回到这个分界（pgtype 显式 vs time.Time
省事）。

## 问题 3：事务的 Begin/Commit/Rollback 纪律长什么样？

```go
tx, err := db.BeginTx(ctx, nil)
if err != nil { return err }
defer func() {
    if p := recover(); p != nil {
        tx.Rollback() // panic 也要回滚
        panic(p)
    }
}()
if _, err := tx.ExecContext(ctx, "UPDATE accounts SET bal=bal-$1 WHERE id=$2", amt, from); err != nil {
    tx.Rollback() // 任何错误路径显式回滚
    return err
}
if err := tx.Commit(); err != nil { return err }
```

对照 sqlx：`let mut tx = pool.begin().await?; ...; tx.commit().await?`。
**Rust 靠类型系统保证 Drop 回滚，Go 靠纪律**（defer + 手写每条错误
路径）。这就是为什么 2026 年 Go 社区还是流行把事务包成函数，10 行
`WithTx` 换来 sqlx 的安全感：

```go
func WithTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
    tx, err := db.BeginTx(ctx, nil)
    if err != nil { return err }
    defer tx.Rollback() // Commit 后 Rollback 是幂等的 ErrTxDone，无害
    if err := fn(tx); err != nil { return err }
    return tx.Commit()
}
// 用法：WithTx(ctx, db, func(tx *sql.Tx) error { ... })
```

纪律要点两条：defer 的 Rollback 留着不删（Commit 之后它是无害的
ErrTxDone），每条错误路径显式回滚不指望 defer 及时性。练习
`TestTxAtomicity` 让 fn 中途 boom，断言余额没有半提交。

**Isolation Level**：`db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})`。
对照 diesel 的 `.isolation_level()`；PG 默认 ReadCommitted，
MySQL InnoDB 是 RepeatableRead，跨库要显式。

## 问题 4：什么时候绕过 database/sql 直接上 pgx？

```go
// 原生 API（推荐，绕过 database/sql 抽象层）
pool, err := pgxpool.New(ctx, dsn)
rows, err := pool.Query(ctx, "SELECT id, name FROM users WHERE age > $1", 18)
defer rows.Close()
for rows.Next() {
    var id int; var name string
    if err := rows.Scan(&id, &name); err != nil { return err }
}
if err := rows.Err(); err != nil { return err } // ⚠️ 迭代中发生的错误在这里冒出

// 批量（COPY 级性能，比循环 INSERT 快 10-100×）
batch := &pgx.Batch{}
for _, u := range users {
    batch.Queue("INSERT INTO users(id,name) VALUES($1,$2)", u.ID, u.Name)
}
br := pool.SendBatch(ctx, batch)
defer br.Close()
for range users { if _, err := br.Exec(); err != nil { ... } }

// 单行便捷
var name string
pool.QueryRow(ctx, "SELECT name FROM users WHERE id=$1", 42).Scan(&name)
```

判据一张表：

| | database/sql + pgx 驱动 | pgx 原生 |
|---|---|---|
| 何时用 | 要换库（sqlite 测试/MySQL 生产） | 锁定 PG |
| 性能 | 抽象层有开销（接口装箱） | 快 20-40%，支持 COPY/batch/listen |
| 类型 | NullXxx 三策略 | 指针 + pgtype 表达力强 |
| JSONB | 手 scan []byte | `pgtype.JSONB` 直接进 struct |

一句话：锁定 PG 且需要 PG 特有能力（数组、jsonb、COPY、listen）就上
原生；要换库自由就 database/sql。练习里的选择是 database/sql 写
（transportable：sqlite 跑测试、PG 跑生产是真实工程模式），pgx 冒烟
验证原生 API 长相。`rows.Err()` 的位置是 pgx 的常见漏检点：迭代中
发生的错误在循环结束后才冒出，练习 `p9pgx` 的注释锚了这条。

生态速查（sqlc/goose/sqlite 驱动怎么选）见章末参考。

## 动手：练习 p9db（45-60 分钟）

```sh
go test -race ./exercises/p9db/...    # 验收：5 tests 全绿（modernc sqlite，无需任何环境）
go test -race -run TestNullStrategies -v ./exercises/p9db/
```

**预测题**（跑之前写答案）：

1. `db.QueryRow("... WHERE id=$1", 999)` 返回的 row 本身带错误吗？
   ErrNoRows 从哪一行冒出？（§问题 1）
2. 表列是 `TEXT NULL`，三种策略读进 `name` 后各怎么判"是 NULL"？
   （§问题 2）
3. `WithTx` 里 `fn` 返回错误后不写 `tx.Rollback()`，只靠
   `defer tx.Rollback()`，事务会回滚吗？Commit 成功之后再走 defer
   呢？（§问题 3，ErrTxDone 幂等）
4. sqlite `:memory:` 加上默认连接池会发生什么？（章末参考的
   modernc 小节）

**观察验证**：

```sh
go test -race -v ./exercises/p9db/    # 看 TestContextCancel 的 50ms 超时杀查询
```

**pgx 冒烟**（可选，需 PG）：

```sh
docker run -d --rm --name p9pg -e POSTGRES_PASSWORD=p9 -e POSTGRES_DB=p9 \
  -p 127.0.0.1:15432:5432 postgres:17-alpine
P9_PG_DSN="postgres://postgres:p9@127.0.0.1:15432/p9?sslmode=disable" \
  go run ./exercises/p9db/p9pgx/       # batch 写入 + 指针 NULL 读取
docker stop p9pg
```

**环境坑（真实踩过）**：sqlite `:memory:` 每连接一份库，连接池会
"看不到表"，用 `file:x?mode=memory&cache=shared` + `SetMaxOpenConns(1)`。

| 练习 | 对应小节 | 检验什么 |
|---|---|---|
| TestCRUD | §问题 1 | 占位符、ErrNoRows 在 Scan 冒出 |
| TestNullStrategies | §问题 2 | 三策略全写一遍（COALESCE/指针/NullString） |
| TestTxAtomicity | §问题 3 | WithTx 助手：中途 boom → 回滚坐实 |
| TestContextCancel | §问题 1 | 50ms 超时杀长查询 |
| TestPoolConcurrency | §问题 1 | 池 + 并发 goroutine |

## Take-away

1. `*sql.DB` 是池不是连接；Open 不连，第一次查询才连，错误要查过
   才知道；QueryRow 的 ErrNoRows 延迟到 Scan 才冒。
2. NULL 三策略按场景选：COALESCE（SQL 消灭，脚本首选）/ 指针（语义
   化可空字段）/ NullXxx（struct 字段）；pgx 原生用指针最顺。
3. 事务靠纪律不靠类型：`defer tx.Rollback()` + 每条错误路径显式
   回滚，10 行 `WithTx` 助手换来 sqlx 的 Drop 安全感（Commit 后
   Rollback 是幂等的 ErrTxDone）。
4. 锁定 PG 就上 pgx 原生（快 20-40% + COPY/batch/listen）；要换库
   自由就 database/sql。sqlc + goose 是 2026 年标配（章末参考）。

---

## 章末参考

### 生态速查（别重复造轮子）

```sh
sqlc generate   # SQL → 类型安全 Go 代码。sqlx 的"编译期校验"在 Go 的对应物：
                # 你写 .sql 文件，生成带类型的方法（≈ diesel schema! 的手写版）
goose up        # 迁移工具（≈ diesel migration run）；sql-migrate/refract 也常用
sqlmock/sqlite  # 测试：练习里用 modernc.org/sqlite（纯 Go 无 CGO，git 直编）
```

**modernc.org/sqlite 的意义**：纯 Go 实现，`go test` 不需要 CGO 编译链，
对照 Rust 的 rusqlite（要 libsqlite3 系统库）。代价：比 mattn/go-sqlite3
慢 ~2×，但测试和单机场景无所谓。这也是练习预测题 4 的背景：
`:memory:` 是 per-connection 的，池里多个连接各拿一份空库。

### 与 sqlx 的完整对照

| database/sql | sqlx (Rust) |
|---|---|
| `sql.Open`（不连） | `PgPool::connect` |
| `QueryRowContext` + `Scan` | `query` + `fetch_one`（ErrNoRows 即时） |
| Null 三策略 | `Option<T>` |
| `WithTx` 手写 10 行 | `pool.begin()` Drop 保证 |
| `BeginTx(ctx, &sql.TxOptions{...})` | `.isolation_level(...)` |
