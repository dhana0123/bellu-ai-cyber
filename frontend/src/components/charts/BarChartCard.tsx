import { Bar, BarChart, CartesianGrid, Cell, XAxis, YAxis } from 'recharts'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from '@/components/ui/chart'
import type { CategoryDatum } from './types'
import { paletteColor } from './palette'

interface BarChartCardProps {
  title: string
  description?: string
  data: CategoryDatum[]
  /** Legend/label for the measured value. */
  valueLabel?: string
  /** Horizontal layout (categories on the Y axis) — good for long labels. */
  horizontal?: boolean
  className?: string
}

/**
 * Bellu bar chart. Renders a single-series bar chart from CategoryDatum[], e.g.
 * entities-by-kind or findings-by-severity. Colors come from Bellu's chart
 * palette unless a datum sets its own color.
 */
export function BarChartCard({
  title,
  description,
  data,
  valueLabel = 'Count',
  horizontal = false,
  className,
}: BarChartCardProps) {
  const config: ChartConfig = {
    value: { label: valueLabel, color: 'hsl(var(--chart-1))' },
  }

  return (
    <Card className={className}>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        {description ? <CardDescription>{description}</CardDescription> : null}
      </CardHeader>
      <CardContent>
        <ChartContainer config={config} className="aspect-[16/9] w-full">
          <BarChart
            accessibilityLayer
            data={data}
            layout={horizontal ? 'vertical' : 'horizontal'}
            margin={{ left: 8, right: 8, top: 8 }}
          >
            <CartesianGrid vertical={!horizontal} horizontal={horizontal} />
            {horizontal ? (
              <>
                <XAxis type="number" dataKey="value" hide />
                <YAxis
                  type="category"
                  dataKey="label"
                  width={110}
                  tickLine={false}
                  axisLine={false}
                />
              </>
            ) : (
              <>
                <XAxis dataKey="label" tickLine={false} axisLine={false} tickMargin={8} />
                <YAxis tickLine={false} axisLine={false} width={32} />
              </>
            )}
            <ChartTooltip content={<ChartTooltipContent />} />
            <Bar dataKey="value" radius={4} fill="var(--color-value)">
              {data.map((d, i) => (
                <Cell key={d.label} fill={d.color ?? paletteColor(i)} />
              ))}
            </Bar>
          </BarChart>
        </ChartContainer>
      </CardContent>
    </Card>
  )
}
