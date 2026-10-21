// src/features/graph-explorer/lib/categories.ts

/**
 * Maps an entity `kind` to its derived category label. Kinds not present in this
 * map fall back to {@link DEFAULT_CATEGORY}.
 */
export const CATEGORY_FOR_KIND: Record<string, string> = {
  Compute: 'Compute',
  Cluster: 'Compute',
  Workload: 'Compute',
  ContainerImage: 'Compute',
  DataStore: 'Storage',
  Identity: 'IAM',
  CloudAccount: 'IAM',
  Repository: 'Source Control',
  Package: 'Supply Chain',
  Vulnerability: 'Vulnerabilities',
  AIAgent: 'AI',
  AIModel: 'AI',
  Prompt: 'AI',
  MCPServer: 'AI',
  AIService: 'AI',
  NetworkEndpoint: 'Networking',
  ExternalService: 'Networking',
}

export const DEFAULT_CATEGORY = 'Other'

/**
 * Stable per-category color palette as 0xRRGGBB integers suitable for a pixi
 * sprite tint. The {@link DEFAULT_CATEGORY} maps to a neutral grey; any category
 * not listed here also resolves to the neutral default.
 */
const CATEGORY_COLORS: Record<string, number> = {
  Compute: 0x3b82f6, // blue
  Storage: 0x10b981, // emerald
  IAM: 0xf59e0b, // amber
  'Source Control': 0x8b5cf6, // violet
  'Supply Chain': 0xec4899, // pink
  Vulnerabilities: 0xef4444, // red
  AI: 0x06b6d4, // cyan
  Networking: 0x14b8a6, // teal
  [DEFAULT_CATEGORY]: 0x94a3b8, // neutral slate grey
}

/**
 * Returns a stable 0xRRGGBB color for the given category, for use as a pixi
 * sprite tint. Unknown categories (including {@link DEFAULT_CATEGORY}) resolve
 * to a neutral grey.
 */
export function colorForCategory(category: string): number {
  return CATEGORY_COLORS[category] ?? CATEGORY_COLORS[DEFAULT_CATEGORY]
}
