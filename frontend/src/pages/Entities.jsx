import React, { useState } from 'react'
import { api } from '../api.js'
import { useAsync } from '../useAsync.js'
import { Card, CardContent } from '../components/ui/card.jsx'
import { Select } from '../components/ui/input.jsx'
import { Badge, Table, THead, TBody, TR, TH, TD, Spinner } from '../components/ui/misc.jsx'

const KINDS = [
  '', 'AIAgent', 'AIModel', 'AIService', 'MCPServer', 'Prompt',
  'CloudAccount', 'Identity', 'DataStore', 'Compute', 'Cluster', 'Workload',
  'ContainerImage', 'Repository', 'Package', 'Vulnerability',
  'NetworkEndpoint', 'ExternalService',
]

export default function Entities() {
  const [kind, setKind] = useState('')
  const { loading, error, data } = useAsync(() => api.entities(kind, 300), [kind])
  const entities = data?.entities || []

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Entities</h1>
        <p className="text-sm text-muted-foreground">Canonical nodes in the graph.</p>
      </div>

      <div className="flex items-center gap-3">
        <Select value={kind} onChange={(e) => setKind(e.target.value)} className="w-56">
          {KINDS.map((k) => <option key={k} value={k}>{k || 'All kinds'}</option>)}
        </Select>
        <span className="text-xs text-muted-foreground">{entities.length} shown</span>
      </div>

      <Card>
        <CardContent className="p-0">
          {loading ? (
            <div className="flex h-32 items-center justify-center"><Spinner /></div>
          ) : error ? (
            <p className="p-4 text-sm text-destructive">{error}</p>
          ) : (
            <Table>
              <THead>
                <TR>
                  <TH>Kind</TH><TH>Name</TH><TH>Provider</TH><TH>Canonical ID</TH><TH>Stale</TH>
                </TR>
              </THead>
              <TBody>
                {entities.map((e) => (
                  <TR key={e.canonical_id}>
                    <TD><Badge variant={e.kind?.startsWith('AI') ? 'ai' : 'secondary'}>{e.kind}</Badge></TD>
                    <TD>{e.name}</TD>
                    <TD className="text-muted-foreground">{e.provider}</TD>
                    <TD className="font-mono text-xs text-muted-foreground">{e.canonical_id}</TD>
                    <TD>{e.stale ? <Badge variant="warning">stale</Badge> : ''}</TD>
                  </TR>
                ))}
                {entities.length === 0 && (
                  <TR><TD colSpan={5} className="text-center text-muted-foreground">No entities.</TD></TR>
                )}
              </TBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
