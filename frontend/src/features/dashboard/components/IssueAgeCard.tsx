import { RadialBar, RadialBarChart, PolarAngleAxis } from 'recharts'
import { Settings, MoreVertical } from 'lucide-react'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { ChartContainer } from '@/components/ui/chart'
import type { ChartConfig } from '@/components/ui/chart'
import { SEVERITY_HSL } from './severity'
import type { IssueAgeGauge } from '@/types/dashboard'

// A single semi-circular gauge: average age vs SLA. The arc fill is the fraction
// of the age relative to a display ceiling (max of age / sla) so breaches read
// as a nearly-full arc.
function Gauge({ gauge }: { gauge: IssueAgeGauge }) {
  const color = SEVERITY_HSL[gauge.severity]
  const ceiling = Math.max(gauge.days, gauge.sla_days)
  const config: ChartConfig = { value: { label: gauge.label, color } }
  const data = [{ name: gauge.label, value: gauge.days, fill: color }]

  return (
    <div className="flex flex-col items-center">
      <div className="relative">
        <ChartContainer config={config} className="h-[90px] w-[150px]">
          <RadialBarChart
            data={data}
            startAngle={180}
            endAngle={0}
            innerRadius={56}
            outerRadius={82}
            barSize={12}
            cy="100%"
          >
            <PolarAngleAxis type="number" domain={[0, ceiling]} tick={false} axisLine={false} />
            <RadialBar dataKey="value" cornerRadius={6} background fill={color} />
          </RadialBarChart>
        </ChartContainer>
        <div className="pointer-events-none absolute inset-x-0 bottom-0 text-center">
          <span className="text-xl font-bold tabular-nums">{gauge.days}</span>
          <span className="ml-1 text-xs text-muted-foreground">days</span>
        </div>
      </div>
      <p className="mt-1 text-center text-xs font-medium">{gauge.label}</p>
      <p className="text-center text-[11px] text-muted-foreground">SLA: {gauge.sla_days} days</p>
    </div>
  )
}

// Card 3 — Average Issue Age. A 2x2 grid of semi-circular gauge charts.
export function IssueAgeCard({ gauges }: { gauges: IssueAgeGauge[] }) {
  return (
    <Card>
      <CardHeader className="flex-row items-start justify-between space-y-0 p-4 pb-2">
        <div className="space-y-1">
          <CardTitle className="text-base">Average Issue Age</CardTitle>
          <CardDescription>Current open issues</CardDescription>
        </div>
        <div className="flex items-center gap-1">
          <Button variant="ghost" size="icon" className="h-7 w-7" aria-label="Settings">
            <Settings className="h-4 w-4" />
          </Button>
          <Button variant="ghost" size="icon" className="h-7 w-7" aria-label="Age options">
            <MoreVertical className="h-4 w-4" />
          </Button>
        </div>
      </CardHeader>
      <CardContent className="grid grid-cols-2 gap-x-2 gap-y-4 p-4 pt-2">
        {gauges.map((g) => (
          <Gauge key={g.severity} gauge={g} />
        ))}
      </CardContent>
    </Card>
  )
}
