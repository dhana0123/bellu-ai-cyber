import { useMemo, useState } from 'react'
import { Responsive, WidthProvider } from 'react-grid-layout'
import { RotateCcw } from 'lucide-react'
import { client } from '@/lib/client'
import { useAsync } from '@/useAsync'
import { Spinner } from '@/components/ui/spinner'
import { Button } from '@/components/ui/button'
import { PreviewBadge } from '@/components/common/PreviewBadge'
import { DashboardCard } from '@/features/dashboard/components/DashboardCard'
import { NavBar } from './components/NavBar'
import { HeaderActions } from './components/HeaderActions'
import { OnboardingCarousel } from './components/OnboardingCarousel'
import { FilterPanel } from './components/FilterPanel'
import { IssueSeverityCard } from './components/IssueSeverityCard'
import { OpenResolvedTrendCard } from './components/OpenResolvedTrendCard'
import { OpenIssuesTrendCard } from './components/OpenIssuesTrendCard'
import { VulnerabilityGrid } from './components/VulnerabilityGrid'
import {
  DEFAULT_ISSUES_LAYOUT,
  GRID_COLS,
  ISSUE_CARD_IDS,
  ROW_HEIGHT,
  useIssuesLayout,
} from './useIssuesLayout'
import type { IssuesData } from '@/types/issues'
import '@/features/dashboard/grid.css'

// WidthProvider measures the container and feeds a pixel width to the grid so
// the analytics row stays fluid/responsive.
const ResponsiveGridLayout = WidthProvider(Responsive)

// Risk Issues page — a self-contained Risk & Vulnerability analysis view backed
// by DEMO data (src/mocks/issues.ts). Layer-1 filters (search + the Status chip)
// are bound to shared state so they filter the grid and the issue count together.
const RULE_CHIP = 'Externally validated Ne…'

export default function Issues() {
  const { loading, error, data } = useAsync<IssuesData>(() => client.issues.overview(), [])

  // Draggable/resizable analytics row (same AWS-style grid as the Dashboard).
  const { layout, onLayoutChange, resetLayout } = useIssuesLayout()

  // Shared filter state.
  const [search, setSearch] = useState('')
  // The spec's active chip reads "Status equals Open or In Progress".
  const [statusChip, setStatusChip] = useState('Open or In Progress')
  const [groupBy, setGroupBy] = useState('None')
  const [selected, setSelected] = useState<Set<string>>(new Set())

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    return (data?.issues ?? []).filter((it) => {
      // Status chip: when set, keep only Open / In Progress.
      if (statusChip && it.status === 'resolved') return false
      if (!q) return true
      return (
        it.title.toLowerCase().includes(q) ||
        it.resource.toLowerCase().includes(q) ||
        it.subscription.toLowerCase().includes(q)
      )
    })
  }, [data, search, statusChip])

  const toggle = (id: string) =>
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })

  const toggleAll = (checked: boolean) =>
    setSelected(checked ? new Set(filtered.map((i) => i.id)) : new Set())

  const resetFilters = () => {
    setSearch('')
    setStatusChip('')
    setGroupBy('None')
  }

  if (loading) {
    return (
      <div className="flex h-40 items-center justify-center">
        <Spinner className="h-6 w-6" />
      </div>
    )
  }
  if (error) return <p className="text-sm text-destructive">Failed to load issues: {error}</p>
  if (!data) return null

  return (
    <div className="space-y-4">
      <NavBar />

      <div className="flex items-center gap-2">
        <div className="flex-1">
          <HeaderActions />
        </div>
      </div>
      <div className="-mt-2 flex items-center gap-2">
        <PreviewBadge />
      </div>

      <OnboardingCarousel />

      <FilterPanel
        search={search}
        onSearch={setSearch}
        statusChip={statusChip}
        ruleChip={RULE_CHIP}
        onClearStatus={() => setStatusChip('')}
        onReset={resetFilters}
        issueCount={filtered.length}
        groupBy={groupBy}
        onGroupBy={setGroupBy}
      />

      {/* Row 5 — analytics (draggable + resizable grid) */}
      <div className="flex items-center justify-between">
        <span className="text-xs text-muted-foreground">
          Drag a card by its header handle to move it; drag the bottom-right corner to resize.
        </span>
        <Button
          variant="outline"
          size="sm"
          onClick={resetLayout}
          disabled={JSON.stringify(layout) === JSON.stringify(DEFAULT_ISSUES_LAYOUT)}
        >
          <RotateCcw className="h-4 w-4" /> Reset layout
        </Button>
      </div>
      <ResponsiveGridLayout
        className="layout"
        layouts={{ lg: layout, md: layout, sm: layout }}
        breakpoints={{ lg: 1200, md: 996, sm: 768, xs: 480, xxs: 0 }}
        cols={{ lg: GRID_COLS, md: GRID_COLS, sm: 6, xs: 4, xxs: 2 }}
        rowHeight={ROW_HEIGHT}
        margin={[16, 16]}
        containerPadding={[0, 0]}
        isDraggable
        isResizable
        draggableHandle=".dash-drag-handle"
        compactType="vertical"
        onLayoutChange={onLayoutChange}
        onDragStop={onLayoutChange}
        onResizeStop={onLayoutChange}
        resizeHandles={['se']}
        useCSSTransforms
      >
        <div key={ISSUE_CARD_IDS.severity}>
          <DashboardCard title="Issue Severity">
            <IssueSeverityCard metrics={data.severity_metrics} />
          </DashboardCard>
        </div>
        <div key={ISSUE_CARD_IDS.openResolved}>
          <DashboardCard title="Open and Resolved Issues">
            <OpenResolvedTrendCard data={data.open_resolved} />
          </DashboardCard>
        </div>
        <div key={ISSUE_CARD_IDS.openIssues}>
          <DashboardCard title="Open Issues">
            <OpenIssuesTrendCard
              data={data.open_issues_trend}
              trendPct={data.open_issues_trend_pct}
            />
          </DashboardCard>
        </div>
      </ResponsiveGridLayout>

      {/* Row 6 — vulnerability grid */}
      <VulnerabilityGrid
        issues={filtered}
        selected={selected}
        onToggle={toggle}
        onToggleAll={toggleAll}
      />
    </div>
  )
}
