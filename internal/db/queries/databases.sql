-- name: CreateDatabaseInstance :one
INSERT INTO database_instances (
    org_id, name, engine, version, redis_mode, host_port, status, admin_secret
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
RETURNING *;

-- name: GetDatabaseInstance :one
SELECT * FROM database_instances WHERE id = $1;

-- name: ListDatabaseInstancesByOrg :many
SELECT * FROM database_instances WHERE org_id = $1 ORDER BY created_at;

-- name: SetDatabaseInstanceStatus :exec
UPDATE database_instances SET status = $2 WHERE id = $1;

-- name: DeleteDatabaseInstance :exec
DELETE FROM database_instances WHERE id = $1;

-- name: CreateDatabaseAttachment :one
INSERT INTO database_attachments (
    instance_id, app_id, db_name, role_name, acl_user, db_index, secret
) VALUES ($1,$2,$3,$4,$5,$6,$7)
RETURNING *;

-- name: GetDatabaseAttachment :one
SELECT * FROM database_attachments WHERE instance_id = $1 AND app_id = $2;

-- name: ListAttachmentsByInstance :many
SELECT * FROM database_attachments WHERE instance_id = $1 ORDER BY created_at;

-- name: ListAttachmentsByApp :many
SELECT sqlc.embed(database_attachments), di.engine, di.name AS instance_name, di.status
FROM database_attachments
JOIN database_instances di ON di.id = database_attachments.instance_id
WHERE database_attachments.app_id = $1
ORDER BY database_attachments.created_at;

-- name: DeleteDatabaseAttachment :exec
DELETE FROM database_attachments WHERE id = $1;

-- name: CountAttachmentsByInstance :one
SELECT COUNT(*) FROM database_attachments WHERE instance_id = $1;

-- name: ListUsedDbIndexes :many
SELECT db_index FROM database_attachments WHERE instance_id = $1 AND db_index IS NOT NULL ORDER BY db_index;
