import { useEffect, useMemo, useState } from 'react'
import {
  ChevronDown,
  Columns3,
  FolderGit2,
  Globe,
  Lock,
  Plus,
  Search,
} from 'lucide-react'
import { client } from '@/lib/client'
import { useAsync } from '@/useAsync'
import { useProject } from '@/project'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Spinner } from '@/components/ui/spinner'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import type { RepositoriesData, Repository, SeverityCounts } from '@/types/repositories'

const GROUP_BY = ['None', 'Organization', 'Platform', 'Project', 'Service'] as const
type GroupBy = (typeof GROUP_BY)[number]

// Severity → dot/text color, matching the C/H/M/L chips in the design.
function relativeTime(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime()
  const h = Math.floor(diff / 3600_000)
  if (h < 1) return 'just now'
  if (h < 24) return `${h} hour${h > 1 ? 's' : ''} ago`
  const d = Math.floor(h / 24)
  return `${d} day${d > 1 ? 's' : ''} ago`
}

// A pill-style dropdown trigger used in the filter bar (visual only).
function FilterPill({ label }: { label: string }) {
  return (
    <button
      type="button"
      className="inline-flex items-center gap-1.5 rounded-md border bg-background px-3 py-1.5 text-xs text-muted-foreground hover:bg-accent"
    >
      {label}
      <ChevronDown className="h-3 w-3" />
    </button>
  )
}

const GIT_PROVIDERS = new Set(['github', 'gitlab', 'atlassian', 'bitbucket'])
const ZERO: SeverityCounts = { critical: 0, high: 0, medium: 0, low: 0 }

// mapConnectorRepo turns a real git-connector repository into a table row,
// reusing the existing Repository shape so the table UI is unchanged. Fields we
// have no real data for yet (issues/findings/policies/scan time) are zeroed —
// these fill in once code scanning runs.
// scanMeta carries the ids needed to toggle a repo's scan coverage against the
// backend. Kept separate from the shared Repository type (which is demo-shaped)
// and keyed by the row id.
type ScanMeta = { connId: number; repoId: number }

function mapConnectorRepo(
  r: { id: number; full_name: string; url: string; private: boolean; selected: boolean },
  platform: Repository['platform'],
  connId: number,
): { row: Repository; meta: ScanMeta } {
  const [org, name] = r.full_name.includes('/')
    ? [r.full_name.slice(0, r.full_name.indexOf('/')), r.full_name.slice(r.full_name.indexOf('/') + 1)]
    : ['', r.full_name]
  const id = `conn-${connId}-${r.id}`
  return {
    row: {
      id,
      name: name || r.full_name,
      organization: org,
      platform,
      project: org,
      visibility: r.private ? 'private' : 'public',
      scanning_enabled: r.selected,
      issues: { ...ZERO },
      findings: { ...ZERO },
      policies: 0,
      project_count: 0,
      last_scan: new Date().toISOString(),
    },
    meta: { connId, repoId: r.id },
  }
}

function platformOf(provider: string): Repository['platform'] {
  const p = provider.toLowerCase()
  if (p === 'gitlab') return 'gitlab'
  if (p === 'atlassian' || p === 'bitbucket') return 'bitbucket'
  return 'github'
}

export default function Repositories() {
  const { loading, error, data } = useAsync<RepositoriesData>(
    () => client.repositories.overview(),
    [],
  )
  const { current } = useProject()
  const projectId = current?.id
  const [tab, setTab] = useState('organizational')
  const [groupBy, setGroupBy] = useState<GroupBy>('None')
  const [query, setQuery] = useState('')

  // Real connected git repositories (GitHub/GitLab/Bitbucket). When one or more
  // git connectors exist, their repositories drive the table; otherwise the
  // page falls back to the demo overview so the layout still renders.
  const [connectorRepos, setConnectorRepos] = useState<Repository[]>([])
  const [scanMeta, setScanMeta] = useState<Record<string, ScanMeta>>({})
  const [hasGitConnector, setHasGitConnector] = useState(false)
  const [togglingId, setTogglingId] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    ;(async () => {
      try {
        const { connections } = await client.connectors(projectId)
        const git = (connections ?? []).filter((c) =>
          GIT_PROVIDERS.has(c.provider.toLowerCase()),
        )
        if (cancelled) return
        setHasGitConnector(git.length > 0)
        const views = await Promise.all(
          git.map(async (c) => {
            try {
              const v = await client.connectorRepositories(c.id)
              return (v.repositories ?? []).map((r) =>
                mapConnectorRepo(r, platformOf(c.provider), c.id),
              )
            } catch {
              return []
            }
          }),
        )
        if (!cancelled) {
          const flat = views.flat()
          setConnectorRepos(flat.map((x) => x.row))
          setScanMeta(Object.fromEntries(flat.map((x) => [x.row.id, x.meta])))
        }
      } catch {
        /* no connectors / not authorized: keep demo data */
      }
    })()
    return () => {
      cancelled = true
    }
  }, [projectId])

  const repos = useMemo(() => {
    // Prefer real connected repositories when a git connector is present.
    const all = hasGitConnector ? connectorRepos : (data?.repositories ?? [])
    const q = query.trim().toLowerCase()
    if (!q) return all
    return all.filter(
      (r) =>
        r.name.toLowerCase().includes(q) ||
        r.organization.toLowerCase().includes(q) ||
        r.project.toLowerCase().includes(q),
    )
  }, [data, query, hasGitConnector, connectorRepos])

  // toggleScan flips a repo's scan coverage against the backend and reflects the
  // result in the row. No-op for demo rows (those have no scanMeta entry).
  async function toggleScan(rowId: string, next: boolean) {
    const meta = scanMeta[rowId]
    if (!meta) return
    setTogglingId(rowId)
    // Optimistic update so the toggle feels instant.
    setConnectorRepos((cur) =>
      cur.map((r) => (r.id === rowId ? { ...r, scanning_enabled: next } : r)),
    )
    try {
      await client.setConnectorRepositorySelection(meta.connId, [meta.repoId], next)
    } catch {
      // Revert on failure.
      setConnectorRepos((cur) =>
        cur.map((r) => (r.id === rowId ? { ...r, scanning_enabled: !next } : r)),
      )
    } finally {
      setTogglingId(null)
    }
  }

  return (
    <div className="space-y-4">
      {/* Header */}
      <div className="flex items-start justify-between">
        <h1 className="text-2xl font-semibold tracking-tight">Code Repositories</h1>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm">
            Save as…
            <ChevronDown className="h-3.5 w-3.5" />
          </Button>
          <Button size="sm">
            <Plus className="h-4 w-4" /> Connect Code Repository
          </Button>
        </div>
      </div>

      {/* Tabs */}
      <Tabs value={tab} onValueChange={setTab}>
        <TabsList>
          <TabsTrigger value="organizational">Organizational</TabsTrigger>
          <TabsTrigger value="personal">Personal</TabsTrigger>
        </TabsList>
      </Tabs>

      {/* Filter bar */}
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search by name"
            className="h-8 w-56 pl-8 text-xs"
          />
        </div>
        <FilterPill label="Organization" />
        <FilterPill label="Platform" />
        <FilterPill label="Scan Finding Severity" />
        <FilterPill label="Visibility" />
        <FilterPill label="Additional Branches Configured" />
        <button
          type="button"
          className="inline-flex items-center gap-1 rounded-md border bg-background px-3 py-1.5 text-xs text-muted-foreground hover:bg-accent"
        >
          <Plus className="h-3 w-3" /> More
        </button>
        <span className="ml-auto text-xs text-muted-foreground">{repos.length} Repositories</span>
      </div>

      {/* GROUP BY chips */}
      <div className="flex items-center gap-2 text-xs">
        <span className="font-medium uppercase tracking-wider text-muted-foreground">Group by</span>
        {GROUP_BY.map((g) => (
          <button
            key={g}
            type="button"
            onClick={() => setGroupBy(g)}
            className={cn(
              'rounded-md border px-2.5 py-1 transition-colors',
              groupBy === g
                ? 'border-primary bg-primary/10 text-foreground'
                : 'border-border text-muted-foreground hover:bg-accent',
            )}
          >
            {g}
          </button>
        ))}
      </div>

      {/* Repositories table (full width). */}
      <div>
        <div className="min-w-0 rounded-lg border bg-card">
          {loading ? (
            <div className="flex h-40 items-center justify-center">
              <Spinner />
            </div>
          ) : error ? (
            <p className="p-4 text-sm text-destructive">{error}</p>
          ) : (
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Code Repository</TableHead>
                    <TableHead>Scan Coverage</TableHead>
                    <TableHead>Issues</TableHead>
                    <TableHead>Visibility</TableHead>
                    <TableHead>Last Scan</TableHead>
                    <TableHead>Project</TableHead>
                    <TableHead className="w-8">
                      <Columns3 className="h-3.5 w-3.5 text-muted-foreground" />
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {repos.map((r) => (
                    <RepoRow
                      key={r.id}
                      r={r}
                      scannable={Boolean(scanMeta[r.id])}
                      busy={togglingId === r.id}
                      onToggleScan={(next) => void toggleScan(r.id, next)}
                    />
                  ))}
                  {repos.length === 0 && (
                    <TableRow>
                      <TableCell colSpan={7} className="py-10 text-center text-muted-foreground">
                        No repositories match the current filters.
                      </TableCell>
                    </TableRow>
                  )}
                </TableBody>
              </Table>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

// ScanToggle is a clickable on/off switch. When onToggle is provided and
// enabled, clicking flips scan coverage; otherwise it is a read-only indicator
// (used for demo rows that have no backend connection).
function ScanToggle({
  on,
  disabled,
  onToggle,
}: {
  on: boolean
  disabled?: boolean
  onToggle?: (next: boolean) => void
}) {
  const interactive = Boolean(onToggle) && !disabled
  return (
    <button
      type="button"
      disabled={!interactive}
      onClick={() => onToggle?.(!on)}
      className={cn(
        'relative inline-flex h-4 w-7 shrink-0 items-center rounded-full transition-colors',
        on ? 'bg-emerald-500' : 'bg-muted-foreground/30',
        interactive ? 'cursor-pointer' : 'cursor-default',
        disabled && 'opacity-50',
      )}
      aria-pressed={on}
      aria-label={on ? 'Scanning enabled' : 'Scanning disabled'}
    >
      <span
        className={cn(
          'inline-block h-3 w-3 transform rounded-full bg-white transition-transform',
          on ? 'translate-x-3.5' : 'translate-x-0.5',
        )}
      />
    </button>
  )
}

function RepoRow({
  r,
  scannable,
  busy,
  onToggleScan,
}: {
  r: Repository
  scannable: boolean
  busy: boolean
  onToggleScan: (next: boolean) => void
}) {
  const issueCount = r.issues.critical + r.issues.high + r.issues.medium + r.issues.low
  return (
    <TableRow className="hover:bg-accent/40">
      <TableCell>
        <div className="flex items-center gap-2">
          <FolderGit2 className="h-4 w-4 shrink-0 text-muted-foreground" />
          <span className="truncate text-sm font-medium">{r.name}</span>
        </div>
      </TableCell>
      <TableCell>
        <div className="flex items-center gap-2 text-xs text-muted-foreground">
          <ScanToggle
            on={r.scanning_enabled}
            disabled={busy}
            onToggle={scannable ? onToggleScan : undefined}
          />
          {r.scanning_enabled ? 'On' : 'Off'}
        </div>
      </TableCell>
      <TableCell className="text-sm tabular-nums text-muted-foreground">{issueCount}</TableCell>
      <TableCell>
        <span className="inline-flex items-center gap-1.5 text-xs capitalize text-muted-foreground">
          {r.visibility === 'public' ? (
            <Globe className="h-3.5 w-3.5" />
          ) : (
            <Lock className="h-3.5 w-3.5" />
          )}
          {r.visibility}
        </span>
      </TableCell>
      <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
        {relativeTime(r.last_scan)}
      </TableCell>
      <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
        {r.project_count} projects
      </TableCell>
      <TableCell />
    </TableRow>
  )
}


