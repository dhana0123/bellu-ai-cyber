import { useCallback, useEffect, useState } from 'react'
import type { Layout } from 'react-grid-layout'

// Persisted, versioned layout for the AI Inventory grid (3 breakdown cards +
// the inventory table). Mirrors the Dashboard/Issues grid hooks but with its
// own storage key and card ids so the pages never collide. 12-column grid.

export const GRID_COLS = 12
export const ROW_HEIGHT = 40
const STORAGE_KEY = 'bellu.inventory.layout'
const LAYOUT_VERSION = 1

export const INV_CARD_IDS = {
  category: 'breakdown-category',
  subcategory: 'breakdown-subcategory',
  type: 'breakdown-type',
  table: 'inventory-table',
} as const

// Three breakdown cards at w:4 each (fill the 12-col row), resizable 3..12, plus
// the full-width inventory table below them.
export const DEFAULT_INV_LAYOUT: Layout[] = [
  { i: INV_CARD_IDS.category, x: 0, y: 0, w: 4, h: 6, minW: 3, maxW: 12, minH: 4 },
  { i: INV_CARD_IDS.subcategory, x: 4, y: 0, w: 4, h: 6, minW: 3, maxW: 12, minH: 4 },
  { i: INV_CARD_IDS.type, x: 8, y: 0, w: 4, h: 6, minW: 3, maxW: 12, minH: 4 },
  { i: INV_CARD_IDS.table, x: 0, y: 6, w: 12, h: 12, minW: 4, maxW: 12, minH: 6 },
]

interface StoredLayout {
  version: number
  layout: Layout[]
}

function loadLayout(): Layout[] {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return DEFAULT_INV_LAYOUT
    const parsed = JSON.parse(raw) as StoredLayout
    if (parsed.version !== LAYOUT_VERSION || !Array.isArray(parsed.layout)) {
      return DEFAULT_INV_LAYOUT
    }
    const ids = new Set(parsed.layout.map((l) => l.i))
    const expected = Object.values(INV_CARD_IDS)
    if (expected.some((id) => !ids.has(id))) return DEFAULT_INV_LAYOUT
    return parsed.layout
  } catch {
    return DEFAULT_INV_LAYOUT
  }
}

export function useInventoryLayout() {
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
    setLayout(DEFAULT_INV_LAYOUT)
    try {
      localStorage.removeItem(STORAGE_KEY)
    } catch {
      /* ignore */
    }
  }, [])

  return { layout, onLayoutChange, resetLayout }
}
