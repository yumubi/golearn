// Command p9pgx 是 P9c 的 pgx 原生 API 冒烟：pgxpool + Query + batch。
// 门控：P9_PG_DSN 未设置则跳过（CI/离线友好）。
// 数据库：postgres:17-alpine docker（练习用一次性容器，用完即删）。
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	dsn := os.Getenv("P9_PG_DSN")
	if dsn == "" {
		fmt.Println("P9_PG_DSN 未设置，跳过 pgx 冒烟（这是设计行为）")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// pgxpool ≈ sqlx::PgPool；pgx 原生 API 不走 database/sql 抽象层
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS p9kv (
			k TEXT PRIMARY KEY,
			v TEXT NOT NULL,
			n BIGINT
		)`); err != nil {
		log.Fatalf("schema: %v", err)
	}

	// 批量写入：pgx.Batch 攒多条一次往返（对照 notes §4，10-100× 于循环 INSERT）
	batch := &pgx.Batch{}
	for i := range 5 {
		batch.Queue("INSERT INTO p9kv(k, v, n) VALUES($1,$2,$3) ON CONFLICT (k) DO UPDATE SET v=EXCLUDED.v, n=EXCLUDED.n",
			fmt.Sprintf("k%d", i), fmt.Sprintf("val%d", i), i)
	}
	br := pool.SendBatch(ctx, batch)
	for range 5 {
		if _, err := br.Exec(); err != nil {
			log.Fatalf("batch exec: %v", err)
		}
	}
	if err := br.Close(); err != nil {
		log.Fatalf("batch close: %v", err)
	}

	// 查询 + NULL 指针策略（pgx 原生直接支持 *int64）
	rows, err := pool.Query(ctx, "SELECT k, v, n FROM p9kv ORDER BY k")
	if err != nil {
		log.Fatalf("query: %v", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var k, v string
		var n *int64 // NULL → nil
		if err := rows.Scan(&k, &v, &n); err != nil {
			log.Fatalf("scan: %v", err)
		}
		if n == nil || *n != int64(count) {
			log.Fatalf("row %d: k=%s n=%v", count, k, n)
		}
		count++
	}
	if err := rows.Err(); err != nil { // 迭代中错误的出口（notes §4 的 ⚠️）
		log.Fatalf("rows: %v", err)
	}
	fmt.Printf("pgx 冒烟 OK：%d 行 batch 写入 + 指针 NULL 读取全部正确\n", count)
}
