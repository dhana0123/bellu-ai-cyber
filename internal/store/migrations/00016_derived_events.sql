-- +goose Up
-- derived_events is the append-only DERIVED log: inference (cross-source edges)
-- and classification (datastore labels). It is NEVER mixed into observed_events.
-- Replay layers these over the base graph: inferred edges carry inferred=true;
-- classifications become namespaced node properties.
--
-- Bi-temporal, mirroring observed_events: event_time is when the derivation
-- became true in the world; recorded_at is when Bellu produced it. The
-- change_type CHECK admits only derived change types and rejects observed ones,
-- which keeps the facts and derived lanes strictly separate.
CREATE TABLE derived_events (
    seq           BIGSERIAL PRIMARY KEY,          -- total order tiebreak within a (event_time, recorded_at)
    org_id        BIGINT NOT NULL,                -- tenant scope (mirrors connector tables)
    run_id        TEXT NOT NULL REFERENCES collection_runs(run_id),
    kind          TEXT NOT NULL,                  -- 'inferred_edge' | 'classification'
    change_type   TEXT NOT NULL,                  -- see CHECK below
    origin        TEXT NOT NULL,                  -- 'rule' | 'model'
    producer      TEXT NOT NULL DEFAULT '',       -- rule name or model id (e.g. 'rule:env-var-dependency')
    -- Target of the derivation.
    canonical_id  TEXT,                           -- classified entity, or inferred-edge src
    relation      TEXT,                           -- inferred edge relation (inferred_edge only)
    dst_id        TEXT,                           -- inferred edge dst (inferred_edge only)
    -- Derived payload: for classification -> {data_class, readers[], writers[]};
    -- for inferred_edge -> optional edge attributes.
    attributes    JSONB NOT NULL DEFAULT '{}'::jsonb,
    confidence    DOUBLE PRECISION NOT NULL DEFAULT 0.0,
    -- Evidence linkage: raw_event ids / observed_events seqs that justify it.
    evidence_ids  TEXT[] NOT NULL DEFAULT '{}',
    -- Bi-temporal.
    event_time    TIMESTAMPTZ NOT NULL,           -- when the derivation became true
    recorded_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT derived_events_change_type_chk CHECK (change_type IN (
        'inferred.added','inferred.removed','inferred.attribute_changed',
        'classified','reclassified','declassified'
    )),
    CONSTRAINT derived_events_shape_chk CHECK (
        (kind = 'inferred_edge' AND relation IS NOT NULL AND dst_id IS NOT NULL)
        OR (kind = 'classification' AND relation IS NULL AND dst_id IS NULL)
    )
);

-- Time-travel / history access paths (mirror observed_events).
CREATE INDEX derived_events_event_time_idx ON derived_events (org_id, event_time);
CREATE INDEX derived_events_recorded_idx   ON derived_events (org_id, recorded_at);
CREATE INDEX derived_events_target_idx     ON derived_events (org_id, canonical_id, event_time);
CREATE INDEX derived_events_run_idx        ON derived_events (run_id);

-- +goose Down
DROP TABLE derived_events;
