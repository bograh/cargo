-- name: InsertAuditLog :exec
INSERT INTO audit_log (actor_id, org_id, action, target_type, target_id, detail)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: ListAuditAll :many
SELECT a.id, a.actor_id, a.org_id, a.action, a.target_type, a.target_id,
       a.detail, a.created_at, u.email AS actor_email
FROM audit_log a LEFT JOIN users u ON u.id = a.actor_id
ORDER BY a.created_at DESC
LIMIT $1;

-- name: ListAuditByOrg :many
SELECT a.id, a.actor_id, a.org_id, a.action, a.target_type, a.target_id,
       a.detail, a.created_at, u.email AS actor_email
FROM audit_log a LEFT JOIN users u ON u.id = a.actor_id
WHERE a.org_id = $1
ORDER BY a.created_at DESC
LIMIT $2;

-- name: PurgeAuditLog :execrows
DELETE FROM audit_log WHERE created_at < $1;
