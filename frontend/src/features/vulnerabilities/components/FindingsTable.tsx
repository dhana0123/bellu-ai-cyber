import { MoreVertical } from 'lucide-react'
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
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import { CountBadges } from './CountBadges'
import type { Severity } from '@/types/foundation'
import type { Finding, FindingStatus } from '@/types/vulnerabilities'

function severityBadge(s: Severity): { variant: BadgeProps['variant']; cls?: string } {
  if (s === 'critical') return { variant: 'danger', cls: 'bg-red-500 text-white border-transparent' }
  if (s === 'high')
    return { variant: 'warning', cls: 'bg-orange-500 text-white border-transparent' }
  if (s === 'medium') return { variant: 'warning' }
  return { variant: 'secondary' }
}

const STATUS_LABEL: Record<FindingStatus, string> = {
  unresolved: 'Unresolved',
  in_progress: 'In Progress',
  resolved: 'Resolved',
}

function StatusTag({ status }: { status: FindingStatus }) {
  const cls =
    status === 'unresolved'
      ? 'border-red-500/40 text-red-500'
      : status === 'resolved'
        ? 'border-emerald-500/40 text-emerald-500'
        : 'border-amber-500/40 text-amber-500'
  return (
    <span className={cn('inline-flex rounded-md border px-2 py-0.5 text-xs font-medium', cls)}>
      {STATUS_LABEL[status]}
    </span>
  )
}

// Row 5 — the main findings inventory table.
export function FindingsTable({ findings }: { findings: Finding[] }) {
  return (
    <div className="rounded-lg border bg-card">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Resource</TableHead>
            <TableHead>Component</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>Severity</TableHead>
            <TableHead>Related Risk Issues</TableHead>
            <TableHead>Fix Version</TableHead>
            <TableHead>Detection Method</TableHead>
            <TableHead>Subscription</TableHead>
            <TableHead className="w-8" />
          </TableRow>
        </TableHeader>
        <TableBody>
          {findings.map((f) => {
            const sev = severityBadge(f.severity)
            return (
              <TableRow key={f.id}>
                <TableCell className="max-w-[260px]">
                  <div className="truncate font-mono text-xs" title={f.resource}>
                    {f.resource}
                  </div>
                  <div className="mt-0.5 text-[11px] text-muted-foreground">{f.resource_type}</div>
                </TableCell>
                <TableCell>
                  <div className="text-sm font-medium">{f.component}</div>
                  <div className="text-[11px] text-muted-foreground">{f.component_tech}</div>
                </TableCell>
                <TableCell>
                  <StatusTag status={f.status} />
                </TableCell>
                <TableCell>
                  <Badge variant={sev.variant} className={cn('capitalize', sev.cls)}>
                    {f.severity}
                  </Badge>
                </TableCell>
                <TableCell>
                  <CountBadges counts={f.related} />
                </TableCell>
                <TableCell className="whitespace-nowrap font-mono text-xs text-muted-foreground">
                  {f.fix_version}
                </TableCell>
                <TableCell className="text-xs text-muted-foreground">
                  {f.detection_method}
                </TableCell>
                <TableCell className="max-w-[160px] truncate font-mono text-xs text-muted-foreground">
                  {f.subscription}
                </TableCell>
                <TableCell>
                  <Button variant="ghost" size="icon" className="h-7 w-7" aria-label="Row options">
                    <MoreVertical className="h-4 w-4" />
                  </Button>
                </TableCell>
              </TableRow>
            )
          })}
          {findings.length === 0 && (
            <TableRow>
              <TableCell colSpan={9} className="py-10 text-center text-sm text-muted-foreground">
                No findings match the current filters.
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
    </div>
  )
}
