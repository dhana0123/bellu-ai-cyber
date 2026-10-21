# Requirements Document

## Introduction

The Graph Explorer is a frontend feature that renders Bellu's canonical security graph — the
normalized `entities` and `edges` of an AI/cloud environment — as an interactive, force-directed
WebGL canvas scaling to approximately 4,000 or more nodes and edges. It provides a left filter panel
(Category, Integrations/provider, and Node Type facets with per-facet counts and free-text type
search), a saved-views vs new-view toggle, a Graph/Table view switch, live node/edge count badges, a
"Show generated query" toggle, and selection-driven node detail. All graph data is reachable through
an accessible Table view and keyboard-operable controls.

The feature is backed by one new, read-only, RBAC-gated backend endpoint
(`GET /api/graph/explorer`) that aggregates nodes, edges, and facet counts from the existing Postgres
`normalized_entities` and `normalized_edges` tables. The endpoint degrades gracefully when Memgraph
is unavailable, serving stored Postgres data rather than returning a server error. Saved views are
persisted in browser `localStorage`, including a built-in, non-deletable "All Resources" view.

These requirements are derived from the approved design document (`design.md`) and are consistent
with its technical approach: Postgres as the authoritative data source, server-computed facets and
exact counts, client-side facet filtering, and accessibility as a first-class concern.

## Glossary

- **Graph_Explorer**: The frontend feature and page mounted at the `/explorer` route that renders the
  canonical security graph and its controls.
- **Graph_Canvas**: The pixi.js WebGL component that renders the filtered node/edge set as a
  force-directed graph and handles pan, zoom, hover, and selection.
- **Table_View**: The accessible, virtualized table component presenting the same filtered node and
  edge set as the canvas.
- **Filter_Panel**: The left-side panel containing the Views toggle, View selector, type search, and
  the Category, Integrations, and Node Types facet sections.
- **Top_Bar**: The top control strip containing the Graph/Table visualize toggle, node-count and
  edge-count badges, the "Show generated query" toggle, and a history control.
- **Node_Detail_Panel**: The slide-over panel shown on node selection, presenting node identity,
  attributes, evidence/sources, and immediate neighbors.
- **Force_Layout**: The custom spatial-grid force simulation (`stepForceLayout`) that positions
  nodes on the canvas over successive ticks.
- **Explorer_API**: The backend endpoint `GET /api/graph/explorer` and its supporting store method
  `Store.GraphExplorer`.
- **Facet**: A grouping dimension over the node set — one of Category, Integration (provider), or
  Node Type (kind) — each with selectable keys and per-key counts.
- **Node**: A canonical entity with `id` (canonical_id), `kind`, `provider`, derived `category`,
  `name`, and `stale` flag.
- **Edge**: A relationship between two nodes with `id`, `source`, `target`, `relation`, and
  `confidence`.
- **Saved_View**: A named, described, persisted filter configuration. The built-in "All Resources"
  view has an empty filter and cannot be deleted.
- **Working_Set**: The node/edge set returned by Explorer_API for a given filter, bounded by the
  server cap.
- **Cap**: The server-side maximum number of nodes returned in a single response (default 5000),
  clamped to protect the database and the browser renderer.
- **Node_Count**: The authoritative count of nodes matching a filter across the whole graph,
  computed server-side with `count(*)`, independent of the cap.
- **Edge_Count**: The authoritative count of edges matching a filter across the whole graph.
- **Graph_Available**: A server-reported boolean indicating whether the optional Memgraph service is
  reachable; `false` triggers degraded UI but not an error.
- **PermGraphRead**: The RBAC permission (`graph:read`) required to access graph data.

## Requirements

### Requirement 1: Render the force-directed graph canvas

**User Story:** As a security analyst, I want to see the canonical security graph as an interactive force-directed canvas, so that I can visually explore relationships across thousands of entities.

#### Acceptance Criteria

1. WHEN the Graph_Explorer loads with a Working_Set, THE Graph_Canvas SHALL render exactly one WebGL sprite per node in the Working_Set and exactly one drawn line per edge in the Working_Set, such that the count of rendered sprites equals the node count and the count of rendered lines equals the edge count.
2. THE Graph_Canvas SHALL render a Working_Set containing up to at least 4000 nodes and up to at least 4000 edges without dropping any node or edge from the rendered set.
3. WHILE a Working_Set of at least 4000 nodes and at least 4000 edges is rendered, THE Graph_Canvas SHALL sustain pan and zoom interactions at a frame rate of at least 30 frames per second.
4. WHEN a user performs a pan gesture, THE Graph_Canvas SHALL translate the rendered view by the same horizontal and vertical displacement as the gesture, keeping each node's world position fixed relative to the other nodes.
5. WHEN a user performs a zoom gesture, THE Graph_Canvas SHALL scale the rendered view by the gesture's scale factor about the gesture's focal point, within a zoom range of 0.1x to 10x.
6. WHEN a user moves the pointer within 16 pixels (screen space) of a rendered node, THE Graph_Canvas SHALL identify the single node whose center is closest to the pointer and apply a visually distinct hover state to only that node.
7. WHEN a user clicks a node, THE Graph_Canvas SHALL report that node's id to its onSelect handler as the selected node, and SHALL report exactly one node id per click.
8. WHEN the Graph_Canvas component unmounts, THE Graph_Canvas SHALL destroy its WebGL application and release all associated GPU textures and tickers so that no WebGL context remains allocated.
9. IF a usable WebGL context cannot be created, THEN THE Graph_Canvas SHALL render a fallback notice indicating that the graph canvas is unavailable, and THE Graph_Explorer SHALL present the Table_View as a selectable alternative that requires no WebGL context.

### Requirement 2: Force layout simulation

**User Story:** As a security analyst, I want the graph to settle into a stable, readable layout, so
that I can distinguish clusters and relationships without the animation running indefinitely.

#### Acceptance Criteria

1. WHEN Force_Layout executes a tick, THE Force_Layout SHALL update node `x` and `y` positions in
   place without adding or removing any node or edge.
2. WHEN Force_Layout executes a tick, THE Force_Layout SHALL return a decreased alpha value equal to
   the input alpha multiplied by the decay factor.
3. WHILE the alpha value remains at or above the minimum threshold, THE Graph_Canvas SHALL continue
   running Force_Layout ticks.
4. WHEN the alpha value falls below the minimum threshold, THE Graph_Canvas SHALL stop running
   Force_Layout ticks.
5. WHILE the browser tab is hidden, THE Graph_Canvas SHALL pause Force_Layout ticks.
6. WHEN Force_Layout computes repulsion, THE Force_Layout SHALL consider each node pair that shares or
   borders a spatial-grid cell at most once per tick.

### Requirement 3: Filter panel views and type search

**User Story:** As a security analyst, I want to switch between creating a new view and selecting a
saved view, and to search node types by text, so that I can quickly focus the graph on relevant
resources.

#### Acceptance Criteria

1. THE Filter_Panel SHALL present a toggle between a "New View" mode and a "Saved Views" mode.
2. WHEN a user selects the "Saved Views" mode, THE Filter_Panel SHALL present a View selector listing
   each Saved_View with its name and description.
3. WHEN a user selects a Saved_View from the View selector, THE Graph_Explorer SHALL apply that
   Saved_View's filter to the Working_Set.
4. THE Filter_Panel SHALL present a free-text "Filter types…" input.
5. WHEN a user enters text in the "Filter types…" input, THE Filter_Panel SHALL narrow the Node Types
   facet list to rows whose label matches the entered text.
6. THE Filter_Panel SHALL present collapsible facet sections for Category, Integrations, and Node
   Types.

### Requirement 4: Facet selection and per-facet counts

**User Story:** As a security analyst, I want to filter the graph by category, integration, and node
type using checkboxes with counts, so that I can narrow the view and see how many resources each
option covers.

#### Acceptance Criteria

1. THE Filter_Panel SHALL render each facet row as a checkbox with a label and a count badge.
2. THE Filter_Panel SHALL display, for each facet row, the count supplied by Explorer_API for that
   facet key.
3. WHEN a user toggles a facet checkbox, THE Graph_Explorer SHALL update the active selection for
   that facet.
4. WHERE a facet selection is empty, THE Graph_Explorer SHALL impose no constraint for that facet and
   SHALL include nodes of every value for that facet.
5. WHEN a node is evaluated against the active filter, THE Graph_Explorer SHALL include that node only
   if the node satisfies every facet that has a non-empty selection.
6. WHEN the node set changes due to filtering, THE Graph_Explorer SHALL include an edge only if both
   the edge source node and the edge target node are in the included node set.
7. WHEN a user adds a facet selection to the active filter, THE Graph_Explorer SHALL produce a visible
   node set that is a subset of the visible node set before the selection was added.

### Requirement 5: Top bar controls and count badges

**User Story:** As a security analyst, I want top-bar controls to switch visualization modes, see
live counts, and reveal the generated query, so that I can understand and verify what the explorer is
showing.

#### Acceptance Criteria

1. THE Top_Bar SHALL present a Graph/Table visualize toggle.
2. WHEN a user selects the Graph option, THE Graph_Explorer SHALL display the Graph_Canvas.
3. WHEN a user selects the Table option, THE Graph_Explorer SHALL display the Table_View.
4. THE Top_Bar SHALL display a node-count badge whose value equals the Node_Count returned by
   Explorer_API.
5. THE Top_Bar SHALL display an edge-count badge whose value equals the Edge_Count returned by
   Explorer_API.
6. WHEN the Working_Set is capped, THE Graph_Explorer SHALL derive the node-count and edge-count
   badge values from the server-reported counts rather than from the length of the returned node or
   edge arrays.
7. THE Top_Bar SHALL present a "Show generated query" toggle.
8. WHEN a user enables the "Show generated query" toggle, THE Top_Bar SHALL display the generated
   query text.
9. THE Top_Bar SHALL present a history control.

### Requirement 6: Table view as the accessible data equivalent

**User Story:** As a keyboard or screen-reader user, I want a table representation of the graph data,
so that I can access the same nodes and edges without relying on the visual canvas.

#### Acceptance Criteria

1. THE Table_View SHALL present the nodes of the current filtered node set in an accessible table.
2. THE Table_View SHALL present the edges of the current filtered edge set in an accessible table.
3. THE set of node ids presented by the Table_View SHALL equal the set of node ids rendered by the
   Graph_Canvas for the same filter.
4. WHEN a user selects a row in the Table_View, THE Graph_Explorer SHALL report that row's node id as
   the selected node.

### Requirement 7: Node selection and detail panel

**User Story:** As a security analyst, I want to select a node and see its full detail, so that I can
inspect its identity, attributes, evidence, and immediate relationships.

#### Acceptance Criteria

1. WHEN a node is selected, THE Node_Detail_Panel SHALL display the selected node's kind, provider,
   name, and canonical_id.
2. WHEN a node is selected, THE Node_Detail_Panel SHALL display the selected node's attributes.
3. WHEN a node is selected, THE Node_Detail_Panel SHALL display the selected node's evidence and
   source references.
4. WHEN a node is selected, THE Node_Detail_Panel SHALL display the selected node's incoming and
   outgoing neighbor edges.
5. WHEN a user activates a neighbor in the Node_Detail_Panel, THE Graph_Explorer SHALL report that
   neighbor's id as the selected node.
6. WHEN a user closes the Node_Detail_Panel, THE Graph_Explorer SHALL clear the selected node.
7. THE Node_Detail_Panel SHALL render entity name and attribute values as text rather than as
   interpreted markup.

### Requirement 8: Graph explorer backend endpoint

**User Story:** As a frontend client, I want a read-only endpoint that returns nodes, edges, facets, and exact counts, so that the explorer can render accurate data and badges from a single request.

#### Acceptance Criteria

1. WHEN a request is received at `GET /api/graph/explorer`, THE Explorer_API SHALL return a response containing the nodes and edges of the Working_Set, the facet count maps, the Node_Count, the Edge_Count, the capped flag, and the Graph_Available flag.
2. THE Explorer_API SHALL source node, edge, and facet data from the Postgres `normalized_entities` and `normalized_edges` tables.
3. THE Explorer_API SHALL issue only read (`SELECT`) statements and SHALL perform no write.
4. WHEN computing Node_Count and Edge_Count, THE Explorer_API SHALL compute totals matching the filter across the whole graph using `count(*)`, independent of the Cap.
5. WHEN computing facet counts, THE Explorer_API SHALL aggregate counts over the whole filtered graph using `GROUP BY`, independent of the Cap, such that the sum of the counts across a facet's partition equals the Node_Count.
6. WHEN the request omits an explicit limit, THE Explorer_API SHALL bound the returned nodes to a default Cap of 5000, and WHEN the request supplies a limit greater than 5000, THE Explorer_API SHALL clamp the Cap to 5000.
7. WHEN the Node_Count exceeds the number of returned nodes, THE Explorer_API SHALL set the capped flag to true; otherwise THE Explorer_API SHALL set the capped flag to false.
8. THE Explorer_API SHALL return an edge in the Working_Set only if both the edge source and the edge target are among the returned nodes.
9. WHEN building SQL for facet and filter inputs, THE Explorer_API SHALL use parameterized queries and SHALL validate facet inputs against the known kind, provider, and category allow-lists before use.
10. IF a supplied facet input is not a member of its allow-list, or the `limit` is non-numeric or less than 1, or the `project_id` is malformed, THEN THE Explorer_API SHALL reject the request without issuing data queries and SHALL return a client-error response indicating the invalid input, leaving stored data unchanged.
11. WHEN the Memgraph service is unavailable, THE Explorer_API SHALL still return the Postgres-sourced Working_Set with the Graph_Available flag set to false and SHALL NOT return a server-error response.

### Requirement 9: Result capping with exact counts

**User Story:** As a security analyst, I want the explorer to stay responsive on very large result
sets while still showing honest totals, so that I can trust the counts even when the canvas shows a
subset.

#### Acceptance Criteria

1. WHEN a request supplies no limit, THE Explorer_API SHALL apply a default Cap to the returned node
   set.
2. WHEN a request supplies a limit that exceeds the server maximum, THE Explorer_API SHALL clamp the
   returned node count to the server maximum.
3. WHEN the filtered result exceeds the Cap, THE Explorer_API SHALL return at most Cap nodes while
   still reporting the exact Node_Count and Edge_Count.
4. WHEN the Working_Set is capped, THE Graph_Explorer SHALL display an indication that it is showing a
   subset of the total matching results.
5. IF the `limit` query parameter is malformed, THEN THE Explorer_API SHALL respond with status 400.

### Requirement 10: Access control and project scoping

**User Story:** As a platform operator, I want the explorer endpoint to enforce authentication,
permissions, and project scoping, so that users only see graph data they are authorized to access.

#### Acceptance Criteria

1. WHEN a request is received at `GET /api/graph/explorer`, THE Explorer_API SHALL require an
   authenticated principal holding the PermGraphRead permission.
2. WHERE a request supplies a `project_id`, THE Explorer_API SHALL authorize access using
   CanAccessProject.
3. IF a request supplies a `project_id` the principal cannot access, THEN THE Explorer_API SHALL
   respond with status 403.
4. IF a request supplies a malformed `project_id`, THEN THE Explorer_API SHALL respond with status
   400.
5. THE Explorer_API SHALL resolve the principal and tenant server-side rather than from client-
   supplied identity values.

### Requirement 11: Graceful degradation when Memgraph is unavailable

**User Story:** As a security analyst, I want the explorer to keep working when the live graph service
is down, so that I can still inspect stored graph data and use the table view.

#### Acceptance Criteria

1. WHILE the Memgraph service is nil or unavailable, THE Explorer_API SHALL respond with status 200
   and Postgres-sourced data rather than a 5xx error.
2. WHEN the Memgraph service is unavailable, THE Explorer_API SHALL set the Graph_Available flag to
   false.
3. WHEN the Memgraph service is unavailable and the filter matches stored entities, THE Explorer_API
   SHALL return a non-empty node set sourced from Postgres.
4. WHEN Graph_Available is false, THE Graph_Explorer SHALL display a non-blocking degraded banner and
   SHALL keep the Table_View and SQL-backed facets functional.

### Requirement 12: Saved views persistence

**User Story:** As a security analyst, I want to save and reuse named filter configurations, so that I
can return to the views I care about across sessions.

#### Acceptance Criteria

1. THE Graph_Explorer SHALL provide a built-in Saved_View named "All Resources" with an empty filter.
2. THE Graph_Explorer SHALL prevent deletion of the built-in "All Resources" view.
3. WHEN a user saves a new view with a name, THE Graph_Explorer SHALL persist that Saved_View to
   browser localStorage keyed by user id.
4. WHEN the Graph_Explorer mounts, THE Graph_Explorer SHALL load previously persisted Saved_Views from
   localStorage.
5. WHEN a persisted Saved_View is selected after reload, THE Graph_Explorer SHALL apply the same
   filter that was saved.

### Requirement 13: Accessibility and keyboard operability

**User Story:** As a keyboard or screen-reader user, I want the explorer controls and data views to be
operable without a mouse and free of accessibility violations, so that I can use every feature of the
explorer.

#### Acceptance Criteria

1. WHEN a user navigates with the keyboard, THE Filter_Panel SHALL expose the Views toggle, View
   selector, type search, and facet checkboxes as reachable and operable controls.
2. THE Graph_Canvas container SHALL expose an accessible label and an explicit affordance to switch to
   the Table_View.
3. WHEN the Filter_Panel, Top_Bar, Table_View, and Node_Detail_Panel are tested with the accessibility
   checker, THE components SHALL report no accessibility violations.

### Requirement 14: Client data seam

**User Story:** As a frontend developer, I want a single typed client method for the explorer data, so
that the page fetches data consistently with the rest of the app.

#### Acceptance Criteria

1. WHEN the Graph_Explorer requests data, THE client SHALL call `client.graphExplorer` with the active
   filter.
2. WHEN building the request, THE client SHALL encode array facets as comma-separated query values and
   SHALL include `project_id` and `limit` when supplied.
3. WHEN the request returns, THE client SHALL return a typed GraphExplorerResponse.
4. IF the request returns status 401, THEN THE client SHALL clear the stored token following the
   existing shared request behavior.
