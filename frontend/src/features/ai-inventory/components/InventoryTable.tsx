import { BarChart3, Boxes, MoreVertical, Network, Settings2, Users, Workflow } from 'lucide-react'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import { CategoryIcon } from './category'
import type { InventoryAsset, ReviewStatus } from '@/types/aiInventory'

const STATUS_LABEL: Record<ReviewStatus, string> = {
  unreviewed: 'Unreviewed',
  reviewed: 'Reviewed',
  flagged: 'Flagged',
}
const STATUS_DOT: Record<ReviewStatus, string> = {
  unreviewed: 'border-muted-foreground',
  reviewed: 'border-emerald-500 bg-emerald-500',
  flagged: 'border-red-500 bg-red-500',
}

function UsageIcon({ kind }: { kind: InventoryAsset['usage_icon'] }) {
  const Icon = kind === 'agent' ? Workflow : kind === 'system' ? Boxes : Users
  return <Icon className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
}

// Row 3 — the dense technology inventory table.
export function InventoryTable({ assets }: { assets: InventoryAsset[] }) {
  return (
    <div className="rounded-lg border bg-card">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Technology</TableHead>
            <TableHead className="text-right">Active Connections</TableHead>
            <TableHead>Type</TableHead>
            <TableHead>Org. Usage</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>
              <span className="inline-flex items-center gap-1">
                Related Policies
                <Settings2 className="h-3.5 w-3.5 text-muted-foreground" />
              </span>
            </TableHead>
            <TableHead className="w-20 text-right">Actions</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {assets.map((a) => (
            <TableRow key={a.id}>
              <TableCell>
                <div className="flex items-center gap-3">
                  <CategoryIcon category={a.category} />
                  <div className="min-w-0">
                    <div className="truncate text-sm font-semibold text-foreground">{a.name}</div>
                    <div className="truncate text-xs text-muted-foreground">{a.subtitle}</div>
                  </div>
                </div>
              </TableCell>
              <TableCell className="text-right">
                <span className="inline-flex items-center gap-1 font-medium tabular-nums">
                  {a.connections}
                  <Network className="h-3.5 w-3.5 text-muted-foreground" />
                </span>
              </TableCell>
              <TableCell className="text-sm text-muted-foreground">{a.type}</TableCell>
              <TableCell>
                <span className="inline-flex items-center gap-1.5 text-sm text-muted-foreground">
                  <UsageIcon kind={a.usage_icon} />
                  {a.usage}
                </span>
              </TableCell>
              <TableCell>
                <span className="inline-flex items-center gap-1.5 text-xs">
                  <span className={cn('h-3 w-3 rounded-full border-2', STATUS_DOT[a.status])} />
                  {STATUS_LABEL[a.status]}
                </span>
              </TableCell>
              <TableCell />
              <TableCell>
                <div className="flex items-center justify-end gap-1">
                  <Button variant="ghost" size="icon" className="h-7 w-7" aria-label="View graph">
                    <BarChart3 className="h-4 w-4" />
                  </Button>
                  <Button variant="ghost" size="icon" className="h-7 w-7" aria-label="Row options">
                    <MoreVertical className="h-4 w-4" />
                  </Button>
                </div>
              </TableCell>
            </TableRow>
          ))}
          {assets.length === 0 && (
            <TableRow>
              <TableCell colSpan={7} className="py-10 text-center text-sm text-muted-foreground">
                No AI assets match the current filters.
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
    </div>
  )
}
