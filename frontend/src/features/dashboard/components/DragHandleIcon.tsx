import { cn } from '@/lib/utils'

// AWS Console-style drag affordance: a 6-dot matrix (two columns of three dots)
// shown to the left of a card title. Purely visual — the drag behavior comes
// from react-grid-layout's `draggableHandle` targeting the header's
// `.dash-drag-handle` class.
export function DragHandleIcon({ className }: { className?: string }) {
  return (
    <span
      className={cn('grid grid-cols-2 gap-x-[3px] gap-y-[2px]', className)}
      aria-hidden="true"
    >
      {Array.from({ length: 6 }).map((_, i) => (
        <span key={i} className="h-[3px] w-[3px] rounded-full bg-current" />
      ))}
    </span>
  )
}
