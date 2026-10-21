// src/features/graph-explorer/GraphExplorer.tsx
//
// GraphExplorer is the top-level page composed into the /explorer route. It
// orchestrates the data fetch (via useGraphExplorer), filter state, and view
// mode, laying out the left FilterPanel, the TopBar, and either the pixi.js
// GraphCanvas or the accessible GraphTableView by viewMode, with the
// selection-driven NodeDetailPanel wired to the current selection.
//
// It renders the loading, error, and graph-unavailable (degraded) states, and
// surfaces a capped hint when the server working set hit its cap so counts and
// rendering stay honest (Requirement 9.4). The DegradedBanner (Requirement
// 11.4) appears when the live graph service is unavailable.
//
// Requirements: 5.2, 5.3 (Graph/Table view switch), 9.4 (capped hint),
// 11.4 (degraded banner).

import { useState } from 'react'

import { Spinner } from '@/components/ui/spinner'

import { HeaderSlot } from '@/components/common/AppShell'

import { DegradedBanner } from './components/DegradedBanner'
import { FilterPanel } from './components/FilterPanel'
import { GraphCanvas } from './components/GraphCanvas'
import { GraphTableView } from './components/GraphTableView'
import { NodeDetailPanel } from './components/NodeDetailPanel'
import { TopBar } from './components/TopBar'
import type { TimeRange } from './lib/timeRange'
import { useGraphExplorer } from './hooks/useGraphExplorer'
import { colorForCategory } from './lib/categories'

export default function GraphExplorer(): JSX.Element {
  const g = useGraphExplorer()
  const [viewMode, setViewMode] = useState<'graph' | 'table'>('graph')
  const [showQuery, setShowQuery] = useState(false)
  const [timeRange, setTimeRange] = useState<TimeRange>('all')

  // Loading: show a centered spinner while the working set is fetched, matching
  // the Dashboard page's loading treatment.
  if (g.loading) {
    return (
      <div className="flex h-full items-center justify-center">
        <Spinner className="h-6 w-6" />
      </div>
    )
  }

  // Error: surface an inline, non-blocking error message.
  if (g.error) {
    return (
      <p className="p-4 text-sm text-destructive">
        Failed to load graph explorer: {g.error}
      </p>
    )
  }

  // The node currently selected, resolved against the visible working set so the
  // detail panel only ever shows a node that is actually rendered.
  const selectedNode = g.visibleNodes.find((n) => n.id === g.selectedId) ?? null
  // Edges touching the selected node, in either direction, for the neighbor list.
  const selectedNeighbors = g.visibleEdges.filter(
    (e) => e.source === g.selectedId || e.target === g.selectedId,
  )

  return (
    // Fill the available height exactly (AppShell main is flex-1, i.e. viewport
    // minus header). `min-h-0` lets the flex children shrink so the left filter
    // panel can scroll internally instead of the page growing past the viewport.
    <div className="flex h-full min-h-0 overflow-hidden">
      <FilterPanel
        facets={g.facets}
        filter={g.filter}
        // setFilter takes Partial<GraphFilter>; FilterPanel emits a full
        // GraphFilter, which is a valid Partial.
        onFilterChange={(next) => g.setFilter(next)}
        views={g.views}
        activeViewId={g.activeViewId}
        onSelectView={g.selectView}
        onSaveView={g.saveView}
      />

      {/* The explorer controls live in the shared app header via HeaderSlot. */}
      <HeaderSlot>
        <TopBar
          viewMode={viewMode}
          onViewModeChange={setViewMode}
          nodeCount={g.visibleNodeCount}
          edgeCount={g.visibleEdgeCount}
          showQuery={showQuery}
          onToggleQuery={setShowQuery}
          timeRange={timeRange}
          onTimeRangeChange={setTimeRange}
        />
      </HeaderSlot>

      <div className="flex min-w-0 flex-1 flex-col">
        {/* Generated query text, revealed when the header toggle is enabled. */}
        {showQuery && (
          <div role="region" aria-label="Generated query" className="border-b border-border p-3">
            <pre className="overflow-x-auto rounded-md bg-muted px-3 py-2 text-xs text-muted-foreground">
              <code>{`${g.visibleNodeCount} nodes, ${g.visibleEdgeCount} edges`}</code>
            </pre>
          </div>
        )}

        {/* Degraded banner when the live graph service is unavailable (11.4). */}
        {!g.graphAvailable && (
          <DegradedBanner
            className="m-3"
            message="Live graph service is unavailable; showing stored graph data. Table view is fully available."
          />
        )}

        {/* Capped hint when the server working set hit its cap (9.4): the badge
            counts are authoritative totals, so we tell the user the canvas/table
            shows a subset and that refining filters will narrow it. */}
        {g.capped && (
          <p className="mx-3 mt-3 text-sm text-muted-foreground" role="status">
            Showing {g.visibleNodes.length.toLocaleString()} of{' '}
            {g.visibleNodeCount.toLocaleString()} — refine filters to narrow the set.
          </p>
        )}

        {viewMode === 'graph' ? (
          <GraphCanvas
            nodes={g.visibleNodes}
            edges={g.visibleEdges}
            selectedId={g.selectedId}
            onSelect={g.select}
            colorForCategory={colorForCategory}
            onSwitchToTable={() => setViewMode('table')}
          />
        ) : (
          <GraphTableView
            nodes={g.visibleNodes}
            edges={g.visibleEdges}
            selectedId={g.selectedId}
            onSelect={g.select}
          />
        )}
      </div>

      <NodeDetailPanel
        node={selectedNode}
        neighbors={selectedNeighbors}
        onClose={() => g.select(null)}
        onFocusNeighbor={g.select}
      />
    </div>
  )
}
