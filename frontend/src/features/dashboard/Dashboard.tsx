import { Responsive, WidthProvider, type Layout } from 'react-grid-layout'
import { Plus, RotateCcw } from 'lucide-react'
import { toast } from 'sonner'
import { client } from '@/lib/client'
import { useAsync } from '@/useAsync'
import { Spinner } from '@/components/ui/spinner'
import { Button } from '@/components/ui/button'
import { PreviewBadge } from '@/components/common/PreviewBadge'
import { AdvisoryBanner } from './components/AdvisoryBanner'
import { OpenIssuesCard } from './components/OpenIssuesCard'
import { SeverityTrendCard } from './components/SeverityTrendCard'
import { IssueAgeCard } from './components/IssueAgeCard'
import { SecurityScoreCard } from './components/SecurityScoreCard'
import { TopIssuesCard } from './components/TopIssuesCard'
import { OpenedResolvedCard } from './components/OpenedResolvedCard'
import { DashboardCard } from './components/DashboardCard'
import { CARD_IDS, GRID_COLS, ROW_HEIGHT, useGridLayout } from './useGridLayout'
import type { DashboardData } from '@/types/dashboard'
import './grid.css'

// WidthProvider measures the container and feeds a pixel width to the grid so it
// stays fluid/responsive without a manual width prop.
const ResponsiveGridLayout = WidthProvider(Responsive)

// Cybersecurity Dashboard — a permanently-editable, AWS Console Home-style grid.
// Every card can be dragged (via its 6-dot header handle) and resized (via the
// corner grip); items snap to a 12-column grid, push neighbors down on overlap,
// and the layout persists to localStorage. DEMO data via the mock client.
export default function Dashboard() {
  const { loading, error, data } = useAsync<DashboardData>(() => client.dashboard.overview(), [])
  const { layout, onLayoutChange, resetLayout } = useGridLayout()

  if (loading) {
    return (
      <div className="flex h-40 items-center justify-center">
        <Spinner className="h-6 w-6" />
      </div>
    )
  }
  if (error) return <p className="text-sm text-destructive">Failed to load dashboard: {error}</p>
  if (!data) return null

  // Fire the layout-update action on drag/resize release (RGL also calls
  // onLayoutChange during compaction; all paths persist through the hook).
  const handleLayoutChange = (next: Layout[]) => onLayoutChange(next)

  const handleAddWidgets = () => {
    // Placeholder hook for a future widget catalog/picker.
    toast('Add widgets', {
      description: 'Widget catalog coming soon — pick metrics, charts, or tables to add.',
    })
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="flex items-center gap-2">
            <h1 className="text-2xl font-semibold tracking-tight">Dashboard</h1>
            <PreviewBadge />
          </div>
          <p className="text-sm text-muted-foreground">
            Drag a card by its header handle to move it; drag the bottom-right corner to resize.
          </p>
        </div>

        {/* Control bar */}
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={resetLayout}>
            <RotateCcw className="h-4 w-4" /> Reset to default layout
          </Button>
          <Button variant="default" size="sm" onClick={handleAddWidgets}>
            <Plus className="h-4 w-4" /> Add widgets
          </Button>
        </div>
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
        onLayoutChange={handleLayoutChange}
        onDragStop={handleLayoutChange}
        onResizeStop={handleLayoutChange}
        resizeHandles={['se']}
        useCSSTransforms
      >
        <div key={CARD_IDS.advisories}>
          {/* Row 1 banner is locked (static in the layout) — no drag handle. */}
          <div className="h-full [&>*]:h-full">
            <AdvisoryBanner advisories={data.advisories} />
          </div>
        </div>

        <div key={CARD_IDS.openIssues}>
          <DashboardCard title="Open Issues">
            <OpenIssuesCard metrics={data.open_issues} />
          </DashboardCard>
        </div>

        <div key={CARD_IDS.severity}>
          <DashboardCard title="Issues by Severity">
            <SeverityTrendCard trends={data.severity_trends} />
          </DashboardCard>
        </div>

        <div key={CARD_IDS.issueAge}>
          <DashboardCard title="Average Issue Age">
            <IssueAgeCard gauges={data.issue_age} />
          </DashboardCard>
        </div>

        <div key={CARD_IDS.securityScore}>
          <DashboardCard title="Security Score">
            <SecurityScoreCard data={data.security_score} />
          </DashboardCard>
        </div>

        <div key={CARD_IDS.topIssues}>
          <DashboardCard title="Top Issues">
            <TopIssuesCard issues={data.top_issues} />
          </DashboardCard>
        </div>

        <div key={CARD_IDS.openedResolved}>
          <DashboardCard title="Opened and Resolved Issues">
            <OpenedResolvedCard data={data.opened_resolved} />
          </DashboardCard>
        </div>
      </ResponsiveGridLayout>
    </div>
  )
}
