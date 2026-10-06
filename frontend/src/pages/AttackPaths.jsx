import React from 'react'
import { api } from '../api.js'
import { useAsync } from '../useAsync.js'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '../components/ui/card.jsx'
import { Badge, Table, THead, TBody, TR, TH, TD, Spinner } from '../components/ui/misc.jsx'
import AttackPathGraph from '../components/graph/AttackPathGraph.jsx'

export default function AttackPaths() {
  const paths = useAsync(() => api.attackPaths(), [])
  const calls = useAsync(() => api.aiCalls(), [])

  const loading = paths.loading || calls.loading
  const err = paths.error || calls.error

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Attack Paths</h1>
        <p className="text-sm text-muted-foreground">
          How AI agents can reach sensitive data and external services, from the internet inward.
        </p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Attack Path Visualization</CardTitle>
          <CardDescription>
            Internet → agent → workload → identity → data store, plus external AI API calls.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {loading && (
            <div className="flex h-[200px] items-center justify-center"><Spinner /></div>
          )}
          {err && <p className="text-sm text-destructive">{err}</p>}
          {!loading && !err && (
            <AttackPathGraph paths={paths.data?.paths} calls={calls.data?.calls} />
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Workloads calling external AI APIs</CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          {calls.loading && <div className="p-4"><Spinner /></div>}
          {calls.error && <p className="p-4 text-sm text-destructive">{calls.error}</p>}
          {calls.data && (
            <Table>
              <THead><TR><TH>Agent</TH><TH>Workload</TH><TH>AI service</TH><TH>Endpoint</TH></TR></THead>
              <TBody>
                {(calls.data.calls || []).map((c, i) => (
                  <TR key={i}>
                    <TD>{c.agent || '—'}</TD>
                    <TD>{c.workload}</TD>
                    <TD><Badge variant="ai">{c.ai_service}</Badge></TD>
                    <TD className="font-mono text-xs text-muted-foreground">{c.endpoint}</TD>
                  </TR>
                ))}
                {(calls.data.calls || []).length === 0 && (
                  <TR><TD colSpan={4} className="text-center text-muted-foreground">No external AI calls observed.</TD></TR>
                )}
              </TBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
