-- +goose Up
-- unresolved_edges records every edge the resolver declined to resolve because
-- its source key, destination key, or both did not match any known entity.
-- Visibility, not auto-repair: the edge is still excluded from the graph, but
-- the failure is queryable so "no relationship" is distinguishable from "lost
-- relationship".
CREATE TABLE unresolved_edges (
    id             BIGSERIAL PRIMARY KEY,
    relation       TEXT NOT NULL,
    src_key        TEXT NOT NULL DEFAULT '',
    dst_key        TEXT NOT NULL DEFAULT '',
    unmatched_side TEXT NOT NULL,                -- 'src' | 'dst' | 'both'
    evidence_id    TEXT NOT NULL DEFAULT '',
    run_id         TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX unresolved_edges_relation_idx ON unresolved_edges (relation);
CREATE INDEX unresolved_edges_src_key_idx ON unresolved_edges (src_key);
CREATE INDEX unresolved_edges_dst_key_idx ON unresolved_edges (dst_key);
CREATE INDEX unresolved_edges_run_idx ON unresolved_edges (run_id);

-- dropped_entities records every entity a normalizer produced with an empty
-- Key, which the resolver cannot index and therefore excludes.
CREATE TABLE dropped_entities (
    id          BIGSERIAL PRIMARY KEY,
    kind        TEXT NOT NULL,
    provider    TEXT NOT NULL DEFAULT '',
    evidence_id TEXT NOT NULL DEFAULT '',
    run_id      TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX dropped_entities_kind_idx ON dropped_entities (kind);
CREATE INDEX dropped_entities_provider_idx ON dropped_entities (provider);
CREATE INDEX dropped_entities_run_idx ON dropped_entities (run_id);

-- graph_write_gaps records a batch submitted to Memgraph where the number of
-- relationships actually created or matched was less than the number of edges
-- submitted (an endpoint node was absent). Non-fatal; recorded for visibility.
CREATE TABLE graph_write_gaps (
    id          BIGSERIAL PRIMARY KEY,
    relation    TEXT NOT NULL,
    sync_run_id TEXT NOT NULL,
    batch_size  INTEGER NOT NULL,
    observed    INTEGER NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX graph_write_gaps_relation_idx ON graph_write_gaps (relation);
CREATE INDEX graph_write_gaps_sync_run_idx ON graph_write_gaps (sync_run_id);

-- +goose Down
DROP TABLE graph_write_gaps;
DROP TABLE dropped_entities;
DROP TABLE unresolved_edges;
