# Implementation Plan: continuous-monitoring-agents

## Overview

This plan delivers continuous, event-triggered monitoring across the three
design phases, built incrementally and safe-before-active. The Go control plane
is established first (migrations and store, then handlers, then routes), the
event taps and sweep feed the durable queue, the claim API and Python worker
loop close the trigger→run→status loop, Phase 2 adds the run trace/timeline, and
Phase 3 adds the finding report write-back and the committed Finding Detail
page.

Code is written in the project's real languages: Go (control plane), Python
(agents plane), TypeScript/React (frontend). Each task builds on the prior ones
and wires into the system with no orphaned code. Property-based test sub-tasks
sit next to the code they validate and are marked optional with `*`; each cites
its design Property number and the requirement clause it checks.

## Tasks

- [x] 1. Phase 1 schema: durable-first `monitor_events` queue + `agent_runs` additions
  - Create `internal/store/migrations/00020_monitor_events.sql` (next migration number after 00017) with the `monitor_events` table: `org_id` FK, `source`/`event_kind`/`state`/`team` CHECK constraints, `payload` JSONB, nullable `run_id` FK to `agent_runs`, timestamps
  - Add the `monitor_events_delivery_uniq` UNIQUE index on `(org_id, source, delivery_id)` (idempotency key) and the partial `monitor_events_claimable_idx` on `(org_id, created_at) WHERE state = 'routed'`
  - In the same migration family, ALTER `agent_runs` to add `agents_count`, `handoffs_count`, `triggered_by` (default `'manual'`), `started_at`, `ended_at`
  - Provide the `+goose Down` that drops the table and reverses the ALTERs
  - _Requirements: 1.2, 1.3, 2.1, 9.1_

- [x] 2. Phase 1 monitor store (`internal/store/monitor_events.go`)
  - [x] 2.1 Define store types and the idempotent durable-first insert
    - Add `ErrDuplicateMonitorEvent` sentinel (mirroring `connector.ErrDuplicateDelivery`), `MonitorEventInput` and `MonitorEvent` structs
    - Implement `InsertMonitorEvent(ctx, org, in)` inserting a `pending` row with org from the caller (never payload); map pg `23505` unique_violation to `ErrDuplicateMonitorEvent`, reject empty/missing `DeliveryID` with no row
    - Implement `SetMonitorEventRoute(ctx, org, id, team)` advancing `pending → routed` (or `skipped` when team is empty)
    - _Requirements: 1.1, 1.4, 2.1, 2.2, 2.4, 3.6, 19.1_
  - [x]* 2.2 Write property test for ingest idempotency
    - **Property 2: Ingest idempotency**
    - **Validates: Requirements 2.1, 2.2** — inserting the same `(org, source, delivery_id)` twice yields exactly one row and `ErrDuplicateMonitorEvent` on the second attempt
  - [x] 2.3 Implement the SKIP LOCKED claim and run/status/list operations
    - Implement `ClaimPendingMonitorEvents(ctx, org, limit)` as the single lock-and-advance CTE using `SELECT ... FOR UPDATE SKIP LOCKED`, ordered oldest-first, org-scoped, moving rows to `claimed` and returning them
    - Implement `UpdateMonitorEventRun(ctx, org, id, runID)` and `UpdateMonitorEventStatus(ctx, org, runID, status)` to stamp the run and fold status transitions onto the row
    - Implement `ListMonitorEvents(ctx, org, limit)` org-scoped, newest-first
    - _Requirements: 6.1, 6.2, 6.3, 7.2, 8.1, 19.1_
  - [x]* 2.4 Write property test for claim exclusivity
    - **Property 4: Claim exclusivity (SKIP LOCKED)**
    - **Validates: Requirements 6.1, 6.2** — N concurrent `ClaimPendingMonitorEvents` calls over the same routed rows return disjoint sets; no row goes to two workers
  - [x]* 2.5 Write property test for tenant isolation on the store
    - **Property 1: Tenant isolation**
    - **Validates: Requirements 8.1, 19.1, 19.2, 20.5** — inserts/claims/list scoped to org A are never visible to org B; cross-org reads find nothing

- [x] 3. Phase 1 classify + static route (`internal/monitor/router.go`)
  - Define the `Team` type and constants (`red_team`, `blue_team`, `pr_reviewer`, `triage`) and the static `routingTable`
  - Implement `Route(eventKind)` returning the team or `""` (unroutable → skipped)
  - Implement `Classify(source, payload)` as the only reader of source-specific payload shapes, deriving `event_kind` as one of `ci`/`vulnerability`/`code_change`/`resource` or none
  - _Requirements: 3.1, 3.2, 3.3, 3.4, 3.5, 3.6_

- [x] 4. Phase 1 ingest + Mission Control handlers (`backend/server/monitor_handlers.go`)
  - Implement `handleMonitorIngest` for `POST /api/monitor/events`: resolve org from principal via `mcpOrg`, decode/`Classify` the body (400 on unparseable, no row), `InsertMonitorEvent`, then route via `SetMonitorEventRoute`; treat `ErrDuplicateMonitorEvent` as an idempotent success no-op; map unexpected store errors to generic `503`
  - Implement `handleMonitorEventsList` for `GET /api/monitor/events`: org-scoped newest-first via `ListMonitorEvents`, default page size, cap caller limit at the configured maximum
  - _Requirements: 1.1, 2.1, 2.2, 8.1, 8.2, 19.1, 19.2, 21.1, 21.2_

- [x] 5. Phase 1 new permission + route wiring (`internal/auth/model.go`, `backend/server/monitor_routes.go`)
  - Add `PermMonitorIngest = "monitor:ingest"` in `internal/auth/model.go`
  - Create `registerMonitorRoutes` (mirroring `registerAgentRoutes` group-by-permission): `monitor:ingest` for `POST /monitor/events`, `findings:read` for `GET /monitor/events`, `agent:run` for `POST /agent/monitor/claim` and `PUT /agent/runs/:id/status`; call it from server setup
  - Confirm the service-account role binds to exactly one org
  - _Requirements: 20.1, 20.2, 20.3, 20.4, 20.5_

- [x] 6. Phase 1 claim handler + read-only launch (`backend/server/monitor_handlers.go`)
  - Implement `handleMonitorClaim` for `POST /api/agent/monitor/claim`: resolve org from principal (ignore body), `ClaimPendingMonitorEvents`, and for each claimed row launch a run via `CreateAgentRun` with `Scope == nil` (so `authz_scope = NULL`, `active_testing = false`), stamp `triggered_by` to the event id, `UpdateMonitorEventRun`, and return the claims with `run_id`
  - Reject any launch attempting `active_testing = true` or a non-NULL scope, creating no run; leave `ActiveTestingGate` untouched
  - _Requirements: 6.3, 9.1, 9.2, 9.4, 19.1_
  - [x]* 6.1 Write property test for read-only auto-runs
    - **Property 5: Read-only auto-runs**
    - **Validates: Requirements 9.1, 9.2, 9.3** — every claim-launched run has `authz_scope == NULL` and `active_testing == false`; a scoped/active launch is rejected with no run

- [x] 7. Phase 1 status write-back (`backend/server/agent_runs_handlers.go`)
  - Implement `handleAgentRunStatusWrite` for `PUT /api/agent/runs/:id/status`: validate status in `{running, completed, failed}` (else `400`, change no row), fold status + findings count onto the org-scoped `agent_runs` row, set `started_at`/`ended_at`, and fold the transition onto the linked `monitor_events` row via `UpdateMonitorEventStatus`
  - Return `404` with no existence disclosure for a run in another org
  - Record auto-triggered findings with status `Unvalidated`
  - _Requirements: 7.1, 7.2, 7.3, 9.3, 19.2_

- [x] 8. Phase 1 event taps (`internal/connector/github_webhook.go`, discovery completion, finding delta)
  - GitHub tap: after the existing `InsertWebhookDelivery` dedup in `Manager.HandleGitHubWebhook`, also `InsertMonitorEvent` with `source="github"` reusing the `X-GitHub-Delivery` value as `delivery_id` — `event_kind = ci` for check-run/workflow, `code_change` for push/PR; persist nothing when the header is absent
  - Resource tap: at discovery completion, emit one `resource` event per newly discovered asset with `delivery_id = discovery_session_id + ":" + asset_id`
  - Vulnerability delta tap: in the `finding.UpsertFinding` path, fire one `vulnerability` event with `delivery_id = asset_id + ":" + vuln_id` only when `UpsertResult.Created == true`; emit nothing when `false`
  - _Requirements: 2.5, 2.6, 4.1, 4.2, 4.3, 4.4, 4.5, 4.6_
  - [x]* 8.1 Write property test for the vulnerability delta
    - **Property 3: Vulnerability delta**
    - **Validates: Requirements 4.5, 4.6, 5.2, 5.3** — a nothing→open transition emits exactly one event; a re-upsert with `Created == false` emits zero

- [x] 9. Phase 1 scheduled sweep (`internal/monitor/sweep.go`)
  - Implement `Sweeper.RunSweep(ctx, org)`: re-run `correlator.Correlate` per org and `UpsertFinding` each match, relying on the delta tap so unchanged still-open findings emit no events (no re-trigger storm)
  - Wire the sweep to run per org on the scheduler
  - _Requirements: 5.1, 5.2, 5.3_

- [x] 10. Phase 1 Python worker loop (`agents-plane/strix/core/bellu_runner.py`)
  - Replace the `poll_launched_runs()` stub with `run_worker_loop(...)`: claim via `POST /api/agent/monitor/claim`, build a `RunDescriptor` with `authz_scope=None` (read-only), `await run_bellu_scan`, and wire `report_status` to `PUT /api/agent/runs/:id/status`
  - On an empty claim, sleep with exponential backoff (`idle_backoff_s` doubling to `max_backoff_s`); reset backoff to initial on the next non-empty claim
  - On a run error, report a `failed` status write-back and continue with the next claim
  - _Requirements: 6.4, 6.5, 6.6, 7.4, 9.1_

- [x] 11. Checkpoint — Phase 1 read-only loop end to end
  - Ensure all tests pass. Verify the loop runs end to end: ingest → route → claim via SKIP LOCKED → read-only run launch → status write-back folds onto both `agent_runs` and `monitor_events` → Mission Control `GET /api/monitor/events` reflects it. Ask the user if questions arise.

- [x] 12. Phase 2 trace schema (`internal/store/migrations/00021_run_trace.sql`)
  - Create `run_agents` (keyed `(run_id, agent_id)` via unique index, `stage` CHECK over the 11 stages + `other`, `agent_name`, `parent_id`, `outcome`, start/end) and `run_handoffs` (`from_agent_id`, `to_agent_id`, `at`) with their indexes and `+goose Down`
  - _Requirements: 10.1, 10.2, 10.3_

- [x] 13. Phase 2 fixed stage vocabulary + mapper (`internal/monitor/stages.go`)
  - Define the `Stage` type and 11 ordered stage constants plus `StageOther`, the ordered `Pipeline` slice, and the `nameToStage` keys
  - Implement `MapStage(agentName)` with case-insensitive, whitespace-trimmed substring match returning the earliest-defined matching stage in pipeline order, falling to `StageOther` (never dropping a leg)
  - _Requirements: 11.1, 11.2, 11.3, 11.4_
  - [x]* 13.1 Write property test for stage-flow leg counts
    - **Property 8: Stage-flow leg counts**
    - **Validates: Requirements 11.4, 11.5, 12.1** — each stage's leg count equals the agents mapping to it; unmapped names land in `other`; sum of leg counts equals the number of emitted agents

- [x] 14. Phase 2 trace store operations (`internal/store/monitor_events.go` / trace store)
  - Implement `UpsertRunAgent(ctx, org, a)` idempotent per `(run_id, agent_id)` (refresh outcome + end time), `InsertRunHandoff(ctx, org, h)`, and `RunTrace(ctx, org, runID)` returning `RunTraceView`/`StageLegs` (one `StageLegs` per `Pipeline` stage, in order, `LegCount` may be 0), org-scoped so cross-org is not-found
  - _Requirements: 10.1, 10.2, 10.3, 11.5, 19.2_

- [x] 15. Phase 2 trace write-back + detail/timeline reads (`backend/server/agent_runs_handlers.go`, `backend/server/monitor_routes.go`)
  - Implement `handleAgentRunTrace` for `POST /api/agent/runs/:id/trace`: map each agent name to a stage, upsert legs/handoffs, fold `agents_count` (distinct `(run_id, agent_id)`) and `handoffs_count` onto the run; `404` (no disclosure, no legs persisted) for a run in another org; continue past a rejected batch item
  - Implement `handleAgentRunDetail` for `GET /api/agent/runs/:id/detail`: header (run id, status, target + scope-rule count, `triggered_by`, start/end, duration = end − start or null when end absent, counts) plus the per-stage stage-flow with per-leg name/duration/outcome
  - Implement `handleAgentRunTimeline` for `GET /api/agent/runs/:id/timeline`: agent-start + handoff events in one list ascending by timestamp, ties broken by agent id; `404` with no disclosure for cross-org
  - Register `trace` under `agent:run` and `detail`/`timeline` under `findings:read` in `registerMonitorRoutes`
  - _Requirements: 12.1, 12.3, 13.1, 13.2, 13.3, 13.4, 13.5, 19.2, 20.2, 20.3, 21.4_
  - [x]* 15.1 Write property test for trace tenant isolation + leg folding
    - **Property 1: Tenant isolation** and **Property 8: Stage-flow leg counts**
    - **Validates: Requirements 12.1, 13.5, 19.2** — a trace write-back / read for a run in another org returns `404` and persists nothing; `agents_count` equals distinct emitted legs

- [x] 16. Phase 2 Python trace emitter (`agents-plane/strix/core/bellu_runner.py`)
  - Add the `TracePayload` shape and `emit_trace(run_id, settings, payload, ...)` posting to `POST /api/agent/runs/{id}/trace`, built from coordinator register/set_status + handoff telemetry in `run.json`; make it best-effort and never-raising so a trace post cannot fail the run
  - Call `emit_trace` from the worker loop after a run so trace flows back
  - _Requirements: 12.1, 12.2_

- [x] 17. Checkpoint — Phase 2 run detail + timeline reflect emitted trace
  - Ensure all tests pass. Verify a run's emitted trace renders: `GET /api/agent/runs/:id/detail` shows the header + 11-stage stage-flow with correct leg counts, and `GET /api/agent/runs/:id/timeline` shows ordered agent/handoff events. Ask the user if questions arise.

- [x] 18. Phase 3 finding_reports schema (`internal/store/migrations/00022_finding_reports.sql`)
  - Create `finding_reports` with `finding_id` FK, nullable `run_id` FK, `team` CHECK (`red|blue`), `title`, `severity`, `poc_available` BOOLEAN (flag only — no `poc_script_code` column), `sections` JSONB, `rendered_markdown` TEXT, the `(finding_id, team)` unique index, the `run_id` index, and `+goose Down`
  - _Requirements: 14.1, 16.3_

- [x] 19. Phase 3 finding report store (`internal/store/monitor_events.go` / finding report store)
  - Define the `FindingReport` struct and implement `UpsertFindingReport(ctx, org, r)` idempotent per `(finding_id, team)` (update in place, no second row), defensively dropping any `poc_script_code` key at any nesting level in `sections` while persisting the rest; and `GetFindingReport(ctx, org, findingID)` org-scoped
  - _Requirements: 14.1, 14.2, 16.2, 16.3, 19.1_
  - [x]* 19.1 Write property test for PoC redaction at the store
    - **Property 6: PoC redaction**
    - **Validates: Requirements 16.1, 16.2, 16.3, 18.3** — for any payload containing `poc_script_code` at any nesting level, the stored row/sections contain only `poc_available`/description and never the body

- [x] 20. Phase 3 report write-back + extended finding read (`backend/server/agent_findings_handlers.go`, `backend/server/findings_handlers.go`, `backend/server/monitor_routes.go`)
  - Implement `handleAgentFindingReportIngest` for `POST /api/agent/findings/reports` under `findings:ingest`: store the artifact + rendered markdown, honor an explicit `team` of `red`/`blue` else derive from the reporting agent's stage (EXPLOIT→`red`; VERIFY/STABILITY/REPORT→`blue`); `404` with no disclosure for a `finding_id` in another org
  - Extend `handleFinding` (`GET /api/findings/:id`) to also load `GetFindingReport` + run linkage and include `reports` and `triggered_by`; degrade gracefully to `reports: []` on a store error or no report; `404` with no disclosure for cross-org
  - Register the report route under `findings:ingest`
  - _Requirements: 14.1, 14.2, 15.1, 15.2, 15.3, 15.4, 17.1, 17.2, 17.3, 19.2, 20.3_
  - [x]* 20.1 Write property test for graceful degradation
    - **Property 7: Graceful degradation**
    - **Validates: Requirements 17.2, 18.2** — for a finding with an empty `finding_reports` set, `GET /api/findings/:id` returns `200` with `reports: []` and the header/intel/evidence

- [x] 21. Phase 3 Python report sender (`agents-plane/strix/tools/finding_report.py`)
  - Extend `BelluReporter` with `REPORT_WRITE_BACK` and `build_report_payload(report, run_id)`: strip `poc_script_code` from `sections` (set `poc_available` flag only), classify `team` explicitly or from stage, and include `render_vulnerability_md(report)` as the rendered artifact; send to `POST /api/agent/findings/reports`
  - Wire the sender into the worker-loop report path
  - _Requirements: 15.1, 15.2, 15.3, 16.1_
  - [x]* 21.1 Write property test for the plane-side PoC strip
    - **Property 6: PoC redaction**
    - **Validates: Requirements 16.1, 18.3** — `build_report_payload` never includes `poc_script_code` at any nesting level; only `poc_available` + description + locations/diffs are present

- [x] 22. Phase 3 frontend API client types (`frontend/src/lib/api.ts`, `frontend/src/types/foundation.ts`)
  - Add `FindingReportView` (`team`, `title`, `severity`, `poc_available` flag only, `sections`, `rendered_markdown`) and extend `FindingDetailResponse` with `reports: FindingReportView[]` and optional `triggered_by`; keep `realApi.finding(id)` returning the extended shape
  - _Requirements: 17.1, 18.3_

- [x] 23. Phase 3 Finding Detail page (`frontend/src/features/issues/FindingDetail.tsx`, `frontend/src/mocks`)
  - Implement `FindingDetail({ id })`: load via `realApi.finding(id)`, render the Red Team section (red report), the Blue Team + remediation section (blue report), the `Unvalidated` status pill, cited intel, and update history; when `reports` is empty render only header + intel + evidence (fallback)
  - Indicate PoC presence from the `poc_available` flag only — never place a weaponized body in the DOM/state (none is in the response)
  - Add `src/mocks` fixtures for red-only / blue-only / both / no-report shapes
  - _Requirements: 18.1, 18.2, 18.3_
  - [x]* 23.1 Write vitest tests for the Finding Detail page
    - **Property 6: PoC redaction** and **Property 7: Graceful degradation**
    - **Validates: Requirements 18.1, 18.2, 18.3** — render red-only/blue-only/both/no-report shapes assert the right sections; across all shapes assert the weaponized PoC body string never appears in the DOM and only the `poc_available` indicator may render

- [x] 24. Final checkpoint — end-to-end integration + safety review
  - [x] 24.1 Write the end-to-end integration test
    - Exercise the full seam: ingest an event → router sets `routed` → worker claims via `FOR UPDATE SKIP LOCKED` → read-only run launches → status/trace/report write-backs land → Mission Control grid, run detail/timeline, and the extended `GET /api/findings/:id` reflect the run, trace legs, and report (and the graceful fallback when no report exists)
    - _Requirements: 7.1, 7.2, 8.1, 12.1, 13.1, 17.1, 17.2_
  - [x]* 24.2 Safety-review assertions across the integrated system
    - **Property 1: Tenant isolation** and **Property 6: PoC redaction**
    - **Validates: Requirements 16.3, 18.3, 19.1, 19.2** — assert P1 holds (a cross-org read of any run/event/report returns `404` with no existence disclosure) and P6 holds (no `poc_script_code` body appears in any stored row, API response, write-back payload, or the DOM anywhere)
  - Ensure all tests pass, ask the user if questions arise.

## Notes

- Tasks marked with `*` are optional test sub-tasks and can be skipped for a
  faster MVP; core implementation tasks are never optional.
- Each task references specific requirement clauses for traceability, and each
  property test sub-task cites its design Property number and the clauses it
  checks (P1 8.1/19.1/19.2/20.5, P2 2.1/2.2, P3 4.5/4.6/5.2/5.3, P4 6.1/6.2,
  P5 9.1/9.2/9.3, P6 16.1/16.2/16.3/18.3, P7 17.2/18.2, P8 11.4/11.5/12.1).
- Work is ordered safe-before-active: the durable queue, store, handlers, and
  routes land before taps/worker, and reads land before the frontend consumes
  them. New code follows existing patterns (mirror `agent_runs.go`,
  `registerAgentRoutes`, and `github_webhook_store.go` dedup; org from principal
  via `mcpOrg`; migrations 00020/00021/00022).
- Checkpoints validate each phase incrementally; the final checkpoint asserts
  the two cross-cutting safety invariants on the fully integrated system.

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1"] },
    { "id": 1, "tasks": ["2.1", "3"] },
    { "id": 2, "tasks": ["2.2", "2.3"] },
    { "id": 3, "tasks": ["2.4", "2.5", "4", "5"] },
    { "id": 4, "tasks": ["6", "7"] },
    { "id": 5, "tasks": ["6.1", "8", "9"] },
    { "id": 6, "tasks": ["8.1", "10"] },
    { "id": 7, "tasks": ["12"] },
    { "id": 8, "tasks": ["13", "14"] },
    { "id": 9, "tasks": ["13.1", "15"] },
    { "id": 10, "tasks": ["15.1", "16"] },
    { "id": 11, "tasks": ["18"] },
    { "id": 12, "tasks": ["19"] },
    { "id": 13, "tasks": ["19.1", "20"] },
    { "id": 14, "tasks": ["20.1", "21", "22"] },
    { "id": 15, "tasks": ["21.1", "23"] },
    { "id": 16, "tasks": ["23.1", "24.1"] },
    { "id": 17, "tasks": ["24.2"] }
  ]
}
```
