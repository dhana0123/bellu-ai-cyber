# Implementation Plan: Global Intelligence

## Overview

This plan implements the Global Intelligence feature additively inside the existing Go
module `github.com/bellu/bellu-ai-cyber` (Go 1.27), in the three phases the design and
requirements define. Phase 1 builds the ingestion pipeline (OSV + KEV + EPSS fetchers,
content-addressed raw store, parsers, canonical normalization, dedup, `intel_*` Postgres
persistence, and the `bellu-intel` CLI). Phase 2 adds the Intelligence Matcher into the
graph, the Attack Path reachability input, the Risk Engine, the findings lifecycle, the
`/api/findings` and `/api/intel` endpoints, and the React Findings page. Phase 3 adds
AI-specific sources, the signed air-gapped release bundle, and the deferred OpenSearch
interface.

Each task builds on the previous ones and ends by wiring new code into the pipeline, CLI,
API, or UI so there is no orphaned code. Tasks reuse the existing skeleton under
`bellu-global-intelligence/` (notably `canonical/version.go`'s `SemverRange.Contains` and
`compareVersions`) and the existing pgx v5, goose v3 embedded migrations, neo4j-go-driver
v5, config package, Gin server + RBAC, `graph.Writer` allow-listed `SyncEntities`/
`SyncEdges`, and testcontainers-go harness. Property-based tests (`testing/quick` or
`pgregory.net/rapid`) and testcontainers integration tests validate the five correctness
properties.

## Tasks

### Phase 1 — Global Intelligence ingestion

- [x] 1. Define canonical model validation and reuse the existing version comparator
  - [x] 1.1 Implement `CanonicalVuln`, `AffectedPackage`, `CVSS`, `Severity`, `SemverRange` and `Validate`/`NormalizeAlias`/`DedupeAliases` in `bellu-global-intelligence/canonical/model.go`
    - Add types and methods required by the existing `canonical/model_test.go` and reuse `SemverRange.Contains`/`compareVersions` already in `canonical/version.go`
    - Enforce validation rules: non-empty `ID` present in `Aliases`; non-empty, normalized, de-duplicated aliases; `EPSS` in `[0,1]`; `CVSS.Score` in `[0,10]`; every `Purl` without a version qualifier
    - _Requirements: 4.1, 4.2, 4.3, 4.4, 4.5, 4.6, 4.7, 4.8, 4.9_
  - [x]* 1.2 Run and extend the existing canonical unit tests
    - Ensure `canonical/model_test.go` and `TestSemverRangeContains`/`TestCompareVersions` pass; add boundary cases on `introduced`/`fixed` edges
    - **Property 5: Version matching is sound (release versions)**
    - **Validates: Requirements 8.1, 8.5**

- [x] 2. Append the intel Postgres migration and intel store access
  - [x] 2.1 Create migration `internal/store/migrations/00005_intel.sql`
    - Define `intel_fetch_runs`, `intel_vulnerabilities`, `intel_aliases`, `intel_affected_packages`, `intel_epss_history`, and `intel_sources` with the columns, indexes, and constraints from the design; append after `00004_rbac` so embedded goose picks it up automatically
    - _Requirements: 6.1, 6.3, 6.4, 18.2, 18.3_
  - [x] 2.2 Implement idempotent `intel_*` upserts in `bellu-global-intelligence/database/intel_store.go`
    - Take `*store.Store` and use `Pool()`; upsert canonical vulns, aliases, and affected ranges via `INSERT ... ON CONFLICT DO UPDATE` touching only `last_seen` on no-op re-runs; append daily `intel_epss_history` rows keyed by `(vuln_id, scored_on)`; record each run in `intel_fetch_runs` with source, cursor, counts, and status
    - _Requirements: 6.1, 6.2, 6.3, 6.4, 18.6_
  - [x]* 2.3 Write testcontainers integration test for intel persistence
    - Apply `00005` against real Postgres, upsert the same canonical set twice, assert table state unchanged except `last_seen`
    - **Property 3: Re-runs are idempotent (persistence)**
    - **Validates: Requirements 6.2, 18.4**

- [x] 3. Implement the RawStore
  - [x] 3.1 Define the `RawStore` interface and `RawRef` in `bellu-global-intelligence/raw/raw.go` and the filesystem implementation in `raw/fsstore.go`
    - `Put` returns a sha256-keyed `RawRef` and is a no-op for identical bytes; `Get` returns the exact stored bytes; treat payloads as immutable; add an `s3store.go` stub for later
    - _Requirements: 2.1, 2.2, 2.3, 2.4_
  - [x]* 3.2 Write unit tests for content-addressed storage
    - Assert identical bytes yield the same ref with no duplicate copy, and round-trip returns exact bytes
    - **Property 3: Re-runs are idempotent (content-addressed storage)**
    - **Validates: Requirements 2.1, 2.2, 2.4**

- [x] 4. Define the fetch and parser interfaces with reserved stubs
  - [x] 4.1 Define `Fetcher`, `Cursor`, `FetchResult` in `bellu-global-intelligence/fetch/fetch.go` and `Parser`, `SourceRecord` in `bellu-global-intelligence/parser/parser.go`
    - Add interface stubs for NVD, GHSA, ATLAS, OWASP GenAI, AVID, MCP, and vendor advisories reserved for later phases
    - _Requirements: 1.6, 1.7, 3.1, 3.3, 3.4_

- [ ] 5. Implement Phase 1 fetchers (OSV, KEV, EPSS)
  - [x] 5.1 Implement the OSV fetcher in `bellu-global-intelligence/fetch/osv/osv.go`
    - Fetch purl ecosystems with PyPI first; empty cursor = full fetch, non-empty cursor = changed-since with next cursor returned; apply rate limiting; stream raw bytes to `RawStore` without parsing or mutation
    - _Requirements: 1.1, 1.2, 1.3, 1.4, 1.5, 1.6_
  - [x] 5.2 Implement the KEV and EPSS fetchers in `bellu-global-intelligence/fetch/kev/kev.go` and `fetch/epss/epss.go`
    - Honor cursor/full-fetch semantics, rate limiting, and raw-only streaming to `RawStore`
    - _Requirements: 1.1, 1.3, 1.4, 1.5, 1.6_

- [x] 6. Implement Phase 1 parsers (OSV, KEV, EPSS)
  - [x] 6.1 Implement the OSV parser in `bellu-global-intelligence/parser/osv/osv.go`
    - Decode raw payloads into typed source records exposing `Aliases()` and `SourceKey()`; same bytes yield same records (pure function)
    - _Requirements: 3.1, 3.2, 3.3, 3.4_
  - [x] 6.2 Implement the KEV and EPSS parsers in `bellu-global-intelligence/parser/kev/kev.go` and `parser/epss/epss.go`
    - Decode into typed source records exposing aliases and stable per-source keys
    - _Requirements: 3.1, 3.2, 3.3, 3.4_
  - [x]* 6.3 Write golden-file parser tests
    - Pin recorded OSV/KEV/EPSS payloads to expected source records; assert identical payloads produce identical records
    - **Property 3 (parser determinism)**
    - **Validates: Requirements 3.2**

- [x] 7. Implement per-source canonicalizers
  - [x] 7.1 Implement the `Canonicalizer` interface and per-source implementations in `bellu-global-intelligence/canonical/canonical.go`
    - Map OSV/KEV/EPSS source records into `CanonicalVuln` carrying aliases (CVE/GHSA/OSV), affected purl + semver ranges, CVSS, severity, KEV flag, and EPSS; reject invalid records per the model validation rules
    - _Requirements: 4.1, 4.2, 4.3, 4.4, 4.5, 4.6, 4.7, 4.8, 4.9_

- [x] 8. Implement dedup / identifier linking
  - [x] 8.1 Implement the `Deduper` (union-find over aliases) in `bellu-global-intelligence/correlation/dedup.go`
    - Merge records sharing any alias into one `CanonicalVuln` via connected components; select a deterministic canonical id (CVE, else GHSA, else OSV) from the merged alias set
    - _Requirements: 5.1, 5.2, 5.3, 5.4_
  - [x]* 8.2 Write property-based test for dedup
    - Generate random alias graphs with `testing/quick`/`rapid`; assert dedup equals connected components, order-independence over permutations, and `Dedup(Dedup(x)) == Dedup(x)`
    - **Property 1: Dedup is deterministic and idempotent**
    - **Validates: Requirements 5.1, 5.2, 5.3**

- [x] 9. Checkpoint — Phase 1 pipeline components
  - Ensure all tests pass, ask the user if questions arise.

- [x] 10. Build the bellu-intel CLI and wire the ingestion pipeline
  - [x] 10.1 Implement `cmd/bellu-intel/main.go` with subcommands and config loading
    - Dispatch `fetch`, `parse`, `normalize`, `correlate`, `score`, `release`, `run` in the existing `cmd/bellu` switch style; read config from the existing `bellu.yaml` via `internal/config`
    - _Requirements: 7.1, 7.5, 18.1_
  - [x] 10.2 Wire `run` to execute fetch → parse → normalize → correlate in sequence
    - Implement `--mode daily|incremental` (daily = full-schedule run, incremental = cursor-based using stored cursors); drive fetchers → RawStore → parsers → canonicalizers → deduper → `intel_store` upsert; record `intel_fetch_runs`
    - _Requirements: 7.2, 7.3, 7.4_
  - [ ]* 10.3 Write integration test for end-to-end Phase 1 idempotence
    - Run `fetch → parse → normalize → correlate` twice over unchanged fixtures against a testcontainers Postgres; assert `intel_*` unchanged except `last_seen`
    - **Property 3: Re-runs are idempotent (end-to-end)**
    - **Validates: Requirements 18.4**

### Phase 2 — Correlation, Risk, and Findings

- [x] 11. Append the findings migration
  - [x] 11.1 Create migration `internal/store/migrations/00006_findings.sql`
    - Define `findings` with unique `(asset_id, vuln_id)`, severity/status indexes, and `first_seen`/`last_seen`; append after `00005_intel`
    - _Requirements: 12.1, 18.2, 18.3_

- [x] 12. Implement the Attack Path reachability input
  - [x] 12.1 Add read-only `ReachesAsset(ctx, canonicalID) (bool, error)` to `internal/graph/query.go`
    - Reuse the existing attack-path Cypher patterns; perform no graph writes while evaluating
    - _Requirements: 10.1, 10.2, 18.6_

- [x] 13. Implement the Risk Engine
  - [x] 13.1 Implement `RiskScorer`, `RiskContext`, `RiskResult` in `internal/risk/score.go`
    - Compute a bounded `[0,100]` score from CVSS, EPSS, KEV flag, reachability/exposure, and AI criticality using the design's formula; bucket severities (`>=90` critical, `>=70` high, `>=40` medium, `>=10` low, else info) and floor KEV-known-exploited at high
    - _Requirements: 11.1, 11.4, 11.5, 10.3_
  - [x]* 13.2 Write property-based test for risk scoring
    - Generate random `RiskContext`s; assert bounds `[0,100]`, determinism, and monotonicity when any single factor increases
    - **Property 4: Risk scoring is deterministic, monotonic, and bounded**
    - **Validates: Requirements 11.1, 11.2, 11.3**

- [x] 14. Implement the Intelligence Matcher
  - [x] 14.1 Implement purl/version/image/AI matching helpers in `bellu-global-intelligence/correlation/match.go`
    - Match canonical vulns to graph `Package` purl names and test the version against affected `SemverRange`s (`introduced ≤ v < fixed`) using the existing comparator; never report a package whose version falls outside every declared range; add ContainerImage and AI-asset matching helpers
    - _Requirements: 8.1, 8.2, 8.3, 8.5_
  - [x] 14.2 Implement the `Matcher` and `Candidate` emission in `internal/intelcorrelate/matcher.go`
    - Enumerate graph assets, look up intel, and emit `Candidate{AssetID, AssetKind, VulnID, IntelRef, MatchBasis}` for each match citing the intel record id
    - _Requirements: 8.1, 8.2, 8.3, 8.4, 8.5_
  - [x] 14.3 Enrich the graph through the allow-listed paths without fabricating evidence
    - Upsert a `Vulnerability` node (reuse `KindVulnerability`) and a `HAS_VULN` edge carrying `cvss`, `epss`, `known_exploited`, `severity`, `intel_ref` only via the existing `SyncEntities`/`SyncEdges`; write no rows to the `evidence` table; annotate only nodes a prior `raw_event` justified; cite the intel record id, never a `raw_event` id
    - _Requirements: 9.1, 9.2, 9.3, 9.4, 9.5, 9.6, 18.5, 18.6_
  - [x]* 14.4 Write testcontainers + Memgraph integration test for correlation
    - Seed `Package` nodes, run the matcher, assert `HAS_VULN` edges and attributes, and assert the `evidence` table is unchanged
    - **Property 2: Correlation never fabricates evidence**
    - **Validates: Requirements 9.4, 9.5, 9.6, 18.5**

- [x] 15. Implement the findings lifecycle store
  - [x] 15.1 Implement `Finding`, `Status`, and store access in `internal/finding/finding.go` and `internal/finding/store.go`
    - Store at most one finding per `(asset_id, vuln_id)`; upsert matched candidates as `open` with `risk_score` and `severity`; preserve `first_seen` and update `last_seen`; transition `open → resolved` when a later run no longer matches
    - _Requirements: 12.1, 12.2, 12.3, 12.4, 12.5_
  - [x]* 15.2 Write integration test for findings lifecycle and re-run idempotence
    - Assert upsert, resolve-on-disappear, first/last-seen handling, and that a second unchanged run leaves findings stable except `last_seen`
    - **Property 3: Re-runs are idempotent (findings)**
    - **Validates: Requirements 12.2, 12.3, 12.4, 18.4**

- [x] 16. Wire correlate/score into the bellu-intel CLI
  - [x] 16.1 Extend `cmd/bellu-intel` `correlate` to run the Matcher and `score` to run the Risk Engine and upsert findings
    - `correlate` runs dedup + intel upsert and the Matcher into the graph; `score` computes risk via reachability and upserts findings
    - _Requirements: 7.1, 10.3, 18.4_

- [x] 17. Checkpoint — correlation, risk, and findings
  - Ensure all tests pass, ask the user if questions arise.

- [x] 18. Implement the Findings and Intelligence API
  - [x] 18.1 Implement handlers in `backend/server/findings_handlers.go`
    - `GET /api/findings` (filter severity, status, asset_kind, limit); `GET /api/findings/:id` (finding + cited intel + evidence trail); `GET /api/intel/vulnerabilities` (filter severity, kev, purl); `GET /api/intel/vulnerabilities/:id` (vuln + aliases + affected ranges), following the existing handler shape
    - _Requirements: 13.1, 13.2, 13.3, 13.4_
  - [x] 18.2 Register the findings group gated by `findings:read` in `backend/server/server.go`
    - Add the authed group using the existing `RequirePermission(auth.PermFindingsRead)` pattern so every findings/intel endpoint denies requests lacking the permission
    - _Requirements: 13.5, 13.6_
  - [x]* 18.3 Write handler tests for API filters and permission gating
    - Assert filter behavior and that requests without `findings:read` are denied
    - _Requirements: 13.1, 13.3, 13.5, 13.6_

- [x] 19. Implement the Findings UI page
  - [x] 19.1 Add `frontend/src/pages/Findings.jsx` and the API calls in `frontend/src/api.js`
    - Use the existing shadcn `card`/`button`/`misc` components and ChatGPT dark theme; show a severity badge, risk score, and affected asset per finding, with links to the cited intelligence and evidence
    - _Requirements: 14.1, 14.2, 14.3_
  - [x] 19.2 Add the permission-gated Findings nav entry in `frontend/src/config/nav.js`
    - Show the entry only when the principal holds `findings:read`; hide it otherwise (permissions read from `/api/auth/me`)
    - _Requirements: 14.4, 14.5_

- [x] 20. Checkpoint — API and UI
  - Ensure all tests pass, ask the user if questions arise.

### Phase 3 — AI sources, signed release bundle, deferred search

- [ ] 21. Implement AI-specific intelligence sources
  - [-] 21.1 Implement fetchers, parsers, and canonical mappings for MITRE ATLAS, OWASP GenAI, AVID, and MCP
    - Flesh out the reserved stubs; map AI-specific intelligence to the existing `AIAgent`, `AIModel`, and `MCPServer` kinds and join via the Matcher
    - _Requirements: 15.1, 15.2, 1.7, 8.3_

- [ ] 22. Implement the signed air-gapped release bundle
  - [-] 22.1 Implement the release builder in `bellu-global-intelligence/release/release.go`
    - Export `intel_*` to canonical JSONL with stable key ordering (byte-identical for identical data); compute per-file sha256 into a manifest; sign the manifest with an ed25519 private key; package manifest + signature + data into a `.tar.zst` bundle
    - _Requirements: 16.1, 16.2, 16.3, 16.4_
  - [~] 22.2 Implement `bellu-intel release import` with verification
    - Verify the ed25519 signature against a pinned public key and per-file checksums before upserting into `intel_*`; reject the bundle and upsert nothing on any verification failure
    - _Requirements: 16.5, 16.6_
  - [ ]* 22.3 Write unit tests for export determinism and verification failure
    - Assert byte-identical export for identical data and that a tampered manifest/checksum rejects the bundle with no `intel_*` writes
    - **Property 3 (export idempotence) / verification safety**
    - **Validates: Requirements 16.1, 16.6**

- [ ] 23. Define the deferred OpenSearch search interface
  - [-] 23.1 Define `bellu-global-intelligence/search/search.go` as an interface only
    - Add no OpenSearch runtime dependency
    - _Requirements: 17.1, 17.2_

- [~] 24. Final checkpoint — Ensure all tests pass
  - Ensure all tests pass, ask the user if questions arise.

## Notes

- Tasks marked with `*` are optional test tasks and can be skipped for a faster MVP; core implementation tasks are never optional.
- Each task references the specific requirement sub-clauses it implements for traceability.
- Property-based tests use `testing/quick` or `pgregory.net/rapid`; integration tests reuse the existing testcontainers-go Postgres harness and the Memgraph fixture pattern.
- The feature stays additive: no new Go module, migrations appended after `00004_rbac`, graph writes only through the existing allow-listed `SyncEntities`/`SyncEdges`, and the evidence-backed invariant preserved (the matcher never writes to the `evidence` table).
- Correctness properties: Property 1 (dedup) → 8.2; Property 2 (no fabricated evidence) → 14.4; Property 3 (idempotence) → 2.3, 6.3-equivalent 2.3, 10.3, 15.2, 22.3; Property 4 (risk scoring) → 13.2; Property 5 (version matching) → 1.2.

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1.1", "3.1", "4.1"] },
    { "id": 1, "tasks": ["1.2", "2.1", "3.2"] },
    { "id": 2, "tasks": ["2.2", "5.1", "5.2", "6.1", "6.2"] },
    { "id": 3, "tasks": ["2.3", "6.3", "7.1", "8.1", "11.1", "12.1"] },
    { "id": 4, "tasks": ["8.2", "10.1", "13.1", "14.1"] },
    { "id": 5, "tasks": ["10.2", "13.2", "14.2", "15.1"] },
    { "id": 6, "tasks": ["10.3", "14.3", "15.2"] },
    { "id": 7, "tasks": ["14.4", "16.1", "18.1"] },
    { "id": 8, "tasks": ["18.2", "19.1"] },
    { "id": 9, "tasks": ["18.3", "19.2", "21.1"] },
    { "id": 10, "tasks": ["22.1", "23.1"] },
    { "id": 11, "tasks": ["22.2"] },
    { "id": 12, "tasks": ["22.3"] }
  ]
}
```
