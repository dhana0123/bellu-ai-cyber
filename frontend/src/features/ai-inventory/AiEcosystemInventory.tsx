import { useMemo, useState } from 'react'
import { Responsive, WidthProvider } from 'react-grid-layout'
import { RotateCcw } from 'lucide-react'
import { useProject } from '@/project'
import { useAsync } from '@/useAsync'
import { Spinner } from '@/components/ui/spinner'
import { Button } from '@/components/ui/button'
import { HeaderSlot } from '@/components/common/AppShell'
import { fetchInventory } from './realInventory'
import { DashboardCard } from '@/features/dashboard/components/DashboardCard'
import { SearchFilterBar } from './components/SearchFilterBar'
import {
  BreakdownByCategory,
  BreakdownBySubcategory,
  BreakdownByType,
} from './components/BreakdownCards'
import { InventoryTable } from './components/InventoryTable'
import {
  DEFAULT_INV_LAYOUT,
  GRID_COLS,
  INV_CARD_IDS,
  ROW_HEIGHT,
  useInventoryLayout,
} from './useInventoryLayout'
import type { AiInventoryData } from '@/types/aiInventory'
import '@/features/dashboard/grid.css'

// WidthProvider feeds a measured pixel width to the grid so it stays fluid.
const ResponsiveGridLayout = WidthProvider(Responsive)

// AI Ecosystem Inventory. Real discovered inventory (GET /api/entities), mapped
// to this page's data shape by ./realInventory, scoped to the active project.
// Layout unchanged:
//   Row 1 — search + filter pills + meta count
//   Row 2 — 3-column breakdown grid (Category | Subcategory | Type donut)
//   Row 3 — technology inventory table
export default function AiEcosystemInventory() {
  const { current } = useProject()
  const projectId = current?.id
  const { loading, error, data } = useAsync<AiInventoryData>(
    () => fetchInventory(projectId),
    [projectId],
  )
  const [search, setSearch] = useState('')
  const [detectedOnly, setDetectedOnly] = useState(true)
  const { layout, onLayoutChange, resetLayout } = useInventoryLayout()

  const header = (
    <div className="flex items-center gap-2">
      <h1 className="text-lg font-semibold tracking-tight">Inventory</h1>
      {current ? <span className="text-xs text-muted-foreground">· {current.name}</span> : null}
    </div>
  )

  const assets = useMemo(() => {
    const q = search.trim().toLowerCase()
    if (!q) return data?.assets ?? []
    return (data?.assets ?? []).filter(
      (a) =>
        a.name.toLowerCase().includes(q) ||
        a.subtitle.toLowerCase().includes(q) ||
        a.type.toLowerCase().includes(q),
    )
  }, [data, search])

  if (loading) {
    return (
      <>
        <HeaderSlot>{header}</HeaderSlot>
        <div className="flex h-40 items-center justify-center">
          <Spinner className="h-6 w-6" />
        </div>
      </>
    )
  }
  if (error)
    return (
      <>
        <HeaderSlot>{header}</HeaderSlot>
        <p className="text-sm text-destructive">Failed to load inventory: {error}</p>
      </>
    )
  if (!data) return <HeaderSlot>{header}</HeaderSlot>

  return (
    <>
      <HeaderSlot>{header}</HeaderSlot>
      <div className="space-y-4">
        <SearchFilterBar
          search={search}
          onSearch={setSearch}
          detectedOnly={detectedOnly}
          onToggleDetected={setDetectedOnly}
          total={data.total}
        />

        {/* Draggable + resizable grid (same AWS-style grid as the Dashboard). */}
        <div className="flex items-center justify-between">
          <span className="text-xs text-muted-foreground">
            Drag a card by its header handle to move it; drag the bottom-right corner to resize.
          </span>
          <Button
            variant="outline"
            size="sm"
            onClick={resetLayout}
            disabled={JSON.stringify(layout) === JSON.stringify(DEFAULT_INV_LAYOUT)}
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
          <div key={INV_CARD_IDS.category}>
            <DashboardCard title="Breakdown by Category">
              <BreakdownByCategory rows={data.by_category} />
            </DashboardCard>
          </div>
          <div key={INV_CARD_IDS.subcategory}>
            <DashboardCard title="Breakdown by Subcategory">
              <BreakdownBySubcategory rows={data.by_subcategory} />
            </DashboardCard>
          </div>
          <div key={INV_CARD_IDS.type}>
            <DashboardCard title="Breakdown by Type">
              <BreakdownByType slices={data.by_type} total={data.total} />
            </DashboardCard>
          </div>
          <div key={INV_CARD_IDS.table}>
            <DashboardCard title="AI Technology Inventory">
              <InventoryTable assets={assets} />
            </DashboardCard>
          </div>
        </ResponsiveGridLayout>
      </div>
    </>
  )
}
