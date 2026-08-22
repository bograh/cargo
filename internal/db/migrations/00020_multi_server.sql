-- +goose Up
-- Worker hosts reachable over SSH (Phase 12a). Applications with
-- host_id NULL deploy to the control plane itself, which stays the default.
-- active_color replaces the per-host state.json: the reconciler owns it inside
-- the advisory-locked deploy transaction, so a restored backup can never
-- disagree with what any host is running.
CREATE TABLE hosts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL,
    address text NOT NULL,
    port int NOT NULL DEFAULT 22,
    private_key_enc bytea,
    key_version int NOT NULL DEFAULT 1,
    host_key_fingerprint text,
    status text NOT NULL DEFAULT 'pending',
    engine_version text,
    cpu_count int,
    mem_total_mb bigint,
    apps_domain_suffix text NOT NULL DEFAULT '',
    letsencrypt_email text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE applications ADD COLUMN host_id uuid REFERENCES hosts(id);
ALTER TABLE applications ADD COLUMN active_color text;
ALTER TABLE deployments ADD COLUMN host_id uuid REFERENCES hosts(id);

-- +goose Down
ALTER TABLE deployments DROP COLUMN host_id;
ALTER TABLE applications DROP COLUMN active_color;
ALTER TABLE applications DROP COLUMN host_id;
DROP TABLE hosts;
