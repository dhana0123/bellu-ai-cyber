# Requirements Document

## Introduction

The Graph Explorer Visualization feature is a visual and interaction overhaul of the existing Graph Explorer rendering layer. Today the graph collapses into a cloud of tiny, identically sized particles with no labels, divergent icons, and no fit-to-view, so it does not read as an explorable knowledge graph. This feature evolves five concerns inside the frontend rendering layer only: (1) radius-aware node spacing that prevents a dense-core "hairball"; (2) variable node size derived from node degree so hubs read as larger; (3) a single shared icon/color registry so the canvas, the inventory UI, and the trace graph render identical kind icons, colors, and provider brand SVGs; (4) resource-name labels revealed progressively by semantic zoom with collision culling so they never overlap; and (5) fit-to-view on load and after every filter/data change alongside the existing zoom controls. Edges are de-emphasized and faded on selection, and all of this must hold at approximately 4000 nodes on a pixi.js v8 WebGL canvas with pooled node containers and pooled, culled label text.

This feature is strictly frontend and client-only. It does not change the backend endpoint, the filter/facet system, the table view, saved views, or the `GraphNode`/`GraphEdge` persisted contracts, which are delivered by the base `graph-explorer` spec. Node degree and radius are held in side Maps and are never written back to the persisted `GraphNode` type.

## Glossary

- **Graph_Explorer**: The frontend feature that renders the filtered working set of nodes and edges as an interactive graph.
- **Graph_Canvas**: The thin pixi.js v8 shell component that owns the pixi `Application`, the `world` container, and the ticker, and orchestrates the rendering sub-systems.
- **Working_Set**: The set of `GraphNode` and `GraphEdge` items currently passed to the Graph_Canvas after filtering.
- **Force_Layout**: The spatial-grid force simulation that assigns world-space positions to nodes.
- **Node_Radius**: The world-unit radius of a node's disc, derived from the node's degree and clamped to `[MIN_NODE_RADIUS, MAX_NODE_RADIUS]`.
- **Degree**: The undirected count of edges incident to a node whose endpoints both exist in the Working_Set.
- **Size_Util**: The pure utility (`lib/nodeSize.ts`) that builds the degree map and maps degree to Node_Radius.
- **Icon_Registry**: The single shared source of truth (`lib/iconRegistry.ts`) mapping entity kind to a Lucide icon and accent color, and provider key to a bundled brand SVG, consumed by the canvas, the inventory `KindIcon`/`ProviderIcon`, and the trace-graph `GraphNode`.
- **Kind_Style**: A record of `{ icon, colorHex, colorCss, label }` describing the visual mark for one canonical entity kind.
- **Kind_Fallback**: The neutral Kind_Style returned for any kind not present in the Icon_Registry.
- **Provider_Logo**: The bundled brand SVG asset resolved for a known provider key.
- **Icon_Texture**: The cached pixi `Texture` for a node's visual mark (provider brand SVG when known, else the rasterized, tinted kind glyph).
- **LOD_Controller**: The pure utility (`lib/lod.ts`) that maps the current zoom to a discrete visual tier.
- **Lod_Tier**: A record `{ showIcons, labelMode, topLabelBudget }` describing what the renderer shows at a given zoom, now also carrying whether edge labels are shown.
- **Label_Manager**: The pure utility (`lib/labelCulling.ts`) that selects the ordered subset of node labels to draw without overlap.
- **Label_Box**: The screen-space axis-aligned bounding box of a candidate label, positioned below its node.
- **Importance**: The ranking value (node degree, tie-broken by id) used to prioritize which labels survive collision culling.
- **Fit_Util**: The pure utility (`lib/fitToView.ts`) that computes the world transform placing the whole laid-out graph inside the viewport with padding.
- **World_Transform**: The uniform scale plus translation applied to the `world` container (`{ scale, x, y }`).
- **Viewport**: The screen rectangle of the canvas, with positive width and height.
- **Zoom**: The current `world.scale` value, always greater than zero.
- **MIN_NODE_RADIUS**: The minimum Node_Radius in world units (10).
- **MAX_NODE_RADIUS**: The maximum Node_Radius in world units (34).
- **MIN_ZOOM**: The minimum allowed Zoom (0.05).
- **MAX_ZOOM**: The maximum allowed Zoom (12).
- **Separation_Floor**: The minimum world-space distance the Force_Layout enforces between two near nodes, equal to the sum of their radii plus a separation pad.
- **Edge_Label**: The relation-type text (`GraphEdge.relation`) drawn near the midpoint of an edge, revealed only at the highest zoom tier.
- **EDGE_LABELS_AT**: The zoom threshold at or above which Edge_Labels are shown (greater than the all-node-labels threshold).

## Requirements

### Requirement 1: Radius-aware node spacing

**User Story:** As a security analyst exploring the graph, I want nodes spread apart proportionally to their size, so that discs and labels do not overlap and the graph never collapses into a dense hairball.

#### Acceptance Criteria

1. THE Force_Layout SHALL enforce a Separation_Floor between near neighbor nodes equal to the sum of the two nodes' radii plus a separation pad.
2. WHEN two near neighbor nodes settle in the layout, THE Force_Layout SHALL keep the world-space distance between them greater than or equal to the sum of their two Node_Radius values.
3. THE Force_Layout SHALL scale edge-spring rest length by the radii of the connected nodes so that larger nodes sit farther from their neighbors.
4. THE Force_Layout SHALL spread the Working_Set so that no dense-core collapse forms at the center of the layout.

### Requirement 2: Variable node size from degree

**User Story:** As a security analyst, I want hub nodes to render larger than leaf nodes, so that I can visually identify the most connected and important entities.

#### Acceptance Criteria

1. THE Size_Util SHALL compute, for each node in the Working_Set, a Degree equal to the count of incident edges whose endpoints both exist in the Working_Set.
2. THE Size_Util SHALL map a node's Degree to a Node_Radius that is non-decreasing as Degree increases.
3. THE Size_Util SHALL clamp every Node_Radius to the range `[MIN_NODE_RADIUS, MAX_NODE_RADIUS]`.
4. WHERE the Working_Set has no edges, THE Size_Util SHALL assign MIN_NODE_RADIUS to every node.
5. THE Graph_Canvas SHALL render each node's disc and icon scaled by that node's Node_Radius so that higher-degree hubs render larger than lower-degree nodes.

### Requirement 3: Shared icon and color registry

**User Story:** As a user of the product, I want the graph to show the same kind icons, colors, and provider brand logos as the Inventory table and the trace graph, so that the graph looks like the same product.

#### Acceptance Criteria

1. THE Icon_Registry SHALL be the single source of truth mapping each entity kind to a Kind_Style and each provider key to a Provider_Logo.
2. THE Graph_Canvas SHALL resolve each node's kind icon and color from the Icon_Registry.
3. THE inventory KindIcon and ProviderIcon components SHALL resolve their kind icon, color, and provider brand SVG from the Icon_Registry.
4. THE trace-graph GraphNode SHALL resolve its kind icon and color from the Icon_Registry.
5. WHEN a kind is resolved, THE Icon_Registry SHALL return the same icon component and the same color for the Graph_Canvas and the inventory KindIcon.
6. WHEN a provider is resolved, THE Icon_Registry SHALL return the same brand SVG URL for the Graph_Canvas and the inventory ProviderIcon.
7. IF a node's kind is not present in the Icon_Registry, THEN THE Icon_Registry SHALL return Kind_Fallback.
8. IF a node's provider is not present in the Icon_Registry, THEN THE Icon_Registry SHALL return no Provider_Logo and the Graph_Canvas SHALL render the kind glyph instead.

### Requirement 4: Resource-name labels with semantic zoom

**User Story:** As a security analyst, I want resource-name labels to appear below nodes and reveal progressively as I zoom in, so that I can read entity names without the view becoming cluttered.

#### Acceptance Criteria

1. THE Graph_Canvas SHALL render each visible resource-name label below its node, centered horizontally and offset by the Node_Radius plus a gap.
2. THE LOD_Controller SHALL map the current Zoom to a Lod_Tier describing whether icons are shown, whether the label mode is `none`, `top`, or `all`, and whether edge labels are shown.
3. WHILE the Zoom is below the icons threshold, THE LOD_Controller SHALL return a Lod_Tier that shows dots only with label mode `none`.
4. WHILE the Zoom is at or above the icons threshold and below the top-labels threshold, THE LOD_Controller SHALL return a Lod_Tier that shows icons with label mode `none`.
5. WHILE the Zoom is at or above the top-labels threshold and below the all-labels threshold, THE LOD_Controller SHALL return a Lod_Tier that shows icons with label mode `top` and a bounded top-label budget.
6. WHILE the Zoom is at or above the all-labels threshold, THE LOD_Controller SHALL return a Lod_Tier that shows icons with label mode `all`.
7. WHEN comparing any two zoom values where the first is less than the second, THE LOD_Controller SHALL return a Lod_Tier for the second zoom that reveals a superset of the detail revealed at the first zoom, where the detail order places edge labels above all node labels (node-label mode `all` precedes edge labels shown).
8. IF a Lod_Tier does not show icons, THEN THE LOD_Controller SHALL set its label mode to `none`.
9. THE Label_Manager SHALL return a set of label ids whose screen-space Label_Box values are pairwise non-overlapping.
10. THE Label_Manager SHALL exclude any node whose Label_Box lies outside the Viewport.
11. THE Label_Manager SHALL return no more label ids than the budget provided for the current Lod_Tier.
12. IF two candidate labels overlap and the Label_Manager keeps one and culls the other, THEN THE Label_Manager SHALL cull the label whose Importance is less than or equal to the Importance of the kept label.
13. THE Label_Manager SHALL return label ids ordered by descending Importance with ties broken deterministically by id.
14. THE LOD_Controller SHALL include in each Lod_Tier whether edge labels are shown.
15. WHILE the Zoom is below EDGE_LABELS_AT, THE LOD_Controller SHALL return a Lod_Tier that does not show edge labels.
16. WHILE the Zoom is at or above EDGE_LABELS_AT, THE LOD_Controller SHALL return a Lod_Tier that shows edge labels and also shows icons and node labels with mode `all`.
17. WHEN edge labels are shown, THE Graph_Canvas SHALL render each visible edge's relation text near the edge midpoint.
18. THE Graph_Canvas SHALL render Edge_Labels only for edges whose both endpoints are on-screen, and SHALL cull Edge_Labels so that their screen-space boxes are pairwise non-overlapping using the same importance-ranked, budgeted collision-culling approach used for node labels, with Importance ranked by the higher endpoint Degree.
19. IF an edge's relation is empty, THEN THE Graph_Canvas SHALL render no Edge_Label for that edge.
20. THE Graph_Canvas SHALL render edge-label Text from a reused pool that is not created or destroyed per frame, consistent with node-label pooling.

### Requirement 5: Fit-to-view

**User Story:** As a security analyst, I want the whole graph to fit the viewport on load and after I change filters, so that I never have to hunt for the graph.

#### Acceptance Criteria

1. WHEN the graph loads, THE Graph_Canvas SHALL compute a fit-to-view World_Transform and apply it so that the whole laid-out graph fits the Viewport.
2. WHEN the Working_Set changes due to a filter or data change, THE Graph_Canvas SHALL compute a fit-to-view World_Transform and apply it once the layout has settled.
3. WHEN the natural fit scale is within `[MIN_ZOOM, MAX_ZOOM]`, THE Fit_Util SHALL produce a World_Transform under which every node's disc, expanded by its Node_Radius times the scale, lies within the Viewport rectangle reduced by the padding on all sides.
4. THE Fit_Util SHALL produce a World_Transform whose scale is within the range `[MIN_ZOOM, MAX_ZOOM]`.
5. THE Fit_Util SHALL center the bounding box of the laid-out nodes, expanded by each node's Node_Radius, at the center of the Viewport.
6. WHEN the user activates the reset-view control, THE Graph_Canvas SHALL compute and apply a fit-to-view World_Transform.
7. THE Graph_Canvas SHALL retain the existing zoom-in control, zoom-out control, and wheel-zoom with focal-point behavior.
8. WHERE the Working_Set is empty or no node has finite coordinates, THE Fit_Util SHALL return a centered World_Transform at scale 1.

### Requirement 6: Edge de-emphasis and selection focus

**User Story:** As a security analyst, I want edges to stay subtle and the edges of a selected node to stand out, so that I can focus on the relationships that matter without visual noise.

#### Acceptance Criteria

1. THE Graph_Canvas SHALL render edges faded and thin by default.
2. WHEN a node is selected, THE Graph_Canvas SHALL keep edges incident to the selected node prominent.
3. WHEN a node is selected, THE Graph_Canvas SHALL fade edges that are not incident to the selected node.
4. WHEN a node is selected, THE Graph_Canvas SHALL de-emphasize non-incident node discs.

### Requirement 7: Performance at scale

**User Story:** As a security analyst with a large environment, I want the overhaul to stay smooth at roughly 4000 nodes, so that exploration remains responsive.

#### Acceptance Criteria

1. THE Graph_Canvas SHALL render the Working_Set at approximately 4000 nodes using pixi.js v8.
2. THE Graph_Canvas SHALL maintain a pool of per-node containers, each holding a disc, an icon, and a label, reconciled against the Working_Set on add, remove, and update.
3. THE Graph_Canvas SHALL maintain a pool of label Text objects that are reused frame-to-frame rather than destroyed per frame.
4. THE Graph_Canvas SHALL hide labels that are off-screen or not selected by the Label_Manager by setting their visibility to false.
5. WHEN identical provider, kind, and color inputs are supplied, THE Icon_Registry SHALL return the same cached Icon_Texture instance.
6. THE Size_Util SHALL compute Node_Radius values once per data change rather than once per frame.
7. THE Graph_Canvas SHALL compute fit-to-view on data change and reset only, rather than once per frame.

### Requirement 8: Rendering tier fallback and accessibility preservation

**User Story:** As a user whose browser lacks WebGL or who relies on assistive technology, I want the fallback and surrounding controls to keep working, so that I can still use the graph explorer.

#### Acceptance Criteria

1. IF WebGL is unavailable, THEN THE Graph_Canvas SHALL display the existing accessible fallback panel with the table-view switch.
2. THE Graph_Canvas SHALL preserve the existing `role="img"` canvas description markup.
3. THE Graph_Canvas SHALL preserve the accessible zoom-in, zoom-out, and reset controls of the surrounding interface.
