-- +goose Up
-- One row per app per sample tick (~15s). Traffic columns are 0 when the app
-- has received no requests in the interval (or Traefik metrics are off).
CREATE TABLE app_metrics (
    app_id          UUID NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    cpu_pct         DOUBLE PRECISION NOT NULL DEFAULT 0,
    mem_bytes       BIGINT NOT NULL DEFAULT 0,
    mem_limit_bytes BIGINT NOT NULL DEFAULT 0,
    net_rx_bytes    BIGINT NOT NULL DEFAULT 0,
    net_tx_bytes    BIGINT NOT NULL DEFAULT 0,
    req_rate        DOUBLE PRECISION NOT NULL DEFAULT 0,
    err_rate        DOUBLE PRECISION NOT NULL DEFAULT 0,
    p50_ms          DOUBLE PRECISION NOT NULL DEFAULT 0,
    p95_ms          DOUBLE PRECISION NOT NULL DEFAULT 0
);
CREATE INDEX app_metrics_app_time_idx ON app_metrics (app_id, created_at DESC);

-- +goose Down
DROP TABLE app_metrics;
