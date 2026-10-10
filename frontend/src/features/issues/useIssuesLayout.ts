import { useCallback, useEffect, useState } from 'react'
import type { Layout } from 'react-grid-layout'

// Persisted, versioned layout for the Risk Issues analytics row (Row 5). Mirrors
// the Dashboard's grid hook but with its own storage key and card ids so the two
// pages never collide. 12-column fluid grid.

export const GRID_COLS = 12
export const ROW_HEIGHT = 40
const STORAGE_KEY = 'bellu.issues.analytics.layout'
const LAYOUT_VERSION = 1

export const ISSUE_CARD_IDS = {
  severity: 'issue-severity',
  openResolved: 'open-resolved',
  openIssues: 'open-issues-trend',
} as const

// Three analytics cards at w:4 each (fills the 12-col row), resizable 3..12.
export const DEFAULT_ISSUES_LAYOUT: Layout[] = [
  { i: ISSUE_CARD_IDS.severity, x: 0, y: 0, w: 4, h: 7, minW: 3, maxW: 12, minH: 5 },
  { i: ISSUE_CARD_IDS.openResolved, x: 4, y: 0, w: 4, h: 7, minW: 3, maxW: 12, minH: 5 },
  { i: ISSUE_CARD_IDS.openIssues, x: 8, y: 0, w: 4, h: 7, minW: 3, maxW: 12, minH: 5 },
]

interface StoredLayout {
  version: number
  layout: Layout[]
}

function loadLayout(): Layout[] {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return DEFAULT_ISSUES_LAYOUT
    const parsed = JSON.parse(raw) as StoredLayout
    if (parsed.version !== LAYOUT_VERSION || !Array.isArray(parsed.layout)) {
      return DEFAULT_ISSUES_LAYOUT
    }
    const ids = new Set(parsed.layout.map((l) => l.i))
    const expected = Object.values(ISSUE_CARD_IDS)
    if (expected.some((id) => !ids.has(id))) return DEFAULT_ISSUES_LAYOUT
    return parsed.layout
  } catch {
    return DEFAULT_ISSUES_LAYOUT
  }
}

export function useIssuesLayout() {
  const [layout, setLayout] = useState<Layout[]>(() => loadLayout())

  useEffect(() => {
    try {
      const payload: StoredLayout = { version: LAYOUT_VERSION, layout }
      localStorage.setItem(STORAGE_KEY, JSON.stringify(payload))
    } catch {
      /* storage full / blocked — layout simply won't persist */
    }
  }, [layout])

  const onLayoutChange = useCallback((next: Layout[]) => {
    setLayout(next)
  }, [])

  const resetLayout = useCallback(() => {
    setLayout(DEFAULT_ISSUES_LAYOUT)
    try {
      localStorage.removeItem(STORAGE_KEY)
    } catch {
      /* ignore */
    }
  }, [])

  return { layout, onLayoutChange, resetLayout }
}
