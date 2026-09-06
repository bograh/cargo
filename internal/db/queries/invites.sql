-- name: CreateInvite :one
INSERT INTO invites (org_id, token_hash, role, expires_at, created_by, email)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetInviteByTokenHash :one
SELECT * FROM invites WHERE token_hash = $1;

-- name: ListInvitesForOrg :many
SELECT * FROM invites
WHERE org_id = $1 AND revoked_at IS NULL AND accepted_at IS NULL
ORDER BY created_at;

-- name: RevokeInvite :exec
UPDATE invites SET revoked_at = now() WHERE id = $1 AND org_id = $2;

-- name: PurgeInvites :execrows
DELETE FROM invites
WHERE expires_at < now() - interval '7 days'
   OR (revoked_at IS NOT NULL AND revoked_at < now() - interval '7 days');

-- name: MarkInviteAccepted :execrows
UPDATE invites SET accepted_at = now() WHERE id = $1 AND accepted_at IS NULL;
