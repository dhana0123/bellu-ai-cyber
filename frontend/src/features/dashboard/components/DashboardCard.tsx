import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'
import { DragHandleIcon } from './DragHandleIcon'

// A positioning shell for one grid cell, modeled on AWS Console Home widgets.
// The header is the drag zone (`.dash-drag-handle` is what react-grid-layout's
// `draggableHandle` targets): a 6-dot grid icon sits directly left of the title,
// and the whole header shows the `move` cursor. The inner dashboard card renders
// as `children` and stretches to fill the remaining cell height.
export function DashboardCard({
  title,
  children,
  className,
}: {
  title: string
  children: ReactNode
  className?: string
}) {
  return (
    <div
      className={cn(
        'flex h-full flex-col overflow-hidden rounded-lg border bg-card',
        className,
      )}
    >
      {/* Drag zone — move cursor across the whole strip and the dot icon. */}
      <div className="dash-drag-handle flex cursor-move items-center gap-2 border-b bg-muted/40 px-3 py-1.5 select-none">
        <DragHandleIcon className="cursor-move text-muted-foreground" />
        <span className="truncate text-xs font-medium text-muted-foreground">{title}</span>
      </div>

      {/* Body fills remaining height; the inner Card is forced to h-full so its
          charts/tables use the full cell. min-h-0 lets inner scroll areas work
          inside the flex column. The card loses its own border here (the shell
          provides it) to avoid a double outline. */}
      <div className="min-h-0 flex-1 overflow-auto [&>*]:h-full [&>*]:rounded-none [&>*]:border-0 [&>*]:shadow-none">
        {children}
      </div>
    </div>
  )
}
