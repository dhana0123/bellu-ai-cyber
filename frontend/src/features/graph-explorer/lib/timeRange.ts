// src/features/graph-explorer/lib/timeRange.ts
//
// Time-range presets for the Graph Explorer time filter. Kept in a dedicated
// module (not the TopBar component file) so sharing these constants does not
// trip the react-refresh "only export components" rule.

/** Time-range presets for the time filter. 'all' imposes no time constraint. */
export type TimeRange = 'all' | '24h' | '7d' | '30d' | '90d'

export const TIME_RANGE_OPTIONS: { value: TimeRange; label: string }[] = [
  { value: 'all', label: 'Any time' },
  { value: '24h', label: 'Last 24 hours' },
  { value: '7d', label: 'Last 7 days' },
  { value: '30d', label: 'Last 30 days' },
  { value: '90d', label: 'Last 90 days' },
]
