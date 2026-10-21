import { AlertTriangle } from 'lucide-react'

import { cn } from '@/lib/utils'

export interface DegradedBannerProps {
  // The message to show. Rendered when `graphAvailable` is false.
  message: string
  className?: string
}

// DegradedBanner — a non-blocking notice shown when the live graph service is
// unavailable (Graph_Available is false). It uses the warning (amber) treatment
// from the Bellu kit and an aria role of "status" so it is announced politely
// without interrupting the user or stealing focus. The surrounding explorer keeps
// the Table view and SQL-backed facets fully functional (Requirement 11.4).
export function DegradedBanner({ message, className }: DegradedBannerProps) {
  return (
    <div
      role="status"
      aria-live="polite"
      className={cn(
        'flex items-center gap-2 rounded-md border border-amber-500/30 bg-amber-500/5 px-3 py-2 text-sm text-amber-600 dark:text-amber-400',
        className,
      )}
    >
      <AlertTriangle className="h-4 w-4 shrink-0" aria-hidden="true" />
      <span>{message}</span>
    </div>
  )
}
