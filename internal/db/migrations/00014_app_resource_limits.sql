-- +goose Up
-- Per-app container resource caps. NULL means "use the instance-wide default"
-- (CARGO_DEFAULT_MEM_LIMIT / _CPU_LIMIT / _PIDS_LIMIT), resolved at deploy time.
-- Without these a single tenant app can OOM, fork-bomb, or CPU-starve the host
-- and take down the control plane and every other app with it.
ALTER TABLE applications
    ADD COLUMN mem_limit  TEXT,
    ADD COLUMN cpu_limit  TEXT,
    ADD COLUMN pids_limit INTEGER;

-- +goose Down
ALTER TABLE applications
    DROP COLUMN mem_limit,
    DROP COLUMN cpu_limit,
    DROP COLUMN pids_limit;
