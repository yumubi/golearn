---
title: 第 11 章 · sqlc + goose：SQL 先行的类型安全数据层
---

# 第 11 章 · sqlc + goose：SQL 先行的类型安全数据层


> 教程章。读者：已学 P9 `database/sql` 手写 Scan 的人，现在升级到
> 2026 年 Go 社区标配：SQL 写在 .sql 文件里，sqlc 生成类型安全 Go 代码，
> goose 管迁移。Rust 对照：diesel（schema + 类型生成）+ diesel migration
> （版本化迁移），或 sqlx（宏里校验 SQL）。Go 没有宏，所以"编译期校验"
> 靠代码生成。

## 这章学完你能干什么

把 P9 手写 Scan 的数据层换成"SQL 是真相源"的生成流水线，并且能在
翻车时自己定位。终点：`exercises/p11sqlm/` 三条测试 -race 全绿：
生成代码 CRUD、事务回滚、迁移 up/down 循环，全程零手写 Scan、
零手写迁移 SQL 执行逻辑。

```sh
sqlc generate   # .sql 文件 → 类型安全的 Queries/Params/Row Go 代码
goose up        # 把 migrations/ 目录按序打到数据库
goose down      # 回退一层
```

四个问题依次展开：

1. 为什么 2026 年数据层标配是 SQL 先行？Go 没有宏，编译期校验从哪来？
2. goose 迁移的约定是什么？版本号语义有几个坑？
3. sqlc 的注解怎么映射成 Go 方法？四种注解各选在什么时候？
4. 两个真实翻车（sqlc 注释解析、goose 注解字面量）教会什么？

## 问题 1：为什么 2026 年数据层标配是 SQL 先行？

先看两端。手写端（P9）：`db.QueryRow` + 一串 `Scan(&id, &email, ...)`，
列序错位全靠测试抓，SQL 藏在字符串里没法校验。另一端是 sqlx/Rust 的
思路：SQL 写在代码里，靠宏或运行时反射做映射。Go 没有过程宏，这两条
路都堵死了。

Go 社区的答案：**SQL 留在 .sql 文件里当真相源，用代码生成补上编译期
校验**。三件套分工：

| 文件 | 谁写 | 给谁用 |
|---|---|---|
| `migrations/NNNN_*.sql` | 人写（Up + Down） | goose 执行；sqlc 读它推表结构 |
| `queries/*.sql` | 人写（带 `-- name: X :one` 注解） | sqlc 生成 Go 方法 |
| `sqlc.yaml` | 一次配置 | sqlc 的 makefile |

表结构在 migrations，查询在 queries，Go 代码全部生成。这和 diesel 一致
（`schema.rs` 也是生成的）；和 sqlx 相反（sqlx 在代码里内嵌 SQL 字符串）。
写错的列名、不匹配的类型，`sqlc generate` 当场报错：这就是 Go 版的
"编译期校验"，检查发生在 generate 而不是 go build。

## 问题 2：goose 迁移的约定和版本号语义是什么？

一个迁移文件长这样：

```sql
-- migrations/0001_users.sql
-- +goose Up
CREATE TABLE users (...);

-- +goose Down
DROP TABLE users;
```

三条纪律：

1. **Down 必写**。写不出回退方案的迁移 = 没想清楚。
2. **只加不改**。要变表结构就加 `0002_*.sql`，改 0001 是版本化自杀
   （别人本地已跑过旧 0001，你改了内容，版本表对不上）。
3. **文件名 = 版本**。`0001` `0002` 递增；goose 在 `goose_db_version`
   表里记版本号。

**版本号语义**（练习 `TestGooseDownAndVersion` 实测断言）：
文件名 `0001_users.sql`、`0002_age.sql`，`GetDBVersion` 返回的是
**序号 2**，不是时间戳（goose 也支持时间戳文件名，序号名取序号）。
Down 一层回到 1，再 Up 回到 2。

**进二进制**（部署时不用带 .sql 文件）：

```go
//go:embed migrations/*.sql
var migrationsFS embed.FS
goose.SetBaseFS(migrationsFS)
goose.Up(db, "migrations")  // 进程启动时自迁移，单二进制部署的标准姿势
```

对照 diesel：`diesel migration run` ≈ `goose up`；`embed` 对应
`include_str!`。进程启动自迁移是 Go 服务的流行做法（Rust 社区更偏
独立 migration 命令），多副本部署时要配 advisory lock（goose 自带）。

## 问题 3：sqlc 注解怎么映射成 Go 方法？

一条带注解的查询：

```sql
-- queries/users.sql
-- name: CreateUser :one
INSERT INTO users (email, name, bio, age) VALUES (?, ?, ?, ?)
RETURNING id, email, name, bio, age, created_at;
```

`sqlc generate` 产出：

```go
type CreateUserParams struct {
    Email string
    Name  string
    Bio   sql.NullString  // 可空列 → Null 类型（P9 的 NULL 三策略，sqlc 替你选了）
    Age   sql.NullInt64
}
type CreateUserRow struct { ID int64; Email string; ... }

func (q *Queries) CreateUser(ctx context.Context, arg CreateUserParams) (CreateUserRow, error)
```

映射规则三句话：

- `-- name: X`：查询名 = Go 方法名，`X` 的大小写原样保留。
- 类型后缀（`:one`/`:many`/`:exec`/`:execrows`）决定返回形态。
- 参数自动收进 `XxxParams` struct：可空列映射 Null 类型，非空列映射
  原生类型；单参数查询直接传值（如 `GetUser(ctx, id int64)`）。

**注解语义四选一**：

| 注解 | 生成方法形态 | 何时用 |
|---|---|---|
| `:one` | `(params) (Row, error)` | 单行返回（RETURNING/SELECT） |
| `:many` | `(params) ([]Row, error)` | 多行 |
| `:exec` | `(params) error` | 不关心结果 |
| `:execrows` | `(params) (int64, error)` | 要影响行数（存在性检查！） |

**生成代码怎么用**（全部流程，注意没有任何手写 SQL 字符串）：

```go
q := db.New(pool)
created, err := q.CreateUser(ctx, db.CreateUserParams{Email: "a@x.io", Name: "alice", ...})
u, err := q.GetUser(ctx, id)            // ErrNoRows 语义不变，仍在这一层
n, err := q.DeleteUser(ctx, id)          // n = 影响行数；再删一次 n==0
young, err := q.ListUsersOlderThan(ctx, sql.NullInt64{Int64: 25, Valid: true})
```

**事务**（P9 的 `WithTx` 助手直接接上）：

```go
err := withTx(ctx, pool, func(tx *sql.Tx) error {
    qtx := q.WithTx(tx)   // 生成代码自带：同一查询集换事务句柄
    if _, err := qtx.CreateUser(ctx, ...); err != nil { return err }
    return nil
})
```

对照 diesel：`CreateUserParams` ≈ `NewUser` struct + `diesel::insert_into`；
`q.WithTx(tx)` ≈ `.transaction(&mut conn)`。要点一致：类型系统保证你
不会把参数顺序搞错。手写 Scan 时代那个"列序错位全靠测试抓"的坑
（P9 没敢直说的），在这里被生成器焊死了。

## 问题 4：两个真实翻车教会什么？

这章实际踩了三个坑，全是真实的。生成器的用法查文档就会，这些坑
文档不会告诉你。

**坑一：queries 文件里的长中文注释让 sqlc 解析错位**。
`-- 命名查询：sqlc 的输入。每个带 name 注解的语句...` 这行 60+ 字节的
中文注释放在文件头，导致**下一条** `-- name: ListUsersOlderThan :many`
注解被解析成"没有类型后缀"，generate 报：
`specifies parameter ":many" without containing a RETURNING clause`。
二分排查（逐段删注释复测）定位到：**注解行的紧邻上一行中文注释过长时，
sqlc v1.31.1 的 scanner 会丢注解尾部 token**。
修法：queries 文件注释全 ASCII。教训：**生成器的解析器不是编译器，
别赌它对 Unicode 鲁棒**。

**坑二：迁移注释里写了 `-- goose` 字样**。
`-- goose 的 SQL 迁移约定：文件名 NNNN_name.sql，-- +goose Up / Down 分区。`
这行注释本身包含 `-- +goose`（在句中），goose 的注解扫描器把它当
真的注解解析：`failed to parse annotation`。
修法：**迁移文件里不要在非注解行写出注解字面量**。

**坑三：goose 版本号语义**（已并入问题 2）：序号文件名取序号，
不是时间戳。测试断言写 `20260101000000` 就红了。先跑一遍看真实值，
再固化断言。

两个翻车的共同形状：报错信息都不指向真凶（一个报 SQL 语法，一个报
注解解析），定位靠二分 + 怀疑生成器本身。这是用代码生成工具的基本功。

## 动手：练习 p11sqlm（30-45 分钟）

```sh
cd /Users/akka/Dev/learning/go
sqlc generate                                  # 改了 .sql 后重新生成
go test -race ./exercises/p11sqlm/             # 验收：3 tests 全绿
goose -dir exercises/p11sqlm/migrations status # 看版本表（需一个真实 sqlite 文件库）
```

**预测题**（跑之前写答案）：

1. 把 `DeleteUser` 的注解从 `:execrows` 改成 `:exec`，生成的签名和
   测试里 `n != 1` 的断言还成立吗？会报什么？（§问题 3）
2. 新建迁移 `0003_posts.sql` 后，`GetDBVersion` 返回几？Down 一层呢？
   （§问题 2）
3. 在 `users.sql` 的 `-- name: GetUser :one` 上一行贴一条 60 字节的
   中文注释，`sqlc generate` 会成功吗？报错长什么样？（§问题 4）

**观察验证**：

```sh
go test -race -v ./exercises/p11sqlm/          # 三条测试逐个看
```

| 练习 | 对应小节 | 检验什么 |
|---|---|---|
| TestGeneratedCRUD | §问题 3 | 四种注解方法全走一遍；Null 进出；execrows |
| TestWithTxGenerated | §问题 3 | q.WithTx(tx) + 回滚坐实 |
| TestGooseDownAndVersion | §问题 2 | 版本号语义 + down/up 循环 |

**改造题**（学有余力）：给 users 加 `posts` 表迁移（0003），写一条
`-- name: ListPostsByUser :many`，重新 generate，补一条测试：
完整走一遍"加迁移 → 写查询 → 生成 → 测试"的工作流。

## Take-away

1. **SQL 先行**：表在 migrations、查询在 queries，Go 全生成。
   Go 没有宏，编译期校验由 generate 补位：类型安全来自生成器，
   不来自运行时检查。
2. **goose 三纪律**：Down 必写、只加不改、文件名即版本（序号名取
   序号）；`embed` + 启动自迁移 = 单二进制部署的标准姿势。
3. **注解即签名**：`:one/:many/:exec/:execrows` 四选一决定方法形态；
   生成代码就是 database/sql 代码，P9 的池/ctx/NULL/ErrNoRows 全复用。
4. **生成器解析器不是编译器**：queries 文件注释保持 ASCII、迁移文件
   别乱写注解字面量；报错不指向真凶时，二分排查是基本功。

---

## 章末参考

### sqlc.yaml（全项目一份）

```yaml
version: '2'
sql:
  - engine: sqlite # sqlite/postgres/mysql
    schema: exercises/p11sqlm/migrations # sqlc 从迁移里推表结构
    queries: exercises/p11sqlm/queries
    gen:
      go:
        package: db
        out: exercises/p11sqlm/db
        sql_package: database/sql # 想上 pgx 原生就换 "pgx/v5"
        emit_json_tags: true # struct 带 json tag
```

生成代码是普通 database/sql 代码（P9 的知识 100% 复用）：
`sql.DB` 连接池、`QueryContext`、`ErrNoRows`、Null 类型全兼容。
要上 pgx 原生 API：`sql_package: "pgx/v5"` 一行换血（P12 专章）。

### queries 文件现行状态

`exercises/p11sqlm/queries/users.sql` 的文件头注释是英文（坑一的
教训固化在那）：8 条查询，CreateUser/GetUser/GetUserByEmail/
ListUsers/ListUsersOlderThan/UpdateBio/DeleteUser/CountUsers，
ASCII-only 注释守则写在文件头第一行。

### 翻车复现记录

坑一、坑二的具体报错文本、二分排查过程保留在 git history
（`exercises/p11sqlm/queries/users.sql` 文件头注释有英文存档）。
上游值得提 issue：sqlc scanner 对长 CJK 注释的处理。
