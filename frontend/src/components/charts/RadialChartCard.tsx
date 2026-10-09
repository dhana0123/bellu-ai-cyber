import { PolarGrid, RadialBar, RadialBarChart } from 'recharts'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { ChartContainer, type ChartConfig } from '@/components/ui/chart'

interface RadialChartCardProps {
  title: string
  description?: string
  /** 0..100 score (e.g. authenticity confidence, risk posture). */
  value: number
  label?: string
  /** Explicit color; otherwise chart-1. */
  color?: string
  className?: string
}

/**
 * Bellu radial gauge for a single 0–100 score, e.g. authenticity confidence or
 * aggregate risk posture. Uses a themed RadialBarChart (no custom SVG).
 */
export function RadialChartCard({
  title,
  description,
  value,
  label = 'Score',
  color = 'hsl(var(--chart-1))',
  className,
}: RadialChartCardProps) {
  const clamped = Math.max(0, Math.min(100, value))
  const config: ChartConfig = { score: { label, color } }
  const data = [{ name: label, score: clamped, fill: color }]

  return (
    <Card className={className}>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        {description ? <CardDescription>{description}</CardDescription> : null}
      </CardHeader>
      <CardContent>
        <ChartContainer config={config} className="mx-auto aspect-square max-h-[240px]">
          <RadialBarChart
            data={data}
            startAngle={90}
            endAngle={90 - (clamped / 100) * 360}
            innerRadius={80}
            outerRadius={130}
          >
            <PolarGrid
              gridType="circle"
              radialLines={false}
              stroke="none"
              className="first:fill-muted"
            />
            <RadialBar dataKey="score" background cornerRadius={8} fill={color} />
            <text
              x="50%"
              y="50%"
              textAnchor="middle"
              dominantBaseline="middle"
              className="fill-foreground text-3xl font-bold"
            >
              {clamped}
            </text>
          </RadialBarChart>
        </ChartContainer>
      </CardContent>
    </Card>
  )
}
