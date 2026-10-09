// Dev-only charts gallery: verifies every Bellu chart wrapper renders with
// shadcn Charts + Recharts in both themes. Clearly-labeled DEMO data. Gated out
// of production in Task 10; not linked from the app nav.
import {
  AreaChartCard,
  BarChartCard,
  DonutChartCard,
  LineChartCard,
  RadialChartCard,
} from '@/components/charts'

const DEMO_SEVERITY: { label: string; value: number; color?: string }[] = [
  { label: 'Critical', value: 8, color: 'hsl(var(--chart-5))' },
  { label: 'High', value: 21, color: 'hsl(var(--chart-4))' },
  { label: 'Medium', value: 44 },
  { label: 'Low', value: 73 },
]

const DEMO_KINDS = [
  { label: 'AIModel', value: 34 },
  { label: 'AIAgent', value: 19 },
  { label: 'DataStore', value: 52 },
  { label: 'Workload', value: 61 },
  { label: 'Identity', value: 88 },
]

const DEMO_PROVIDER_SERIES = [
  { x: 'Mon', openai: 420, anthropic: 240, local: 110 },
  { x: 'Tue', openai: 510, anthropic: 300, local: 140 },
  { x: 'Wed', openai: 480, anthropic: 280, local: 160 },
  { x: 'Thu', openai: 620, anthropic: 350, local: 180 },
  { x: 'Fri', openai: 700, anthropic: 410, local: 220 },
]

const PROVIDER_DEFS = [
  { key: 'openai', label: 'OpenAI' },
  { key: 'anthropic', label: 'Anthropic' },
  { key: 'local', label: 'Local' },
]

const DEMO_MEDIA = [
  { label: 'Authentic', value: 182 },
  { label: 'Voice synth', value: 24 },
  { label: 'Deepfake video', value: 11 },
  { label: 'CGI', value: 7 },
]

export default function ChartsGallery() {
  return (
    <div className="min-h-screen bg-background p-8 text-foreground">
      <h1 className="mb-1 text-2xl font-semibold tracking-tight">Bellu charts gallery</h1>
      <p className="mb-6 text-sm text-muted-foreground">
        shadcn Charts (Recharts) wrappers — all values below are DEMO data.
      </p>
      <div className="grid gap-6 md:grid-cols-2">
        <BarChartCard
          title="Findings by severity"
          description="DEMO — open findings grouped by severity"
          data={DEMO_SEVERITY}
          valueLabel="Findings"
        />
        <BarChartCard
          title="Entities by kind"
          description="DEMO — inventory distribution"
          data={DEMO_KINDS}
          valueLabel="Entities"
          horizontal
        />
        <AreaChartCard
          title="API calls by provider"
          description="DEMO — gateway traffic over the week"
          data={DEMO_PROVIDER_SERIES}
          series={PROVIDER_DEFS}
        />
        <LineChartCard
          title="Latency trend"
          description="DEMO — p95 latency by provider (ms)"
          data={DEMO_PROVIDER_SERIES}
          series={PROVIDER_DEFS}
        />
        <DonutChartCard
          title="Media authenticity mix"
          description="DEMO — scanned media classification"
          data={DEMO_MEDIA}
          centerLabel="Scanned"
        />
        <RadialChartCard
          title="Risk posture"
          description="DEMO — aggregate posture score"
          value={72}
          label="Posture"
        />
      </div>
    </div>
  )
}
