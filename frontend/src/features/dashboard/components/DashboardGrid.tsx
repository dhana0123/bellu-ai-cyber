import { Responsive, WidthProvider, type Layout } from 'react-grid-layout'
import { Plus, RotateCcw } from 'lucide-react'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { AdvisoryBanner } from './AdvisoryBanner'
import { OpenIssuesCard } from './OpenIssuesCard'
import { SeverityTrendCard } from './SeverityTrendCard'
import { IssueAgeCard } from './IssueAgeCard'
import { SecurityScoreCard } from './SecurityScoreCard'
import { TopIssuesCard } from './TopIssuesCard'
import { OpenedResolvedCard } from './OpenedResolvedCard'
import { DashboardCard } from './DashboardCard'
import { CARD_IDS, GRID_COLS, ROW_HEIGHT, useGridLayout } from '../useGridLayout'
import type { DashboardData } from '@/types/dashboard'
import '../grid.css'

// WidthProvider measures the container and feeds a pixel width to the grid so it
// stays fluid/responsive without a manual width prop.
const ResponsiveGridLayout = WidthProvider(Responsive)

// The AWS Console Home-style, permanently-editable grid view of the dashboard.
// Extracted from the page so it can live behind the "Grid" tab alongside "Map".
export function DashboardGrid({ data }: { data: DashboardData }) {
  const { layout, onLayoutChange, resetLayout } = useGridLayout()

  const handleLayoutChange = (next: Layout[]) => onLayoutChange(next)

  const handleAddWidgets = () => {
    toast('Add widgets', {
      description: 'Widget catalog coming soon — pick metrics, charts, or tables to add.',
    })
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-sm text-muted-foreground">
          Drag a card by its header handle to move it; drag the bottom-right corner to resize.
        </p>
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
