import type { Severity } from '@/types/foundation'

// Severity → visual tokens for the dashboard. Dot/accent colors use fixed Tailwind
// classes so they read the same in light and dark; the chart gauges reuse the
// same hues via the `hsl` map below.

export const SEVERITY_DOT: Record<Severity, string> = {
  critical: 'bg-red-500',
  high: 'bg-orange-500',
  medium: 'bg-amber-400',
  low: 'bg-sky-500',
  info: 'bg-slate-400',
}

export const SEVERITY_TEXT: Record<Severity, string> = {
  critical: 'text-red-500',
  high: 'text-orange-500',
  medium: 'text-amber-500',
  low: 'text-sky-500',
  info: 'text-slate-400',
}

// Explicit colors for Recharts (areas, gauges). CSS color strings.
export const SEVERITY_HSL: Record<Severity, string> = {
  critical: 'hsl(0 73% 54%)',
  high: 'hsl(24 95% 53%)',
  medium: 'hsl(43 96% 56%)',
  low: 'hsl(199 89% 48%)',
  info: 'hsl(215 16% 60%)',
}
