-- +goose Up
-- GitHub App integration (Stage B): installation records, CSRF-safe single-use
-- install state, webhook delivery dedupe, encrypted credentials (GitLab/
-- Bitbucket), and GitHub-specific delta columns on connector_repositories.

-- github_installations binds a verified GitHub App installation to a tenant
-- (org_id) and a connector connection. The installation id is globally unique
-- per App, enforced by github_installations_inst_uniq so processing is
-- idempotent.
CREATE TABLE github_installations (
    id                   BIGSERIAL PRIMARY KEY,
    org_id               BIGINT NOT NULL,
    connection_id        BIGINT NOT NULL REFERENCES connector_connections(id) ON DELETE CASCADE,
    github_installation_id BIGINT NOT NULL,
    github_account_id    BIGINT NOT NULL,
    github_account_login TEXT NOT NULL,
    github_account_type  TEXT NOT NULL,              -- User | Organization
    status               TEXT NOT NULL DEFAULT 'active', -- active|suspended|deleted|pending
    repo_selection_mode  TEXT NOT NULL DEFAULT 'all',
    granted_permissions  JSONB NOT NULL DEFAULT '{}',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_verified_at     TIMESTAMPTZ,
    CONSTRAINT github_installations_inst_uniq UNIQUE (github_installation_id)
);
CREATE INDEX github_installations_org_idx ON github_installations (org_id);
CREATE INDEX github_installations_conn_idx ON github_installations (connection_id);

-- github install state (CSRF-safe, single-use). The callback validates and
-- marks a state used in one transaction; the resulting installation's tenant
-- comes from org_id recorded here, never from the browser.
CREATE TABLE github_install_states (
    state         TEXT PRIMARY KEY,
    org_id        BIGINT NOT NULL,
    user_id       BIGINT NOT NULL,
    connection_id BIGINT NOT NULL REFERENCES connector_connections(id) ON DELETE CASCADE,
    used          BOOLEAN NOT NULL DEFAULT false,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ NOT NULL
);

-- delta columns on the existing connector_repositories
ALTER TABLE connector_repositories ADD COLUMN github_repo_id BIGINT;
ALTER TABLE connector_repositories ADD COLUMN owner TEXT NOT NULL DEFAULT '';
ALTER TABLE connector_repositories ADD COLUMN visibility TEXT NOT NULL DEFAULT '';
ALTER TABLE connector_repositories ADD COLUMN sync_status TEXT NOT NULL DEFAULT 'pending';
ALTER TABLE connector_repositories ADD COLUMN last_synced_at TIMESTAMPTZ;
ALTER TABLE connector_repositories ADD COLUMN last_error TEXT NOT NULL DEFAULT '';

CREATE TABLE github_webhook_deliveries (
    delivery_id   TEXT PRIMARY KEY,          -- X-GitHub-Delivery, dedupe/idempotency
    installation_id BIGINT NOT NULL,
    event_type    TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'received', -- received|processed|failed|ignored
    received_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at  TIMESTAMPTZ,
    error         TEXT NOT NULL DEFAULT ''   -- sanitized
);
CREATE INDEX github_webhook_deliveries_inst_idx ON github_webhook_deliveries (installation_id);

-- (optional, for GitLab/Bitbucket) encrypted credentials, not used by GitHub
CREATE TABLE connector_credentials (
    connection_id BIGINT PRIMARY KEY REFERENCES connector_connections(id) ON DELETE CASCADE,
    ciphertext    BYTEA NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE connector_credentials;
DROP TABLE github_webhook_deliveries;
ALTER TABLE connector_repositories DROP COLUMN last_error;
ALTER TABLE connector_repositories DROP COLUMN last_synced_at;
ALTER TABLE connector_repositories DROP COLUMN sync_status;
ALTER TABLE connector_repositories DROP COLUMN visibility;
ALTER TABLE connector_repositories DROP COLUMN owner;
ALTER TABLE connector_repositories DROP COLUMN github_repo_id;
DROP TABLE github_install_states;
DROP TABLE github_installations;
