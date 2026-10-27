# 12 数据库：连接池、事务纪律与 ORM 的取舍

> 详细程度：标准 · 约 80 分钟
> 材料：W02-2、W06-2、W16-1、C:go_basic/database、C:go_frame/orm、旧长版 `notes/p9-database.md` `notes/p11-sqlc-goose.md` `notes/p12-sqlc-pgx.md`
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

`sql.Open(driverName, dataSourceName)` 返回数据库句柄，可能只校验参数而不建立连接；具体行为取决于驱动。〔C:go_basic/database/db_test.go:24-26〕连接错误可能等到 `Exec`、`Query` 或 `PingContext` 才出现。需要启动时确认数据库可达，可以用带超时的 `db.PingContext(ctx)`；它只能证明当时的连接可用，不能保证之后不会故障。

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

池上限为 2 时，持有连接的操作最多同时有两个，其他操作需要等待。输出中的等待次数和耗时是一次运行结果，调度、机器负载与数据库锁竞争都会影响它们，不能保证每次恰好为 4 或超过两倍。`db.Stats().WaitCount` 是累计等待连接的次数。连接池限制的是连接数，而不是排队 goroutine 的数量；仍需限制请求并发、设置超时并监控等待。配置上限时要结合数据库连接预算、其他客户端以及应用实例数，不能简单把服务器上限全部分给应用。

驱动用之前的另一个坑也要交代：驱动必须**匿名导入**。`database/sql` 通过注册表找驱动，`import _ "modernc.org/sqlite"` 的下划线表示只用它的 `init()` 注册，不直接用包里的任何名字。忘了这一行，编译照样通过，`sql.Open` 运行时才报 `sql: unknown driver "sqlite" (forgotten import?)`。

> 〔W02-2 p7〕GORM 的很多接口都是接收 interface 的，根据你传入不同类型的参数，会执行不同的行为。……虽然 GORM 我用过很多次，但是我还是觉得这些方法语义过于模糊，经常写错。

GORM 在 `*sql.DB` 之上又包了一层。课程 MySQL DSN 使用 `charset=utf8mb4&parseTime=True&loc=Local`：〔C:go_frame/orm/gorm/connection.go:17-21〕`utf8mb4` 支持完整 Unicode，`parseTime=True` 让驱动把日期时间列解析为 `time.Time`，`loc` 控制解析时使用的时区；具体配置需按驱动和应用需求选择。大量接口收 `interface{}`，同一个方法传不同类型行为不同，第四节会拆解 `Updates`。

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

## 课件实战：七个现场

手册主线用的都是最小示例；课件把同样的知识铺成了成套的实战代码。`C:go_basic/database` 是一套 `database/sql` 教学代码，从建表、写入、事务到查询走完整流程；`C:dachang/database` 用 GORM 编了五个专题，本节取其中四题（另一个 PrepareStmt 预编译提速题见"想深入去哪查"）[校注：对应 `C:go_frame/orm` 已展开的部分，此处不再重复]。下面每小节先给课件代码，再拆里面起作用的机制；所有数据都是作者机器上 `go test -v` 的留档，倍数关系比绝对值可靠。

### 写入的返回值：LastInsertId 与 RowsAffected

`db.Exec` 返回 `(res, error)`，结果从 res 上取两个值：`LastInsertId` 与 `RowsAffected`。`Insert` 用一条 SQL 插 2 行（小明、小红），注释留了三个事实：ID 自增、用过的 id（即使对应的行已 delete）不会重复使用；单条 INSERT 插多行时，它是第一条数据的 id；插 2 行所以 RowsAffected 是 2。〔C:go_basic/database/crud.go:14-25〕

```go
//一条sql，插入2行记录
res, err := db.Exec("insert into student (name,province,city,enrollment) values ('小明', '深圳', '深圳', '2022-07-03'), ('小红', '上海', '上海', '2022-07-03')")
lastId, err := res.LastInsertId() //ID自增，用过的id（即使对应的行已delete）不会重复使用。如果使用单个INSERT语句将多行插入到表中，则LastInsertId是第一条数据使用的id
rows, err := res.RowsAffected() //插入2行，所以影响了2行
```

这些返回值依赖数据库和驱动：`LastInsertId` 并非所有驱动都支持，多行插入的 ID 语义也要查对应文档；`RowsAffected` 对更新操作可能受数据库和连接配置影响，不能一概当作 WHERE 命中行数。以下表格只描述课件中 MySQL 示例的观察。〔C:go_basic/database/crud.go:41-48〕〔C:go_basic/database/crud.go:72-76〕

| 操作 | LastInsertId | RowsAffected |
|---|---|---|
| INSERT 两行 | 第一条数据的 id | 2（新插行数） |
| UPDATE | 恒 0（仅插入才赋值） | where 命中行数 |
| DELETE | 恒 0（仅插入才赋值） | where 命中行数 |
| REPLACE 两行 | 取最接近被替换的那条 id | 4＝先删 2 后插 2 |

`Replace` 是 Exec 三兄弟里唯一改写语义的：student 表的 name 上有唯一索引 idx_name，普通 insert 撞上重复 name 直接报错；replace into 遇到重复行的动作序列是**先删除旧行、再插入新行**。对已存在的小明、小红两行执行一次 replace 两行，RowsAffected 是 4（先删 2 行、后插 2 行的总和）。把 4 当成"插了 4 行"、或拿影响行数当零一判断（"影响 0 行就是没生效"），都会得出错误结论；对 replace 语义的把握必须精确到"删除+插入各算一遍"。〔C:go_basic/database/crud.go:28-38〕〔C:go_basic/database/create_table.sql:11-23〕

### 遍历查询结果：Next、Scan 与错误归宿

拿到 `*sql.Rows` 后的标准处理序列三步：`defer rows.Close()`；`for rows.Next()` 遍历，没有数据或发生 error 时返回 false，循环自然结束；循环体内 `rows.Scan` 按查询列的顺序把列值赋给传入的变量地址。〔C:go_basic/database/crud.go:109-119〕

```go
rows, err := db.Query("select id,name,city,score,enrollment from student where enrollment>=20250130 limit 5")
defer rows.Close()
for rows.Next() { //没有数据或发生error时返回false
	var id int
	var score float32
	var name, city string
	var enrollment time.Time
	err = rows.Scan(&id, &name, &city, &score, &enrollment) //通过scan把db里的数据赋给go变量
}
```

count(*) 的单行结果也必须先 `rows.Next()` 再 `Scan`。分页函数第一步先查 total，专门留了注释"一定要先执行Next()"；不 Next 就 Scan，拿到的不是行数据而是零值，total 变成 0，分页直接算错。机理：`*sql.Rows` 是游标，`Scan` 取的是"当前行"，初始时刻游标还没落在第一行上，`Next()` 才把它推进去。分页第二步 `limit` 的 offset 用 `pageSize*(page-1)` 算出，limit 值本身用 Sprintf 拼接（offset 在课件里是 `int`，不构成注入面，但只要用户能碰它就得走参数化）。〔C:go_basic/database/crud.go:86-106〕

这套代码里查询和写入的错误统一走 `CheckError`：log 打印后 `os.Exit(1)`，教学代码从简，生产代码应把 error 上抛交给调用方处理（注意 `os.Exit` 会跳过上面 `defer rows.Close()`）。〔C:go_basic/database/util.go:20-25〕

### DSN 与连接池：sql.Open 之后发生了什么

这套代码把连接的建立放在包 `init` 里：`sql.Open("mysql", DSN)` 得到全局 `*sql.DB`。手册开头"sql.Open 不建立连接"在这里成立：包初始化只登记参数，物理连接要等第一条语句执行才真正建立。〔C:go_basic/database/db_test.go:14-29〕

```text
DSN(data source name)格式：[username[:password]@][protocol[(address)]]/dbname[?param1=value1&...&paramN=valueN]
```

DSN 参数各管一件事：`parseTime=True` 让 DATE/DATETIME 列扫进 time.Time（漏了它，上一小节的 enrollment 就扫不进去）；`charset=utf8mb4` 支持完整 UTF-8 编码（含 emoji）；`loc` 声明时间解析用哪个时区，课件示例用 Asia/Shanghai。注释明示"db可以并发使用"，全局一份句柄被各测试共享，`db.Close()` 被注释掉，进程生命周期内复用。这条"不调 SetMaxOpenConns"的事实来自课件：课件源码里没有调过连接池参数，那组旋钮留作练习，因此**原课未规定具体数值**。

### 深翻页：offset 与游标的代价

同一个"遍历 student 全表"的任务，课件给出两种取"下一页"的写法。Traverse1 通过 limit offset,n 遍历：每轮 `Limit(PAGE_SIZE=100).Offset(offset)`，offset 累加本页行数，取到空页为止。Traverse2 通过 id>x limit 0,n 遍历：每轮 `Where("id>?", maxid).Limit(100)`，源码注释明示"默认会按id排好序"，因此本页最后一行就是本页最大 id，直接拿它当下一轮游标，逐元素比较取最大值的写法被注释掉，注释交代"已按id排好序，所以最后一个students的Id最大"。〔C:dachang/database/limit.go:32-72〕

```go
// Traverse1 通过limit offset,n遍历表
db.Select("id,name").Limit(PAGE_SIZE).Offset(offset).Find(&students)
offset += len(students)

// Traverse2 通过id>x limit 0,n遍历表
db.Select("id,name").Where("id>?", maxid).Limit(PAGE_SIZE) //默认会按id排好序
maxid = students[len(students)-1].Id //已按id排好序，所以最后一个students的Id最大
```

测试注释记录了作者机器上的结果：offset 总计 92.646ms，游标总计 26.5669ms。〔C:dachang/database/limit_test.go:31-58〕深 offset 通常需要扫描并跳过前面的行；游标查询有合适索引时可从边界继续扫描，但代价仍取决于索引、过滤条件和执行计划，并非恒定。课件没有显式排序，SQL 不保证返回顺序；实际使用应写 `ORDER BY id`，并为非唯一排序键增加唯一的并列排序键，才能正确推进游标。

### SQL 注入：拼接与占位符的一行之差

LoginUnsafe 和 LoginSafe 的唯一代码区别在 Where 的写法，两函数其余逻辑完全相同：〔C:dachang/database/stmt.go:8-19〕

```go
// 登录成功返回true。容易被SQL注入攻击
db.Table("login").Select("*").Where("username='" + name + "' and password='" + passwd + "'").Count(&cnt)

// 登录成功返回true。拒绝SQL注入攻击
db.Table("login").Select("*").Where("username=? and password=?", name, passwd).Count(&cnt)
```

攻击效果以测试注释留档的 SQL 为准：〔C:dachang/database/stmt_test.go:35〕

```sql
SELECT count(*) FROM `login` WHERE username='jane' and password='asdfghj' or '1'='1'
```

and 先于 or，整句变成 (username='jane' and password='asdfghj') or ('1'='1')，后半恒真，count>0，测试断言 LoginUnsafe 返回 true 才算复现成功〔C:dachang/database/stmt_test.go:36-38〕。同样的攻防中，Safe 版把同类载荷整个当作一个普通参数参与 password 的等值比较，登不进，count=0，测试断言返回 false〔C:dachang/database/stmt_test.go:56-58〕。防御原理：占位符下用户输入由驱动层参数化传递，以"纯数据"的身份到达数据库，永不进入 SQL 语法层拼接，无法改变语句结构（注意区分：LoginUnsafe 拼接用的载荷是 `asdfghj' or '1'='1`，LoginSafe 测试用的是同型的 `safsdghf' or '1'='1`，字面不同）。

### 用一张表做分布式锁

机巧全在约束上：distributed_lock 表的 lock_name 上建唯一索引 idx_lock，update_time 由 MySQL 自动维护（default current_timestamp on update current_timestamp）。唯一索引保证同一个锁名全表只有一行，插入成功即持锁并返回 true；函数非阻塞，抢不到立即返回 false。〔C:dachang/database/create_table.sql:1-9〕〔C:dachang/database/lock.go:41-66〕

```go
// 如果强到锁，就返回true；没强到就立即返回false，不阻塞
func (model *MysqlDistributedLock) Lock(name string, expiration float64) bool {
	err := db.Model(model).Create(map[string]any{"lock_name": name}).Error //提交数据
	if err != nil && expiration > 0 { //第一次没抢到，尝试释放过期锁后再加锁
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) { //  只有唯一索引冲突才尝试夺锁
			if mysqlErr.Number == 1062 {
				var data MysqlDistributedLock
				if db.Where("lock_name=?", name).First(&data).Error == nil {
					if time.Since(data.UpdateTime).Seconds() >= expiration {
						if db.Delete(data).Error == nil {
							return model.Lock(name, expiration)
						}
					}
				}
			}
		}
	}
	//出错或抢锁失败都返回false
}
```

源码自身注释写出全部四步：抢不到锁且 expiration>0 时，errors.As 把 err 解析成 `*mysql.MySQLError` 确认错误码 1062（违反唯一索引），按 lock_name 查出旧记录，`time.Since(UpdateTime).Seconds() >= expiration` 判断上次加锁是否已过期，已过期就删除旧记录后递归调用 Lock 再次插入。旧记录未过期、查询失败或删除失败，都返回 false。Unlock 按 lock_name 删行。〔C:dachang/database/lock.go:44-70〕

`expiration` 在两处起作用：一是 >0 才开启"过期可夺"路径，二是作为判断过期的时间阈值（课件测试设 3 秒）。测试用 10 个协程模拟 10 个进程争抢同一把锁，验证三个阶段：初次争抢、过期后重新争抢（sleep 1+Expire 秒）、Unlock 后重新争抢。〔C:dachang/database/lock_test.go:11-57〕

注意"删旧再重抢"是先删后插两步，不是原子操作：两个客户端可能同时判定过期，一个删除成功后、另一个递归插入前被插队，极端情况下两个进程都认为自己持锁。这是对代码流程的直接分析（摘要"易错"栏同结论），课件没有把非原子定性写成注释。〔C:dachang/database/lock.go:44-66〕

### 批量插入：提交点决定性能

三种写法各插 INSERT_COUNT=10000 条，差别在提交点。InsertOneByOne：循环内逐条 `db.Create(&student)`，每条一次往返且各自隐式提交，实测 1m2.96s。InsertByTransaction1：`db.Begin()` 开事务，循环内改用 `tx.Create` 逐条插，循环结束后 `tx.Commit()` 一次提交，实测 1.795s，省掉的是每条语句各自的提交往返。InsertByTransaction2：事务内再按 BATCH=100 攒一个 []Student，每批一次 `tx.Create(&students)`（一条多值 INSERT），共 100 次往返+1 次提交，实测 310.6ms；三者相差约 200 倍。〔C:dachang/database/transaction.go:12-61〕〔C:dachang/database/transaction_test.go:43-47〕

```go
// 一次插入多条，整体再放到一个事务里
tx := db.Begin()
const BATCH = 100 // 一条SQL语句插入多条
for i := 0; i < INSERT_COUNT; i += BATCH {
	students := make([]Student, 0, BATCH)
	for j := 0; j < BATCH; j++ { students = append(students, student) }
	tx.Create(&students)
}
tx.Commit()
```

事务边界与失败处理：Begin 后所有 Create 挂在 tx 上，Commit 在全部插入成功后执行一次；三个函数的错误路径一致：打印错误并 return，事务版没有显式 Rollback（摘要有意保留的简化，不是"失败自动回滚"）。测试每次先 `db.Delete(Student{}, "1=1")` 清空表再计时。〔C:dachang/database/transaction_test.go:14-47〕

源码注释明示"注意需要传student的指针"，Create 必须传指针（&student / &students），GORM 要回填自增主键和 CreatedAt。BATCH=100 是"一条SQL语句插入多条"的教学默认值，课件未解释如何选取，调优时需要自行权衡往返次数与单语句体积。〔C:dachang/database/transaction.go:12-61〕

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

## 五、内容状态机：制作库与线上库两套状态

发帖系统的状态不在一张表里。创作侧有草稿和定时发表的需求，读者侧只该看到已发表的内容，所以课程把存储拆成两库：制作库（作者读写，含未发表草稿）与线上库（读者读，只有已发表内容）。状态机也跟着分叉：制作库三态（未发表/已发表/仅自己可见），线上库两态（已发表/仅自己可见）。〔W06-2 p4〕保存语义有三种情况容易漏：新建保存是未发表、修改保存保持未发表、**已发表后再编辑保存要退回未发表**（改过的内容必须重新过审或重新发表，不能直接可见）。〔W06-2 p6〕

状态常量定义在 domain 层，有一条零值禁忌：`Unknown=0` 表示"没有值"，**不要把零值做成有意义的业务状态**，否则 Go 的零值初始化会把"忘了赋值"和"处于某状态"混为一谈。DAO 层做领域状态到存储状态（uint8）的转换，Web 层完全不关心状态：〔W06-2 p5〕〔W06-2 p7〕

```go
package main

import "fmt"

// 领域状态:Unknown=0 是"没有值",不是任何真实状态。
const (
	Unknown     uint8 = 0
	Unpublished uint8 = 1
	Published   uint8 = 2
	Private     uint8 = 3
)

// toStorage 是 DAO 层的领域状态到存储状态的转换,零值在这里暴露。
func toStorage(s uint8) (uint8, error) {
	if s == Unknown || s > Private {
		return 0, fmt.Errorf("非法领域状态: %d", s)
	}
	return s, nil
}

func main() {
	for _, s := range []uint8{Published, Unknown, 9} {
		v, err := toStorage(s)
		if err != nil {
			fmt.Println("err:", err)
			continue
		}
		fmt.Println("ok:", v)
	}
}
```

```text
ok: 2
err: 非法领域状态: 0
err: 非法领域状态: 9
```

两个跨存储操作各有一个坑。withdraw 可在同一数据库事务中更新两张表，并检查影响行数；MongoDB 版发布接口则需考虑跨存储的部分失败。〔W06-2 p9〕〔W06-2 p44〕制作库换 MongoDB 时，课程选雪花算法生成 int64 主键。〔W06-2 p32-33〕课程的 OSS 方案把正文放到对象存储、线上库保留元数据，读者直接访问正文。〔W06-2 p55〕数据库事务不能原子提交 OSS 的 `PutObject`，失败或超时需要明确补偿与一致性方案，不能把外部调用当作事务的一部分。

## 六、树形评论：四种建模，选一种


前面都在讲"一张平表怎么读写"。评论围绕文章、视频这类资源展开，评论本身也能被回复和点赞；打开资源就要加载评论，所以查询是高并发场景。〔W16-1 p4〕回复把评论连成树，删除时可能要删整个子树。同样的树，四种建模差别巨大：〔W16-1 p7-12〕

| 建模 | 形状 | 查子节点 | 评价 |
|---|---|---|---|
| 邻接表 | `pid` 指父主键 | `WHERE pid=?` | 最常见；推荐 |
| 分段 Path | `path` 列存 `a/b/c` | `LIKE 'a/%'` | 索引下推后尚可，Like 有上限 |
| Nested Set | `nsleft/nsright` 存 DFS 序 | 区间查询 | 插入要改半张表，难维护 |
| Closure Table | 另一张 `TreePaths(祖先,后继)` | 直接查 | 数据量爆炸 |

课程先拆出三层查询场景：资源首页第一页评论、每条评论的前几条回复、点"更多"加载后续回复。〔W16-1 p5〕核心操作是"查某节点的子节点"，邻接表一条 `WHERE pid=?` 解决，其余三种要么查询别扭要么写入昂贵，落选。表上再补一个 **RootID 冗余列**（回复与它所属的根评论同值），"加载更多回复"就从递归遍历退化成 `WHERE root_id=? AND id<? ORDER BY id DESC LIMIT ?`，按页查询根评论下的回复列表；分页不用 offset（offset 有并发漂移），用 min_id keyset。索引三件：Uid（查自己的评论）、Biz+BizID 复合（按资源查）、PID（查子节点）。〔W16-1 p15-19〕〔W16-1 p26〕〔W16-1 p30-31〕

删除子树的两条路：手动级联（循环"找父=当前 id 的行再删"，几层就几次；path 建模可 `DELETE WHERE path LIKE` 一条搞定）与 GORM 外键 `OnDelete:CASCADE` 交给数据库。写量大的平台（Kafka 异步削峰在 13 节展开）和热门/非热门分级限流是评论系统的并发配套，属于 16 节稳定性话题，这里记住数据形状就够。〔W16-1 p18〕〔W16-1 p19-21〕

## 七、迁移：表结构是服务器状态，不在代码里

代码能随 `git checkout` 回到任意版本，表结构不行：它是数据库服务器上的状态，改了就是改了。两台机器、两个同事、一条上线流水线，都要把表结构推到同一个形状。办法是把每次变更写成一个带序号的 SQL 文件，数据库里留一张表记录"已经跑到第几个"，goose 就是这么做的。

建表和改表有两条路线：〔W02-2 p22-23〕

> 〔W02-2 p22-23〕一般在规范比较严格的公司里面，表结构变更都是要走审批流程的。……评审最重要的就是看你的索引对不对。……后续有新的数据要存储，都要来这里初始化一下表。

规范严格的公司走 SQL 评审，leader 到 DBA，重点看索引对不对；中小企业用 GORM 的 `AutoMigrate`，新表都往一处注册。GORM 那条路的隐患是 `AutoMigrate` 偏向"只增不减"：按 GORM 文档，它会建表、补缺失的列和索引，不会删除不再使用的列，改列名也会被当成新增一列，所以破坏性变更要自己写迁移，它更适合开发期。（以上来自 GORM 文档，本机未验证。）两条工具的坑值得记：
- sqlc 的注解 `-- name: Xxx :many` 紧邻长中文注释时，旧章怀疑会解析失败，但在 sqlc v1.31.1 上两次构造都没有复现；生成器的解析器不是编译器，查询文件里的注释保留 ASCII 是便宜的防御。来源是旧章复盘记录，见 `notes/p11-sqlc-goose.md` 的翻车记档（1），资料原话里没有这条。
- goose 的迁移文件里，普通注释行中不要出现 `-- +goose`，会被当成真注解，报错是 `failed to parse annotation line`，不是"你的注释写错了"。来源是旧章复盘记录，见 `notes/p11-sqlc-goose.md` 的翻车记档（2），带完整复现命令。

**配套代码：`C:dachang/database` 的四段小实验**。这个单元没有讲义页，`work/text` 里只有一份 9 个源文件的清单，正文就是源码本身，四个知识点各占一个小文件。先看全表遍历的两种翻页（〔C:dachang/database/limit.go:27-72〕，`PAGE_SIZE = 100`）：Traverse1 是 offset 式，每轮 `db.Select("id,name").Limit(PAGE_SIZE).Offset(offset).Find(&students)`，推进量取本次实际返回的行数 `offset += len(students)`，不是固定加 100，所以尾页不足 100 行也不会跳行，返回空切片就停；Traverse2 是 id 游标式，每轮 `db.Where("id>?", maxid).Limit(PAGE_SIZE).Find(&students)`，游标更新为本页最后一个元素的 `maxid = students[len(students)-1].Id`。这行代码依赖一个前提：结果集已按 id 排好序。代码注释原话是"默认会按id排好序"和"已按id排好序，所以最后一个students的Id最大"（limit.go:57、70），没有写显式的 `ORDER BY`，靠的是 InnoDB 按主键序返回的隐式行为；文件里还保留一段被注释掉的逐元素取最大值的备选写法（limit.go:64-68）。作者机器 MySQL 上的实测（〔C:dachang/database/limit_test.go:28-50〕输出注释）：1 万行全表遍历，offset 式总耗时 `92.646ms`，每 10 页打一次的抽样（`i%10==0`）从 i=10 的 `515μs` 涨到 i=80 的 `1760μs`，越翻越慢；游标式总耗时 `26.5669ms`，抽样页在 0 到 `630μs` 间波动，不随页码增大。这给第 6 节树形评论"分页不用 offset（offset 有并发漂移），用 min_id keyset"补上了数字注脚。最后一条边界是摘要归纳、课件代码没有写：两种翻页都受翻页期间的数据变动影响，游标式会跳过翻页期间新插入的更小 id。

再看 MySQL 唯一键分布式锁。表结构的关键是 `distributed_lock` 的 `lock_name` 上有唯一索引 `idx_lock`，`update_time` 是 `datetime default current_timestamp on update current_timestamp`（〔C:dachang/database/create_table.sql:1-9〕），所以抢锁的本质是向唯一索引列 insert。连接由谁建、何时建：`GetDB()` 用 `sync.Once` 懒加载全局 `*gorm.DB`，第一次调用 `Lock`/`Unlock` 时才执行 `gorm.Open`，DSN 带 `charset=utf8mb4&parseTime=True&loc=Local`，开 `PrepareStmt`，日志丢弃到 `io.Discard`，失败 panic（〔C:dachang/database/lock.go:21-31〕）；连接由这次 `gorm.Open` 建立，进程内只建一次，之后所有抢锁复用它背后的连接池。`Lock(name, expiration)` 的主流程（〔C:dachang/database/lock.go:43-64〕）：第一步先 `Create({"lock_name": name})`，`err == nil` 说明插进去了、唯一索引没撞，锁到手，立即返回 `true`，不阻塞也不等待；失败且 `expiration > 0` 时，用 `errors.As` 解出 `*mysql.MySQLError`，比对 `Number == 1062` 判定是唯一索引冲突，靠稳定错误码而不是字符串匹配（这个手法下文"想深入"的 W02-2 段还出现过一次：把 1062 转成包内业务错误）。判明撞锁之后，到拿到锁还要连续三步：按 `lock_name` `First` 查出旧记录；`time.Since(UpdateTime).Seconds() >= expiration` 才认定锁已过期；`Delete` 成功后递归调用 `Lock` 重抢。任何一步不满足都落到 `return false`。查过期、删旧、再插这三步不是原子操作：`Delete` 成功到再次 `Create` 之间可能被其他客户端插队，两个客户端也可能同时读到已过期的 `UpdateTime`、各自去删旧抢锁（课件没有做原子化处理，这条边界是从代码结构直接看出的）。还有一个 bug 要点名：`return true` 写在 `else` 分支里，而这个 `else` 覆盖的是"`err == nil` 或 `expiration <= 0`"两种情况，于是 `Create` 失败且调用方把 `expiration` 传成 0 时同样走进 `else` 返回 `true`，把失败误报为抢锁成功；测试只用 `expiration=3`，没有触达这个分支。释放锁就是 `Unlock(name)` 按 `lock_name` 删行（〔C:dachang/database/lock.go:66-69〕）。测试用 10 个协程模拟多进程抢锁，分三阶段验证：首轮竞争、睡 `1+Expire` 秒等锁过期后重抢、主动 `Unlock` 后重抢（〔C:dachang/database/lock_test.go:11-55〕）。文件尾的注释附送两组定义：乐观锁假设多数情况不冲突、提交时才检测（比如用 version 或 `update_time`），悲观锁总是假设最坏情况、高并发下容易大量阻塞甚至死锁；InnoDB 只有明确指定主键或索引的等值条件才走行锁，否则锁全表（〔C:dachang/database/lock.go:71-76〕）。

然后是注入对照实验。同一个登录查询两版（〔C:dachang/database/stmt.go:7-19〕）：LoginUnsafe 把用户输入直接拼进 `Where`："username='" + name + "' and password='" + passwd + "'"，输入进入 SQL 语法层；LoginSafe 改用占位符 `Where("username=? and password=?", name, passwd)`，参数在驱动层绑定。当密码位收到 `asdfghj' or '1'='1`，unsafe 版拼出 `SELECT count(*) FROM \`login\` WHERE username='jane' and password='asdfghj' or '1'='1'`（〔C:dachang/database/stmt_test.go:35〕注释原样）：载荷的前导单引号先闭合了 password 字符串，剩下的 `or '1'='1'` 成为独立的 OR 分支，SQL 里 AND 优先级高于 OR，整个条件变成（账号密码匹配）OR 恒真，恒真放行，登录成功。占位符版把同型载荷整体当成一个普通密码字符串参与等值比较，count 为 0，登录被拒：用户输入永远不进入 SQL 语法层，只在驱动层当数据。测试断言组合（〔C:dachang/database/stmt_test.go:20-63〕）：两个函数对正确密码（32 字符）都返回 `true`、对错误密码都返回 `false`；TestLoginUnsafe 断言注入载荷返回 `true`（中招），TestLoginSafe 断言同型载荷返回 `false`（免疫）；两个测试都开着 `PrepareStmt`，并假定 `login` 表里已有 jane 行（该表 DDL 在 go_basic 单元：两列 `varchar(100)`、无唯一索引，〔C:go_basic/database/create_table.sql:25-28〕）。占位符纪律在 minifeed 一段已经立过规矩，这里的对照实验给"为什么"补了现场。

最后是批量插入三阶梯，它把第 2 节的事务纪律反着演了一遍。三个函数都在同一个源文件里，各插 `INSERT_COUNT = 10000` 行（〔C:dachang/database/transaction.go:11-61〕）：InsertOneByOne 不开事务，逐条 `db.Create(&student)`，注释只提醒"注意需要传student的指针"；InsertByTransaction1 用 `tx := db.Begin()` 包住逐条 `tx.Create`，最后一次 `tx.Commit()`；InsertByTransaction2 在同一事务内每 100 行 `make` 一个 `[]Student` 后 `tx.Create(&students)`，一条 SQL 插 100 行，全程只有 100 条语句。实测（〔C:dachang/database/transaction_test.go:41-46〕输出注释）：逐条 `1m2.9626637s`（约 63 秒），纯事务 `1.795087s`（约 1.8 秒），事务加批量 `310.6484ms`，最快的和最慢的差约 200 倍；三个测试前置 `db.Delete(Student{}, "1=1")` 清表，`Student` 没有 DeletedAt 字段，是真删除。事务失败时谁回滚：没人。InsertByTransaction1 和 2 在循环中某行 `Create` 失败时只 `fmt.Println(err)` 后 `return`（transaction.go:34-37、54-57），既没有 `tx.Rollback()` 也没有 `tx.Commit()`，事务悬空，`Begin` 从池里拿的那条独占连接不会归还，连 `tx.Commit()` 的返回错误也没检查。这正是第 2 节 `WithTx` 加 `defer tx.Rollback()` 要防的场面，教学代码自己当了反面教材。为什么传指针：dachang 的注释只写了"注意需要传student的指针"，没有说原因；go_frame 单元的注释把原因说清楚了，"必须传指针，因为要给User的主键赋值。主键为0值时Create会自动给主键赋值，也会给CreatedAt字段赋值"（〔C:go_frame/orm/gorm/create.go:17〕），传值的话 GORM 拿不到可写的内存地址，主键回填无从谈起。为什么三档差 200 倍：dachang 测试的 `gorm.Config` 只开 `PrepareStmt`、没设 `SkipDefaultTransaction`，而 GORM 默认把写操作（创建、更新、删除）包进事务，禁用可提约 30%+ 性能（〔C:go_frame/orm/gorm/connection.go:36〕注释），所以逐条版相当于约 1 万次提交，事务版合并成 1 次提交，批量版再省到 100 条多值 INSERT，省的是往返和提交次数（这段机制是摘要归纳，源码只给出结构与耗时）。

## 小结

1. `sql.Open` 只登记参数不建连接，连接错误要等第一次查询或 `PingContext`；`*sql.DB` 是并发安全的池，全局一份。
2. 池三旋钮：`SetMaxOpenConns` 限流、`SetMaxIdleConns` 复用、`SetConnMaxLifetime` 换血；超限的查询排队，`db.Stats().WaitCount` 留痕。
3. 事务靠纪律：`WithTx` 加 `defer tx.Rollback()`，任何出口（包括 panic）都回滚；`Commit` 之后的 `Rollback` 是无害的 `ErrTxDone`。
4. 事务里只用 `tx`，事务要短，隔离级别显式写。
5. NULL 三策略：`sql.Null*`（看 `Valid`）、指针（nil 即无）、SQL 里 `COALESCE`；`ErrNoRows` 在 `Scan` 才冒出，`errors.Is` 判断。
6. GORM 快但接口收 `interface{}`，同参不同型（`Updates` 的 struct/map 之分）；sqlc 以 SQL 为真相源生成类型安全代码。
7. 树形评论用邻接表加 RootID 冗余：查子节点 `WHERE pid=?`，"更多回复"退化为 root_id 上的 keyset 分页；四种建模里它是查询、写入、删除的综合最优。
8. 表结构是服务器状态，用带序号的迁移文件管理；严格公司走 SQL 评审，小团队用 `AutoMigrate` 起步。
9. 全表遍历两式：深 offset 需要跳过前面的行；id 游标配合合适索引可以从边界继续。课件测得分别为 `92.646ms` 和 `26.5669ms`；游标写法必须显式按 id 排序，课件未写 `ORDER BY`。
10. MySQL 唯一键分布式锁：抢锁即向唯一索引列 insert，撞 1062 后走"查旧记录、判过期、删旧重抢"三步非原子；`Create` 失败且 `expiration<=0` 会误报成功；释放即按 `lock_name` 删行。
11. SQL 注入现场：拼接版被 `or '1'='1` 恒真放行，占位符版把载荷当普通字符串，输入永不进 SQL 语法层。
12. 批量插入三阶梯：逐条约 63 秒、事务包住约 1.8 秒、事务加每 100 行一条多值 INSERT 约 0.31 秒；教学代码事务内出错只打印就 `return`，无人回滚、连接悬着不还池，是 `WithTx` 纪律的反面教材；GORM 默认给每次写操作包隐式事务。

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

1. 同时持有连接的查询最多 2 个；其余查询若在连接释放前到达就会等待。等待次数和总耗时取决于调度与查询耗时，不保证恰好等待 4 次或正好分成三批。
2. 返回 `sql.ErrTxDone`，不产生任何效果。所以 `defer tx.Rollback()` 写在 `Commit` 之前是安全且必要的兜底。
3. GORM 对 `struct` 参数约定"零值等于没赋值"，因为分不清"没赋值"和"赋了零值"；要把字段更新成零值用 `Updates(map)`，`map` 的键显式列出了列名，没有歧义。
4. 连接被独占，持锁时间随外部接口的延迟和超时放大；外部服务一慢，池被慢慢占光，整个服务不可用。事务里只做数据库操作，外部调用放到事务外。

## 在项目里

- `dnsproxi-go` 是纯内存缓存，没有数据库，这里没有现成例子。
- minifeed 的 `user/store.go` 与 `post/post.go` 用 `database/sql` 加 `SQLiteStore` 实现了 repository 接口：占位符查询、`defer rows.Close()` 加 `rows.Err()`、`errors.Is(err, sql.ErrNoRows)` 转成包内哨兵错误 `ErrNotFound`，是本节纪律的最小落地（25 节）。练习包 `exercises/p9db` 里 `WithTx` 是本节事务纪律的可测试版本，`openTestDB` 展示了内存库配池的正确姿势。

## 想深入：正文之外的干货

### W02-2 GORM 落地与用户模块的存储细节

- GORM 的安装拆成两个包：本体 `gorm.io/gorm` 是接口定义，驱动是二次封装的 `gorm.io/driver/mysql`，驱动内部再包住 `go-sql-driver/mysql`；换 PostgreSQL 只换驱动包，模型代码不动。〔W02-2 p5-6〕
- 模型标签不用背：`primaryKey`、`unique`、`default`、`size`、`serializer:json`、`autoCreateTime` 这类标签面试不问，用时查官方文档；唯一要知道的是 `autoCreateTime` 作用在 `int` 字段上写的是 Unix 时间戳秒。〔W02-2 p9〕
- 公共字段组合 `gorm.Model`（ID、CreatedAt、UpdatedAt、DeletedAt）：只要结构体里有 `DeletedAt` 字段，这个模型就是软删除模型，`Delete` 变成改列而不是删行；实践中每家公司的公共字段都不一样，别照抄。〔W02-2 p8〕
- 表设计三要点：主键 `Id int64` 配 `primaryKey,autoIncrement` 让数据库生成主键；Email 加 `gorm:"unique"` 唯一索引防重复注册；GORM 生成的 DDL 里 Email 列是 `varchar(191)`，这个 191 是 utf8mb4 编码下索引长度限制的产物。〔W02-2 p21-23〕
- 唯一索引冲突怎么变成业务错误：DAO 层判 `errors.As` 出 `*mysql.MySQLError` 且 `Number == 1062`（MySQL 的重复键错误码），转成自己的 `ErrUserDuplicateEmail`；repository 和 service 层各用一行 `var ErrUserDuplicateEmail = dao.ErrUserDuplicateEmail` 别名转发，Handler 只依赖 service 层的错误，不跨层引 dao 类型。〔W02-2 p34-35〕
- 登录失败只返回一个统一的 `ErrInvalidUserOrPassword`："用户不存在"和"密码错误"分开提示，等于告诉攻击者哪些邮箱注册过，给了枚举的口子。〔W02-2 p39〕
- 密码加密的位置四选一都有理由：service（业务概念）、repository（"加密存储"）、dao（数据库自身加密）、domain（User 自己管）；课程选 service，理由是它是业务逻辑但不是 domain 的职责。选定之后连带决定登录时在哪儿调 `CompareHashAndPassword`。〔W02-2 p30〕
- 敏感信息防两类人：防研发（自己和同事）与防攻击者，密码连日志都不能打。〔W02-2 p29-30〕
- 算法三级递进：裸 md5 哈希、加盐或多轮哈希、BCrypt/PBKDF2 这类随机盐算法。选择标准就一条：难破解，同一密码密文不同，防彩虹表。〔W02-2 p31〕
- BCrypt 自带随机盐，cost 参数控制计算成本，校验使用 `CompareHashAndPassword`。注意 Go 的 `golang.org/x/crypto/bcrypt.GenerateFromPassword` 对超过 72 字节的密码返回错误，不能假定所有实现都静默截断；限制应按字节数与所用实现确定。cost 和机器负载共同决定延迟，应实测预算。〔W02-2 p32-33〕
- HTTP 无状态，所以登录要靠 Cookie 加 Session 记状态：Cookie 是浏览器本地的键值对，不安全，只放不关键的数据；Session 数据在服务端，浏览器只带一个 `sess_id`。认证是认 ID 不认人，ID 被偷就被冒充。Cookie 安全参数一句话版：Domain/Path 最小化、Max-Age 只留必要时间、HttpOnly 永远 true、Secure 生产永远 true、SameSite 尽量避免跨站；`sess_id` 放 Cookie 是最优解，禁 Cookie 的浏览器退而求其次放 Header 或查询参数。〔W02-2 p41-47〕
- gin-contrib/sessions 两件套：`sessions.Sessions("ssid", store)` 中间件负责从 Cookie 解出 Session，`CheckLogin` 中间件里 `sessions.Default(ctx).Get("userId") == nil` 就 401，signup/login 路径豁免。注意 `cookie.NewStore([]byte("secret"))` 是把 Session 数据加密后存在 Cookie 本身的客户端存储，数据大会撞 4K 上限，多实例要换 redis store。〔W02-2 p49-51〕
- 登录优化别上来就加缓存，压测先行。真实瓶颈里 bcrypt 的 100ms 量级延迟往往是第一位，属于第 11 节登录优化的内容。〔W02-2 p7〕

- 三层落地的透传骨架：repository 层 `UserRepository.Create` 先做类型转换（domain → dao 结构体）再调 `UserDAO.Insert`；`Insert` 内一行 `db.WithContext(ctx).Create(&u).Error`。先让调用链通起来，再逐层填业务。〔W02-2 p18〕

### W16-1 评论系统未入正文的取舍与配套

- 层级先选过一次：完整树形（嵌套任意深）还是两级简化（资源、评论、子评论），课程选完整树；两级的好处是删除和分页都简单（`DELETE WHERE parent=?`），代价是"评论的评论的评论"表达不了。〔W16-1 p6〕
- 根节点在邻接表里用 `pid IS NULL` 或非法值 `-1` 都能表示；查询直接评论时用 `pid IS NULL` 过滤，这是 min_id 分页那行 SQL 里 `AND pid IS NULL` 的来历。〔W16-1 p7〕
- Path 设计有两种解释约定：`path` 值含不含当前节点，含则父是倒数第二段，不含则父是最后一段；团队要统一，否则级联删除的 `LIKE` 前缀会写错。〔W16-1 p8〕
- Nested Set 是深坑的根源在于写入：每个节点的 `nsleft/nsright` 是全局 DFS 序，插入一个节点要把所有比它大的编号加二，等价于改半张表。〔W16-1 p9-10〕
- Closure Table 每个节点和每个祖先、每个后继各存一行，`TreePaths(ancestor, descendant)` 联合主键；深度 d 的链就有 d 行，数据量随深度线性爆炸。〔W16-1 p11-12〕
- 添加评论本身是普通插入，另一种设计是"直接评论"和"回复"拆成两个接口（或只在 gRPC 层拆），但无论哪种都要始终维持 root 和 parent 两个关键字段，查询性能全靠它们。〔W16-1 p17〕
- 异步写的代价要兑现给用户：写进 MQ 之后消费有延迟，用户发完评论可能查不到，前端要给"审核中/已发布"这样的过渡态；复合策略是平时直写、触发瓶颈才转异步。〔W16-1 p18〕
- 大厂不用外键的真实理由：级联删除在分库分表后失效、外键锁传播拖垮并发、运维不可控；课程演示 GORM 的 `constraint:OnDelete:CASCADE` 但生产多为应用层删除。〔W16-1 p21-22〕
- 查一级评论的子评论时，repository 层聚合即可；查子评论是可容错步骤，降级触发就跳过不查。用复杂子查询一条 SQL 拉全也能做到，但可读性太差，不值得。〔W16-1 p27〕
- 加载更多的缓存命中率天然低，因为大多数人不点；用户点了一次就预加载下一页写缓存，因为点过的人大概率要看完全部。〔W16-1 p32〕
- 读写分组部署：查 100 实例一个读组、增删 20 实例一个写组，客户端负载均衡各走各的；读组故障把部分流量导入写组，本质是分组加动态流量调度（`read_weight`/`write_weight` 可实现），代价是临时牺牲写吞吐。〔W16-1 p33〕
- 评论限流按资源热度分开：热门 400/s、非热门 100/s 各自限流；降级时只查热门资源的评论，非热门直接给空态。〔W16-1 p29〕

### W06-2 状态映射、Mongo 查询与索引

- 状态映射：领域状态最真实，存储里的状态是衍生/辅助；DAO 层直接写 `uint8` 魔法值可接受但不优雅，集中定义更好。〔W06-2 p10〕
- BSON 四类型各管一种形状：`bson.E` 是一个键值对，`bson.D` 是保留顺序的 E 切片，`bson.M` 是 `map[string]any`，`bson.A` 是表达 BSON 数组的切片。Value 可以继续嵌套这些类型。构造查询时先分清文档与数组，再把操作符放到对应的层。〔W06-2 p20-21〕

下面沿用课件的 Go driver v1 包路径：`bson` 来自 `go.mongodb.org/mongo-driver/bson`，`options` 来自 `go.mongodb.org/mongo-driver/mongo/options`。这些是函数体内的查询片段，`col` 是已经取得的集合，`ctx` 是调用方传入的 context。〔W06-2 p15〕〔W06-2 p20-21〕〔W06-2 p22-25〕

```go
conditions := bson.A{
	bson.D{{Key: "id", Value: int64(123)}},
	bson.D{{Key: "id", Value: int64(234)}},
}
orFilter := bson.D{{Key: "$or", Value: conditions}}

andFilter := bson.D{{Key: "$and", Value: bson.A{
	bson.D{{Key: "id", Value: int64(123)}},
	bson.D{{Key: "title", Value: "有标题"}},
}}}

inFilter := bson.D{{Key: "id", Value: bson.D{
	{Key: "$in", Value: bson.A{int64(123), int64(234)}},
}}}
projection := options.Find().SetProjection(bson.D{
	{Key: "id", Value: 1},
})
```

`orFilter` 表示两个 ID 任意一个匹配；`andFilter` 要求 ID 和标题同时匹配。两者的操作符都接一个条件数组，所以是 A 包 D，再由 D 里的 E 表达条件。`inFilter` 则先定位字段 id，再把 `$in` 文档放进这个字段的 Value。〔W06-2 p22-24〕

`col.Find(ctx, inFilter, projection)` 按条件找文档，并用 Projection 选择返回字段；课件用 `{id: 1}` 请求 id 字段。换成另外两个 filter，就得到 OR 或 AND 查询。Find 返回后先检查 error，再读取 cursor，并关闭 cursor；课程用 `cursor.All(ctx, &articles)` 把结果解码到切片，传入的是切片指针。〔W06-2 p22-25〕

- Mongo 建唯一索引一行：`col.Indexes().CreateOne(ctx, mongo.IndexModel{Keys: ..., Options: options.Index().SetUnique(true)})`，键按业务最常用的查询条件选。〔W06-2 p26〕

省略未吸收：W02-2 的 docker-compose 起 MySQL（K6，环境搭建，非数据库知识）、模型标签清单细节（K5，上面已给出查文档的结论）；W16-1 的面试要点页（K22）；W06-2 的 p2 为单元概览页，内容已由正文与本块覆盖。其余必读条目已融入正文或本块。

## 想深入去哪查

- GORM：W02-2（CRUD、软删除、钩子、三层分层、密码加密与 BCrypt）、`C:go_frame/orm`（`PrepareStmt`、读写分离、钩子、索引提示）。标准库：`C:go_basic/database`（`Exec`/`Query`/事务/批量插入/sqlbuilder）。
- 树形评论：W16-1 全文（四种建模、RootID 冗余、加载更多与 min_id 分页、级联删除与外键）。
- 旧长版：`notes/p9-database.md`（连接池实测、隔离级别、pgx 原生）、`notes/p11-sqlc-goose.md`（迁移、sqlc 生成）、`notes/p12-sqlc-pgx.md`（PG 类型、批量写入）。
