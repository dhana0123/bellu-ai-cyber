import { useState } from 'react'
import { client } from '@/lib/client'
import { useAsync } from '@/useAsync'
import { Spinner } from '@/components/ui/spinner'
import { PreviewBadge } from '@/components/common/PreviewBadge'
import { FilterBar } from './components/FilterBar'
import { TopExploitableComponents, TopVulnerableResources } from './components/AnalyticsCards'
import { FindingsTable } from './components/FindingsTable'
import type { VulnerabilitiesData } from '@/types/vulnerabilities'

// Vulnerabilities (Findings inventory). DEMO data via the mock client. Layout:
//   Row 1 — title + total count
//   Row 2 — filter bar + Group By
//   Row 3 — 1:1 analytics split (Top Components | Top Resources)
//   Row 4 — findings inventory table
export default function Vulnerabilities() {
  const { loading, error, data } = useAsync<VulnerabilitiesData>(
    () => client.vulnerabilities.overview(),
    [],
  )
  const [groupBy, setGroupBy] = useState('__none__')

  if (loading) {
    return (
      <div className="flex h-40 items-center justify-center">
        <Spinner className="h-6 w-6" />
      </div>
    )
  }
  if (error) return <p className="text-sm text-destructive">Failed to load vulnerabilities: {error}</p>
  if (!data) return null

  return (
    <div className="space-y-5">
      {/* Row 1 — title + count */}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <h1 className="text-2xl font-semibold tracking-tight">Vulnerabilities</h1>
          <PreviewBadge />
        </div>
        <span className="rounded-md bg-muted px-2.5 py-1 text-sm font-medium tabular-nums text-muted-foreground">
          {data.total.toLocaleString()} vulnerabilities
        </span>
      </div>

      {/* Row 2 — filters + group by */}
      <FilterBar groupBy={groupBy} onGroupBy={setGroupBy} />

      {/* Row 3 — 1:1 analytics split */}
      <div className="grid gap-4 lg:grid-cols-2">
        <TopExploitableComponents items={data.top_components} />
        <TopVulnerableResources items={data.top_resources} />
      </div>

      {/* Row 4 — findings inventory */}
      <FindingsTable findings={data.findings} />
    </div>
  )
}
