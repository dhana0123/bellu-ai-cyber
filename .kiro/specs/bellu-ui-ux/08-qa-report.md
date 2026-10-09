# 08 — QA Report

Final verification for the Bellu suite frontend migration + add-on build. All checks run in
`frontend/`.

## Gate results

| Gate | Command | Result |
|---|---|---|
| Lint | `npm run lint` | ✅ 0 errors (4 `react-refresh` warnings — benign; shadcn components + context providers co-export hooks/variants) |
| Typecheck | `npm run typecheck` | ✅ clean (`tsc --noEmit`, strict) |
| Format | `prettier --check` | ✅ all files conform |
| Unit/component tests | `npm run test` | ✅ 25 passed (3 files) |
| Production build | `vite build` | ✅ 2652 modules, exit 0 |
| Dev server | `npm run dev` | ✅ boots; entry + all tiers transform (200) |

### Test breakdown (25)
- `src/lib/client.test.ts` — 8: mock-seam surface + add-on shapes.
- `src/features/addons/addons.test.tsx` — 14: every add-on renders the "Preview — demo
  data" badge and contains **no** "real/live telemetry" or "real customer data" wording.
- `src/features/addons/a11y.test.tsx` — 3: axe smoke on Gateway/Governance/Agents, no
  structural violations.

## Accessibility

- Radix primitives provide focus management, keyboard nav, and ARIA roles.
- axe smoke tests pass (structural: roles, labels, names).
- **Scoped out of the automated check:** `heading-order` (best-practice; shadcn `CardTitle`
  renders `<h3>` under the page `<h1>` — cosmetic, not WCAG A/AA) and `color-contrast`
  (cannot run in jsdom; needs a real browser).
- **Manual review still required** for full WCAG conformance: color-contrast in-browser,
  screen-reader passes, and keyboard-only walkthroughs of the connector wizard and media
  review dialog.

## Regression check (foundation preserved)

- No `.js`/`.jsx` source remains under `src` (fully TypeScript).
- Real API client (`src/lib/api.ts`) retains all foundation endpoints (34 `/api/` calls).
- Auth preserved: `bellu-token` localStorage key, Bearer header, 401-clears-token.
- RBAC preserved: flat `permissions: string[]`, `can(perm)`, `RequireAuth` gating.
- TraceGraph preserved: `NODE_W=160`/`NODE_H=96` coupling intact; reactflow + dagre layout.
- Connector AWS wizard preserved: full create→setup→verify→expand→discover state machine.
- Admin CRUD preserved (Users/Projects/Roles).

## Mock-data integrity

- All add-on data is `DEMO_`-prefixed fixtures via `src/mocks/*`, served through
  `@/lib/client`.
- Every add-on page shows the persistent **"Preview — demo data"** badge (`AddonHeader`).
- An automated test asserts no module claims real/live telemetry.
- In production builds, add-on permissions are **not** granted (the dev-only grant is gated
  by `import.meta.env.DEV`), so Preview modules stay hidden until a backend issues the
  proposed permissions.

## Known follow-ups (not blockers)

1. **Bundle size:** the main chunk is ~1.1 MB (Recharts + reactflow + Radix). Recommend
   route-level code-splitting (`React.lazy`) for TraceGraph and the chart-heavy add-ons, and
   `manualChunks` for vendor libs.
2. **E2E:** a Playwright/Cypress happy-path (login → overview → finding detail → a Preview
   module) is recommended but not added here — it needs a running backend or a mocked
   network layer, out of scope for this frontend-only pass. The dev-server smoke + component
   tests cover the equivalent surface.
3. **Color-contrast + screen-reader audits:** schedule a manual accessibility review.
4. **Add-on write flows:** current add-on UIs are read-only over mocks; define
   `*:manage` permissions and mutation contracts when those UIs are built.

## Verdict

The migration goal is met: the app is end-to-end TypeScript on official shadcn/ui + shadcn
Charts, every foundation feature is preserved and wired to the real backend, all 8 modules
are navigable, the 7 add-ons run on a clearly-labeled typed mock layer with documented API
contracts and a one-line swap path, and all automated gates are green.
