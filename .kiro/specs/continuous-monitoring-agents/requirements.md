# Requirements Document

## Introduction

This feature adds continuous, event-triggered monitoring to the Bellu AI Cyber
platform. Incoming signals (CI results, newly-opened vulnerabilities, code
changes, and discovered resources) are durably captured, classified, routed to
an agent team, and executed as read-only agent runs whose trace and generated
report flow back to the Go control plane and surface in the UI.

The Go control plane owns the durable queue, ingest, classify/route, the claim
API, and all status/trace/report write-back. The Python agents plane consumes:
it claims assigned work, builds a read-only run descriptor, awaits the scan
engine, and reports status/trace/report back.

These requirements are derived from the approved design document
(`design.md`) and are organized into the three delivery phases plus a
cross-cutting section:

- Phase 1 — Monitoring triggers (durable-first inbox, idempotent ingest,
  classify + route, event taps, scheduled sweep, claim API, status write-back,
  Mission Control read, worker loop).
- Phase 2 — Run trace/timeline (`run_agents` + `run_handoffs`, fixed 11-stage
  vocabulary + mapper, trace write-back, run detail/timeline reads).
- Phase 3 — Finding/remediation detail (`finding_reports`, report write-back,
  extended finding read, Finding Detail page, PoC redaction).
- Cross-cutting — tenant isolation, permissions, and error handling that apply
  to every new seam.

Two safety invariants hold across every new seam: tenant isolation (org is
always resolved from the authenticated principal; cross-org reads are 404 with
no existence disclosure) and PoC redaction (the weaponized proof-of-concept
body is never transported, stored, or served).

## Glossary

- **Monitor_Event**: A row in the `monitor_events` table. Serves simultaneously
  as the durable event log entry, the work-queue item, and the carrier of agent
  assignment and run state for one incoming signal.
- **Delivery_Id**: The per-source idempotency key for a Monitor_Event, unique
  per `(org_id, source)`. GitHub reuses the `X-GitHub-Delivery` header;
  vulnerability events use `asset_id + ":" + vuln_id`; discovery resource events
  use `discovery_session_id + ":" + asset_id`.
- **Leg**: One agent instance recorded in one Stage for a run (agent-level
  granularity). A Leg is identified by `(run_id, agent_id)`.
- **Stage**: One member of the fixed 11-stage pipeline vocabulary (RECON,
  POSTURE, DISCOVERY, EXPLOIT, SWEEP, VERIFY, STABILITY, SCORE, FORMAT,
  COVERAGE, REPORT), plus the `other` bucket for unmapped agent names.
- **Team**: The agent team a routed Monitor_Event targets (`red_team`,
  `blue_team`, `pr_reviewer`, `triage`) in Phase 1, and the report classifier
  (`red`, `blue`) in Phase 3.
- **Triggered_By**: A field on an agent run recording which Monitor_Event
  launched it, or `manual` for operator-launched runs.
- **Unvalidated**: The status assigned to findings produced by auto-triggered
  read-only runs, indicating the finding was not confirmed by active testing.
- **Claim**: The atomic operation that moves routed Monitor_Events to `claimed`
  using a `SELECT ... FOR UPDATE SKIP LOCKED` statement, so a claimed row is
  never handed to a second worker.
- **Delta**: The nothing→open transition of a finding (`UpsertResult.Created ==
  true`). A vulnerability Monitor_Event fires only on a Delta, so re-seen
  still-open findings emit nothing.
- **Monitor_Store**: The Go store component (`internal/store/monitor_events.go`)
  that owns insert, route, claim, status, and list operations on Monitor_Events.
- **Router**: The Go component (`internal/monitor/router.go`) that classifies a
  signal into an `event_kind` and statically routes the kind to a Team.
- **Sweeper**: The Go component (`internal/monitor/sweep.go`) that periodically
  re-runs correlation per org.
- **Worker_Loop**: The Python agents-plane loop that claims assigned
  Monitor_Events, builds read-only run descriptors, awaits the scan engine, and
  reports back.
- **Control_Plane**: The Go control plane that owns the durable queue and all
  write-back endpoints.
- **Finding_Detail_Page**: The committed frontend page
  (`features/issues/FindingDetail.tsx`) that renders a finding's red/blue team
  and remediation detail.

## Requirements

## Phase 1 — Monitoring Triggers

### Requirement 1: Durable-first ingest

**User Story:** As a platform operator, I want every incoming event persisted
before it is processed, so that nothing is lost if the system crashes
mid-processing.

#### Acceptance Criteria

1. WHEN an event is received at the ingest seam, THE Monitor_Store SHALL persist the event to the `monitor_events` table in the `pending` state before any classification, routing, or run launch occurs, and SHALL NOT begin any such downstream work until the `pending` row is durably committed.
2. THE `monitor_events` table SHALL serve as both the durable event log and the work queue, carrying the agent Team assignment and run state on the same row.
3. WHEN a persisted Monitor_Event is routed, claimed, started, or reaches a terminal state, THE Monitor_Store SHALL record the corresponding state on that same row, where state is exactly one of `pending`, `routed`, `claimed`, `running`, `completed`, `failed`, or `skipped`, and `completed`, `failed`, and `skipped` are terminal.
4. IF persisting the `pending` row fails, THEN THE Monitor_Store SHALL perform no classification, routing, or run launch, SHALL leave no partial row, and SHALL surface the failure to the caller.

### Requirement 2: Idempotent ingest

**User Story:** As a platform operator, I want duplicate deliveries to be
no-ops, so that redelivered signals never create a second event or a second
run.

#### Acceptance Criteria

1. WHEN an event is received whose `(org_id, source, delivery_id)` already exists, THE Monitor_Store SHALL leave exactly one row for that key, creating no second row and launching no second run.
2. WHEN a duplicate `(org_id, source, delivery_id)` insert is attempted, THE Monitor_Store SHALL return the `ErrDuplicateMonitorEvent` sentinel to the caller.
3. WHEN two inserts for the same `(org_id, source, delivery_id)` race concurrently, THE Monitor_Store SHALL commit exactly one row and return `ErrDuplicateMonitorEvent` to every losing caller.
4. IF an event is received with an empty or missing Delivery_Id, THEN THE Monitor_Store SHALL reject it, creating no row and launching no run.
5. WHEN a GitHub event is ingested, THE GitHub_Tap SHALL use the `X-GitHub-Delivery` header value as the Delivery_Id.
6. WHEN a vulnerability event is ingested, THE Vulnerability_Tap SHALL use `asset_id + ":" + vuln_id` as the Delivery_Id.

### Requirement 3: Classification and static routing

**User Story:** As a platform operator, I want each event classified and routed
to the right agent team, so that signals reach the team that can act on them.

#### Acceptance Criteria

1. WHEN classifying a received signal, THE Router SHALL derive the canonical `event_kind` as exactly one of `ci`, `vulnerability`, `code_change`, or `resource`, or none, as the only reader of source-specific payload shapes.
2. WHERE the `event_kind` is `ci`, THE Router SHALL route the Monitor_Event to the `red_team` Team.
3. WHERE the `event_kind` is `vulnerability`, THE Router SHALL route the Monitor_Event to the `blue_team` Team.
4. WHERE the `event_kind` is `code_change`, THE Router SHALL route the Monitor_Event to the `pr_reviewer` Team.
5. WHERE the `event_kind` is `resource`, THE Router SHALL route the Monitor_Event to the `triage` Team.
6. IF the `event_kind` is unknown or unroutable, THEN THE Router SHALL assign no Team and THE Monitor_Store SHALL move the Monitor_Event to the terminal `skipped` state and launch no run.

### Requirement 4: Event taps

**User Story:** As a platform operator, I want signals captured from GitHub,
discovery, and the finding lifecycle, so that monitoring is driven by real
platform activity.

#### Acceptance Criteria

1. WHEN a GitHub webhook check-run or workflow event is received, THE GitHub_Tap SHALL persist exactly one Monitor_Event with `source = github` and `event_kind = ci`, using the `X-GitHub-Delivery` value as the Delivery_Id.
2. WHEN a GitHub webhook push or pull-request event is received, THE GitHub_Tap SHALL persist exactly one Monitor_Event with `source = github` and `event_kind = code_change`, using the `X-GitHub-Delivery` value as the Delivery_Id.
3. IF a GitHub webhook is received with no `X-GitHub-Delivery` value, THEN THE GitHub_Tap SHALL persist no Monitor_Event.
4. WHEN discovery completes, THE Resource_Tap SHALL persist exactly one Monitor_Event with `source = discovery` and `event_kind = resource` per newly discovered asset, using `discovery_session_id + ":" + asset_id` as the Delivery_Id.
5. WHEN a finding upsert reports `UpsertResult.Created == true`, THE Vulnerability_Tap SHALL persist exactly one Monitor_Event with `source = correlate` and `event_kind = vulnerability`.
6. IF a finding upsert reports `UpsertResult.Created == false`, THEN THE Vulnerability_Tap SHALL persist no Monitor_Event.

### Requirement 5: Scheduled re-correlation sweep

**User Story:** As a platform operator, I want correlation re-run periodically
without re-triggering already-open findings, so that monitoring stays current
without a re-trigger storm.

#### Acceptance Criteria

1. WHEN the sweep runs for an org, THE Sweeper SHALL re-run correlation for that org and upsert the resulting finding matches.
2. WHEN a finding first transitions nothing→open during a sweep, THE Vulnerability_Tap SHALL emit exactly one vulnerability Monitor_Event for that finding.
3. WHILE a finding remains open across consecutive sweeps, THE Vulnerability_Tap SHALL emit no new Monitor_Event for that finding, because the re-upsert reports `UpsertResult.Created == false`.

### Requirement 6: Claim API with exclusive locking

**User Story:** As a worker, I want to claim routed events exclusively, so that
a claimed event is never processed by a second worker.

#### Acceptance Criteria

1. WHEN claiming work for an org, THE Monitor_Store SHALL select up to the caller-supplied limit of routed rows, ordered oldest-first, using `SELECT ... FOR UPDATE SKIP LOCKED`, and SHALL move the selected rows to the `claimed` state in the same transaction.
2. WHILE a row is claimed by one worker, THE Monitor_Store SHALL NOT return that same row to any other concurrent worker.
3. WHEN a row is claimed, THE Control_Plane SHALL stamp the launched `run_id` onto the Monitor_Event row.
4. WHEN the Worker_Loop obtains claims, THE Worker_Loop SHALL build a read-only run descriptor, await the scan engine, and report status back to the Control_Plane.
5. IF a claim returns no rows, THEN THE Worker_Loop SHALL sleep, doubling the idle interval up to a configured maximum.
6. WHEN a claim returns one or more rows after an idle period, THE Worker_Loop SHALL reset the idle interval to its configured initial value.

### Requirement 7: Status write-back

**User Story:** As a worker, I want to report run status back to the control
plane, so that both the run and its originating event reflect the terminal
state.

#### Acceptance Criteria

1. WHEN a status write-back is received at `PUT /api/agent/runs/:id/status`, THE Control_Plane SHALL fold the reported status and findings count onto the `agent_runs` row, where status is exactly one of `running`, `completed`, or `failed`.
2. WHEN a status write-back is received, THE Control_Plane SHALL fold the corresponding status transition onto the linked `monitor_events` row.
3. IF a status write-back carries a status value outside the allowed set, THEN THE Control_Plane SHALL reject it with `400 Bad Request` and change no row.
4. WHEN a run errors, THE Worker_Loop SHALL report a `failed` status write-back so the `monitor_events` row and `agent_runs` row reflect the terminal state, and SHALL continue with the next claim.

### Requirement 8: Mission Control read

**User Story:** As an operator, I want to read monitoring events for my org, so
that I can observe the monitoring queue in Mission Control.

#### Acceptance Criteria

1. WHEN `GET /api/monitor/events` is requested, THE Control_Plane SHALL return Monitor_Events scoped to the requesting principal's org, ordered newest-first.
2. WHEN `GET /api/monitor/events` is requested without a limit, THE Control_Plane SHALL apply a configured default page size, and SHALL cap any caller-supplied limit at a configured maximum.

### Requirement 9: Read-only auto-triggered runs

**User Story:** As a security owner, I want auto-triggered runs to be strictly
read-only, so that monitoring never performs active testing on its own.

#### Acceptance Criteria

1. WHEN a run is launched from a claimed Monitor_Event, THE Control_Plane SHALL create the run with no authorization scope (`authz_scope = NULL`) and `active_testing = false`.
2. IF a launch from a claimed Monitor_Event attempts `active_testing = true` or a non-NULL authorization scope, THEN THE Control_Plane SHALL reject the launch and create no run.
3. WHEN an auto-triggered run produces a finding, THE Control_Plane SHALL record the finding with status `Unvalidated`.
4. WHILE processing auto-triggered runs, THE Control_Plane SHALL leave the `ActiveTestingGate` untouched as the only path to active testing.

## Phase 2 — Run Trace / Timeline

### Requirement 10: Agent and handoff trace tables

**User Story:** As an operator, I want each agent instance and handoff recorded
per run, so that I can reconstruct what the run did.

#### Acceptance Criteria

1. WHEN an agent Leg is emitted for a run, THE Control_Plane SHALL record one row in the `run_agents` table keyed by `(run_id, agent_id)` and SHALL record one row in the `run_handoffs` table for each emitted handoff.
2. WHEN an agent Leg is emitted for a `(run_id, agent_id)` that already exists, THE Monitor_Store SHALL update the existing Leg in place, overwriting its outcome and end time, and SHALL NOT insert a second row for that same `(run_id, agent_id)`.
3. WHEN a handoff is recorded, THE Control_Plane SHALL store its source agent id, destination agent id, and the handoff timestamp (`at`).

### Requirement 11: Fixed stage vocabulary and name-to-stage mapping

**User Story:** As an operator, I want a stable stage vocabulary with every
agent mapped to a stage, so that the stage-flow view is consistent and no agent
is lost.

#### Acceptance Criteria

1. THE Control_Plane SHALL define the fixed stage vocabulary as exactly the 11 ordered stages RECON, POSTURE, DISCOVERY, EXPLOIT, SWEEP, VERIFY, STABILITY, SCORE, FORMAT, COVERAGE, and REPORT, in that order, plus the `other` bucket.
2. WHEN mapping an agent name to a Stage, THE Control_Plane SHALL compare the agent name against the known stage-name keys using a case-insensitive substring match, independent of leading or trailing whitespace.
3. IF an agent name matches more than one known stage-name key, THEN THE Control_Plane SHALL select the Stage of the earliest-defined matching key in the fixed stage order, so that mapping is deterministic.
4. IF an agent name matches no known stage-name key, THEN THE Control_Plane SHALL map the agent to the `other` Stage and SHALL NOT drop the Leg.
5. WHEN rendering the stage-flow view, THE Control_Plane SHALL render every one of the 11 pipeline Stages, reporting a Leg count equal to the number of Legs mapped to that Stage, and SHALL report a Leg count of 0 for any Stage with no mapped Legs.

### Requirement 12: Trace write-back

**User Story:** As a worker, I want to report the run trace back best-effort, so
that trace detail is captured without ever failing the run it describes.

#### Acceptance Criteria

1. WHEN a trace write-back is received at `POST /api/agent/runs/:id/trace`, THE Control_Plane SHALL map each supplied agent name to a Stage, upsert the Legs and handoffs, and set `agents_count` to the number of distinct `(run_id, agent_id)` Legs and `handoffs_count` to the number of recorded handoffs on the `agent_runs` row.
2. WHEN emitting trace from the agents plane, THE Worker_Loop SHALL perform the trace write-back as a best-effort operation that raises no exception to the run it describes and changes neither the run's status nor its terminal outcome, regardless of whether the write-back succeeds or fails.
3. IF a trace write-back references a `run_id` that does not exist for the requesting principal's org, THEN THE Control_Plane SHALL return `404 Not Found` with no existence disclosure and SHALL persist no Legs or handoffs.

### Requirement 13: Run detail and timeline reads

**User Story:** As an operator, I want a run detail header, stage-flow, and
timeline, so that I can inspect a run's execution.

#### Acceptance Criteria

1. WHEN `GET /api/agent/runs/:id/detail` is requested, THE Control_Plane SHALL return a header containing the run id, status, target with its scope-rule count, `triggered_by`, `started_at`, `ended_at`, duration, `agents_count`, and `handoffs_count`.
2. WHEN a run detail header is returned and both `started_at` and `ended_at` are present, THE Control_Plane SHALL report duration as `ended_at` minus `started_at`; IF `ended_at` is absent, THEN THE Control_Plane SHALL report duration as null.
3. WHEN `GET /api/agent/runs/:id/detail` is requested, THE Control_Plane SHALL return a stage-flow containing, for each of the 11 pipeline Stages, that Stage's Leg count and, for each Leg in the Stage, the agent name, the Leg duration, and the Leg outcome.
4. WHEN `GET /api/agent/runs/:id/timeline` is requested, THE Control_Plane SHALL return the run's agent-start events and handoff events as a single list ordered ascending by event timestamp, with ties broken deterministically by agent id.
5. IF `GET /api/agent/runs/:id/detail` or `GET /api/agent/runs/:id/timeline` references a run that does not belong to the requesting principal's org, THEN THE Control_Plane SHALL return `404 Not Found` with no existence disclosure.

## Phase 3 — Finding / Remediation Detail

### Requirement 14: Finding report storage

**User Story:** As an operator, I want the agent's actual generated report
carried through and stored, so that the finding detail reflects what the agent
produced.

#### Acceptance Criteria

1. WHEN a report is received for a `(finding_id, team)` that does not yet exist, THE Control_Plane SHALL insert one row in the `finding_reports` table with structured columns, a JSONB `sections` field for long-form content, and the agents plane's rendered markdown artifact.
2. WHEN a report is re-reported for an existing `(finding_id, team)`, THE Monitor_Store SHALL update the existing report row in place, replacing its fields, and SHALL NOT insert a second row for that same `(finding_id, team)`.

### Requirement 15: Report write-back and team classification

**User Story:** As a worker, I want to send the generated report artifact with a
team classification, so that reports are attributed to the correct team.

#### Acceptance Criteria

1. WHEN a report write-back is received at `POST /api/agent/findings/reports`, THE Control_Plane SHALL store the actual generated report artifact, including the rendered markdown, provided by the agents plane.
2. WHERE the agents plane provides an explicit Team of `red` or `blue`, THE Control_Plane SHALL store that Team on the report.
3. IF the agents plane provides no explicit Team, THEN THE Control_Plane SHALL derive the Team from the reporting agent's Stage, mapping EXPLOIT to `red` and VERIFY, STABILITY, or REPORT to `blue`.
4. IF a report write-back references a `finding_id` that does not exist for the requesting principal's org, THEN THE Control_Plane SHALL return `404 Not Found` with no existence disclosure.

### Requirement 16: Proof-of-concept redaction

**User Story:** As a security owner, I want the weaponized proof-of-concept body
never transported, stored, or served, so that exploit code cannot leak through
the monitoring seams.

#### Acceptance Criteria

1. WHEN building a report write-back payload, THE Worker_Loop SHALL exclude the `poc_script_code` body at any nesting level and include only a `poc_available` flag, a proof-of-concept description, and code locations or fix diffs.
2. IF a report payload contains a `poc_script_code` key at any nesting level, THEN THE Monitor_Store SHALL drop that key so the weaponized body is never persisted, while continuing to persist the remaining fields.
3. THE Control_Plane SHALL exclude the `poc_script_code` body from every stored row, JSONB `sections` field, API response, and write-back payload in every serialized form, serving only the `poc_available` flag and proof-of-concept description.

### Requirement 17: Extended finding read with graceful degradation

**User Story:** As an operator, I want the finding detail endpoint to include the
report and run linkage, and still render when no report exists, so that I always
get a usable finding view.

#### Acceptance Criteria

1. WHEN `GET /api/findings/:id` is requested for a finding with a report, THE Control_Plane SHALL return `200` and include the finding's `finding_reports` document and the run linkage with `triggered_by` in the response.
2. IF a finding has no `finding_reports` row, THEN THE Control_Plane SHALL return `200` with `reports` as an empty list and the finding header, intel, and evidence only.
3. IF `GET /api/findings/:id` references a finding that does not belong to the requesting principal's org, THEN THE Control_Plane SHALL return `404 Not Found` with no existence disclosure.

### Requirement 18: Finding Detail page

**User Story:** As an operator, I want a Finding Detail page showing red and blue
team detail without ever exposing the exploit body, so that I can review
findings safely.

#### Acceptance Criteria

1. WHEN rendering a finding whose reports list is non-empty, THE Finding_Detail_Page SHALL render the Red Team section, the Blue Team and remediation section, the Unvalidated status, the cited intel, and the update history.
2. IF a finding's reports list is empty, THEN THE Finding_Detail_Page SHALL render the finding header, intel, and evidence only, omitting the red/blue report sections.
3. WHERE `poc_available` is boolean true, THE Finding_Detail_Page SHALL indicate proof-of-concept presence using the `poc_available` flag only and SHALL never place the weaponized `poc_script_code` body in the DOM, page source, or client-side state.

## Cross-Cutting Requirements

### Requirement 19: Tenant isolation

**User Story:** As a security owner, I want every new seam scoped to the
authenticated org, so that one tenant can never read or affect another's data.

#### Acceptance Criteria

1. WHEN resolving the org for any ingest, claim, write-back, or read operation, THE Control_Plane SHALL resolve the org from the authenticated principal or service account, and SHALL ignore any org supplied in the request body, query, or path.
2. IF a run, event, or report belongs to a different org than the requesting principal, THEN THE Control_Plane SHALL return `404 Not Found`, returning a response identical to that for a nonexistent resource so existence is never disclosed.

### Requirement 20: Permissions and service-account binding

**User Story:** As a security owner, I want each seam gated by the correct
permission and the service account bound to a single org, so that access is
least-privilege.

#### Acceptance Criteria

1. THE Control_Plane SHALL require the new `monitor:ingest` permission for `POST /api/monitor/events`.
2. THE Control_Plane SHALL require the `agent:run` permission for the claim, status write-back, and trace write-back seams.
3. THE Control_Plane SHALL require the `findings:read` permission for the Mission Control events read and the finding detail read.
4. IF a request lacks the required permission, THEN THE Control_Plane SHALL deny it with no side effect on any row.
5. THE Control_Plane SHALL bind the service-account role to exactly one org.

### Requirement 21: Error handling

**User Story:** As a platform operator, I want predictable, non-leaking error
behavior, so that failures fail closed without exposing backend detail or
sinking runs.

#### Acceptance Criteria

1. IF an ingest body cannot be parsed or classified, THEN THE Control_Plane SHALL return `400 Bad Request` with a JSON content type and persist no row.
2. IF an unexpected store failure occurs, THEN THE Control_Plane SHALL return a generic `503 temporarily unavailable` JSON response, logging the detail server-side and leaking no stack trace or internal identifier to the caller.
3. WHEN a status, trace, or report write-back fails, THE Control_Plane SHALL treat the write-back as best-effort and SHALL NOT fail the run it describes.
4. WHEN one item in a batch write-back is rejected, THE Control_Plane SHALL continue processing the remaining items in the batch.

## Correctness Property Mapping

The design's eight Correctness Properties map to the requirement acceptance
criteria they validate. When the design properties carry a traceability
reference, they SHALL reference the clauses below.

- **P1 — Tenant isolation:** Validates Requirements 8.1, 19.1, 19.2, 20.5.
- **P2 — Ingest idempotency:** Validates Requirements 2.1, 2.2.
- **P3 — Vulnerability delta:** Validates Requirements 4.5, 4.6, 5.2, 5.3.
- **P4 — Claim exclusivity (SKIP LOCKED):** Validates Requirements 6.1, 6.2.
- **P5 — Read-only auto-runs:** Validates Requirements 9.1, 9.2, 9.3.
- **P6 — PoC redaction:** Validates Requirements 16.1, 16.2, 16.3, 18.3.
- **P7 — Graceful degradation:** Validates Requirements 17.2, 18.2.
- **P8 — Stage-flow leg counts:** Validates Requirements 11.4, 11.5, 12.1.
