-- name: CreateUser :one
INSERT INTO users (email, password_hash, is_instance_admin)
VALUES ($1, $2, NOT EXISTS (SELECT 1 FROM users))
RETURNING *;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: ListUsers :many
SELECT * FROM users ORDER BY created_at;
