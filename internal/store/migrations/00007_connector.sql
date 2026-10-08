-- +goose Up
-- Cloud Connector operational state. CUSTOMER PLANE ONLY. NO SECRETS:
-- this schema stores the assumed-role ARN, the AWS account id, the per-connection
-- external id, and non-secret connection metadata ONLY. AWS access key ids,
-- secret access keys, and STS session tokens are NEVER written here by design --
-- there is deliberately no column for any credential. Temporary credentials live
-- in process memory (internal/connector/session) for the lifetime of a session
-- and are then discarded. Added as the next goose migration after 00006_findings;
-- the embedded goose mechanism (store.MigrationsFS, //go:embed migrations/*.sql)
-- picks this up automatically with no code change.

CREATE TABLE connector_connections (
    id               BIGSERIAL PRIMARY KEY,
    org_id           BIGINT      NOT NULL REFERENCES organizations(id), -- Tenant
    project_id       BIGINT      NOT NULL REFERENCES projects(id),      -- Project scope
    provider         TEXT        NOT NULL,                              -- 'aws' (phase 1)
    account_id       TEXT        NOT NULL,                              -- AWS 12-digit account id
    role_arn         TEXT        NOT NULL,                              -- assumed cross-account role
    external_id      TEXT        NOT NULL,                              -- per-connection STS ExternalId
    region           TEXT        NOT NULL DEFAULT '',
    capabilities     TEXT[]      NOT NULL DEFAULT ARRAY['core'],        -- enabled capabilities
    status           TEXT        NOT NULL DEFAULT 'PENDING',            -- state machine status
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_verified_at TIMESTAMPTZ,
    CONSTRAINT connector_connections_provider_account_uniq
        UNIQUE (org_id, provider, account_id)
);
CREATE INDEX connector_connections_org_idx     ON connector_connections (org_id);
CREATE INDEX connector_connections_project_idx ON connector_connections (project_id);

-- Per-connection capability enablement so permissions can be granted progressively.
CREATE TABLE connector_capabilities (
    connection_id BIGINT  NOT NULL REFERENCES connector_connections(id) ON DELETE CASCADE,
    capability    TEXT    NOT NULL,                 -- core|eks|workload|dspm
    enabled       BOOLEAN NOT NULL DEFAULT true,
    PRIMARY KEY (connection_id, capability)
);

-- Verification results: permission names + PASS/FAIL/NOT_ENABLED only (non-secret).
CREATE TABLE connector_verification_results (
    id            BIGSERIAL PRIMARY KEY,
    connection_id BIGINT NOT NULL REFERENCES connector_connections(id) ON DELETE CASCADE,
    capability    TEXT   NOT NULL,
    permission    TEXT   NOT NULL,
    outcome       TEXT   NOT NULL,                 -- PASS|FAIL|NOT_ENABLED
    checked_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX connector_verification_conn_idx ON connector_verification_results (connection_id);

-- Discovery sessions: a tracked unit of a single discovery execution.
CREATE TABLE connector_discovery_sessions (
    id            TEXT PRIMARY KEY,                -- unique session id
    connection_id BIGINT NOT NULL REFERENCES connector_connections(id) ON DELETE CASCADE,
    run_id        TEXT   NOT NULL,                 -- bellu pipeline run id
    status        TEXT   NOT NULL DEFAULT 'running', -- running|complete|failed|cancelled
    started_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at      TIMESTAMPTZ,
    error         TEXT   NOT NULL DEFAULT ''
);
CREATE INDEX connector_discovery_conn_idx ON connector_discovery_sessions (connection_id, started_at DESC);

-- +goose Down
DROP TABLE connector_discovery_sessions;
DROP TABLE connector_verification_results;
DROP TABLE connector_capabilities;
DROP TABLE connector_connections;
