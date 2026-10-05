-- +goose Up
-- Normalized canonical entities. Keyed by canonical_id; one row per node.
CREATE TABLE normalized_entities (
    canonical_id TEXT PRIMARY KEY,
    kind         TEXT NOT NULL,
    provider     TEXT NOT NULL DEFAULT '',
    name         TEXT NOT NULL DEFAULT '',
    attributes   JSONB NOT NULL DEFAULT '{}'::jsonb,
    first_seen   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen    TIMESTAMPTZ NOT NULL DEFAULT now(),
    stale        BOOLEAN NOT NULL DEFAULT false
);

CREATE INDEX normalized_entities_kind_idx ON normalized_entities (kind);

-- Normalized canonical edges. One row per (src, relation, dst).
CREATE TABLE normalized_edges (
    id         BIGSERIAL PRIMARY KEY,
    src_id     TEXT NOT NULL,
    relation   TEXT NOT NULL,
    dst_id     TEXT NOT NULL,
    attributes JSONB NOT NULL DEFAULT '{}'::jsonb,
    confidence DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    first_seen TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen  TIMESTAMPTZ NOT NULL DEFAULT now(),
    stale      BOOLEAN NOT NULL DEFAULT false,
    CONSTRAINT normalized_edges_uniq UNIQUE (src_id, relation, dst_id)
);

CREATE INDEX normalized_edges_src_idx ON normalized_edges (src_id);
CREATE INDEX normalized_edges_dst_idx ON normalized_edges (dst_id);

-- Evidence linking graph elements to the raw events that justify them. The
-- full evidence lives here; the graph only stores the ids, keeping it lean.
CREATE TABLE evidence (
    id           BIGSERIAL PRIMARY KEY,
    element_kind TEXT NOT NULL,                 -- 'entity' | 'edge'
    element_id   TEXT NOT NULL,                 -- canonical_id or edge id (text)
    raw_event_id TEXT NOT NULL REFERENCES raw_events(id),
    source       TEXT NOT NULL,
    collector    TEXT NOT NULL,
    run_id       TEXT NOT NULL,
    observed_at  TIMESTAMPTZ NOT NULL,
    CONSTRAINT evidence_uniq UNIQUE (element_kind, element_id, raw_event_id)
);

CREATE INDEX evidence_element_idx ON evidence (element_kind, element_id);
CREATE INDEX evidence_raw_idx ON evidence (raw_event_id);

-- +goose Down
DROP TABLE evidence;
DROP TABLE normalized_edges;
DROP TABLE normalized_entities;
