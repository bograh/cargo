-- +goose Up
-- Desired runtime state for an app's container. 'running' (the default) means
-- the deploy pipeline should keep it up; 'stopped' means an operator has paused
-- it. A manual deploy sets this back to 'running'.
ALTER TABLE applications
    ADD COLUMN desired_state TEXT NOT NULL DEFAULT 'running'
        CHECK (desired_state IN ('running', 'stopped'));

-- +goose Down
ALTER TABLE applications DROP COLUMN desired_state;
