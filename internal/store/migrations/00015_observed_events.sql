-- +goose Up
-- observed_events is the append-only FACTS log. One row per classified change
-- a source asserted about an entity or edge during a run. It is the source of
-- truth for the base graph; Memgraph is a replayable projection of it.
--
-- Bi-temporal: event_time is when the fact became true in the world;
-- recorded_at is when Bellu learned it (ingest time). Default (reality) queries
-- range over event_time; asKnownAt(t) adds recorded_at <= t (audit view).
CREATE TABLE observed_events (
    seq           BIGSERIAL PRIMARY KEY,          -- total order tiebreak within a (event_time, recorded_at)
    org_id        BIGINT NOT NULL,                -- tenant scope (mirrors connector tables)
    run_id        TEXT NOT NULL REFERENCES collection_runs(run_id),
    source        TEXT NOT NULL,                  -- producing collector family: cloudquery, syft, zeek...
    element_kind  TEXT NOT NULL,                  -- 'entity' | 'edge'
    change_type   TEXT NOT NULL,                  -- see CHECK below
    -- Identity of the changed element.
    canonical_id  TEXT,                           -- entity canonical_id, or edge src for edge changes
    relation      TEXT,                           -- edge relation (edge changes only)
    dst_id        TEXT,                           -- edge dst (edge changes only)
    -- Payload of the change. For attribute_updated/attribute_changed this is the
    -- new attribute set (full snapshot) plus an optional diff; empty for deleted.
    attributes    JSONB NOT NULL DEFAULT '{}'::jsonb,
    diff          JSONB NOT NULL DEFAULT '{}'::jsonb,   -- {added,removed,changed} keys, informational
    confidence    DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    -- Evidence linkage: the raw events that justify this change (ids into raw_events).
    evidence_ids  TEXT[] NOT NULL DEFAULT '{}',
    -- Bi-temporal.
    event_time    TIMESTAMPTZ NOT NULL,           -- when the fact became true (defaults to observed_at)
    recorded_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT observed_events_change_type_chk CHECK (change_type IN (
        'entity.created','entity.attribute_updated','entity.state_changed',
        'entity.deleted','entity.unchanged',
        'relation.added','relation.removed','relation.attribute_changed'
    )),
    CONSTRAINT observed_events_edge_shape_chk CHECK (
        (element_kind = 'entity' AND relation IS NULL AND dst_id IS NULL)
        OR (element_kind = 'edge' AND relation IS NOT NULL AND dst_id IS NOT NULL)
    )
);

-- Time-travel / history access paths.
CREATE INDEX observed_events_event_time_idx  ON observed_events (org_id, event_time);
CREATE INDEX observed_events_recorded_idx    ON observed_events (org_id, recorded_at);
CREATE INDEX observed_events_entity_idx      ON observed_events (org_id, canonical_id, event_time);
CREATE INDEX observed_events_edge_idx        ON observed_events (org_id, canonical_id, relation, dst_id, event_time);
CREATE INDEX observed_events_run_idx         ON observed_events (run_id);

-- Reference-counted liveness per source: who currently asserts an entity.
-- An entity is deleted (entity.deleted written) only when this set becomes empty.
CREATE TABLE entity_assertions (
    org_id       BIGINT NOT NULL,
    canonical_id TEXT NOT NULL,
    source       TEXT NOT NULL,
    last_run_id  TEXT NOT NULL,
    last_seen    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, canonical_id, source)
);
CREATE INDEX entity_assertions_entity_idx ON entity_assertions (org_id, canonical_id);

-- +goose Down
DROP TABLE entity_assertions;
DROP TABLE observed_events;
