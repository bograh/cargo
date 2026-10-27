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

-- name: AnyUserExists :one
SELECT EXISTS (SELECT 1 FROM users);

-- name: LockUserCreation :exec
-- Serialises account creation for the rest of the transaction. CreateUser
-- decides instance-admin by "no users exist yet"; under READ COMMITTED two
-- concurrent first registrations would both see an empty table and both
-- become admin. Taking this lock first, then inserting in a later statement
-- (fresh snapshot), makes that check exact.
SELECT pg_advisory_xact_lock(hashtext('cargo.users.create'));
