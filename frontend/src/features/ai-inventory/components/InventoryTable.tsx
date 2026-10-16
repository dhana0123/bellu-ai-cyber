import { useEffect, useMemo, useState } from 'react'
import ReactFlow, { Background, Controls, Position, type Edge, type Node } from 'reactflow'
import 'reactflow/dist/style.css'
import dagre from 'dagre'
import {
  BarChart3,
  Boxes,
  ChevronDown,
  ChevronRight,
  MoreVertical,
  Network,
  Settings2,
  Users,
  Workflow,
} from 'lucide-react'
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
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { Spinner } from '@/components/ui/spinner'
import { client } from '@/lib/client'
import { cn } from '@/lib/utils'
import { useTheme } from '@/theme'
import GraphNode, { NODE_W, NODE_H, type GraphNodeData } from '@/components/graph/GraphNode'
import { CategoryIcon, KindIcon, ProviderIcon } from './category'
import type { InventoryAsset, ReviewStatus } from '@/types/aiInventory'
import type { EdgeDetail } from '@/types/foundation'

type FlowNode = Node<GraphNodeData>
const inventoryNodeTypes = { entity: GraphNode }

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

            <Tabs defaultValue="overview" className="mt-5">
              <TabsList>
                <TabsTrigger value="overview">Overview</TabsTrigger>
                <TabsTrigger value="graph">View Graph</TabsTrigger>
              </TabsList>

              {/* Overview: basic details first, with full attributes behind a
                  "View more details" expander. */}
              <TabsContent value="overview" className="mt-4 space-y-5">
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
                    <div className="mb-1 text-xs font-medium text-muted-foreground">
                      Canonical ID
                    </div>
                    <code className="block break-all rounded-md bg-muted px-2 py-1.5 font-mono text-xs">
                      {asset.canonical_id}
                    </code>
                  </div>
                ) : null}

                <MoreDetails attrKeys={attrKeys} attrs={attrs} />
              </TabsContent>

              {/* View Graph: this resource and its connected environment. */}
              <TabsContent value="graph" className="mt-4">
                <ConnectionsGraph asset={asset} />
              </TabsContent>
            </Tabs>
          </>
        ) : null}
      </SheetContent>
    </Sheet>
  )
}

// MoreDetails shows the full attribute list behind a collapsible toggle so the
// Overview stays scannable while still offering "View more details".
function MoreDetails({ attrKeys, attrs }: { attrKeys: string[]; attrs: Record<string, unknown> }) {
  const [open, setOpen] = useState(false)
  if (attrKeys.length === 0) {
    return <p className="text-xs text-muted-foreground">No additional attributes recorded.</p>
  }
  return (
    <div>
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="inline-flex items-center gap-1 text-xs font-medium text-primary hover:underline"
      >
        {open ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
        {open ? 'Hide details' : `View more details (${attrKeys.length})`}
      </button>
      {open && (
        <div className="mt-2 max-h-80 overflow-y-auto rounded-md border px-3">
          {attrKeys.map((k) => (
            <AttrRow key={k} k={k} v={attrs[k]} />
          ))}
        </div>
      )}
    </div>
  )
}

// kindFromCanonicalID derives the entity Kind from a canonical id of the form
// "<kindlower>:<provider>:<native-id>" (e.g. "datastore:aws:arn:..." ->
// "DataStore"). Falls back to a best-effort title-case of the first segment.
function kindFromCanonicalID(cid: string): string {
  const first = (cid.split(':')[0] || '').toLowerCase()
  const map: Record<string, string> = {
    cloudaccount: 'CloudAccount',
    identity: 'Identity',
    datastore: 'DataStore',
    compute: 'Compute',
    cluster: 'Cluster',
    workload: 'Workload',
    containerimage: 'ContainerImage',
    repository: 'Repository',
    package: 'Package',
    vulnerability: 'Vulnerability',
    aiagent: 'AIAgent',
    aimodel: 'AIModel',
    aiservice: 'AIService',
    mcpserver: 'MCPServer',
    networkendpoint: 'NetworkEndpoint',
    externalservice: 'ExternalService',
    prompt: 'Prompt',
  }
  return map[first] ?? 'default'
}

// shortLabel trims a canonical id down to a readable node label.
function graphLabel(cid: string): string {
  const parts = cid.split(':')
  const tail = parts[parts.length - 1] || cid
  return tail.length > 26 ? '…' + tail.slice(-26) : tail
}

// ConnectionsGraph renders the selected resource and its connected environment
// using the SAME reactflow graph component as the main graph view, with the
// clicked resource highlighted by a pulsing glow. Edge data comes from
// GET /api/edges/find (findEdges) filtered to this resource's canonical id.
function ConnectionsGraph({ asset }: { asset: InventoryAsset }) {
  const { theme } = useTheme()
  const isDark = theme === 'dark'
  const [edges, setEdges] = useState<EdgeDetail[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const id = asset.canonical_id ?? ''

  useEffect(() => {
    let cancelled = false
    setEdges(null)
    setError(null)
    if (!id) {
      setEdges([])
      return
    }
    client
      .findEdges('', id, 60)
      .then((r) => {
        if (!cancelled) setEdges(r.edges ?? [])
      })
      .catch((e) => {
        if (!cancelled) setError((e as Error).message)
      })
    return () => {
      cancelled = true
    }
  }, [id])

  const flow = useMemo(() => {
    if (!edges) return { nodes: [] as FlowNode[], flowEdges: [] as Edge[] }
    const nodeMap = new Map<string, FlowNode>()
    const edgeMap = new Map<string, Edge>()

    const ensure = (cid: string, highlight = false) => {
      if (!nodeMap.has(cid)) {
        nodeMap.set(cid, {
          id: cid,
          type: 'entity',
          position: { x: 0, y: 0 },
          data: { kind: kindFromCanonicalID(cid), label: graphLabel(cid), highlight },
        })
      } else if (highlight) {
        nodeMap.get(cid)!.data.highlight = true
      }
    }
    // Always include the focused resource, highlighted.
    ensure(id, true)

    for (const e of edges) {
      if (e.SrcID !== id && e.DstID !== id) continue
      ensure(e.SrcID)
      ensure(e.DstID)
      const eid = `${e.SrcID}__${e.Relation}__${e.DstID}`
      if (!edgeMap.has(eid)) {
        edgeMap.set(eid, {
          id: eid,
          source: e.SrcID,
          target: e.DstID,
          label: e.Relation,
          labelShowBg: true,
          labelStyle: { fill: isDark ? '#d4d4d8' : '#374151', fontSize: 9, fontWeight: 500 },
          labelBgPadding: [4, 2],
          labelBgBorderRadius: 4,
          labelBgStyle: { fill: isDark ? '#2d2d2d' : '#f3f4f6', fillOpacity: 0.95 },
          style: { stroke: isDark ? '#6b7280' : '#9ca3af', strokeWidth: 2 },
        })
      }
    }
    const laid = layoutInventoryGraph([...nodeMap.values()], [...edgeMap.values()])
    return { nodes: laid, flowEdges: [...edgeMap.values()] }
  }, [edges, id])

  if (error) return <p className="text-sm text-destructive">{error}</p>
  if (edges === null) {
    return (
      <div className="flex h-72 items-center justify-center rounded-lg border">
        <Spinner />
      </div>
    )
  }
  if (flow.nodes.length <= 1) {
    return (
      <div className="rounded-md border bg-muted/30 p-6 text-center text-sm text-muted-foreground">
        No connected resources found for this item.
      </div>
    )
  }

  return (
    <div
      className="h-[460px] w-full overflow-hidden rounded-lg border"
      style={{ background: isDark ? '#1a1a1a' : '#ffffff' }}
    >
      <ReactFlow
        nodes={flow.nodes}
        edges={flow.flowEdges}
        nodeTypes={inventoryNodeTypes}
        fitView
        fitViewOptions={{ padding: 0.2 }}
        proOptions={{ hideAttribution: true }}
        nodesConnectable={false}
        minZoom={0.3}
        maxZoom={1.75}
      >
        <Background color={isDark ? '#333' : '#e5e7eb'} gap={22} size={1} />
        <Controls
          showInteractive={false}
          className={cn(
            '!border-border [&>button]:!border-border [&>button]:!fill-foreground',
            isDark
              ? '[&>button]:!bg-[#2d2d2d] [&>button:hover]:!bg-[#3a3a3a]'
              : '[&>button]:!bg-white [&>button:hover]:!bg-gray-100',
          )}
        />
      </ReactFlow>
    </div>
  )
}

// Dagre left-to-right layout for the inventory connections graph (mirrors the
// main TraceGraph layout so nodes read the same way).
function layoutInventoryGraph(nodes: FlowNode[], edges: Edge[]): FlowNode[] {
  const g = new dagre.graphlib.Graph()
  g.setDefaultEdgeLabel(() => ({}))
  g.setGraph({ rankdir: 'LR', nodesep: 50, ranksep: 110, marginx: 24, marginy: 24 })
  nodes.forEach((n) => g.setNode(n.id, { width: NODE_W, height: NODE_H }))
  edges.forEach((e) => g.setEdge(e.source, e.target))
  dagre.layout(g)
  return nodes.map((n) => {
    const p = g.node(n.id)
    return {
      ...n,
      sourcePosition: Position.Right,
      targetPosition: Position.Left,
      position: { x: p.x - NODE_W / 2, y: p.y - NODE_H / 2 },
    }
  })
}
