import {
  ChevronDown,
  LayoutGrid,
  RefreshCw,
  Search,
  Sparkles,
  Star,
  X,
} from 'lucide-react'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

const GROUP_BY = [
  'None',
  'Rule',
  'Resource',
  'Subscription',
  'K8s Cluster',
  'K8s Namespace',
  'Code Repository',
]

function FilterDropdown({ label }: { label: string }) {
  return (
    <button className="inline-flex items-center gap-1 rounded-md border px-2.5 py-1.5 text-sm hover:bg-accent">
      {label}
      <ChevronDown className="h-3.5 w-3.5 text-muted-foreground" />
    </button>
  )
}

function Chip({ children, onRemove }: { children: React.ReactNode; onRemove?: () => void }) {
  return (
    <span className="inline-flex items-center gap-1.5 rounded-md border border-primary/40 bg-primary/5 px-2 py-1 text-xs text-foreground">
      {children}
      {onRemove ? (
        <button onClick={onRemove} aria-label="Remove filter" className="text-muted-foreground hover:text-foreground">
          <X className="h-3 w-3" />
        </button>
      ) : null}
    </span>
  )
}

export interface FilterPanelProps {
  search: string
  onSearch: (v: string) => void
  statusChip: string
  ruleChip: string
  onClearStatus: () => void
  onReset: () => void
  issueCount: number
  groupBy: string
  onGroupBy: (v: string) => void
}

// Row 4 — complex query toolbar (Layer 1) + GROUP BY selectors (Layer 2).
export function FilterPanel({
  search,
  onSearch,
  statusChip,
  ruleChip,
  onClearStatus,
  onReset,
  issueCount,
  groupBy,
  onGroupBy,
}: FilterPanelProps) {
  return (
    <div className="space-y-3 rounded-lg border bg-card p-3">
      {/* Layer 1 */}
      <div className="flex flex-wrap items-center gap-2">
        <Button variant="ghost" size="icon" className="h-8 w-8" aria-label="Bookmark">
          <Star className="h-4 w-4" />
        </Button>
        <Button variant="ghost" size="icon" className="h-8 w-8" aria-label="AI assist">
          <Sparkles className="h-4 w-4" />
        </Button>

        <div className="relative min-w-[220px] flex-1">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={search}
            onChange={(e) => onSearch(e.target.value)}
            placeholder="Search rule or resource"
            className="h-8 pl-8"
          />
        </div>

        <FilterDropdown label="Risk" />
        <FilterDropdown label="Category" />
        <FilterDropdown label="Subscription" />
        <FilterDropdown label="Severity" />
      </div>

      {/* Active selection chips + end utilities */}
      <div className="flex flex-wrap items-center gap-2">
        {statusChip ? (
          <Chip onRemove={onClearStatus}>
            <span className="text-muted-foreground">Status</span> equals {statusChip}
          </Chip>
        ) : null}
        <Chip>
          Type <ChevronDown className="h-3 w-3" />
        </Chip>
        {ruleChip ? (
          <Chip>
            <span className="text-muted-foreground">Rule</span> equals {ruleChip}
          </Chip>
        ) : null}

        <button className="text-xs text-primary hover:underline">+ More</button>

        <div className="ml-auto flex items-center gap-3">
          <button onClick={onReset} className="text-xs text-muted-foreground hover:text-foreground">
            Reset
          </button>
          <span className="text-xs text-muted-foreground">{issueCount} issues</span>
          <Button variant="ghost" size="icon" className="h-8 w-8" aria-label="Table layout">
            <LayoutGrid className="h-4 w-4" />
          </Button>
          <Button variant="ghost" size="icon" className="h-8 w-8" aria-label="Refresh">
            <RefreshCw className="h-4 w-4" />
          </Button>
        </div>
      </div>

      {/* Layer 2 — GROUP BY */}
      <div className="flex flex-wrap items-center gap-2 border-t pt-3">
        <span className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
          Group by
        </span>
        {GROUP_BY.map((g) => (
          <button
            key={g}
            onClick={() => onGroupBy(g)}
            className={cn(
              'rounded-md px-2.5 py-1 text-sm transition-colors',
              groupBy === g
                ? 'bg-primary/10 font-medium text-primary ring-1 ring-primary/40'
                : 'text-muted-foreground hover:bg-accent hover:text-foreground',
            )}
          >
            {g}
          </button>
        ))}
      </div>
    </div>
  )
}
