import { AlertTriangle, Sun } from 'lucide-react'
import { Card } from '@/components/ui/card'
import { cn } from '@/lib/utils'
import { SEVERITY_TEXT } from './severity'
import type { Advisory } from '@/types/dashboard'

// Row 1 — Latest Published Advisories. A full-width banner split into one alert
// column per advisory.
export function AdvisoryBanner({ advisories }: { advisories: Advisory[] }) {
  return (
    <Card className="grid grid-cols-1 divide-y md:grid-cols-2 md:divide-x md:divide-y-0">
      {advisories.map((a) => (
        <div key={a.id} className="flex items-start gap-3 p-4">
          <AlertTriangle className={cn('mt-0.5 h-5 w-5 shrink-0', SEVERITY_TEXT[a.severity])} />
          <div className="min-w-0 flex-1">
            <p className="truncate text-sm font-semibold">{a.title}</p>
            <p className="mt-0.5 text-xs text-muted-foreground">Published: {a.published}</p>
          </div>
          <div className="flex shrink-0 items-center gap-2 text-muted-foreground">
            <span
              className="inline-flex h-5 min-w-5 items-center justify-center rounded-full bg-muted px-1.5 text-xs font-medium"
              aria-label={`${a.indicator_count} indicators`}
            >
              {a.indicator_count}
            </span>
            <Sun className="h-4 w-4" />
          </div>
        </div>
      ))}
    </Card>
  )
}
