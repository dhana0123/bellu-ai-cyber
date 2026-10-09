-- +goose Up
-- Organization-scope connector support: the target OU for StackSet deployment,
-- and a self-referential parent link so per-member-account connections expanded
-- from an organization connection point back at it. Still NO SECRETS.

ALTER TABLE connector_connections
    ADD COLUMN organizational_unit_id TEXT   NOT NULL DEFAULT '',
    ADD COLUMN parent_connection_id   BIGINT REFERENCES connector_connections(id) ON DELETE CASCADE;

CREATE INDEX connector_connections_parent_idx ON connector_connections (parent_connection_id);

-- +goose Down
DROP INDEX connector_connections_parent_idx;
ALTER TABLE connector_connections
    DROP COLUMN parent_connection_id,
    DROP COLUMN organizational_unit_id;
