-- +goose Up
-- Widen the managed-database engine set to include mysql and mongodb.
ALTER TABLE database_instances DROP CONSTRAINT database_instances_engine_check;
ALTER TABLE database_instances
    ADD CONSTRAINT database_instances_engine_check
    CHECK (engine IN ('postgres','redis','mysql','mongodb'));

-- +goose Down
ALTER TABLE database_instances DROP CONSTRAINT database_instances_engine_check;
ALTER TABLE database_instances
    ADD CONSTRAINT database_instances_engine_check
    CHECK (engine IN ('postgres','redis'));
