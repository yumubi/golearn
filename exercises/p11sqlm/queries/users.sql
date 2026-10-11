-- Named queries for sqlc: each annotated statement becomes one Go method.
-- SQL-first: write correct SQL, sqlc generates typed wrappers.
-- Wrong columns/types fail at generate time (Go's answer to sqlx's compile-time checks).
-- NOTE: keep comments in this file ASCII-only. sqlc v1.31.1's comment scanner
-- mis-parses the NEXT -- name: annotation when a preceding comment line is long
-- CJK text (annotation loses its :one/:many suffix -> generate error).
-- Repro + analysis kept in git history; upstream bug worth filing.

-- name: CreateUser :one
INSERT INTO users (email, name, bio, age) VALUES (?, ?, ?, ?)
RETURNING id, email, name, bio, age, created_at;

-- name: GetUser :one
SELECT id, email, name, bio, age, created_at FROM users WHERE id = ?;

-- name: GetUserByEmail :one
SELECT id, email, name, bio, age, created_at FROM users WHERE email = ?;

-- name: ListUsers :many
SELECT id, email, name, bio, age, created_at FROM users ORDER BY id;

-- name: ListUsersOlderThan :many
SELECT id, email, name, bio, age, created_at FROM users WHERE age > ? ORDER BY id;

-- name: UpdateBio :exec
UPDATE users SET bio = ? WHERE id = ?;

-- name: DeleteUser :execrows
DELETE FROM users WHERE id = ?;

-- name: CountUsers :one
SELECT COUNT(*) FROM users;
