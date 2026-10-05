-- +goose Up
-- entity_aliases records that a source observed an entity under alias_key and
-- that it resolved to canonical_id. Time-bounded for reused keys (e.g. IPs).
CREATE TABLE entity_aliases (
    id           BIGSERIAL PRIMARY KEY,
    alias_key    TEXT NOT NULL,
    canonical_id TEXT NOT NULL,
    source       TEXT NOT NULL DEFAULT '',
    valid_from   TIMESTAMPTZ NOT NULL DEFAULT now(),
    valid_to     TIMESTAMPTZ,
    CONSTRAINT entity_aliases_uniq UNIQUE (alias_key, canonical_id, source)
);

CREATE INDEX entity_aliases_key_idx ON entity_aliases (alias_key);
CREATE INDEX entity_aliases_canonical_idx ON entity_aliases (canonical_id);

-- resolution_conflicts logs cases where the resolver declined to merge or found
-- inconsistent evidence. Nothing is auto-merged on weak keys.
CREATE TABLE resolution_conflicts (
    id         BIGSERIAL PRIMARY KEY,
    key        TEXT NOT NULL,
    reason     TEXT NOT NULL,
    detail     TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE resolution_conflicts;
DROP TABLE entity_aliases;
