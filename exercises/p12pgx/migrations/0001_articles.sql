-- P12 迁移 0001：articles 表——覆盖 PG 特有类型（text[] 数组、jsonb、timestamptz）。
-- 这些类型是 database/sql 抽象层的盲区，正是 pgx/v5 + sqlc 组合的价值所在。

-- +goose Up
CREATE TABLE articles (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    title      TEXT NOT NULL,
    tags       TEXT[] NOT NULL DEFAULT '{}',          -- PG 数组：database/sql 要手动 parse
    meta       JSONB NOT NULL DEFAULT '{}',           -- jsonb：Go 侧 map/struct 直接进出
    views      BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE articles;
