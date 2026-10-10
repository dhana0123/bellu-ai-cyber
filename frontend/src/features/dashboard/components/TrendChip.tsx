import { ArrowDown, ArrowUp } from 'lucide-react'
import { cn } from '@/lib/utils'
import type { Trend } from '@/types/dashboard'

// A small trend chip, e.g. "↓ 6%". Color intent: by default an increase is bad
// (red up / green down), matching issue counts. Set increase_is_bad=false for
// metrics where going up is good (e.g. security score).
export function TrendChip({ trend, className }: { trend: Trend; className?: string }) {
  const up = trend.pct >= 0
  const increaseIsBad = trend.increase_is_bad ?? true
  const good = increaseIsBad ? !up : up
  const Icon = up ? ArrowUp : ArrowDown

  return (
    <span
      className={cn(
        'inline-flex items-center gap-0.5 rounded-full px-1.5 py-0.5 text-xs font-medium',
        good
          ? 'bg-emerald-500/10 text-emerald-500'
          : 'bg-red-500/10 text-red-500',
        className,
      )}
    >
      <Icon className="h-3 w-3" />
      {Math.abs(trend.pct)}%
    </span>
  )
}
