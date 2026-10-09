# 04 — Module → UI Mapping

Per-module breakdown: pages, widgets, tables, filters, drilldowns, key entity fields, and
primary user roles. Reflects the implemented pages. Foundation modules bind to the real
backend; add-ons render typed mock data (`src/mocks/*`) through `@/lib/client` and are
labeled Preview.

Primary roles referenced: **CISO** (posture/overview), **Analyst** (triage/investigate),
**SRE** (ops/throughput), **Developer** (my-apps view), **Admin** (platform/governance).

---

## A. AI-SPM (Foundation · real API)

### A.1 Security Overview — `/overview`
- **Widgets:** 5 KPI cards (raw events, entities, edges, runs, conflicts). Two
  `BarChartCard`s: entities-by-kind, edges-by-relation (horizontal).
- **Data:** `GET /api/stats`.
- **Roles:** CISO (headline posture), Analyst (jump-off to Findings/TraceGraph).

### A.2 AI Inventory — `/inventory`
- **Table:** Kind, Name, Provider, Canonical ID, Stale. **Filter:** kind (Radix Select over
  the 17 kinds). AI kinds get the `ai` badge.
- **Data:** `GET /api/entities?kind&limit`.
- **Drilldown (future):** entity detail drawer (metadata, risk factors, linked findings).
- **Roles:** Analyst, Developer.

### A.3 TraceGraph — `/tracegraph`
- **Graph:** reactflow + dagre LR layout; Internet → AIAgent → Workload → Identity →
  DataStore, plus external AI calls. Node style per kind; edge color per relation.
- **Side panel:** "Top paths" list (agent → workload → identity → datastore → access).
- **Table:** workloads calling external AI APIs (Agent, Workload, AI service, Endpoint).
- **Data:** `GET /api/graph/attack-paths`, `GET /api/graph/ai-calls`.
- **Roles:** CISO (impact view), Analyst (path isolation).

### A.4 Findings — `/findings`
- **Filters:** severity, status (Radix Selects). **Table:** Severity, Risk, Affected asset
  (+kind), Vulnerability, Status, Investigate action.
- **Drilldown:** Finding detail — cited intel (ID, KEV, EPSS, CVSS, summary, aliases,
  affected package ranges) + evidence trail (collector, resource, run, raw event).
- **Fields (PascalCase):** `ID, AssetID, AssetKind, VulnID, RiskScore, Severity, Status`.
- **Data:** `GET /api/findings`, `GET /api/findings/:id`.
- **Roles:** Analyst (triage), CISO (severity rollups).

### A.5 Investigate — `/investigate`
- **Form:** relation + endpoint-contains search. **Columns:** edge list (#id, relation,
  src → dst). **Drilldown:** evidence chain for a selected edge.
- **Data:** `GET /api/edges/find`, `GET /api/edges/explain`.
- **Roles:** Analyst.

---

## B. Platform (real API)

### B.1 Cloud Connectors — `/connectors`
- **Table:** Provider, Account, Scope, Capabilities, Status, Open/Delete. **Actions:**
  Refresh, Connect AWS (gated `integrations:manage`), delete (confirm).
- **Data:** `GET /api/connectors`, `DELETE /api/connectors/:id`.

### B.2 AWS Connector Wizard — `/connectors/new/aws`
- **Two-level stepper:** Connection (scope / installation / deployment method / capability
  checkboxes) → Details (Deploy → Verify → Connected).
- **Deploy:** CloudFormation (launch URL / copy / download template), Terraform (file list),
  or Manual (trust + permission policy JSON). **Verify:** paste role ARN → per-capability
  PASS/FAIL. **Connected:** org member-account expansion + Start Discovery.
- **Data:** `POST /api/connectors`, `/:id/setup|verify|expand|discover`.
- **Roles:** Admin / SRE.

### B.3 Users — `/users`, Projects — `/projects`, Roles — `/roles`
- **Users:** table (email, name, roles, projects, status) + create form (Radix Select role,
  project scope pills), enable/disable. **Projects:** list + create. **Roles:** cards with
  scope + permission chips.
- **Data:** users/roles/permissions/projects CRUD.
- **Roles:** Admin.

---

## C. Add-ons (Preview · mock data)

Each add-on page carries the **"Preview — demo data"** badge and organizes sub-pages in
Tabs with uniform loading/empty/error/populated states (`DataState`).

### C.1 AI Gateway — `/gateway`
- **Dashboard:** 6 KPI cards (calls 24h, avg latency, error rate, instances, failovers,
  cache hit) + `AreaChartCard` of calls-by-provider.
- **Routing policies:** table (priority, name, source, model match, destination, status).
- **Budgets & keys:** table (key, owner, budget, spent, rate limit, status).
- **Usage logs:** table (time, key, provider, prompt tokens, latency, status).
- **Roles:** SRE (throughput), Admin (policies/budgets).

### C.2 Runtime Defense — `/runtime`
- **Runtime alerts:** severity, agent, event type, summary + prompt excerpt, status.
- **Guardrail rules:** rule, condition, action (allow/deny/notify), hits, status.
- **Live chat monitor:** session feed with flagged-message highlighting.
- **Roles:** Analyst, SRE.

### C.3 Agent Security — `/agents`
- **Agent inventory:** agent, creator, status, actions/24h, risk score, allowed tools.
- **Tool authorization:** tool, scope, #agents, allowed/blocked.
- **Audit log:** time, agent, tool, action, outcome.
- **Roles:** Analyst, Admin.

### C.4 AI Data Security — `/data`
- **Data inventory:** asset, type (PII/PHI/secret/IP/financial), location, owner,
  sensitivity.
- **Lineage flow:** source → via (model/agent) → destination with policy-violation
  highlighting.
- **DLP policies:** policy, pattern, category, action (block/warn/log), matches, status.
- **Roles:** Analyst, Admin.

### C.5 AI Supply Chain — `/supply-chain`
- **Model registry:** model, framework, source, hash, criticality, signed.
- **Scan results:** severity, artifact, component (purl), CVE, fixed-in.
- **Provenance:** model, built-by, built-at, dataset, signature verified.
- **Roles:** Developer, Analyst.

### C.6 Media Authenticity — `/media`
- **Media queue:** type, source, submitted-by, authenticity score, confidence, status,
  Review action.
- **Review dialog:** media/waveform placeholder + `RadialChartCard` authenticity gauge +
  suspicious-segment timeline.
- **Integrations:** integration, status, scanned/24h.
- **Roles:** Analyst (SOC review).

### C.7 Governance & Compliance — `/governance`
- **Compliance reports:** per-framework cards (controls passing/total + % progress bar).
- **Policy library:** policy, category, applies-to, status.
- **Audit log:** time, actor, module, action, target.
- **Roles:** CISO, Admin, auditors.

---

## D. Cross-cutting UI flows

- **Finding → Investigate:** a finding's "Investigate" opens its detail (cited intel +
  evidence); the Investigate page traces arbitrary edges.
- **Connector onboarding:** Connectors list → AWS wizard → back to list with new status.
- **Add-on → real backend:** each add-on's data call is one line in `@/lib/client`; swapping
  a mock for a real endpoint does not change any page.
