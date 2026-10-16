import { ChevronDown, Maximize2, PieChart, Search, X } from 'lucide-react'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuCheckboxItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
} from '@/components/ui/dropdown-menu'
import { cn } from '@/lib/utils'

/** One selectable option in a filter dropdown (value + display label + count). */
export interface FilterOption {
  value: string
  label: string
  count: number
}

/** The full set of inventory filters controlled by this bar. */
export interface InventoryFilters {
  category: string[]
  type: string[]
  status: string[]
}

export interface FilterOptionSets {
  category: FilterOption[]
  type: FilterOption[]
  status: FilterOption[]
}

// A single multi-select filter dropdown rendered as a pill. Shows a count badge
// when selections are active.
function FilterDropdown({
  label,
  options,
  selected,
  onChange,
}: {
  label: string
  options: FilterOption[]
  selected: string[]
  onChange: (next: string[]) => void
}) {
  const active = selected.length > 0
  const toggle = (value: string) => {
    onChange(selected.includes(value) ? selected.filter((v) => v !== value) : [...selected, value])
  }
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          className={cn(
            'inline-flex items-center gap-1 whitespace-nowrap rounded-full border px-3 py-1 text-xs',
            active
              ? 'border-primary/40 bg-primary/10 font-medium text-foreground'
              : 'text-muted-foreground hover:bg-accent hover:text-foreground',
          )}
        >
          {label}
          {active ? (
            <Badge variant="secondary" className="h-4 min-w-4 px-1 text-[10px]">
              {selected.length}
            </Badge>
          ) : null}
          <ChevronDown className="h-3 w-3" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="max-h-80 w-56 overflow-y-auto">
        <DropdownMenuLabel className="flex items-center justify-between">
          <span>{label}</span>
          {active ? (
            <button
              onClick={() => onChange([])}
              className="text-[11px] font-normal text-primary hover:underline"
            >
              Clear
            </button>
          ) : null}
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        {options.length === 0 ? (
          <div className="px-2 py-1.5 text-xs text-muted-foreground">No options</div>
        ) : (
          options.map((o) => (
            <DropdownMenuCheckboxItem
              key={o.value}
              checked={selected.includes(o.value)}
              onCheckedChange={() => toggle(o.value)}
              onSelect={(e) => e.preventDefault()}
            >
              <span className="flex-1 truncate">{o.label}</span>
              <span className="ml-2 text-[10px] tabular-nums text-muted-foreground">{o.count}</span>
            </DropdownMenuCheckboxItem>
          ))
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

// Row 1 — search field + working multi-select filter dropdowns + right-side
// meta. `shown` is the count after filtering, `total` the full population.
export function SearchFilterBar({
  search,
  onSearch,
  detectedOnly,
  onToggleDetected,
  filters,
  onFiltersChange,
  options,
  shown,
  total,
}: {
  search: string
  onSearch: (v: string) => void
  detectedOnly: boolean
  onToggleDetected: (v: boolean) => void
  filters: InventoryFilters
  onFiltersChange: (next: InventoryFilters) => void
  options: FilterOptionSets
  shown: number
  total: number
}) {
  const activeCount =
    filters.category.length +
    filters.type.length +
    filters.status.length +
    (detectedOnly ? 1 : 0) +
    (search.trim() ? 1 : 0)

  const clearAll = () => {
    onSearch('')
    onToggleDetected(false)
    onFiltersChange({ category: [], type: [], status: [] })
  }

  const set = (key: keyof InventoryFilters, next: string[]) =>
    onFiltersChange({ ...filters, [key]: next })

  return (
    <div className="flex flex-wrap items-center justify-between gap-3">
      <div className="flex flex-1 flex-wrap items-center gap-2">
        <div className="relative min-w-[260px] flex-1 md:max-w-sm">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={search}
            onChange={(e) => onSearch(e.target.value)}
            placeholder="Search AI assets, tools, or MCP servers…"
            className="h-9 pl-8"
          />
        </div>

        {/* Detected in Environment toggle pill. */}
        {detectedOnly ? (
          <span className="inline-flex items-center gap-1 rounded-full border border-primary/40 bg-primary/10 px-3 py-1 text-xs font-medium text-foreground">
            Detected in Environment: True
            <button
              onClick={() => onToggleDetected(false)}
              aria-label="Clear detected filter"
              className="text-muted-foreground hover:text-foreground"
            >
              <X className="h-3 w-3" />
            </button>
          </span>
        ) : (
          <button
            onClick={() => onToggleDetected(true)}
            className="inline-flex items-center gap-1 rounded-full border px-3 py-1 text-xs text-muted-foreground hover:bg-accent hover:text-foreground"
          >
            Detected in Environment
          </button>
        )}

        <FilterDropdown
          label="Category"
          options={options.category}
          selected={filters.category}
          onChange={(next) => set('category', next)}
        />
        <FilterDropdown
          label="Type"
          options={options.type}
          selected={filters.type}
          onChange={(next) => set('type', next)}
        />
        <FilterDropdown
          label="Status"
          options={options.status}
          selected={filters.status}
          onChange={(next) => set('status', next)}
        />

        {activeCount > 0 ? (
          <button
            onClick={clearAll}
            className="inline-flex items-center gap-1 rounded-full px-2 py-1 text-xs font-medium text-primary hover:underline"
          >
            <X className="h-3 w-3" />
            Clear all ({activeCount})
          </button>
        ) : null}
      </div>

      <div className="flex items-center gap-2">
        <span className="whitespace-nowrap text-sm text-muted-foreground">
          {shown === total
            ? `${total.toLocaleString()} AI Assets`
            : `${shown.toLocaleString()} of ${total.toLocaleString()} AI Assets`}
        </span>
        <Button variant="ghost" size="icon" className="h-8 w-8" aria-label="Toggle chart view">
          <PieChart className="h-4 w-4" />
        </Button>
        <Button variant="ghost" size="icon" className="h-8 w-8" aria-label="Full screen">
          <Maximize2 className="h-4 w-4" />
        </Button>
      </div>
    </div>
  )
}
