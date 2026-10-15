-- +goose Up
-- Durable discovery progress: persist the live per-table progress snapshot on a
-- discovery session so the UI can rehydrate progress after a browser reload or a
-- bellu-api restart. The progress JSON mirrors the discovery.Progress shape
-- (tables_total, tables_done, resources_collected, current_table, tables[]).
-- NULL means "no progress recorded yet". No secrets: only table names, counts,
-- states, and error strings are stored.
ALTER TABLE connector_discovery_sessions
    ADD COLUMN progress JSONB;

-- A session whose status is 'running' but whose owning process has gone away
-- (API restart/crash) is reconciled to 'interrupted' on startup. Index the
-- status lookup used by that reconciliation.
CREATE INDEX IF NOT EXISTS connector_discovery_status_idx
    ON connector_discovery_sessions (status);

-- +goose Down
DROP INDEX IF EXISTS connector_discovery_status_idx;
ALTER TABLE connector_discovery_sessions DROP COLUMN progress;
