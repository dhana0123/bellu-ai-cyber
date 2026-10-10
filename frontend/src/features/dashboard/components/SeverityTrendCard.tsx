import { Area, AreaChart } from 'recharts'
import { Filter, RefreshCw } from 'lucide-react'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { ChartContainer, ChartTooltip, ChartTooltipContent } from '@/components/ui/chart'
import type { ChartConfig } from '@/components/ui/chart'
import { TrendChip } from './TrendChip'
import { SEVERITY_HSL } from './severity'
import type { SeverityTrend } from '@/types/dashboard'

// Card 2 — Issues by Severity. Two stacked mini gradient area charts (Critical,
// High) showing daily volume over the past 30 days.
function MiniChart({ trend }: { trend: SeverityTrend }) {
  const color = SEVERITY_HSL[trend.severity]
  const gradId = `grad-${trend.severity}`
  const config: ChartConfig = { value: { label: trend.label, color } }

  return (
    <div className="space-y-1">
      <div className="flex items-center justify-between">
        <span className="text-sm font-medium">{trend.label}</span>
        <div className="flex items-center gap-2">
          <span className="text-lg font-bold tabular-nums">{trend.current}</span>
          <TrendChip trend={trend.trend} />
        </div>
      </div>
      <ChartContainer config={config} className="h-[90px] w-full">
        <AreaChart data={trend.series} margin={{ left: 0, right: 0, top: 4, bottom: 0 }}>
          <defs>
            <linearGradient id={gradId} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor={color} stopOpacity={0.4} />
              <stop offset="100%" stopColor={color} stopOpacity={0.02} />
            </linearGradient>
          </defs>
          <ChartTooltip content={<ChartTooltipContent hideLabel />} />
          <Area
            dataKey="value"
            type="monotone"
            stroke={color}
            strokeWidth={2}
            fill={`url(#${gradId})`}
            dot={false}
          />
        </AreaChart>
      </ChartContainer>
    </div>
  )
}

export function SeverityTrendCard({ trends }: { trends: SeverityTrend[] }) {
  return (
    <Card>
      <CardHeader className="flex-row items-start justify-between space-y-0 p-4 pb-2">
        <div className="space-y-1">
          <CardTitle className="text-base">Issues by Severity</CardTitle>
          <CardDescription>Past 30 days</CardDescription>
        </div>
        <div className="flex items-center gap-1">
          <Button variant="ghost" size="icon" className="h-7 w-7" aria-label="Filter">
            <Filter className="h-4 w-4" />
          </Button>
          <Button variant="ghost" size="icon" className="h-7 w-7" aria-label="Refresh">
            <RefreshCw className="h-4 w-4" />
          </Button>
        </div>
      </CardHeader>
      <CardContent className="space-y-4 p-4 pt-2">
        {trends.map((t) => (
          <MiniChart key={t.severity} trend={t} />
        ))}
      </CardContent>
    </Card>
  )
}
