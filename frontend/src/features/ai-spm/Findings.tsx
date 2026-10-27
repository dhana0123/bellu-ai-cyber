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
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetDescription,
} from '@/components/ui/sheet'
import type { BadgeProps } from '@/components/ui/badge'
// The committed, spec'd Finding Detail page (red/blue team + remediation,
// Unvalidated status, cited intel, evidence, update history; PoC flag only).
import { FindingDetail } from '@/features/issues/FindingDetail'

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

      {/* Drill-in: the spec'd Finding Detail (with remediation) in a slide-over. */}
      <Sheet open={selected != null} onOpenChange={(o) => !o && setSelected(null)}>
        <SheetContent className="w-full overflow-y-auto sm:max-w-2xl">
          <SheetHeader>
            <SheetTitle>Finding detail</SheetTitle>
            <SheetDescription>
              Red/blue team findings and remediation for this match.
            </SheetDescription>
          </SheetHeader>
          <div className="mt-4">{selected != null && <FindingDetail id={selected} />}</div>
        </SheetContent>
      </Sheet>
    </div>
  )
}


