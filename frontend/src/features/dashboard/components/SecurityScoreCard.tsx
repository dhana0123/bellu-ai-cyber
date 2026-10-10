import { Label, Pie, PieChart, Area, AreaChart } from 'recharts'
import { Eye, Settings } from 'lucide-react'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { ChartContainer, ChartTooltip, ChartTooltipContent } from '@/components/ui/chart'
import type { ChartConfig } from '@/components/ui/chart'
import { TrendChip } from './TrendChip'
import type { SecurityScore } from '@/types/dashboard'

const SCORE_COLOR = 'hsl(161 84% 39%)' // Bellu green

// Card 4 — Security Score. A prominent donut showing the posture score plus a
// supporting historical area chart.
export function SecurityScoreCard({ data }: { data: SecurityScore }) {
  const score = Math.max(0, Math.min(100, data.score))
  const donutData = [
    { key: 'score', value: score, fill: SCORE_COLOR },
    { key: 'rest', value: 100 - score, fill: 'hsl(var(--muted))' },
  ]
  const donutConfig: ChartConfig = { value: { label: 'Score' } }
  const historyConfig: ChartConfig = { value: { label: 'Score', color: SCORE_COLOR } }

  return (
    <Card>
      <CardHeader className="flex-row items-start justify-between space-y-0 p-4 pb-2">
        <div className="space-y-1">
          <CardTitle className="text-base">Security Score</CardTitle>
          <CardDescription>{data.source}</CardDescription>
        </div>
        <div className="flex items-center gap-1">
          <Button variant="ghost" size="icon" className="h-7 w-7" aria-label="Visibility">
            <Eye className="h-4 w-4" />
          </Button>
          <Button variant="ghost" size="icon" className="h-7 w-7" aria-label="Settings">
            <Settings className="h-4 w-4" />
          </Button>
        </div>
      </CardHeader>
      <CardContent className="p-4 pt-2">
        <div className="relative mx-auto">
          <ChartContainer config={donutConfig} className="mx-auto aspect-square max-h-[150px]">
            <PieChart>
              <Pie
                data={donutData}
                dataKey="value"
                nameKey="key"
                innerRadius={50}
                outerRadius={68}
                startAngle={90}
                endAngle={-270}
                strokeWidth={0}
              >
                <Label
                  content={({ viewBox }) => {
                    if (viewBox && 'cx' in viewBox && 'cy' in viewBox) {
                      return (
                        <text
                          x={viewBox.cx}
                          y={viewBox.cy}
                          textAnchor="middle"
                          dominantBaseline="middle"
                        >
                          <tspan
                            x={viewBox.cx}
                            y={viewBox.cy}
                            className="fill-foreground text-2xl font-bold"
                          >
                            {score}%
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
        </div>

        <div className="mt-1 flex justify-center">
          <TrendChip trend={data.trend} />
        </div>

        <ChartContainer config={historyConfig} className="mt-3 h-[70px] w-full">
          <AreaChart data={data.history} margin={{ left: 0, right: 0, top: 4, bottom: 0 }}>
            <defs>
              <linearGradient id="score-grad" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor={SCORE_COLOR} stopOpacity={0.35} />
                <stop offset="100%" stopColor={SCORE_COLOR} stopOpacity={0.02} />
              </linearGradient>
            </defs>
            <ChartTooltip content={<ChartTooltipContent hideLabel />} />
            <Area
              dataKey="value"
              type="monotone"
              stroke={SCORE_COLOR}
              strokeWidth={2}
              fill="url(#score-grad)"
              dot={false}
            />
          </AreaChart>
        </ChartContainer>
      </CardContent>
    </Card>
  )
}
