-- +goose Up
-- collection_runs tracks each ingest invocation and its lifecycle.
CREATE TABLE collection_runs (
    run_id      TEXT PRIMARY KEY,
    source      TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'running',  -- running | completed | failed
    started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ,
    event_count INTEGER NOT NULL DEFAULT 0
);

-- raw_events is append-only evidence. The unique constraint over the identity
-- tuple plus payload hash gives free dedupe when a source is re-collected.
CREATE TABLE raw_events (
    id            TEXT PRIMARY KEY,
    run_id        TEXT NOT NULL REFERENCES collection_runs(run_id),
    source        TEXT NOT NULL,
    collector     TEXT NOT NULL,
    provider      TEXT NOT NULL,
    scope         TEXT NOT NULL DEFAULT '',
    region        TEXT NOT NULL DEFAULT '',
    resource_type TEXT NOT NULL,
    resource_id   TEXT NOT NULL,
    observed_at   TIMESTAMPTZ NOT NULL,
    payload       JSONB NOT NULL,
    payload_hash  TEXT NOT NULL,
    ingested_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT raw_events_dedupe UNIQUE (source, resource_type, resource_id, payload_hash)
);

CREATE INDEX raw_events_run_idx ON raw_events (run_id);
CREATE INDEX raw_events_source_type_idx ON raw_events (source, resource_type);
CREATE INDEX raw_events_resource_idx ON raw_events (resource_id);

-- +goose Down
DROP TABLE raw_events;
DROP TABLE collection_runs;
