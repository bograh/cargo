-- name: CreateInvite :one
INSERT INTO invites (org_id, token_hash, role, expires_at, created_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetInviteByTokenHash :one
SELECT * FROM invites WHERE token_hash = $1;

-- name: ListInvitesForOrg :many
SELECT * FROM invites WHERE org_id = $1 AND revoked_at IS NULL ORDER BY created_at;

-- name: RevokeInvite :exec
UPDATE invites SET revoked_at = now() WHERE id = $1 AND org_id = $2;
