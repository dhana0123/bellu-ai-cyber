import { useState } from 'react'
import { LayoutGrid, Share2 } from 'lucide-react'
import { client } from '@/lib/client'
import { useAsync } from '@/useAsync'
import { Spinner } from '@/components/ui/spinner'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { PreviewBadge } from '@/components/common/PreviewBadge'
import { HeaderSlot } from '@/components/common/AppShell'
import { DashboardGrid } from './components/DashboardGrid'
import { TopologyMap } from './components/TopologyMap'
import type { DashboardData } from '@/types/dashboard'

// Cybersecurity Dashboard with two views:
//   • Grid (default) — the AWS Console Home-style draggable/resizable card grid.
//   • Map — a 2D fluid topology canvas (dark) that fills the content area.
// The title + Preview badge + Grid/Map tabs render in the app header via
// HeaderSlot; the tab state lives here so the header controls drive the body.
export default function Dashboard() {
  const { loading, error, data } = useAsync<DashboardData>(() => client.dashboard.overview(), [])
  const [tab, setTab] = useState<'grid' | 'map'>('grid')

  const header = (
    <>
      <div className="flex items-center gap-2">
        <h1 className="text-lg font-semibold tracking-tight">Dashboard</h1>
        <PreviewBadge />
      </div>
      <Tabs value={tab} onValueChange={(v) => setTab(v as 'grid' | 'map')} className="ml-2">
        <TabsList>
          <TabsTrigger value="grid">
            <LayoutGrid className="mr-1.5 h-4 w-4" /> Grid
          </TabsTrigger>
          <TabsTrigger value="map">
            <Share2 className="mr-1.5 h-4 w-4" /> Map
          </TabsTrigger>
        </TabsList>
      </Tabs>
    </>
  )

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
        <p className="text-sm text-destructive">Failed to load dashboard: {error}</p>
      </>
    )
  if (!data) return <HeaderSlot>{header}</HeaderSlot>

  return (
    <>
      <HeaderSlot>{header}</HeaderSlot>

      {tab === 'grid' ? (
        <DashboardGrid data={data} />
      ) : (
        // Full-bleed: cancel <main>'s padding and fill the viewport below the
        // 3.5rem (h-14) header so the canvas takes the full body.
        <div className="-m-4 h-[calc(100vh-3.5rem)] md:-m-6">
          <TopologyMap data={data} />
        </div>
      )}
    </>
  )
}
