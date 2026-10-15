import { useState } from 'react'
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
import { Badge } from '@/components/ui/badge'
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetDescription,
} from '@/components/ui/sheet'
import { cn } from '@/lib/utils'
import { CategoryIcon, KindIcon, ProviderIcon } from './category'
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
  // Selected row -> opens the right-side detail sheet.
  const [selected, setSelected] = useState<InventoryAsset | null>(null)

  return (
    <div className="rounded-lg border bg-card">
      <ResourceDetailSheet asset={selected} onClose={() => setSelected(null)} />
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
            <TableRow
              key={a.id}
              onClick={() => setSelected(a)}
              className="cursor-pointer hover:bg-accent/50"
            >
              <TableCell>
                <div className="flex items-center gap-3">
                  {a.kind ? <KindIcon kind={a.kind} /> : <CategoryIcon category={a.category} />}
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
                  {a.provider ? (
                    <ProviderIcon provider={a.provider} className="h-4 w-4 shrink-0" />
                  ) : (
                    <UsageIcon kind={a.usage_icon} />
                  )}
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
              <TableCell onClick={(e) => e.stopPropagation()}>
                <div className="flex items-center justify-end gap-1">
                  <Button
                    variant="ghost"
                    size="icon"
                    className="h-7 w-7"
                    aria-label="View details"
                    onClick={() => setSelected(a)}
                  >
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

// --- Resource detail (right-side slide-over) --------------------------------

// Render a scalar attribute value as a short string; objects/arrays become JSON.
function renderValue(v: unknown): string {
  if (v === null || v === undefined) return '—'
  if (typeof v === 'string') return v
  if (typeof v === 'number' || typeof v === 'boolean') return String(v)
  try {
    return JSON.stringify(v)
  } catch {
    return String(v)
  }
}

function AttrRow({ k, v }: { k: string; v: unknown }) {
  return (
    <div className="grid grid-cols-[160px_1fr] gap-3 border-b py-2 last:border-b-0">
      <span className="truncate font-mono text-xs text-muted-foreground">{k}</span>
      <span className="break-words text-xs text-foreground">{renderValue(v)}</span>
    </div>
  )
}

function Field({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div>
      <div className="mb-1 text-xs font-medium text-muted-foreground">{label}</div>
      <div className="text-sm text-foreground">{value}</div>
    </div>
  )
}

// ResourceDetailSheet shows the full details of a selected inventory resource in
// a right-side slide-over: identity (kind/provider/status/canonical id) plus the
// raw discovered attributes.
function ResourceDetailSheet({
  asset,
  onClose,
}: {
  asset: InventoryAsset | null
  onClose: () => void
}) {
  const attrs = asset?.attributes ?? {}
  const attrKeys = Object.keys(attrs).sort()

  return (
    <Sheet open={!!asset} onOpenChange={(o) => (!o ? onClose() : undefined)}>
      <SheetContent side="right" className="w-full overflow-y-auto sm:max-w-xl">
        {asset ? (
          <>
            <SheetHeader>
              <div className="flex items-center gap-3">
                {asset.kind ? (
                  <KindIcon kind={asset.kind} />
                ) : (
                  <CategoryIcon category={asset.category} />
                )}
                <div className="min-w-0 text-left">
                  <SheetTitle className="truncate">{asset.name}</SheetTitle>
                  <SheetDescription className="truncate">{asset.subtitle}</SheetDescription>
                </div>
              </div>
            </SheetHeader>

            <div className="mt-6 space-y-6">
              <div className="grid grid-cols-2 gap-3">
                <Field label="Kind" value={asset.kind ?? asset.type} />
                <Field
                  label="Provider"
                  value={
                    <span className="inline-flex items-center gap-1.5">
                      <ProviderIcon provider={asset.provider} className="h-4 w-4" />
                      {(asset.provider ?? '').toUpperCase() || '—'}
                    </span>
                  }
                />
                <Field
                  label="Status"
                  value={
                    <Badge
                      variant={
                        asset.status === 'flagged'
                          ? 'danger'
                          : asset.status === 'reviewed'
                            ? 'success'
                            : 'secondary'
                      }
                    >
                      {asset.status}
                    </Badge>
                  }
                />
                <Field label="Connections" value={String(asset.connections)} />
              </div>

              {asset.canonical_id ? (
                <div>
                  <div className="mb-1 text-xs font-medium text-muted-foreground">Canonical ID</div>
                  <code className="block break-all rounded-md bg-muted px-2 py-1.5 font-mono text-xs">
                    {asset.canonical_id}
                  </code>
                </div>
              ) : null}

              <div>
                <div className="mb-1 text-xs font-medium text-muted-foreground">
                  Attributes {attrKeys.length ? `(${attrKeys.length})` : ''}
                </div>
                {attrKeys.length === 0 ? (
                  <p className="text-xs text-muted-foreground">No attributes recorded.</p>
                ) : (
                  <div className="rounded-md border px-3">
                    {attrKeys.map((k) => (
                      <AttrRow key={k} k={k} v={attrs[k]} />
                    ))}
                  </div>
                )}
              </div>
            </div>
          </>
        ) : null}
      </SheetContent>
    </Sheet>
  )
}
