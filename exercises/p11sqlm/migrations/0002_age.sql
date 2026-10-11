-- 迁移 0002：给 users 加 age 列——练习用。
-- 教程点：迁移是链，goose 按序号跑；改了 0001 是坏习惯，
-- 加 0002 才是版本化思维（≈ diesel migration add add_age）。

-- +goose Up
ALTER TABLE users ADD COLUMN age INTEGER;  -- 可空列，无默认值

-- +goose Down
-- sqlite 3.35+ 支持 DROP COLUMN
ALTER TABLE users DROP COLUMN age;
