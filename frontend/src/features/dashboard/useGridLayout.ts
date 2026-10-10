import { useCallback, useEffect, useState } from 'react'
import type { Layout } from 'react-grid-layout'

// Persisted, versioned dashboard layout. Bumping LAYOUT_VERSION invalidates any
// stored layout (e.g. after cards are added/removed) so users don't load a
// stale shape. The grid is a 12-column fluid grid.

export const GRID_COLS = 12
export const ROW_HEIGHT = 40
const STORAGE_KEY = 'bellu.dashboard.layout'
const LAYOUT_VERSION = 1

// Stable card ids used both here and in the page. Keep in sync.
export const CARD_IDS = {
  advisories: 'advisories',
  openIssues: 'open-issues',
  severity: 'severity-trends',
  issueAge: 'issue-age',
  securityScore: 'security-score',
  topIssues: 'top-issues',
  openedResolved: 'opened-resolved',
} as const

// Default layout per the spec:
//   Row 1 — Advisories banner: full width (w:12), locked (not draggable/resizable).
//   Row 2 — four analytical cards at w:3 each (min 2, can grow to 12).
//   Row 3 — table + burn chart at w:6 each.
export const DEFAULT_LAYOUT: Layout[] = [
  { i: CARD_IDS.advisories, x: 0, y: 0, w: 12, h: 2, minH: 2, static: true },

  { i: CARD_IDS.openIssues, x: 0, y: 2, w: 3, h: 7, minW: 2, maxW: 12, minH: 4 },
  { i: CARD_IDS.severity, x: 3, y: 2, w: 3, h: 7, minW: 2, maxW: 12, minH: 4 },
  { i: CARD_IDS.issueAge, x: 6, y: 2, w: 3, h: 7, minW: 2, maxW: 12, minH: 4 },
  { i: CARD_IDS.securityScore, x: 9, y: 2, w: 3, h: 7, minW: 2, maxW: 12, minH: 4 },

  { i: CARD_IDS.topIssues, x: 0, y: 9, w: 6, h: 9, minW: 3, maxW: 12, minH: 5 },
  { i: CARD_IDS.openedResolved, x: 6, y: 9, w: 6, h: 9, minW: 3, maxW: 12, minH: 5 },
]

interface StoredLayout {
  version: number
  layout: Layout[]
}

function loadLayout(): Layout[] {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return DEFAULT_LAYOUT
    const parsed = JSON.parse(raw) as StoredLayout
    if (parsed.version !== LAYOUT_VERSION || !Array.isArray(parsed.layout)) {
      return DEFAULT_LAYOUT
    }
    // Guard: if the set of ids changed, fall back to defaults.
    const ids = new Set(parsed.layout.map((l) => l.i))
    const expected = Object.values(CARD_IDS)
    if (expected.some((id) => !ids.has(id))) return DEFAULT_LAYOUT
    // Re-assert the static/locked flag on the banner regardless of stored value.
    return parsed.layout.map((l) =>
      l.i === CARD_IDS.advisories ? { ...l, static: true } : l,
    )
  } catch {
    return DEFAULT_LAYOUT
  }
}

export function useGridLayout() {
  const [layout, setLayout] = useState<Layout[]>(() => loadLayout())

  // Persist on every change (debounce-free: changes are coarse, user-driven).
  useEffect(() => {
    try {
      const payload: StoredLayout = { version: LAYOUT_VERSION, layout }
      localStorage.setItem(STORAGE_KEY, JSON.stringify(payload))
    } catch {
      /* storage full / blocked — layout simply won't persist */
    }
  }, [layout])

  // onLayoutChange fires for drag, resize, and compaction. It's the single
  // source of truth; onDragStop/onResizeStop are kept for explicit hooks/UX.
  const onLayoutChange = useCallback((next: Layout[]) => {
    setLayout(next)
  }, [])

  const resetLayout = useCallback(() => {
    setLayout(DEFAULT_LAYOUT)
    try {
      localStorage.removeItem(STORAGE_KEY)
    } catch {
      /* ignore */
    }
  }, [])

  return { layout, onLayoutChange, resetLayout }
}
