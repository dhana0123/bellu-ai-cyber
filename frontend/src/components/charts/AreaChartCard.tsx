import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from 'recharts'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import {
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from '@/components/ui/chart'
import type { SeriesDatum } from './types'
import { paletteColor } from './palette'

export interface SeriesDef {
  key: string
  label: string
  color?: string
}

interface AreaChartCardProps {
  title: string
  description?: string
  data: SeriesDatum[]
  series: SeriesDef[]
  /** Stack the areas (e.g. provider mix). */
  stacked?: boolean
  className?: string
}

/**
 * Bellu stacked/overlaid area chart for time- or category-series data, e.g. API
 * calls per provider over time. Legend + themed tooltip included.
 */
export function AreaChartCard({
  title,
  description,
  data,
  series,
  stacked = true,
  className,
}: AreaChartCardProps) {
  const config: ChartConfig = Object.fromEntries(
    series.map((s, i) => [s.key, { label: s.label, color: s.color ?? paletteColor(i) }]),
  )

  return (
    <Card className={className}>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        {description ? <CardDescription>{description}</CardDescription> : null}
      </CardHeader>
      <CardContent>
        <ChartContainer config={config} className="aspect-[16/9] w-full">
          <AreaChart accessibilityLayer data={data} margin={{ left: 8, right: 8, top: 8 }}>
            <CartesianGrid vertical={false} />
            <XAxis dataKey="x" tickLine={false} axisLine={false} tickMargin={8} />
            <YAxis tickLine={false} axisLine={false} width={32} />
            <ChartTooltip content={<ChartTooltipContent />} />
            <ChartLegend content={<ChartLegendContent />} />
            {series.map((s) => (
              <Area
                key={s.key}
                dataKey={s.key}
                type="monotone"
                stackId={stacked ? 'a' : undefined}
                stroke={`var(--color-${s.key})`}
                fill={`var(--color-${s.key})`}
                fillOpacity={0.2}
              />
            ))}
          </AreaChart>
        </ChartContainer>
      </CardContent>
    </Card>
  )
}
