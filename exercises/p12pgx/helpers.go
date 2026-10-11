// helpers：pgx 侧的可复用件（withTx 纪律 + batch 攒批）。
package p12pgx

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgxTx / pgxPool 别名：测试文件里少一串 import。
type pgxTx = pgx.Tx
type pgxPool = pgxpool.Pool

// withTxPgx P11 的 withTx 在 pgx.Tx 上的形态：
// pgx 事务 Begin → defer Rollback（Commit 后幂等）→ fn → Commit。
// 注意：pgx 的 Rollback/Commit 都要传 ctx（database/sql 版不传）——
// 传被取消的 ctx 会让收尾失败，惯用 context.WithoutCancel(ctx)。
func withTxPgx(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// batchInsert 用 pgx.Batch + 位置参数 SQL：N 条 INSERT 一次网络往返。
// sqlc 生成的 SQL 常量是小写未导出（createArticle），跨包不可用——
// batch 场景直接写 SQL 或把 batch 生成也交给 sqlc（:copyfrom / batchexec）。
func batchInsert(ctx context.Context, pool *pgxpool.Pool, titles []string) error {
	batch := &pgx.Batch{}
	for _, title := range titles {
		batch.Queue(`INSERT INTO articles (title, tags, meta) VALUES ($1, $2, $3)`,
			title, []string{"batch"}, []byte(`{}`))
	}
	br := pool.SendBatch(ctx, batch)
	defer br.Close()
	for range titles {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return nil
}
