-- name: CreateOrganization :one
INSERT INTO organizations (name, slug) VALUES ($1, $2) RETURNING *;

-- name: GetOrganizationByID :one
SELECT * FROM organizations WHERE id = $1;

-- name: DeleteOrganization :exec
DELETE FROM organizations WHERE id = $1;

-- name: ListOrganizationsForUser :many
SELECT o.*, m.role FROM organizations o
JOIN memberships m ON m.org_id = o.id
WHERE m.user_id = $1
ORDER BY o.created_at;

-- name: ListAllOrganizations :many
SELECT * FROM organizations ORDER BY created_at;

-- name: CreateMembership :one
INSERT INTO memberships (org_id, user_id, role) VALUES ($1, $2, $3) RETURNING *;

-- name: GetMembership :one
SELECT * FROM memberships WHERE org_id = $1 AND user_id = $2;

-- name: ListMembers :many
SELECT m.org_id, m.user_id, m.role, m.created_at, u.email
FROM memberships m JOIN users u ON u.id = m.user_id
WHERE m.org_id = $1 ORDER BY m.created_at;

-- name: UpdateMembershipRole :one
UPDATE memberships SET role = $3 WHERE org_id = $1 AND user_id = $2 RETURNING *;

-- name: DeleteMembership :exec
DELETE FROM memberships WHERE org_id = $1 AND user_id = $2;

-- name: CountOwners :one
SELECT COUNT(*) FROM memberships WHERE org_id = $1 AND role = 'owner';
