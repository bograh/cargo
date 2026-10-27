-- +goose Up
-- Where an app used to run and may still have containers. Moving an app to
-- another host (or between a host and the control plane) records the old
-- target here; the deploy that brings the app up on its new target tears the
-- old one down and deletes the row. A row that survives means that teardown
-- failed, so the next deploy retries it.
--
-- A table rather than a previous_host_id column: an app moved twice before a
-- deploy finishes has containers on two old targets, and one column can only
-- remember one of them.
--
-- target is 'control-plane' or a hosts.id. It carries no FK on purpose: a
-- deleted host has nothing left to tear down, which the retire pass treats as
-- done rather than as an error.
CREATE TABLE app_retirements (
    app_id     uuid        NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    target     text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (app_id, target)
);

-- +goose Down
DROP TABLE app_retirements;
