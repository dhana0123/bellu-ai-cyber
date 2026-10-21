// src/features/graph-explorer/lib/applyFilter.ts

import type { GraphEdge, GraphFilter, GraphNode } from '@/types/graphExplorer'

/**
 * Filters the fetched working set down to the nodes and edges that satisfy the
 * client-held {@link GraphFilter}.
 *
 * Semantics (see design.md "Client-side filtering"):
 * - An empty facet array imposes no constraint on that facet.
 * - A node is kept iff it satisfies every *active* (non-empty) facet (AND).
 * - An edge is kept iff both of its endpoints are kept.
 * - `filter.typeSearch` does NOT filter the graph; it only narrows the Node
 *   Types list in the panel elsewhere.
 *
 * The returned arrays are subsets of the inputs: the same node/edge object
 * references are reused (no new objects are synthesized), preserving identity
 * for memoized pixi sprites.
 */
export function applyFilter(
  nodes: GraphNode[],
  edges: GraphEdge[],
  filter: GraphFilter,
): { nodes: GraphNode[]; edges: GraphEdge[] } {
  const included = new Set<string>()

  for (const n of nodes) {
    let keep = true
    if (filter.categories.length > 0 && !filter.categories.includes(n.category)) {
      keep = false
    }
    if (filter.providers.length > 0 && !filter.providers.includes(n.provider)) {
      keep = false
    }
    if (filter.kinds.length > 0 && !filter.kinds.includes(n.kind)) {
      keep = false
    }
    if (keep) {
      included.add(n.id)
    }
  }

  const outNodes = nodes.filter((n) => included.has(n.id))
  const outEdges = edges.filter(
    (e) => included.has(e.source) && included.has(e.target),
  )

  return { nodes: outNodes, edges: outEdges }
}
