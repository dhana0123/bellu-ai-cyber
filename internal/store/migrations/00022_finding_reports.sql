-- +goose Up
-- finding_reports carries the agent's ACTUAL generated report artifact through
-- (not re-derived): structured columns for list/badges, JSONB for long-form
-- sections, and the plane's RENDERED markdown for the detail view.
-- SECURITY: poc_script_code (weaponized body) is NEVER a column here. Only a
-- poc_available flag + poc_description (inside sections) are stored, matching
-- the SARIF redaction rule in strix/report/sarif.py (Property P6).
-- Added as the next goose migration after 00021; the embedded goose mechanism
-- (store.MigrationsFS, //go:embed migrations/*.sql) picks this up automatically
-- with no code change.
CREATE TABLE finding_reports (
    id               BIGSERIAL   PRIMARY KEY,
    finding_id       BIGINT      NOT NULL REFERENCES findings(id),
    run_id           TEXT        REFERENCES agent_runs(run_id), -- nullable
    team             TEXT        NOT NULL,                      -- red|blue
    title            TEXT        NOT NULL DEFAULT '',
    severity         TEXT        NOT NULL DEFAULT '',
    poc_available    BOOLEAN     NOT NULL DEFAULT false,        -- FLAG ONLY
    sections         JSONB       NOT NULL DEFAULT '{}'::jsonb,  -- long-form fields
    rendered_markdown TEXT       NOT NULL DEFAULT '',           -- plane's artifact
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT finding_reports_team_chk CHECK (team IN ('red','blue'))
);
-- One current report per (finding, team); re-report upserts.
CREATE UNIQUE INDEX finding_reports_uniq ON finding_reports (finding_id, team);
CREATE INDEX finding_reports_run_idx ON finding_reports (run_id);

-- +goose Down
DROP TABLE finding_reports;
