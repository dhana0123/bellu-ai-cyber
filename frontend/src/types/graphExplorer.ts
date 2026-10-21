// TypeScript shapes for the Graph Explorer page. These mirror the server-side
// GraphExplorerResponse payload and the client-held filter/view state.

/** A single node in the graph working set. */
export interface GraphNode {
  id: string            // canonical_id
  kind: string          // entity kind -> Node Type facet
  provider: string      // provider -> Integration facet
  category: string      // derived -> Category facet
  name: string
  stale: boolean
  // layout fields are mutated by the force sim, kept out of equality checks
  x?: number
  y?: number
}

/** A directed relationship between two nodes. */
export interface GraphEdge {
  id: string
  source: string        // src canonical_id
  target: string        // dst canonical_id
  relation: string      // e.g. RUNS_IN, CAN_READ
  confidence: number
}

/** A single bucket in a facet, with its display label and matching count. */
export interface FacetCount {
  key: string
  label: string
  count: number
}

/** Server-authoritative facet buckets for the current working set. */
export interface GraphFacets {
  categories: FacetCount[]
  providers: FacetCount[]
  kinds: FacetCount[]
}

/** Client-held filter state driving the working set query. */
export interface GraphFilter {
  view: string              // view key, e.g. 'all'
  categories: string[]      // selected category keys (empty = all)
  providers: string[]
  kinds: string[]
  typeSearch: string        // free-text narrowing of the Node Types list
}

/** A saved (or built-in) named filter preset. */
export interface SavedView {
  id: string
  name: string
  description: string
  filter: GraphFilter
  builtin: boolean
}

/** The full Graph Explorer payload returned by the server. */
export interface GraphExplorerResponse {
  nodes: GraphNode[]
  edges: GraphEdge[]
  facets: GraphFacets
  nodeCount: number    // total matching server-side (may exceed returned nodes)
  edgeCount: number
  capped: boolean      // true when the working set hit the server cap
  graphAvailable: boolean
}
