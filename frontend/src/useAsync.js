import { useEffect, useState, useCallback } from 'react'

// useAsync runs an async function and tracks loading/error/data state. Pass a
// deps array to re-run when inputs change.
export function useAsync(fn, deps = []) {
  const [state, setState] = useState({ loading: true, error: null, data: null })

  // eslint-disable-next-line react-hooks/exhaustive-deps
  const run = useCallback(fn, deps)

  useEffect(() => {
    let active = true
    setState((s) => ({ ...s, loading: true, error: null }))
    run()
      .then((data) => active && setState({ loading: false, error: null, data }))
      .catch((e) => active && setState({ loading: false, error: e.message, data: null }))
    return () => {
      active = false
    }
  }, [run])

  return state
}
