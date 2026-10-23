-- +goose Up
-- Agent runs: the org-scoped lifecycle rows for the agents plane's Runs seam
-- (POST/GET /api/agent/runs). A run records its target, its optional
-- active-testing authorization scope, its status, and a running findings count.
-- Org is the tenant: every read and write is scoped to it so a caller can never
-- observe another org's runs (tenant isolation). Added as the next goose
-- migration after 00016_derived_events; the embedded goose mechanism
-- (store.MigrationsFS, //go:embed migrations/*.sql) picks this up automatically
-- with no code change.

CREATE TABLE agent_runs (
    run_id         TEXT        PRIMARY KEY,                              -- opaque server-generated id
    org_id         BIGINT      NOT NULL REFERENCES organizations(id),    -- Tenant
    target         JSONB       NOT NULL,                                 -- RunTargetRef (kind + canonical_id|raw)
    authz_scope    JSONB,                                                -- AuthzScope; NULL => read-only
    active_testing BOOLEAN     NOT NULL DEFAULT false,                   -- whether this run was authorized for it
    status         TEXT        NOT NULL DEFAULT 'queued',                -- queued|running|completed|failed
    findings_count INTEGER     NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Status reads are always scoped by (org_id, run_id); index the org lookup so a
-- cross-tenant probe collapses to a scoped not-found without a full scan.
CREATE INDEX agent_runs_org_idx ON agent_runs (org_id);

-- +goose Down
DROP TABLE agent_runs;
