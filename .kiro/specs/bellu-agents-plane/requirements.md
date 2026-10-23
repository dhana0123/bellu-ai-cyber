# Requirements Document

## Introduction

The **Bellu Agents Plane** adds Strix-style autonomous pentest agents to the Bellu platform
as a separately deployable Python subsystem. It is created by hard-forking the Strix agent
engine and editing it in place, vendored under a new top-level `agents-plane/` directory, and
layered on top of Bellu's existing Go control plane without altering the authoritative Go core.

The two subsystems form two distinct planes connected by three thin, authenticated HTTP/MCP
seams: a graph-context seam (initial bounded subgraph), a findings write-back seam, and a runs
lifecycle seam. The agents plane's default behavior is read-only graph reasoning over Bellu's
canonical security graph using the existing six MCP read tools. Active testing against real
targets via a Docker sandbox is opt-in and authorization-gated per run.

A scan target may be either a Bellu-known graph entity (addressed by a canonical id
`kind:provider:native-id`) or an operator-supplied raw target (URL/IP). Findings always flow
back into Bellu's existing finding model through the write seam, linked to a graph entity when
one applies or attached to a synthetic unlinked asset ref otherwise.

These requirements are derived from the approved design document and are written so that each of
the design's six correctness properties is traceable to at least one acceptance criterion.

## Glossary

- **Agents_Plane**: The Python subsystem (forked Strix engine) running in its own process/container that performs autonomous agent scans. Also referred to as "the agents plane."
- **Control_Plane**: Bellu's existing Go backend (`backend/server`, `internal/*`) that stays authoritative over Postgres and Memgraph. Also referred to as "the control plane."
- **AgentCoordinator**: The authoritative owner within the Agents_Plane of the agent tree, including identities, parent-links, statuses, mailboxes, snapshots, and budgets.
- **Root_Agent**: The single top-level agent in a scan that owns overall scan completion via the `root_complete` tool.
- **Specialist_Child**: A non-root agent in the tree that owns a sub-investigation and completes via the `child_complete` tool.
- **Scan_Runner**: The Agents_Plane component that owns a single run's lifecycle: fetch and inject graph-context, start the coordinator, drive to terminal state, and report status.
- **Graph_Context_Seam**: The Control_Plane endpoint `GET /api/agent/graph-context` that returns a bounded scoped subgraph for a target.
- **Findings_Ingest_Seam**: The Control_Plane endpoint `POST /api/agent/findings:ingest` that accepts agent-produced findings and writes them to the finding model.
- **Runs_Seam**: The Control_Plane endpoints `POST /api/agent/runs` and `GET /api/agent/runs/{id}` that launch a scan and report its status.
- **MCP_Read_Tools**: The six existing read-only MCP tools at `POST /api/mcp/*`: `find_entities`, `get_entity`, `get_neighbors`, `entity_history`, `recent_changes`, `describe_schema`.
- **Service_Account**: The machine principal (role `agent_service_account`) the Agents_Plane authenticates as; bound to exactly one org.
- **Org**: The tenant identifier resolved server-side from the authenticated principal, never read from client input.
- **Target_Ref**: A scan target reference with `kind` either `entity` (carrying a `canonical_id`) or `raw` (carrying a URL/IP string).
- **Canonical_ID**: A graph entity address of the form `kind:provider:native-id`.
- **Authz_Scope**: Per-run authorization object enabling active testing and listing allowed targets (`active_testing` flag plus `allow_targets`).
- **Active_Testing_Gate**: The Agents_Plane component that enforces `Authz_Scope` before any sandbox action.
- **Docker_Sandbox**: The Agents_Plane backend that executes active tests against in-scope targets only.
- **Synthetic_Asset_Ref**: An org-scoped asset identifier generated for a raw target that has no graph entity (e.g. `synthetic:<org>:<hash>`).
- **Memgraph**: The graph projection store; represented in the Control_Plane by a nullable `graph` field that is nil when the projection is unavailable.
- **maxContextNodes**: The server-side hard cap (200) on neighbor nodes in a graph-context payload.
- **maxContextDepth**: The server-side hard cap (2) on traversal depth in a graph-context payload.

## Requirements

### Requirement 1: Agents plane as a separate subsystem

**User Story:** As a platform maintainer, I want the agents plane to run as a separate Python subsystem layered on the Go core, so that I can add autonomous pentest agents without modifying Bellu's authoritative control plane.

#### Acceptance Criteria

1. THE Agents_Plane SHALL be implemented as a Python subsystem vendored under the top-level `agents-plane/` directory.
2. THE Agents_Plane SHALL run in a process and container separate from the Control_Plane.
3. THE Agents_Plane SHALL access the Control_Plane only over authenticated HTTP and MCP calls.
4. THE Agents_Plane SHALL NOT read from or write to Postgres or Memgraph directly.
5. THE Control_Plane packages `internal/model`, `internal/mcp`, `internal/finding` producer path, and `internal/graph` SHALL remain unaltered by the Agents_Plane.
6. THE Control_Plane SHALL expose the `/api/agent/*` endpoints using the existing group-by-permission registration pattern in `backend/server/server.go`.

### Requirement 2: Agent engine coordinator and tree ownership

**User Story:** As an agent engine author, I want the AgentCoordinator to be the authoritative owner of the agent tree, so that agent identities, statuses, and resources are managed consistently and no child can end a scan prematurely.

#### Acceptance Criteria

1. THE AgentCoordinator SHALL own the agent tree, including agent identities, parent-links, statuses, mailboxes, snapshots, and budgets.
2. WHEN an agent requires its identity or status, THE AgentCoordinator SHALL be the source of that information rather than the agent itself.
3. THE AgentCoordinator SHALL represent each agent status as exactly one of `running`, `waiting`, `completed`, `failed`, or `budget_paused`.
4. WHILE an agent is awaiting a child result or a mailbox message, THE AgentCoordinator SHALL set that agent's status to `waiting`.
5. WHILE an agent has exhausted its budget, THE AgentCoordinator SHALL set that agent's status to `budget_paused`.
6. WHEN the Root_Agent invokes the `root_complete` tool, THE AgentCoordinator SHALL mark the entire scan complete and drive the run to a terminal state.
7. WHEN a Specialist_Child invokes the `child_complete` tool, THE AgentCoordinator SHALL close that child's sub-investigation, return its result to the parent's mailbox, and free that child's budget without terminating the scan.
8. IF an agent that is not the Root_Agent invokes `root_complete`, THEN THE AgentCoordinator SHALL reject the invocation.
9. IF the Root_Agent invokes `child_complete`, THEN THE AgentCoordinator SHALL treat the invocation as an error and SHALL NOT close the scan through it.

### Requirement 3: Scan targets

**User Story:** As an operator, I want to scan either a known Bellu graph entity or a raw target, so that I can investigate both catalogued assets and ad-hoc URLs or IPs.

#### Acceptance Criteria

1. THE Runs_Seam SHALL accept a Target_Ref whose `kind` is either `entity` or `raw`.
2. WHERE a Target_Ref has `kind` equal to `entity`, THE Control_Plane SHALL interpret the `canonical_id` as a graph entity address of the form `kind:provider:native-id`.
3. WHERE a Target_Ref has `kind` equal to `raw`, THE Control_Plane SHALL interpret the `raw` value as an operator-supplied URL or IP with no associated graph entity.
4. IF a Target_Ref cannot be parsed into a valid entity or raw target, THEN THE Graph_Context_Seam SHALL respond with HTTP 400 and an invalid-target error.

### Requirement 4: Graph-context seam

**User Story:** As a scan runner, I want to fetch a bounded scoped subgraph for a target, so that I can inject initial graph context into the root agent without the payload growing with graph size.

#### Acceptance Criteria

1. WHEN the Graph_Context_Seam receives a request, THE Control_Plane SHALL resolve the Org from the authenticated principal and SHALL NOT read the Org from the request query or body.
2. IF the request to the Graph_Context_Seam carries no authenticated principal, THEN THE Control_Plane SHALL respond with HTTP 401 and a not-authenticated error.
3. WHEN the Graph_Context_Seam returns a subgraph, THE Control_Plane SHALL limit the neighbor node count to at most `maxContextNodes` and the traversal depth to at most `maxContextDepth`, independent of total graph size.
4. WHEN server-side caps clip the subgraph result, THE Control_Plane SHALL set the `truncated` field to true in the response.
5. WHEN the Graph_Context_Seam returns a subgraph, THE Control_Plane SHALL set the `bounded` field to true.
6. WHERE the target `kind` is `raw`, THE Graph_Context_Seam SHALL return an empty-but-valid bounded context with empty `neighbors` and empty `edges`.
7. IF the Control_Plane `graph` field is nil, THEN THE Graph_Context_Seam SHALL respond with HTTP 200, empty `neighbors`, and the `graph` field set to false.
8. IF a graph read fails while building the subgraph, THEN THE Graph_Context_Seam SHALL respond with HTTP 503 and a generic temporarily-unavailable error.

### Requirement 5: Live graph queries via MCP tools

**User Story:** As a scanning agent, I want to query the live security graph through the existing MCP read tools during a scan, so that I can reason over current graph state while the org stays enforced server-side.

#### Acceptance Criteria

1. THE `bellu_mcp` tool SHALL call only the six MCP_Read_Tools at `POST /api/mcp/*`.
2. IF the `bellu_mcp` tool is asked to call a tool name that is not one of the six MCP_Read_Tools, THEN THE `bellu_mcp` tool SHALL raise an error and SHALL NOT issue the request.
3. WHEN the `bellu_mcp` tool issues an MCP request, THE `bellu_mcp` tool SHALL send the Service_Account bearer token and SHALL NOT send an Org value.
4. WHEN an MCP_Read_Tool processes a request, THE Control_Plane SHALL resolve the Org from the authenticated principal and SHALL NOT read the Org from client input.
5. IF an MCP_Read_Tool call returns HTTP 503, THEN THE `bellu_mcp` tool SHALL report the result as unavailable rather than raising an error.

### Requirement 6: Findings write-back seam

**User Story:** As an operator, I want agent-produced findings to flow back into Bellu's finding model reliably and idempotently, so that findings for both linked and raw targets are captured without duplicates or unsafe data.

#### Acceptance Criteria

1. WHEN the Findings_Ingest_Seam receives a request, THE Control_Plane SHALL resolve the Org from the authenticated principal and SHALL ignore any Org value present in the request body.
2. IF a request to the Findings_Ingest_Seam carries no authenticated principal, THEN THE Control_Plane SHALL respond with HTTP 401 and a not-authenticated error.
3. WHEN an ingested finding has a non-empty `asset_id`, THE Control_Plane SHALL treat the finding as linked and SHALL require its `asset_kind` to pass the `model.ValidKind` allow-list.
4. IF a linked ingested finding has an `asset_kind` that fails the `model.ValidKind` allow-list, THEN THE Control_Plane SHALL reject that finding, increment the `rejected` count, and continue processing the remaining findings in the batch.
5. WHEN an ingested finding has an empty `asset_id` and a non-empty `raw_target`, THE Control_Plane SHALL attach an org-scoped Synthetic_Asset_Ref so the finding is unique and does not collide across orgs or with real canonical ids.
6. IF an ingested finding has both an empty `asset_id` and an empty `raw_target`, THEN THE Control_Plane SHALL reject that finding and increment the `rejected` count.
7. IF an ingested finding has an empty `vuln_id`, THEN THE Control_Plane SHALL reject that finding and increment the `rejected` count.
8. WHEN the Control_Plane persists an ingested finding, THE Control_Plane SHALL upsert the finding keyed on `(asset_id, vuln_id)` for the Org so that repeated ingests of the same key update the existing row rather than creating a duplicate.
9. WHEN the Control_Plane maps an ingested finding, THE Control_Plane SHALL clamp the `risk_score` to the range 0 through 100.
10. WHEN the Findings_Ingest_Seam completes a batch, THE Control_Plane SHALL respond with the counts of `created`, `updated`, and `rejected` findings.

### Requirement 7: Runs seam

**User Story:** As an operator, I want to launch agent runs and read their status scoped to my org, so that I can start scans and monitor them without seeing other tenants' runs.

#### Acceptance Criteria

1. WHEN the Runs_Seam receives a launch request, THE Control_Plane SHALL resolve the Org from the authenticated principal and SHALL NOT read the Org from the request body.
2. WHEN a launch request is accepted, THE Control_Plane SHALL persist an org-scoped run row and respond with HTTP 202 carrying a `run_id` and a `status` of `queued`.
3. IF a launch request body cannot be parsed, THEN THE Control_Plane SHALL respond with HTTP 400 and an invalid-request error.
4. WHEN a run-status request is received, THE Control_Plane SHALL return the run's `status` and `findings_count` only for runs belonging to the caller's Org.
5. IF a run-status request references a run that does not belong to the caller's Org, THEN THE Control_Plane SHALL respond with HTTP 404 run-not-found and SHALL NOT disclose the run's existence.
6. WHEN the Scan_Runner launches a run, THE Scan_Runner SHALL fetch the graph-context and inject it before the AgentCoordinator initializes the Root_Agent.

### Requirement 8: Authorization-scoped sandbox active testing

**User Story:** As a security owner, I want active testing to be opt-in and strictly scope-gated, so that the sandbox never reaches a target outside the run's declared authorization.

#### Acceptance Criteria

1. WHERE a launch request omits an Authz_Scope or the Authz_Scope does not enable active testing, THE Agents_Plane SHALL operate the run as read-only graph reasoning only.
2. IF a launch request enables active testing with an empty `allow_targets` list, THEN THE Control_Plane SHALL respond with HTTP 400 and an active-testing-requires-allow_targets error.
3. WHILE active testing is not enabled for a run, THE Active_Testing_Gate SHALL refuse every Docker_Sandbox action.
4. WHEN the Active_Testing_Gate evaluates a sandbox action against a target, THE Active_Testing_Gate SHALL permit the action only if active testing is enabled for the run and the target matches the run's `allow_targets`.
5. IF a sandbox action targets a value that is not matched by the run's `allow_targets`, THEN THE Active_Testing_Gate SHALL refuse the action and THE Docker_Sandbox SHALL NOT launch a container for that target.

### Requirement 9: Service-account authentication and authorization

**User Story:** As a security administrator, I want the agents plane to authenticate as a least-privilege service account bound to one org, so that tenant isolation is enforced at the single control-plane boundary.

#### Acceptance Criteria

1. THE Control_Plane SHALL define the permissions `agent:run` and `findings:ingest`.
2. THE Control_Plane SHALL gate `POST /api/agent/runs`, `GET /api/agent/runs/{id}`, and `GET /api/agent/graph-context` behind the `agent:run` permission.
3. THE Control_Plane SHALL gate `POST /api/agent/findings:ingest` behind the `findings:ingest` permission.
4. THE Control_Plane SHALL define a service-account role `agent_service_account` bound to exactly one Org and granted only the permissions `graph:read`, `findings:read`, `agent:run`, and `findings:ingest`.
5. WHEN any `/api/agent/*` endpoint resolves the tenant, THE Control_Plane SHALL derive the Org from the authenticated principal and SHALL NOT read the Org from any request input.
6. THE `/api/agent/*` request types SHALL NOT carry an Org field.

### Requirement 10: Bellu-graph-aware prompting

**User Story:** As an agent engine author, I want prompting adapted from Strix to be Bellu-graph-aware, so that agents reason effectively over the injected scoped subgraph.

#### Acceptance Criteria

1. THE `graph_context` provider SHALL render the injected bounded subgraph as a prompt block for injection into the Root_Agent's context.
2. WHEN the Scan_Runner initializes the Root_Agent, THE Agents_Plane SHALL inject the fetched graph-context into the Root_Agent's context using prompting adapted from the Strix prompting guide.
