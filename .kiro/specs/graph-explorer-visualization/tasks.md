# Implementation Plan: Graph Explorer Visualization

## Overview

This plan overhauls the existing Graph Explorer rendering layer (frontend-only, TypeScript + React + pixi.js v8) in four phases, built bottom-up so each step is independently verifiable and nothing is left orphaned:

1. **Pure utils first** — `iconRegistry`, `nodeSize`, `lod`, `labelCulling`, `fitToView`. Each is pure (no WebGL), independently unit-testable, and carries the correctness load. Their unit tests map directly to the 15 correctness properties.
2. **Layout + canvas integration** — evolve `forceLayout.ts` for radius-aware spacing, then wire everything into the `GraphCanvas.tsx` pixi shell (pooled node containers, radius sizing, LOD-driven visibility, labels, fit-to-view, selection edge fade).
3. **Parity refactor** — point the inventory `KindIcon`/`ProviderIcon` and the trace-graph `GraphNode` at the shared `iconRegistry`, and retire the divergent `nodeIcons.ts`. This is a cross-feature change and must keep the inventory and trace graph typechecking and rendering identically.
4. **Verification** — typecheck, lint, build, and the full Vitest suite.

All code is TypeScript/TSX under `frontend/src/features/graph-explorer/lib/` (utils), `.../components/` (canvas), and the two cross-feature consumer files. Paths are relative to `frontend/`.

## Tasks

- [x] 1. Create the shared icon/color registry utility (`lib/iconRegistry.ts`)
  - Create `frontend/src/features/graph-explorer/lib/iconRegistry.ts` as the single source of truth.
  - Define `KindStyle` interface `{ icon: LucideIcon; colorHex: number; colorCss: string; label: string }` and the authoritative `KIND_STYLE: Readonly<Record<string, KindStyle>>`, seeding it from the **union** of the existing `ai-inventory/components/category.tsx` `KIND_META` and `components/graph/GraphNode.tsx` `KIND_STYLE` so neither consumer regresses (read both files first and merge every key).
  - Define `KIND_FALLBACK` (neutral box + muted color) and `styleForKind(kind): KindStyle` that never throws (returns `KIND_FALLBACK` on unknown kind).
  - Define `PROVIDER_LOGO` (provider key → bundled brand SVG) and `providerLogoUrl(provider): string | undefined` returning `undefined` for unknown providers; lowercase the key before lookup.
  - Implement `iconTexture(provider, kind, onReady): Texture` — resolve provider brand SVG when known, else rasterize the Lucide React icon via `renderToStaticMarkup` → SVG data URI → `Image` → pixi `Texture`, tinted with the kind color; cache by `(provider | kind, colorHex)`; `onReady` fires exactly once per created texture.
  - Implement `clearIconCache(): void` to free all cached textures on teardown.
  - _Requirements: 3.1, 3.2, 3.7, 3.8, 7.5_

  - [x]* 1.1 Write unit tests for the icon registry (`lib/iconRegistry.test.ts`)
    - **Property 11: Icon/color parity with inventory** — every `KIND_STYLE` key resolves the same icon component and color that the inventory consumes; `providerLogoUrl` parity across callers.
    - **Property 12: Texture cache identity** — identical `(provider|kind, color)` inputs to `iconTexture` return the same `Texture` instance; `onReady` fires once.
    - Also test `styleForKind` returns `KIND_FALLBACK` on unknown kind and `providerLogoUrl` returns `undefined` on unknown provider.
    - **Validates: Requirements 3.5, 3.6, 3.7, 3.8, 7.5**

- [x] 2. Create the degree/size utility (`lib/nodeSize.ts`)
  - Create `frontend/src/features/graph-explorer/lib/nodeSize.ts` (pure, no pixi import).
  - Export `MIN_NODE_RADIUS = 10`, `MAX_NODE_RADIUS = 34`, `ICON_RADIUS_RATIO = 0.78`.
  - Implement `buildDegreeMap(nodes, edges): Map<string, number>` counting incident edges whose endpoints both exist in the node set (ignore dangling endpoints); nodes with no edges map to 0.
  - Implement `radiusForDegree(degree, maxDegree): number` using sqrt compression, non-decreasing in degree, clamped to `[MIN_NODE_RADIUS, MAX_NODE_RADIUS]`; `maxDegree <= 0` ⇒ `MIN_NODE_RADIUS`.
  - Implement `buildRadiusMap(nodes, edges): Map<string, number>` as the convenience id→radius builder.
  - _Requirements: 2.1, 2.2, 2.3, 2.4, 7.6_

  - [x]* 2.1 Write unit tests for the size util (`lib/nodeSize.test.ts`)
    - **Property 10: Degree correctness** — `buildDegreeMap` yields the count of incident edges with existing endpoints, including the dangling-endpoint case.
    - **Property 9: Radius monotonic in degree** — `radiusForDegree` is non-decreasing in degree, always within `[MIN_NODE_RADIUS, MAX_NODE_RADIUS]`, and `maxDegree === 0` ⇒ every node gets `MIN_NODE_RADIUS`.
    - **Validates: Requirements 2.1, 2.2, 2.3, 2.4**

- [x] 3. Create the LOD controller utility (`lib/lod.ts`)
  - Create `frontend/src/features/graph-explorer/lib/lod.ts` (pure).
  - Export `LodTier` interface `{ showIcons: boolean; labelMode: 'none' | 'top' | 'all'; topLabelBudget: number; showEdgeLabels: boolean }`.
  - Export thresholds `ICONS_AT = 0.35`, `TOP_LABELS_AT = 0.7`, `ALL_LABELS_AT = 1.6`, and `TOP_LABEL_BUDGET = 40` (plus `DOTS_ONLY_BELOW = 0.35`).
  - Add the deepest-tier threshold `EDGE_LABELS_AT = 2.4` (strictly greater than `ALL_LABELS_AT`) and the `EDGE_LABEL_BUDGET = 30` constant.
  - Implement `tierForZoom(zoom): LodTier` per the design: dots-only below `ICONS_AT`; icons+`none` to `TOP_LABELS_AT`; icons+`top` (budget `TOP_LABEL_BUDGET`) to `ALL_LABELS_AT`; icons+`all` to `EDGE_LABELS_AT`; icons+`all`+edge labels above. Monotonic in zoom; `showIcons === false` ⇒ `labelMode === 'none'`.
  - Set `showEdgeLabels: true` only at or above `EDGE_LABELS_AT` (the most-detailed tier), where `labelMode === 'all'` and `showIcons === true`; every lower tier has `showEdgeLabels: false`.
  - _Requirements: 4.2, 4.3, 4.4, 4.5, 4.6, 4.7, 4.8, 4.14, 4.15, 4.16_

  - [x]* 3.1 Write unit tests for the LOD controller (`lib/lod.test.ts`)
    - **Property 7: LOD monotonic in zoom** — across a sweep of zoom values, higher zoom reveals a superset of detail (icons stay on; label mode `none ≤ top ≤ all`).
    - **Property 8: No labels on dot-only tier** — `tierForZoom(z).showIcons === false ⇒ labelMode === 'none'`.
    - **Property 14: Edge labels only at the deepest zoom** — `showEdgeLabels` is true only at/above `EDGE_LABELS_AT`; it is monotonic in zoom (never turns off as zoom increases); and `showEdgeLabels === true ⇒ labelMode === 'all' ∧ showIcons === true`.
    - Also assert threshold boundary behavior at `ICONS_AT`, `TOP_LABELS_AT`, `ALL_LABELS_AT`, `EDGE_LABELS_AT`.
    - **Validates: Requirements 4.7, 4.8, 4.14, 4.15, 4.16**

- [x] 4. Create the label collision-culling utility (`lib/labelCulling.ts`)
  - Create `frontend/src/features/graph-explorer/lib/labelCulling.ts` (pure).
  - Export `LabelBox` `{ id; x; y; w; h; importance }` and `LabelCullInput` (`nodes`, `radii`, `importance`, `toScreen`, `measure`, `viewport`, `budget`, `labelMode`) per the design signatures.
  - Implement `selectVisibleLabels(input): string[]` — build screen-space boxes below each node (centered, offset by radius + gap), cull off-screen boxes, sort by importance desc with id asc tie-break, then greedily place using a screen-space uniform grid for near-O(n) overlap tests; stop at `budget`.
  - Guarantee: returned ids are ordered by descending importance, pairwise non-overlapping, fully inside the viewport, and `|result| <= budget`.
  - Export the edge-label siblings `EdgeLabelBox` `{ edgeId; x; y; w; h; importance }` and `EdgeLabelCullInput` (`boxes`, `viewport`, `budget`) per the design signatures.
  - Implement `selectVisibleEdgeLabels(input): string[]` as a thin sibling of `selectVisibleLabels` that takes pre-built midpoint candidate boxes (caller has already filtered to edges with a non-empty relation and both endpoints on-screen) and reuses the identical greedy, importance-first, screen-space-grid culling machinery: guard each box against the viewport, sort by importance desc with `edgeId` asc tie-break, greedily place, stop at `budget`. Returns `edgeId`s ordered by descending importance, pairwise non-overlapping, fully inside the viewport, and `|result| <= budget`.
  - _Requirements: 4.1, 4.9, 4.10, 4.11, 4.12, 4.13, 4.18_

  - [x]* 4.1 Write unit tests for label culling (`lib/labelCulling.test.ts`)
    - **Property 1: Labels never overlap** — pairwise non-overlap on dense random layouts.
    - **Property 2: Labels are on-screen** — every emitted box lies fully inside the viewport (off-screen excluded).
    - **Property 3: Label budget honored** — `|selectVisibleLabels(..., budget)| <= budget`.
    - **Property 4: Importance priority** — on forced overlaps, the culled label's importance ≤ the kept label's; results are deterministically ordered (stable id tie-break).
    - **Property 15: Edge labels never overlap, on-screen, budgeted** — on dense random midpoint boxes, `selectVisibleEdgeLabels` returns pairwise non-overlapping screen boxes, all fully inside the viewport, ordered by descending importance (stable `edgeId` tie-break), with `|result| <= EDGE_LABEL_BUDGET`.
    - **Validates: Requirements 4.9, 4.10, 4.11, 4.12, 4.13, 4.18**

- [x] 5. Create the fit-to-view utility (`lib/fitToView.ts`)
  - Create `frontend/src/features/graph-explorer/lib/fitToView.ts` (pure).
  - Export `Viewport`, `WorldTransform`, `FitOptions` (`padding`, `minZoom`, `maxZoom`) and the `MIN_ZOOM = 0.05` / `MAX_ZOOM = 12` constants.
  - Implement `computeFitToView(nodes, radii, viewport, opts): WorldTransform` — filter to nodes with finite x/y, compute the radius-expanded world bbox, derive a uniform scale to fit inside the padded viewport, clamp to `[minZoom, maxZoom]`, and center the bbox at the viewport center.
  - Empty input or no finite coordinates ⇒ centered transform at scale 1. Guard single-node/collinear bbox with an epsilon.
  - _Requirements: 5.3, 5.4, 5.5, 5.8_

  - [x]* 5.1 Write unit tests for fit-to-view (`lib/fitToView.test.ts`)
    - **Property 5: Fit-to-view contains all nodes with padding** — when the natural fit scale is within `[minZoom, maxZoom]`, every node's disc (center ± radius·scale) lies within the padded viewport, including the largest-hub-radius-not-clipped case.
    - **Property 6: Fit-to-view scale clamped** — the returned scale is always within `[minZoom, maxZoom]`, including empty-input centering at scale 1.
    - Also cover single-node and collinear degenerate bboxes.
    - **Validates: Requirements 5.3, 5.4, 5.5, 5.8**

- [x] 6. Checkpoint - pure utils complete
  - Run the Vitest suite for the five new utils. Ensure all tests pass, ask the user if questions arise.

- [x] 7. Evolve the force layout for radius-aware spacing (`lib/forceLayout.ts`)
  - Modify `frontend/src/features/graph-explorer/lib/forceLayout.ts` to accept an injected `Map<string, number>` of radii, set once per data change (not per tick).
  - Add `separationFloor(a, b, radii): number` returning `radii.get(a.id) + radii.get(b.id) + SEPARATION_PAD`; use it in the repulsion phase in place of the constant `MIN_SEPARATION` so near neighbors are pushed apart within that floor.
  - Scale the edge-spring rest length to `LINK_DISTANCE + radii.get(s.id) + radii.get(t.id)` so hubs sit farther from neighbors.
  - Raise `REPULSION` (9000 → 16000) and base `LINK_DISTANCE` (90 → 150) per the design; keep `CENTER_GRAVITY` tiny so no dense-core collapse forms.
  - _Requirements: 1.1, 1.2, 1.3, 1.4_

  - [x]* 7.1 Write unit tests for the spacing floor (`lib/forceLayout.test.ts`)
    - **Property 13: Spacing floor respects radius** — for near neighbors a,b, `separationFloor(a, b, radii) >= radius(a) + radius(b)` (asserted on the pure function).
    - **Validates: Requirements 1.1, 1.2**

- [x] 8. Integrate sizing, LOD, labels, and fit-to-view into the pixi shell (`components/GraphCanvas.tsx`)
  - [x] 8.1 Add the pooled per-node container and radius-driven sizing
    - Modify `frontend/src/features/graph-explorer/components/GraphCanvas.tsx` to replace the separate disc/icon maps with a `Map<string, NodeView>` where `NodeView = { group: Container; disc: Sprite; icon: Sprite; label: Text }`.
    - Reconcile the pool against the working set on add/remove/update (create/destroy groups on add/remove, reuse on update).
    - On data change, call `buildDegreeMap`/`buildRadiusMap` once and inject radii into the sim; size each disc by its radius and each icon by `radius * ICON_RADIUS_RATIO`; resolve icon textures via `iconTexture` from the registry.
    - _Requirements: 2.5, 7.1, 7.2, 7.6_

  - [x] 8.2 Wire LOD-driven visibility and progressive labels
    - Each frame, read `world.scale`, call `tierForZoom`, and switch disc-only / icon / label rendering accordingly.
    - Render each visible label below its node (centered, offset by radius + gap) using a pooled `Text` per `NodeView` reused frame-to-frame; when `labelMode !== 'none'`, feed `selectVisibleLabels` (with importance = degree) and show only the returned subset, hiding the rest with `visible = false`; hide off-screen and unselected labels.
    - _Requirements: 4.1, 7.3, 7.4_

  - [x] 8.3 Wire fit-to-view to load, data change, and reset
    - Call `computeFitToView` and apply the returned transform on first settled frame after load and after every working-set change (once the layout cools), and from `resetView()`.
    - Compute fit-to-view on data change and reset only, never per frame. Retain the existing zoom-in, zoom-out, and wheel-zoom focal-point behavior.
    - _Requirements: 5.1, 5.2, 5.6, 5.7, 7.7_

  - [x] 8.4 Wire selection-based edge fade and disc de-emphasis
    - Render edges faded and thin by default; on selection keep edges incident to the selected node prominent, fade non-incident edges, and de-emphasize non-incident node discs.
    - _Requirements: 6.1, 6.2, 6.3, 6.4_

  - [x] 8.5 Preserve the WebGL fallback and accessibility markup
    - Keep the existing accessible fallback panel with the table-view switch when WebGL is unavailable, the `role="img"` canvas description markup, and the accessible zoom-in/zoom-out/reset controls verbatim.
    - _Requirements: 8.1, 8.2, 8.3_

  - [x] 8.7 Render edge-relation labels at the deepest zoom tier
    - Maintain a pooled `edgeLabelPool: Text[]` in a separate edge-label layer, reused frame-to-frame (grown on demand, capped by `EDGE_LABEL_BUDGET`, never created or destroyed per frame — consistent with node-label pooling).
    - Each frame, when `tier.showEdgeLabels === true`: build screen-space midpoint candidate boxes for every edge whose `relation` is non-empty and whose **both** endpoints are on-screen, with `importance = max(degree(source), degree(target))`; run `selectVisibleEdgeLabels` with `EDGE_LABEL_BUDGET`; assign the selected subset to pooled `Text` (text = `GraphEdge.relation`) positioned at each edge midpoint and set `visible = true`, hiding every other pooled edge label with `visible = false`.
    - An edge with an empty `relation` never produces a label. When `tier.showEdgeLabels === false` (not the top tier), hide every pooled edge label. Node-label behavior from 8.2 is unchanged.
    - _Requirements: 4.16, 4.17, 4.18, 4.19, 4.20_

  - [x]* 8.6 Write a smoke test for GraphCanvas and vitest-axe on surrounding controls
    - Assert `GraphCanvas` mounts, shows the fallback when WebGL is absent, and wires the zoom/reset buttons; run `vitest-axe` on the surrounding controls (zoom in/out/reset and the `role="img"` description / table-view fallback).
    - _Requirements: 8.1, 8.2, 8.3_

- [x] 9. Checkpoint - canvas integration complete
  - Run the Vitest suite and the dev typecheck for the graph-explorer feature. Ensure all tests pass, ask the user if questions arise.

- [x] 10. Parity refactor of existing icon consumers
  - [x] 10.1 Point the inventory icons at the shared registry (`ai-inventory/components/category.tsx`)
    - Modify `frontend/src/features/ai-inventory/components/category.tsx` so `KindIcon` and `ProviderIcon` import `KIND_STYLE`/`styleForKind`/`providerLogoUrl` from `features/graph-explorer/lib/iconRegistry` instead of defining local maps.
    - Verify the inventory still typechecks and the icons/colors/brand SVGs render identically to before (the registry was seeded from this file's `KIND_META`).
    - _Requirements: 3.3, 3.5, 3.6_

  - [x] 10.2 Point the trace-graph node at the shared registry (`components/graph/GraphNode.tsx`)
    - Modify `frontend/src/components/graph/GraphNode.tsx` so its `KIND_STYLE` is imported from `features/graph-explorer/lib/iconRegistry`; keep the trace graph's own layout, only the icon + color source changes.
    - Verify the trace graph still typechecks and renders identical kind icons and colors.
    - _Requirements: 3.4, 3.5_

  - [x] 10.3 Retire the superseded `nodeIcons.ts`
    - Replace `frontend/src/features/graph-explorer/lib/nodeIcons.ts` with a thin re-export shim to `iconRegistry` (or remove it and update all callers to import from `iconRegistry`), so there is a single icon/texture source and no divergent map remains.
    - _Requirements: 3.1, 3.2_

- [x] 11. Final checkpoint and verification
  - Run `npm run typecheck`, `npm run lint`, `npm run build`, and `npm run test` from `frontend/`; fix any failures so all pass.
  - Confirm the inventory table and trace graph still render identically after the parity refactor. (Rebuilding the frontend container is out of scope for these tasks.)
  - Ensure all tests pass, ask the user if questions arise.

## Notes

- Tasks marked with `*` are optional pure-util test sub-tasks; they map 1:1 to the 15 correctness properties and can be skipped for a faster MVP, but are strongly recommended since the utils carry the correctness load.
- Each task references specific requirement sub-clauses for traceability.
- Checkpoints (tasks 6, 9, 11) ensure incremental validation at phase boundaries.
- Property tests use `fast-check` where available, else randomized loops (per the design testing strategy).
- `vitest-axe` applies only to the surrounding controls; the canvas remains a visual aid with the Table view as the accessible equivalent.
- This is a frontend-only, client-only change. No backend, network, or `GraphNode`/`GraphEdge` contract changes; degree/radius live in side Maps.
- Task 10 is a cross-feature refactor: the inventory and trace graph must keep typechecking and rendering identically, which is why the registry is seeded from the union of both existing maps in task 1.

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1", "2", "3", "4", "5"] },
    { "id": 1, "tasks": ["1.1", "2.1", "3.1", "4.1", "5.1", "7"] },
    { "id": 2, "tasks": ["7.1", "8.1"] },
    { "id": 3, "tasks": ["8.2", "8.3", "8.4", "8.5", "8.7"] },
    { "id": 4, "tasks": ["8.6", "10.1", "10.2", "10.3"] }
  ]
}
```
