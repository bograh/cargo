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

-- name: ResolveAuditOrg :one
-- Maps a resource addressed outside /orgs/{orgID} to its owning org, so
-- mutations on apps, deployments and databases appear in the org audit view.
SELECT COALESCE(
    (SELECT a.org_id FROM applications a
      WHERE sqlc.arg(target_type)::text = 'apps' AND a.id = sqlc.arg(target_id)::uuid),
    (SELECT a.org_id FROM deployments d JOIN applications a ON a.id = d.app_id
      WHERE sqlc.arg(target_type)::text = 'deployments' AND d.id = sqlc.arg(target_id)::uuid),
    (SELECT i.org_id FROM database_instances i
      WHERE sqlc.arg(target_type)::text = 'databases' AND i.id = sqlc.arg(target_id)::uuid)
)::uuid AS org_id;
