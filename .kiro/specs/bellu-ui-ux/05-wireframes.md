# 05 — Wireframes (annotated)

Low-fidelity wireframes for the key screens, annotated with the components and data behind
them. These describe the implemented layouts; they are intentionally structural, not pixel
mocks. The common frame (sidebar + topbar) is `AppShell`.

---

## 0. App frame (every authenticated page)

```
┌──────────────┬──────────────────────────────────────────────────────────┐
│ ● Bellu      │  Signed in as user@co   [Role]           ☀/☾   Sign out   │  ← topbar
├──────────────┼──────────────────────────────────────────────────────────┤
│ AI-SPM       │                                                            │
│  Overview    │                                                            │
│  Inventory   │                   <routed page content>                   │
│  TraceGraph  │                                                            │
│  Findings    │                                                            │
│  Investigate │                                                            │
│ ADD-ONS ⚑Prev│                                                            │
│  Gateway …   │                                                            │
│ PLATFORM     │                                                            │
│  Connectors… │                                                            │
└──────────────┴──────────────────────────────────────────────────────────┘
 .cg-sidebar (#171717, dark)       main (bg #212121)
```
Sidebar items are permission-filtered. "⚑ Prev" = Preview badge on the Add-ons section.
Mobile: sidebar collapses into a hamburger → Sheet.

---

## 1. Security Overview — `/overview`

```
Security Overview
Posture of the AI security graph: assets, relationships, ingestion health.

┌─ Raw ──┐ ┌─ Ent ──┐ ┌─ Edges ┐ ┌─ Runs ─┐ ┌─ Conf ─┐      ← 5 KPI cards
│ 48,213 │ │   312  │ │  1,204 │ │   27   │ │    3   │
└────────┘ └────────┘ └────────┘ └────────┘ └────────┘

┌─ Entities by kind ───────────┐  ┌─ Edges by relation ─────────┐
│ Identity   ▇▇▇▇▇▇▇▇▇ 88      │  │ CONTAINS   ▇▇▇▇▇▇▇ …         │  ← BarChartCard (horizontal)
│ Workload   ▇▇▇▇▇▇ 61         │  │ RUNS_IN    ▇▇▇▇▇ …           │
│ DataStore  ▇▇▇▇▇ 52          │  │ HAS_VULN   ▇▇▇ …             │
└──────────────────────────────┘  └──────────────────────────────┘
```
Data: `GET /api/stats`. Charts are shadcn Charts (Recharts), themed tooltips on hover.

---

## 2. TraceGraph — `/tracegraph`

```
TraceGraph
How AI agents can reach sensitive data and external services.

┌─ Attack path visualization ─────────────────┐ ┌─ Top paths ───────────┐
│                                              │ │ [agent] support-bot   │
│  (Internet)──CONNECTS_TO──▶(AIAgent)         │ │  wl → sa → id          │
│       └─RUNS_IN─▶(Workload)─USES─▶(Identity) │ │   CAN_READ customers   │
│          └AUTHENTICATES_AS▶(Identity)        │ │ ───────────────────── │
│             └CAN_READ─▶(DataStore)           │ │ [agent] sales-asst …   │
│   [reactflow canvas, zoom/pan, LR dagre]     │ │                        │
└──────────────────────────────────────────────┘ └────────────────────────┘

┌─ Workloads calling external AI APIs ─────────────────────────────────────┐
│ Agent        Workload      AI service     Endpoint                        │
│ support-bot  api-gw        [openai]        api.openai.com/v1/…             │
└───────────────────────────────────────────────────────────────────────────┘
```
Nodes colored by kind (GraphNode); edges colored by relation. `NODE_W=160/NODE_H=96`.

---

## 3. Findings — `/findings` (+ detail)

```
Findings
[ Severity ▾ ]  [ Status ▾ ]        12 shown           ← Radix Selects

┌───────────────────────────────────────────────────────────────────────┐
│ Severity  Risk  Affected asset         Vulnerability   Status    Action │
│ [danger]   87   arn:…:bucket/pii        CVE-2025-0001  [open]   [Invest]│
│ [warning]  54   agent:support-bot       CVE-2024-9911  [open]   [Invest]│
└───────────────────────────────────────────────────────────────────────┘

(when a row's Investigate is clicked → detail card below)
┌─ Finding detail ──────────────────────────────────────────────  [Close] ┐
│ [danger] risk 87 · arn:…:bucket/pii (DataStore)                          │
│ Cited intelligence: CVE-2025-0001 [KEV] EPSS 0.421 CVSS 9.1              │
│   summary … | aliases [GHSA-…] | pkg:pypi/x [1.0, 1.4)                   │
│ Evidence trail: [steampipe] observed S3Bucket arn:… run … raw abc123…   │
└──────────────────────────────────────────────────────────────────────────┘
```
Fields are PascalCase from the backend. Severity → Badge variant mapping.

---

## 4. Connectors onboarding — `/connectors/new/aws`

```
← Connect to Amazon Web Services (AWS)                                 [AWS]

┌ ①Connection ┐   Choose Your Setup
│ ②Details    │     Connector Scope:  (•) Organization (Recommended)  ( ) Account
└─────────────┘     Installation:     (•) Standard   ( ) Bellu Outpost [soon]
                  Deploy
                    Method: (•) CloudFormation ( ) Terraform ( ) Manual
                  Permissions
                    [x] Core Security (Required)   [ ] EKS   [ ] Workload
                                                     [ Generate Setup ]
```
Step 2 (Details) swaps to: Deploy (template/launch/download) → Verify (paste Role ARN →
per-capability PASS/FAIL) → Connected (member accounts + Start Discovery).

---

## 5. Media Authenticity review — `/media` (dialog)

```
Media queue
┌──────────────────────────────────────────────────────────────────────┐
│ Type   Source        Submitted by     Authenticity  Conf  Status       │
│ 🎧audio inbound-call  twilio-webhook   [danger]24/100 93%  [flagged] [Review]│
└──────────────────────────────────────────────────────────────────────┘

(Review →)
┌─ Media authenticity review ───────────────────────────────────────────┐
│ ┌ media / waveform (demo) ┐   ┌ Authenticity gauge ┐                   │
│ │                         │   │        24           │  ← RadialChartCard │
│ │                         │   │     Authentic       │                   │
│ └─────────────────────────┘   └─────────────────────┘                   │
│ Suspicious segments:                                                    │
│   [4s–9s] voice synthesis artifact                                      │
│   [15s–18s] spectral discontinuity                                      │
└──────────────────────────────────────────────────────────────────────────┘
```
"Preview — demo data" badge in the page header. All values are demo.

---

## 6. Gateway dashboard — `/gateway`

```
AI Gateway   [Preview — demo data]
[ Dashboard | Routing policies | Budgets & keys | Usage logs ]           ← Tabs

┌calls24h┐ ┌latency┐ ┌err%┐ ┌inst┐ ┌failovr┐ ┌cache┐    ← 6 KPI cards
│ 48,213 │ │ 182ms │ │0.6%│ │  3 │ │   2   │ │37.4%│
└────────┘ └───────┘ └────┘ └────┘ └───────┘ └─────┘

┌─ API calls by provider (DEMO) ─────────────────────────────────────────┐
│  ▁▂▃▅▆▇  stacked area: OpenAI / Anthropic / Local   [legend] [tooltip]  │  ← AreaChartCard
└───────────────────────────────────────────────────────────────────────┘
```

---

## Notes for designers

- These wireframes map 1:1 to implemented components; refine spacing/visuals against the
  design tokens in `02-design-system.md` without changing the IA.
- Any new screen should reuse `AppShell`, the shadcn primitives, and the chart wrappers, and
  (for add-ons) `AddonHeader` + `DataState`.
