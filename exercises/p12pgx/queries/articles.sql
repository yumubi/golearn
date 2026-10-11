-- P12 查询：PG 特性专项。ASCII-only 注释（sqlc scanner bug，见 P11 正文问题 4）。

-- name: CreateArticle :one
INSERT INTO articles (title, tags, meta) VALUES ($1, $2, $3)
RETURNING id, title, tags, meta, views, created_at;

-- name: GetArticle :one
SELECT id, title, tags, meta, views, created_at FROM articles WHERE id = $1;

-- name: ListByTag :many
-- PG 数组操作符：tags 包含某元素（= ANY / @> 都行，@> 走 GIN 索引）
SELECT id, title, tags, meta, views, created_at FROM articles
WHERE tags @> $1 ORDER BY id;

-- name: BumpViews :execrows
UPDATE articles SET views = views + 1 WHERE id = $1;

-- name: TopArticles :many
SELECT id, title, tags, meta, views, created_at FROM articles
ORDER BY views DESC LIMIT $1;

-- name: DeleteArticle :execrows
DELETE FROM articles WHERE id = $1;
