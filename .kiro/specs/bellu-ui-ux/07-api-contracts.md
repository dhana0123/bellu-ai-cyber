# 07 — API Contracts

Two parts:

1. **Foundation endpoints** — the REAL backend the frontend already consumes. Documented as
   implemented (casing matters — see `01-...md §2.3`). The typed client is
   `src/lib/api.ts`; TypeScript shapes are in `src/types/foundation.ts`.
2. **Add-on contracts (proposed)** — the SEVEN mock modules have no backend. These are the
   contracts a future backend should serve so the one-line mock→real swap in
   `src/lib/client.ts` works. Shapes are in `src/types/addons.ts`; mock data in
   `src/mocks/*`.

Conventions: base path `/api`; `Authorization: Bearer <JWT>`; errors `{ "error": string }`;
list endpoints are single-key enveloped; `401` clears the client token.

---

## PART 1 — Foundation (real, implemented)

### Auth
```
POST /api/auth/login   { email, password }
  → 200 { token, expires_at, user, memberships, permissions }   (snake_case)
GET  /api/auth/me       → 200 { user, memberships, permissions }
GET  /api/health        → 200 { status, graph }                 (public)
```

### Graph / evidence (perm `graph:read`)
```
GET /api/stats           → { raw_events, entities, edges, runs, conflicts,
                             entities_by_kind{}, edges_by_relation{} }   (un-enveloped)
GET /api/entities?kind&limit → { entities: EntityRow[] }                 (snake_case)
GET /api/runs?limit      → { runs: RunRow[] }
GET /api/conflicts?limit → { conflicts: ConflictRow[] }                  (PascalCase)
GET /api/graph/attack-paths → { paths: AttackPathRow[] }                 (raw rows)
GET /api/graph/ai-calls     → { calls: AiCallRow[] }
```

### Findings / intel (perm `findings:read`) — PascalCase
```
GET /api/findings?severity&status&asset_kind&limit → { findings: Finding[] }
GET /api/findings/:id → { finding: Finding, intel: CanonicalVuln|null, evidence: EvidenceDetail[] }
GET /api/intel/vulnerabilities?severity&kev&purl&limit → { vulnerabilities: CanonicalVuln[] }
GET /api/intel/vulnerabilities/:id → { vulnerability: CanonicalVuln }
```

### Investigation (perm `investigate`) — PascalCase
```
GET /api/edges/find?relation&like&limit → { edges: EdgeDetail[] }
GET /api/edges/explain?id → EdgeDetail                                   (un-enveloped)
```

### Admin (perms as noted)
```
GET   /api/users (users:read)            → { users: UserRow[] }
POST  /api/users (users:manage)          { email,name,password,role_key,project_ids[] } → 201 { id }
PATCH /api/users/:id/active (users:manage) { active } → 200 { ok:true }
GET   /api/roles (users:read)            → { roles: RoleRow[] }
POST  /api/roles (roles:manage)          { key,name,description,scope_level?,permissions[] } → 201 { id }
GET   /api/permissions (users:read)      → { permissions: { key, description }[] }
GET   /api/projects (projects:read)      → { projects: ProjectRow[] }
POST  /api/projects (projects:manage)    { key, name } → 201 { id }
```

### Connectors (reads `projects:read`, mutations `integrations:manage`)
```
POST   /api/connectors                   CreateConnectorInput → 201 { connection: Connection }
GET    /api/connectors/capabilities      → { capabilities: CapabilityCard[] }
GET    /api/connectors                   → { connections: Connection[] }
GET    /api/connectors/:id               → { connection: Connection }
GET    /api/connectors/:id/status        → { status: ConnectorStatusView }
POST   /api/connectors/:id/setup         → { setup: Setup }
POST   /api/connectors/:id/verify        { role_arn } → { verification: VerifyResult }
POST   /api/connectors/:id/expand        { member_account_ids[] } → { connections: Connection[] }
POST   /api/connectors/:id/discover      → { session: DiscoverySession }
POST   /api/connectors/:id/cancel        → { status: "cancelling" }
DELETE /api/connectors/:id               → { status: "deleted" }
```

Key enums: entity kinds (17), relations (16), severities
(`critical|high|medium|low|info`), finding status (`open|resolved`), connector status
(`PENDING|VERIFYING|CONNECTED|DISCOVERY_RUNNING|COMPLETE|ERROR`). Full TS definitions:
`src/types/foundation.ts`.

---

## PART 2 — Add-on contracts (PROPOSED — no backend yet)

Serve these to replace the mocks. snake_case, Bearer-authed, enveloped like the foundation.
Each module has a proposed read permission; suggested role grants below.

### Proposed permissions → roles

| Permission | Suggested built-in grants |
|---|---|
| `gateway:read` | global_admin, global_reader, project_admin, security_analyst, developer |
| `runtime:read` | global_admin, global_reader, project_admin, security_analyst |
| `agents:read` | global_admin, global_reader, project_admin, security_analyst |
| `data:read` | global_admin, global_reader, project_admin, security_analyst |
| `supplychain:read` | global_admin, global_reader, project_admin, security_analyst, developer |
| `media:read` | global_admin, project_admin, security_analyst |
| `governance:read` | global_admin, global_reader, project_admin |

(Write permissions — e.g. `gateway:manage`, `runtime:manage` — to be defined when the
mutating UIs are built. Current add-on UIs are read-only views over mock data.)

### AI Gateway (`gateway:read`)
```
GET /api/gateway/stats     → GatewayStats
  { total_calls_24h, avg_latency_ms, error_rate_pct, active_instances,
    failover_events_24h, cache_hit_rate_pct }
GET /api/gateway/series    → { series: GatewayTimePoint[] }   // {x, openai, anthropic, local}
GET /api/gateway/policies  → { policies: RoutingPolicy[] }
GET /api/gateway/keys      → { keys: ApiKey[] }
GET /api/gateway/logs?from&to&key&provider → { logs: GatewayLogEntry[] }
```

### Runtime Defense (`runtime:read`)
```
GET /api/runtime/alerts?severity&status → { alerts: RuntimeAlert[] }
GET /api/runtime/guardrails             → { guardrails: GuardrailRule[] }
GET /api/runtime/monitor?session        → { messages: ChatMonitorMessage[] }  // or WS stream
```

### Agent Security (`agents:read`)
```
GET /api/agents            → { agents: Agent[] }
GET /api/agents/tools      → { tools: ToolAuthorization[] }
GET /api/agents/audit?agent → { entries: AgentAuditEntry[] }
```

### AI Data Security (`data:read`)
```
GET /api/data/assets       → { assets: DataAsset[] }
GET /api/data/lineage      → { flows: LineageFlow[] }
GET /api/data/dlp          → { policies: DlpPolicy[] }
```

### AI Supply Chain (`supplychain:read`)
```
GET /api/supply-chain/models      → { models: ModelRegistryEntry[] }
GET /api/supply-chain/scans?severity → { results: ScanResult[] }
GET /api/supply-chain/provenance  → { records: ProvenanceRecord[] }
```

### Media Authenticity (`media:read`)
```
GET /api/media/queue?status       → { items: MediaItem[] }
GET /api/media/:id                 → { item: MediaItem }          // includes segments
GET /api/media/integrations        → { integrations: MediaIntegration[] }
```

### Governance & Compliance (`governance:read`)
```
GET /api/governance/frameworks    → { frameworks: ComplianceFramework[] }
GET /api/governance/policies      → { policies: GovernancePolicy[] }
GET /api/governance/audit?module&actor → { entries: AuditLogEntry[] }
```

Full TS shapes for every type above: `src/types/addons.ts`.

---

## PART 3 — The mock → real swap

The whole app imports data through `src/lib/client.ts`:

```ts
export const client = {
  ...realApi,                 // foundation → real backend
  gateway: gatewayMock,       // ← replace with realApi.gateway* when the API ships
  runtime: runtimeMock,
  agents: agentsMock,
  data: dataMock,
  supplyChain: supplyChainMock,
  media: mediaMock,
  governance: governanceMock,
}
```

To wire a real add-on endpoint:
1. Add the typed methods to `realApi` (`src/lib/api.ts`) matching the contract above.
2. Replace that module's mock reference in `client` with the real methods.
3. Remove the dev-only `ADDON_PERMISSIONS` grant for that permission once the backend issues
   it (so prod gating becomes real).

No page changes are required — pages depend only on `client` and `src/types/addons.ts`.
