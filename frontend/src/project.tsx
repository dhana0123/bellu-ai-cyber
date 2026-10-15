import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react'
import { realApi } from '@/lib/api'
import { useAuth } from '@/auth'
import type { ProjectRow } from '@/types/foundation'

// LocalStorage key for the operator's last-selected project, so the choice
// survives reloads. Scoped per app, not per user — cleared on logout below.
const CURRENT_PROJECT_KEY = 'bellu-current-project'

interface ProjectContextValue {
  projects: ProjectRow[]
  current: ProjectRow | null
  loading: boolean
  error: string | null
  /** Select the active project by id. No-op if the id is unknown. */
  select: (id: number) => void
  /** Reload the project list from the backend. */
  refresh: () => Promise<void>
  /** Create a project, refresh the list, select it, and return it. */
  create: (key: string, name: string) => Promise<ProjectRow>
}

const ProjectContext = createContext<ProjectContextValue | null>(null)

function readStoredId(): number | null {
  const raw = localStorage.getItem(CURRENT_PROJECT_KEY)
  if (!raw) return null
  const n = Number(raw)
  return Number.isFinite(n) ? n : null
}

// ProjectProvider loads the org's projects once the user is authenticated and
// tracks the active project. The active project is the single source of truth
// for project-scoped views (inventory, connectors) across the app.
export function ProjectProvider({ children }: { children: ReactNode }) {
  const { principal } = useAuth()
  const [projects, setProjects] = useState<ProjectRow[]>([])
  const [currentId, setCurrentId] = useState<number | null>(readStoredId())
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const refresh = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const { projects: rows } = await realApi.projects()
      setProjects(rows)
      // Reconcile the selection: keep the stored id if still valid, else default
      // to the first project.
      setCurrentId((prev) => {
        if (prev != null && rows.some((p) => p.id === prev)) return prev
        return rows[0]?.id ?? null
      })
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [])

  // Load projects when a principal is present; clear when logged out.
  useEffect(() => {
    if (principal) {
      void refresh()
    } else {
      setProjects([])
      setCurrentId(null)
      localStorage.removeItem(CURRENT_PROJECT_KEY)
    }
  }, [principal, refresh])

  // Persist the active project id.
  useEffect(() => {
    if (currentId != null) localStorage.setItem(CURRENT_PROJECT_KEY, String(currentId))
  }, [currentId])

  const select = useCallback(
    (id: number) => {
      if (projects.some((p) => p.id === id)) setCurrentId(id)
    },
    [projects],
  )

  const create = useCallback(async (key: string, name: string): Promise<ProjectRow> => {
    const { id } = await realApi.createProject(key, name)
    const row: ProjectRow = { id, key, name }
    // Refresh from the backend for the canonical list, then select the new one.
    const { projects: rows } = await realApi.projects()
    setProjects(rows)
    setCurrentId(id)
    return rows.find((p) => p.id === id) ?? row
  }, [])

  const current = useMemo(
    () => projects.find((p) => p.id === currentId) ?? null,
    [projects, currentId],
  )

  const value: ProjectContextValue = {
    projects,
    current,
    loading,
    error,
    select,
    refresh,
    create,
  }

  return <ProjectContext.Provider value={value}>{children}</ProjectContext.Provider>
}

export function useProject(): ProjectContextValue {
  const ctx = useContext(ProjectContext)
  if (!ctx) throw new Error('useProject must be used within a ProjectProvider')
  return ctx
}
