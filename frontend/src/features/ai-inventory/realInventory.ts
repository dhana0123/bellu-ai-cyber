// Real-data adapter for the AI Ecosystem Inventory page. It fetches the
// discovered entities from the backend (GET /api/entities) and maps them into
// the AiInventoryData shape the existing UI already renders — so the page keeps
// its exact layout (breakdown cards, donut, technology table) but shows real
// discovered inventory instead of mock demo data.
import { realApi } from '@/lib/api'
import type { EntityRow } from '@/types/foundation'
import type {
  AiInventoryData,
  AssetCategory,
  BreakdownRow,
  InventoryAsset,
  TypeSlice,
} from '@/types/aiInventory'

// Human-readable label per entity kind, shown as the asset "type" and used for
// the breakdowns.
const KIND_LABEL: Record<string, string> = {
  CloudAccount: 'Cloud Account',
  Identity: 'Identity',
  DataStore: 'Data Store',
  Compute: 'Compute',
  Cluster: 'Cluster',
  Workload: 'Workload',
  ContainerImage: 'Container Image',
  Repository: 'Repository',
  Package: 'Package',
  Vulnerability: 'Vulnerability',
  AIAgent: 'AI Agent',
  AIModel: 'AI Model',
  Prompt: 'Prompt',
  MCPServer: 'MCP Server',
  AIService: 'AI Service',
  NetworkEndpoint: 'Network Endpoint',
  ExternalService: 'External Service',
}

// Map an entity kind to the UI's colored category slot. AI kinds get their
// specific slots; everything else falls back to "tool" so cloud resources
// (identities, data stores, compute, etc.) still render with an icon.
const KIND_CATEGORY: Record<string, AssetCategory> = {
  AIAgent: 'agent',
  AIModel: 'model',
  AIService: 'model',
  MCPServer: 'mcp',
  Prompt: 'tool',
  DataStore: 'rag',
}

// Stable palette for the donut, cycled across whatever kinds are present.
const PALETTE = [
  'hsl(221 83% 60%)',
  'hsl(161 84% 42%)',
  'hsl(43 96% 56%)',
  'hsl(262 83% 66%)',
  'hsl(0 84% 63%)',
  'hsl(199 89% 54%)',
  'hsl(291 64% 56%)',
  'hsl(215 16% 55%)',
]

function labelFor(kind: string): string {
  return KIND_LABEL[kind] ?? kind
}

function categoryFor(kind: string): AssetCategory {
  return KIND_CATEGORY[kind] ?? 'tool'
}

// High-level domain group per entity kind, used by the inventory Category
// filter (AI, SaaS, Kubernetes, Data, Identity, Compute, Cloud, Code,
// Security). Kubernetes membership also considers the provider below.
const KIND_GROUP: Record<string, string> = {
  AIAgent: 'AI',
  AIModel: 'AI',
  AIService: 'AI',
  MCPServer: 'AI',
  Prompt: 'AI',
  Cluster: 'Kubernetes',
  Workload: 'Kubernetes',
  ContainerImage: 'Kubernetes',
  DataStore: 'Data',
  Identity: 'Identity',
  Compute: 'Compute',
  CloudAccount: 'Cloud',
  NetworkEndpoint: 'Cloud',
  Repository: 'Code',
  Package: 'Code',
  Vulnerability: 'Security',
  ExternalService: 'SaaS',
}

// groupFor classifies an entity into a high-level domain group. Provider is a
// strong signal for Kubernetes/SaaS so e.g. a k8s-sourced resource lands in
// "Kubernetes" even when its kind is generic.
function groupFor(kind: string, provider?: string): string {
  const p = (provider ?? '').toLowerCase()
  if (p === 'k8s' || p === 'kubernetes') return 'Kubernetes'
  const g = KIND_GROUP[kind]
  if (g) return g
  // Cloud-provider resources default to "Cloud"; everything else to "SaaS".
  if (p === 'aws' || p === 'azure' || p === 'gcp' || p === 'google' || p === 'googlecloud') {
    return 'Cloud'
  }
  return 'SaaS'
}

// countByKind returns [kind, count] pairs sorted by count descending.
function countByKind(entities: EntityRow[]): Array<[string, number]> {
  const m = new Map<string, number>()
  for (const e of entities) m.set(e.kind, (m.get(e.kind) ?? 0) + 1)
  return [...m.entries()].sort((a, b) => b[1] - a[1])
}

// countByProvider returns [provider, count] pairs sorted by count descending.
function countByProvider(entities: EntityRow[]): Array<[string, number]> {
  const m = new Map<string, number>()
  for (const e of entities) {
    const p = e.provider || 'unknown'
    m.set(p, (m.get(p) ?? 0) + 1)
  }
  return [...m.entries()].sort((a, b) => b[1] - a[1])
}

// toInventoryData maps discovered entities into the page's data shape.
export function toInventoryData(entities: EntityRow[]): AiInventoryData {
  const byKind = countByKind(entities)

  const by_category: BreakdownRow[] = byKind.map(([kind, count]) => ({
    id: `cat-${kind}`,
    label: labelFor(kind),
    count,
  }))

  // "Subcategory" view: break down by cloud provider (aws, azure, …), which is
  // the most useful secondary axis for discovered cloud resources.
  const by_subcategory: BreakdownRow[] = countByProvider(entities).map(([prov, count]) => ({
    id: `sub-${prov}`,
    label: prov.toUpperCase(),
    count,
  }))

  // Donut: top kinds get their own slice, the remainder collapses into "+N More".
  const TOP = 5
  const top = byKind.slice(0, TOP)
  const rest = byKind.slice(TOP)
  const by_type: TypeSlice[] = top.map(([kind, count], i) => ({
    id: `typ-${kind}`,
    label: labelFor(kind),
    count,
    color: PALETTE[i % PALETTE.length],
  }))
  if (rest.length > 0) {
    const restCount = rest.reduce((s, [, c]) => s + c, 0)
    by_type.push({
      id: 'typ-more',
      label: `+${rest.length} More`,
      count: restCount,
      color: PALETTE[PALETTE.length - 1],
    })
  }

  const assets: InventoryAsset[] = entities.map((e, i) => ({
    id: e.canonical_id || `e-${i}`,
    name: e.name || e.canonical_id,
    subtitle: `${labelFor(e.kind)}${e.provider ? ` / ${e.provider.toUpperCase()}` : ''}`,
    category: categoryFor(e.kind),
    connections: 0,
    type: labelFor(e.kind),
    usage: e.provider ? e.provider.toUpperCase() : '—',
    usage_icon: 'system',
    status: e.stale ? 'flagged' : 'unreviewed',
    kind: e.kind,
    group: groupFor(e.kind, e.provider),
    provider: e.provider,
    canonical_id: e.canonical_id,
    attributes: e.attributes,
  }))

  return {
    total: entities.length,
    by_category,
    by_subcategory,
    by_type,
    assets,
  }
}

// fetchInventory loads discovered entities and maps them to AiInventoryData.
// projectId optionally scopes the inventory to a single project.
export async function fetchInventory(projectId?: number): Promise<AiInventoryData> {
  const { entities } = await realApi.entities('', 1000, projectId)
  return toInventoryData(entities ?? [])
}
