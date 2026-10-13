import { ChevronDown, Maximize2, PieChart, Search, X } from 'lucide-react'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'

function FilterPill({ label }: { label: string }) {
  return (
    <button className="inline-flex items-center gap-1 whitespace-nowrap rounded-full border px-3 py-1 text-xs text-muted-foreground hover:bg-accent hover:text-foreground">
      {label}
      <ChevronDown className="h-3 w-3" />
    </button>
  )
}

// Row 1 — search field + inline filter pills + right-side meta.
export function SearchFilterBar({
  search,
  onSearch,
  detectedOnly,
  onToggleDetected,
  total,
}: {
  search: string
  onSearch: (v: string) => void
  detectedOnly: boolean
  onToggleDetected: (v: boolean) => void
  total: number
}) {
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

        {/* Active pill: Detected in Environment = True */}
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

        <FilterPill label="Category" />
        <FilterPill label="Type" />
        <FilterPill label="Properties" />
        <button className="rounded-full px-2 py-1 text-xs font-medium text-primary hover:underline">
          + More
        </button>
      </div>

      <div className="flex items-center gap-2">
        <span className="whitespace-nowrap text-sm text-muted-foreground">
          {total.toLocaleString()} AI Assets
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
