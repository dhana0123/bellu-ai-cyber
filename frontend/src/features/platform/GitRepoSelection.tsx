import { useCallback, useEffect, useMemo, useState } from 'react'
import { Globe, Lock, RefreshCw } from 'lucide-react'
import { client } from '@/lib/client'
import { useProject } from '@/project'
import { Card, CardContent } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Spinner } from '@/components/ui/spinner'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { ProviderIcon } from '@/features/ai-inventory/components/category'
import type {
  Connection,
  ConnectorRepositoriesView,
  RepoSelectionMode,
} from '@/types/foundation'

const GIT_PROVIDERS = new Set(['github', 'gitlab', 'atlassian', 'bitbucket'])

// GitRepoSelection lets the operator choose which repositories a connected git
// provider should scan: a radio toggle (all vs selected) plus a per-repo
// checklist. Connecting a git provider selects all repos by default.
export default function GitRepoSelection() {
  const { current } = useProject()
  const projectId = current?.id

  const [gitConns, setGitConns] = useState<Connection[]>([])
  const [connId, setConnId] = useState<string>('')
  const [view, setView] = useState<ConnectorRepositoriesView | null>(null)
  const [loading, setLoading] = useState(true)
  const [reposLoading, setReposLoading] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // Load the project's connectors and keep only git ones.
  useEffect(() => {
    let cancelled = false
    setLoading(true)
    client
      .connectors(projectId)
      .then(({ connections }) => {
        if (cancelled) return
        const git = (connections ?? []).filter((c) => GIT_PROVIDERS.has(c.provider.toLowerCase()))
        setGitConns(git)
        if (git.length && !connId) setConnId(String(git[0].id))
      })
      .catch((e) => !cancelled && setError((e as Error).message))
      .finally(() => !cancelled && setLoading(false))
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId])

  const loadRepos = useCallback(async (id: string) => {
    if (!id) return
    setReposLoading(true)
    setError(null)
    try {
      setView(await client.connectorRepositories(id))
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setReposLoading(false)
    }
  }, [])

  useEffect(() => {
    if (connId) void loadRepos(connId)
  }, [connId, loadRepos])

  async function refresh() {
    if (!connId) return
    setBusy(true)
    setError(null)
    try {
      setView(await client.refreshConnectorRepositories(connId))
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function setMode(mode: RepoSelectionMode) {
    if (!connId) return
    setBusy(true)
    setError(null)
    try {
      setView(await client.setConnectorRepoMode(connId, mode))
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function toggleRepo(repoId: number, selected: boolean) {
    if (!connId) return
    setBusy(true)
    setError(null)
    try {
      setView(await client.setConnectorRepositorySelection(connId, [repoId], selected))
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const mode = view?.mode ?? 'all'
  const repos = useMemo(() => view?.repositories ?? [], [view])
  const selectedCount = repos.filter((r) => r.selected).length

  if (loading) {
    return (
      <div className="flex h-24 items-center justify-center">
        <Spinner />
      </div>
    )
  }

  if (gitConns.length === 0) return null

  const activeConn = gitConns.find((c) => String(c.id) === connId)

  return (
    <Card>
      <CardContent className="space-y-5 p-5">
        <div className="flex flex-wrap items-center gap-3">
          <div className="min-w-0 flex-1">
            <div className="text-sm font-semibold">Repository access</div>
            <p className="text-xs text-muted-foreground">
              Choose which repositories this git connector scans.
            </p>
          </div>
          {gitConns.length > 1 && (
            <Select value={connId} onValueChange={setConnId}>
              <SelectTrigger className="w-56">
                <SelectValue placeholder="Select a connector" />
              </SelectTrigger>
              <SelectContent>
                {gitConns.map((c) => (
                  <SelectItem key={c.id} value={String(c.id)}>
                    {c.provider} · {c.account_id || `#${c.id}`}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
          {activeConn && (
            <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
              <ProviderIcon provider={activeConn.provider} className="h-4 w-4" />
              {activeConn.provider}
            </span>
          )}
          <Button variant="outline" size="sm" onClick={refresh} disabled={busy}>
            <RefreshCw className="h-4 w-4" /> Refresh
          </Button>
        </div>

        {error && <p className="text-sm text-destructive">{error}</p>}

        {/* Scan scope radio. */}
        <fieldset className="space-y-2" disabled={busy}>
          <legend className="sr-only">Repository scan scope</legend>
          <label className="flex items-start gap-2 text-sm">
            <input
              type="radio"
              name="repo-mode"
              className="mt-0.5"
              checked={mode === 'all'}
              onChange={() => void setMode('all')}
            />
            <span>
              <span className="font-medium">Scan all repositories</span>
              <span className="block text-xs text-muted-foreground">
                Every repository this connector can access, including ones added later.
              </span>
            </span>
          </label>
          <label className="flex items-start gap-2 text-sm">
            <input
              type="radio"
              name="repo-mode"
              className="mt-0.5"
              checked={mode === 'selected'}
              onChange={() => void setMode('selected')}
            />
            <span>
              <span className="font-medium">Scan selected repositories only</span>
              <span className="block text-xs text-muted-foreground">
                Only the repositories you tick below ({selectedCount} selected).
              </span>
            </span>
          </label>
        </fieldset>

        {/* Repo checklist. */}
        <div className="rounded-md border">
          {reposLoading ? (
            <div className="flex h-24 items-center justify-center">
              <Spinner />
            </div>
          ) : repos.length === 0 ? (
            <div className="py-8 text-center text-sm text-muted-foreground">
              No repositories found for this connector yet. Click Refresh to fetch them.
            </div>
          ) : (
            <ul className="divide-y">
              {repos.map((r) => (
                <li key={r.id} className="flex items-center gap-3 px-4 py-2.5 text-sm">
                  <Checkbox
                    checked={r.selected}
                    disabled={mode === 'all' || busy}
                    onCheckedChange={(v) => void toggleRepo(r.id, Boolean(v))}
                  />
                  <span className="min-w-0 flex-1 truncate font-medium">{r.full_name}</span>
                  {r.default_branch && (
                    <Badge variant="outline" className="text-[10px]">
                      {r.default_branch}
                    </Badge>
                  )}
                  {r.private ? (
                    <Lock className="h-3.5 w-3.5 text-muted-foreground" aria-label="Private" />
                  ) : (
                    <Globe className="h-3.5 w-3.5 text-muted-foreground" aria-label="Public" />
                  )}
                </li>
              ))}
            </ul>
          )}
        </div>
      </CardContent>
    </Card>
  )
}
