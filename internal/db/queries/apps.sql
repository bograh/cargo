-- name: CreateApplication :one
INSERT INTO applications (
    org_id, name, slug, source_type, builder, git_repo_url, git_branch, image_ref,
    registry_creds_enc, exposed_port, healthcheck_path, auto_deploy,
    build_context, dockerfile_path, build_args
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
RETURNING *;

-- name: GetApplication :one
SELECT * FROM applications WHERE id = $1;

-- name: ListApplicationsForOrg :many
SELECT * FROM applications WHERE org_id = $1 ORDER BY created_at;

-- name: UpdateApplication :one
UPDATE applications SET
    name = $2, builder = $3, git_branch = $4, image_ref = $5,
    exposed_port = $6, healthcheck_path = $7, auto_deploy = $8,
    build_context = $9, dockerfile_path = $10, build_args = $11,
    registry_creds_enc = $12, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteApplication :exec
DELETE FROM applications WHERE id = $1;
