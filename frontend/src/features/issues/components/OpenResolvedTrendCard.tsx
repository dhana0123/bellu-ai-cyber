import { Area, AreaChart, CartesianGrid, Tooltip, XAxis, YAxis, ResponsiveContainer } from 'recharts'
import { ChevronDown, MoreVertical, Plus } from 'lucide-react'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import type { OpenResolvedPoint } from '@/types/issues'

const OPENED = 'hsl(0 73% 54%)'
const RESOLVED = 'hsl(161 84% 39%)'

// Custom tooltip matching the Row 5 spec: Opened (+change), New Resource, and
// Resolved (+change) for the hovered day.
function CustomTooltip({
  active,
  payload,
  label,
}: {
  active?: boolean
  payload?: Array<{ payload: OpenResolvedPoint }>
  label?: string
}) {
  if (!active || !payload || payload.length === 0) return null
  const p = payload[0].payload
  const fmt = (n: number) => (n >= 0 ? `+${n}` : `${n}`)
  return (
    <div className="rounded-md border bg-popover px-3 py-2 text-xs shadow-md">
      <div className="mb-1.5 font-medium">{label}, 2025</div>
      <div className="space-y-1">
        <div className="flex items-center gap-2">
          <span className="h-2 w-2 rounded-full" style={{ background: OPENED }} />
          <span className="text-muted-foreground">Opened</span>
          <span className="ml-auto font-medium">{p.opened}</span>
          <span className="text-muted-foreground">({fmt(p.opened_change)} since prev. day)</span>
        </div>
        <div className="flex items-center gap-2">
          <span className="h-2 w-2 rounded-full bg-sky-500" />
          <span className="text-muted-foreground">New Resource</span>
          <span className="ml-auto font-medium">{p.new_resource}</span>
        </div>
        <div className="flex items-center gap-2">
          <span className="h-2 w-2 rounded-full" style={{ background: RESOLVED }} />
          <span className="text-muted-foreground">Resolved</span>
          <span className="ml-auto font-medium">{p.resolved}</span>
          <span className="text-muted-foreground">({fmt(p.resolved_change)} change)</span>
        </div>
      </div>
    </div>
  )
}

export function OpenResolvedTrendCard({ data }: { data: OpenResolvedPoint[] }) {
  return (
    <Card>
      <CardHeader className="flex-row items-start justify-between space-y-0 p-4 pb-2">
        <div className="space-y-1">
          <CardTitle className="text-base">Open and Resolved Issues</CardTitle>
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
        <div className="mb-2 flex items-center gap-4 text-xs text-muted-foreground">
          <span className="inline-flex items-center gap-1">
            <span className="h-2 w-2 rounded-full" style={{ background: OPENED }} /> Opened
          </span>
          <span className="inline-flex items-center gap-1">
            <span className="h-2 w-2 rounded-full" style={{ background: RESOLVED }} /> Resolved
          </span>
        </div>
        <ResponsiveContainer width="100%" height={200}>
          <AreaChart data={data} margin={{ left: -16, right: 8, top: 8 }}>
            <defs>
              <linearGradient id="iss-opened" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor={OPENED} stopOpacity={0.25} />
                <stop offset="100%" stopColor={OPENED} stopOpacity={0.02} />
              </linearGradient>
              <linearGradient id="iss-resolved" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor={RESOLVED} stopOpacity={0.25} />
                <stop offset="100%" stopColor={RESOLVED} stopOpacity={0.02} />
              </linearGradient>
            </defs>
            <CartesianGrid vertical={false} stroke="hsl(var(--border))" />
            <XAxis dataKey="x" tickLine={false} axisLine={false} tickMargin={8} fontSize={11} />
            <YAxis tickLine={false} axisLine={false} width={32} fontSize={11} />
            <Tooltip content={<CustomTooltip />} cursor={{ stroke: 'hsl(var(--border))' }} />
            <Area
              dataKey="opened"
              type="monotone"
              stroke={OPENED}
              strokeWidth={2}
              fill="url(#iss-opened)"
              dot={false}
            />
            <Area
              dataKey="resolved"
              type="monotone"
              stroke={RESOLVED}
              strokeWidth={2}
              fill="url(#iss-resolved)"
              dot={false}
            />
          </AreaChart>
        </ResponsiveContainer>
      </CardContent>
    </Card>
  )
}
