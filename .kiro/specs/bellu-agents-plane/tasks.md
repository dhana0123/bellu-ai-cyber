# Implementation Plan: Bellu Agents Plane

## Overview

This plan builds the agents plane as an incremental, test-driven set of increments. Each phase
is a working, demoable slice that builds on the prior one. The order is deliberately
safe-before-active: the Go control-plane seams come first (the stable contract everything else
depends on), then the forked Strix engine, then the Bellu tools, then the read-only
graph-reasoning vertical slice end-to-end, and finally the authorization-gated sandbox active
testing path — the most safety-critical surface is built last and reviewed last.

Both languages are fixed by the design: the control plane is **Go** (`internal/*`,
`backend/server`), the agents plane is **Python** (new top-level `agents-plane/`). No
implementation-language selection is needed.

The design defines six correctness properties. Property-style test sub-tasks are placed next to
the code they validate, each annotated with its Property number and the requirements clause it
checks. Test sub-tasks are marked optional with `*`.

## Tasks

### Phase A — Bellu control-plane Go seams

- [x] 1. Auth additions: permissions and service-account role
  - [x] 1.1 Add `PermAgentRun` (`agent:run`) and `PermFindingsIngest` (`findings:ingest`) to the permission catalog in `internal/auth/model.go`; define built-in role `RoleAgentServiceAccount` (`agent_service_account`, bound to exactly one org) granting only `graph:read`, `findings:read`, `agent:run`, `findings:ingest`; wire it into catalog/role seeding
    - _Requirements: 9.1, 9.4_
  - [ ]* 1.2 Write unit tests for the auth additions
    - Permission catalog seeds both new permissions
    - Service-account principal resolves with exactly the four granted permissions and no more (cannot escalate past its single org)
    - _Requirements: 9.1, 9.4, 9.5_

- [x] 2. Graph-context seam handler
  - [x] 2.1 Implement `GET /api/agent/graph-context` in `backend/server`: add `GraphContext`, `TargetRef`, `GraphNode`, `GraphEdge` types; `parseTargetRef` (entity vs raw, 400 on unparseable); `buildScopedSubgraph` scoped to org via `s.graph`, bounded by `maxContextNodes=200` and `maxContextDepth=2`, setting `truncated` when caps clip; resolve org from principal via the `mcpOrg` pattern; raw targets return empty-but-valid bounded context; `s.graph == nil` returns 200 with `graph:false` + empty neighbors; graph read failure returns 503 generic
    - _Requirements: 1.6, 3.1, 3.2, 3.3, 3.4, 4.1, 4.2, 4.3, 4.4, 4.5, 4.6, 4.7, 4.8_
  - [ ]* 2.2 Write property test for graceful degradation
    - **Property 5: Graceful degradation when Memgraph is nil**
    - **Validates: Requirements 4.7**
  - [ ]* 2.3 Write property test for bounded payload
    - **Property 6: Graph-context payload bounded** — generate graphs of varying size, assert neighbor count never exceeds `maxContextNodes` and depth never exceeds `maxContextDepth`, `truncated` set when clipped
    - **Validates: Requirements 4.3, 4.4**
  - [ ]* 2.4 Write unit tests for target parsing and org resolution
    - `parseTargetRef` 400 on invalid target; raw target returns empty neighbors/edges; org resolved from principal and never from query
    - _Requirements: 3.4, 4.1, 4.6_

- [x] 3. Findings ingest seam handler
  - [x] 3.1 Implement `POST /api/agent/findings:ingest` in `backend/server`: add `IngestRequest`/`IngestFinding` types; `mapIngestFinding` enforcing `model.ValidKind` allow-list on linked assets, synthetic org-scoped asset ref for raw targets (`syntheticAssetID`), `risk_score` clamp to `[0,100]`, reject on empty `vuln_id` and on empty `asset_id`+`raw_target`; add `store.UpsertFinding` keyed on `(asset_id, vuln_id)` per org; resolve org from principal (ignore any body org); return `{created, updated, rejected}` with per-finding rejection continuing the batch
    - _Requirements: 6.1, 6.2, 6.3, 6.4, 6.5, 6.6, 6.7, 6.8, 6.9, 6.10_
  - [ ]* 3.2 Write property test for ingest idempotency
    - **Property 2: findings:ingest idempotency** — N ingests of the same `(asset_id, vuln_id)` yield exactly one row for the org, updating rather than duplicating
    - **Validates: Requirements 6.8**
  - [ ]* 3.3 Write property test for allow-list validation
    - **Property 3: Allow-list validation on ingested asset kinds** — a linked finding whose kind fails `model.ValidKind` is rejected and never persisted
    - **Validates: Requirements 6.3, 6.4**
  - [ ]* 3.4 Write unit tests for finding mapping edge cases
    - Raw-target synthetic asset uniqueness across orgs; empty `vuln_id` rejected; empty `asset_id`+`raw_target` rejected; `risk_score` clamp
    - _Requirements: 6.5, 6.6, 6.7, 6.9_

- [x] 4. Runs seam handlers
  - [x] 4.1 Implement `POST /api/agent/runs` and `GET /api/agent/runs/:id` in `backend/server`: add `LaunchRequest`, `AuthzScope`, `RunStatus` types; launch returns 202 with `run_id` + `status:queued`, 400 on unparseable body, 400 when `active_testing=true` with empty `allow_targets`; status read scoped by org (404 for other-org runs, no existence disclosure); add `store.CreateAgentRun` and `store.GetAgentRun` with org-scoped persistence; resolve org from principal (never from body)
    - _Requirements: 7.1, 7.2, 7.3, 7.4, 7.5, 8.1, 8.2_
  - [ ]* 4.2 Write property test for tenant isolation on run status
    - **Property 1: Tenant isolation** — a run belonging to org B is a 404 for an org A principal and its existence is not disclosed
    - **Validates: Requirements 7.4, 7.5, 9.5**
  - [ ]* 4.3 Write unit tests for launch validation
    - 202 + queued on valid launch; 400 on bad body; 400 active-testing-requires-allow_targets; absent scope accepted as read-only
    - _Requirements: 7.2, 7.3, 8.1, 8.2_

- [x] 5. Wire agent routes into the server
  - Add `registerAgentRoutes` to `backend/server/server.go` following the existing group-by-permission pattern: `agent:run` group for `POST/GET /agent/runs*` and `GET /agent/graph-context`; `findings:ingest` group for `POST /agent/findings:ingest`
  - _Requirements: 1.6, 9.2, 9.3_

- [x] 6. Checkpoint — control-plane seams complete
  - Ensure all Go tests pass, ask the user if questions arise.

### Phase B — Fork and stand up the Strix engine (Python)

- [x] 7. Hard-fork the Strix engine into `agents-plane/`
  - [x] 7.1 Vendor the Strix engine under `agents-plane/strix/` (agents, core/coordinator, llm, runtime, report, tools, config); pin dependencies with `uv`; add project scaffolding so it runs standalone
    - _Requirements: 1.1, 1.2_
  - [ ]* 7.2 Confirm the forked engine's coordination tests pass standalone
    - Run the vendored coordinator/tree tests as-is to establish a known-good fork baseline
    - _Requirements: 2.1_

- [x] 8. Bellu config and model route in the plane
  - Add plane configuration: `BELLU_API_BASE`, service-account token wiring, LLM provider route selection, and a startup health check against the control plane
  - _Requirements: 1.3_

### Phase C — Bellu tools inside the plane (Python)

- [x] 9. `bellu_mcp` tool
  - [x] 9.1 Implement `BelluMCP`: maps one-to-one onto the six MCP read tools at `POST /api/mcp/*`, sends the service-account bearer token and no org, rejects unknown tool names before issuing a request, treats 503 as `unavailable` rather than raising, 15s timeout; register via `register_agent_tools`
    - _Requirements: 5.1, 5.2, 5.3, 5.5_
  - [ ]* 9.2 Write property test that the tool never sends an org
    - **Property 1: Tenant isolation** — across generated tool/arg combinations, no outbound MCP request carries an org field
    - **Validates: Requirements 5.3, 5.4**
  - [ ]* 9.3 Write unit tests for unknown-tool rejection and 503 handling
    - Unknown tool name raises without issuing a request; 503 returns an unavailable result
    - _Requirements: 5.2, 5.5_

- [x] 10. `graph_context` provider
  - [x] 10.1 Implement `GraphContext.fetch` against `GET /api/agent/graph-context` at run start and `as_prompt_block` rendering (nodes with kinds, edges with relations) adapted from the Strix prompting guide; inject the rendered block into the root agent's context; handle empty/unavailable (`graph:false`) subgraphs gracefully
    - _Requirements: 10.1, 10.2, 7.6_
  - [ ]* 10.2 Write unit tests for prompt rendering
    - Non-empty subgraph renders nodes and edges; empty/unavailable subgraph renders a valid, degraded block
    - _Requirements: 10.1_

- [x] 11. `finding_report` tool
  - [x] 11.1 Implement `report_findings` + `_to_bellu`: map Strix findings to the Bellu ingest schema (linked -> `asset_id`+`asset_kind`; raw -> `raw_target`), clamp `risk_score` client-side, POST to `findings:ingest`, report incrementally and on root completion
    - _Requirements: 6.1, 6.3, 6.5_
  - [ ]* 11.2 Write unit tests for the `_to_bellu` mapping
    - Linked vs raw-target mapping; risk clamp; batch payload shape
    - _Requirements: 6.3, 6.5, 6.9_

### Phase D — Read-only vertical slice end-to-end

- [x] 12. Scan runner dispatch
  - Implement the Scan_Runner: consume launched runs, fetch and inject graph-context before the coordinator initializes the root, start the coordinator, drive to terminal state, and report status back to the Runs seam
  - _Requirements: 7.6, 2.1_

- [x] 13. Coordinator completion-boundary enforcement
  - [x] 13.1 Enforce `root_complete` vs `child_complete` in the AgentCoordinator: `root_complete` from a non-root agent is rejected; `child_complete` from the root is treated as an error and does not close the scan; model statuses `running`, `waiting`, `completed`, `failed`, `budget_paused` with `waiting` while awaiting child/mailbox and `budget_paused` while budget exhausted
    - _Requirements: 2.3, 2.4, 2.5, 2.6, 2.7, 2.8, 2.9_
  - [ ]* 13.2 Write unit tests on the coordinator completion boundary
    - Non-root `root_complete` rejected; root `child_complete` errors without closing the scan; child `child_complete` returns result to parent mailbox and frees budget without terminating the scan
    - _Requirements: 2.6, 2.7, 2.8, 2.9_

- [x] 14. Checkpoint — read-only graph-reasoning slice runs end-to-end
  - Ensure all tests pass, ask the user if questions arise. The full read-only loop should run: launch -> graph-context -> live MCP -> findings ingested -> visible in Bellu.

### Phase E — Authorization-gated sandbox active testing

- [x] 15. Active testing gate
  - [x] 15.1 Implement `ActiveTestingGate`: default read-only refuses every sandbox action when scope is absent or disabled; when enabled, `ensure_allowed` permits only targets matched by `allow_targets` (hostname/IP/CIDR matching via `_compile_targets`/`_target_matches`); out-of-scope targets are refused and the refusal recorded
    - _Requirements: 8.1, 8.3, 8.4, 8.5_
  - [ ]* 15.2 Write property test for scope gating
    - **Property 4: Authorization-scope gating** — over generated (scope, target) pairs, a sandbox action is permitted iff active testing is enabled and the target is in the allow list; absent scope permits nothing
    - **Validates: Requirements 8.3, 8.4, 8.5**

- [x] 16. Docker sandbox behind the gate
  - Wire `DockerSandbox.run_active_test` to call `gate.ensure_allowed(target)` before launching any container; implement entity->target resolution for Bellu-known targets where scope permits; no container launches for refused targets
  - _Requirements: 8.3, 8.5, 3.2_

- [x] 17. Final checkpoint — end-to-end integration and safety review
  - Add an end-to-end integration test covering the full loop including an active test against an authorized throwaway target; assert tenant isolation (cross-org run/entity reads fail) and out-of-scope refusal both hold. Ensure all tests pass, ask the user if questions arise.

## Notes

- Tasks marked with `*` are optional test sub-tasks and can be skipped for a faster MVP; core implementation tasks are never optional.
- Each task references specific granular requirements for traceability.
- The safe read-only slice (Phases A–D) is fully demoable before any active-testing code exists; the opt-in, safety-critical sandbox path (Phase E) is built and reviewed last.
- Property tests validate the design's six universal correctness properties and are placed next to the code they check.
- Checkpoints ensure incremental validation at phase boundaries.

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1.1", "7.1"] },
    { "id": 1, "tasks": ["1.2", "2.1", "3.1", "4.1", "7.2", "8"] },
    { "id": 2, "tasks": ["2.2", "2.3", "2.4", "3.2", "3.3", "3.4", "4.2", "4.3", "9.1"] },
    { "id": 3, "tasks": ["5", "9.2", "9.3", "10.1", "11.1", "15.1"] },
    { "id": 4, "tasks": ["10.2", "11.2", "12", "13.1", "15.2", "16"] },
    { "id": 5, "tasks": ["13.2"] }
  ]
}
```
