// TypeScript shapes for the AI Ecosystem Inventory page. DEMO data only
// (src/mocks/aiInventory.ts); no backend. snake_case mirrors the backend's
// prevailing convention.

/** A category of AI asset, used for the colored icon slots. */
export type AssetCategory = 'agent' | 'model' | 'tool' | 'rag' | 'mcp'

export type ReviewStatus = 'unreviewed' | 'reviewed' | 'flagged'

/** A labelled count row (Breakdown by Category / Subcategory). */
export interface BreakdownRow {
  id: string
  label: string
  count: number
}

/** A color-coded slice/legend entry for the Breakdown by Type donut. */
export interface TypeSlice {
  id: string
  label: string
  count: number
  /** CSS color for the donut slice + legend bullet. */
  color: string
}

/** One row in the technology inventory table. */
export interface InventoryAsset {
  id: string
  /** Asset name, e.g. "Claude 3.5 Sonnet". */
  name: string
  /** Sub-categorization line, e.g. "Foundation Model / LLM". */
  subtitle: string
  category: AssetCategory
  /** Active connections (linked nodes/agents). */
  connections: number
  /** Deployment type label, e.g. "Autonomous Agent". */
  type: string
  /** Org usage string, e.g. "14 workspaces". */
  usage: string
  /** Icon hint for the usage column: workspace vs agent vs system. */
  usage_icon: 'workspace' | 'agent' | 'system'
  status: ReviewStatus
}

export interface AiInventoryData {
  total: number
  by_category: BreakdownRow[]
  by_subcategory: BreakdownRow[]
  by_type: TypeSlice[]
  assets: InventoryAsset[]
}
