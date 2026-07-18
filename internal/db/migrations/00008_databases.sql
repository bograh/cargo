-- +goose Up
CREATE TABLE database_instances (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    name TEXT NOT NULL,
    engine TEXT NOT NULL CHECK (engine IN ('postgres','redis')),
    version TEXT NOT NULL,
    redis_mode TEXT CHECK (redis_mode IN ('acl','shared')),
    host_port INT,
    status TEXT NOT NULL DEFAULT 'provisioning' CHECK (status IN ('provisioning','running','error','stopped')),
    admin_secret JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, name)
);

CREATE TABLE database_attachments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    instance_id UUID NOT NULL REFERENCES database_instances(id) ON DELETE CASCADE,
    app_id UUID NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    db_name TEXT,
    role_name TEXT,
    acl_user TEXT,
    db_index INT,
    secret JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (instance_id, app_id)
);

-- +goose Down
DROP TABLE database_attachments;
DROP TABLE database_instances;
