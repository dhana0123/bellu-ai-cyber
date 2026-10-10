import { CartesianGrid, Line, LineChart, Tooltip, XAxis, YAxis, ResponsiveContainer } from 'recharts'
import { ArrowUp, ChevronDown, MoreVertical, Plus } from 'lucide-react'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import type { OpenIssuesPoint } from '@/types/issues'

const CRITICAL = 'hsl(0 73% 54%)'
const HIGH = 'hsl(215 16% 60%)'

export function OpenIssuesTrendCard({
  data,
  trendPct,
}: {
  data: OpenIssuesPoint[]
  trendPct: number
}) {
  const latestCritical = data.length ? data[data.length - 1].critical : 0

  return (
    <Card>
      <CardHeader className="flex-row items-start justify-between space-y-0 p-4 pb-2">
        <div className="space-y-1">
          <CardTitle className="text-base">Open Issues</CardTitle>
          <CardDescription>Past 30 days</CardDescription>
        </div>
        <div className="flex items-center gap-1">
          <Button variant="ghost" size="icon" className="h-7 w-7" aria-label="Add">
            <Plus className="h-4 w-4" />
          </Button>
          <Button variant="ghost" size="icon" className="h-7 w-7" aria-label="Filter">
            <ChevronDown className="h-4 w-4" />
          </Button>
          <Button variant="ghost" size="icon" className="h-7 w-7" aria-label="Options">
            <MoreVertical className="h-4 w-4" />
          </Button>
        </div>
      </CardHeader>
      <CardContent className="p-4 pt-2">
        <div className="mb-2 flex items-center gap-3">
          <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
            <span className="h-2 w-2 rounded-full" style={{ background: CRITICAL }} /> Critical
          </span>
          <span className="inline-flex items-center gap-0.5 rounded-full bg-red-500/10 px-1.5 py-0.5 text-xs font-medium text-red-500">
            <ArrowUp className="h-3 w-3" />
            {trendPct}% · {latestCritical}
          </span>
          <span className="ml-auto inline-flex items-center gap-1 text-xs text-muted-foreground">
            <span className="h-2 w-2 rounded-full" style={{ background: HIGH }} /> High · 4
          </span>
        </div>
        <ResponsiveContainer width="100%" height={200}>
          <LineChart data={data} margin={{ left: -16, right: 8, top: 8 }}>
            <CartesianGrid vertical={false} stroke="hsl(var(--border))" />
            <XAxis dataKey="x" tickLine={false} axisLine={false} tickMargin={8} fontSize={11} />
            <YAxis tickLine={false} axisLine={false} width={32} fontSize={11} />
            <Tooltip
              cursor={{ stroke: 'hsl(var(--border))' }}
              contentStyle={{
                borderRadius: 8,
                border: '1px solid hsl(var(--border))',
                background: 'hsl(var(--popover))',
                fontSize: 12,
              }}
            />
            {/* Stepped lines mirror the AWS stair-step trend look. */}
            <Line
              dataKey="critical"
              name="Critical"
              type="stepAfter"
              stroke={CRITICAL}
              strokeWidth={2}
              dot={false}
            />
            <Line
              dataKey="high"
              name="High"
              type="stepAfter"
              stroke={HIGH}
              strokeWidth={2}
              dot={false}
            />
          </LineChart>
        </ResponsiveContainer>
      </CardContent>
    </Card>
  )
}
