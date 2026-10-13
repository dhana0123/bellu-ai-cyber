import { ChevronDown, Plus } from 'lucide-react'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

const FILTERS = [
  'Vulnerability',
  'Severity',
  'Related Issue Severity',
  'Has Fix',
  'Has Public Exploit',
  'Resource Type',
  'Dependency Type',
]

const GROUP_BY = ['Resource', 'Component', 'Severity', 'Subscription', 'Detection Method']

function FilterButton({ label }: { label: string }) {
  return (
    <button className="inline-flex items-center gap-1 whitespace-nowrap rounded-md border px-2.5 py-1.5 text-sm text-muted-foreground hover:bg-accent hover:text-foreground">
      {label}
      <ChevronDown className="h-3.5 w-3.5" />
    </button>
  )
}

// Row 3 — horizontal filter bar + Group By row.
export function FilterBar({
  groupBy,
  onGroupBy,
}: {
  groupBy: string
  onGroupBy: (v: string) => void
}) {
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        {FILTERS.map((f) => (
          <FilterButton key={f} label={f} />
        ))}
        <button className="inline-flex items-center gap-1 rounded-md border border-dashed px-2.5 py-1.5 text-sm text-primary hover:bg-accent">
          <Plus className="h-3.5 w-3.5" /> Filter
        </button>
      </div>

      <div className="flex items-center gap-2">
        <span className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
          Group by
        </span>
        <Select value={groupBy} onValueChange={onGroupBy}>
          <SelectTrigger className="h-8 w-48">
            <SelectValue placeholder="Select…" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="__none__">Select…</SelectItem>
            {GROUP_BY.map((g) => (
              <SelectItem key={g} value={g}>
                {g}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
    </div>
  )
}
