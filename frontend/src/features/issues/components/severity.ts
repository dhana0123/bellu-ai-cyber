import type { Severity } from '@/types/foundation'
import type { BadgeProps } from '@/components/ui/badge'

// Severity → visual tokens for the Risk Issues page.
export const SEVERITY_DOT: Record<Severity, string> = {
  critical: 'bg-red-500',
  high: 'bg-orange-500',
  medium: 'bg-amber-400',
  low: 'bg-sky-500',
  info: 'bg-slate-400',
}

export const SEVERITY_HSL: Record<Severity, string> = {
  critical: 'hsl(0 73% 54%)',
  high: 'hsl(24 95% 53%)',
  medium: 'hsl(43 96% 56%)',
  low: 'hsl(199 89% 48%)',
  info: 'hsl(215 16% 60%)',
}

export function severityBadgeVariant(s: Severity): BadgeProps['variant'] {
  if (s === 'critical' || s === 'high') return 'danger'
  if (s === 'medium') return 'warning'
  return 'secondary'
}
