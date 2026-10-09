import * as React from 'react'
import { Label, Pie, PieChart } from 'recharts'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from '@/components/ui/chart'
import type { CategoryDatum } from './types'
import { paletteColor } from './palette'

interface DonutChartCardProps {
  title: string
  description?: string
  data: CategoryDatum[]
  /** Label under the center total. */
  centerLabel?: string
  className?: string
}

/**
 * Bellu donut chart for compositional breakdowns, e.g. media manipulation types
 * or finding status mix. Shows the summed total in the center.
 */
export function DonutChartCard({
  title,
  description,
  data,
  centerLabel = 'Total',
  className,
}: DonutChartCardProps) {
  const total = React.useMemo(() => data.reduce((acc, d) => acc + d.value, 0), [data])

  const config: ChartConfig = Object.fromEntries(
    data.map((d, i) => [d.label, { label: d.label, color: d.color ?? paletteColor(i) }]),
  )

  const chartData = data.map((d, i) => ({ ...d, fill: d.color ?? paletteColor(i) }))

  return (
    <Card className={className}>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        {description ? <CardDescription>{description}</CardDescription> : null}
      </CardHeader>
      <CardContent>
        <ChartContainer config={config} className="mx-auto aspect-square max-h-[260px]">
          <PieChart>
            <ChartTooltip content={<ChartTooltipContent hideLabel />} />
            <Pie data={chartData} dataKey="value" nameKey="label" innerRadius={60} strokeWidth={4}>
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
                          {total.toLocaleString()}
                        </tspan>
                        <tspan
                          x={viewBox.cx}
                          y={(viewBox.cy ?? 0) + 20}
                          className="fill-muted-foreground text-xs"
                        >
                          {centerLabel}
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
      </CardContent>
    </Card>
  )
}
