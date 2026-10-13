import { Bot, BrainCircuit, Boxes, Database, Plug } from 'lucide-react'
import type { AssetCategory } from '@/types/aiInventory'

// Distinct icon + color per asset category for the table's leading icon slot.
const CATEGORY_META: Record<
  AssetCategory,
  { icon: typeof Bot; color: string; bg: string }
> = {
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
