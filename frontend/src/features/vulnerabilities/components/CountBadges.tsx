import type { SeverityCount } from '@/types/vulnerabilities'

// Compact "16 C  6 H" critical/high badge pair used in the analytics cards and
// the findings table's related-risk column.
export function CountBadges({ counts }: { counts: SeverityCount }) {
  return (
    <div className="flex items-center gap-1.5 text-xs font-medium tabular-nums">
      {counts.critical > 0 && (
        <span className="rounded bg-red-500/15 px-1.5 py-0.5 text-red-500">{counts.critical} C</span>
      )}
      {counts.high > 0 && (
        <span className="rounded bg-orange-500/15 px-1.5 py-0.5 text-orange-500">
          {counts.high} H
        </span>
      )}
      {counts.critical === 0 && counts.high === 0 && (
        <span className="text-muted-foreground">—</span>
      )}
    </div>
  )
}
