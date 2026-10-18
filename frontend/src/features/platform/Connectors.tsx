import { useCallback, useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  ArrowRight,
  FolderKanban,
  Loader2,
  Pencil,
  Play,
  Plus,
  RefreshCw,
  Search,
  SquareArrowOutUpRight,
  Trash2,
} from 'lucide-react'
import { client } from '@/lib/client'
import { useProject } from '@/project'
import { useAuth } from '@/auth'
import { cn } from '@/lib/utils'
import { Card, CardContent } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Spinner } from '@/components/ui/spinner'
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
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { ProviderIcon } from '@/features/ai-inventory/components/category'
import { DiscoveryProgress } from './DiscoveryProgress'
import type { Connection, ConnectorStatus } from '@/types/foundation'

function statusVariant(
  s: ConnectorStatus,
): 'default' | 'secondary' | 'success' | 'warning' | 'danger' {
  switch (s) {
    case 'COMPLETE':
    case 'CONNECTED':
      return 'success'
    case 'DISCOVERY_RUNNING':
    case 'VERIFYING':
      return 'warning'
    case 'ERROR':
      return 'danger'
    default:
      return 'secondary'
  }
}

// Per-provider header tint for the connected card. The band is a soft themed
// background; the icon sits on a round white/off-white badge so it stays legible
// in both light and dark themes regardless of the band color.
function providerTheme(provider: string): string {
  switch (provider.toLowerCase()) {
    case 'aws':
      return 'bg-amber-500/15'
    case 'gcp':
      return 'bg-blue-500/15'
    case 'azure':
      return 'bg-sky-500/15'
    case 'github':
      return 'bg-zinc-500/15'
    case 'gitlab':
      return 'bg-orange-500/15'
    case 'atlassian':
    case 'bitbucket':
      return 'bg-indigo-500/15'
    default:
      return 'bg-primary/10'
  }
}

// ScanRing is a circular progress donut. When running it shows the live percent
// (tables scanned) with a pulsing accent; when idle it shows the last-scan
// outcome glyph. It is a button so clicking opens the progress sheet.
function ScanRing({
  pct,
  running,
  tone,
  onClick,
  title,
}: {
  pct: number
  running: boolean
  tone: 'running' | 'ok' | 'error' | 'idle'
  onClick: () => void
  title: string
}) {
  const r = 13
  const circ = 2 * Math.PI * r
  const dash = (Math.min(100, Math.max(0, pct)) / 100) * circ
  const stroke =
    tone === 'error'
      ? 'stroke-destructive'
      : tone === 'ok'
        ? 'stroke-emerald-500'
        : tone === 'running'
          ? 'stroke-sky-500'
          : 'stroke-muted-foreground/40'
  return (
    <button
      type="button"
      onClick={onClick}
      title={title}
      aria-label={title}
      className="relative flex h-9 w-9 shrink-0 items-center justify-center rounded-full transition-transform hover:scale-105"
    >
      <svg viewBox="0 0 32 32" className={cn('h-9 w-9 -rotate-90', running && 'animate-pulse')}>
        <circle cx="16" cy="16" r={r} className="fill-none stroke-muted" strokeWidth="3" />
        <circle
          cx="16"
          cy="16"
          r={r}
          className={cn('fill-none transition-all', stroke)}
          strokeWidth="3"
          strokeLinecap="round"
          strokeDasharray={`${dash} ${circ}`}
        />
      </svg>
      <span className="absolute text-[9px] font-semibold tabular-nums">
        {running ? `${pct}%` : tone === 'ok' ? '✓' : tone === 'error' ? '!' : '—'}
      </span>
    </button>
  )
}

// The providers Bellu can connect, each shown as a card. Cloud providers scan
// an account/subscription/project; CI/VCS providers connect source platforms
// (GitHub reuses the code-security GitHub App).
type ConnectorCategory = 'cloud' | 'ci'

interface ProviderCard {
  id: string
  name: string
  description: string
  category: ConnectorCategory
  available: boolean
}

const PROVIDERS: ProviderCard[] = [
  // Cloud.
  {
    id: 'aws',
    name: 'Amazon Web Services',
    description: 'Scan an AWS account or organization for cloud security posture.',
    category: 'cloud',
    available: true,
  },
  {
    id: 'gcp',
    name: 'Google Cloud',
    description: 'Connect a Google Cloud project to map its security posture.',
    category: 'cloud',
    available: true,
  },
  {
    id: 'azure',
    name: 'Microsoft Azure',
    description: 'Connect an Azure subscription for cloud visibility.',
    category: 'cloud',
    available: true,
  },
  // CI / VCS.
  {
    id: 'github',
    name: 'GitHub',
    description: 'Connect repositories via the Bellu GitHub App for code security.',
    category: 'ci',
    available: true,
  },
  {
    id: 'gitlab',
    name: 'GitLab',
    description: 'Connect GitLab projects with a read-only access token.',
    category: 'ci',
    available: true,
  },
  {
    id: 'atlassian',
    name: 'Atlassian / Bitbucket',
    description: 'Connect Bitbucket repositories with a read-only app password.',
    category: 'ci',
    available: true,
  },
]

const PROVIDER_NAME: Record<string, string> = Object.fromEntries(
  PROVIDERS.map((p) => [p.id, p.name]),
)

export default function ConnectorsPage() {
  const { current, projects } = useProject()
  const { can } = useAuth()
  const navigate = useNavigate()
  const canManage = can('integrations:manage')
  const projectId = current?.id

  // Map project id -> name for the per-connector project label.
  const projectName = (id: number) => projects.find((p) => p.id === id)?.name ?? `Project ${id}`

  const [conns, setConns] = useState<Connection[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [deleting, setDeleting] = useState<number>(0)
  const [scanning, setScanning] = useState<number>(0)

  // Connected-section search + filter.
  const [query, setQuery] = useState('')
  const [providerFilter, setProviderFilter] = useState('all')
  const [statusFilter, setStatusFilter] = useState('all')

  // Progress sheet: which connection's scan progress is open (null = closed).
  const [sheetConn, setSheetConn] = useState<Connection | null>(null)

  // Live scan percent per connection id, polled while a scan is running so the
  // ring on the card updates without opening the sheet.
  const [livePct, setLivePct] = useState<Record<number, number>>({})

  // reload shows the full-page spinner — use only for the initial load and
  // explicit Refresh. Background updates use refreshQuiet to avoid flicker.
  const reload = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const { connections } = await client.connectors(projectId)
      setConns(connections ?? [])
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [projectId])

  // refreshQuiet re-fetches connections WITHOUT toggling the loading spinner,
  // and only replaces state when something actually changed, so a background
  // poll does not remount/flicker the card grid.
  const refreshQuiet = useCallback(async () => {
    try {
      const { connections } = await client.connectors(projectId)
      const next = connections ?? []
      setConns((prev) => {
        // Shallow-compare id+status+account to avoid pointless re-renders.
        const same =
          prev.length === next.length &&
          prev.every((p, i) => {
            const n = next[i]
            return n && p.id === n.id && p.status === n.status && p.account_id === n.account_id
          })
        return same ? prev : next
      })
    } catch {
      /* transient: keep showing current data */
    }
  }, [projectId])

  useEffect(() => {
    void reload()
  }, [reload])

  // Stable key of the currently-running connection ids. The polling effect
  // depends on THIS (not the whole conns array), so it re-subscribes only when
  // the set of running scans changes — not on every background refresh.
  const runningKey = useMemo(
    () =>
      conns
        .filter((c) => c.status === 'DISCOVERY_RUNNING')
        .map((c) => c.id)
        .sort((a, b) => a - b)
        .join(','),
    [conns],
  )

  // While any connector is scanning, poll its status to drive the ring percent
  // and quietly refresh the list when a scan finishes (so the badge flips
  // without a full-page spinner / flicker).
  useEffect(() => {
    if (!runningKey) return
    const ids = runningKey.split(',').map(Number)
    let cancelled = false
    const tick = async () => {
      let anyTerminal = false
      await Promise.all(
        ids.map(async (id) => {
          try {
            const { status } = await client.connectorStatus(id)
            const p = status.latest_session?.progress
            if (p && p.tables_total > 0) {
              const pct = Math.round((p.tables_done / p.tables_total) * 100)
              if (!cancelled) setLivePct((cur) => (cur[id] === pct ? cur : { ...cur, [id]: pct }))
            }
            if (status.latest_session && status.latest_session.status !== 'running') {
              anyTerminal = true
            }
          } catch {
            /* transient */
          }
        }),
      )
      if (anyTerminal && !cancelled) await refreshQuiet()
    }
    void tick()
    const timer = setInterval(tick, 2000)
    return () => {
      cancelled = true
      clearInterval(timer)
    }
  }, [runningKey, refreshQuiet])

  async function remove(id: number) {
    if (!window.confirm('Delete this connection? This cannot be undone.')) return
    setDeleting(id)
    setError(null)
    try {
      await client.deleteConnector(id)
      await reload()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setDeleting(0)
    }
  }

  async function scan(id: number) {
    setScanning(id)
    setError(null)
    try {
      const res = await client.discoverConnector(id)
      // Reflect the new session's state on just this connection (no full reload,
      // so the grid does not flicker). Git scans return a terminal session
      // synchronously; cloud scans return a running one and the poll takes over.
      const sessStatus = res?.session?.status
      if (sessStatus) {
        const mapped: ConnectorStatus =
          sessStatus === 'running'
            ? 'DISCOVERY_RUNNING'
            : sessStatus === 'failed'
              ? 'ERROR'
              : 'COMPLETE'
        setConns((cur) => cur.map((c) => (c.id === id ? { ...c, status: mapped } : c)))
      }
      // Quiet reconcile against the server (picks up account_id/capabilities).
      await refreshQuiet()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setScanning(0)
    }
  }

  function connect(p: ProviderCard) {
    if (!p.available || !canManage) return
    navigate(`/connectors/new/${p.id}`)
  }

  // A provider is "one connection per project": once connected here, it is not
  // offered again in the available grid.
  const connectedProviders = useMemo(
    () => new Set(conns.map((c) => c.provider.toLowerCase())),
    [conns],
  )

  // Available providers = catalog minus the ones already connected.
  const availableCloud = PROVIDERS.filter(
    (p) => p.category === 'cloud' && !connectedProviders.has(p.id),
  )
  const availableCi = PROVIDERS.filter(
    (p) => p.category === 'ci' && !connectedProviders.has(p.id),
  )

  // Connected list filtered by search + provider + status.
  const filteredConns = useMemo(() => {
    const q = query.trim().toLowerCase()
    return conns.filter((c) => {
      if (providerFilter !== 'all' && c.provider.toLowerCase() !== providerFilter) return false
      if (statusFilter !== 'all' && c.status !== statusFilter) return false
      if (!q) return true
      const name = (c.account_id || '').toLowerCase()
      return (
        name.includes(q) ||
        c.provider.toLowerCase().includes(q) ||
        (PROVIDER_NAME[c.provider] ?? '').toLowerCase().includes(q)
      )
    })
  }, [conns, query, providerFilter, statusFilter])

  function providerGrid(items: ProviderCard[]) {
    return (
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {items.map((p) => {
          const clickable = p.available && canManage
          return (
            <button
              key={p.id}
              type="button"
              onClick={() => connect(p)}
              disabled={!clickable}
              className={cn(
                'group flex flex-col overflow-hidden rounded-lg border text-left transition-colors',
                clickable
                  ? 'cursor-pointer bg-card hover:border-primary/50'
                  : 'cursor-default bg-muted/30 opacity-80',
              )}
            >
              {/* Themed header strip: round white icon badge + provider name,
                  matching the connected cards. */}
              <div className={cn('flex items-center gap-3 p-4', providerTheme(p.id))}>
                <span className="flex h-10 w-10 items-center justify-center rounded-full bg-white shadow-sm ring-1 ring-black/5">
                  <ProviderIcon provider={p.id} className="h-6 w-6" />
                </span>
                <div className="min-w-0 flex-1">
                  <div className="truncate text-sm font-semibold">{p.name}</div>
                </div>
                {p.available ? (
                  canManage ? (
                    <ArrowRight className="h-4 w-4 text-muted-foreground transition-transform group-hover:translate-x-0.5" />
                  ) : null
                ) : (
                  <Badge variant="secondary" className="text-[10px]">
                    Soon
                  </Badge>
                )}
              </div>
              <div className="flex flex-1 flex-col gap-3 p-4">
                <p className="text-sm text-muted-foreground">{p.description}</p>
                {p.available && canManage && (
                  <span className="mt-auto inline-flex items-center gap-1 text-xs font-medium text-primary">
                    <Plus className="h-3.5 w-3.5" /> Connect
                  </span>
                )}
              </div>
            </button>
          )
        })}
      </div>
    )
  }

  const hasAvailable = availableCloud.length > 0 || availableCi.length > 0

  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Connectors</h1>
          <p className="text-sm text-muted-foreground">
            Connect a cloud account or source platform so Bellu can collect and map its security
            posture.
            {current ? (
              <>
                {' '}
                Project: <span className="font-medium text-foreground">{current.name}</span>.
              </>
            ) : null}
          </p>
        </div>
        <Button variant="outline" size="sm" onClick={() => void reload()}>
          <RefreshCw className="h-4 w-4" /> Refresh
        </Button>
      </div>

      {error && <p className="text-sm text-destructive">{error}</p>}

      {/* Connected (primary section): searchable + filterable cards at the top. */}
      <section className="space-y-3">
        <div className="flex flex-wrap items-center gap-2">
          <h2 className="text-sm font-medium text-muted-foreground">Connected</h2>
          <span className="text-xs text-muted-foreground">({filteredConns.length})</span>
          <div className="ml-auto flex flex-wrap items-center gap-2">
            <div className="relative">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="Search connectors"
                className="h-8 w-52 pl-8 text-xs"
              />
            </div>
            <Select value={providerFilter} onValueChange={setProviderFilter}>
              <SelectTrigger className="h-8 w-36 text-xs">
                <SelectValue placeholder="Provider" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">All providers</SelectItem>
                {[...connectedProviders].map((p) => (
                  <SelectItem key={p} value={p}>
                    {PROVIDER_NAME[p] ?? p}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Select value={statusFilter} onValueChange={setStatusFilter}>
              <SelectTrigger className="h-8 w-36 text-xs">
                <SelectValue placeholder="Status" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">All statuses</SelectItem>
                <SelectItem value="CONNECTED">Connected</SelectItem>
                <SelectItem value="COMPLETE">Complete</SelectItem>
                <SelectItem value="DISCOVERY_RUNNING">Scanning</SelectItem>
                <SelectItem value="VERIFYING">Verifying</SelectItem>
                <SelectItem value="PENDING">Pending</SelectItem>
                <SelectItem value="ERROR">Error</SelectItem>
              </SelectContent>
            </Select>
          </div>
        </div>

        {loading ? (
          <div className="flex h-32 items-center justify-center">
            <Spinner />
          </div>
        ) : conns.length === 0 ? (
          <Card>
            <CardContent className="py-10 text-center text-sm text-muted-foreground">
              No connectors yet{current ? ` in ${current.name}` : ''}. Pick a provider below to get
              started.
            </CardContent>
          </Card>
        ) : filteredConns.length === 0 ? (
          <Card>
            <CardContent className="py-10 text-center text-sm text-muted-foreground">
              No connectors match the current search or filters.
            </CardContent>
          </Card>
        ) : (
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {filteredConns.map((c) => (
              <Card key={c.id} className="overflow-hidden">
                {/* Themed header band: round white icon badge + title + scan ring. */}
                <div className={cn('flex items-center gap-3 p-4', providerTheme(c.provider))}>
                  <span className="flex h-10 w-10 items-center justify-center rounded-full bg-white shadow-sm ring-1 ring-black/5">
                    <ProviderIcon provider={c.provider} className="h-6 w-6" />
                  </span>
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-sm font-semibold">
                      {c.account_id || PROVIDER_NAME[c.provider] || c.provider}
                    </div>
                    <div className="text-xs capitalize text-muted-foreground">{c.scope} scope</div>
                  </div>
                  {(() => {
                    const running = c.status === 'DISCOVERY_RUNNING'
                    const tone: 'running' | 'ok' | 'error' | 'idle' = running
                      ? 'running'
                      : c.status === 'ERROR'
                        ? 'error'
                        : c.status === 'CONNECTED' || c.status === 'COMPLETE'
                          ? 'ok'
                          : 'idle'
                    const pct = running ? (livePct[c.id] ?? 0) : tone === 'ok' ? 100 : 0
                    return (
                      <ScanRing
                        pct={pct}
                        running={running}
                        tone={tone}
                        onClick={() => setSheetConn(c)}
                        title={running ? 'Scanning — view progress' : 'View last scan'}
                      />
                    )
                  })()}
                </div>

                <CardContent className="space-y-3 p-4">
                  {/* Status + project label */}
                  <div className="flex items-center gap-2">
                    <Badge variant={statusVariant(c.status)}>{c.status}</Badge>
                    <span className="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
                      <FolderKanban className="h-3.5 w-3.5 shrink-0" />
                      <span className="truncate">{projectName(c.project_id)}</span>
                    </span>
                  </div>

                  {(c.capabilities ?? []).length > 0 && (
                    <div>
                      <div className="mb-1 text-xs text-muted-foreground">Capabilities</div>
                      <div className="flex flex-wrap gap-1">
                        {(c.capabilities ?? []).map((cap) => (
                          <Badge key={cap} variant="outline" className="text-[10px]">
                            {cap}
                          </Badge>
                        ))}
                      </div>
                    </div>
                  )}

                  {/* Actions: Open, Edit, Scan (+ Delete). */}
                  <div className="flex items-center gap-2 pt-1">
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => navigate(`/connectors/new/${c.provider}?id=${c.id}`)}
                    >
                      <SquareArrowOutUpRight className="h-3.5 w-3.5" /> Open
                    </Button>
                    {canManage && (
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => navigate(`/connectors/new/${c.provider}?id=${c.id}&edit=1`)}
                      >
                        <Pencil className="h-3.5 w-3.5" /> Edit
                      </Button>
                    )}
                    {canManage && (
                      <Button
                        size="sm"
                        onClick={() => void scan(c.id)}
                        disabled={scanning === c.id || c.status === 'DISCOVERY_RUNNING'}
                      >
                        {scanning === c.id ? (
                          <Loader2 className="h-3.5 w-3.5 animate-spin" />
                        ) : (
                          <Play className="h-3.5 w-3.5" />
                        )}
                        Scan
                      </Button>
                    )}
                    {canManage && (
                      <button
                        onClick={() => remove(c.id)}
                        disabled={deleting === c.id}
                        className="ml-auto text-muted-foreground hover:text-destructive disabled:opacity-50"
                        title="Delete connection"
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </button>
                    )}
                  </div>
                </CardContent>
              </Card>
            ))}
          </div>
        )}
      </section>

      {/* Available providers to connect (one connection per provider per
          project, so connected providers are not shown here). */}
      {hasAvailable ? (
        <section className="space-y-5">
          <h2 className="text-sm font-medium text-muted-foreground">Add a connector</h2>
          {availableCloud.length > 0 && (
            <div className="space-y-3">
              <h3 className="text-xs font-medium uppercase tracking-wider text-muted-foreground">
                Cloud
              </h3>
              {providerGrid(availableCloud)}
            </div>
          )}
          {availableCi.length > 0 && (
            <div className="space-y-3">
              <h3 className="text-xs font-medium uppercase tracking-wider text-muted-foreground">
                CI / Source control
              </h3>
              {providerGrid(availableCi)}
            </div>
          )}
        </section>
      ) : (
        conns.length > 0 && (
          <p className="text-sm text-muted-foreground">
            All available providers are connected for this project.
          </p>
        )
      )}

      {/* Scan progress sheet (slides in from the right): live progress while a
          scan runs, or the last scan's result when idle. */}
      <Sheet open={sheetConn !== null} onOpenChange={(o) => !o && setSheetConn(null)}>
        <SheetContent className="w-full overflow-y-auto sm:max-w-md">
          {sheetConn && (
            <>
              <SheetHeader>
                <SheetTitle className="flex items-center gap-2">
                  <span className="flex h-8 w-8 items-center justify-center rounded-full bg-white shadow-sm ring-1 ring-black/5">
                    <ProviderIcon provider={sheetConn.provider} className="h-5 w-5" />
                  </span>
                  {sheetConn.account_id || PROVIDER_NAME[sheetConn.provider] || sheetConn.provider}
                </SheetTitle>
                <SheetDescription>
                  {sheetConn.status === 'DISCOVERY_RUNNING'
                    ? 'Live scan progress.'
                    : 'Most recent scan.'}
                </SheetDescription>
              </SheetHeader>
              <div className="mt-4">
                <DiscoveryProgress
                  connectionId={sheetConn.id}
                  onDone={() => void reload()}
                />
              </div>
            </>
          )}
        </SheetContent>
      </Sheet>
    </div>
  )
}
