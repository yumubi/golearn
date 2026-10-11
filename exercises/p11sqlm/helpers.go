// withTx + embed 迁移：练习的可复用件（主测试文件的陪衬，单独放避免噪音）。
package p11sqlm

import (
	"context"
	"database/sql"
	"embed"
	"errors"
)

// withTx 事务纪律助手——P9 问题 3 的同款 10 行（defer Rollback 幂等）。
func withTx(ctx context.Context, d *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// ErrRollback 显式回滚信号（示例代码里用 errors.New 即可，这里留档）。
var ErrRollback = errors.New("rollback")

//go:embed migrations/*.sql
var migrationsFS embed.FS
