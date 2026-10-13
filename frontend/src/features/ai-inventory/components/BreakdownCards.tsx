import { Label, Pie, PieChart } from 'recharts'
import { Code2, MoreVertical, Plus } from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from '@/components/ui/chart'
import type { BreakdownRow, TypeSlice } from '@/types/aiInventory'

const CATEGORY_SWATCHES = [
  'bg-emerald-500',
  'bg-blue-500',
  'bg-amber-500',
  'bg-violet-500',
  'bg-sky-500',
]

function CardMenu() {
  return (
    <div className="flex items-center gap-1">
      <Button variant="ghost" size="icon" className="h-7 w-7" aria-label="Add">
        <Plus className="h-4 w-4" />
      </Button>
      <Button variant="ghost" size="icon" className="h-7 w-7" aria-label="Options">
        <MoreVertical className="h-4 w-4" />
      </Button>
    </div>
  )
}

// Column 1 — Breakdown by Category (color block + label + count).
export function BreakdownByCategory({ rows }: { rows: BreakdownRow[] }) {
  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between space-y-0 p-4 pb-2">
        <CardTitle className="text-base">Breakdown by Category</CardTitle>
        <CardMenu />
      </CardHeader>
      <CardContent className="space-y-0.5 p-2">
        {rows.map((r, i) => (
          <div key={r.id} className="flex items-center gap-3 rounded-md px-2 py-1.5 hover:bg-accent">
            <span
              className={`h-3.5 w-3.5 shrink-0 rounded-sm ${CATEGORY_SWATCHES[i % CATEGORY_SWATCHES.length]}`}
            />
            <span className="min-w-0 flex-1 truncate text-sm">{r.label}</span>
            <span className="shrink-0 text-sm font-semibold tabular-nums">{r.count}</span>
          </div>
        ))}
      </CardContent>
    </Card>
  )
}

// Column 2 — Breakdown by Subcategory (bracket/code icon + label + count).
export function BreakdownBySubcategory({ rows }: { rows: BreakdownRow[] }) {
  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between space-y-0 p-4 pb-2">
        <CardTitle className="text-base">Breakdown by Subcategory</CardTitle>
        <CardMenu />
      </CardHeader>
      <CardContent className="space-y-0.5 p-2">
        {rows.map((r) => (
          <div key={r.id} className="flex items-center gap-3 rounded-md px-2 py-1.5 hover:bg-accent">
            <span className="flex h-5 w-5 shrink-0 items-center justify-center rounded bg-amber-500/15 text-amber-500">
              <Code2 className="h-3.5 w-3.5" />
            </span>
            <span className="min-w-0 flex-1 truncate text-sm">{r.label}</span>
            <span className="shrink-0 text-sm font-semibold tabular-nums">{r.count}</span>
          </div>
        ))}
      </CardContent>
    </Card>
  )
}

// Column 3 — Breakdown by Type (donut + legend split).
export function BreakdownByType({ slices, total }: { slices: TypeSlice[]; total: number }) {
  const config: ChartConfig = Object.fromEntries(
    slices.map((s) => [s.label, { label: s.label, color: s.color }]),
  )
  const data = slices.map((s) => ({ ...s, fill: s.color }))

  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between space-y-0 p-4 pb-2">
        <CardTitle className="text-base">Breakdown by Type</CardTitle>
        <CardMenu />
      </CardHeader>
      <CardContent className="flex items-center gap-2 p-4 pt-2">
        <ChartContainer config={config} className="aspect-square h-[150px]">
          <PieChart>
            <ChartTooltip content={<ChartTooltipContent hideLabel />} />
            <Pie data={data} dataKey="count" nameKey="label" innerRadius={46} strokeWidth={3}>
              <Label
                content={({ viewBox }) => {
                  if (viewBox && 'cx' in viewBox && 'cy' in viewBox) {
                    return (
                      <text x={viewBox.cx} y={viewBox.cy} textAnchor="middle" dominantBaseline="middle">
                        <tspan x={viewBox.cx} y={viewBox.cy} className="fill-foreground text-xl font-bold">
                          {total}
                        </tspan>
                        <tspan
                          x={viewBox.cx}
                          y={(viewBox.cy ?? 0) + 16}
                          className="fill-muted-foreground text-[10px]"
                        >
                          Technologies
                        </tspan>
                      </text>
                    )
                  }
                  return null
                }}
              />
            </Pie>
          </PieChart>
        </ChartContainer>

        <div className="min-w-0 flex-1 space-y-1.5">
          {slices.map((s) => (
            <div key={s.id} className="flex items-center gap-2 text-sm">
              <span className="h-2.5 w-2.5 shrink-0 rounded-full" style={{ background: s.color }} />
              <span className="min-w-0 flex-1 truncate text-muted-foreground">{s.label}</span>
              <span className="shrink-0 font-semibold tabular-nums">{s.count}</span>
            </div>
          ))}
        </div>
      </CardContent>
    </Card>
  )
}
