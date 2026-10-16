# Requirements Document

## Introduction

This feature adds a code-security and automated-remediation subsystem to Bellu, built on the principle that **source code is just another evidence source**, like Steampipe cloud inventory, Zeek network flows, or a CycloneDX SBOM. Code analysis does not get its own product, findings database, or graph. It feeds the *existing* Bellu pipeline (raw evidence → normalize → resolve → sync into the graph), lands correlation results in the *existing* `finding.Finding` model and Postgres store, and surfaces them through the *existing* API with the *existing* RBAC.

The subsystem reuses Bellu infrastructure (the HTTP API and server wiring, the Postgres `*store.Store` pool and goose migrations, the `finding.Finding`/`finding.Store` model, the connector control-plane pattern of `Manager`/`Scheduler`/`Repository`/status FSM, the CycloneDX ingestion path, the graph model and writer, and `internal/auth` RBAC) and adds genuinely new pieces: a GitHub App integration, isolated repository-analysis workers that run four scanners (Semgrep, CodeQL, Trivy, Gitleaks), an AI investigation worker, a patch-verification workflow, a policy gate, and a narrowly-scoped PR-creation service.

The design is organized around three claims that must never be conflated: (1) **the AI generated a patch**, (2) **tests passed**, and (3) **the vulnerability is fixed**. These are distinct statements with distinct confidences, recorded as distinct states, and nothing merges automatically — a policy gate plus human review always stands between a verified fix and a merged PR.

### Accuracy and attribution caveats (please read before implementing)

- **Referenced Bellu symbols are to be verified at implementation.** This specification builds on Bellu symbols that were present at design time (`finding.Finding`, `finding.Store`, `connector.Manager`/`Scheduler`/`Repository`, `store.Store`, `model.Kind`, the CycloneDX source). Any symbol this document assumes (for example, that `finding.Store` can be extended with a code-finding link column, or that the graph writer accepts a new relation) must be confirmed against the tree during implementation before it is relied upon.
- **The AI investigation reuses and extends RepoAudit; it is inspired-by, not a reproduction.** The AI investigation worker reuses and extends [RepoAudit](https://github.com/PurCL/RepoAudit) — a compilation-free, tree-sitter-based, LLM repository-level auditing agent — for the memory-safety classes it already evaluates and extends the same engine (by rule data) for Bellu's security classes. It is inspired-by, not a reproduction of any specific system's internals, and these components are all new work, not pre-existing Bellu components. *Content was rephrased for compliance with licensing restrictions.*
- **RepoAudit is a Python project and is invoked as a tool, not rewritten in Go.** Because RepoAudit is Python and Bellu core is Go, the investigation engine runs inside the isolated worker tier and is invoked as an analysis tool/worker (the same binary/tool-wrapping stance taken for the scanners), not reimplemented in Go. The Go types in the design are the control-plane-side contract Bellu holds.
- **RepoAudit's reported precision numbers are its own, not Bellu's.** Any precision, cost, or latency figures associated with RepoAudit are RepoAudit's own reported results on its own benchmarks. Bellu's findings, languages, rule extensions, and sandbox differ; this specification makes no performance claim and will measure Bellu's own numbers.
- **AI reduces, but cannot guarantee, correctness.** Language-aware static analysis and executed tests are stronger evidence than an LLM assertion. The investigation worker is designed so that machine-checkable evidence (data-flow paths, compiled tests, re-scan results) dominates, and the LLM forms and challenges hypotheses rather than being the source of truth.

### Scope and Assumptions

- The subsystem ships in four MVP stages (repo scanning; AI investigation; verified remediation; PR automation) so value lands before the hardest parts are complete, and each stage is independently gated.
- Dependency vulnerabilities flow into the existing CycloneDX graph path; secrets and IaC misconfigurations stay Postgres-only findings.
- Isolated workers run as a separate deployable (the sandbox tier); the API process communicates with them over the control plane and never shares its credentials with them.

## Glossary

- **GitHub_App**: The installed GitHub App (not personal access tokens) that provides short-lived, per-repo-scoped installation tokens, with a clean separation between a read permission (clone/scan) and a write permission (open PRs).
- **Webhook_Receiver**: The GitHub webhook endpoint hosted in `backend/server` that validates the HMAC signature, dedupes by delivery id, and enqueues scans.
- **Code_Repository**: One row in `code_repositories` representing one installed repository, carrying `installation_id`, project/org scope, and the `pr_automation` opt-in flag.
- **Scan_Worker**: An isolated analysis worker that takes a `Job` (repo snapshot ref + least-privilege read token) and returns a structured `Result`, running the enabled scanners inside a disposable sandbox.
- **Scanner**: A single tool adapter (Semgrep, CodeQL, Trivy, or Gitleaks) behind a common `Scanner` interface producing a common `RawFinding` schema.
- **Normalizer**: The finding normalization-and-dedup component that maps `RawFinding`s to the canonical `NormalizedFinding` schema, computes the deterministic fingerprint, dedupes, and routes findings.
- **Fingerprint**: The deterministic, pure-function dedup key computed from a finding's category-specific fields (e.g. secrets use `secret_fingerprint + file + start_line + commit`).
- **Investigation_Worker**: The AI investigation worker that runs three sequential stages — Explorer, Investigator, Validator — for one finding and records a verdict.
- **Explorer**: The stage (mapping RepoAudit's Initiator + Explorer) that seeds starting points via tree-sitter patterns and the scanner finding's location, performs demand-driven one-function-at-a-time traversal, and assembles a compact, redacted `EvidenceBundle` for one finding.
- **Investigator**: The stage that tests the vulnerability hypothesis against the evidence bundle (is the input attacker-controlled, does tainted data reach the sink, is it mitigated).
- **Validator**: The stage that runs two explicit verification layers — parser-based alignment validation and LLM path-feasibility validation — discards any candidate that fails either layer as a false positive, re-reads survivors at the exact commit, assigns a verdict, and records missing evidence.
- **Fact_Cache**: The agent-memory cache of function-level data-flow facts keyed as `M(f, v@s)` (a fact about variable `v` at statement `s` in function `f`), used to avoid redundant re-analysis and to carry inter-procedural propagation across caller/callee boundaries; every cached fact is redacted and never holds a full secret.
- **Call_Depth_Bound**: The configurable bound (default approximately 4 calls deep) on inter-procedural propagation during the Explorer's demand-driven traversal, keeping cost bounded.
- **Bug_Type**: A class of vulnerability driving an investigation — the memory-safety classes reused as-is from RepoAudit (`npd` Null Pointer Dereference, `mlk` Memory Leak, `uaf` Use-After-Free) and the extension security classes (`injection`, `secret`, `dependency_vuln`).
- **Analyzer_Registry**: The registry that resolves a `Bug_Type` to its `AnalyzerSpec` (tree-sitter source/sink/sanitizer rules plus a reused-vs-extended flag), so adding a security class is adding rule data rather than forking the engine.
- **Verdict**: One of `confirmed`, `likely`, `inconclusive`, or `false_positive`.
- **Fix_Worker**: The worker that generates a patch (unified diff) for a well-understood vulnerability class.
- **Verification_Worker**: The worker that applies a patch in a disposable sandbox, runs the project's build and tests, and re-scans, recording the three claims separately.
- **Policy_Gate**: A pure, deny-by-default decision function over a finding, investigation, verification, and repo policy that emits allow/deny with reasons.
- **PR_Service**: The only component holding GitHub write access; it mints its own narrow write token and opens pull requests, never auto-merging.
- **Disposition**: The per-finding lifecycle state (`new`, `investigating`, `confirmed`, `false_positive`, `inconclusive`, `fix_proposed`, `fix_verified`, `fix_failed`, `pr_opened`, `resolved`) governed by an allowed-transition table.
- **Scan_Session**: One analysis run against a repo at an exact commit, with a status FSM mirroring the connector's.
- **Evidence_Bundle**: The compact, redacted context handed to the AI stages; every repo-derived string in it is untrusted data.
- **Code_Finding**: A deduped, persisted finding in `code_findings`, optionally linked to a `finding.Finding` via nullable `finding_id`.
- **Store**: The shared Bellu Postgres persistence layer (`*store.Store` pool, goose migrations).

## Requirements

### Requirement 1: GitHub App integration and webhook intake

**User Story:** As a security engineer, I want repositories integrated through a GitHub App whose webhooks reliably enqueue commit-pinned scans, so that scans run on the exact code a developer pushed without duplicate or lost work.

#### Acceptance Criteria

1. WHEN the GitHub_App installation webhook for a repository is received and validated, THE GitHub_App integration SHALL create or update exactly one `code_repositories` row for that repository carrying its `installation_id`, provider, owner, repo, and project/org scope, and SHALL set that row's state to installed.
2. WHEN the GitHub_App uninstallation webhook for a repository is received and validated, THE GitHub_App integration SHALL update the corresponding `code_repositories` row's state to not-installed so that the repository is no longer treated as installed, while retaining the row and its prior scan history.
3. WHEN the Webhook_Receiver receives a webhook delivery, THE Webhook_Receiver SHALL validate the delivery's HMAC signature.
4. IF a received webhook delivery carries no signature or a signature that does not validate, THEN THE Webhook_Receiver SHALL reject the delivery with a non-2xx response, SHALL NOT enqueue any scan, and SHALL NOT mutate any `code_repositories` row.
5. WHEN the Webhook_Receiver receives a webhook delivery whose delivery id matches a delivery id recorded as processed within the preceding 72 hours, THE Webhook_Receiver SHALL treat the redelivery as a no-op, SHALL return a 2xx response, and SHALL NOT enqueue a duplicate scan.
6. WHEN the Webhook_Receiver accepts a valid pull-request or push webhook delivery, THE Webhook_Receiver SHALL enqueue a scan whose commit SHA equals the exact commit SHA carried in that event and SHALL record the event's delivery id as processed.
7. THE Webhook_Receiver SHALL set the enqueued scan job's commit SHA equal to the full commit SHA in the triggering webhook event, and SHALL NOT resolve the scan target to a floating reference such as a branch name, tag, or "latest".
8. IF a transient failure prevents the Webhook_Receiver from enqueuing an accepted delivery, THEN THE Webhook_Receiver SHALL return a non-2xx response so that GitHub retries the delivery, and SHALL NOT record the delivery id as processed.
9. THE GitHub_App integration SHALL request read permission (contents, metadata) for scanning separately from write permission (pull requests, branch contents), and SHALL grant write permission only for repositories opted into PR automation.
10. WHERE a repository has opted into GitHub code-scanning-alert mirroring, THE GitHub_App integration SHALL mirror Bellu findings as GitHub code-scanning alerts for that repository.

### Requirement 2: Isolated repository scanning

**User Story:** As a security engineer, I want each repository scanned by complementary tools inside an isolated worker on a commit-pinned checkout, so that detection is broad and reproducible without exposing Bellu to untrusted repository code.

#### Acceptance Criteria

1. WHEN a Scan_Worker runs a scan job, THE Scan_Worker SHALL check out the exact pinned 40-character commit SHA supplied in the job before invoking any scanner.
2. IF the pinned commit SHA in a scan job cannot be resolved or checked out, THEN THE Scan_Worker SHALL fail the job cleanly, return a fatal error indicating checkout failure for that job, and SHALL NOT invoke any scanner.
3. WHEN a Scan_Worker checks out a repository, THE Scan_Worker SHALL use only the short-lived, repo-scoped, read-only token supplied in the job for that checkout.
4. WHEN a Scan_Worker runs a scan job, THE Scan_Worker SHALL run Semgrep, Trivy, and Gitleaks for that scan.
5. WHERE a scan job's scan options enable deep analysis for a supported language, THE Scan_Worker SHALL run CodeQL for that scan selectively, in addition to Semgrep, Trivy, and Gitleaks.
6. WHEN each enabled scanner completes, THE Scan_Worker SHALL return a structured result that records each tool's version, each tool's run status as one of succeeded or failed, and the tool's `RawFinding`s in the common raw-finding schema.
7. WHEN Trivy produces SBOM output during a scan, THE Scan_Worker SHALL return that SBOM as a CycloneDX artifact so that it can be ingested through the existing CycloneDX source path.
8. IF one scanner exits with a non-zero status or fails to produce parseable output during a scan, THEN THE Scan_Worker SHALL record that tool's run status as failed, retain the findings already produced by the remaining scanners, and SHALL continue and complete the scan rather than failing the whole scan.
9. IF a scan job exceeds its configured wall-clock limit or configured resource limit, THEN THE Scan_Worker SHALL terminate all running scanners for that job, fail the job cleanly, and return a fatal error indicating limit exhaustion for that job.

### Requirement 3: Finding normalization and deduplication

**User Story:** As a security engineer, I want findings from all tools normalized to one schema and deduplicated deterministically, so that overlapping tools and repeated scans produce one stable finding per real issue.

#### Acceptance Criteria

1. THE Normalizer SHALL map every `RawFinding` to the common `NormalizedFinding` schema and SHALL map each tool-native severity to a value in the canonical severity vocabulary already used by `finding.Finding`.
2. IF a `RawFinding` carries a tool-native severity that does not map to any value in the canonical severity vocabulary, THEN THE Normalizer SHALL assign it the highest canonical severity value and SHALL retain the finding rather than dropping it.
3. THE Normalizer SHALL compute the Fingerprint as a deterministic function of a fixed set of a finding's fields, producing the identical fingerprint for the same logical finding regardless of the order in which findings are processed or the number of times a scan is re-run, where the input fields are: for a code vulnerability or dependency vulnerability, the rule id, file, start line, end line, and category; for a secret, the tool-reported secret fingerprint, file, start line, and commit; and for an IaC misconfiguration, the rule id, file, and resource key.
4. WHEN the same logical secret is reported by both Trivy and Gitleaks such that both reports resolve to the same secret fingerprint, file, start line, and commit, THE Normalizer SHALL compute one identical fingerprint for both reports and SHALL collapse them into exactly one `code_findings` row.
5. WHEN the Normalizer merges a finding reported by more than one tool, THE Normalizer SHALL set the resulting finding's `tools` set to exactly the union of the tools that reported that finding and SHALL set its severity to the highest value, per the canonical severity vocabulary's defined ordering, among the severities reported by those tools.
6. WHEN the Normalizer upserts normalized findings for a re-scan whose inputs are unchanged from the prior scan, THE Normalizer SHALL produce the identical set of `code_findings` rows as the prior scan, with identical fingerprints and `tools` sets, and SHALL change only the `last_seen` timestamp.
7. WHEN a normalized finding's category is a dependency vulnerability, THE Normalizer SHALL route that finding through the existing CycloneDX graph path (`Package`/`Vulnerability`/`HAS_VULN`) rather than minting a parallel package graph.
8. WHEN a normalized finding's category is a secret or an IaC misconfiguration, THE Normalizer SHALL persist that finding as a Postgres-only `code_findings` row and SHALL NOT promote it to a graph node.
9. THE Normalizer SHALL emit a finding only when at least one scanner reported it, and for each emitted finding SHALL restrict its tool attribution and evidence to exactly the tools and evidence that scanners actually reported.

### Requirement 4: AI investigation

**User Story:** As a security engineer, I want each finding investigated by an evidence-driven AI pipeline that reaches a verdict grounded in machine-checked evidence, so that I can triage findings by confidence instead of reading every raw alert.

#### Acceptance Criteria

1. WHEN the Investigation_Worker investigates a finding, THE Investigation_Worker SHALL run the Explorer, Investigator, and Validator stages in sequence, each stage starting only after the prior stage completes, and IF any stage errors THEN THE Investigation_Worker SHALL halt the pipeline and apply the error handling in criterion 16.
2. WHEN the Investigation_Worker begins investigating a finding, THE Investigation_Worker SHALL resolve the finding's Bug_Type to its `AnalyzerSpec` through the Analyzer_Registry before running the Explorer.
3. IF the Analyzer_Registry has no `AnalyzerSpec` for a finding's Bug_Type, THEN THE Investigation_Worker SHALL not run the Explorer, SHALL record an `inconclusive` verdict with the missing `AnalyzerSpec` noted as missing evidence, and SHALL NOT advance the finding to fix or PR stages.
4. WHEN the Explorer initiates an investigation for a finding, THE Explorer SHALL seed starting points from both the scanner finding's exact location and the tree-sitter source patterns registered in the `AnalyzerSpec` for that finding's Bug_Type.
5. WHEN the Explorer traverses code for a finding, THE Explorer SHALL explore one function at a time following execution paths on demand rather than eagerly expanding the whole repository, and SHALL bound inter-procedural propagation across caller/callee boundaries by the configured Call_Depth_Bound, an integer in the range 1 through 20 inclusive, defaulting to 5 when unconfigured.
6. WHEN the Explorer computes a function-level data-flow fact, THE Explorer SHALL cache that fact in the Fact_Cache keyed as `M(f, v@s)` and SHALL reuse a cached fact instead of recomputing it when the same key is encountered again.
7. THE Explorer SHALL store every Fact_Cache entry in redacted form and SHALL NOT cache or carry forward any full secret value.
8. WHEN the Explorer runs for a finding, THE Explorer SHALL assemble a compact Evidence_Bundle containing the affected function, callers and callees bounded by the Call_Depth_Bound, candidate taint sources and sinks, related findings at the same location, the relevant redacted `M(f, v@s)` facts, and CodeQL path results when CodeQL ran.
9. WHEN the finding's Bug_Type is a memory-safety class (`npd`, `mlk`, or `uaf`), THE Investigation_Worker SHALL drive RepoAudit's existing analyzer for that class unchanged.
10. WHEN the finding's Bug_Type is a security extension class (`injection`, `secret`, or `dependency_vuln`), THE Investigation_Worker SHALL analyze it using the same engine driven by the tree-sitter source, sink, and sanitizer rule data in that Bug_Type's `AnalyzerSpec`, without forking the engine.
11. WHEN the Validator evaluates a candidate, THE Validator SHALL run two verification layers — a parser-based alignment-validation layer that checks the inferred data-flow facts respect the code's actual control-flow order, and an LLM path-feasibility-validation layer that rejects an inter-procedural path whose branch conditions cannot all hold at once.
12. IF a candidate fails either the alignment-validation layer or the path-feasibility-validation layer, THEN THE Validator SHALL discard that candidate as a false positive.
13. WHEN the Validator completes a finding, THE Investigation_Worker SHALL record a verdict of exactly one of `confirmed`, `likely`, `inconclusive`, or `false_positive`, together with a confidence value in the range 0 through 1 inclusive and any missing evidence that would change the verdict.
14. WHEN a claim in the Evidence_Bundle can be checked by a tool result (a tree-sitter alignment check, a CodeQL path, a compiled snippet, or a focused test), THE Investigation_Worker SHALL let that machine-checked result govern the verdict over any conflicting LLM assertion.
15. WHEN the Investigation_Worker records a verdict, THE Investigation_Worker SHALL record the reasons supporting the verdict and the evidence it could not verify.
16. IF the Investigation_Worker errors or returns an `inconclusive` verdict for a finding, THEN THE Investigation_Worker SHALL persist that finding with its verdict and missing evidence and SHALL NOT advance that finding to fix or PR stages.

### Requirement 5: Finding-to-fix-to-verification-to-PR workflow and outcome states

**User Story:** As a security engineer, I want remediation to keep patch generation, test results, and fix confirmation as separate recorded claims gated by policy and human review, so that a PR is only ever opened for a genuinely verified fix and is never merged automatically.

#### Acceptance Criteria

1. THE workflow SHALL record a finding's disposition as exactly one value drawn from the set { `new`, `investigating`, `confirmed`, `false_positive`, `inconclusive`, `fix_proposed`, `fix_verified`, `fix_failed`, `pr_opened`, `resolved` }, and IF a disposition value outside this set is supplied, THEN THE workflow SHALL reject the write, return an error indication identifying the invalid value, and leave the existing disposition unchanged.
2. WHEN a disposition event is applied to a finding, THE workflow SHALL perform the transition only if the `(from, event)` pair is present in the allowed-transition table, and IF the `(from, event)` pair is absent from the allowed-transition table, THEN THE workflow SHALL reject the event, return an error indication identifying the disallowed `(from, event)` pair, and leave the disposition unchanged.
3. THE workflow SHALL record the three claims — a patch was generated, tests passed, and the vulnerability no longer reproduces — as three distinct fields in `code_remediations` (`patch_diff`, `tests_passed`, and `rescan_clean` respectively).
4. WHEN a remediation verification event is applied to a finding, THE workflow SHALL transition the finding to `fix_verified` only if both `tests_passed` and `rescan_clean` are true for that remediation.
5. IF a remediation's patch was generated and either `tests_passed` or `rescan_clean` is false, THEN THE workflow SHALL transition the finding to `fix_failed` rather than `fix_verified`.
6. WHEN the PR_Service applies an open-pull-request event for a finding, THE workflow SHALL reach `pr_opened` only if the current disposition is `fix_verified`, the transition applied is `gate_allow_and_open_pr`, the finding is `confirmed`, and the Policy_Gate returns allow.
7. THE workflow SHALL NOT provide any transition that merges a pull request automatically.
8. THE Policy_Gate SHALL deny by default and SHALL return allow only when all of the following hold: the repository is opted into PR automation, the bug class is in the supported-bug-class set, the finding confidence is greater than or equal to the configured confidence threshold (a configurable value in the range 0.00 to 1.00 inclusive), and verification passed (both `tests_passed` and `rescan_clean` are true for the finding's remediation).
9. IF the Policy_Gate returns deny when an open-pull-request event is applied, THEN THE workflow SHALL not open a pull request, SHALL leave the finding's disposition unchanged at `fix_verified`, and SHALL return an error indication conveying that policy denied the PR.

### Requirement 6: Security boundary and worker isolation

**User Story:** As a platform operator, I want every worker that touches repository content to run in a disposable, least-privilege, network-restricted sandbox that treats repository content as data, so that hostile repositories cannot execute attacks, steal credentials, or move laterally.

#### Acceptance Criteria

1. WHEN a repository job runs, THE Scan_Worker SHALL run it in its own disposable, single-use sandbox that is created at job start, destroyed within 5 seconds of job completion, and shares no filesystem, memory, environment variables, or cached credentials with any prior or concurrent job or repository.
2. THE Scan_Worker SHALL run as a non-root user (UID not equal to 0), with all Linux capabilities dropped except those on an explicit allow-list required for checkout and scanning, and with the `no-new-privileges` flag enabled.
3. THE Scan_Worker SHALL enforce a per-job CPU quota, memory quota, process/PID count limit, and writable-disk quota, each configured to a fixed maximum value, and SHALL enforce a per-stage wall-clock timeout of at most 900 seconds.
4. IF a job exceeds any configured CPU, memory, PID, disk, or wall-clock limit, THEN THE Scan_Worker SHALL terminate the job, mark it as failed with a result indicating which limit was exceeded, and release all sandbox resources without leaving residual state.
5. THE Scan_Worker SHALL default-deny all outbound network egress, SHALL permit the checkout stage to establish connections only to hosts on a fixed GitHub allow-list, and SHALL block all access to cloud instance metadata endpoints.
6. THE Scan_Worker SHALL NOT mount the host Docker socket, SHALL NOT mount any host filesystem path, and SHALL NOT be provided with cloud credentials, a GitHub write-scoped token, or production secrets.
7. THE Scan_Worker SHALL be provided with a short-lived, repository-scoped, read-only checkout token as the only credential available to it, and IF any other credential is present in the sandbox environment at job start, THEN THE Scan_Worker SHALL fail the job before executing repository content.
8. WHEN repository-derived content (source, comments, READMEs, commit messages, or tool output) is included in an AI prompt, THE Investigation_Worker SHALL enclose that content within explicit delimiters that mark it as untrusted data separated from instruction content, and SHALL NOT execute, interpret, or act on any instruction contained within that content.
9. THE Investigation_Worker SHALL restrict the tool and function outputs the model may act on to a fixed allow-list, and IF the model requests a tool or function not on the allow-list, requests a network call, or requests credential access, THEN THE Investigation_Worker SHALL reject the request and continue without performing it.
10. WHERE a stage executes repository-controlled code (the project test suite or a CodeQL database build), THE workflow SHALL run that stage in a disposable micro-VM or dedicated sandbox tier with its own kernel rather than a shared-kernel container.
11. THE PR_Service SHALL be the only component granted GitHub write access, and IF a Scan_Worker, Investigation_Worker, Fix_Worker, or Verification_Worker attempts to open or push a pull request, THEN the attempt SHALL be denied.

### Requirement 7: Secret handling and redaction

**User Story:** As a security engineer, I want detected secrets reduced to a fingerprint, location, and redacted preview everywhere they are stored or emitted, so that a discovered credential is never leaked by the system that found it.

#### Acceptance Criteria

1. WHEN a secret finding is persisted, THE subsystem SHALL store only its fingerprint, its location (file path, line number, and commit identifier), and a redacted preview, and SHALL NOT store the full credential value in any field.
2. THE subsystem SHALL NOT write a full credential value into logs, AI prompts, finding descriptions, graph attributes, or API responses.
3. WHEN the Explorer builds an Evidence_Bundle for a finding carrying a secret, THE Explorer SHALL populate every AI-facing and persisted field (the Evidence_Bundle, `evidence_summary`, `preview`, `model_meta`, and logs) with only the fingerprint, location, and redacted preview, and SHALL NOT include the full credential value in any such field.
4. WHEN a redacted preview of a secret is produced, THE subsystem SHALL mask all characters of the credential except at most the first 4 and the last 4 characters, and WHERE the credential length is 8 characters or fewer, THE subsystem SHALL mask every character so that no plaintext fragment is revealed.
5. IF a tool emits a secret value in its output or an error message, THEN THE subsystem SHALL replace that value with a redacted preview before persisting it to `code_scan_runs.error`, logs, or any other stored field, and the stored field SHALL NOT contain the full credential value.
6. IF persisting, logging, or emitting a secret finding would include a full credential value, THEN THE subsystem SHALL reject the operation, SHALL NOT persist or emit the full credential value, and SHALL record an error indicating that unredacted secret content was blocked.

### Requirement 8: Data model and commit-pinned persistence

**User Story:** As a security engineer, I want code-security data persisted in dedicated project-scoped tables that reuse Bellu's store and link to the existing finding model, with every record tied to an exact commit, so that findings are tenant-isolated and every artifact describes a specific commit.

#### Acceptance Criteria

1. THE Store SHALL create the `code_*` tables (`code_repositories`, `code_scan_runs`, `code_findings`, `code_evidence`, `code_investigations`, `code_remediations`, `code_pull_requests`) through migration `00013_code_security.sql`, using the shared Postgres pool and the existing goose migration conventions.
2. THE Store SHALL scope every `code_*` record by a non-null project identifier and a non-null org identifier, consistent with `connector_connections`, so that RBAC project scoping and tenant isolation apply unchanged.
3. IF a `code_*` record is created without a project identifier or without an org identifier, THEN THE Store SHALL reject the write and SHALL NOT persist the record.
4. WHEN a dependency vulnerability is correlated into `findings`, THE Store SHALL populate the `code_findings.finding_id` with the identifier of the correlated `finding.Finding` record.
5. WHEN a secret, code vulnerability, or IaC misconfiguration that has no canonical vuln to correlate is persisted, THE Store SHALL leave `code_findings.finding_id` null.
6. THE Store SHALL record a non-empty commit SHA of 7 to 64 hexadecimal characters on every scan run, every code finding location, and every remediation.
7. IF a scan run, code finding location, or remediation is written with an empty commit SHA, a non-hexadecimal commit SHA, or a floating reference such as "latest", "HEAD", or a branch name, THEN THE Store SHALL reject the write and SHALL NOT persist the record.
8. WHEN the Store starts up and detects a session in `scanning` or `investigating` state, THE Store SHALL transition that session to `interrupted` so that the session is re-runnable from the pinned commit.
9. WHEN migration `00013_code_security.sql` is applied to a fresh database and then reverted, THE Store SHALL complete both the up migration and the down migration without error, leaving the schema in its pre-migration state.

### Requirement 9: Evidence-gated graph integration

**User Story:** As a security engineer, I want cross-domain graph links created only when evidence establishes the relationship, so that the attack-path graph joining repo, image, package, workload, and exposure never contains fabricated edges.

#### Acceptance Criteria

1. WHEN code analysis produces graph-eligible evidence, THE subsystem SHALL feed that evidence through the existing normalize → resolve → sync pipeline into the existing Memgraph graph rather than any separate graph store.
2. WHEN a dependency vulnerability is ingested, THE subsystem SHALL reuse the existing `Package`, `Vulnerability`, and `HAS_VULN` nodes and relations from the CycloneDX path and SHALL NOT create new node kinds or relation kinds for that vulnerability.
3. IF an evidence item (a build provenance or attestation tying an image digest to the repo and commit, an SBOM subject tying the image digest to the source repo and commit, or an observed CI configuration pushing to that exact image reference) establishes a repo-to-image relationship, THEN THE subsystem SHALL create exactly one `BUILT_FROM` (repo→image) edge for that relationship.
4. IF a repository name matches an image name and no evidence item of the kinds enumerated in criterion 3 establishes the relationship, THEN THE subsystem SHALL create zero repo-to-image edges.
5. WHEN the subsystem creates any graph edge, THE subsystem SHALL attach to that edge a reference to the justifying `Evidence` item and a confidence value that is a decimal in the inclusive range 0.0 through 1.0.
6. IF the subsystem would create a graph node, edge, or attribute that no scanner result or observation produced, THEN THE subsystem SHALL NOT create that node, edge, or attribute.
7. IF edge creation is requested but no justifying `Evidence` reference is available, THEN THE subsystem SHALL create zero edges and SHALL record an indication that the edge was rejected for missing evidence, preserving existing graph contents unchanged.

### Requirement 10: RBAC and read API

**User Story:** As a security engineer, I want code-security data exposed through the existing authenticated API under new permission keys, so that access follows Bellu's existing authorization model.

#### Acceptance Criteria

1. THE subsystem SHALL reuse the existing `internal/auth` RBAC and SHALL add exactly two new permission keys to the existing permission catalog, following the existing `domain:action` key format: one key for code-security read operations and one key for code-security manage operations.
2. THE subsystem SHALL expose code-security read endpoints for repositories, scan runs, findings, investigations, remediations, and pull requests over the existing bellu-api server, each requiring the code-security read permission through the existing RBAC enforcement.
3. WHEN a principal with the code-security read permission requests a code-security read endpoint, THE subsystem SHALL return only repositories and findings belonging to projects in the principal's authorized project set, where a global-scope principal's authorized set is all projects and a project-scope principal's authorized set is only the projects assigned to that principal.
4. WHEN a principal with the code-security read permission requests a code-security read endpoint returning a collection, THE subsystem SHALL return results in pages of at most 100 items per page, defaulting to 50 items when no page size is specified.
5. IF a principal has the code-security read permission but is authorized for zero projects, THEN THE subsystem SHALL return an empty collection without an error indication.
6. IF a principal is not authenticated when requesting a code-security endpoint, THEN THE subsystem SHALL deny the request through the existing RBAC enforcement with an authentication-required error indication and without returning any code-security data.
7. IF an authenticated principal lacks the required code-security permission for an operation, THEN THE subsystem SHALL deny the request through the existing RBAC enforcement with a permission-denied error indication, without returning any code-security data and without modifying any state.

### Requirement 11: Incremental MVP staging

**User Story:** As a product owner, I want the subsystem delivered in independently gated stages, so that repository scanning delivers value before AI investigation, remediation, and PR automation are complete.

#### Acceptance Criteria

1. THE subsystem SHALL gate the AI investigation, verified remediation, and PR automation capabilities behind feature gates that are independent of the repository-scanning capability.
2. WHILE only the repository-scanning stage is enabled, THE subsystem SHALL scan repositories, normalize and dedupe findings, and persist them through the existing findings infrastructure, and SHALL complete those scans without requiring the AI investigation, remediation, or PR automation stages to be enabled.
3. IF a capability whose feature gate is disabled is invoked, THEN THE subsystem SHALL reject the invocation with an error indication that the stage is disabled and SHALL leave finding and repository state unchanged.
4. WHEN a repository is newly configured, THE subsystem SHALL default its PR-automation gate to disabled until explicitly opted in.
5. WHERE PR automation is not enabled for a repository, THE subsystem SHALL still scan, investigate, and verify fixes for that repository without opening a pull request.
6. THE subsystem SHALL enable each later stage additively, such that enabling a later stage does not disable or block the value delivered by an earlier stage.
