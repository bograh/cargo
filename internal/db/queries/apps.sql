-- name: CreateApplication :one
INSERT INTO applications (
    org_id, name, slug, source_type, builder, git_repo_url, git_branch, image_ref,
    registry_creds_enc, exposed_port, healthcheck_path, auto_deploy,
    build_context, dockerfile_path, build_args, mem_limit, cpu_limit, pids_limit,
    notify_on_success, deploy_strategy
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
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
    registry_creds_enc = $12, mem_limit = $13, cpu_limit = $14,
    pids_limit = $15, notify_on_success = $16, deploy_strategy = $17,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SetApplicationDesiredState :one
UPDATE applications SET desired_state = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteApplication :exec
DELETE FROM applications WHERE id = $1;

-- name: ListGitAppsByBranch :many
SELECT * FROM applications WHERE source_type = 'git' AND git_branch = $1;

-- name: ListAllAppIDs :many
SELECT id, slug FROM applications;

-- name: CreateAppMetric :exec
INSERT INTO app_metrics (
    app_id, cpu_pct, mem_bytes, mem_limit_bytes,
    net_rx_bytes, net_tx_bytes, req_rate, err_rate, p50_ms, p95_ms
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10);

-- name: ListAppMetricsSince :many
SELECT * FROM app_metrics
WHERE app_id = $1 AND created_at >= $2
ORDER BY created_at;

-- name: LatestAppMetric :one
SELECT * FROM app_metrics
WHERE app_id = $1
ORDER BY created_at DESC
LIMIT 1;

-- name: PurgeAppMetrics :execrows
DELETE FROM app_metrics WHERE created_at < now() - interval '48 hours';
