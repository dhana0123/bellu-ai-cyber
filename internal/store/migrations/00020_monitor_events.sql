-- +goose Up
-- monitor_events is BOTH the durable event log AND the work queue; agent
-- assignment and run state live on the same row (backs Mission Control).
-- Durable-first: an event is persisted here BEFORE any processing, so a crash
-- loses nothing. Idempotency mirrors github_webhook_deliveries: a unique key on
-- (org_id, source, delivery_id) makes a redelivery a no-op via unique_violation.
-- Added as the next goose migration in the sequence; the embedded goose
-- mechanism (store.MigrationsFS, //go:embed migrations/*.sql) picks this up
-- automatically with no code change.
CREATE TABLE monitor_events (
    id          BIGSERIAL   PRIMARY KEY,                              -- opaque server-generated id
    org_id      BIGINT      NOT NULL REFERENCES organizations(id),    -- Tenant
    source      TEXT        NOT NULL,                                 -- github|correlate|discovery|sweep
    delivery_id TEXT        NOT NULL,                                 -- per-source idempotency key
    event_kind  TEXT        NOT NULL,                                 -- ci|vulnerability|code_change|resource
    payload     JSONB       NOT NULL,
    team        TEXT,                                                 -- red_team|blue_team|pr_reviewer|triage (set at route)
    state       TEXT        NOT NULL DEFAULT 'pending',               -- pending|routed|claimed|running|completed|failed|skipped
    run_id      TEXT        REFERENCES agent_runs(run_id),            -- NULL until claimed
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT monitor_events_source_chk
        CHECK (source IN ('github','correlate','discovery','sweep')),
    CONSTRAINT monitor_events_kind_chk
        CHECK (event_kind IN ('ci','vulnerability','code_change','resource')),
    CONSTRAINT monitor_events_state_chk
        CHECK (state IN ('pending','routed','claimed','running','completed','failed','skipped')),
    CONSTRAINT monitor_events_team_chk
        CHECK (team IS NULL OR team IN ('red_team','blue_team','pr_reviewer','triage'))
);

-- Idempotency: a redelivery with the same (org, source, delivery_id) collides
-- here and is treated as a no-op. GitHub reuses X-GitHub-Delivery end-to-end;
-- vulnerability uses (asset_id,vuln_id) as the delivery_id.
CREATE UNIQUE INDEX monitor_events_delivery_uniq
    ON monitor_events (org_id, source, delivery_id);

-- Claim scan: workers poll routed-but-unclaimed rows for their org, oldest-first
-- blocked only by SKIP LOCKED. Partial index keeps the queue hot-set small.
CREATE INDEX monitor_events_claimable_idx
    ON monitor_events (org_id, created_at)
    WHERE state = 'routed';

-- Phase 2 folds trace counts and lifecycle timestamps onto the run row; the
-- triggered_by link records which monitor_event launched it ('manual' for
-- operator launches). Added as ALTERs so existing 00017 data survives.
ALTER TABLE agent_runs ADD COLUMN agents_count   INTEGER NOT NULL DEFAULT 0;
ALTER TABLE agent_runs ADD COLUMN handoffs_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE agent_runs ADD COLUMN triggered_by   TEXT    NOT NULL DEFAULT 'manual';
ALTER TABLE agent_runs ADD COLUMN started_at     TIMESTAMPTZ;
ALTER TABLE agent_runs ADD COLUMN ended_at       TIMESTAMPTZ;

-- +goose Down
ALTER TABLE agent_runs DROP COLUMN ended_at;
ALTER TABLE agent_runs DROP COLUMN started_at;
ALTER TABLE agent_runs DROP COLUMN triggered_by;
ALTER TABLE agent_runs DROP COLUMN handoffs_count;
ALTER TABLE agent_runs DROP COLUMN agents_count;
DROP TABLE monitor_events;
