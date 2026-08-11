-- name: CreateHostMetric :exec
INSERT INTO host_metrics (
    cpu_pct, mem_used_bytes, mem_total_bytes,
    disk_free_bytes, disk_total_bytes, containers, running_apps
) VALUES ($1,$2,$3,$4,$5,$6,$7);

-- name: ListHostMetricsSince :many
SELECT * FROM host_metrics
WHERE created_at >= $1
ORDER BY created_at;

-- name: LatestHostMetric :one
SELECT * FROM host_metrics
ORDER BY created_at DESC
LIMIT 1;

-- name: PurgeHostMetrics :execrows
DELETE FROM host_metrics WHERE created_at < now() - interval '48 hours';

-- name: ListLatestAppMetrics :many
-- The newest sample for every app that has one, with the app's identity, for
-- the all-apps overview. DISTINCT ON keeps this a single index scan rather
-- than one LatestAppMetric query per app.
SELECT DISTINCT ON (m.app_id)
    m.app_id, a.org_id, a.name, a.slug, a.desired_state,
    m.created_at, m.cpu_pct, m.mem_bytes, m.mem_limit_bytes,
    m.req_rate, m.err_rate, m.p95_ms
FROM app_metrics m
JOIN applications a ON a.id = m.app_id
ORDER BY m.app_id, m.created_at DESC;
