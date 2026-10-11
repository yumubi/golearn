// Package p9db 练习：database/sql 心智模型——CRUD / 事务 / NULL 三策略 / ctx 取消。
// 驱动用 modernc.org/sqlite（纯 Go 无 CGO，go test 直编）。
// 对照 notes/p9-database.md；Rust 侧 = sqlx::sqlite。
package p9db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	_ "modernc.org/sqlite" // database/sql 驱动注册：sql.Open("sqlite", dsn)
	"sync"
	"testing"
	"time"
)

// WithTx 事务纪律助手（notes §3 的 10 行，sqlx Drop 安全的手写替代）。
func WithTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() // Commit 后是幂等的 ErrTxDone，无害
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	// file::memory:?cache=shared：一个 :memory: 每个连接各一份库，
	// 连接池多连接会"看不到表"。共享缓存 + 上限 1 是 sqlite 内存库配池的正确姿势。
	db, err := sql.Open("sqlite", "file:p9db_test?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1) // 内存库单写者；测试要的"池并发"由 goroutine 侧排队体现
	t.Cleanup(func() { db.Close() })
	schema := `
	CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		deleted_at TIMESTAMP   -- 可空列：NULL 三策略的靶子
	);`
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	return db
}

// ---- CRUD + 占位符 ----

func TestCRUD(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	// INSERT
	res, err := db.ExecContext(ctx, "INSERT INTO users(id, name) VALUES($1, $2)", 1, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("rows affected = %d", n)
	}

	// QueryRow + ErrNoRows（notes §1 的坑：err 在 Scan 才冒出）
	var name string
	err = db.QueryRowContext(ctx, "SELECT name FROM users WHERE id = $1", 1).Scan(&name)
	if err != nil || name != "alice" {
		t.Fatalf("get: %v %q", err, name)
	}
	err = db.QueryRowContext(ctx, "SELECT name FROM users WHERE id = $1", 99).Scan(&name)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("want ErrNoRows, got %v", err)
	}

	// UPDATE
	if _, err := db.ExecContext(ctx, "UPDATE users SET name=$1 WHERE id=$2", "alice2", 1); err != nil {
		t.Fatal(err)
	}
	// DELETE
	if _, err := db.ExecContext(ctx, "DELETE FROM users WHERE id=$1", 1); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT name FROM users WHERE id=$1", 1).Scan(&name); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("after delete want ErrNoRows, got %v", err)
	}
}

// ---- NULL 三策略（notes §2）----

func TestNullStrategies(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seed := func(id int, name string, del any) {
		if _, err := db.ExecContext(ctx,
			"INSERT INTO users(id, name, deleted_at) VALUES($1,$2,$3)", id, name, del); err != nil {
			t.Fatal(err)
		}
	}
	seed(1, "live", nil)
	seed(2, "gone", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	// 策略一：sql.NullXxx
	var ns sql.NullString
	if err := db.QueryRowContext(ctx, "SELECT deleted_at FROM users WHERE id=$1", 1).Scan(&ns); err != nil {
		t.Fatal(err)
	}
	if ns.Valid {
		t.Fatal("live user should have NULL deleted_at")
	}
	if err := db.QueryRowContext(ctx, "SELECT deleted_at FROM users WHERE id=$1", 2).Scan(&ns); err != nil {
		t.Fatal(err)
	}
	if !ns.Valid || ns.String[:4] != "2026" {
		t.Fatalf("gone user deleted_at = %+v", ns)
	}

	// 策略二：指针
	var p *time.Time
	if err := db.QueryRowContext(ctx, "SELECT deleted_at FROM users WHERE id=$1", 1).Scan(&p); err != nil {
		t.Fatal(err)
	}
	if p != nil {
		t.Fatalf("pointer strategy: want nil, got %v", p)
	}

	// 策略三：COALESCE 消灭 NULL
	var s string
	if err := db.QueryRowContext(ctx,
		"SELECT COALESCE(deleted_at, '1970-01-01') FROM users WHERE id=$1", 1).Scan(&s); err != nil {
		t.Fatal(err)
	}
	if s[:4] != "1970" {
		t.Fatalf("coalesce = %q", s)
	}
}

// ---- 事务：原子性 + 回滚（notes §3）----

func TestTxAtomicity(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seedUsers(t, db, 3)

	err := WithTx(ctx, db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO users(id,name) VALUES($1,$2)", 10, "txrow"); err != nil {
			return err
		}
		return errors.New("boom") // 中途失败
	})
	if err == nil || err.Error() != "boom" {
		t.Fatalf("want boom, got %v", err)
	}
	// 回滚生效：id=10 不存在
	var n int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE id=10").Scan(&n); err != nil || n != 0 {
		t.Fatalf("tx not rolled back: n=%d err=%v", n, err)
	}

	// 提交路径：两条都进去
	if err := WithTx(ctx, db, func(tx *sql.Tx) error {
		for _, id := range []int{11, 12} {
			if _, err := tx.ExecContext(ctx, "INSERT INTO users(id,name) VALUES($1,$2)", id, fmt.Sprintf("u%d", id)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE id IN (11,12)").Scan(&n); err != nil || n != 2 {
		t.Fatalf("tx commit lost: n=%d err=%v", n, err)
	}
}

// ---- ctx 取消：查询中途叫停（notes §1 的 Context 谱系）----

func TestContextCancel(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// sqlite 没有内置 sleep 函数（PG 有 pg_sleep）；用递归 CTE 制造长查询
	if _, err := db.Exec("CREATE TABLE t(x)"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = db.QueryContext(ctx, "WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c WHERE x < 50000000) SELECT SUM(x) FROM c")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("long query should be cancelled")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("cancel took too long: %v（ctx 没生效？）", elapsed)
	}
}

// ---- 连接池并发（notes §1：*sql.DB 是池，天然并发安全）----

func TestPoolConcurrency(t *testing.T) {
	db := openTestDB(t)
	db.SetMaxOpenConns(4)
	ctx := context.Background()
	seedUsers(t, db, 20)

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for id := 1; id <= 20; id++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			var name string
			if err := db.QueryRowContext(ctx, "SELECT name FROM users WHERE id=$1", id).Scan(&name); err != nil {
				errs <- err
			}
		}(id)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}

func seedUsers(t *testing.T, db *sql.DB, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		if _, err := db.Exec("INSERT INTO users(id,name) VALUES($1,$2)", i, fmt.Sprintf("u%d", i)); err != nil {
			t.Fatal(err)
		}
	}
}
