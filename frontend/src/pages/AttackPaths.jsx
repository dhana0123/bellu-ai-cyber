import React from 'react'
import { ArrowRight } from 'lucide-react'
import { api } from '../api.js'
import { useAsync } from '../useAsync.js'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '../components/ui/card.jsx'
import { Badge, Table, THead, TBody, TR, TH, TD, Spinner } from '../components/ui/misc.jsx'

export default function AttackPaths() {
  const paths = useAsync(() => api.attackPaths(), [])
  const calls = useAsync(() => api.aiCalls(), [])

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Attack Paths</h1>
        <p className="text-sm text-muted-foreground">How AI agents can reach sensitive data and external services.</p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Agent → Identity → Data store</CardTitle>
          <CardDescription>AI agents whose workload identity can read a sensitive data store.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          {paths.loading && <Spinner />}
          {paths.error && <p className="text-sm text-destructive">{paths.error}</p>}
          {paths.data && (paths.data.paths || []).map((p, i) => (
            <div key={i} className="flex flex-wrap items-center gap-1.5">
              <Node kind="AIAgent" label={p.agent} />
              <Edge label="RUNS_IN" />
              <Node kind="Workload" label={p.workload} />
              <Edge label="USES" />
              <Node kind="Identity" label={p.service_account} />
              <Edge label="AUTH_AS" />
              <Node kind="Identity" label={p.cloud_identity} />
              <Edge label={p.access} />
              <Node kind="DataStore" label={p.datastore} />
            </div>
          ))}
          {paths.data && (paths.data.paths || []).length === 0 && (
            <p className="text-sm text-muted-foreground">No agent → data store paths found.</p>
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

function Node({ kind, label }) {
  return (
    <div className="flex flex-col rounded-lg border bg-muted/40 px-3 py-1.5">
      <span className="text-[10px] uppercase tracking-wide text-primary">{kind}</span>
      <span className="text-sm">{label}</span>
    </div>
  )
}

function Edge({ label }) {
  return (
    <div className="flex flex-col items-center px-1 text-muted-foreground">
      <span className="text-[9px]">{label}</span>
      <ArrowRight className="h-3.5 w-3.5" />
    </div>
  )
}
