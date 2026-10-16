import { useCallback, useEffect, useState } from 'react'
import type { Layout } from 'react-grid-layout'

// Persisted, versioned layout for the Code Repositories findings summary cards.
// Own storage key + card ids so it never collides with the Dashboard/Inventory
// grids. 12-column fluid grid, same conventions as the other grid hooks.

export const GRID_COLS = 12
export const ROW_HEIGHT = 40
const STORAGE_KEY = 'bellu.repo.findings.layout'
const LAYOUT_VERSION = 1

// Stable ids match the FindingCategory ids from the repositories mock.
export const FINDING_CARD_IDS = [
  'vuln',
  'secret',
  'data',
  'iac',
  'sast',
  'ai-sast',
  'swmgmt',
] as const

// Default: a row of compact cards. Seven cards across 12 cols → ~w:3 wrapping to
// a second row. Each is resizable (min 2 wide / 3 tall) and draggable.
export const DEFAULT_FINDINGS_LAYOUT: Layout[] = FINDING_CARD_IDS.map((id, i) => ({
  i: id,
  x: (i * 3) % 12,
  y: Math.floor((i * 3) / 12) * 4,
  w: 3,
  h: 4,
  minW: 2,
  maxW: 12,
  minH: 3,
}))

interface StoredLayout {
  version: number
  layout: Layout[]
}

function loadLayout(): Layout[] {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return DEFAULT_FINDINGS_LAYOUT
    const parsed = JSON.parse(raw) as StoredLayout
    if (parsed.version !== LAYOUT_VERSION || !Array.isArray(parsed.layout)) {
      return DEFAULT_FINDINGS_LAYOUT
    }
    const ids = new Set(parsed.layout.map((l) => l.i))
    if (FINDING_CARD_IDS.some((id) => !ids.has(id))) return DEFAULT_FINDINGS_LAYOUT
    return parsed.layout
  } catch {
    return DEFAULT_FINDINGS_LAYOUT
  }
}

export function useRepoFindingsLayout() {
  const [layout, setLayout] = useState<Layout[]>(() => loadLayout())

  useEffect(() => {
    try {
      localStorage.setItem(
        STORAGE_KEY,
        JSON.stringify({ version: LAYOUT_VERSION, layout } satisfies StoredLayout),
      )
    } catch {
      /* storage full / blocked — layout simply won't persist */
    }
  }, [layout])

  const onLayoutChange = useCallback((next: Layout[]) => setLayout(next), [])

  const resetLayout = useCallback(() => {
    setLayout(DEFAULT_FINDINGS_LAYOUT)
    try {
      localStorage.removeItem(STORAGE_KEY)
    } catch {
      /* ignore */
    }
  }, [])

  return { layout, onLayoutChange, resetLayout }
}
