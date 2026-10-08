import React, { useState } from 'react'
import { api } from '../api.js'
import { useAsync } from '../useAsync.js'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '../components/ui/card.jsx'
import { Select } from '../components/ui/input.jsx'
import { Button } from '../components/ui/button.jsx'
import { Badge, Table, THead, TBody, TR, TH, TD, Spinner } from '../components/ui/misc.jsx'

// Map a canonical severity string onto an available Badge variant. The misc
// Badge exposes danger/warning/success/secondary/outline, so critical+high
// share the red "danger" treatment, medium is amber "warning", low is the
// muted "secondary" chip, and anything else (info/empty) falls back to the
// subdued "outline" style.
const SEVERITY_VARIANT = {
  critical: 'danger',
  high: 'danger',
  medium: 'warning',
  low: 'secondary',
  info: 'outline',
}
function severityVariant(sev) {
  return SEVERITY_VARIANT[String(sev || '').toLowerCase()] || 'outline'
}

const SEVERITIES = ['', 'critical', 'high', 'medium', 'low', 'info']
const STATUSES = ['', 'open', 'resolved']

export default function Findings() {
  const [severity, setSeverity] = useState('')
  const [status, setStatus] = useState('')
  // selected is the finding id whose detail (cited intel + evidence) is shown.
  const [selected, setSelected] = useState(null)

  const { loading, error, data } = useAsync(
    () => api.findings({ severity, status, limit: 200 }),
    [severity, status],
  )
  const findings = data?.findings || []

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Findings</h1>
        <p className="text-sm text-muted-foreground">
          Risk-scored, evidence-backed matches between your assets and Bellu's intelligence corpus.
        </p>
      </div>

      <div className="flex flex-wrap items-center gap-3">
        <Select value={severity} onChange={(e) => setSeverity(e.target.value)} className="w-44">
          {SEVERITIES.map((s) => <option key={s} value={s}>{s || 'All severities'}</option>)}
        </Select>
        <Select value={status} onChange={(e) => setStatus(e.target.value)} className="w-44">
          {STATUSES.map((s) => <option key={s} value={s}>{s || 'All statuses'}</option>)}
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
            <div className="flex h-32 items-center justify-center"><Spinner /></div>
          ) : error ? (
            <p className="p-4 text-sm text-destructive">{error}</p>
          ) : (
            <Table>
              <THead>
                <TR>
                  <TH>Severity</TH><TH>Risk</TH><TH>Affected asset</TH>
                  <TH>Vulnerability</TH><TH>Status</TH><TH></TH>
                </TR>
              </THead>
              <TBody>
                {findings.map((f) => (
                  <TR key={f.ID}>
                    <TD>
                      <Badge variant={severityVariant(f.Severity)}>{f.Severity || 'info'}</Badge>
                    </TD>
                    <TD className="font-mono text-xs">{Math.round(f.RiskScore)}</TD>
                    <TD>
                      <div>{f.AssetID}</div>
                      <div className="text-xs text-muted-foreground">{f.AssetKind}</div>
                    </TD>
                    <TD className="font-mono text-xs text-muted-foreground">{f.VulnID}</TD>
                    <TD>
                      <Badge variant={f.Status === 'resolved' ? 'success' : 'outline'}>
                        {f.Status}
                      </Badge>
                    </TD>
                    <TD>
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => setSelected(f.ID)}
                      >
                        Investigate
                      </Button>
                    </TD>
                  </TR>
                ))}
                {findings.length === 0 && (
                  <TR><TD colSpan={6} className="text-center text-muted-foreground">No findings.</TD></TR>
                )}
              </TBody>
            </Table>
          )}
        </CardContent>
      </Card>

      {selected != null && (
        <FindingDetail id={selected} onClose={() => setSelected(null)} />
      )}
    </div>
  )
}

// FindingDetail loads a single finding together with the cited intelligence and
// its evidence trail (Requirement 14.3). The intel block links each affected
// package range back to the canonical vulnerability, and the evidence list
// shows the raw_events that justify the asset's presence in the graph.
function FindingDetail({ id, onClose }) {
  const { loading, error, data } = useAsync(() => api.finding(id), [id])
  const finding = data?.finding
  const intel = data?.intel
  const evidence = data?.evidence || []

  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between gap-3">
        <div>
          <CardTitle className="text-base">Finding detail</CardTitle>
          <CardDescription>Cited intelligence and the evidence behind this match.</CardDescription>
        </div>
        <Button variant="ghost" size="sm" onClick={onClose}>Close</Button>
      </CardHeader>
      <CardContent className="space-y-4">
        {loading && <div className="flex h-24 items-center justify-center"><Spinner /></div>}
        {error && <p className="text-sm text-destructive">{error}</p>}
        {!loading && !error && (
          <>
            {finding && (
              <div className="flex flex-wrap items-center gap-2 text-sm">
                <Badge variant={severityVariant(finding.Severity)}>{finding.Severity || 'info'}</Badge>
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
                      <span className="text-xs text-muted-foreground">EPSS {intel.EPSS.toFixed(3)}</span>
                    )}
                    {intel.CVSS?.Score != null && (
                      <span className="text-xs text-muted-foreground">CVSS {intel.CVSS.Score}</span>
                    )}
                  </div>
                  {intel.Summary && <p className="mt-2 text-muted-foreground">{intel.Summary}</p>}
                  {(intel.Aliases || []).length > 0 && (
                    <div className="mt-2 flex flex-wrap gap-1">
                      {intel.Aliases.map((a) => (
                        <Badge key={a} variant="outline">{a}</Badge>
                      ))}
                    </div>
                  )}
                  {(intel.Affected || []).length > 0 && (
                    <ul className="mt-2 space-y-1 text-xs text-muted-foreground">
                      {intel.Affected.map((ap, i) => (
                        <li key={i} className="font-mono">
                          {ap.Purl}
                          {(ap.Ranges || []).map((r, j) => (
                            <span key={j}> [{r.Introduced || '0'}, {r.Fixed || '∞'})</span>
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
                      <Badge variant="ai">{ev.Collector}</Badge>{' '}
                      observed <strong>{ev.ResourceType}</strong>{' '}
                      <span className="font-mono text-xs text-muted-foreground">{ev.ResourceID}</span>
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
