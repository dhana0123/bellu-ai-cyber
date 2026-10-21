// src/features/graph-explorer/components/GraphTableView.tsx
//
// GraphTableView is the accessible, data-equivalent counterpart to the pixi.js
// canvas. It presents the same filtered node and edge working set as two tables
// behind a Nodes/Edges tab switch, so screen-reader and keyboard users reach
// every node and edge the canvas renders. Selecting a node row reports that
// node's id through `onSelect`; the selected row is visually indicated and
// marked `aria-selected`.
//
// Requirements: 6.1 (nodes table), 6.2 (edges table), 6.4 (row selection
// reports the node id).

import * as React from 'react'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'
import type { GraphEdge, GraphNode } from '@/types/graphExplorer'

export interface GraphTableViewProps {
  nodes: GraphNode[]
  edges: GraphEdge[]
  selectedId: string | null
  onSelect: (id: string | null) => void
}

// Shared cell styling for the native-table-as-grid layout. We use role="grid"
// on the table so that `aria-selected` on rows is a valid attribute (it is not
// permitted on plain `role="row"` rows outside a grid/treegrid/table-with-
// selection context), which keeps the markup free of accessibility violations.
const headCellClass =
  'h-10 px-3 text-left align-middle text-xs font-medium text-muted-foreground'
const cellClass = 'p-3 align-middle text-sm'

function NodesTable({
  nodes,
  selectedId,
  onSelect,
}: {
  nodes: GraphNode[]
  selectedId: string | null
  onSelect: (id: string | null) => void
}): JSX.Element {
  const handleSelect = React.useCallback(
    (id: string) => {
      // Toggle: selecting the already-selected row clears the selection so the
      // table can both set and unset the current node.
      onSelect(selectedId === id ? null : id)
    },
    [onSelect, selectedId],
  )

  const handleKeyDown = React.useCallback(
    (event: React.KeyboardEvent<HTMLTableRowElement>, id: string) => {
      if (event.key === 'Enter' || event.key === ' ' || event.key === 'Spacebar') {
        event.preventDefault()
        handleSelect(id)
      }
    },
    [handleSelect],
  )

  return (
    <div className="relative w-full overflow-auto">
      <table role="grid" aria-label="Graph nodes" className="w-full caption-bottom text-sm">
        <thead className="[&_tr]:border-b">
          <tr>
            <th scope="col" className={headCellClass}>
              Name
            </th>
            <th scope="col" className={headCellClass}>
              Kind
            </th>
            <th scope="col" className={headCellClass}>
              Provider
            </th>
            <th scope="col" className={headCellClass}>
              Category
            </th>
            <th scope="col" className={headCellClass}>
              Stale
            </th>
          </tr>
        </thead>
        <tbody className="[&_tr:last-child]:border-0">
          {nodes.length === 0 ? (
            <tr>
              <td className={cn(cellClass, 'text-muted-foreground')} colSpan={5}>
                No nodes match the current filter.
              </td>
            </tr>
          ) : (
            nodes.map((node) => {
              const selected = node.id === selectedId
              return (
                <tr
                  key={node.id}
                  aria-selected={selected}
                  tabIndex={0}
                  onClick={() => handleSelect(node.id)}
                  onKeyDown={(event) => handleKeyDown(event, node.id)}
                  className={cn(
                    'cursor-pointer border-b outline-none transition-colors hover:bg-muted/50',
                    'focus-visible:ring-2 focus-visible:ring-ring',
                    selected && 'bg-muted',
                  )}
                >
                  <td className={cn(cellClass, 'font-medium')}>{node.name}</td>
                  <td className={cellClass}>{node.kind}</td>
                  <td className={cellClass}>{node.provider}</td>
                  <td className={cellClass}>{node.category}</td>
                  <td className={cellClass}>
                    {node.stale ? (
                      <Badge variant="warning">Stale</Badge>
                    ) : (
                      <Badge variant="success">Fresh</Badge>
                    )}
                  </td>
                </tr>
              )
            })
          )}
        </tbody>
      </table>
    </div>
  )
}

function EdgesTable({ edges }: { edges: GraphEdge[] }): JSX.Element {
  return (
    <div className="relative w-full overflow-auto">
      <table aria-label="Graph edges" className="w-full caption-bottom text-sm">
        <thead className="[&_tr]:border-b">
          <tr>
            <th scope="col" className={headCellClass}>
              Relation
            </th>
            <th scope="col" className={headCellClass}>
              Source
            </th>
            <th scope="col" className={headCellClass}>
              Target
            </th>
            <th scope="col" className={headCellClass}>
              Confidence
            </th>
          </tr>
        </thead>
        <tbody className="[&_tr:last-child]:border-0">
          {edges.length === 0 ? (
            <tr>
              <td className={cn(cellClass, 'text-muted-foreground')} colSpan={4}>
                No edges match the current filter.
              </td>
            </tr>
          ) : (
            edges.map((edge) => (
              <tr key={edge.id} className="border-b transition-colors hover:bg-muted/50">
                <td className={cn(cellClass, 'font-medium')}>{edge.relation}</td>
                <td className={cellClass}>{edge.source}</td>
                <td className={cellClass}>{edge.target}</td>
                <td className={cellClass}>{edge.confidence.toFixed(2)}</td>
              </tr>
            ))
          )}
        </tbody>
      </table>
    </div>
  )
}

/**
 * Accessible table equivalent of the graph canvas. Renders the filtered node
 * set and, behind an Edges tab, the filtered edge set. Node rows are keyboard
 * operable (focusable, Enter/Space to select) and report the selected node id
 * through `onSelect`.
 */
export function GraphTableView({
  nodes,
  edges,
  selectedId,
  onSelect,
}: GraphTableViewProps): JSX.Element {
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <Tabs defaultValue="nodes" className="flex min-h-0 flex-1 flex-col">
        <TabsList className="m-3 self-start">
          <TabsTrigger value="nodes">Nodes ({nodes.length})</TabsTrigger>
          <TabsTrigger value="edges">Edges ({edges.length})</TabsTrigger>
        </TabsList>
        <TabsContent value="nodes" className="mt-0 min-h-0 flex-1 overflow-auto">
          <NodesTable nodes={nodes} selectedId={selectedId} onSelect={onSelect} />
        </TabsContent>
        <TabsContent value="edges" className="mt-0 min-h-0 flex-1 overflow-auto">
          <EdgesTable edges={edges} />
        </TabsContent>
      </Tabs>
    </div>
  )
}

export default GraphTableView
