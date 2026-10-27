// src/features/graph-explorer/hooks/useGraphExplorer.ts
//
// Encapsulates fetching the graph working set, filter state, saved views,
// derived counts, and client-side filtering so the GraphExplorer page stays
// presentational. See design.md "Hook: useGraphExplorer".
//
// Saved-views persistence (task 9.2): user-created views are loaded from and
// written to localStorage under a per-user key (`graph-explorer:views:<userId>`).
// The built-in, non-deletable "All Resources" view is always provided in code —
// it is never stored and always appears first. selectView applies a view's
// filter; saveView snapshots the current filter into a new persisted view.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { useAuth } from '@/auth'
import { client } from '@/lib/client'
import { useAsync } from '@/useAsync'
import type {
  FacetCount,
  GraphFacets,
  GraphFilter,
  GraphNode,
  GraphEdge,
  SavedView,
} from '@/types/graphExplorer'

import { applyFilter } from '../lib/applyFilter'

/** Public shape consumed by the GraphExplorer page and its children. */
export interface UseGraphExplorer {
  loading: boolean
  /** True once the first response has arrived; stays true across refetches so
   *  filter changes show an overlay instead of unmounting the whole page. */
  hasLoaded: boolean
  error: string | null
  graphAvailable: boolean // false => degraded banner, canvas disabled, table still works
  facets: GraphFacets
  filter: GraphFilter
  setFilter: (next: Partial<GraphFilter>) => void
  visibleNodes: GraphNode[] // after client-side filter
  visibleEdges: GraphEdge[]
  visibleNodeCount: number
  visibleEdgeCount: number
  capped: boolean
  views: SavedView[]
  activeViewId: string | null
  selectView: (id: string | null) => void
  saveView: (name: string) => void
  selectedId: string | null
  select: (id: string | null) => void
}

/** A filter that constrains nothing — the "All Resources" baseline. */
export const EMPTY_FILTER: GraphFilter = {
  view: 'all',
  categories: [],
  providers: [],
  kinds: [],
  typeSearch: '',
}

/** Built-in, non-deletable "All Resources" view with an empty filter. */
export const ALL_RESOURCES_VIEW: SavedView = {
  id: 'all',
  name: 'All Resources',
  description: 'Every node and edge in the stored graph',
  filter: EMPTY_FILTER,
  builtin: true,
}

/** Bump when the persisted shape changes so stale entries are discarded. */
const VIEWS_VERSION = 1

/** Shape of the JSON payload written to localStorage. */
interface StoredViews {
  version: number
  views: SavedView[]
}

/**
 * Storage key for a given user id. Views are scoped per user so one operator's
 * saved views never leak into another's on a shared browser. A null/undefined
 * user id (not yet authenticated) yields a null key and persistence is skipped.
 */
function viewsStorageKey(userId: number | null): string | null {
  return userId == null ? null : `graph-explorer:views:${userId}`
}

/**
 * Load user-created views for a user id. Returns only persisted, non-builtin
 * views (the built-in "All Resources" view is never stored). Guards against a
 * missing key, corrupt JSON, and version/shape drift so a bad entry can never
 * crash the hook.
 */
function loadUserViews(userId: number | null): SavedView[] {
  const key = viewsStorageKey(userId)
  if (!key) return []
  try {
    const raw = localStorage.getItem(key)
    if (!raw) return []
    const parsed = JSON.parse(raw) as StoredViews
    if (parsed.version !== VIEWS_VERSION || !Array.isArray(parsed.views)) return []
    // Never resurrect a stored built-in; it is provided in code, not storage.
    return parsed.views.filter((v) => v && v.id !== ALL_RESOURCES_VIEW.id && !v.builtin)
  } catch {
    // Corrupt entry / storage blocked — fall back to no persisted views.
    return []
  }
}

/** Persist the user-created views for a user id. No-op when storage is blocked. */
function persistUserViews(userId: number | null, userViews: SavedView[]): void {
  const key = viewsStorageKey(userId)
  if (!key) return
  try {
    const payload: StoredViews = { version: VIEWS_VERSION, views: userViews }
    localStorage.setItem(key, JSON.stringify(payload))
  } catch {
    /* storage full / blocked — views simply won't persist */
  }
}

export function useGraphExplorer(): UseGraphExplorer {
  // The authenticated user's id keys the persisted views so they are scoped
  // per operator. Null until the principal resolves; persistence is skipped
  // while null so we never write under an anonymous key.
  const { principal } = useAuth()
  const userId = principal?.user.id ?? null
  // Client-held filter state. Facet selections drive the client-side filter;
  // the server fetch uses the same facet arrays to bound the working set.
  const [filter, setFilterState] = useState<GraphFilter>(EMPTY_FILTER)

  // Selection state (node id, or null when nothing is selected).
  const [selectedId, setSelectedId] = useState<string | null>(null)

  // User-created views, held separately from the built-in so the built-in is
  // always present and first and can never be overwritten or removed (12.2).
  const [userViews, setUserViews] = useState<SavedView[]>(() => loadUserViews(userId))
  const [activeViewId, setActiveViewId] = useState<string | null>(ALL_RESOURCES_VIEW.id)

  // Reload persisted views when the authenticated user changes (e.g. after the
  // principal resolves on mount, or a login/logout swaps the user). Reset the
  // active view to the built-in so one user's selection never carries over.
  useEffect(() => {
    setUserViews(loadUserViews(userId))
    setActiveViewId(ALL_RESOURCES_VIEW.id)
  }, [userId])

  // The built-in "All Resources" view is always present and first; the
  // user-created views follow it (3.3, 12.1).
  const views: SavedView[] = useMemo(
    () => [ALL_RESOURCES_VIEW, ...userViews],
    [userViews],
  )

  // Fetch the working set. The server bounds the set by the facet selections and
  // returns authoritative counts/facets over the whole filtered graph. We refetch
  // when a facet selection changes (typeSearch only narrows the panel, so it is
  // intentionally excluded from the dependency list).
  const { loading, error, data } = useAsync(
    () =>
      client.graphExplorer({
        categories: filter.categories,
        providers: filter.providers,
        kinds: filter.kinds,
      }),
    [filter.categories, filter.providers, filter.kinds],
  )

  const graphAvailable = data?.graphAvailable ?? false
  const capped = data?.capped ?? false

  // Keep the FULL set of facet rows stable across filter changes (Option B):
  // selecting a facet updates the counts but never removes or reorders rows.
  // We remember every facet key/label ever seen (the union, seeded by the first
  // unfiltered load) and always render that known set, merging in the current
  // response's counts (0 for a key absent from the filtered response).
  const knownFacets = useRef<{
    categories: Map<string, string>
    providers: Map<string, string>
    kinds: Map<string, string>
  }>({ categories: new Map(), providers: new Map(), kinds: new Map() })

  // Record any newly-seen keys/labels from the latest response.
  if (data?.facets) {
    for (const group of ['categories', 'providers', 'kinds'] as const) {
      const known = knownFacets.current[group]
      for (const f of data.facets[group]) {
        if (!known.has(f.key)) known.set(f.key, f.label)
      }
    }
  }

  // Merge the known key set with the current counts so the row LIST is stable
  // (every known key present) while counts reflect the active filter.
  const facets: GraphFacets = useMemo(() => {
    const build = (group: 'categories' | 'providers' | 'kinds'): FacetCount[] => {
      const current = new Map((data?.facets?.[group] ?? []).map((f) => [f.key, f.count]))
      return Array.from(knownFacets.current[group].entries()).map(([key, label]) => ({
        key,
        label,
        count: current.get(key) ?? 0,
      }))
    }
    return {
      categories: build('categories'),
      providers: build('providers'),
      kinds: build('kinds'),
    }
    // Recompute whenever the response changes; knownFacets is a ref updated above.
  }, [data])
  // Once any response (data) has been received the layout should persist across
  // subsequent filter refetches; useAsync keeps `data` during a refetch.
  const hasLoaded = data != null

  // Derive the visible set via applyFilter, memoized so the node/edge arrays keep
  // referential stability across renders (important for memoized pixi sprites).
  // applyFilter reuses the fetched object references, so identity is preserved
  // whenever the inputs are unchanged.
  const { nodes: visibleNodes, edges: visibleEdges } = useMemo(() => {
    const nodes = data?.nodes ?? []
    const edges = data?.edges ?? []
    return applyFilter(nodes, edges, filter)
  }, [data, filter])

  // CRITICAL (Requirement 5.6): badge counts come from the server-reported
  // totals, NEVER from visibleNodes.length / visibleEdges.length. The server
  // count is authoritative even when the working set is capped.
  const visibleNodeCount = data?.nodeCount ?? 0
  const visibleEdgeCount = data?.edgeCount ?? 0

  const setFilter = useCallback((next: Partial<GraphFilter>) => {
    setFilterState((prev) => ({ ...prev, ...next }))
  }, [])

  const select = useCallback((id: string | null) => {
    setSelectedId(id)
  }, [])

  // Applying a saved view sets `filter` to that view's filter and updates
  // `activeViewId` (3.3, 12.5). Selecting the built-in resets to the empty
  // filter. An unknown id only updates the active id without touching the
  // filter.
  const selectView = useCallback(
    (id: string | null) => {
      const view = views.find((v) => v.id === id)
      setActiveViewId(id)
      if (view) {
        setFilterState(view.filter)
      }
    },
    [views],
  )

  // Persist a new user view built from the CURRENT filter: assign a stable
  // unique id (builtin:false), append it, persist to localStorage keyed by user
  // id, and make it active (12.3). The built-in view is never affected.
  const saveView = useCallback(
    (name: string) => {
      const id = `view:${Date.now()}:${Math.random().toString(36).slice(2, 10)}`
      const view: SavedView = {
        id,
        name,
        description: '',
        // Snapshot the current filter so the saved view is immutable even if
        // the live filter changes afterward.
        filter: { ...filter },
        builtin: false,
      }
      setUserViews((prev) => {
        const next = [...prev, view]
        persistUserViews(userId, next)
        return next
      })
      setActiveViewId(id)
    },
    [filter, userId],
  )

  return {
    loading,
    hasLoaded,
    error,
    graphAvailable,
    facets,
    filter,
    setFilter,
    visibleNodes,
    visibleEdges,
    visibleNodeCount,
    visibleEdgeCount,
    capped,
    views,
    activeViewId,
    selectView,
    saveView,
    selectedId,
    select,
  }
}
