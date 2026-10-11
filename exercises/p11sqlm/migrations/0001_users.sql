-- 迁移 0001：users 表 + 唯一索引。
-- goose 约定：文件名 NNNN_name.sql，用 Up / Down 分区。
-- Down 不是可选项，回退时跑它，写不出回退的迁移是坏迁移。

-- +goose Up
CREATE TABLE users (
    id    INTEGER PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    name  TEXT NOT NULL,
    bio   TEXT,                -- 可空：NULL 三策略在生成代码里的形态见 queries
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_users_email ON users(email);

-- +goose Down
DROP INDEX idx_users_email;
DROP TABLE users;
