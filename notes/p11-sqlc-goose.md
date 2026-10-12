# 第 11 章 · sqlc + goose：SQL 先行的类型安全数据层

> 这章学完你能干什么：把 P9 手写 `Scan` 的数据层换成"SQL 是真相源"的生成流水线，并且在翻车时能自己定位。终点是 `exercises/p11sqlm/` 的三条测试 `-race` 全绿：生成代码 CRUD、事务回滚、迁移 up/down 循环，全程零手写 `Scan`，零手写迁移执行逻辑。
> 读者：学过 P9 的 `database/sql`。前置：P9。后续：P12 把同一套工作流换成 PostgreSQL 和 pgx。
>
> 动手方式：本章要 `sqlc` 和 `goose` 两个命令行工具。例子在仓库根目录跑，数据库用 sqlite，不需要容器。

## 体系背景：迁移为什么要版本化

数据库的表结构是服务器上的状态，不在代码里。代码可以随 `git checkout` 回到任何版本，表结构不行。

两台机器、两个同事、一条上线流水线都要把表结构推到同一个形状。办法是把每次变更写成一个带序号的文件，数据库里留一张表记录"已经跑到第几个"。这就是迁移。

先看 goose 怎么做这件事。对一个空的 sqlite 文件执行：

```sh
$ goose -dir exercises/p11sqlm/migrations sqlite3 file:/tmp/p11.db up
OK   0001_users.sql (660.92µs)
OK   0002_age.sql (451.25µs)
goose: successfully migrated database to version: 2
$ sqlite3 /tmp/p11.db '.tables'
goose_db_version     users
```

输出里每行前面还有时间戳，这里略去。多出来的 `goose_db_version` 就是那张记录表：

```sh
$ sqlite3 -header /tmp/p11.db 'select version_id, is_applied from goose_db_version;'
version_id|is_applied
0|1
1|1
2|1
```

第二件事是失败怎么办。给 `0003_posts.sql` 故意写错第二条语句：

```sql
-- +goose Up
CREATE TABLE posts (id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL, title TEXT NOT NULL);
INSERT INTO posts (id, user_id, nope) VALUES (1, 1, 'x');
```

```sh
$ goose -dir exercises/p11sqlm/migrations sqlite3 file:/tmp/p11.db up
goose run: ERROR 0003_posts.sql: failed to run SQL migration: failed to execute SQL query "INSERT INTO posts (id, user_id, nope) VALUES (1, 1, 'x');": SQL logic error: table posts has no column named nope (1)
$ sqlite3 /tmp/p11.db '.tables'
goose_db_version     users
$ goose -dir exercises/p11sqlm/migrations sqlite3 file:/tmp/p11.db version
goose: version 2
```

第一条 `CREATE TABLE` 成功了，但 `posts` 表并没有留下来。goose 默认把每个迁移包在一个事务里，sqlite 和 PostgreSQL 的建表语句（DDL）都能回滚，所以失败的迁移是"全有或全无"。

MySQL 的 DDL 会隐式提交，没有这个保护。换数据库时要重新评估。

## 迁移文件

一个迁移文件分两个区：

```sql
-- +goose Up
ALTER TABLE users ADD COLUMN age INTEGER;

-- +goose Down
ALTER TABLE users DROP COLUMN age;
```

`up` 往前走，`down` 回退一层。上面的版本表告诉你它们怎么记账：

```sh
$ goose -dir exercises/p11sqlm/migrations sqlite3 file:/tmp/p11.db down
OK   0002_age.sql (1.37ms)
$ goose -dir exercises/p11sqlm/migrations sqlite3 file:/tmp/p11.db version
goose: version 1
$ goose -dir exercises/p11sqlm/migrations sqlite3 file:/tmp/p11.db up
OK   0002_age.sql (592.71µs)
goose: successfully migrated database to version: 2
```

CLI 的形态是 `goose DRIVER DBSTRING COMMAND`，驱动名和连接串必须前置，`-dir` 指向迁移目录。

有三条纪律：

1. **`Down` 必写。** 写不出回退方案的迁移，多半没想清楚。
2. **只加不改。** 要变表结构就新增 `0003_*.sql`。改已经跑过的 `0001`，别人本地的版本表就对不上了。
3. **文件名就是版本。** goose 按序号排序，版本号记在 `goose_db_version` 里。

Flyway 和 Liquibase 是 Java 世界里同一个角色，Flyway 的 `V1__init.sql` 加 `flyway_schema_history` 表和这里完全对应。JS 这边是 Knex 迁移和 Prisma Migrate。

## 版本号

文件名 `0001_users.sql` 和 `0002_age.sql` 的版本号是什么？不是时间戳，是序号：

```sh
$ goose -dir exercises/p11sqlm/migrations sqlite3 file:/tmp/p11.db version
goose: version 2
```

练习 `TestGooseDownAndVersion` 断言的就是这个值。goose 也支持时间戳命名，`goose create` 默认用时间戳，加 `-s` 才用序号：

```sh
$ goose -dir . create add_posts sql
Created new file: 20261012114340_add_posts.sql
$ goose -dir . -s create add_tags sql
Created new file: 00001_add_tags.sql
```

团队里选一种别混用。时间戳命名适合多人并行开分支，不容易撞号。序号命名读起来清楚，但两个人同时建 `0003` 就要手动改。

## 嵌入二进制

部署时不想带一堆 `.sql` 文件。`go:embed` 把它们编进二进制：

```go
//go:embed migrations/*.sql
var migrationsFS embed.FS

goose.SetBaseFS(migrationsFS)
goose.SetDialect("sqlite3")
goose.Up(db, "migrations")      // 进程启动时自迁移
```

这是 Go 服务单二进制部署的标准姿势。Flyway 默认从 classpath 读迁移，打进 jar 里，思路一致。

启动自迁移在多副本部署时有并发问题，要用锁保证只有一个实例在迁移。goose 自带锁的能力（`go doc github.com/pressly/goose/v3` 可查）。

## sqlc 生成

迁移管表结构，查询怎么办？把 SQL 写在 `.sql` 文件里，让 sqlc 生成 Go 代码。三件套分工如下：

- `migrations/NNNN_*.sql` 人写，goose 执行，sqlc 读它推导表结构。
- `queries/*.sql` 人写，带 `-- name: X :one` 注解，sqlc 为每条生成一个 Go 方法。
- `sqlc.yaml` 一次配置，是 sqlc 的入口。

Go 没有宏，没法像 Rust 的 sqlx 那样在编译期校验 SQL。sqlc 的办法是把检查挪到生成阶段：

```sh
$ sqlc generate
$ echo $?
0
```

成功时没有任何输出。我在临时目录里重新生成了一次，`db/` 下的三个文件和仓库里的逐字节一致，说明生成是确定性的，生成物可以提交进 git。

把查询里的列名写错，生成阶段就报错：

```sql
-- name: GetUser :one
SELECT id, emial, name, bio, age, created_at FROM users WHERE id = ?;
```

```sh
$ sqlc generate
# package db
exercises/p11sqlm/queries/users.sql:14:12: column "emial" does not exist
$ echo $?
1
```

表名写错、`WHERE` 里的列写错、`UPDATE` 和 `INSERT` 里的列写错，我都试了，全部报错。

但检查并不完备。把 `CountUsers` 改成 `SELECT COUNT(*) FROM users WHERE agee > 1;`，`sqlc generate`（v1.31.1，sqlite 引擎）退出码是 0，没有任何提示。所以 sqlc 的校验是辅助，涉及聚合的查询要配测试。

这和 jOOQ 的"从库表生成 Java 代码"思路相近，区别是 jOOQ 生成的是查询 DSL，sqlc 直接把你写的 SQL 包成方法。更接近的是 Kotlin 的 SQLDelight，或者 TS 里的 PgTyped：SQL 写在 `.sql` 文件里，生成带类型的函数。

## 注解和生成的方法

`queries/users.sql` 里的一条：

```sql
-- name: CreateUser :one
INSERT INTO users (email, name, bio, age) VALUES (?, ?, ?, ?)
RETURNING id, email, name, bio, age, created_at;
```

`sqlc generate` 之后，`db/users.sql.go` 里有这些方法签名：

```sh
$ grep -n "^func (q \*Queries)" exercises/p11sqlm/db/users.sql.go
18:func (q *Queries) CountUsers(ctx context.Context) (int64, error) {
54:func (q *Queries) CreateUser(ctx context.Context, arg CreateUserParams) (CreateUserRow, error) {
77:func (q *Queries) DeleteUser(ctx context.Context, id int64) (int64, error) {
98:func (q *Queries) GetUser(ctx context.Context, id int64) (GetUserRow, error) {
125:func (q *Queries) GetUserByEmail(ctx context.Context, email string) (GetUserByEmailRow, error) {
152:func (q *Queries) ListUsers(ctx context.Context) ([]ListUsersRow, error) {
195:func (q *Queries) ListUsersOlderThan(ctx context.Context, age sql.NullInt64) ([]ListUsersOlderThanRow, error) {
234:func (q *Queries) UpdateBio(ctx context.Context, arg UpdateBioParams) error {
```

参数被收进了 `CreateUserParams`，可空列映射成了 `sql.NullString` 和 `sql.NullInt64`。这是 P9 里 NULL 三种处理策略的第一种，sqlc 替你选了：

```go
type CreateUserParams struct {
	Email string         `json:"email"`
	Name  string         `json:"name"`
	Bio   sql.NullString `json:"bio"`
	Age   sql.NullInt64  `json:"age"`
}
```

方法名等于 `-- name:` 后面的名字，注解的类型后缀决定返回形态：

- `:one` 返回一行：`(Row, error)`。
- `:many` 返回多行：`([]Row, error)`。
- `:exec` 不关心结果：`error`。
- `:execrows` 要影响行数：`(int64, error)`，存在性检查用它。

看 `DeleteUser`，它的签名是 `(int64, error)`，因为注解是 `:execrows`。如果把它改成 `:exec`，生成会成功，但测试文件编译不过：

```sh
$ go vet ./exercises/p11sqlm/
vet: exercises/p11sqlm/p11sqlm_test.go:94:12: assignment mismatch: 2 variables but q.DeleteUser returns 1 value
```

生成代码的签名变了，调用方立刻在编译期暴露。这就是"类型安全来自生成器"的意思。

## 使用生成代码

下面这段程序把全部流程走一遍，没有任何手写的 SQL 字符串或 `Scan`：

```go
q := db.New(d)
created, err := q.CreateUser(ctx, db.CreateUserParams{
	Email: "a@x.io", Name: "alice",
	Bio: sql.NullString{String: "hi", Valid: true},
	Age: sql.NullInt64{Int64: 30, Valid: true},
})
u, _ := q.GetUser(ctx, created.ID)
_, err = q.GetUser(ctx, 999)
young, _ := q.ListUsersOlderThan(ctx, sql.NullInt64{Int64: 25, Valid: true})
_ = q.UpdateBio(ctx, db.UpdateBioParams{Bio: sql.NullString{}, ID: created.ID})
n, _ := q.DeleteUser(ctx, created.ID)    // 再删一次，n 变成 0
```

```sh
$ go run main.go
迁移到版本 2
CreateUser: id=1 email=a@x.io err=<nil>
GetUser: bio={String:hi Valid:true}
GetUser(999): sql: no rows in result set true
older than 25: 1
清空 bio 后: {String: Valid:false}
第一次删除影响行数: 1
第二次删除影响行数: 0
```

`ErrNoRows` 仍然在 `Scan` 那一层冒出来，生成代码不隐藏它。`errors.Is(err, sql.ErrNoRows)` 的判断和 P9 一样。

## 事务

生成的 `Queries` 带一个 `WithTx`，把同一套查询切到事务句柄上：

```go
tx, _ := d.BeginTx(ctx, nil)
qtx := q.WithTx(tx)
qtx.CreateUser(ctx, db.CreateUserParams{Email: "t@x.io", Name: "tx"})
inTx, _ := qtx.CountUsers(ctx)
tx.Rollback()
after, _ := q.CountUsers(ctx)
fmt.Println("事务内行数:", inTx, "回滚后行数:", after)
```

```sh
事务内行数: 1 回滚后行数: 0
```

练习的 `helpers.go` 里有 P9 同款的 `withTx` 助手：`defer tx.Rollback()` 保证任何出口都回滚，`Commit` 之后再回滚是无害的空操作。`TestWithTxGenerated` 验证回滚和提交两条路。

## 两个翻车

这章踩了三个坑。其中一个在当前版本里复现不了，我照实记录。

**（1）长中文注释让 sqlc 丢失注解。** 早期版本里，注解行紧邻上一行的长中文注释，会让 `-- name: ListUsersOlderThan :many` 被解析成没有类型后缀，报 `specifies parameter ":many" without containing a RETURNING clause`。

在 sqlc v1.31.1 里，我把同样措辞的 60 字节以上中文注释贴到 `GetUser` 上一行：

```sh
$ sqlc generate
$ echo $?
0
```

不复现。`queries/users.sql` 的注释保留 ASCII 作为防御，文件头第一行写了这条守则。教训是生成器的解析器不是编译器，别赌它对 Unicode 和版本鲁棒。

**（2）迁移注释里写了注解字面量。** 在 `0002_age.sql` 第一行加一句说明：

```sql
-- goose 的 SQL 迁移约定：文件名 NNNN_name.sql，-- +goose Up / Down 分区。
```

```sh
$ goose -dir exercises/p11sqlm/migrations sqlite3 file:/tmp/trap.db up
OK   0001_users.sql (441.67µs)
goose run: ERROR 0002_age.sql: failed to parse SQL migration file: failed to parse annotation line "-- goose 的 SQL 迁移约定：文件名 NNNN_name.sql，-- +goose Up / Down 分区。": "goose 的 SQL 迁移约定：文件名 NNNN_name.sql，  Up / Down 分区。" not supported: invalid annotation
```

句中的 `-- +goose` 被 goose 的扫描器当成真注解。报错说"注解解析失败"，不说"你的注释里写了注解"，定位靠二分和怀疑工具本身。迁移文件里不要在非注解行写出注解字面量。

**（3）版本号的语义。** 序号文件名取序号，不是时间戳。如果测试断言写 `20260101000000`，就红了。先跑一遍看真实值，再固化断言。

## 小结

1. SQL 先行：表在 migrations，查询在 queries，Go 代码全生成。校验靠 generate，不靠 `go build`。
2. goose 三纪律：`Down` 必写、只加不改、文件名即版本。失败的迁移在 sqlite 和 PostgreSQL 里整体回滚。
3. 注解即签名：`:one`、`:many`、`:exec`、`:execrows` 决定方法形态，改注解会让调用方编译不过。
4. sqlc 的校验不完备，聚合查询里的错列名它不一定拦，要配测试。
5. 生成器的解析器不是编译器。报错不指向真凶时，二分排查是基本功。

## 练习：p11sqlm

先重新生成并跑测试：

```sh
$ sqlc generate
$ go test -race -v ./exercises/p11sqlm/
--- PASS: TestGeneratedCRUD (0.01s)
--- PASS: TestWithTxGenerated (0.01s)
--- PASS: TestGooseDownAndVersion (0.02s)
PASS
ok  	golearn/exercises/p11sqlm	1.387s
```

三条测试各考一件事：

- `TestGeneratedCRUD`：四种注解的方法全走一遍，NULL 进出，`:execrows` 的影响行数。
- `TestWithTxGenerated`：`q.WithTx(tx)` 加回滚。
- `TestGooseDownAndVersion`：版本号语义，down/up 循环。

**预测**，先写答案再跑：

1. 把 `DeleteUser` 的注解从 `:execrows` 改成 `:exec`，生成的签名变成什么？测试里 `n != 1` 的断言会怎样？
2. 新建迁移 `0003_posts.sql` 后，`GetDBVersion` 返回几？`Down` 一层呢？
3. 在 `users.sql` 的 `-- name: GetUser :one` 上一行贴一条 60 字节的中文注释，`sqlc generate` 会成功吗？

第 3 题我在 v1.31.1 上实测过，会成功。

**观察**：用 CLI 看版本表，驱动名和连接串要写在前面：

```sh
goose -dir exercises/p11sqlm/migrations sqlite3 file:/tmp/p11.db status
```

**改造**：给 `users` 加 `posts` 表迁移 `0003`，写一条 `-- name: ListPostsByUser :many`，重新 `sqlc generate`，补一条测试。完整走一遍"加迁移、写查询、生成、测试"。

## 章末参考

### sqlc.yaml

全项目一份：

```yaml
version: '2'
sql:
  - engine: sqlite # sqlite/postgresql/mysql
    schema: exercises/p11sqlm/migrations # 从迁移里推表结构
    queries: exercises/p11sqlm/queries
    gen:
      go:
        package: db
        out: exercises/p11sqlm/db
        sql_package: database/sql # 想上 pgx 原生就换 "pgx/v5"
        emit_json_tags: true
```

生成代码是普通 `database/sql` 代码，P9 的知识完全复用：连接池、`QueryContext`、`ErrNoRows`、Null 类型都兼容。换成 `pgx/v5` 只改一行，P12 专章。

### queries 文件的现状

`exercises/p11sqlm/queries/users.sql` 的文件头注释是英文，ASCII 守则写在第一行。里面有 8 条查询：`CreateUser`、`GetUser`、`GetUserByEmail`、`ListUsers`、`ListUsersOlderThan`、`UpdateBio`、`DeleteUser`、`CountUsers`。

### 翻车复现记录

2026-10-12 审读复测：坑二在 goose v3.28.0 上复现，坑一在 sqlc v1.31.1 上两次构造都未复现。报错文本按历史记录转述，上游 issue 是否值得提，以复现为准。

### Rust 对照

| Rust | Go | 差异 |
|---|---|---|
| diesel `schema.rs` | sqlc 生成的 `models.go` | 都是生成的 |
| `diesel migration run` | `goose up` | 同一角色 |
| sqlx 宏校验 SQL | `sqlc generate` | 检查挪到生成阶段 |
| `NewUser` 结构体 | `CreateUserParams` | 参数被收成类型 |
| `.transaction(&mut conn)` | `q.WithTx(tx)` | 句柄切换 |
| `include_str!` | `go:embed` | 迁移进二进制 |

### Java/Kotlin 与 TS/JS 类比

| 概念 | Java / Kotlin | TS / JS |
|---|---|---|
| goose 迁移 | Flyway、Liquibase | Knex 迁移、Prisma Migrate |
| 版本记录表 | `flyway_schema_history` | `_prisma_migrations` |
| sqlc 生成 | jOOQ、MyBatis，Kotlin 的 SQLDelight | PgTyped |
| `go:embed` 迁移 | Flyway 读 classpath 资源 | 打包进 bundle |
| `sql.NullString` | `Optional`，Kotlin 的 `String?` | `string \| null` |
