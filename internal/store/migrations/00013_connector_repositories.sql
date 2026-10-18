-- +goose Up
-- connector_repositories lists the repositories a git connector (github /
-- gitlab / bitbucket) can access, and which ones the operator selected for
-- Bellu to collect. On connect, every accessible repo is inserted selected =
-- true (collect all by default); the operator can toggle individual repos or
-- switch the connection to "selected repos only".
CREATE TABLE connector_repositories (
    id             BIGSERIAL PRIMARY KEY,
    connection_id  BIGINT NOT NULL REFERENCES connector_connections(id) ON DELETE CASCADE,
    external_id    TEXT NOT NULL,              -- provider repo id (stable)
    full_name      TEXT NOT NULL,              -- owner/repo (or group/project)
    url            TEXT NOT NULL DEFAULT '',
    default_branch TEXT NOT NULL DEFAULT '',
    private        BOOLEAN NOT NULL DEFAULT true,
    selected       BOOLEAN NOT NULL DEFAULT true,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT connector_repositories_uniq UNIQUE (connection_id, external_id)
);

CREATE INDEX connector_repositories_conn_idx ON connector_repositories (connection_id);

-- repo_selection_mode: 'all' (collect every accessible repo, including ones
-- added later) or 'selected' (collect only repos with selected = true).
ALTER TABLE connector_connections
    ADD COLUMN repo_selection_mode TEXT NOT NULL DEFAULT 'all';

-- +goose Down
ALTER TABLE connector_connections DROP COLUMN repo_selection_mode;
DROP TABLE connector_repositories;
