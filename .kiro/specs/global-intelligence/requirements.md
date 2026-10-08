# Requirements Document

## Introduction

The **Global Intelligence** feature gives Bellu its own curated, provider-independent
corpus of security intelligence (vulnerabilities, advisories, exploited-in-the-wild
signals, exploit-prediction scores, and later AI-specific threat knowledge) and
correlates that corpus into each customer's existing AI Security Graph so that assets
surface risk-scored, evidence-backed findings.

The feature is additive and lives entirely inside the existing Go module
`github.com/bellu/bellu-ai-cyber`. It is delivered in three phases, mirrored in these
requirements:

- **Phase 1 — Global Intelligence ingestion**: fetch, raw storage, parse, canonical
  normalization, dedup/identifier linking, persistence to `intel_*` Postgres tables, and
  the `bellu-intel` CLI.
- **Phase 2 — Correlation, Risk, and Findings**: the Intelligence Matcher into the graph,
  the Attack Path reachability input, the Risk Engine, the findings lifecycle, the
  `/api/findings` and `/api/intel` endpoints, and the React Findings page.
- **Phase 3 — Future (lower priority)**: AI-specific intelligence sources, the signed
  air-gapped release bundle, and the deferred OpenSearch search package.

These requirements are derived from the approved design document and trace to the design's
components, data models, and stated correctness properties.

## Glossary

- **Global_Intelligence_System**: The complete feature comprising ingestion, correlation,
  risk, findings, API, and UI described in this document.
- **Fetcher**: A component that pulls raw payloads from one upstream intelligence source.
- **RawStore**: Content-addressed storage of unmodified fetched payloads, keyed by sha256.
- **Parser**: A pure component that decodes raw payloads of one source into typed source records.
- **Canonicalizer**: A component that maps source records into the canonical intelligence model.
- **Deduper**: A component that merges canonical records sharing any alias into one record.
- **Intelligence_Matcher**: The component that joins canonical intelligence to customer graph assets.
- **Risk_Engine**: The component that computes a bounded risk score and severity per finding.
- **Attack_Path_Engine**: The read-only graph component that reports whether an attack path reaches an asset.
- **Findings_Store**: The component that persists and manages the lifecycle of findings.
- **Findings_API**: The Gin HTTP endpoints exposing findings and canonical intelligence.
- **Findings_UI**: The React page that displays findings to authorized users.
- **bellu-intel_CLI**: The command-line tool orchestrating the ingestion and correlation pipeline.
- **OSV**: Open Source Vulnerabilities, an upstream source keyed by package URL ecosystems.
- **KEV**: CISA Known Exploited Vulnerabilities catalog; marks vulnerabilities exploited in the wild.
- **KEV_known_exploited**: A boolean flag on a vulnerability indicating presence in the KEV catalog.
- **EPSS**: FIRST Exploit Prediction Scoring System; a probability in the range 0 to 1.
- **CVSS**: Common Vulnerability Scoring System; a base score in the range 0 to 10.
- **purl**: Package URL, a standard identifier for a software package (name without version qualifier).
- **Canonical_Vulnerability**: Bellu's provider-independent, deduplicated view of one vulnerability.
- **Finding**: A durable record pairing a customer asset with a vulnerability, carrying risk and status.
- **Reachability**: Whether a read-only attack-path query shows a path reaching a given asset.
- **Air_gapped_release**: A signed, verifiable intelligence bundle imported by disconnected customers.
- **Candidate**: An (asset, vulnerability) pairing with the matched intel reference and match basis.
- **Cursor**: An opaque incremental marker used by a Fetcher to retrieve only new data.

## Requirements

### Requirement 1: Fetch raw intelligence from upstream sources (Phase 1)

**User Story:** As an intelligence operator, I want fetchers to pull raw intelligence from upstream sources, so that Bellu has an independent corpus of security data.

#### Acceptance Criteria

1. THE Global_Intelligence_System SHALL provide a Fetcher for OSV, a Fetcher for CISA KEV, and a Fetcher for FIRST EPSS in Phase 1.
2. WHERE a Fetcher targets OSV, THE Global_Intelligence_System SHALL fetch purl ecosystems with PyPI as the first implemented ecosystem.
3. WHEN a Fetcher runs with a non-empty Cursor, THE Fetcher SHALL retrieve only payloads changed since that Cursor and return the next Cursor.
4. WHEN a Fetcher runs with an empty Cursor, THE Fetcher SHALL perform a full fetch.
5. WHILE a Fetcher is retrieving payloads, THE Fetcher SHALL apply rate limiting to upstream requests.
6. THE Fetcher SHALL stream raw bytes to the RawStore without parsing or mutating the payload contents.
7. WHERE a source is NVD, GHSA, ATLAS, OWASP GenAI, AVID, MCP, or a vendor advisory, THE Global_Intelligence_System SHALL expose the Fetcher as an interface stub reserved for a later phase.

### Requirement 2: Content-addressed raw payload storage (Phase 1)

**User Story:** As an intelligence operator, I want fetched payloads stored content-addressed and immutable, so that storage is deduplicated and reproducible.

#### Acceptance Criteria

1. WHEN the RawStore stores a payload, THE RawStore SHALL return a reference keyed by the sha256 hash of the payload content.
2. WHEN the RawStore receives bytes identical to previously stored bytes, THE RawStore SHALL return the same reference and store no duplicate copy.
3. THE RawStore SHALL treat stored payloads as immutable.
4. WHEN the RawStore receives a request for a stored reference, THE RawStore SHALL return the exact bytes originally stored.

### Requirement 3: Parse raw payloads into source records (Phase 1)

**User Story:** As an intelligence operator, I want each source's raw payloads parsed into typed records, so that downstream normalization has structured input.

#### Acceptance Criteria

1. THE Parser SHALL decode a raw payload into zero or more source records specific to the source.
2. FOR ALL identical raw payloads, THE Parser SHALL produce identical source records (pure-function determinism).
3. THE Parser SHALL expose on each source record the list of external aliases that record carries.
4. THE Parser SHALL expose on each source record the stable per-source key of that record.

### Requirement 4: Canonical normalization to CanonicalVuln (Phase 1)

**User Story:** As an intelligence operator, I want source records normalized into a canonical vulnerability model, so that intelligence is provider-independent.

#### Acceptance Criteria

1. THE Canonicalizer SHALL map source records into Canonical_Vulnerability objects carrying aliases, affected package ranges, CVSS, severity, the KEV_known_exploited flag, and the EPSS score.
2. THE Canonicalizer SHALL record aliases drawn from CVE, GHSA, and OSV identifier schemes.
3. THE Canonicalizer SHALL represent affected package ranges as a purl plus one or more semver ranges with an inclusive introduced bound and an exclusive fixed bound.
4. IF a Canonical_Vulnerability identifier is not present in its alias list, THEN THE Canonicalizer SHALL reject the record as invalid.
5. IF the alias list of a Canonical_Vulnerability is empty, THEN THE Canonicalizer SHALL reject the record as invalid.
6. THE Canonicalizer SHALL normalize alias strings to a canonical form and remove duplicate aliases.
7. IF the EPSS value of a Canonical_Vulnerability is outside the range 0 to 1 inclusive, THEN THE Canonicalizer SHALL reject the record as invalid.
8. IF the CVSS score of a Canonical_Vulnerability is outside the range 0 to 10 inclusive, THEN THE Canonicalizer SHALL reject the record as invalid.
9. IF an affected package purl contains a version qualifier, THEN THE Canonicalizer SHALL reject the record as invalid.

### Requirement 5: Dedup and identifier linking (Phase 1)

**User Story:** As an intelligence operator, I want canonical records that share an identifier merged into one vulnerability, so that the corpus has no duplicate vulnerabilities.

#### Acceptance Criteria

1. WHEN the Deduper processes canonical records, THE Deduper SHALL merge records that share any alias into one Canonical_Vulnerability using connected components of the alias identifier graph.
2. FOR ALL input orderings of the same canonical records, THE Deduper SHALL produce the same set of merged vulnerabilities (order independence).
3. WHEN the Deduper is applied to its own output, THE Deduper SHALL produce the same result as a single application (idempotence).
4. WHEN the Deduper merges records, THE Deduper SHALL select a deterministic canonical identifier from the merged alias set.

### Requirement 6: Persist intelligence to intel_* Postgres tables (Phase 1)

**User Story:** As an intelligence operator, I want canonical intelligence persisted to dedicated Postgres tables, so that it is queryable and provenance is retained.

#### Acceptance Criteria

1. THE Global_Intelligence_System SHALL persist intelligence to the intel_fetch_runs, intel_vulnerabilities, intel_aliases, intel_affected_packages, intel_epss_history, and intel_sources tables.
2. WHEN the Global_Intelligence_System persists a canonical record that already exists, THE Global_Intelligence_System SHALL perform an idempotent upsert that leaves table state unchanged except for last_seen timestamps.
3. WHEN the Global_Intelligence_System persists an EPSS score, THE Global_Intelligence_System SHALL retain daily EPSS history keyed by vulnerability and scored date in intel_epss_history.
4. WHEN a Fetcher completes a run, THE Global_Intelligence_System SHALL record the run in intel_fetch_runs with source, cursor, counts, and status.

### Requirement 7: bellu-intel CLI orchestration (Phase 1)

**User Story:** As an intelligence operator, I want a command-line tool that runs the ingestion pipeline, so that I can fetch and process intelligence on a schedule.

#### Acceptance Criteria

1. THE bellu-intel_CLI SHALL provide the subcommands fetch, parse, normalize, correlate, score, release, and run.
2. WHEN the run subcommand is invoked, THE bellu-intel_CLI SHALL execute fetch then parse then normalize then correlate in sequence.
3. WHERE the run subcommand is given mode daily, THE bellu-intel_CLI SHALL execute a full-schedule run.
4. WHERE the run subcommand is given mode incremental, THE bellu-intel_CLI SHALL execute an incremental run using stored cursors.
5. THE bellu-intel_CLI SHALL read configuration from the existing bellu.yaml file.

### Requirement 8: Intelligence Matcher correlates intel to graph assets (Phase 2)

**User Story:** As a security analyst, I want canonical intelligence joined to my graph assets, so that affected assets are identified.

#### Acceptance Criteria

1. WHEN the Intelligence_Matcher runs, THE Intelligence_Matcher SHALL join canonical vulnerabilities to graph Package nodes by matching purl name and testing that the package version lies within an affected range where introduced is less than or equal to the version and the version is less than fixed.
2. WHEN the Intelligence_Matcher runs, THE Intelligence_Matcher SHALL join canonical vulnerabilities to ContainerImage assets.
3. WHERE intelligence is AI-specific, THE Intelligence_Matcher SHALL join it to AIAgent, AIModel, and MCPServer assets.
4. WHEN the Intelligence_Matcher finds a match, THE Intelligence_Matcher SHALL emit a Candidate carrying the asset identifier, asset kind, vulnerability identifier, cited intel reference, and match basis.
5. IF a package version lies outside every declared affected range, THEN THE Intelligence_Matcher SHALL NOT report that package as matched.

### Requirement 9: Matcher enriches the graph without fabricating evidence (Phase 2)

**User Story:** As a platform operator, I want correlation to annotate the graph without weakening the evidence-backed invariant, so that trust in the graph is preserved.

#### Acceptance Criteria

1. WHEN the Intelligence_Matcher enriches the graph, THE Intelligence_Matcher SHALL upsert a Vulnerability node reusing the existing KindVulnerability kind.
2. WHEN the Intelligence_Matcher enriches the graph, THE Intelligence_Matcher SHALL upsert a HAS_VULN edge carrying the attributes cvss, epss, known_exploited, severity, and intel_ref.
3. THE Intelligence_Matcher SHALL write graph entities and edges only through the existing allow-listed SyncEntities and SyncEdges paths.
4. THE Intelligence_Matcher SHALL write no rows to the evidence table.
5. THE Intelligence_Matcher SHALL annotate only graph nodes that a prior customer raw_event already justified.
6. WHEN the Intelligence_Matcher records a citation, THE Intelligence_Matcher SHALL cite the intel record identifier and SHALL NOT cite a raw_event identifier.

### Requirement 10: Attack Path reachability input (Phase 2)

**User Story:** As a security analyst, I want reachability to vulnerable assets to feed risk, so that exposed assets are prioritized.

#### Acceptance Criteria

1. THE Attack_Path_Engine SHALL provide a read-only ReachesAsset operation that accepts a canonical identifier and returns whether an attack path reaches that asset.
2. THE Attack_Path_Engine SHALL perform no write to the graph while evaluating reachability.
3. WHEN the Risk_Engine computes a score, THE Risk_Engine SHALL use the reachability result for the asset as an input.

### Requirement 11: Deterministic, bounded, monotonic risk scoring (Phase 2)

**User Story:** As a security analyst, I want a transparent risk score per finding, so that I can prioritize remediation consistently.

#### Acceptance Criteria

1. THE Risk_Engine SHALL compute a risk score in the range 0 to 100 inclusive from CVSS, EPSS, the KEV_known_exploited flag, reachability or exposure, and AI-asset criticality.
2. FOR ALL identical risk inputs, THE Risk_Engine SHALL produce the same score (determinism).
3. WHEN any single risk input increases while others are held constant, THE Risk_Engine SHALL produce a score that is greater than or equal to the prior score (monotonicity).
4. THE Risk_Engine SHALL assign severity critical when the score is at least 90, high when at least 70, medium when at least 40, low when at least 10, and info otherwise.
5. WHERE a vulnerability is KEV_known_exploited, THE Risk_Engine SHALL assign a severity of at least high regardless of the computed score.

### Requirement 12: Findings lifecycle and persistence (Phase 2)

**User Story:** As a security analyst, I want findings deduplicated and tracked over time, so that I can see what is open and what has been resolved.

#### Acceptance Criteria

1. THE Findings_Store SHALL store at most one finding per combination of asset identifier and vulnerability identifier.
2. WHEN a correlation run matches an asset to a vulnerability, THE Findings_Store SHALL upsert a finding with status open, risk_score, and severity.
3. WHEN a later correlation run no longer matches an existing finding, THE Findings_Store SHALL transition that finding status from open to resolved.
4. WHEN the Findings_Store upserts an existing finding, THE Findings_Store SHALL preserve the first_seen timestamp and update the last_seen timestamp.
5. THE Findings_Store SHALL store the risk_score and severity on each finding.

### Requirement 13: Findings and intelligence API (Phase 2)

**User Story:** As a security analyst, I want API endpoints for findings and intelligence, so that tools and the UI can read them.

#### Acceptance Criteria

1. WHEN a request is made to GET /api/findings, THE Findings_API SHALL return findings filterable by severity, status, asset_kind, and limit.
2. WHEN a request is made to GET /api/findings/:id, THE Findings_API SHALL return the finding together with the cited intelligence and the evidence trail.
3. WHEN a request is made to GET /api/intel/vulnerabilities, THE Findings_API SHALL return canonical vulnerabilities filterable by severity, kev, and purl.
4. WHEN a request is made to GET /api/intel/vulnerabilities/:id, THE Findings_API SHALL return the canonical vulnerability with its aliases and affected ranges.
5. THE Findings_API SHALL gate every findings and intelligence endpoint behind the existing findings:read permission using the existing Gin RequirePermission group pattern.
6. IF a request to a findings or intelligence endpoint lacks the findings:read permission, THEN THE Findings_API SHALL deny the request.

### Requirement 14: Findings UI page (Phase 2)

**User Story:** As a security analyst, I want a Findings page in the console, so that I can review risk-scored findings visually.

#### Acceptance Criteria

1. THE Findings_UI SHALL provide a React Findings page within the existing frontend using the existing ChatGPT dark shadcn theme.
2. THE Findings_UI SHALL display for each finding a severity badge, the risk score, and the affected asset.
3. THE Findings_UI SHALL provide a link from each finding to the cited intelligence and evidence.
4. WHERE the principal holds the findings:read permission, THE Findings_UI SHALL display the Findings navigation entry.
5. IF the principal lacks the findings:read permission, THEN THE Findings_UI SHALL hide the Findings navigation entry.

### Requirement 15: AI-specific intelligence sources (Phase 3)

**User Story:** As a security analyst, I want AI-specific intelligence ingested, so that AI assets are covered by the corpus.

#### Acceptance Criteria

1. THE Global_Intelligence_System SHALL provide fetchers, parsers, and canonical mappings for MITRE ATLAS techniques, OWASP GenAI, AVID, and MCP vulnerabilities.
2. WHEN AI-specific intelligence is canonicalized, THE Global_Intelligence_System SHALL map it to the existing AI graph kinds AIAgent, AIModel, and MCPServer.

### Requirement 16: Signed air-gapped release bundle (Phase 3)

**User Story:** As an operator of a disconnected deployment, I want signed intelligence bundles, so that I can import verified intelligence without internet access.

#### Acceptance Criteria

1. WHEN the release builder exports intelligence, THE Global_Intelligence_System SHALL produce a canonical JSONL export with stable key ordering that yields byte-identical output for identical data.
2. THE Global_Intelligence_System SHALL compute a sha256 checksum per exported file and record it in a manifest.
3. THE Global_Intelligence_System SHALL sign the manifest with an ed25519 private key.
4. THE Global_Intelligence_System SHALL package the manifest, signature, and data files into a .tar.zst bundle.
5. WHEN the bellu-intel release import command processes a bundle, THE Global_Intelligence_System SHALL verify the ed25519 signature against a pinned public key and verify the per-file checksums before upserting into the intel_* tables.
6. IF signature verification or a checksum verification fails, THEN THE Global_Intelligence_System SHALL reject the bundle and SHALL NOT upsert any intel_* rows.

### Requirement 17: Deferred OpenSearch search package (Phase 3)

**User Story:** As a platform maintainer, I want the search package defined as an interface only, so that OpenSearch can be added later without a dependency now.

#### Acceptance Criteria

1. THE Global_Intelligence_System SHALL define the search package as an interface only.
2. THE Global_Intelligence_System SHALL add no OpenSearch runtime dependency in Phase 1.

### Requirement 18: Module, migrations, and reuse constraints (Cross-cutting)

**User Story:** As a platform maintainer, I want the feature to stay additive and reuse existing infrastructure, so that existing behavior and invariants are preserved.

#### Acceptance Criteria

1. THE Global_Intelligence_System SHALL remain within the existing Go module github.com/bellu/bellu-ai-cyber and SHALL add no new Go module.
2. THE Global_Intelligence_System SHALL append new database migrations after the 00004_rbac migration.
3. WHEN the application starts, THE Global_Intelligence_System SHALL run new migrations automatically through the existing embedded goose mechanism.
4. WHEN the full fetch-parse-normalize-correlate-score pipeline is run twice over unchanged upstream data, THE Global_Intelligence_System SHALL leave intel_* tables, graph enrichments, and findings in the same state except for last_seen timestamps (end-to-end idempotence).
5. THE Global_Intelligence_System SHALL preserve the existing evidence-backed graph invariant and the existing model allow-lists without modification.
6. THE Global_Intelligence_System SHALL reuse the existing Postgres access via pgx v5, Memgraph access via neo4j-go-driver v5, the existing configuration package, and the existing testcontainers test harness.

## Traceability to Correctness Properties

The following design correctness properties are validated by these requirements:

- **Determinism** (parsers, dedup, scoring): Requirements 3.2, 5.2, 11.2.
- **Idempotency** (dedup, persistence, end-to-end re-runs): Requirements 2.2, 5.3, 6.2, 18.4.
- **Evidence-citation** (correlation never fabricates evidence): Requirements 9.4, 9.5, 9.6, 18.5.
- **Bounded and monotonic scoring**: Requirements 11.1, 11.3.
- **Sound version matching**: Requirements 8.1, 8.5.
