// src/features/graph-explorer/components/NodeDetailPanel.tsx
//
// NodeDetailPanel is the slide-over shown when a node is selected on the canvas
// or in the table. It presents the selected node's identity (name, kind,
// provider, canonical_id, and stale flag), an Attributes section derived from
// the fields the GraphNode carries, an Evidence / sources section, and the
// node's incoming/outgoing neighbor edges. Activating a neighbor reports the
// other endpoint's id through `onFocusNeighbor`; closing the panel clears the
// selection through `onClose`.
//
// The panel is a slide-over drawer built on the shared Sheet primitive
// (@radix-ui/react-dialog under the hood), which gives it a labelled dialog,
// focus trapping, Escape-to-close, and a close affordance for free.
//
// SECURITY (Requirement 7.7): the node name and every attribute value are
// rendered as plain text through JSX children. React escapes these by default;
// this component never uses dangerouslySetInnerHTML, so untrusted entity data
// can never be interpreted as markup.
//
// Requirements: 7.1 (kind/provider/name/canonical_id), 7.2 (attributes),
// 7.3 (evidence / source refs), 7.4 (incoming + outgoing neighbors),
// 7.5 (activating a neighbor reports its id), 7.6 (close clears selection),
// 7.7 (values rendered as text, never interpreted markup).

import * as React from 'react'

import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'
import type { GraphEdge, GraphNode } from '@/types/graphExplorer'

export interface NodeDetailPanelProps {
  node: GraphNode | null
  neighbors: GraphEdge[]
  onClose: () => void
  onFocusNeighbor: (id: string) => void
}

/** One label/value row in the Attributes section. Values render as plain text. */
function AttributeRow({ label, value }: { label: string; value: React.ReactNode }): JSX.Element {
  return (
    <div className="grid grid-cols-[8rem_minmax(0,1fr)] gap-2 py-1 text-sm">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="break-words font-medium text-foreground">{value}</dd>
    </div>
  )
}

/**
 * Slide-over detail panel for a selected graph node. Renders nothing while
 * `node` is null (closed state); when a node is set, it opens a right-side
 * drawer presenting identity, attributes, evidence, and neighbor edges.
 */
export function NodeDetailPanel({
  node,
  neighbors,
  onClose,
  onFocusNeighbor,
}: NodeDetailPanelProps): JSX.Element | null {
  // The sheet is open exactly when a node is selected. Any close affordance
  // (the X button, Escape, or an overlay click) flips `open` to false, which we
  // forward to `onClose` so the parent clears the selection (Requirement 7.6).
  const open = node !== null

  const handleOpenChange = React.useCallback(
    (next: boolean) => {
      if (!next) onClose()
    },
    [onClose],
  )

  // Nothing to render until a node is selected. Returning null keeps the panel
  // fully unmounted in its closed state.
  if (!node) return null

  // Partition neighbor edges relative to the selected node: an edge is outgoing
  // when it originates at this node (source), incoming when it terminates here
  // (target). For each, the "other endpoint" is the neighbor id we focus on
  // activation (Requirement 7.4, 7.5).
  const outgoing = neighbors.filter((edge) => edge.source === node.id)
  const incoming = neighbors.filter((edge) => edge.target === node.id)

  return (
    <Sheet open={open} onOpenChange={handleOpenChange}>
      <SheetContent
        side="right"
        className="flex w-full flex-col gap-0 overflow-y-auto sm:max-w-md"
        aria-label="Node detail"
      >
        <SheetHeader className="pr-8 text-left">
          {/* Name and all identity values render as escaped text (7.1, 7.7). */}
          <SheetTitle className="break-words">{node.name}</SheetTitle>
          <SheetDescription className="flex flex-wrap items-center gap-2">
            <Badge variant="secondary">{node.kind}</Badge>
            <Badge variant="outline">{node.provider}</Badge>
            {node.stale ? (
              <Badge variant="warning">Stale</Badge>
            ) : (
              <Badge variant="success">Fresh</Badge>
            )}
          </SheetDescription>
        </SheetHeader>

        {/* Attributes — the identity and classification fields the node carries.
            canonical_id is the node id (7.1). The shared GraphNode type does not
            carry a free-form attributes map, so we surface the available fields. */}
        <section className="mt-6" aria-labelledby="node-detail-attributes">
          <h3
            id="node-detail-attributes"
            className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground"
          >
            Attributes
          </h3>
          <dl className="divide-y divide-border">
            <AttributeRow label="Name" value={node.name} />
            <AttributeRow label="Kind" value={node.kind} />
            <AttributeRow label="Provider" value={node.provider} />
            <AttributeRow label="Category" value={node.category} />
            <AttributeRow label="Canonical ID" value={node.id} />
            <AttributeRow label="Stale" value={node.stale ? 'Yes' : 'No'} />
          </dl>
        </section>

        {/* Evidence / sources (7.3). The GraphNode type does not yet carry
            evidence or source references, so we degrade gracefully with an
            explicit empty state rather than omitting the section. */}
        <section className="mt-6" aria-labelledby="node-detail-evidence">
          <h3
            id="node-detail-evidence"
            className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground"
          >
            Evidence / sources
          </h3>
          <p className="text-sm text-muted-foreground">No evidence references available.</p>
        </section>

        {/* Neighbors (7.4). Each row is a button that focuses the other endpoint
            (7.5). Relation and endpoint ids render as escaped text (7.7). */}
        <section className="mt-6" aria-labelledby="node-detail-neighbors">
          <h3
            id="node-detail-neighbors"
            className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground"
          >
            Neighbors
          </h3>

          <NeighborGroup
            title="Outgoing"
            emptyLabel="No outgoing edges."
            edges={outgoing}
            endpointOf={(edge) => edge.target}
            onFocusNeighbor={onFocusNeighbor}
          />
          <NeighborGroup
            title="Incoming"
            emptyLabel="No incoming edges."
            edges={incoming}
            endpointOf={(edge) => edge.source}
            onFocusNeighbor={onFocusNeighbor}
            className="mt-4"
          />
        </section>
      </SheetContent>
    </Sheet>
  )
}

/** A labelled group of neighbor-edge buttons (incoming or outgoing). */
function NeighborGroup({
  title,
  emptyLabel,
  edges,
  endpointOf,
  onFocusNeighbor,
  className,
}: {
  title: string
  emptyLabel: string
  edges: GraphEdge[]
  endpointOf: (edge: GraphEdge) => string
  onFocusNeighbor: (id: string) => void
  className?: string
}): JSX.Element {
  return (
    <div className={className}>
      <h4 className="mb-1 text-xs font-medium text-muted-foreground">{title}</h4>
      {edges.length === 0 ? (
        <p className="text-sm text-muted-foreground">{emptyLabel}</p>
      ) : (
        <ul className="flex flex-col gap-1">
          {edges.map((edge) => {
            const neighborId = endpointOf(edge)
            return (
              <li key={edge.id}>
                <button
                  type="button"
                  onClick={() => onFocusNeighbor(neighborId)}
                  className={cn(
                    'flex w-full items-center justify-between gap-2 rounded-md border border-border px-3 py-2 text-left text-sm',
                    'transition-colors hover:bg-muted/50',
                    'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring',
                  )}
                >
                  <span className="min-w-0 break-words font-medium">{neighborId}</span>
                  <Badge variant="outline" className="shrink-0">
                    {edge.relation}
                  </Badge>
                </button>
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}

export default NodeDetailPanel
