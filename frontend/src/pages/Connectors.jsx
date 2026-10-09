import React, { useEffect, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { Cloud, Plus, RefreshCw, Trash2 } from 'lucide-react'
import { api } from '../api.js'
import { useAuth } from '../auth.jsx'
import { Card, CardContent } from '../components/ui/card.jsx'
import { Button } from '../components/ui/button.jsx'
import { Table, THead, TBody, TR, TH, TD, Spinner, Badge } from '../components/ui/misc.jsx'

// statusVariant maps a connection status to a badge color.
function statusVariant(status) {
  switch (status) {
    case 'CONNECTED':
    case 'COMPLETE':
      return 'success'
    case 'DISCOVERY_RUNNING':
    case 'VERIFYING':
      return 'warning'
    case 'ERROR':
      return 'danger'
    default:
      return 'outline'
  }
}

export default function ConnectorsPage() {
  const { can } = useAuth()
  const navigate = useNavigate()
  const canManage = can('integrations:manage')
  const [conns, setConns] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const [deleting, setDeleting] = useState(0)

  async function reload() {
    setLoading(true)
    setError(null)
    try {
      setConns((await api.connectors()).connections || [])
    } catch (e) {
      setError(e.message)
    } finally {
      setLoading(false)
    }
  }
  async function remove(id) {
    if (!window.confirm('Delete this connection? This cannot be undone.')) return
    setDeleting(id)
    setError(null)
    try {
      await api.deleteConnector(id)
      await reload()
    } catch (e) {
      setError(e.message)
    } finally {
      setDeleting(0)
    }
  }

  useEffect(() => {
    reload()
  }, [])

  return (
    <div className="space-y-4">
      <div className="flex items-start justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Cloud Connectors</h1>
          <p className="text-sm text-muted-foreground">
            Connect a cloud account so Bellu can collect and map its security posture.
          </p>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" onClick={reload}>
            <RefreshCw className="h-4 w-4" /> Refresh
          </Button>
          {canManage && (
            <Button size="sm" onClick={() => navigate('/connectors/new/aws')}>
              <Plus className="h-4 w-4" /> Connect AWS
            </Button>
          )}
        </div>
      </div>

      {error && <p className="text-sm text-destructive">{error}</p>}

      <Card>
        <CardContent className="p-0">
          {loading ? (
            <div className="flex h-32 items-center justify-center">
              <Spinner />
            </div>
          ) : (
            <Table>
              <THead>
                <TR>
                  <TH>Provider</TH>
                  <TH>Account</TH>
                  <TH>Scope</TH>
                  <TH>Capabilities</TH>
                  <TH>Status</TH>
                  <TH></TH>
                </TR>
              </THead>
              <TBody>
                {conns.map((c) => (
                  <TR key={c.id}>
                    <TD className="font-medium uppercase">{c.provider}</TD>
                    <TD className="font-mono text-xs">{c.account_id}</TD>
                    <TD className="capitalize">{c.scope}</TD>
                    <TD className="text-xs text-muted-foreground">
                      {(c.capabilities || []).join(', ')}
                    </TD>
                    <TD>
                      <Badge variant={statusVariant(c.status)}>{c.status}</Badge>
                    </TD>
                    <TD className="text-right">
                      <div className="flex items-center justify-end gap-3">
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
                            className="text-muted-foreground hover:text-destructive disabled:opacity-50"
                            title="Delete connection"
                          >
                            <Trash2 className="h-3.5 w-3.5" />
                          </button>
                        )}
                      </div>
                    </TD>
                  </TR>
                ))}
                {conns.length === 0 && (
                  <TR>
                    <TD colSpan={6} className="py-10 text-center text-muted-foreground">
                      <Cloud className="mx-auto mb-2 h-6 w-6 opacity-40" />
                      No cloud connectors yet.
                      {canManage && ' Click "Connect AWS" to get started.'}
                    </TD>
                  </TR>
                )}
              </TBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
