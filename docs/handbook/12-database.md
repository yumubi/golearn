---
title: 12 数据库：连接池、事务纪律与 ORM 的取舍
---

# 12 数据库：连接池、事务纪律与 ORM 的取舍


> 详细程度：标准 · 约 50 分钟
> 材料：W02-2、W16-1、C:go_basic/database、C:go_frame/orm、旧长版 `notes/p9-database.md` `notes/p11-sqlc-goose.md` `notes/p12-sqlc-pgx.md`
> 前置：10 分层与依赖注入

## 先看一个"连上了"的假象

下面这段程序先对一个**根本不存在的路径**调用 `sql.Open`，它不报错；再用内存库走一遍建表、插入、查询。先自己猜一下每一行输出，再往下读：

```go
package main

import (
	"database/sql"
	"errors"
	"fmt"

	_ "modernc.org/sqlite" // 匿名导入：驱动注册进 database/sql
)

func main() {
	bad, err := sql.Open("sqlite", "file:/no/such/dir/x.db?mode=ro")
	fmt.Println("坏路径 Open 报错吗:", err != nil)
	fmt.Println("坏路径 Ping 报错吗:", bad.Ping() != nil)
	bad.Close()

	db, err := sql.Open("sqlite", "file:l12s1?mode=memory&cache=shared")
	if err != nil {
		panic(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE users(id INTEGER PRIMARY KEY, name TEXT)"); err != nil {
		panic(err)
	}
	if _, err := db.Exec("INSERT INTO users(id, name) VALUES ($1, $2)", 1, "alice"); err != nil {
		panic(err)
	}
	var name string
	err = db.QueryRow("SELECT name FROM users WHERE id = $1", 1).Scan(&name)
	fmt.Println("查到:", name, err == nil)
	err = db.QueryRow("SELECT name FROM users WHERE id = $1", 99).Scan(&name)
	fmt.Println("查不到:", errors.Is(err, sql.ErrNoRows))
}
```

```text
坏路径 Open 报错吗: false
坏路径 Ping 报错吗: true
查到: alice true
查不到: true
```

前两行才是真正的谜面：路径根本不存在，`Open` 返回的 `err` 却是 `nil`，直到 `Ping` 才暴露问题。后面三行是内存库上的建表、查到和查不到。要解释这些现象，得先建立 Go 数据库访问的心智模型：`database/sql` 不是驱动，也不是某家数据库的客户端，它是**一个连接池加一套统一接口**，具体怎么跟 MySQL、PostgreSQL、sqlite 说话，由驱动补全。所有上层方案（GORM、sqlc、sqlx、pgx 的标准接口模式）都站在它上面，或者干脆绕开它直接说数据库协议。这一节就沿这条线走：先看清池，再守住事务纪律，最后决定 SQL 和 Go 类型之间用什么方式对接。

## 和你已知语言的差异

| 问题 | Java | Rust | Go |
|---|---|---|---|
| 连接池 | HikariCP 等，配置面大 | `sqlx::Pool` | `*sql.DB` 自带，旋钮少 |
| 事务 | `@Transactional` 声明式 | guard 离开作用域自动回滚 | 手动 `BeginTx`，靠 `defer Rollback` 兜底 |
| NULL | `Optional` 或包装类型 | `Option<T>` | `sql.Null*` 或指针，直接 Scan 进 `string` 会报错 |
| 类型安全 SQL | jOOQ / QueryDSL | sqlx 宏、diesel | sqlc 生成，或 GORM 链式 |

表里最陌生的是第二行。Rust 的 `Transaction` 实现了 `Drop`，离开作用域没提交就自动回滚；Spring 用注解把事务边界声明在方法上。Go 两样都没有，事务正确性完全靠纪律，这是下面第二节的主题。

## 一、`*sql.DB` 是池，不是连接

先回答开头的谜面。`sql.Open(driverName, dataSourceName)` 只按驱动名找到驱动、记下连接串，不保证建立任何连接（具体驱动可能在这一步解析 DSN，但不会去连服务端），所以连一个不存在的地址也照样"成功"，开头示例的前两行就是证据。课程的教学代码把 `sql.Open` 和连接串放在 `db_test.go`。〔C:go_basic/database/db_test.go:24-26〕连接错误要等到第一次真正用连接的操作（`Exec`、`Query`、或者显式的 `PingContext`）才冒出来。所以生产代码的正确姿势是启动时 `db.PingContext(ctx)` 一次，把配置错误在启动阶段暴露，而不是等第一个请求进来才炸。

第二个推论：`*sql.DB` 是连接池，不是一根连接。它是并发安全的，全局建一份就够，到处传着用；给每个请求 `sql.Open` 一次是新手常见的错误，既浪费，还丢掉了池的复用。池有三个旋钮：〔C:go_frame/orm/gorm/connection.go:52-60〕

1. `SetMaxOpenConns(n)`：同时最多开几条连接，超出的查询排队等；
2. `SetMaxIdleConns(n)`：空闲时保留几条，多了关掉，下次请求不用重新握手；
3. `SetConnMaxLifetime(d)`：一条连接最长用多久就换新，防止被数据库或它前面的负载均衡悄悄掐断。

池满了之后会发生什么？直接跑给你看。下面把池上限压到 2，同时发 6 个同样的慢查询，并先单独测一次它的耗时：

```go
package main

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

func main() {
	db, _ := sql.Open("sqlite", "file:l12s3?mode=memory&cache=shared")
	defer db.Close()
	db.SetMaxOpenConns(2) // 池上限 2，第 3 个查询必须等
	db.Exec("CREATE TABLE t(x)")
	slow := func() {
		_, _ = db.ExecContext(context.Background(), // 递归 CTE 模拟一个慢查询
			"WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c WHERE x < 3000000) SELECT SUM(x) FROM c")
	}
	t0 := time.Now()
	slow()
	one := time.Since(t0) // 单个查询的耗时
	start := time.Now()
	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slow()
		}()
	}
	wg.Wait()
	fmt.Println("总耗时超过单个查询的 2 倍:", time.Since(start) > 2*one)
	s := db.Stats()
	fmt.Printf("结束后: open=%d idle=%d wait=%d\n", s.OpenConnections, s.Idle, s.WaitCount)
}
```

```text
总耗时超过单个查询的 2 倍: true
结束后: open=2 idle=2 wait=4
```

6 个查询、2 条连接，分三批跑完，总耗时是单个查询的两倍以上（具体秒数随机器波动，所以示例只打印布尔值）。`db.Stats()` 里的 `WaitCount` 记下了有 4 个查询排队等过连接。〔实测〕`go1.26.4` 加 `modernc.org/sqlite`，示例由检查脚本实际运行过。这个排队机制就是 Go 并发模型下"一个请求一个 goroutine 直接查库"不出事的原因：goroutine 非常便宜，来一千个请求就开一千个 goroutine，但真正同时压到数据库上的连接数被池的上限钳住，多出来的 goroutine 在池前排队。池在这里同时是限流器。反过来也要记住它的代价：上限设得太小，吞吐就卡在池上（上面总耗时超过单个查询的两倍就是证据）；设得太大，对 PostgreSQL 这类"一条连接一个进程"的数据库，实例一多就撞服务端的 `max_connections`。PG 的默认上限是 100，生产环境要按"max_connections 除以应用实例数"来规划 `MaxConns`。

驱动用之前的另一个坑也要交代：驱动必须**匿名导入**。`database/sql` 通过注册表找驱动，`import _ "modernc.org/sqlite"` 的下划线表示只用它的 `init()` 注册，不直接用包里的任何名字。忘了这一行，编译照样通过，`sql.Open` 运行时才报 `sql: unknown driver "sqlite" (forgotten import?)`。

> 〔W02-2 p7〕GORM 的很多接口都是接收 interface 的，根据你传入不同类型的参数，会执行不同的行为。……虽然 GORM 我用过很多次，但是我还是觉得这些方法语义过于模糊，经常写错。

GORM 在 `*sql.DB` 之上又包了一层，先看它怎么开连接。它的 DSN 必须带 `charset=utf8mb4&parseTime=True&loc=Local`：〔C:go_frame/orm/gorm/connection.go:17-21〕`utf8mb4` 兼容 emoji，`parseTime=True` 才能把数据库的时间列扫进 `time.Time`，少了它，MySQL 驱动把时间列当 `[]byte` 交出来，扫进 `time.Time` 会报错。上面引的那段话点出了 GORM 的学习难点：大量接口收 `interface{}`，同一个方法传不同类型行为不同，典型就是 `Updates`，第四节专门拆它。

## 二、事务：靠纪律，不靠语法

Rust 的 guard 在离开作用域时回滚，Spring 的 `@Transactional` 把边界声明在方法上。Go 都没有，只有手动三步：`BeginTx`、业务操作、`Commit` 或 `Rollback`。危险在于第三步靠人记：函数在 `Commit` 之前任何一处 `return err`，事务就悬在那里，**连接被一直占着，池慢慢耗尽，整个服务卡死**。这不是危言耸听，`BeginTx` 从池里拿的连接归事务独占，你不结束事务，池拿不回它。

关键在 `defer tx.Rollback()` 这一行。它利用了两个事实：`Rollback` 在 `Commit` 成功之后调用只返回 `sql.ErrTxDone`，不做任何事，所以**无论哪条出口都先回滚是安全的**；`fn` 若 panic，`defer` 在栈展开时照样执行，事务同样回滚。业务代码只管在 `fn` 里返回错误，回滚是助手的职责。跑一遍验证原子性：

```go
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	_ "modernc.org/sqlite"
)

// WithTx 全项目唯一的事务入口：fn 返回错误就回滚，成功才提交。
func WithTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() // Commit 之后再 Rollback 只返回 ErrTxDone，无害
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func main() {
	db, _ := sql.Open("sqlite", "file:l12s2?mode=memory&cache=shared")
	defer db.Close()
	db.Exec("CREATE TABLE accounts(id INTEGER PRIMARY KEY, bal INT)")
	db.Exec("INSERT INTO accounts(id, bal) VALUES (1, 100)")
	ctx := context.Background()
	err := WithTx(ctx, db, func(tx *sql.Tx) error {
		if _, err := tx.Exec("UPDATE accounts SET bal = bal - 30 WHERE id = 1"); err != nil {
			return err
		}
		return errors.New("风控拦截") // fn 返回错误：扣款被整体撤销
	})
	var bal int
	_ = db.QueryRow("SELECT bal FROM accounts WHERE id = 1").Scan(&bal)
	fmt.Println(err, bal)
	err = WithTx(ctx, db, func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE accounts SET bal = bal - 30 WHERE id = 1")
		return err
	})
	_ = db.QueryRow("SELECT bal FROM accounts WHERE id = 1").Scan(&bal)
	fmt.Println(err, bal)
	tx, _ := db.BeginTx(ctx, nil) // Commit 只能成功一次；事后再 Rollback 是无害的空操作
	ok := tx.Commit() == nil
	fmt.Println(ok, errors.Is(tx.Rollback(), sql.ErrTxDone))
}
```

```text
风控拦截 100
<nil> 70
true true
```

〔实测〕在 go1.26.4 加 `modernc.org/sqlite` 上运行本节示例：`fn` 先扣 30 再返回错误，余额保持 100；`fn` 正常返回，余额变 70；`Commit` 成功后再 `Rollback` 得到 `ErrTxDone`。资料的教学代码同样演示了 `Commit` 与 `Rollback` 各只能执行一次。〔C:go_basic/database/crud.go:53-69〕

三条配套规则：

1. **事务里的所有操作用 `tx`，不要混用 `db`**。`db.Exec` 从池里另拿连接，那条连接不在事务里，你以为在事务里的写入其实已经独立提交了。
2. **事务要短**。事务期间不要调外部服务、不要睡眠：连接被独占，持锁时间随外部延迟放大，池和数据库两边都在挨饿。
3. **隔离级别不要依赖默认值**。PG 默认 read committed，MySQL InnoDB 默认 repeatable read，跨库迁移时语义悄悄变了。`BeginTx` 的第二个参数 `*sql.TxOptions` 就是用来显式设置的。

## 三、NULL 与"没有这一行"

数据库列可以是 NULL，但 Go 的 `string` 没有这个状态，两套类型系统在这里对不上。直接 `Scan` 进 `string` 的下场：

```text
sql: Scan error on column index 0, name "deleted_at": converting NULL to string is unsupported
```

Rust 有 `Option<String>`，Kotlin 有 `String?`，TS 有 `string | null`，Go 的基础类型没有对应物，所以要靠三种绕法，前两种是标准库和语言自带的，第三种在 SQL 里解决：

```go
package main

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

func main() {
	db, _ := sql.Open("sqlite", "file:l12s4?mode=memory&cache=shared")
	defer db.Close()
	db.Exec("CREATE TABLE users(id INTEGER PRIMARY KEY, deleted_at TIMESTAMP)")
	db.Exec("INSERT INTO users(id, deleted_at) VALUES (1, NULL), (2, '2026-01-01 00:00:00')")
	var ns sql.NullString // 策略一：sql.Null 类型，用 Valid 字段区分 NULL
	_ = db.QueryRow("SELECT deleted_at FROM users WHERE id = 1").Scan(&ns)
	fmt.Printf("NULL 行 NullString: Valid=%v String=%q\n", ns.Valid, ns.String)
	_ = db.QueryRow("SELECT deleted_at FROM users WHERE id = 2").Scan(&ns)
	fmt.Printf("非 NULL 行 NullString: Valid=%v String=%s\n", ns.Valid, ns.String[:10])
	var p *string // 策略二：指针，NULL 扫进来就是 nil
	_ = db.QueryRow("SELECT deleted_at FROM users WHERE id = 1").Scan(&p)
	fmt.Println("NULL 行指针为 nil:", p == nil)
	var s string // 策略三：在 SQL 里用 COALESCE 把 NULL 消灭掉
	_ = db.QueryRow("SELECT COALESCE(deleted_at, '1970-01-01') FROM users WHERE id = 1").Scan(&s)
	fmt.Println("COALESCE 后:", s[:10])
}
```

```text
NULL 行 NullString: Valid=false String=""
非 NULL 行 NullString: Valid=true String=2026-01-01
NULL 行指针为 nil: true
COALESCE 后: 1970-01-01
```

怎么选：一次性脚本和报表用 COALESCE，最快；有业务语义的可空字段（比如"删除时间"）用指针，nil 就是"没有"；不想让结构体字段是指针时用 `sql.NullString`，Go 1.22 起还有泛型版 `sql.Null[string]`，不用再背 `NullInt64` 这一族名字。"没有这一行"是另一回事：`QueryRow` 本身永远不报错，查不到行时错误藏在返回的 `*sql.Row` 里，要等 `Scan` 那一行才冒出来，而且是哨兵错误 `sql.ErrNoRows`，用 `errors.Is` 判断，不要当成故障。开头第一个示例的最后两行就是这两件事的现场：`Scan` 之前一切安好，`Scan` 之后才有 `ErrNoRows`。

## 四、GORM 与 sqlc：SQL 和类型对接的两条路

| 方式 | 适合 | 代价 |
|---|---|---|
| 原生 `database/sql` + `Scan` | 简单、要换库、测试用 sqlite | 手写 `Scan`，PG 类型要写胶水 |
| sqlc（SQL 先行，生成类型安全代码） | 锁定一种库，又要类型安全 | 多一个生成步骤，CI 一行命令 |
| GORM（结构体先行，链式 API） | 快速出活，常规 CRUD 多 | 大量接口收 `interface{}`，同一个方法传不同类型行为不同 |

GORM 的难点就是第一节引的那句话：同参不同型行为不同。典型是 `Updates`，把它拆开看：

- `Updates(struct)`：只更新**非零值**字段。想把某字段更新成零值（年龄清零、状态归零），它根本不进 SQL；
- `Updates(map)`：map 里写什么更新什么，零值照写；
- `Save`：保存**所有**字段含零值，主键为 0 时相当于 `Create`。〔C:go_frame/orm/gorm/update.go:10-33〕

为什么这样设计？一个合理的解释是 `struct` 参数里 GORM 分不清"你没赋值"和"你赋了零值"，只能约定零值等于没说（这是推断，课程只演示了行为）；`map` 的键显式列出了列名，不存在歧义。代价是你必须记住每个方法对每个参数类型的语义。资料作者的原话就是应对方案：拿不准就看文档、看源码，或者写个小例子验证。〔W02-2 p7〕
GORM 还有几条值得记的约定：〔C:go_frame/orm/gorm/read.go:13-46〕〔C:go_frame/orm/gorm/create.go:16-48〕

1. `First`/`Take`/`Last` 查不到返回 `gorm.ErrRecordNotFound`，`Find` 不会，查无结果就不改你传的结构体；
2. `Create` 必须传指针，GORM 要回填自增主键和 `CreatedAt`；
3. 钩子按 `BeforeSave`、`BeforeCreate`、插入、`AfterCreate`、`AfterSave` 的顺序执行，任一钩子返回错误，整个操作回滚。

sqlc 走的是相反的路：SQL 是真相源，Go 代码是生成物。`queries/users.sql` 里写一条带注解的 SQL，`sqlc generate` 一步生成方法：

```sql
-- queries/users.sql
-- name: CreateUser :one
INSERT INTO users (email, name, bio, age) VALUES (?, ?, ?, ?)
RETURNING id, email, name, bio, age;
```

生成物长这样（来自 `exercises/p11sqlm/db/users.sql.go`，sqlc v1.31.1）：参数收进 `CreateUserParams` 结构体，可空列自动映射成 `sql.NullString`，也就是第三节的三策略之一被生成器替你选了：

```go
type CreateUserParams struct {
	Email string; Name string
	Bio sql.NullString; Age sql.NullInt64
}
func (q *Queries) CreateUser(ctx context.Context, arg CreateUserParams) (CreateUserRow, error)
```

写错列名、表名，`sqlc generate` 通常直接报错，类型安全来自生成阶段而不是编译器（Go 没有宏，做不到 Rust sqlx 那样的编译期校验）。但这个检查不完备：旧章实测把 `SELECT COUNT(*) ... WHERE agee > 1` 里的列名拼错，v1.31.1 的 sqlite 引擎照样退出码 0，所以涉及聚合的查询要配测试。来源是 `notes/p11-sqlc-goose.md`。下面的程序把生成的调用效果原样走一遍，看可空列怎么进出：

```go
package main

import (
	"database/sql"
	"errors"
	"fmt"

	_ "modernc.org/sqlite"
)

func main() {
	db, err := sql.Open("sqlite", "file:l12s6?mode=memory&cache=shared")
	if err != nil {
		panic(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT NOT NULL UNIQUE, name TEXT NOT NULL, bio TEXT, age INTEGER)`); err != nil {
		panic(err)
	}
	var u struct {
		ID int64; Email, Name string
		Bio sql.NullString; Age sql.NullInt64
	}
	err = db.QueryRow(`INSERT INTO users (email, name, bio, age) VALUES (?, ?, ?, ?)
RETURNING id, email, name, bio, age`, "a@x.io", "alice", sql.NullString{String: "hi", Valid: true}, sql.NullInt64{Int64: 30, Valid: true}).
		Scan(&u.ID, &u.Email, &u.Name, &u.Bio, &u.Age)
	fmt.Println("CreateUser:", u.ID, u.Name, u.Bio.String, u.Age.Int64)
	err = db.QueryRow(`SELECT id, email, name, bio, age FROM users WHERE id = ?`, 999).
		Scan(&u.ID, &u.Email, &u.Name, &u.Bio, &u.Age)
	fmt.Println("GetUser(999):", errors.Is(err, sql.ErrNoRows))
}
```

```text
CreateUser: 1 alice hi 30
GetUser(999): true
```

`ErrNoRows` 照样在 `Scan` 那层冒出来，生成代码不隐藏它，第三节的判断方式原样适用。

选哪条路？课程的实际做法是 **GORM 作 dao 层的实现，上面套 repository 抽象**（10 节的三层结构），这样换成 sqlc 或别的存储时，上层不动。〔W02-2 p14-15〕新项目从 SQL 出发的话，sqlc 加 goose 的组合更干净：`migrations/`、`queries/*.sql`、`sqlc.yaml` 三件套，`sqlc generate` 一步生成。

## 五、树形评论：四种建模，选一种

前面都在讲"一张平表怎么读写"。评论是关系建模的进阶题：它天然是树（可被回复），查询是高并发场景（打开资源必加载），删的是子树。同样的树，四种建模差别巨大：〔W16-1 p7-12〕

| 建模 | 形状 | 查子节点 | 评价 |
|---|---|---|---|
| 邻接表 | `pid` 指父主键 | `WHERE pid=?` | 最常见；推荐 |
| 分段 Path | `path` 列存 `a/b/c` | `LIKE 'a/%'` | 索引下推后尚可，Like 有上限 |
| Nested Set | `nsleft/nsright` 存 DFS 序 | 区间查询 | 插入要改半张表，难维护 |
| Closure Table | 另一张 `TreePaths(祖先,后继)` | 直接查 | 数据量爆炸 |

课程按三层查询场景选型：资源首页第一页评论、每条评论的前几条回复、点"更多"加载整棵回复。核心操作是"查某节点的子节点"，邻接表一条 `WHERE pid=?` 解决，其余三种要么查询别扭要么写入昂贵，落选。表上再补一个 **RootID 冗余列**（回复与它所属的根评论同值），"加载更多回复"就从递归遍历退化成 `WHERE root_id=? AND id<? ORDER BY id DESC LIMIT ?`，一次查询出整棵回复列表；分页不用 offset（offset 有并发漂移），用 min_id keyset。索引三件：Uid（查自己的评论）、Biz+BizID 复合（按资源查）、PID（查子节点）。〔W16-1 p15-19〕〔W16-1 p26〕〔W16-1 p30-31〕

删除子树的两条路：手动级联（循环"找父=当前 id 的行再删"，几层就几次；path 建模可 `DELETE WHERE path LIKE` 一条搞定）与 GORM 外键 `OnDelete:CASCADE` 交给数据库。写量大的平台（Kafka 异步削峰在 13 节展开）和热门/非热门分级限流是评论系统的并发配套，属于 16 节稳定性话题，这里记住数据形状就够。〔W16-1 p18〕〔W16-1 p19-21〕

## 六、迁移：表结构是服务器状态，不在代码里

代码能随 `git checkout` 回到任意版本，表结构不行：它是数据库服务器上的状态，改了就是改了。两台机器、两个同事、一条上线流水线，都要把表结构推到同一个形状。办法是把每次变更写成一个带序号的 SQL 文件，数据库里留一张表记录"已经跑到第几个"，goose 就是这么做的。

建表和改表有两条路线：〔W02-2 p22-23〕

> 〔W02-2 p22-23〕一般在规范比较严格的公司里面，表结构变更都是要走审批流程的。……评审最重要的就是看你的索引对不对。……后续有新的数据要存储，都要来这里初始化一下表。

规范严格的公司走 SQL 评审，leader 到 DBA，重点看索引对不对；中小企业用 GORM 的 `AutoMigrate`，新表都往一处注册。GORM 那条路的隐患是 `AutoMigrate` 偏向"只增不减"：按 GORM 文档，它会建表、补缺失的列和索引，不会删除不再使用的列，改列名也会被当成新增一列，所以破坏性变更要自己写迁移，它更适合开发期。（以上来自 GORM 文档，本机未验证。）两条工具的坑值得记：
- sqlc 的注解 `-- name: Xxx :many` 紧邻长中文注释时，旧章怀疑会解析失败，但在 sqlc v1.31.1 上两次构造都没有复现；生成器的解析器不是编译器，查询文件里的注释保留 ASCII 是便宜的防御。来源是旧章复盘记录，见 `notes/p11-sqlc-goose.md` 的翻车记档（1），资料原话里没有这条。
- goose 的迁移文件里，普通注释行中不要出现 `-- +goose`，会被当成真注解，报错是 `failed to parse annotation line`，不是"你的注释写错了"。来源是旧章复盘记录，见 `notes/p11-sqlc-goose.md` 的翻车记档（2），带完整复现命令。

## 小结

1. `sql.Open` 只登记参数不建连接，连接错误要等第一次查询或 `PingContext`；`*sql.DB` 是并发安全的池，全局一份。
2. 池三旋钮：`SetMaxOpenConns` 限流、`SetMaxIdleConns` 复用、`SetConnMaxLifetime` 换血；超限的查询排队，`db.Stats().WaitCount` 留痕。
3. 事务靠纪律：`WithTx` 加 `defer tx.Rollback()`，任何出口（包括 panic）都回滚；`Commit` 之后的 `Rollback` 是无害的 `ErrTxDone`。
4. 事务里只用 `tx`，事务要短，隔离级别显式写。
5. NULL 三策略：`sql.Null*`（看 `Valid`）、指针（nil 即无）、SQL 里 `COALESCE`；`ErrNoRows` 在 `Scan` 才冒出，`errors.Is` 判断。
6. GORM 快但接口收 `interface{}`，同参不同型（`Updates` 的 struct/map 之分）；sqlc 以 SQL 为真相源生成类型安全代码。
7. 树形评论用邻接表加 RootID 冗余：查子节点 `WHERE pid=?`，"更多回复"退化为 root_id 上的 keyset 分页；四种建模里它是查询、写入、删除的综合最优。
8. 表结构是服务器状态，用带序号的迁移文件管理；严格公司走 SQL 评审，小团队用 `AutoMigrate` 起步。

## 常见坑

1. **把 `sql.Open` 当成连接成功**。要用 `PingContext` 验证。
2. **忘记 `rows.Close()`**，连接一直被占。查询用 `defer rows.Close()`，并检查 `rows.Err()`。
3. **事务里混用 `db` 和 `tx`**，事务外的写入独立提交。
4. **GORM `Updates(struct)` 想把字段改成零值**，没改上。
5. **`DSN` 少了 `parseTime=True`**，时间字段扫不进 `time.Time`。
6. **内存库按连接隔离**：sqlite 的 `:memory:` 库要 `SetMaxOpenConns(1)`，否则并发测试里每个连接看到的是不同的库。
7. **`int(11)` 之类的显示宽度**在 MySQL 8.0.17 之后已废弃，教学 DDL 里的旧写法别照抄。〔C:go_basic/database/create_table.sql〕

第 6 条值得亲眼一看：把一个 `:memory:` 库的池放开到 2 条连接，每条连接各自开了一个独立的空库，在连接 1 里建的表，连接 2 看不见：

```go
package main

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

func main() {
	db, err := sql.Open("sqlite", ":memory:") // 不带 cache=shared：每条连接各开一个独立的内存库
	if err != nil {
		panic(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(2)
	if _, err := db.Exec("CREATE TABLE t(x)"); err != nil {
		panic(err)
	}
	c1, _ := db.Conn(context.Background())
	c2, _ := db.Conn(context.Background())
	_, err1 := c1.ExecContext(context.Background(), "INSERT INTO t VALUES (1)")
	_, err2 := c2.ExecContext(context.Background(), "INSERT INTO t VALUES (2)")
	fmt.Printf("连接 1 插入: %v\n连接 2 插入: %v\n", err1 == nil, err2)
	c1.Close()
	c2.Close()
}
```

```text
连接 1 插入: true
连接 2 插入: SQL logic error: no such table: t (1)
```

正确姿势有两种：`:memory:` 配 `SetMaxOpenConns(1)`；或者 DSN 写 `file:x?mode=memory&cache=shared`，让所有连接共享同一个内存库。本节前面的示例用的是后一种，练习包 `exercises/p9db` 的 `openTestDB` 则两者都用了。

## 判断题与答案

先自己答，再看后面。

1. 池的 `MaxOpenConns` 设成 2，同时发 6 个查询，会怎样？
2. `WithTx` 里 `defer tx.Rollback()` 在 `Commit` 成功后执行会发生什么？
3. 为什么 `Updates(struct{Age: 0})` 不会把年龄清零？
4. 事务里调用了一个远程 HTTP 接口，会有什么风险？

**答案**

1. 2 条连接先跑，其余 4 个在池前排队，`db.Stats().WaitCount` 记 4；总耗时约等于三批查询之和而不是一批。排队是池的限流行为，不是故障。
2. 返回 `sql.ErrTxDone`，不产生任何效果。所以 `defer tx.Rollback()` 写在 `Commit` 之前是安全且必要的兜底。
3. GORM 对 `struct` 参数约定"零值等于没赋值"，因为分不清"没赋值"和"赋了零值"；要把字段更新成零值用 `Updates(map)`，`map` 的键显式列出了列名，没有歧义。
4. 连接被独占，持锁时间随外部接口的延迟和超时放大；外部服务一慢，池被慢慢占光，整个服务不可用。事务里只做数据库操作，外部调用放到事务外。

## 在项目里

- `dnsproxi-go` 是纯内存缓存，没有数据库，这里没有现成例子。
- minifeed 的 `user/store.go` 与 `post/post.go` 用 `database/sql` 加 `SQLiteStore` 实现了 repository 接口：占位符查询、`defer rows.Close()` 加 `rows.Err()`、`errors.Is(err, sql.ErrNoRows)` 转成包内哨兵错误 `ErrNotFound`，是本节纪律的最小落地（25 节）。练习包 `exercises/p9db` 里 `WithTx` 是本节事务纪律的可测试版本，`openTestDB` 展示了内存库配池的正确姿势。

## 想深入去哪查

- GORM：W02-2（CRUD、软删除、钩子、三层分层、密码加密与 BCrypt）、`C:go_frame/orm`（`PrepareStmt`、读写分离、钩子、索引提示）。标准库：`C:go_basic/database`（`Exec`/`Query`/事务/批量插入/sqlbuilder）。
- 树形评论：W16-1 全文（四种建模、RootID 冗余、加载更多与 min_id 分页、级联删除与外键）。
- 旧长版：`notes/p9-database.md`（连接池实测、隔离级别、pgx 原生）、`notes/p11-sqlc-goose.md`（迁移、sqlc 生成）、`notes/p12-sqlc-pgx.md`（PG 类型、批量写入）。
