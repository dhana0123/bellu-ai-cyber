import type { ReactNode } from 'react'
import { Spinner } from '@/components/ui/spinner'
import { PreviewBadge } from '@/components/common/PreviewBadge'
import type { AsyncState } from '@/useAsync'

// Shared chrome for the seven Preview add-on modules: a titled header with the
// persistent "Preview — demo data" badge, plus a <DataState> helper that
// renders loading / error / empty / populated consistently across modules.

export function AddonHeader({
  title,
  blurb,
  actions,
}: {
  title: string
  blurb: string
  actions?: ReactNode
}) {
  return (
    <div className="flex items-start justify-between gap-4">
      <div>
        <div className="flex items-center gap-2">
          <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
          <PreviewBadge />
        </div>
        <p className="text-sm text-muted-foreground">{blurb}</p>
      </div>
      {actions ? <div className="flex items-center gap-2">{actions}</div> : null}
    </div>
  )
}

export function DataState<T>({
  state,
  isEmpty,
  empty = 'Nothing to show yet.',
  children,
}: {
  state: AsyncState<T>
  isEmpty?: (data: T) => boolean
  empty?: string
  children: (data: T) => ReactNode
}) {
  if (state.loading) {
    return (
      <div className="flex h-40 items-center justify-center">
        <Spinner className="h-5 w-5" />
      </div>
    )
  }
  if (state.error) {
    return <p className="p-4 text-sm text-destructive">{state.error}</p>
  }
  if (state.data == null || (isEmpty && isEmpty(state.data))) {
    return <p className="p-4 text-sm text-muted-foreground">{empty}</p>
  }
  return <>{children(state.data)}</>
}
