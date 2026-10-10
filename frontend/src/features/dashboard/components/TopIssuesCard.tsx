import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Badge } from '@/components/ui/badge'
import type { BadgeProps } from '@/components/ui/badge'
import { cn } from '@/lib/utils'
import { CardMenu } from './CardMenu'
import { SEVERITY_DOT } from './severity'
import type { Severity } from '@/types/foundation'
import type { TopIssue } from '@/types/dashboard'

function sevVariant(s: Severity): BadgeProps['variant'] {
  if (s === 'critical' || s === 'high') return 'danger'
  if (s === 'medium') return 'warning'
  return 'secondary'
}

// Card 5 — Top Issues. A dense data table of top vulnerabilities: rule, issue
// count, risk tags, and a severity pill.
export function TopIssuesCard({ issues }: { issues: TopIssue[] }) {
  return (
    <Card>
      <CardHeader className="flex-row items-start justify-between space-y-0 p-4 pb-2">
        <div className="space-y-1">
          <CardTitle className="text-base">Top Issues</CardTitle>
          <CardDescription>{issues.length} rules</CardDescription>
        </div>
        <CardMenu label="Top issues options" />
      </CardHeader>
      <CardContent className="p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Rule</TableHead>
              <TableHead className="w-16 text-right">Issues</TableHead>
              <TableHead>Risks</TableHead>
              <TableHead className="w-24">Severity</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {issues.map((it) => (
              <TableRow key={it.id}>
                <TableCell>
                  <div className="flex items-start gap-2">
                    <span
                      className={cn('mt-1.5 h-2 w-2 shrink-0 rounded-full', SEVERITY_DOT[it.severity])}
                      aria-hidden
                    />
                    <span className="text-sm">{it.rule}</span>
                  </div>
                </TableCell>
                <TableCell className="text-right font-medium tabular-nums">{it.issues}</TableCell>
                <TableCell>
                  <div className="flex flex-wrap gap-1">
                    {it.risks.map((r) => (
                      <Badge key={r.id} variant="outline" className="text-[11px]">
                        {r.label}
                      </Badge>
                    ))}
                  </div>
                </TableCell>
                <TableCell>
                  <Badge variant={sevVariant(it.severity)} className="capitalize">
                    {it.severity}
                  </Badge>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  )
}
