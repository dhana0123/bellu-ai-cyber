-- +goose Up
-- Findings: correlation results linking a graph asset to a canonical
-- vulnerability. Added as the next goose migration after 00005_intel; the
-- embedded goose mechanism (store.MigrationsFS, //go:embed migrations/*.sql)
-- picks this up automatically with no code change.

CREATE TABLE findings (
    id         BIGSERIAL PRIMARY KEY,
    asset_id   TEXT NOT NULL,               -- graph canonical_id
    asset_kind TEXT NOT NULL,
    vuln_id    TEXT NOT NULL,               -- intel_vulnerabilities.id
    intel_ref  TEXT NOT NULL,               -- cited intel record id
    risk_score DOUBLE PRECISION NOT NULL DEFAULT 0,
    severity   TEXT NOT NULL DEFAULT 'info',
    status     TEXT NOT NULL DEFAULT 'open',
    first_seen TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT findings_uniq UNIQUE (asset_id, vuln_id)
);
CREATE INDEX findings_severity_idx ON findings (severity);
CREATE INDEX findings_status_idx ON findings (status);

-- +goose Down
DROP TABLE findings;
