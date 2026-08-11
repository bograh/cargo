-- +goose Up
-- One row per sample tick (~15s) describing the whole server, alongside the
-- per-app rows in app_metrics. Instance admins get a "is the host healthy"
-- view; app_metrics only ever answered "is this app healthy".
-- Retained for 48h by the housekeeping job, matching app_metrics.
CREATE TABLE host_metrics (
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    cpu_pct          DOUBLE PRECISION NOT NULL DEFAULT 0,
    mem_used_bytes   BIGINT NOT NULL DEFAULT 0,
    mem_total_bytes  BIGINT NOT NULL DEFAULT 0,
    disk_free_bytes  BIGINT NOT NULL DEFAULT 0,
    disk_total_bytes BIGINT NOT NULL DEFAULT 0,
    containers       INTEGER NOT NULL DEFAULT 0,
    running_apps     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX host_metrics_time_idx ON host_metrics (created_at DESC);

-- +goose Down
DROP TABLE host_metrics;
