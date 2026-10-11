// Package p12pgx 练习：sqlc(pgx/v5) 生成代码 + pgxpool 真连 PostgreSQL。
// 门控：P12_PG_DSN 未设置则 skip——CI/离线友好；有 PG 时验证 PG 特有类型。
//
// 考点（对照 notes/p12-sqlc-pgx.md）：
//   - 生成代码换血：pgxpool/pgx.Tx，不是 database/sql（§2）
//   - PG 类型原生映射：text[] → []string、jsonb → []byte、timestamptz（§3）
//   - COPY 级 batch 写入（pgx.Batch）配生成查询（§4）
//   - P11 的 withTx 纪律在 pgx.Tx 上重写（§5）
package p12pgx

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"golearn/exercises/p12pgx/db"

	"github.com/jackc/pgx/v5/pgxpool"
)

// newPool 起真连接池 + 跑迁移。DSN 从 P12_PG_DSN 读。
func newPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("P12_PG_DSN")
	if dsn == "" {
		t.Skip("P12_PG_DSN 未设置，跳过（go test 默认离线安全）")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	// 迁移：教程版直接 Exec schema（多文件时换 goose，同 P11）
	if _, err := pool.Exec(context.Background(), schemaSQL); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DROP TABLE IF EXISTS articles")
	})
	return pool
}

const schemaSQL = `
CREATE TABLE IF NOT EXISTS articles (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    title      TEXT NOT NULL,
    tags       TEXT[] NOT NULL DEFAULT '{}',
    meta       JSONB NOT NULL DEFAULT '{}',
    views      BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);`

func seedArticle(t *testing.T, q *db.Queries, title string, tags []string, meta map[string]any) db.Article {
	t.Helper()
	raw, _ := json.Marshal(meta)
	a, err := q.CreateArticle(context.Background(), db.CreateArticleParams{
		Title: title,
		Tags:  tags,
		Meta:  raw,
	})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// TestPGTypes：text[] / jsonb / timestamptz 原生进出（§3）。
func TestPGTypes(t *testing.T) {
	pool := newPool(t)
	q := db.New(pool)
	ctx := context.Background()

	meta := map[string]any{"author": "alice", "words": 1200}
	a := seedArticle(t, q, "hello pgx", []string{"go", "db"}, meta)

	// 数组原生读写：[]string 直接进出到扫描，无中间 parse
	got, err := q.GetArticle(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "go" {
		t.Fatalf("Tags = %v", got.Tags)
	}
	// jsonb 回来是 []byte（[]byte 是 jsonb 的默认映射）；unmarshal 验内容
	var back map[string]any
	if err := json.Unmarshal(got.Meta, &back); err != nil {
		t.Fatal(err)
	}
	if back["author"] != "alice" {
		t.Fatalf("meta = %v", back)
	}
	// timestamptz → pgtype.Timestamptz：Valid + 时间合理
	if !got.CreatedAt.Valid || time.Since(got.CreatedAt.Time) > time.Minute {
		t.Fatalf("CreatedAt = %+v", got.CreatedAt)
	}

	// 数组包含查询（@> 操作符，GIN 索引友好）
	tagged, err := q.ListByTag(ctx, []string{"db"})
	if err != nil || len(tagged) != 1 {
		t.Fatalf("ListByTag = %d, %v", len(tagged), err)
	}
}

// TestExecRowsAndTop：影响行数 + 排序分页（§3）。
func TestExecRowsAndTop(t *testing.T) {
	pool := newPool(t)
	q := db.New(pool)
	ctx := context.Background()

	a := seedArticle(t, q, "hot", []string{"x"}, nil)
	n, err := q.BumpViews(ctx, a.ID)
	if err != nil || n != 1 {
		t.Fatalf("BumpViews = %d, %v", n, err)
	}
	n, _ = q.BumpViews(ctx, 99999) // 不存在的 id
	if n != 0 {
		t.Fatalf("bump missing rows = %d, want 0", n)
	}

	seedArticle(t, q, "cold", []string{"y"}, nil)
	top, err := q.TopArticles(ctx, 1)
	if err != nil || len(top) != 1 || top[0].Title != "hot" {
		t.Fatalf("TopArticles = %+v, %v", top, err)
	}
}

// TestBatchInsert：pgx.Batch 攒 N 条 INSERT 一次网络往返（§4）。
func TestBatchInsert(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()

	titles := make([]string, 20)
	for i := range titles {
		titles[i] = "batch-" + strconv.Itoa(i)
	}
	if err := batchInsert(ctx, pool, titles); err != nil {
		t.Fatal(err)
	}
	q := db.New(pool)
	top, err := q.TopArticles(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 20 {
		t.Fatalf("batch inserted %d rows, want 20", len(top))
	}
	for _, a := range top {
		if len(a.Tags) != 1 || a.Tags[0] != "batch" {
			t.Fatalf("row %d tags = %v", a.ID, a.Tags)
		}
	}
}

// TestWithTxPgx：P11 的 withTx 纪律移植到 pgx.Tx（§5）。
func TestWithTxPgx(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	q := db.New(pool)

	err := withTxPgx(ctx, pool, func(tx pgxTx) error {
		qtx := q.WithTx(tx)
		if _, err := qtx.CreateArticle(ctx, db.CreateArticleParams{
			Title: "in-tx", Tags: []string{}, Meta: []byte(`{}`),
		}); err != nil {
			return err
		}
		return errors.New("rollback me")
	})
	if err == nil {
		t.Fatal("want rollback")
	}
	top, _ := q.TopArticles(ctx, 100)
	if len(top) != 0 {
		t.Fatalf("rollback failed: %d rows", len(top))
	}

	err = withTxPgx(ctx, pool, func(tx pgxTx) error {
		qtx := q.WithTx(tx)
		for _, title := range []string{"a", "b"} {
			if _, err := qtx.CreateArticle(ctx, db.CreateArticleParams{
				Title: title, Tags: []string{}, Meta: []byte(`{}`),
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	top2, _ := q.TopArticles(ctx, 10)
	if len(top2) != 2 {
		t.Fatalf("commit lost: %d rows", len(top2))
	}
}
