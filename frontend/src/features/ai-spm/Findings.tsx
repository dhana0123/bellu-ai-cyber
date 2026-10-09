import { useState } from 'react'
import { client } from '@/lib/client'
import { useAsync } from '@/useAsync'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Spinner } from '@/components/ui/spinner'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import type { BadgeProps } from '@/components/ui/badge'
import type { FindingDetailResponse } from '@/types/foundation'

const ALL = '__all__'

const SEVERITY_VARIANT: Record<string, BadgeProps['variant']> = {
  critical: 'danger',
  high: 'danger',
  medium: 'warning',
  low: 'secondary',
  info: 'outline',
}
function severityVariant(sev: string): BadgeProps['variant'] {
  return SEVERITY_VARIANT[String(sev || '').toLowerCase()] || 'outline'
}

const SEVERITIES = ['critical', 'high', 'medium', 'low', 'info']
const STATUSES = ['open', 'resolved']

export default function Findings() {
  const [severity, setSeverity] = useState<string>(ALL)
  const [status, setStatus] = useState<string>(ALL)
  const [selected, setSelected] = useState<number | null>(null)

  const sev = severity === ALL ? '' : severity
  const st = status === ALL ? '' : status

  const { loading, error, data } = useAsync(
    () => client.findings({ severity: sev, status: st, limit: 200 }),
    [sev, st],
  )
  const findings = data?.findings ?? []

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Findings</h1>
        <p className="text-sm text-muted-foreground">
          Risk-scored, evidence-backed matches between your assets and Bellu's intelligence corpus.
        </p>
      </div>

      <div className="flex flex-wrap items-center gap-3">
        <Select value={severity} onValueChange={setSeverity}>
          <SelectTrigger className="w-44">
            <SelectValue placeholder="All severities" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL}>All severities</SelectItem>
            {SEVERITIES.map((s) => (
              <SelectItem key={s} value={s}>
                {s}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Select value={status} onValueChange={setStatus}>
          <SelectTrigger className="w-44">
            <SelectValue placeholder="All statuses" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL}>All statuses</SelectItem>
            {STATUSES.map((s) => (
              <SelectItem key={s} value={s}>
                {s}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <span className="text-xs text-muted-foreground">{findings.length} shown</span>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Findings</CardTitle>
          <CardDescription>
            Each row cites the intelligence record and evidence trail backing the match.
          </CardDescription>
        </CardHeader>
        <CardContent className="p-0">
          {loading ? (
            <div className="flex h-32 items-center justify-center">
              <Spinner />
            </div>
          ) : error ? (
            <p className="p-4 text-sm text-destructive">{error}</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Severity</TableHead>
                  <TableHead>Risk</TableHead>
                  <TableHead>Affected asset</TableHead>
                  <TableHead>Vulnerability</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead />
                </TableRow>
              </TableHeader>
              <TableBody>
                {findings.map((f) => (
                  <TableRow key={f.ID}>
                    <TableCell>
                      <Badge variant={severityVariant(f.Severity)}>{f.Severity || 'info'}</Badge>
                    </TableCell>
                    <TableCell className="font-mono text-xs">{Math.round(f.RiskScore)}</TableCell>
                    <TableCell>
                      <div>{f.AssetID}</div>
                      <div className="text-xs text-muted-foreground">{f.AssetKind}</div>
                    </TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      {f.VulnID}
                    </TableCell>
                    <TableCell>
                      <Badge variant={f.Status === 'resolved' ? 'success' : 'outline'}>
                        {f.Status}
                      </Badge>
                    </TableCell>
                    <TableCell>
                      <Button variant="outline" size="sm" onClick={() => setSelected(f.ID)}>
                        Investigate
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
                {findings.length === 0 && (
                  <TableRow>
                    <TableCell colSpan={6} className="text-center text-muted-foreground">
                      No findings.
                    </TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      {selected != null && <FindingDetail id={selected} onClose={() => setSelected(null)} />}
    </div>
  )
}

// FindingDetail loads a single finding with its cited intelligence and evidence
// trail. Field casing is PascalCase (backend has no json tags on these types).
function FindingDetail({ id, onClose }: { id: number; onClose: () => void }) {
  const { loading, error, data } = useAsync<FindingDetailResponse>(() => client.finding(id), [id])
  const finding = data?.finding
  const intel = data?.intel
  const evidence = data?.evidence ?? []

  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between gap-3">
        <div>
          <CardTitle className="text-base">Finding detail</CardTitle>
          <CardDescription>Cited intelligence and the evidence behind this match.</CardDescription>
        </div>
        <Button variant="ghost" size="sm" onClick={onClose}>
          Close
        </Button>
      </CardHeader>
      <CardContent className="space-y-4">
        {loading && (
          <div className="flex h-24 items-center justify-center">
            <Spinner />
          </div>
        )}
        {error && <p className="text-sm text-destructive">{error}</p>}
        {!loading && !error && (
          <>
            {finding && (
              <div className="flex flex-wrap items-center gap-2 text-sm">
                <Badge variant={severityVariant(finding.Severity)}>
                  {finding.Severity || 'info'}
                </Badge>
                <span className="text-muted-foreground">risk</span>
                <span className="font-mono">{Math.round(finding.RiskScore)}</span>
                <span className="text-muted-foreground">·</span>
                <span className="font-mono text-xs">{finding.AssetID}</span>
                <span className="text-xs text-muted-foreground">({finding.AssetKind})</span>
              </div>
            )}

            <div className="space-y-2">
              <h3 className="text-sm font-medium">Cited intelligence</h3>
              {intel ? (
                <div className="rounded-md border bg-muted/30 p-3 text-sm">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-mono">{intel.ID}</span>
                    {intel.KnownExploited && <Badge variant="danger">KEV</Badge>}
                    {typeof intel.EPSS === 'number' && (
                      <span className="text-xs text-muted-foreground">
                        EPSS {intel.EPSS.toFixed(3)}
                      </span>
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
                  {(intel.Affected ?? []).length > 0 && (
                    <ul className="mt-2 space-y-1 text-xs text-muted-foreground">
                      {intel.Affected.map((ap, i) => (
                        <li key={i} className="font-mono">
                          {ap.Purl}
                          {(ap.Ranges ?? []).map((r, j) => (
                            <span key={j}>
                              {' '}
                              [{r.Introduced || '0'}, {r.Fixed || '∞'})
                            </span>
                          ))}
                        </li>
                      ))}
                    </ul>
                  )}
                </div>
              ) : (
                <p className="text-sm text-muted-foreground">
                  No intelligence record resolved for{' '}
                  <span className="font-mono">{finding?.VulnID}</span>.
                </p>
              )}
            </div>

            <div className="space-y-2">
              <h3 className="text-sm font-medium">Evidence trail</h3>
              {evidence.length === 0 ? (
                <p className="text-sm text-muted-foreground">No evidence recorded.</p>
              ) : (
                <ul className="space-y-2">
                  {evidence.map((ev, i) => (
                    <li key={i} className="rounded-md border bg-muted/30 p-3 text-sm">
                      <Badge variant="ai">{ev.Collector}</Badge> observed{' '}
                      <strong>{ev.ResourceType}</strong>{' '}
                      <span className="font-mono text-xs text-muted-foreground">
                        {ev.ResourceID}
                      </span>
                      <div className="mt-1 text-xs text-muted-foreground">
                        run {ev.RunID} · raw {String(ev.RawEventID).slice(0, 12)}…
                      </div>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          </>
        )}
      </CardContent>
    </Card>
  )
}
