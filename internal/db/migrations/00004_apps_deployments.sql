-- +goose Up
CREATE TABLE applications (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id             UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name               TEXT NOT NULL,
    slug               TEXT NOT NULL UNIQUE,
    source_type        TEXT NOT NULL CHECK (source_type IN ('git', 'image')),
    builder            TEXT NOT NULL DEFAULT 'auto' CHECK (builder IN ('auto', 'dockerfile', 'nixpacks')),
    git_repo_url       TEXT NOT NULL DEFAULT '',
    git_branch         TEXT NOT NULL DEFAULT '',
    image_ref          TEXT NOT NULL DEFAULT '',
    registry_creds_enc BYTEA,
    exposed_port       INTEGER NOT NULL DEFAULT 8080 CHECK (exposed_port BETWEEN 1 AND 65535),
    healthcheck_path   TEXT NOT NULL DEFAULT '/',
    auto_deploy        BOOLEAN NOT NULL DEFAULT true,
    build_context      TEXT NOT NULL DEFAULT '.',
    dockerfile_path    TEXT NOT NULL DEFAULT 'Dockerfile',
    build_args         JSONB NOT NULL DEFAULT '{}',
    key_version        INTEGER NOT NULL DEFAULT 1,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX applications_org_idx ON applications (org_id);

CREATE TABLE env_vars (
    app_id      UUID NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    key         TEXT NOT NULL,
    value_enc   BYTEA NOT NULL,
    key_version INTEGER NOT NULL DEFAULT 1,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (app_id, key)
);

CREATE TABLE deployments (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id      UUID NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    trigger     TEXT NOT NULL CHECK (trigger IN ('webhook', 'manual', 'rollback')),
    status      TEXT NOT NULL DEFAULT 'queued'
                CHECK (status IN ('queued', 'building', 'deploying', 'live', 'failed', 'cancelled')),
    commit_sha  TEXT NOT NULL DEFAULT '',
    image_tag   TEXT NOT NULL DEFAULT '',
    error       TEXT NOT NULL DEFAULT '',
    actor       UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at  TIMESTAMPTZ,
    finished_at TIMESTAMPTZ
);
CREATE INDEX deployments_app_idx ON deployments (app_id, created_at DESC);

-- +goose Down
DROP TABLE deployments;
DROP TABLE env_vars;
DROP TABLE applications;
