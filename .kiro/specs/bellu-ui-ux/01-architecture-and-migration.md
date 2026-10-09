# 01 — Existing Architecture & Phased Migration Plan

> Status: authoritative snapshot taken before any migration work.
> Scope: **frontend only** (`frontend/`). No backend code is modified by this effort.
> Goal: incrementally migrate the existing JS/JSX control panel to **TypeScript + official
> shadcn/ui (Radix) + official shadcn Charts (Recharts)**, preserve every working backend
> integration, and extend it into the full 8‑module Bellu AI security suite — with the 7
> add‑on modules running on a typed mock layer and documented API contracts.

---

## 0. Non‑negotiable execution commitments

These apply to **every** task in the plan:

1. **Incremental migration only.** TypeScript is enabled *alongside* existing JSX
   (`allowJs: true`). Files are converted one at a time. The app builds and runs after
   every task.
2. **Preserve working backend integrations.** Auth (JWT in `localStorage['bellu-token']`),
   RBAC (`permissions: string[]` + `can(perm)`), the reactflow/dagre TraceGraph, the
   connectors AWS wizard, admin CRUD, and the real foundation API wiring must keep working.
   The exact field casing the backend sends is preserved. A `.jsx` file is deleted **only
   after** its `.tsx` replacement passes verification.
3. **Verify each phase before moving on.** After each task: `npm run build` and
   `npm run typecheck` (plus lint/tests/a11y where noted). The next task does not start
   until the current one builds, runs, and preserves existing behavior.
4. **Never claim mock data is real.** All add‑on data is clearly labeled demo/preview
   (`DEMO_`‑prefixed fixtures, a persistent "Preview — demo data" badge). No "live/real
   telemetry" wording appears on mock modules.
5. **Frontend only.** No backend code is written. Add‑on API contracts are documentation
   for later backend work.

---

## 1. Current frontend architecture (verified snapshot)

### 1.1 Toolchain

| Thing | Current value |
|---|---|
| Bundler | Vite 5 (`vite@5.4.21`) |
| Framework | React 18.3 + React Router 7 |
| Language | JavaScript / JSX (no TypeScript, no `tsconfig`) |
| Path alias | none — relative imports with explicit `.js`/`.jsx` extensions |
| Styling | Tailwind 3.4, `darkMode: ['class']`, `tailwindcss-animate` plugin |
| CSS tokens | shadcn‑style CSS variables in `src/styles.css` (`:root` + `.dark`) |
| Class merge | `cn()` = `twMerge(clsx(...))` in `src/lib/utils.js` |
| Component kit | hand‑rolled, shadcn‑shaped, **no Radix** |
| Graph | `reactflow@11.11` + `dagre@0.8` |
| Icons | `lucide-react` |
| Entry | `index.html` → `/src/main.jsx` |
| Dev proxy | `/api` → `http://localhost:8080` (`VITE_API_TARGET` override) |
| Build scripts | `dev`, `build`, `preview` (no `typecheck`/`lint`/`format` yet) |

Baseline build verified green (2437 modules, `vite build` exit 0) before migration.

### 1.2 Provider nesting (must be preserved)

`src/main.jsx`:

```
React.StrictMode
└─ ThemeProvider defaultTheme="dark"   (independent of auth; toggles .dark on <html>)
   └─ BrowserRouter                    (wraps auth so auth can navigate)
      └─ AuthProvider                  (loads principal; exposes can())
         └─ App                        (route table + guard)
```

Order matters: Theme outermost, Router before Auth, Auth before App.

### 1.3 File map (`frontend/src`)

| File | Purpose | Migration target |
|---|---|---|
| `main.jsx` | entry; provider tree + router mount | `main.tsx` |
| `App.jsx` | route table + `RequireAuth` guard | `App.tsx` |
| `api.js` | flat fetch client, token storage, 401 handling | `lib/api.ts` (+ `lib/client.ts` seam) |
| `auth.jsx` | `AuthProvider`, `useAuth`, `can()` | `auth.tsx` |
| `theme.jsx` | `ThemeProvider`, `useTheme` | `theme.tsx` |
| `useAsync.js` | universal async fetch hook (`error` as string) | `useAsync.ts` |
| `lib/utils.js` | `cn()` | `lib/utils.ts` |
| `config/nav.js` | config‑driven sidebar, permission‑gated | `config/nav.ts` (extended) |
| `components/Shell.jsx` | sidebar + topbar chrome | `components/common/AppShell.tsx` |
| `components/ui/{button,card,input,misc}.jsx` | hand‑rolled primitives | `components/ui/*` (shadcn/Radix) |
| `components/graph/{AttackPathGraph,GraphNode}.jsx` | TraceGraph | `components/graph/*` (typed) |
| `pages/*.jsx` | 11 route screens | `features/ai-spm/*` + `features/*` |

### 1.4 Auth & RBAC (preserve exactly)

- JWT stored under `localStorage['bellu-token']`; `request()` attaches
  `Authorization: Bearer <token>`.
- **401 clears the token** and throws `unauthorized`, forcing re‑login.
- `AuthProvider` loads the principal from `api.me()` on mount; on failure clears token.
- `can(perm)` = `!!principal?.permissions?.includes(perm)` — flat string permissions.
- `useAsync(fn, deps)` returns `{ loading, error, data }` where **`error` is the message
  string** (`e.message`), not an `Error`. Pages render `data?.field || []` guards.

### 1.5 Design system — "ChatGPT‑style" (preserve)

CSS variables in `styles.css`. Dark theme emulates OpenAI ChatGPT:

- `--background` `#212121` main surface; `.cg-sidebar` overrides to `#171717` (sidebar is
  darker than the surface).
- `--foreground` `#ededed`; muted `#b0b0b0`.
- **White primary** (`--primary: 0 0% 98%` with dark `--primary-foreground`), *not* a
  colored primary. Green `#10a37f` reserved as a secondary accent.
- **`card === background`** by design — separation via borders, not fill.
- Base layer applies `border-border` to all elements and `bg-background text-foreground`
  to `body`.

### 1.6 Component kit (hand‑rolled, shadcn‑shaped)

- `Button` — `cva` variants `default (white pill) | primary | destructive | outline |
  secondary | ghost | link`; sizes `default | sm | lg | icon`. `buttonVariants` is **not**
  exported today (shadcn exports it — add on migration).
- `Card` family — matches shadcn API (`Card/CardHeader/CardTitle/CardDescription/
  CardContent/CardFooter`).
- `Input`, `Label`, **native `<select>` `Select`** (NOT Radix — pages pass `<option>`
  children and read `onChange(e.target.value)`).
- `Badge` — variants `default | secondary | outline | ai | success | warning | danger`.
  Note custom `ai/success/warning/danger` and `danger` (not shadcn's `destructive`).
- Table primitives `Table/THead/TBody/TR/TH/TD`; `Spinner`.

### 1.7 TraceGraph (reactflow + dagre) — coupling to preserve

- `nodeTypes = { entity: GraphNode }`.
- **`NODE_W=160`, `NODE_H=96`** are exported by `GraphNode` and consumed by
  `AttackPathGraph.layout()` dagre math (`x - NODE_W/2`, `y - NODE_H/2`). These must stay
  in sync when typing — a code comment already warns about this.
- Dagre `rankdir: 'LR'`; `sourcePosition: Right`, `targetPosition: Left`.
- `EDGE_COLOR` keyed by relation; `KIND_STYLE` keyed by canonical kind — both must keep
  the same keys the backend emits.

---

## 2. Backend API & domain reality (verified; keep wired)

The backend is a Gin JSON API. The frontend talks only to `/api`. These shapes are honored
exactly by the TypeScript types in `types/foundation.ts`.

### 2.1 Permissions (10) and roles (6)

Permissions: `graph:read`, `findings:read`, `investigate`, `users:read`, `users:manage`,
`roles:manage`, `projects:read`, `projects:manage`, `policies:manage`,
`integrations:manage`.

Built‑in roles: `global_admin`, `global_reader`, `project_admin`, `security_analyst`,
`developer`, `viewer`.

### 2.2 Entity Kinds (17) and Relations (16)

Kinds: `CloudAccount, Identity, DataStore, Compute, Cluster, Workload, ContainerImage,
Repository, Package, Vulnerability, AIAgent, AIModel, Prompt, MCPServer, AIService,
NetworkEndpoint, ExternalService`.

Relations: `CONTAINS, RUNS_IN, RUNS_IMAGE, BUILT_FROM, DEPENDS_ON, HAS_VULN, USES,
AUTHENTICATES_AS, CAN_READ, CAN_WRITE, CAN_ADMIN, CALLS, USES_MODEL, CONNECTS_TO,
RESOLVES_TO, EXPOSED_TO`.

### 2.3 Serialization casing table (the big gotcha)

Different endpoints serialize with different casing because some Go structs lack `json`
tags. **Type each shape to its real serialization.** All list endpoints are single‑key
enveloped (e.g. `{ "findings": [...] }`).

| Endpoint | Casing | Notes |
|---|---|---|
| `POST /api/auth/login` | snake_case | `{ token, expires_at, user, memberships, permissions }` |
| `GET /api/auth/me` | snake_case | `{ user, memberships, permissions }` (shape differs slightly from login) |
| `GET /api/health` | snake_case | `{ status, graph }` |
| `GET /api/stats` | snake_case | returned **un‑enveloped**; `entities_by_kind`, `edges_by_relation` maps |
| `GET /api/entities` | snake_case | `{ entities: [{ canonical_id, kind, provider, name, attributes, stale }] }` |
| `GET /api/runs` | snake_case | `{ runs: [{ run_id, source, status, event_count, started_at, finished_at }] }` |
| `GET /api/conflicts` | **PascalCase** | `{ conflicts: [{ Key, Reason, Detail }] }` (no json tags) |
| `GET /api/graph/attack-paths` | snake_case (raw rows) | `{ paths: [{ agent, workload, service_account, cloud_identity, access, datastore }] }` |
| `GET /api/graph/ai-calls` | snake_case (raw rows) | `{ calls: [{ agent, workload, endpoint, ai_service }] }` |
| `GET /api/findings` | **PascalCase** | `{ findings: [{ ID, AssetID, AssetKind, VulnID, IntelRef, RiskScore, Severity, Status, FirstSeen, LastSeen }] }` |
| `GET /api/findings/:id` | **PascalCase** | `{ finding, intel: CanonicalVuln\|null, evidence: [{ RawEventID, Source, Collector, RunID, ResourceType, ResourceID }] }` |
| `GET /api/intel/vulnerabilities[/:id]` | **PascalCase** | `CanonicalVuln`: `{ ID, Aliases, Summary, Affected, CVSS, Severity, KnownExploited, EPSS, Published, Modified, Sources }` |
| `GET /api/edges/find`, `/api/edges/explain` | **PascalCase** | `EdgeDetail`: `{ ID, SrcID, Relation, DstID, Confidence, Evidence }` |
| users/roles/permissions/projects | snake_case | `{ users }`, `{ roles }`, `{ permissions:[{key,description}] }`, `{ projects }` |
| connectors (`/api/connectors*`) | snake_case | `Connection`, `Setup`, `VerifyResult`, `Session`, `Card`, `StatusView` |

### 2.4 Connector lifecycle (AWS)

`create → setup → verify → expand (org only) → discover`, with cancel/delete. Status
machine: `PENDING → VERIFYING → CONNECTED → DISCOVERY_RUNNING → COMPLETE`, with `ERROR`.
No cloud secrets ever cross the API; `OrgID` never serialized.

---

## 3. The 7 add‑on modules (no backend — fully mock)

Gateway, Runtime Defense, Agent Security, AI Data Security, AI Supply Chain, Media
Authenticity, Governance & Compliance. These have **no backend endpoints**. They render
from a typed mock layer (`src/mocks/*`) through `lib/client.ts`, gated by new documented
**Preview** permission strings granted to a dev mock principal:

`gateway:read, runtime:read, agents:read, data:read, supplychain:read, media:read,
governance:read`.

Each add‑on page shows a persistent **"Preview — demo data"** badge. Future backend wiring
is a one‑line change per method in `lib/client.ts`.

---

## 4. Risks & mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| **Radix Select behavior change** | native `<select>`/`<option>` → Radix `Select` changes event model (`onValueChange` vs `onChange(e.target.value)`) | Migrate per page; verify each converted page renders and filters correctly before deleting its `.jsx`. The only semantic UI change in the whole effort. |
| **TraceGraph `NODE_W/NODE_H` coupling** | wrong node positions / broken layout | Keep the exported constants and the `x - NODE_W/2` math identical; type them as `const`. |
| **Principal shape drift** (`login` vs `/me`) | runtime `undefined` reads (`memberships[0].role_name`, `user.email`) | Normalize into one typed `Principal` in `auth.tsx`; add a single adapter at both ingestion points. |
| **Casing mismatch** (PascalCase vs snake_case) | silent `undefined` in findings/intel/edges tables | `types/foundation.ts` encodes exact casing per endpoint; no renaming in the client. |
| **Big‑bang temptation** | breakage, hard rollback | `allowJs: true`; convert leaf files first; app builds after each file. |
| **Mock/real confusion** | mock data mistaken for telemetry | `DEMO_` fixtures + `PreviewBadge`; test asserts no "live/real telemetry" copy on add‑ons. |
| **Chart lib drift** | custom SVG creeping in | Use only official shadcn Charts (`ChartContainer`/`ChartConfig`/`ChartTooltip*`/`ChartLegend*`) on Recharts. |

---

## 5. File‑by‑file migration order (safe sequence)

Leaf utilities first (no dependents break), then providers, then shell, then pages. The app
builds after every step.

1. **Toolchain** — add `tsconfig.json`, `@/*` alias, TS deps, `ts,tsx` globs; `allowJs: true`.
   *(No file converted yet; JSX untouched.)*
2. **shadcn/ui** — `shadcn init`, `components.json`, generate Radix primitives; map tokens;
   extend badge variants; export `buttonVariants`. Keep old `components/ui/*.jsx` until pages
   switch imports.
3. **Charts** — add shadcn `chart` + Recharts; author `components/charts/*` wrappers.
4. **Types + mock seam** — `types/*`, `mocks/*`, `lib/api.ts`, `lib/client.ts`, `PreviewBadge`,
   dev mock principal. Pure additions.
5. **Leaf utils → TS**: `lib/utils.ts`, `useAsync.ts`.
6. **Providers → TS**: `theme.tsx`, `auth.tsx` (normalize principal), `api` re‑export shim.
7. **Shell/router → TS**: `config/nav.ts` (extended), `components/common/AppShell.tsx`,
   `App.tsx`, `main.tsx`. Switch `index.html` to `/src/main.tsx`.
8. **Foundation pages → TS** (one at a time, delete `.jsx` after each verifies):
   Dashboard→Security Overview, Entities→AI Inventory, Findings (+detail), Explain→Investigate,
   AttackPaths→TraceGraph, Connectors + ConnectAws, Users/Projects/Roles.
9. **Add‑on modules** — new `features/*` on mocks (no JSX to delete).
10. **Specs + QA** — remaining spec docs; typecheck/build/lint/tests/a11y; gate scratch routes.

### Rollback notes

- Each step is a small, self‑contained commit. If a converted page fails verification, the
  original `.jsx` is still present (we delete only after green), so reverting is dropping the
  new `.tsx` and restoring its import.
- `allowJs: true` means a half‑migrated tree always compiles; TS and JSX coexist.
- Token mapping (step 2) is reversible by restoring `styles.css` variables; no component
  logic depends on the mapping.

---

## 6. Task → verification matrix

| Task | Build/Typecheck | Extra verification |
|---|---|---|
| 1 Architecture doc | n/a | review checklist: every page/route/permission accounted for |
| 2 TS toolchain | `build` + `typecheck` green, JSX untouched | `@/` import resolves in a scratch import |
| 3 shadcn/ui + tokens | green | kitchen‑sink route renders all primitives in dark+light |
| 4 Charts | green | charts gallery renders each type responsively, both themes |
| 5 Types + mock seam | green | unit test: client returns typed foundation + typed add‑on mocks |
| 6 Shell/router TS | green | login → guarded routes → sign out; nav hides lacked perms |
| 7 Foundation pages | green + a11y | each page works vs backend (or mocked api); wizard transitions; TraceGraph renders |
| 8 Add‑on modules | green + a11y | all demo states; Preview badge present; no "live telemetry" copy |
| 9 Spec deliverables | n/a | every route/component cross‑checked; every add‑on maps to a contract |
| 10 QA | `typecheck/build/lint` green | Vitest + RTL components; happy‑path e2e; axe no critical issues |

---

## 7. Definition of done (whole effort)

- App is TypeScript end‑to‑end (no remaining foundation `.jsx`), builds and typechecks clean.
- Every foundation feature still works against the real backend with correct field casing.
- All 8 modules are navigable; the 7 add‑ons render realistic, clearly‑labeled demo data.
- Official shadcn/ui + shadcn Charts throughout; no custom SVG charts.
- Spec set (`02`–`07`) complete; add‑on API contracts documented for later backend work.
- QA report green; no mock data presented as real telemetry.
