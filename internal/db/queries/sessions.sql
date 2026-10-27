-- name: CreateSession :one
INSERT INTO sessions (user_id, family_id, access_hash, refresh_hash, access_expires_at, refresh_expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetSessionByAccessHash :one
SELECT * FROM sessions WHERE access_hash = $1;

-- name: GetSessionByRefreshHash :one
SELECT * FROM sessions WHERE refresh_hash = $1;

-- name: MarkSessionRotated :exec
UPDATE sessions SET rotated_at = now() WHERE id = $1;

-- name: ClaimSessionRotation :execrows
-- Atomically claims the right to rotate a refresh token: exactly one of any
-- number of concurrent refreshes with the same token sees 1 row affected.
UPDATE sessions SET rotated_at = now()
WHERE id = $1 AND rotated_at IS NULL AND revoked_at IS NULL;

-- name: RevokeSessionFamily :exec
UPDATE sessions SET revoked_at = now() WHERE family_id = $1 AND revoked_at IS NULL;

-- name: PurgeSessions :execrows
DELETE FROM sessions
WHERE refresh_expires_at < now()
   OR (revoked_at IS NOT NULL AND revoked_at < now() - interval '24 hours');
