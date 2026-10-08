-- +goose Up
-- Canonical Global Intelligence schema. Intel tables share the dev Postgres
-- instance but are namespaced intel_* so they could move to a separate
-- database later. Added as the next goose migration after 00004_rbac; the
-- embedded goose mechanism (store.MigrationsFS, //go:embed migrations/*.sql)
-- picks this up automatically with no code change.

-- One row per source fetch, for provenance, incremental cursors, and audit.
CREATE TABLE intel_fetch_runs (
    id              BIGSERIAL PRIMARY KEY,
    source          TEXT NOT NULL,              -- 'osv' | 'kev' | 'epss' | ...
    cursor          TEXT NOT NULL DEFAULT '',   -- next incremental cursor
    raw_count       INT  NOT NULL DEFAULT 0,
    records_added   INT  NOT NULL DEFAULT 0,    -- canonical rows inserted this run
    records_updated INT  NOT NULL DEFAULT 0,    -- canonical rows updated this run
    records_deleted INT  NOT NULL DEFAULT 0,    -- canonical rows removed this run
    cursor_before   TEXT NOT NULL DEFAULT '',   -- cursor at run start
    cursor_after    TEXT NOT NULL DEFAULT '',   -- cursor at run end (== cursor)
    status          TEXT NOT NULL DEFAULT 'running', -- see pipeline failure states
    error           TEXT NOT NULL DEFAULT '',   -- failure detail; '' when SUCCESS
    started_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at     TIMESTAMPTZ
);

-- Canonical, deduplicated vulnerabilities.
CREATE TABLE intel_vulnerabilities (
    id              TEXT PRIMARY KEY,        -- canonical id (preferred alias)
    summary         TEXT NOT NULL DEFAULT '',
    cvss_score      DOUBLE PRECISION,
    cvss_vector     TEXT NOT NULL DEFAULT '',
    cvss_version    TEXT NOT NULL DEFAULT '',
    severity        TEXT NOT NULL DEFAULT 'info',
    known_exploited BOOLEAN NOT NULL DEFAULT false,  -- KEV
    epss            DOUBLE PRECISION,                -- latest EPSS
    published       TIMESTAMPTZ,
    modified        TIMESTAMPTZ,
    sources         TEXT[] NOT NULL DEFAULT '{}',
    first_seen      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX intel_vuln_severity_idx ON intel_vulnerabilities (severity);
CREATE INDEX intel_vuln_kev_idx ON intel_vulnerabilities (known_exploited);

-- Alias -> canonical id. Enables identifier linking and dedup lookups.
CREATE TABLE intel_aliases (
    alias   TEXT PRIMARY KEY,               -- normalized "CVE-...", "GHSA-..."
    vuln_id TEXT NOT NULL REFERENCES intel_vulnerabilities(id) ON DELETE CASCADE
);
CREATE INDEX intel_aliases_vuln_idx ON intel_aliases (vuln_id);

-- Affected package ranges, one row per (vuln, purl, range).
CREATE TABLE intel_affected_packages (
    id         BIGSERIAL PRIMARY KEY,
    vuln_id    TEXT NOT NULL REFERENCES intel_vulnerabilities(id) ON DELETE CASCADE,
    purl       TEXT NOT NULL,               -- name only, no version
    ecosystem  TEXT NOT NULL,
    introduced TEXT NOT NULL DEFAULT '',    -- inclusive lower bound
    fixed      TEXT NOT NULL DEFAULT '',    -- exclusive upper bound ('' = open)
    CONSTRAINT intel_affected_uniq UNIQUE (vuln_id, purl, introduced, fixed)
);
CREATE INDEX intel_affected_purl_idx ON intel_affected_packages (purl);

-- EPSS history (daily series) so trends are available later.
CREATE TABLE intel_epss_history (
    vuln_id    TEXT NOT NULL REFERENCES intel_vulnerabilities(id) ON DELETE CASCADE,
    scored_on  DATE NOT NULL,
    epss       DOUBLE PRECISION NOT NULL,
    percentile DOUBLE PRECISION,
    PRIMARY KEY (vuln_id, scored_on)
);

-- Source catalog + per-source scheduler state. Each source is scheduled and
-- recovered independently (not one global cron), carrying its own cadence,
-- sync mode, incremental markers (cursor/etag/last-modified/checksum), and
-- backoff state so a transient upstream outage never affects other sources.
CREATE TABLE intel_sources (
    source          TEXT PRIMARY KEY,
    kind            TEXT NOT NULL DEFAULT 'vuln',  -- 'vuln' | 'exploit' | 'ai'
    enabled         BOOLEAN NOT NULL DEFAULT true,
    schedule        TEXT,                          -- cron-like cadence; NULL = use default/config
    sync_mode       TEXT NOT NULL DEFAULT 'incremental', -- 'bootstrap' | 'incremental' | 'reconciliation'
    last_started_at TIMESTAMPTZ,                   -- when the most recent run began
    last_success_at TIMESTAMPTZ,                   -- when the most recent run succeeded
    last_cursor     TEXT,                          -- stored incremental cursor
    last_etag       TEXT,                          -- HTTP ETag for conditional GET
    last_modified   TEXT,                          -- HTTP Last-Modified for conditional GET
    last_checksum   TEXT,                          -- content checksum of last payload
    last_error      TEXT,                          -- last failure detail (NULL when healthy)
    retry_count     INT NOT NULL DEFAULT 0         -- consecutive failures; drives exponential backoff
);

-- +goose Down
DROP TABLE intel_sources;
DROP TABLE intel_epss_history;
DROP TABLE intel_affected_packages;
DROP TABLE intel_aliases;
DROP TABLE intel_vulnerabilities;
DROP TABLE intel_fetch_runs;
