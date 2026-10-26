import type { ReactNode } from 'react'
import { ShieldAlert, ShieldCheck, FlaskConical, GitBranch } from 'lucide-react'
import { realApi } from '@/lib/api'
import { useAsync } from '@/useAsync'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Spinner } from '@/components/ui/spinner'
import type { BadgeProps } from '@/components/ui/badge'
import type { CanonicalVuln, EvidenceDetail, FindingReportView } from '@/types/foundation'

// FindingDetail renders the extended GET /api/findings/:id response: the finding
// header (with the prominent "Unvalidated" status), a Red Team section (the
// report whose team === 'red'), a Blue Team + remediation section (team ===
// 'blue'), the cited intel, the evidence trail, and the update history.
//
// When `reports` is empty it renders ONLY the header + intel + evidence — a
// graceful fallback, never an error (Req 18.2 / P7).
//
// SAFETY (Req 18.3 / P6): PoC presence is shown from the `poc_available` flag
// ONLY. The weaponized `poc_script_code` body is never in the response, the type,
// or component state, so it can never reach the DOM.

const SEVERITY_VARIANT: Record<string, BadgeProps['variant']> = {
  critical: 'danger',
  high: 'danger',
  medium: 'warning',
  low: 'secondary',
  info: 'outline',
}
function severityVariant(sev: string): BadgeProps['variant'] {
  return SEVERITY_VARIANT[String(sev || '').toLowerCase()] ?? 'outline'
}

// Known structured keys the red/blue sections surface explicitly; anything else
// in `sections` is rendered generically so nothing is silently dropped.
const RED_KEYS = [
  'description',
  'impact',
  'technical_analysis',
  'evidence',
  'code_locations',
  'fix_diffs',
] as const
const BLUE_KEYS = ['remediation_steps', 'fix_effort', 'fix_verification'] as const

function humanize(key: string): string {
  return key.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase())
}

// Renders a single section value as text, a list, or a code block (for diffs),
// never interpreting it as HTML. Returns null for empty values.
function SectionValue({ field, value }: { field: string; value: unknown }) {
  if (value == null || value === '') return null
  const isDiff = field === 'fix_diffs'
  if (Array.isArray(value)) {
    if (value.length === 0) return null
    if (isDiff) {
      return (
        <div className="space-y-2">
          {value.map((v, i) => (
            <pre
              key={i}
              className="overflow-x-auto rounded-md border bg-muted/40 p-3 font-mono text-xs"
            >
              {String(v)}
            </pre>
          ))}
        </div>
      )
    }
    return (
      <ul className="ml-4 list-disc space-y-1 text-sm text-muted-foreground">
        {value.map((v, i) => (
          <li key={i}>{typeof v === 'string' ? v : JSON.stringify(v)}</li>
        ))}
      </ul>
    )
  }
  if (typeof value === 'object') {
    return (
      <pre className="overflow-x-auto rounded-md border bg-muted/40 p-3 font-mono text-xs">
        {JSON.stringify(value, null, 2)}
      </pre>
    )
  }
  if (isDiff) {
    return (
      <pre className="overflow-x-auto rounded-md border bg-muted/40 p-3 font-mono text-xs">
        {String(value)}
      </pre>
    )
  }
  return <p className="text-sm text-muted-foreground">{String(value)}</p>
}

function SectionField({ field, value }: { field: string; value: unknown }) {
  if (value == null || value === '' || (Array.isArray(value) && value.length === 0)) return null
  return (
    <div className="space-y-1">
      <h4 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
        {humanize(field)}
      </h4>
      <SectionValue field={field} value={value} />
    </div>
  )
}

// A "Proof of concept available" indicator driven ONLY by the poc_available
// flag. There is intentionally no path here that reads a PoC script body (P6).
function PocIndicator({ available }: { available: boolean }) {
  if (!available) return null
  return (
    <Badge variant="warning" className="gap-1">
      <FlaskConical className="h-3 w-3" aria-hidden />
      Proof of concept available
    </Badge>
  )
}

function ReportSection({
  report,
  keys,
  accent,
  icon,
}: {
  report: FindingReportView
  keys: readonly string[]
  accent: string
  icon: ReactNode
}) {
  const sections = report.sections ?? {}
  const knownKeys = new Set<string>(keys)
  // Render known keys first in their defined order, then any extra keys so
  // nothing in `sections` is dropped (poc_script_code is never present).
  const extraKeys = Object.keys(sections).filter(
    (k) => !knownKeys.has(k) && k !== 'poc_description' && k !== 'update_history',
  )
  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between gap-3">
        <div className="space-y-1">
          <CardTitle className={`flex items-center gap-2 text-base ${accent}`}>
            {icon}
            {report.team === 'red' ? 'Red Team' : 'Blue Team & Remediation'}
          </CardTitle>
          <CardDescription>{report.title}</CardDescription>
        </div>
        <div className="flex shrink-0 flex-wrap items-center justify-end gap-2">
          <Badge variant={severityVariant(report.severity)}>{report.severity || 'info'}</Badge>
          <PocIndicator available={report.poc_available} />
        </div>
      </CardHeader>
      <CardContent className="space-y-4">
        {keys.map((k) => (
          <SectionField key={k} field={k} value={(sections as Record<string, unknown>)[k]} />
        ))}
        {typeof sections.poc_description === 'string' && (
          <SectionField field="poc_description" value={sections.poc_description} />
        )}
        {extraKeys.map((k) => (
          <SectionField key={k} field={k} value={(sections as Record<string, unknown>)[k]} />
        ))}
      </CardContent>
    </Card>
  )
}

function CitedIntel({ intel, vulnId }: { intel: CanonicalVuln | null; vulnId?: string }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Cited intelligence</CardTitle>
        <CardDescription>The intelligence record backing this match.</CardDescription>
      </CardHeader>
      <CardContent>
        {intel ? (
          <div className="rounded-md border bg-muted/30 p-3 text-sm">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-mono">{intel.ID}</span>
              {intel.KnownExploited && <Badge variant="danger">KEV</Badge>}
              {typeof intel.EPSS === 'number' && (
                <span className="text-xs text-muted-foreground">EPSS {intel.EPSS.toFixed(3)}</span>
              )}
              {intel.CVSS?.Score != null && (
                <span className="text-xs text-muted-foreground">CVSS {intel.CVSS.Score}</span>
              )}
            </div>
            {intel.Summary && <p className="mt-2 text-muted-foreground">{intel.Summary}</p>}
            {(intel.Aliases ?? []).length > 0 && (
              <div className="mt-2 flex flex-wrap gap-1">
                {intel.Aliases.map((a) => (
                  <Badge key={a} variant="outline">
                    {a}
                  </Badge>
                ))}
              </div>
            )}
          </div>
        ) : (
          <p className="text-sm text-muted-foreground">
            No intelligence record resolved for <span className="font-mono">{vulnId}</span>.
          </p>
        )}
      </CardContent>
    </Card>
  )
}

function EvidenceTrail({ evidence }: { evidence: EvidenceDetail[] }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Evidence trail</CardTitle>
        <CardDescription>What the collectors observed for this match.</CardDescription>
      </CardHeader>
      <CardContent>
        {evidence.length === 0 ? (
          <p className="text-sm text-muted-foreground">No evidence recorded.</p>
        ) : (
          <ul className="space-y-2">
            {evidence.map((ev, i) => (
              <li key={i} className="rounded-md border bg-muted/30 p-3 text-sm">
                <Badge variant="ai">{ev.Collector}</Badge> observed{' '}
                <strong>{ev.ResourceType}</strong>{' '}
                <span className="font-mono text-xs text-muted-foreground">{ev.ResourceID}</span>
                <div className="mt-1 text-xs text-muted-foreground">
                  run {ev.RunID} · raw {String(ev.RawEventID).slice(0, 12)}…
                </div>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  )
}

// UpdateHistory renders the audit trail carried in the blue report's sections
// (if present). Accepts a list of { at, note }-ish entries.
function UpdateHistory({ history }: { history: unknown }) {
  if (!Array.isArray(history) || history.length === 0) return null
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Update history</CardTitle>
      </CardHeader>
      <CardContent>
        <ol className="space-y-2">
          {history.map((entry, i) => {
            const e = (entry ?? {}) as Record<string, unknown>
            const at = typeof e.at === 'string' ? e.at : undefined
            const note = typeof e.note === 'string' ? e.note : JSON.stringify(entry)
            return (
              <li key={i} className="rounded-md border bg-muted/30 p-3 text-sm">
                {at && <div className="text-xs text-muted-foreground">{at}</div>}
                <div>{note}</div>
              </li>
            )
          })}
        </ol>
      </CardContent>
    </Card>
  )
}

export function FindingDetail({ id }: { id: number | string }) {
  const { loading, error, data } = useAsync(() => realApi.finding(id), [id])

  if (loading) {
    return (
      <div className="flex h-40 items-center justify-center">
        <Spinner className="h-6 w-6" />
      </div>
    )
  }
  if (error) return <p className="text-sm text-destructive">Failed to load finding: {error}</p>
  if (!data) return null

  const { finding, intel, evidence, reports, triggered_by: triggeredBy } = data
  const red = reports.find((r) => r.team === 'red')
  const blue = reports.find((r) => r.team === 'blue')
  const updateHistory = (blue?.sections as Record<string, unknown> | undefined)?.update_history

  // The originating run link / triggered_by. 'manual' means an operator launch;
  // any other value is the monitor_events id that auto-triggered the run.
  const runLabel =
    !triggeredBy || triggeredBy === 'manual' ? 'Manual' : `Monitor event #${triggeredBy}`

  return (
    <div className="space-y-4">
      {/* Header */}
      <Card>
        <CardHeader className="space-y-3">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div className="space-y-1">
              <CardTitle className="text-xl">
                {finding.VulnID || `Finding #${finding.ID}`}
              </CardTitle>
              <CardDescription>
                <span className="font-mono">{finding.AssetID}</span>{' '}
                <span className="text-xs">({finding.AssetKind})</span>
              </CardDescription>
            </div>
            {/* The "Unvalidated" status is shown prominently for auto-triggered findings. */}
            <Badge variant="warning" className="text-sm">
              {finding.Status || 'Unvalidated'}
            </Badge>
          </div>
          <div className="flex flex-wrap items-center gap-2 text-sm">
            <Badge variant={severityVariant(finding.Severity)}>{finding.Severity || 'info'}</Badge>
            <span className="text-muted-foreground">risk</span>
            <span className="font-mono">{Math.round(finding.RiskScore)}</span>
            <span className="text-muted-foreground">·</span>
            <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
              <GitBranch className="h-3 w-3" aria-hidden />
              Triggered by: {runLabel}
            </span>
          </div>
        </CardHeader>
      </Card>

      {/* Red Team section — only when a red report exists. */}
      {red && (
        <ReportSection
          report={red}
          keys={RED_KEYS}
          accent="text-red-400"
          icon={<ShieldAlert className="h-4 w-4" aria-hidden />}
        />
      )}

      {/* Blue Team + remediation section — only when a blue report exists. */}
      {blue && (
        <ReportSection
          report={blue}
          keys={BLUE_KEYS}
          accent="text-emerald-400"
          icon={<ShieldCheck className="h-4 w-4" aria-hidden />}
        />
      )}

      {/* Cited intel + evidence render in every shape (including the no-report fallback). */}
      <CitedIntel intel={intel} vulnId={finding.VulnID} />
      <EvidenceTrail evidence={evidence} />

      {/* Update history, when the blue report carries it. */}
      <UpdateHistory history={updateHistory} />
    </div>
  )
}

export default FindingDetail
