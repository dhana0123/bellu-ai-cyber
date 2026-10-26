-- +goose Up
CREATE TABLE run_agents (
    id         BIGSERIAL   PRIMARY KEY,
    run_id     TEXT        NOT NULL REFERENCES agent_runs(run_id),
    agent_id   TEXT        NOT NULL,                 -- engine agent id
    agent_name TEXT        NOT NULL,                 -- Recon|Posture|Discovery|...
    parent_id  TEXT,                                 -- nullable (root agent)
    stage      TEXT        NOT NULL,                 -- mapped from name
    outcome    TEXT        NOT NULL DEFAULT '',      -- status/error text
    started_at TIMESTAMPTZ,
    ended_at   TIMESTAMPTZ,
    CONSTRAINT run_agents_stage_chk CHECK (stage IN (
        'RECON','POSTURE','DISCOVERY','EXPLOIT','SWEEP','VERIFY',
        'STABILITY','SCORE','FORMAT','COVERAGE','REPORT','other'))
);
-- A leg is one (run_id, agent_id); re-emitting the same agent is idempotent.
CREATE UNIQUE INDEX run_agents_uniq ON run_agents (run_id, agent_id);
CREATE INDEX run_agents_stage_idx ON run_agents (run_id, stage);

CREATE TABLE run_handoffs (
    id            BIGSERIAL   PRIMARY KEY,
    run_id        TEXT        NOT NULL REFERENCES agent_runs(run_id),
    from_agent_id TEXT        NOT NULL,
    to_agent_id   TEXT        NOT NULL,
    at            TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX run_handoffs_run_idx ON run_handoffs (run_id);

-- +goose Down
DROP TABLE run_handoffs;
DROP TABLE run_agents;
