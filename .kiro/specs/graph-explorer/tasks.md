# Implementation Plan: Graph Explorer

## Overview

This plan builds the Graph Explorer from the data source outward: first the read-only backend
(`Store.GraphExplorer` + `handleGraphExplorer` wired under the authed `graph:read` group), then the
frontend data seam and types, then the pure-logic libraries (`categories`, `applyFilter`,
`forceLayout`), then the React components and page, and finally route registration plus accessibility
and parity tests. Each step builds on the previous one and ends by wiring the new piece into the
app, so there is no orphaned code. Backend is Go (as specified in the design); frontend is TypeScript
+ React (as specified in the design), so no implementation-language selection is needed.

Test sub-tasks marked with `*` are optional and may be skipped for a faster MVP; the eight
correctness properties from `design.md` are each covered by a dedicated test sub-task.

## Tasks

- [x] 1. Backend store aggregation: `Store.GraphExplorer`
  - [x] 1.1 Add `ExplorerFilter`, `ExplorerData`, and facet result types plus the `GraphExplorer` method in `internal/store`
    - Define `ExplorerFilter{ Kinds, Providers, Categories []string; ProjectID *int64; Limit int }` and `ExplorerData` (nodes, edges, category/provider/kind facet maps, NodeCount, EdgeCount, Capped).
    - Implement node query over `normalized_entities` selecting `canonical_id, kind, provider, name, stale` with parameterized `WHERE (kinds empty OR kind = ANY($1)) AND (providers empty OR provider = ANY($2))`, `ORDER BY kind, canonical_id`, `LIMIT $n`.
    - Implement edge query over `normalized_edges` selecting `id, src_id, relation, dst_id, confidence` restricted to `src_id = ANY($ids) AND dst_id = ANY($ids)` (edge closure among returned nodes).
    - Compute authoritative `count(*)` NodeCount and EdgeCount over the whole filtered graph (independent of the cap) and `GROUP BY` kind/provider facet maps; fold kind facets through `CATEGORY_FOR_KIND` to build category facets, summing counts.
    - Set `Capped = NodeCount > len(nodes)`; issue only `SELECT` statements (no writes).
    - _Requirements: 8.2, 8.3, 8.4, 8.5, 8.8, 9.3_

  - [ ]* 1.2 Write Go unit test for facet-sum consistency and edge closure on a seeded fixture
    - **Property 4: Facet-sum consistency** — assert `Σ facets.kinds[*].count == NodeCount`.
    - **Validates: Requirements 8.5**
    - Also assert every returned edge has both endpoints in the returned node set (property 2 at the store layer).
    - **Property 2: Edge closure (store)** — **Validates: Requirements 8.8**
    - _Requirements: 8.4, 8.5, 8.8_

- [x] 2. Backend handler: `handleGraphExplorer`
  - [x] 2.1 Define the response contract types in `backend/server`
    - Add `explorerNode`, `explorerEdge`, `facetCount`, `facets`, and `explorerResponse` structs with the snake/camel json tags from the design.
    - Add a helper to map `store.ExplorerData` into `explorerResponse`.
    - _Requirements: 8.1_

  - [x] 2.2 Implement `handleGraphExplorer` with input validation, clamping, and project scoping
    - Parse CSV `kinds`, `providers`, `categories`; validate each against the known kind/provider/category allow-lists and reject with 400 (without issuing data queries) on any non-member input.
    - Parse `limit`: default 5000 when omitted, clamp to 5000 when greater, respond 400 when non-numeric or `< 1`.
    - When `project_id` is supplied: parse it (400 on malformed), resolve the principal server-side via `PrincipalFrom`, and enforce `CanAccessProject` (403 when not accessible), mirroring `handleEntities`.
    - Call `s.store.GraphExplorer`, build the response, set `GraphAvailable = s.graph != nil`, and return 200. Never return 5xx merely because `s.graph == nil`.
    - _Requirements: 8.1, 8.6, 8.9, 8.10, 9.1, 9.2, 9.5, 10.1, 10.2, 10.3, 10.4, 10.5, 11.1, 11.2, 11.3_

  - [x] 2.3 Wire the route into the `gr` group in `backend/server/server.go`
    - Register `gr.GET("/graph/explorer", s.handleGraphExplorer)` inside the `Authenticate` + `RequirePermission(PermGraphRead)` group so the endpoint requires `graph:read`.
    - _Requirements: 10.1_

  - [ ]* 2.4 Write Go handler unit/integration tests (graceful degradation, capping, access control)
    - `graph == nil` returns 200 with `graphAvailable:false` and a non-empty Postgres-sourced node set (**Property 6: Graceful degradation**, **Validates: Requirements 11.1, 11.2, 11.3**).
    - `limit` greater than 5000 clamps to 5000; `capped:true` reported while `nodeCount`/`edgeCount` stay exact.
    - **Property 1: Badge accuracy under capping** — badge values equal `count(*)` totals independent of capping. **Validates: Requirements 5.4, 5.5, 5.6, 8.4**
    - 403 on an inaccessible `project_id`; 400 on malformed `project_id`, bad `limit`, and allow-list violations.
    - Mirror `findings_handlers_integration_test.go` for the DB-backed assertions.
    - _Requirements: 8.6, 8.10, 9.2, 9.3, 9.5, 10.3, 10.4, 11.1_

- [x] 3. Checkpoint - backend complete
  - Ensure all tests pass, ask the user if questions arise.

- [x] 4. Frontend types and data seam
  - [x] 4.1 Add `src/types/graphExplorer.ts`
    - Define `GraphNode`, `GraphEdge`, `FacetCount`, `GraphFacets`, `GraphFilter`, `SavedView`, and `GraphExplorerResponse` exactly as in the design.
    - _Requirements: 14.3_

  - [x] 4.2 Add `client.graphExplorer` in `src/lib/api.ts` and surface it through `src/lib/client.ts`
    - Build a `URLSearchParams` query encoding array facets (`categories`, `providers`, `kinds`) as comma-separated values and including `project_id`/`limit` when supplied.
    - Call the shared `get(...)`/`request(...)` helper so 401 clears the token per existing behavior; return the typed `GraphExplorerResponse`.
    - Register in `realApi` and expose on `client`.
    - _Requirements: 14.1, 14.2, 14.3, 14.4_

  - [ ]* 4.3 Write unit test for `client.graphExplorer`
    - Mock `fetch`; assert CSV facet encoding, `project_id`/`limit` inclusion, typed parsing, and 401 token-clear behavior.
    - _Requirements: 14.2, 14.3, 14.4_

- [x] 5. Frontend lib: categories
  - [x] 5.1 Add `src/features/graph-explorer/lib/categories.ts`
    - Export `CATEGORY_FOR_KIND` and `DEFAULT_CATEGORY`; add a `colorForCategory(category): number` helper (0xRRGGBB) for pixi tint.
    - _Requirements: 4.2_

  - [ ]* 5.2 Write unit test for category folding completeness
    - Assert every `ENTITY_KINDS` member maps to a category or falls back to `DEFAULT_CATEGORY`.
    - _Requirements: 4.2_

- [x] 6. Frontend lib: client-side filter
  - [x] 6.1 Add `applyFilter` in `src/features/graph-explorer/lib/applyFilter.ts`
    - Implement per the pseudocode: empty facet array imposes no constraint; a node is kept only if it satisfies every non-empty facet (AND); an edge is kept only if both endpoints are kept.
    - Return subsets of the input arrays (preserve object identity; synthesize no new node/edge objects).
    - _Requirements: 4.4, 4.5, 4.6, 4.7_

  - [ ]* 6.2 Write unit tests for `applyFilter` correctness properties
    - **Property 3: Empty-facet semantics** — empty facet keeps all values. **Validates: Requirements 4.4**
    - **Property 2: Edge closure** — every output edge has both endpoints in the output node set. **Validates: Requirements 4.6, 8.8**
    - **Property 5: Filter monotonicity** — adding a selection never grows the visible set (`visible(filter ∪ {k}) ⊆ visible(filter)`). **Validates: Requirements 4.7**
    - Also assert AND semantics across facets and subset/identity preservation.
    - _Requirements: 4.4, 4.5, 4.6, 4.7_

- [x] 7. Frontend lib: force layout
  - [x] 7.1 Add `SpatialGrid` and `stepForceLayout` in `src/features/graph-explorer/lib/forceLayout.ts`
    - Implement the spatial grid (`rebuild`, `neighbors`) and the three-phase tick: grid-bounded repulsion, edge-spring attraction, weak centering + velocity integration.
    - Seed positions on first tick (phyllotaxis/circle spread); mutate `x`/`y` in place; add/remove no nodes or edges; return `alpha * ALPHA_DECAY`.
    - Export `ALPHA_MIN`, `ALPHA_DECAY`, and force constants.
    - _Requirements: 2.1, 2.2, 2.6_

  - [ ]* 7.2 Write unit tests for `stepForceLayout`
    - **Property 7: Layout stability** — node/edge counts preserved each tick and `alpha` strictly decreases toward `ALPHA_MIN` (termination). **Validates: Requirements 2.1, 2.2, 2.4**
    - Assert repulsion considers each shared/adjacent-cell pair at most once per tick.
    - _Requirements: 2.1, 2.2, 2.4, 2.6_

- [x] 8. Checkpoint - pure logic libraries complete
  - Ensure all tests pass, ask the user if questions arise.

- [x] 9. Frontend hook: `useGraphExplorer`
  - [x] 9.1 Implement `useGraphExplorer` in `src/features/graph-explorer/hooks/useGraphExplorer.ts`
    - Fetch via `client.graphExplorer`; expose `loading`, `error`, `graphAvailable`, `facets`, `filter`, `setFilter`.
    - Derive `visibleNodes`/`visibleEdges` via `applyFilter` (memoized for referential stability); expose `visibleNodeCount`/`visibleEdgeCount` from the server `nodeCount`/`edgeCount` (never from array length), and `capped`.
    - Manage `selectedId`/`select`.
    - _Requirements: 4.3, 5.4, 5.5, 5.6, 9.4, 11.4, 14.1_

  - [x] 9.2 Implement saved-views persistence inside the hook
    - Provide the built-in non-deletable "All Resources" view (empty filter); load/persist views in `localStorage` keyed by user id; expose `views`, `activeViewId`, `selectView`, `saveView`; applying a saved view sets the filter.
    - _Requirements: 3.3, 12.1, 12.2, 12.3, 12.4, 12.5_

  - [ ]* 9.3 Write unit tests for the hook
    - Badge counts derive from server counts under capping (**Property 1**, **Validates: Requirements 5.4, 5.5, 5.6**); degraded flag handling; saved-view persistence and reload-apply; built-in view non-deletable.
    - _Requirements: 5.6, 11.4, 12.2, 12.4, 12.5_

- [x] 10. Frontend components: filter controls
  - [x] 10.1 Implement `FacetSection` in `src/features/graph-explorer/components/FacetSection.tsx`
    - Collapsible, keyboard-operable group rendering each row as a `Checkbox` + label + count `Badge`; call `onToggle(key)`; honor `searchTerm` narrowing.
    - _Requirements: 4.1, 4.2, 4.3, 3.6_

  - [x] 10.2 Implement `FilterPanel` in `src/features/graph-explorer/components/FilterPanel.tsx`
    - `Tabs` for New View / Saved Views; `Select` listing each saved view's name + description; "Filter types…" input narrowing the Node Types list; `FacetSection` instances for Category, Integrations, Node Types.
    - _Requirements: 3.1, 3.2, 3.3, 3.4, 3.5, 3.6, 4.1_

  - [ ]* 10.3 Write unit + accessibility tests for `FacetSection`/`FilterPanel`
    - Checkbox toggles call `onToggle`; `typeSearch` narrows the Node Types list; saved-view selection applies its filter.
    - vitest-axe `toHaveNoViolations()` on `FilterPanel`; keyboard reachability of the Views toggle, View selector, type search, and facet checkboxes.
    - _Requirements: 3.5, 4.3, 13.1, 13.3_

- [x] 11. Frontend component: `TopBar`
  - [x] 11.1 Implement `TopBar` in `src/features/graph-explorer/components/TopBar.tsx`
    - Graph/Table visualize toggle; node-count and edge-count `Badge`s bound to the passed counts; "Show generated query" `Switch` revealing the query text; history control.
    - _Requirements: 5.1, 5.2, 5.3, 5.4, 5.5, 5.7, 5.8, 5.9_

  - [ ]* 11.2 Write unit + accessibility tests for `TopBar`
    - Toggle invokes `onViewModeChange`; badges render passed counts; show-query reveals text.
    - vitest-axe `toHaveNoViolations()`.
    - _Requirements: 5.1, 5.4, 5.5, 5.8, 13.3_

- [x] 12. Frontend component: `GraphTableView`
  - [x] 12.1 Implement `GraphTableView` in `src/features/graph-explorer/components/GraphTableView.tsx`
    - Accessible table of the filtered node set plus an edges tab; row selection reports the node id via `onSelect`.
    - _Requirements: 6.1, 6.2, 6.4_

  - [ ]* 12.2 Write unit + accessibility tests for `GraphTableView`
    - Row selection reports the correct node id; renders nodes and edges.
    - vitest-axe `toHaveNoViolations()`.
    - _Requirements: 6.1, 6.2, 6.4, 13.3_

- [x] 13. Frontend component: `GraphCanvas` (pixi.js)
  - [x] 13.1 Implement `GraphCanvas` in `src/features/graph-explorer/components/GraphCanvas.tsx`
    - Own one `PIXI.Application` created on mount in a container ref; draw edges in a single batched `PIXI.Graphics` and nodes as tinted sprites from a shared circle texture (`colorForCategory`).
    - Run `stepForceLayout` on a decoupled ticker, syncing sprite positions each frame; stop ticking below `ALPHA_MIN` and while the tab is hidden; support pan (0.1x–10x zoom about focal point) and hover within 16px via nearest-node hit-testing; report selection via `onSelect`.
    - Destroy the application and release all GPU textures/tickers on unmount (`app.destroy(true)`).
    - Detect an unusable WebGL context and render a fallback notice; expose an accessible container label and an explicit "switch to Table view" affordance.
    - _Requirements: 1.1, 1.2, 1.3, 1.4, 1.5, 1.6, 1.7, 1.8, 1.9, 2.3, 2.4, 2.5, 13.2_

  - [ ]* 13.2 Write unit tests for `GraphCanvas` lifecycle and fallback
    - Create-once / destroy-on-unmount (no leaked WebGL context); WebGL-unavailable renders the fallback notice; one `onSelect` id per click.
    - _Requirements: 1.7, 1.8, 1.9_

- [x] 14. Frontend component: `NodeDetailPanel` and `DegradedBanner`
  - [x] 14.1 Implement `NodeDetailPanel` in `src/features/graph-explorer/components/NodeDetailPanel.tsx`
    - Slide-over showing kind/provider/name/canonical_id, attributes, evidence/source refs, and incoming/outgoing neighbor edges; render name and attribute values as text (never interpreted markup); activating a neighbor reports its id; close clears selection.
    - _Requirements: 7.1, 7.2, 7.3, 7.4, 7.5, 7.6, 7.7_

  - [x] 14.2 Implement `DegradedBanner` in `src/features/graph-explorer/components/DegradedBanner.tsx`
    - Non-blocking banner message shown when `graphAvailable` is false.
    - _Requirements: 11.4_

  - [ ]* 14.3 Write unit + accessibility tests for `NodeDetailPanel`
    - Displays identity/attributes/evidence/neighbors; neighbor activation and close behavior; untrusted values rendered as text.
    - vitest-axe `toHaveNoViolations()`.
    - _Requirements: 7.1, 7.4, 7.5, 7.6, 7.7, 13.3_

- [x] 15. Frontend page and route wiring
  - [x] 15.1 Implement the `GraphExplorer` page in `src/features/graph-explorer/GraphExplorer.tsx`
    - Mount `useGraphExplorer`; lay out `FilterPanel`, `TopBar`, and either `GraphCanvas` or `GraphTableView` by `viewMode`; render loading/error/degraded states and the `DegradedBanner` + capped hint; wire `NodeDetailPanel` to selection.
    - _Requirements: 5.2, 5.3, 9.4, 11.4_

  - [x] 15.2 Register the `/explorer` route in `src/App.tsx`
    - Add the route behind `RequireAuth permission="graph:read"`, importing the `GraphExplorer` page.
    - _Requirements: 10.1_

- [x] 16. Table/canvas parity and final checkpoint
  - [ ]* 16.1 Write the table/canvas parity test
    - **Property 8: Table/canvas parity** — the set of node ids shown in Table view equals the set rendered on the canvas for the same filter. **Validates: Requirements 6.3**
    - _Requirements: 6.3_

  - [x] 16.2 Final checkpoint
    - Ensure all tests pass, ask the user if questions arise.

## Notes

- Tasks marked with `*` are optional (tests) and can be skipped for a faster MVP; core implementation
  tasks are never optional.
- Each task references the specific requirement sub-clauses it implements for traceability.
- Checkpoints (3, 8, 16.2) provide incremental validation at backend, pure-logic, and integration
  boundaries.
- All eight correctness properties from `design.md` are covered: P1 (2.4, 9.3), P2 (1.2, 6.2),
  P3 (6.2), P4 (1.2), P5 (6.2), P6 (2.4), P7 (7.2), P8 (16.1).
- The feature has a Correctness Properties section, so property-based/property-style test sub-tasks
  are included and placed close to the code they validate.

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1.1", "4.1", "5.1"] },
    { "id": 1, "tasks": ["1.2", "2.1", "4.2", "5.2", "6.1", "7.1"] },
    { "id": 2, "tasks": ["2.2", "4.3", "6.2", "7.2"] },
    { "id": 3, "tasks": ["2.3", "9.1"] },
    { "id": 4, "tasks": ["2.4", "9.2"] },
    { "id": 5, "tasks": ["9.3", "10.1", "11.1", "12.1", "13.1", "14.1", "14.2"] },
    { "id": 6, "tasks": ["10.2", "11.2", "12.2", "13.2", "14.3"] },
    { "id": 7, "tasks": ["10.3", "15.1"] },
    { "id": 8, "tasks": ["15.2"] },
    { "id": 9, "tasks": ["16.1"] }
  ]
}
```
