# 12 数据库：连接池、事务纪律与 ORM 的取舍

> 详细程度：标准 · 约 20 分钟
> 材料：W02-2、C:go_basic/database、C:go_frame/orm、旧长版 `notes/p9-database.md` `notes/p11-sqlc-goose.md` `notes/p12-sqlc-pgx.md`
> 前置：10 分层与依赖注入

## 这一节要建立的认知

Go 访问数据库的核心是标准库 `database/sql`：它定义的是**一个连接池加一套统一接口**，具体数据库由驱动补全。所有上层方案（GORM、sqlc、sqlx、pgx 的标准接口）都站在它上面或绕开它。你要建立的是三个直觉：`*sql.DB` 是池不是连接；事务靠纪律而不是语法；SQL 和类型之间用什么方式对接，是团队取舍。

## 和你已知语言的差异

| 问题 | Java | Rust | Go |
|---|---|---|---|
| 连接池 | HikariCP 等，配置面大 | `sqlx::Pool` | `*sql.DB` 自带，旋钮少 |
| 事务 | `@Transactional` 声明式 | guard 离开作用域自动回滚 | 手动 `BeginTx`，靠 `defer Rollback` 兜底 |
| NULL | `Optional` 或包装类型 | `Option<T>` | `sql.Null*` 或指针，直接 Scan 进 `string` 会报错 |
| 类型安全 SQL | jOOQ / QueryDSL | sqlx 宏、diesel | sqlc 生成，或 GORM 链式 |

## 一、`*sql.DB` 是连接池，不是连接

`sql.Open` 只校验参数，不建立连接，连接错误要到第一次查询或 `Ping` 才暴露。〔C:go_basic/database/crud.go:14-48〕`*sql.DB` 线程安全，全局一份。池有三个旋钮：同时最多几条连接 `SetMaxOpenConns`、空闲时保留几条 `SetMaxIdleConns`、连接寿命 `SetConnMaxLifetime`（防止被负载均衡悄悄掐断）。

池满了之后，多出来的查询排队等连接，`db.Stats()` 里的 `WaitCount` 会记下来。这也是为什么 Go 里"一个请求一个 goroutine 直接查库"不会出问题：goroutine 便宜，池替你限流。

GORM 的 DSN 必须带 `charset=utf8mb4&parseTime=True&loc=Local`，否则 emoji 和 `time.Time` 出问题。它还有两个影响性能的开关：`PrepareStmt` 缓存预编译语句，`SkipDefaultTransaction` 关掉写操作的默认事务。〔C:go_frame/orm/gorm/connection.go:17-60〕

## 二、事务：靠纪律，不靠语法

Rust 的 guard 会在离开作用域时回滚，Spring 有注解。Go 都没有，所以**忘记 `Rollback`，连接就一直被占着，池耗尽，服务卡死**。纪律压缩成一个十行的助手函数，全项目只用它开事务：

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

〔实测〕在 go1.26.4 加 `modernc.org/sqlite` 内存库上，`fn` 里先扣 30 再返回错误，余额保持 100；`fn` 正常返回，余额变 70。`Commit` 和 `Rollback` 各只能执行一次，第二次调用会报错。〔C:go_basic/database/crud.go:50-72〕

几条配套规则：

1. 事务里的所有操作用 `tx`，不要混用 `db`，否则那部分不在事务里。
2. 事务要短。事务期间不要调外部服务、不要睡眠。
3. 隔离级别不要依赖默认值。PG 默认 read committed，MySQL InnoDB 默认 repeatable read，跨库迁移时要显式写出来。`BeginTx` 的第二个参数就是用来设它的。

## 三、NULL 与"没有这一行"

数据库列可以是 NULL，Go 的 `string` 没有这个状态。直接 `Scan` 进 `string`，会得到 `converting NULL to string is unsupported`。标准做法三种：`sql.NullString`（带 `Valid` 标志）、指针 `*string`、在 SQL 里 `COALESCE` 成默认值。

〔实测〕同一个 NULL 列：`Scan` 进 `sql.NullString` 得 `Valid=false`，进 `*string` 得 `nil`，进 `string` 报上面的错误。查不到行时 `QueryRow().Scan` 返回 `sql.ErrNoRows`，用 `errors.Is` 判断，不要把它当成故障。

## 四、三种对接 SQL 的方式

| 方式 | 适合 | 代价 |
|---|---|---|
| 原生 `database/sql` + `Scan` | 简单、要换库、测试用 sqlite | 手写 `Scan`，PG 类型要写胶水 |
| sqlc（SQL 先行，生成类型安全代码） | 锁定一种库，又要类型安全 | 多一个生成步骤，CI 一行命令 |
| GORM（结构体先行，链式 API） | 快速出活，常规 CRUD 多 | 大量接口收 `interface{}`，同一个方法传不同类型行为不同 |

GORM 的学习难点在于此：同参不同型行为不同，典型就是 `Updates`。`Updates(struct)` 只更新非零值，要把字段更新成零值必须用 `Updates(map)`；`Save` 则保存所有字段含零值。〔W02-2 p7〕〔C:go_frame/orm/gorm/update.go:10-33〕拿不准就查文档或写例子验证。

GORM 的几个值得记的约定：`First`/`Take` 查不到返回 `gorm.ErrRecordNotFound` 而 `Find` 不会；`Create` 必须传指针，才能回填主键；钩子按 `BeforeSave`、`BeforeCreate`、插入、`AfterCreate`、`AfterSave` 顺序执行，任一钩子返回错误就回滚。〔C:go_frame/orm/gorm/create.go:16-48〕〔C:go_frame/orm/gorm/create.go:73-103〕〔C:go_frame/orm/gorm/read.go:13-46〕

选哪个，课程的实际做法是 **GORM 作 dao 层的实现**，上面套 repository 抽象（见 10 节），这样换成 sqlc 或别的存储时，上层不动。〔W02-2 p14-15〕新项目从 SQL 出发的话，sqlc 加 goose 的组合更干净：`migrations/`、`queries/*.sql`、`sqlc.yaml` 三件套，`sqlc generate` 一步生成。

## 五、迁移：表结构是服务器状态，不在代码里

代码能随 `git checkout` 回到任意版本，表结构不行。办法是把每次变更写成带序号的迁移文件，数据库里留一张表记录"已经跑到第几个"，goose 就是这么做的。建表有两条路线：规范严格的公司走 SQL 评审（leader 到 DBA，重点看索引），中小企业用 `AutoMigrate`，新表都往一处注册。〔W02-2 p22-23〕

两条工具的坑值得记：

- sqlc 的注解 `-- name: Xxx :many` 紧邻长中文注释时，早期版本曾解析失败，当前版本（旧章在 sqlc v1.31.1 复现过）已不复现；生成器的解析器不是编译器，查询文件里的注释保留 ASCII 是便宜的防御。来源是旧章复盘记录，见 `notes/p11-sqlc-goose.md` 的翻车记档（1），资料原话里没有这条。
- goose 的迁移文件里，普通注释行中不要出现 `-- +goose`，会被当成真注解，报错是 `failed to parse annotation line`，不是"你的注释写错了"。来源是旧章复盘记录，见 `notes/p11-sqlc-goose.md` 的翻车记档（2），带完整复现命令。

## 常见坑

1. **把 `sql.Open` 当成连接成功**。要用 `PingContext` 验证。
2. **忘记 `rows.Close()`**，连接一直被占。查询用 `defer rows.Close()`，并检查 `rows.Err()`。
3. **事务里混用 `db` 和 `tx`**。
4. **GORM `Updates(struct)` 想把字段改成零值**，没改上。
5. **`DSN` 少了 `parseTime=True`**，时间字段扫不进 `time.Time`。
6. **内存库按连接隔离**：sqlite 的 `:memory:` 库要 `SetMaxOpenConns(1)`，否则并发测试里每个连接看到的是不同的库。
7. **`int(11)` 之类的显示宽度**在 MySQL 8.0.17 之后已废弃，教学 DDL 里的旧写法别照抄。〔C:go_basic/database/create_table.sql〕

## 判断题

1. 池的 `MaxOpenConns` 设成 2，同时发 6 个查询，会怎样？
2. `WithTx` 里 `defer tx.Rollback()` 在 `Commit` 成功后执行会发生什么？
3. 为什么 `Updates(struct{Age: 0})` 不会把年龄清零？
4. 事务里调用了一个远程 HTTP 接口，会有什么风险？

练习包：`exercises/p9db` 的 `TestCRUD`、`TestNullStrategies`、`TestTxAtomicity`、`TestContextCancel`、`TestPoolConcurrency`；`exercises/p11sqlm`（sqlc 生成代码、事务回滚、goose 升降级循环）；`exercises/p12pgx`（PG 原生类型、批量、事务，需要 PG）。

## 在项目里

这里没有现成例子。`dnsproxi-go` 是纯内存缓存，没有数据库。数据库的实际使用见 minifeed（25 节）和练习包 `exercises/p9db`。

## 想深入去哪查

- GORM：W02-2（CRUD、软删除、钩子、三层分层、密码加密与 BCrypt）、`C:go_frame/orm`（`PrepareStmt`、读写分离、钩子、索引提示）。
- 标准库：`C:go_basic/database`（`Exec`/`Query`/事务/批量插入/sqlbuilder）。
- 旧长版：`notes/p9-database.md`（连接池实测、隔离级别、pgx 原生）、`notes/p11-sqlc-goose.md`（迁移、sqlc 生成）、`notes/p12-sqlc-pgx.md`（PG 类型、批量写入）。
