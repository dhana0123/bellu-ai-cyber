import { client } from '@/lib/client'
import { useAsync } from '@/useAsync'
import { Card, CardContent } from '@/components/ui/card'
import { Spinner } from '@/components/ui/spinner'
import { BarChartCard } from '@/components/charts'
import type { CategoryDatum } from '@/components/charts'
import type { Stats } from '@/types/foundation'

// Security Overview (AI-SPM foundation). Real API: GET /api/stats. KPI cards +
// shadcn charts for entities-by-kind and edges-by-relation breakdowns.
export default function SecurityOverview() {
  const { loading, error, data } = useAsync<Stats>(() => client.stats(), [])

  if (loading)
    return (
      <div className="flex h-40 items-center justify-center">
        <Spinner className="h-6 w-6" />
      </div>
    )
  if (error) return <p className="text-sm text-destructive">Failed to load stats: {error}</p>
  if (!data) return null

  const cards = [
    { label: 'Raw events', value: data.raw_events },
    { label: 'Entities', value: data.entities },
    { label: 'Edges', value: data.edges },
    { label: 'Collection runs', value: data.runs },
    { label: 'Conflicts', value: data.conflicts },
  ]

  const toData = (obj: Record<string, number>): CategoryDatum[] =>
    Object.entries(obj ?? {})
      .sort((a, b) => b[1] - a[1])
      .map(([label, value]) => ({ label, value }))

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Security Overview</h1>
        <p className="text-sm text-muted-foreground">
          Posture of the AI security graph: assets, relationships, and ingestion health.
        </p>
      </div>

      <div className="grid grid-cols-2 gap-3 md:grid-cols-5">
        {cards.map((c) => (
          <Card key={c.label}>
            <CardContent className="p-5 text-center">
              <div className="text-3xl font-bold text-primary">{c.value}</div>
              <div className="mt-1 text-xs text-muted-foreground">{c.label}</div>
            </CardContent>
          </Card>
        ))}
      </div>

      <div className="grid gap-4 md:grid-cols-2">
        <BarChartCard
          title="Entities by kind"
          description="Canonical graph nodes grouped by kind"
          data={toData(data.entities_by_kind)}
          valueLabel="Entities"
          horizontal
        />
        <BarChartCard
          title="Edges by relation"
          description="Graph relationships grouped by type"
          data={toData(data.edges_by_relation)}
          valueLabel="Edges"
          horizontal
        />
      </div>
    </div>
  )
}
