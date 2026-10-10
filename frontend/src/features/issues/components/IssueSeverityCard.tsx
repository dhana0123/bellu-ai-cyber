import { ArrowUp, MoreVertical, Plus } from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import { SEVERITY_DOT } from './severity'
import type { SeverityMetric } from '@/types/issues'

// Row 5, Card 1 — Issue Severity. A vertical list of severity metrics. A nonzero
// positive trend renders a red "↑ N%" pill; zero renders a neutral chip.
function TrendPill({ pct }: { pct: number }) {
  if (pct === 0) {
    return (
      <span className="rounded-full bg-muted px-1.5 py-0.5 text-xs text-muted-foreground">0%</span>
    )
  }
  const up = pct > 0
  return (
    <span
      className={cn(
        'inline-flex items-center gap-0.5 rounded-full px-1.5 py-0.5 text-xs font-medium',
        up ? 'bg-red-500/10 text-red-500' : 'bg-emerald-500/10 text-emerald-500',
      )}
    >
      <ArrowUp className={cn('h-3 w-3', !up && 'rotate-180')} />
      {Math.abs(pct)}%
    </span>
  )
}

export function IssueSeverityCard({ metrics }: { metrics: SeverityMetric[] }) {
  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between space-y-0 p-4 pb-2">
        <CardTitle className="text-base">Issue Severity</CardTitle>
        <div className="flex items-center gap-1">
          <Button variant="ghost" size="icon" className="h-7 w-7" aria-label="Add">
            <Plus className="h-4 w-4" />
          </Button>
          <Button variant="ghost" size="icon" className="h-7 w-7" aria-label="Options">
            <MoreVertical className="h-4 w-4" />
          </Button>
        </div>
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
            <span className="w-6 text-xl font-bold tabular-nums">{m.count}</span>
            <span className="min-w-0 flex-1 truncate text-sm text-muted-foreground">
              {m.label}
            </span>
            <TrendPill pct={m.trend_pct} />
          </div>
        ))}
      </CardContent>
    </Card>
  )
}
