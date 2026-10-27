-- +goose Up
-- deployments.host_id is an audit trail of where each version ran. With the
-- default NO ACTION, a host that had ever run a deployment could never be
-- deleted (hosts.Delete only checks applications.host_id). Forget the host on
-- its old deployment rows instead.
ALTER TABLE deployments DROP CONSTRAINT deployments_host_id_fkey;
ALTER TABLE deployments ADD CONSTRAINT deployments_host_id_fkey
    FOREIGN KEY (host_id) REFERENCES hosts(id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE deployments DROP CONSTRAINT deployments_host_id_fkey;
ALTER TABLE deployments ADD CONSTRAINT deployments_host_id_fkey
    FOREIGN KEY (host_id) REFERENCES hosts(id);
