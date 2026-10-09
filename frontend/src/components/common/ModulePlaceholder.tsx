import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { PreviewBadge } from './PreviewBadge'

// Temporary placeholder for modules whose pages land in a later step. Add-on
// (Preview) modules pass preview so it is never mistaken for a finished,
// real-data screen; Platform placeholders (e.g. Settings) omit it.
export function ModulePlaceholder({
  title,
  blurb,
  preview = true,
}: {
  title: string
  blurb: string
  preview?: boolean
}) {
  return (
    <div className="space-y-5">
      <div className="flex items-start justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
          <p className="text-sm text-muted-foreground">{blurb}</p>
        </div>
        {preview ? <PreviewBadge /> : null}
      </div>
      <Card>
        <CardHeader>
          <CardTitle>Module scaffolding</CardTitle>
          <CardDescription>
            {preview
              ? 'This Preview module is wired to the typed mock layer. Its full UI is built in a later step. All data shown here is demo data, not real customer telemetry.'
              : 'This section is scaffolded. Its full UI is built in a later step.'}
          </CardDescription>
        </CardHeader>
        <CardContent className="text-sm text-muted-foreground">
          Navigation, routing, and permission gating for this module are active.
        </CardContent>
      </Card>
    </div>
  )
}
