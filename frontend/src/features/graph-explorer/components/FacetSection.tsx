import { useId, useMemo, useState } from 'react'
import { ChevronDown, ChevronRight } from 'lucide-react'
import { Checkbox } from '@/components/ui/checkbox'
import { Badge } from '@/components/ui/badge'
import { Label } from '@/components/ui/label'
import { cn } from '@/lib/utils'

/** One selectable facet bucket: a stable key, display label, and matching count. */
export interface FacetRow {
  key: string
  label: string
  count: number
}

export interface FacetSectionProps {
  title: string
  rows: FacetRow[]
  selected: Set<string>
  onToggle: (key: string) => void
  collapsible?: boolean
  searchTerm?: string
}

// FacetSection renders one collapsible, keyboard-operable group of checkbox facet
// rows with per-bucket count badges. Each row is a Radix Checkbox + Label + Badge;
// toggling a row (click or keyboard) reports the row key through onToggle. When a
// searchTerm is supplied the visible rows are narrowed to matching labels/keys —
// the narrowing only affects which rows are shown, never the counts themselves.
export function FacetSection({
  title,
  rows,
  selected,
  onToggle,
  collapsible = true,
  searchTerm,
}: FacetSectionProps) {
  const [open, setOpen] = useState(true)
  const headingId = useId()
  const regionId = useId()

  // Narrow the visible rows by the free-text search term (case-insensitive match
  // against the label or key). An empty/whitespace term imposes no narrowing.
  const visibleRows = useMemo(() => {
    const term = searchTerm?.trim().toLowerCase()
    if (!term) return rows
    return rows.filter(
      (r) => r.label.toLowerCase().includes(term) || r.key.toLowerCase().includes(term),
    )
  }, [rows, searchTerm])

  const expanded = collapsible ? open : true

  return (
    <section className="space-y-1" aria-labelledby={headingId}>
      {collapsible ? (
        <button
          type="button"
          onClick={() => setOpen((o) => !o)}
          aria-expanded={expanded}
          aria-controls={regionId}
          className="flex w-full items-center gap-1 rounded-md px-1 py-1 text-left text-xs font-semibold uppercase tracking-wide text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
        >
          {expanded ? (
            <ChevronDown className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
          ) : (
            <ChevronRight className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
          )}
          <span id={headingId} className="flex-1">
            {title}
          </span>
        </button>
      ) : (
        <div
          id={headingId}
          className="px-1 py-1 text-xs font-semibold uppercase tracking-wide text-muted-foreground"
        >
          {title}
        </div>
      )}

      {expanded && (
        <div id={regionId} role="group" aria-labelledby={headingId} className="space-y-0.5">
          {visibleRows.length === 0 ? (
            <p className="px-1 py-1.5 text-xs text-muted-foreground">No matching types</p>
          ) : (
            visibleRows.map((row) => (
              <FacetCheckboxRow
                key={row.key}
                row={row}
                checked={selected.has(row.key)}
                onToggle={onToggle}
              />
            ))
          )}
        </div>
      )}
    </section>
  )
}

// A single facet row: a Checkbox associated with its Label (so clicking the label
// or focusing the box both toggle it) plus a count Badge aligned to the right.
function FacetCheckboxRow({
  row,
  checked,
  onToggle,
}: {
  row: FacetRow
  checked: boolean
  onToggle: (key: string) => void
}) {
  const id = useId()
  return (
    <div
      className={cn(
        'flex items-center gap-2 rounded-md px-1 py-1 hover:bg-accent/50',
        checked && 'bg-accent/40',
      )}
    >
      <Checkbox id={id} checked={checked} onCheckedChange={() => onToggle(row.key)} />
      <Label htmlFor={id} className="flex-1 cursor-pointer truncate text-sm font-normal">
        {row.label}
      </Label>
      <Badge variant="outline" className="shrink-0 tabular-nums">
        {row.count.toLocaleString()}
      </Badge>
    </div>
  )
}
