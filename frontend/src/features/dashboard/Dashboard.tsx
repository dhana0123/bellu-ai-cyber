import { client } from '@/lib/client'
import { useAsync } from '@/useAsync'
import { Spinner } from '@/components/ui/spinner'
import { PreviewBadge } from '@/components/common/PreviewBadge'
import { HeaderSlot } from '@/components/common/AppShell'
import { DashboardGrid } from './components/DashboardGrid'
import type { DashboardData } from '@/types/dashboard'

// Cybersecurity Dashboard — the AWS Console Home-style draggable/resizable card
// grid. The title + Preview badge render in the app header via HeaderSlot.
export default function Dashboard() {
  const { loading, error, data } = useAsync<DashboardData>(() => client.dashboard.overview(), [])

  const header = (
    <div className="flex items-center gap-2">
      <h1 className="text-lg font-semibold tracking-tight">Dashboard</h1>
      <PreviewBadge />
    </div>
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
      <DashboardGrid data={data} />
    </>
  )
}
