import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from 'recharts'
import { Filter } from 'lucide-react'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { ChartContainer, ChartTooltip, ChartTooltipContent } from '@/components/ui/chart'
import type { ChartConfig } from '@/components/ui/chart'
import type { OpenedResolvedPoint } from '@/types/dashboard'

const OPENED = 'hsl(0 73% 54%)'
const RESOLVED = 'hsl(161 84% 39%)'

// Card 6 — Opened and Resolved Issues. A dual-line area (burn-up) chart tracking
// cumulative opened vs resolved issues over the past 30 days.
export function OpenedResolvedCard({ data }: { data: OpenedResolvedPoint[] }) {
  const config: ChartConfig = {
    opened: { label: 'Opened', color: OPENED },
    resolved: { label: 'Resolved', color: RESOLVED },
  }

  return (
    <Card>
      <CardHeader className="flex-row items-start justify-between space-y-0 p-4 pb-2">
        <div className="space-y-1">
          <CardTitle className="text-base">Opened and Resolved Issues</CardTitle>
          <CardDescription>Past 30 days</CardDescription>
        </div>
        <div className="flex items-center gap-3">
          <div className="flex items-center gap-3 text-xs text-muted-foreground">
            <span className="inline-flex items-center gap-1">
              <span className="h-2 w-2 rounded-full" style={{ background: OPENED }} /> Opened
            </span>
            <span className="inline-flex items-center gap-1">
              <span className="h-2 w-2 rounded-full" style={{ background: RESOLVED }} /> Resolved
            </span>
          </div>
          <Button variant="ghost" size="icon" className="h-7 w-7" aria-label="Filter">
            <Filter className="h-4 w-4" />
          </Button>
        </div>
      </CardHeader>
      <CardContent className="p-4 pt-2">
        <ChartContainer config={config} className="h-[280px] w-full">
          <AreaChart data={data} margin={{ left: 8, right: 8, top: 8 }}>
            <defs>
              <linearGradient id="opened-grad" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor={OPENED} stopOpacity={0.3} />
                <stop offset="100%" stopColor={OPENED} stopOpacity={0.02} />
              </linearGradient>
              <linearGradient id="resolved-grad" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor={RESOLVED} stopOpacity={0.3} />
                <stop offset="100%" stopColor={RESOLVED} stopOpacity={0.02} />
              </linearGradient>
            </defs>
            <CartesianGrid vertical={false} />
            <XAxis dataKey="x" tickLine={false} axisLine={false} tickMargin={8} minTickGap={24} />
            <YAxis tickLine={false} axisLine={false} width={36} />
            <ChartTooltip content={<ChartTooltipContent />} />
            <Area
              dataKey="opened"
              type="monotone"
              stroke={OPENED}
              strokeWidth={2}
              fill="url(#opened-grad)"
              dot={false}
            />
            <Area
              dataKey="resolved"
              type="monotone"
              stroke={RESOLVED}
              strokeWidth={2}
              fill="url(#resolved-grad)"
              dot={false}
            />
          </AreaChart>
        </ChartContainer>
      </CardContent>
    </Card>
  )
}
