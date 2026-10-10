import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { cn } from '@/lib/utils'
import { CardMenu } from './CardMenu'
import { TrendChip } from './TrendChip'
import { SEVERITY_DOT } from './severity'
import type { SeverityMetric } from '@/types/dashboard'

// Card 1 — Open Issues. A vertical stack of severity metric rows: colored letter
// indicator, count, label, and a trend chip.
export function OpenIssuesCard({ metrics }: { metrics: SeverityMetric[] }) {
  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between space-y-0 p-4 pb-2">
        <CardTitle className="text-base">Open Issues</CardTitle>
        <CardMenu label="Open issues options" />
      </CardHeader>
      <CardContent className="space-y-1 p-4 pt-2">
        {metrics.map((m) => (
          <div key={m.severity} className="flex items-center gap-3 py-1.5">
            <span
              className={cn(
                'flex h-6 w-6 shrink-0 items-center justify-center rounded-md text-xs font-bold text-white',
                SEVERITY_DOT[m.severity],
              )}
              aria-hidden
            >
              {m.letter}
            </span>
            <span className="text-xl font-bold tabular-nums">{m.count}</span>
            <span className="min-w-0 flex-1 truncate text-sm text-muted-foreground">
              {m.label}
            </span>
            <TrendChip trend={m.trend} />
          </div>
        ))}
      </CardContent>
    </Card>
  )
}
