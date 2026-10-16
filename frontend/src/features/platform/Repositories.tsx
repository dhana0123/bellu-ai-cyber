import { useMemo, useState } from 'react'
import { Responsive, WidthProvider } from 'react-grid-layout'
import {
  ChevronDown,
  Columns3,
  FolderGit2,
  Globe,
  Lock,
  Plus,
  RotateCcw,
  Search,
} from 'lucide-react'
import { client } from '@/lib/client'
import { useAsync } from '@/useAsync'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Spinner } from '@/components/ui/spinner'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { DashboardCard } from '@/features/dashboard/components/DashboardCard'
import {
  DEFAULT_FINDINGS_LAYOUT,
  GRID_COLS,
  ROW_HEIGHT,
  useRepoFindingsLayout,
} from './useRepoFindingsLayout'
import '@/features/dashboard/grid.css'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import type {
  FindingCategory,
  RepositoriesData,
  Repository,
  SeverityCounts,
} from '@/types/repositories'

// WidthProvider feeds a measured pixel width so the grid stays fluid.
const ResponsiveGridLayout = WidthProvider(Responsive)

const GROUP_BY = ['None', 'Organization', 'Platform', 'Project', 'Service'] as const
type GroupBy = (typeof GROUP_BY)[number]

// Severity → dot/text color, matching the C/H/M/L chips in the design.
const SEV = {
  critical: { label: 'C', dot: 'bg-red-600', text: 'text-red-600' },
  high: { label: 'H', dot: 'bg-orange-500', text: 'text-orange-500' },
  medium: { label: 'M', dot: 'bg-amber-500', text: 'text-amber-500' },
  low: { label: 'L', dot: 'bg-sky-500', text: 'text-sky-500' },
} as const

function fmt(n: number): string {
  if (n >= 1000) return `${(n / 1000).toFixed(n >= 10000 ? 0 : 1)}K`
  return String(n)
}

// A compact row of severity pills: C H M L with counts, zeros muted.
function SeverityPills({ counts, size = 'sm' }: { counts: SeverityCounts; size?: 'sm' | 'md' }) {
  const order: (keyof SeverityCounts)[] = ['critical', 'high', 'medium', 'low']
  return (
    <div className="flex items-center gap-2">
      {order.map((k) => {
        const n = counts[k]
        const s = SEV[k]
        const muted = n === 0
        return (
          <span
            key={k}
            className={cn(
              'inline-flex items-center gap-1 tabular-nums',
              size === 'md' ? 'text-sm' : 'text-xs',
              muted ? 'text-muted-foreground/50' : 'text-foreground',
            )}
          >
            <span className={cn('font-medium tabular-nums')}>{fmt(n)}</span>
            <span
              className={cn(
                'flex h-4 w-4 items-center justify-center rounded-[3px] text-[9px] font-bold text-white',
                muted ? 'bg-muted-foreground/30' : s.dot,
              )}
            >
              {s.label}
            </span>
          </span>
        )
      })}
    </div>
  )
}

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

export default function Repositories() {
  const { loading, error, data } = useAsync<RepositoriesData>(
    () => client.repositories.overview(),
    [],
  )
  const [tab, setTab] = useState('organizational')
  const [groupBy, setGroupBy] = useState<GroupBy>('None')
  const [query, setQuery] = useState('')
  const { layout, onLayoutChange, resetLayout } = useRepoFindingsLayout()

  const repos = useMemo(() => {
    const all = data?.repositories ?? []
    const q = query.trim().toLowerCase()
    if (!q) return all
    return all.filter(
      (r) =>
        r.name.toLowerCase().includes(q) ||
        r.organization.toLowerCase().includes(q) ||
        r.project.toLowerCase().includes(q),
    )
  }, [data, query])

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

      {/* Findings summary cards: a draggable + resizable grid (same AWS-style
          grid as the Dashboard). Drag a card by its header handle to move it;
          drag the bottom-right corner to resize. */}
      {!loading && !error && (data?.finding_categories?.length ?? 0) > 0 && (
        <div className="space-y-2">
          <div className="flex items-center justify-between">
            <span className="text-xs text-muted-foreground">
              Drag a card by its header to move it; drag the bottom-right corner to resize.
            </span>
            <Button
              variant="outline"
              size="sm"
              onClick={resetLayout}
              disabled={JSON.stringify(layout) === JSON.stringify(DEFAULT_FINDINGS_LAYOUT)}
            >
              <RotateCcw className="h-4 w-4" /> Reset layout
            </Button>
          </div>
          <ResponsiveGridLayout
            className="layout"
            layouts={{ lg: layout, md: layout, sm: layout }}
            breakpoints={{ lg: 1200, md: 996, sm: 768, xs: 480, xxs: 0 }}
            cols={{ lg: GRID_COLS, md: GRID_COLS, sm: 6, xs: 4, xxs: 2 }}
            rowHeight={ROW_HEIGHT}
            margin={[12, 12]}
            containerPadding={[0, 0]}
            isDraggable
            isResizable
            draggableHandle=".dash-drag-handle"
            compactType="horizontal"
            onLayoutChange={onLayoutChange}
            onDragStop={onLayoutChange}
            onResizeStop={onLayoutChange}
            resizeHandles={['se']}
            useCSSTransforms
          >
            {(data?.finding_categories ?? []).map((c) => (
              <div key={c.id}>
                <DashboardCard title={c.label}>
                  <FindingCategoryCard cat={c} />
                </DashboardCard>
              </div>
            ))}
          </ResponsiveGridLayout>
        </div>
      )}

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
                    <TableHead>Policies</TableHead>
                    <TableHead>Project</TableHead>
                    <TableHead className="w-8">
                      <Columns3 className="h-3.5 w-3.5 text-muted-foreground" />
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {repos.map((r) => (
                    <RepoRow key={r.id} r={r} />
                  ))}
                  {repos.length === 0 && (
                    <TableRow>
                      <TableCell colSpan={8} className="py-10 text-center text-muted-foreground">
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

function ScanToggle({ on }: { on: boolean }) {
  return (
    <span
      className={cn(
        'relative inline-flex h-4 w-7 shrink-0 items-center rounded-full transition-colors',
        on ? 'bg-emerald-500' : 'bg-muted-foreground/30',
      )}
      aria-label={on ? 'Scanning enabled' : 'Scanning disabled'}
    >
      <span
        className={cn(
          'inline-block h-3 w-3 transform rounded-full bg-white transition-transform',
          on ? 'translate-x-3.5' : 'translate-x-0.5',
        )}
      />
    </span>
  )
}

function RepoRow({ r }: { r: Repository }) {
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
          <ScanToggle on={r.scanning_enabled} />
          All
        </div>
      </TableCell>
      <TableCell>
        <SeverityPills counts={r.issues} />
      </TableCell>
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
      <TableCell className="text-xs text-muted-foreground">{r.policies}</TableCell>
      <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
        {r.project_count} projects
      </TableCell>
      <TableCell />
    </TableRow>
  )
}

function FindingCategoryCard({ cat }: { cat: FindingCategory }) {
  const total = cat.counts.critical + cat.counts.high + cat.counts.medium + cat.counts.low
  // Rendered inside a DashboardCard whose header already shows cat.label, so the
  // body only shows the total + severity pills.
  return (
    <div className="flex h-full flex-col justify-center gap-2 bg-card p-4">
      <div className="text-2xl font-semibold tabular-nums">{fmt(total)}</div>
      <SeverityPills counts={cat.counts} />
    </div>
  )
}
