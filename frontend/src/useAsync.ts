import { useCallback, useEffect, useState } from 'react'

// useAsync runs an async function and tracks loading/error/data state. `error`
// is the message STRING (not an Error) — pages render it directly. Pass a deps
// array to re-run when inputs change. Behavior preserved from the pre-migration
// JS version.
export interface AsyncState<T> {
  loading: boolean
  error: string | null
  data: T | null
}

export function useAsync<T>(fn: () => Promise<T>, deps: unknown[] = []): AsyncState<T> {
  const [state, setState] = useState<AsyncState<T>>({ loading: true, error: null, data: null })

  // eslint-disable-next-line react-hooks/exhaustive-deps
  const run = useCallback(fn, deps)

  useEffect(() => {
    let active = true
    setState((s) => ({ ...s, loading: true, error: null }))
    run()
      .then((data) => active && setState({ loading: false, error: null, data }))
      .catch((e) => active && setState({ loading: false, error: (e as Error).message, data: null }))
    return () => {
      active = false
    }
  }, [run])

  return state
}
