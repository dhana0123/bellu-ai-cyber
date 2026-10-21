import { Bot, BrainCircuit, Boxes, Cloud, Database, Plug } from 'lucide-react'
import type { AssetCategory } from '@/types/aiInventory'
// Shared icon registry: single source of truth for entity-kind icons/colors
// and provider brand marks, so the inventory, the pixi canvas, and the trace
// graph all render the same marks (Requirement #3: one map, not three).
import { styleForKind, providerLogoUrl } from '@/features/graph-explorer/lib/iconRegistry'

// Distinct icon + color per asset category for the table's leading icon slot.
const CATEGORY_META: Record<AssetCategory, { icon: typeof Bot; color: string; bg: string }> = {
  agent: { icon: Bot, color: 'text-emerald-500', bg: 'bg-emerald-500/15' },
  model: { icon: BrainCircuit, color: 'text-blue-500', bg: 'bg-blue-500/15' },
  tool: { icon: Plug, color: 'text-amber-500', bg: 'bg-amber-500/15' },
  rag: { icon: Database, color: 'text-violet-500', bg: 'bg-violet-500/15' },
  mcp: { icon: Boxes, color: 'text-sky-500', bg: 'bg-sky-500/15' },
}

// A square icon slot used in the table's Technology column.
export function CategoryIcon({ category }: { category: AssetCategory }) {
  const m = CATEGORY_META[category]
  const Icon = m.icon
  return (
    <span
      className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-md ${m.bg} ${m.color}`}
      aria-hidden
    >
      <Icon className="h-4 w-4" />
    </span>
  )
}

// KindIcon renders the icon for a canonical entity kind. Icon + accent color
// come from the shared registry (styleForKind), so discovered cloud resources
// render identically here, on the pixi canvas, and in the trace graph; unknown
// kinds fall back to the registry's neutral box glyph.
export function KindIcon({ kind }: { kind: string }) {
  const style = styleForKind(kind)
  const Icon = style.icon
  return (
    <span
      className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md"
      // Translucent tile derived from the same accent color (~9% alpha) keeps
      // the rounded-square treatment while matching the glyph's hue.
      style={{ backgroundColor: `${style.colorCss}17` }}
      aria-hidden
    >
      <Icon className="h-4 w-4" style={{ color: style.colorCss }} />
    </span>
  )
}

// ProviderIcon renders the provider's official brand logo for a resource.
// Cloud and CI/VCS providers use bundled brand SVGs from the shared registry;
// unknown providers fall back to a neutral cloud glyph so the slot is never
// empty.
export function ProviderIcon({ provider, className }: { provider?: string; className?: string }) {
  const cls = className ?? 'h-4 w-4'
  const logo = providerLogoUrl(provider)
  if (logo) {
    return <img src={logo} alt={`${provider} logo`} className={`${cls} object-contain`} />
  }
  return <Cloud className={cls} />
}
