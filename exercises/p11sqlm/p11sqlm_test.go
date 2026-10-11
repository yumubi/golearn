// Package p11sqlm 练习：sqlc 生成代码真用起来 + goose 迁移生命周期。
// 全部用 modernc.org/sqlite（纯 Go）+ 进程内迁移，无外部环境依赖。
//
// 考点（对照 notes/p11-sqlc-goose.md 小节）：
//   - 生成代码的形状：Queries 结构体、Params/Row 类型（§3）
//   - sql.NullXxx 在生成代码里的原生形态（§3.1）
//   - WithTx 事务纪律 + :execrows 影响行数（§3.2）
//   - goose 迁移 up/down/版本表（§4）
package p11sqlm

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"golearn/exercises/p11sqlm/db"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

// newMigratedDB 打开内存库并把 migrations/ 迁移到最新。
// DSN 同 P9 的坑：cache=shared + 单连接，否则池里第二连接看不到表。
func newMigratedDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := sql.Open("sqlite", "file:p11sqlm?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	d.SetMaxOpenConns(1)
	t.Cleanup(func() { d.Close() })

	goose.SetBaseFS(migrationsFS) // embed.FS——迁移文件编译进二进制（§4.3）
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(d, "migrations"); err != nil {
		t.Fatal(err)
	}
	return d
}

// TestGeneratedCRUD 走一遍生成代码的完整 CRUD（§3）。
func TestGeneratedCRUD(t *testing.T) {
	d := newMigratedDB(t)
	ctx := context.Background()
	q := db.New(d)

	// Create：Params 显式类型 + RETURNING 出 Row（sqlite 驱动支持 RETURNING）
	created, err := q.CreateUser(ctx, db.CreateUserParams{
		Email: "a@x.io",
		Name:  "alice",
		Bio:   sql.NullString{String: "hi", Valid: true},
		Age:   sql.NullInt64{Int64: 30, Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == 0 || created.CreatedAt.IsZero() {
		t.Fatalf("CreateUserRow = %+v", created)
	}

	// Get
	u, err := q.GetUser(ctx, created.ID)
	if err != nil || u.Email != "a@x.io" {
		t.Fatalf("GetUser = %+v, %v", u, err)
	}
	// NULL 语义：Bio 从 COALESCE 场景外的可空列回来是 NullString
	if !u.Bio.Valid || u.Bio.String != "hi" {
		t.Fatalf("Bio = %+v", u.Bio)
	}
	// ErrNoRows 依旧在 Scan 层冒出（生成代码不隐藏它）
	if _, err := q.GetUser(ctx, 999); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("want ErrNoRows, got %v", err)
	}

	// List 过滤参数
	young, err := q.ListUsersOlderThan(ctx, sql.NullInt64{Int64: 25, Valid: true})
	if err != nil || len(young) != 1 {
		t.Fatalf("ListUsersOlderThan = %d rows, %v", len(young), err)
	}

	// Update：:exec 无返回
	if err := q.UpdateBio(ctx, db.UpdateBioParams{Bio: sql.NullString{}, ID: created.ID}); err != nil {
		t.Fatal(err)
	}
	u2, _ := q.GetUser(ctx, created.ID)
	if u2.Bio.Valid {
		t.Fatalf("bio should be NULL now, got %+v", u2.Bio)
	}

	// Delete：:execrows 返回影响行数——存在性检查的经典姿势
	n, err := q.DeleteUser(ctx, created.ID)
	if err != nil || n != 1 {
		t.Fatalf("DeleteUser rows = %d, %v", n, err)
	}
	n, _ = q.DeleteUser(ctx, created.ID) // 再删一次
	if n != 0 {
		t.Fatalf("second delete rows = %d, want 0", n)
	}
}

// TestWithTxGenerated 生成代码配事务：WithTx 助手 + q.WithTx(tx)（§3.2）。
func TestWithTxGenerated(t *testing.T) {
	d := newMigratedDB(t)
	ctx := context.Background()
	q := db.New(d)

	err := withTx(ctx, d, func(tx *sql.Tx) error {
		qtx := q.WithTx(tx) // 同一查询集，事务句柄
		if _, err := qtx.CreateUser(ctx, db.CreateUserParams{Email: "t@x.io", Name: "tx"}); err != nil {
			return err
		}
		return errors.New("rollback me")
	})
	if err == nil {
		t.Fatal("want rollback error")
	}
	if n, _ := q.CountUsers(ctx); n != 0 {
		t.Fatalf("rollback failed: %d rows", n)
	}

	err = withTx(ctx, d, func(tx *sql.Tx) error {
		qtx := q.WithTx(tx)
		for _, em := range []string{"a@x.io", "b@x.io"} {
			if _, err := qtx.CreateUser(ctx, db.CreateUserParams{Email: em, Name: em}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := q.CountUsers(ctx); n != 2 {
		t.Fatalf("commit lost: %d rows", n)
	}
}

// TestGooseDownAndVersion 迁移生命周期：版本号、down、再 up（§4）。
func TestGooseDownAndVersion(t *testing.T) {
	d := newMigratedDB(t)

	v, err := goose.GetDBVersion(d)
	if err != nil {
		t.Fatal(err)
	}
	if v != 2 { // NNNN 文件名 → 版本号取序号：0002 → 2
		t.Fatalf("version = %d, want 2", v)
	}

	// down 一层 → 回到 0001；age 列消失
	if err := goose.Down(d, "migrations"); err != nil {
		t.Fatal(err)
	}
	v, _ = goose.GetDBVersion(d)
	if v != 1 {
		t.Fatalf("after down version = %d", v)
	}

	// 再 up → 回到最新
	if err := goose.Up(d, "migrations"); err != nil {
		t.Fatal(err)
	}
	if n, err := goose.GetDBVersion(d); err != nil || n != 2 {
		t.Fatalf("re-up version = %d, %v", n, err)
	}
}
