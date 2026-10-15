import { useCallback, useEffect, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { ArrowRight, FolderKanban, Pencil, Plus, RefreshCw, Trash2 } from 'lucide-react'
import { client } from '@/lib/client'
import { useProject } from '@/project'
import { useAuth } from '@/auth'
import { cn } from '@/lib/utils'
import { Card, CardContent } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Spinner } from '@/components/ui/spinner'
import { ProviderIcon } from '@/features/ai-inventory/components/category'
import type { Connection, ConnectorStatus } from '@/types/foundation'

function statusVariant(
  s: ConnectorStatus,
): 'default' | 'secondary' | 'success' | 'warning' | 'danger' {
  switch (s) {
    case 'COMPLETE':
    case 'CONNECTED':
      return 'success'
    case 'DISCOVERY_RUNNING':
    case 'VERIFYING':
      return 'warning'
    case 'ERROR':
      return 'danger'
    default:
      return 'secondary'
  }
}

// The cloud providers Bellu can connect, each shown as a card. Only AWS is wired
// end-to-end today; the others are shown as "coming soon".
interface ProviderCard {
  id: string
  name: string
  description: string
  available: boolean
}

const PROVIDERS: ProviderCard[] = [
  {
    id: 'aws',
    name: 'Amazon Web Services',
    description: 'Scan an AWS account or organization for cloud security posture.',
    available: true,
  },
  {
    id: 'gcp',
    name: 'Google Cloud',
    description: 'Connect a Google Cloud project to map its security posture.',
    available: false,
  },
  {
    id: 'azure',
    name: 'Microsoft Azure',
    description: 'Connect an Azure subscription for cloud visibility.',
    available: false,
  },
]

export default function ConnectorsPage() {
  const { current, projects } = useProject()
  const { can } = useAuth()
  const navigate = useNavigate()
  const canManage = can('integrations:manage')
  const projectId = current?.id

  // Map project id -> name for the per-connector project label.
  const projectName = (id: number) => projects.find((p) => p.id === id)?.name ?? `Project ${id}`

  const [conns, setConns] = useState<Connection[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [deleting, setDeleting] = useState<number>(0)

  const reload = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const { connections } = await client.connectors(projectId)
      setConns(connections ?? [])
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [projectId])

  useEffect(() => {
    void reload()
  }, [reload])

  async function remove(id: number) {
    if (!window.confirm('Delete this connection? This cannot be undone.')) return
    setDeleting(id)
    setError(null)
    try {
      await client.deleteConnector(id)
      await reload()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setDeleting(0)
    }
  }

  function connect(p: ProviderCard) {
    if (!p.available || !canManage) return
    navigate('/connectors/new/aws')
  }

  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Cloud Connectors</h1>
          <p className="text-sm text-muted-foreground">
            Connect a cloud account so Bellu can collect and map its security posture.
            {current ? (
              <>
                {' '}
                Project: <span className="font-medium text-foreground">{current.name}</span>.
              </>
            ) : null}
          </p>
        </div>
        <Button variant="outline" size="sm" onClick={() => void reload()}>
          <RefreshCw className="h-4 w-4" /> Refresh
        </Button>
      </div>

      {error && <p className="text-sm text-destructive">{error}</p>}

      {/* Provider cards: pick a cloud to connect. */}
      <section className="space-y-3">
        <h2 className="text-sm font-medium text-muted-foreground">Add a connector</h2>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {PROVIDERS.map((p) => {
            const clickable = p.available && canManage
            return (
              <button
                key={p.id}
                type="button"
                onClick={() => connect(p)}
                disabled={!clickable}
                className={cn(
                  'group flex flex-col gap-3 rounded-lg border p-5 text-left transition-colors',
                  clickable
                    ? 'cursor-pointer bg-card hover:border-primary/50 hover:bg-accent'
                    : 'cursor-default bg-muted/30 opacity-80',
                )}
              >
                <div className="flex items-center gap-3">
                  <ProviderIcon provider={p.id} className="h-9 w-9" />
                  <div className="min-w-0 flex-1">
                    <div className="truncate font-semibold">{p.name}</div>
                  </div>
                  {p.available ? (
                    canManage ? (
                      <ArrowRight className="h-4 w-4 text-muted-foreground transition-transform group-hover:translate-x-0.5" />
                    ) : null
                  ) : (
                    <Badge variant="secondary" className="text-[10px]">
                      Soon
                    </Badge>
                  )}
                </div>
                <p className="text-sm text-muted-foreground">{p.description}</p>
                {p.available && canManage && (
                  <span className="mt-auto inline-flex items-center gap-1 text-xs font-medium text-primary">
                    <Plus className="h-3.5 w-3.5" /> Connect
                  </span>
                )}
              </button>
            )
          })}
        </div>
      </section>

      {/* Connected accounts for this project. */}
      <section className="space-y-3">
        <h2 className="text-sm font-medium text-muted-foreground">Connected</h2>
        {loading ? (
          <div className="flex h-32 items-center justify-center">
            <Spinner />
          </div>
        ) : conns.length === 0 ? (
          <Card>
            <CardContent className="py-10 text-center text-sm text-muted-foreground">
              No cloud connectors yet{current ? ` in ${current.name}` : ''}. Pick a provider above
              to get started.
            </CardContent>
          </Card>
        ) : (
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {conns.map((c) => (
              <Card key={c.id}>
                <CardContent className="space-y-3 p-5">
                  <div className="flex items-center gap-3">
                    <ProviderIcon provider={c.provider} className="h-8 w-8" />
                    <div className="min-w-0 flex-1">
                      <div className="truncate text-sm font-semibold">
                        {c.account_id || 'AWS account'}
                      </div>
                      <div className="text-xs capitalize text-muted-foreground">
                        {c.scope} scope
                      </div>
                    </div>
                    <Badge variant={statusVariant(c.status)}>{c.status}</Badge>
                  </div>

                  {/* Project label */}
                  <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
                    <FolderKanban className="h-3.5 w-3.5" />
                    <span className="truncate">{projectName(c.project_id)}</span>
                  </div>

                  <div>
                    <div className="mb-1 text-xs text-muted-foreground">Capabilities</div>
                    <div className="flex flex-wrap gap-1">
                      {(c.capabilities ?? []).map((cap) => (
                        <Badge key={cap} variant="outline" className="text-[10px]">
                          {cap}
                        </Badge>
                      ))}
                    </div>
                  </div>

                  <div className="flex items-center gap-2 pt-1">
                    {canManage && (
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => navigate(`/connectors/new/aws?id=${c.id}&edit=1`)}
                      >
                        <Pencil className="h-3.5 w-3.5" /> Edit
                      </Button>
                    )}
                    <Link
                      to={`/connectors/new/aws?id=${c.id}`}
                      className="text-xs text-primary hover:underline"
                    >
                      Open
                    </Link>
                    {canManage && (
                      <button
                        onClick={() => remove(c.id)}
                        disabled={deleting === c.id}
                        className="ml-auto text-muted-foreground hover:text-destructive disabled:opacity-50"
                        title="Delete connection"
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </button>
                    )}
                  </div>
                </CardContent>
              </Card>
            ))}
          </div>
        )}
      </section>
    </div>
  )
}
