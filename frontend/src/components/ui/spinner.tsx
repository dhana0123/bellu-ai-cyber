import { cn } from '@/lib/utils'

// Spinner — preserved from the pre-migration kit (not a shadcn primitive).
export function Spinner({ className }: { className?: string }) {
  return (
    <div
      role="status"
      aria-label="Loading"
      className={cn(
        'h-4 w-4 animate-spin rounded-full border-2 border-muted-foreground/30 border-t-foreground',
        className,
      )}
    />
  )
}
