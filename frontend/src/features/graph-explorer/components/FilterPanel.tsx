import { useId, useState } from 'react'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import type { FacetCount, GraphFacets, GraphFilter, SavedView } from '@/types/graphExplorer'
import { FacetSection, type FacetRow } from './FacetSection'

export interface FilterPanelProps {
  facets: GraphFacets
  filter: GraphFilter
  onFilterChange: (next: GraphFilter) => void
  views: SavedView[]
  activeViewId: string | null
  onSelectView: (id: string | null) => void
  onSaveView: (name: string) => void
}

// The facet keys in GraphFilter that map one-to-one to the three facet sections.
type FacetField = 'categories' | 'providers' | 'kinds'

// Project server-supplied FacetCount buckets onto the {key,label,count} rows the
// FacetSection expects, sorted by a STABLE key (label, then key) so the row
// order never changes when counts change — e.g. checking a facet must not
// reorder the list. The server may return counts in a count-based order that
// shifts per filter; sorting client-side keeps the UI steady.
function toRows(counts: FacetCount[]): FacetRow[] {
  return counts
    .map((c) => ({ key: c.key, label: c.label, count: c.count }))
    .sort((a, b) => a.label.localeCompare(b.label) || a.key.localeCompare(b.key))
}

// FilterPanel is the left control column: a New View / Saved Views tab toggle, a
// Saved_View selector (name + description), a free-text type-search input that only
// narrows the Node Types list, and the collapsible Category / Integrations / Node
// Types facet sections. All state lives in the parent; every change is reported
// through onFilterChange / onSelectView / onSaveView with a freshly built value.
export function FilterPanel(props: FilterPanelProps): JSX.Element {
  const { facets, filter, onFilterChange, views, activeViewId, onSelectView, onSaveView } = props

  const typeSearchId = useId()
  const viewSelectId = useId()
  const saveNameId = useId()
  const [newViewName, setNewViewName] = useState('')

  // Toggle a single key within one facet array, immutably, and report the full
  // updated GraphFilter. Empty array ⇒ "no constraint" (REQ 4.3, 4.4).
  const toggleFacet = (field: FacetField, key: string) => {
    const current = filter[field]
    const next = current.includes(key)
      ? current.filter((k) => k !== key)
      : [...current, key]
    onFilterChange({ ...filter, [field]: next })
  }

  // Free-text "Filter types…" input drives filter.typeSearch (REQ 3.4, 3.5); it
  // is passed to the Node Types section's searchTerm and nowhere else.
  const onTypeSearchChange = (value: string) => {
    onFilterChange({ ...filter, typeSearch: value })
  }

  const handleSaveView = () => {
    const name = newViewName.trim()
    if (!name) return
    onSaveView(name)
    setNewViewName('')
  }

  return (
    <aside
      aria-label="Filters"
      className="flex h-full min-h-0 w-72 shrink-0 flex-col gap-3 overflow-y-auto border-r border-border p-3"
    >
      {/* New View / Saved Views toggle (REQ 3.1) */}
      <Tabs defaultValue="new">
        <TabsList className="grid w-full grid-cols-2">
          <TabsTrigger value="new">New View</TabsTrigger>
          <TabsTrigger value="saved">Saved Views</TabsTrigger>
        </TabsList>

        {/* New View: name + save the current filter as a new Saved_View. */}
        <TabsContent value="new" className="space-y-2">
          <Label htmlFor={saveNameId}>View name</Label>
          <div className="flex items-center gap-2">
            <Input
              id={saveNameId}
              value={newViewName}
              placeholder="e.g. Public compute"
              onChange={(e) => setNewViewName(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault()
                  handleSaveView()
                }
              }}
            />
            <Button type="button" onClick={handleSaveView} disabled={!newViewName.trim()}>
              Save
            </Button>
          </div>
        </TabsContent>

        {/* Saved Views: selector listing each view's name + description (REQ 3.2). */}
        <TabsContent value="saved" className="space-y-2">
          <Label htmlFor={viewSelectId}>Saved view</Label>
          <Select
            value={activeViewId ?? undefined}
            onValueChange={(id) => onSelectView(id)}
          >
            <SelectTrigger id={viewSelectId} aria-label="Saved view">
              <SelectValue placeholder="Select a view" />
            </SelectTrigger>
            <SelectContent>
              {views.map((view) => (
                <SelectItem key={view.id} value={view.id}>
                  <span className="flex flex-col">
                    <span className="font-medium">{view.name}</span>
                    {view.description && (
                      <span className="text-xs text-muted-foreground">{view.description}</span>
                    )}
                  </span>
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </TabsContent>
      </Tabs>

      {/* Free-text node-type search (REQ 3.4); narrows only the Node Types list. */}
      <div className="space-y-1">
        <Label htmlFor={typeSearchId}>Filter types</Label>
        <Input
          id={typeSearchId}
          type="search"
          value={filter.typeSearch}
          placeholder="Filter types…"
          onChange={(e) => onTypeSearchChange(e.target.value)}
        />
      </div>

      {/* Collapsible facet sections (REQ 3.6, 4.1). */}
      <div className="space-y-3">
        <FacetSection
          title="Category"
          rows={toRows(facets.categories)}
          selected={new Set(filter.categories)}
          onToggle={(key) => toggleFacet('categories', key)}
        />
        <FacetSection
          title="Integrations"
          rows={toRows(facets.providers)}
          selected={new Set(filter.providers)}
          onToggle={(key) => toggleFacet('providers', key)}
        />
        <FacetSection
          title="Node Types"
          rows={toRows(facets.kinds)}
          selected={new Set(filter.kinds)}
          onToggle={(key) => toggleFacet('kinds', key)}
          searchTerm={filter.typeSearch}
        />
      </div>
    </aside>
  )
}
