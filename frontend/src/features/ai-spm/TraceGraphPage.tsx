import { client } from '@/lib/client'
import { useAsync } from '@/useAsync'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Spinner } from '@/components/ui/spinner'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import TraceGraph from '@/components/graph/TraceGraph'
import type { AiCallRow, AttackPathRow } from '@/types/foundation'

// TraceGraph page (AI-SPM foundation). Real API: /api/graph/attack-paths and
// /api/graph/ai-calls. Visualizes internet → agent → workload → identity →
// data store, plus a "Top paths" side panel and an external AI-calls table.
export default function TraceGraphPage() {
  const paths = useAsync<{ paths: AttackPathRow[] }>(() => client.attackPaths(), [])
  const calls = useAsync<{ calls: AiCallRow[] }>(() => client.aiCalls(), [])

  const loading = paths.loading || calls.loading
  const err = paths.error || calls.error
  const pathRows = paths.data?.paths ?? []
  const callRows = calls.data?.calls ?? []

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">TraceGraph</h1>
        <p className="text-sm text-muted-foreground">
          How AI agents can reach sensitive data and external services, from the internet inward.
        </p>
      </div>

      <div className="grid gap-4 lg:grid-cols-[1fr_320px]">
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Attack path visualization</CardTitle>
            <CardDescription>
              Internet → agent → workload → identity → data store, plus external AI API calls.
            </CardDescription>
          </CardHeader>
          <CardContent>
            {loading && (
              <div className="flex h-[200px] items-center justify-center">
                <Spinner />
              </div>
            )}
            {err && <p className="text-sm text-destructive">{err}</p>}
            {!loading && !err && <TraceGraph paths={pathRows} calls={callRows} />}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="text-base">Top paths</CardTitle>
            <CardDescription>Highest-impact routes to sensitive data.</CardDescription>
          </CardHeader>
          <CardContent className="max-h-[520px] space-y-2 overflow-auto">
            {loading && <Spinner />}
            {!loading &&
              pathRows.slice(0, 20).map((p, i) => (
                <div key={i} className="rounded-md border bg-muted/30 p-3 text-xs">
                  <div className="flex items-center gap-1 font-medium">
                    <Badge variant="ai" className="px-1.5">
                      {p.agent}
                    </Badge>
                  </div>
                  <div className="mt-1 font-mono text-[11px] leading-relaxed text-muted-foreground">
                    {p.workload} → {p.service_account} → {p.cloud_identity}
                    <span className="text-primary"> {p.access || 'CAN_READ'} </span>
                    {p.datastore}
                  </div>
                </div>
              ))}
            {!loading && pathRows.length === 0 && (
              <p className="text-sm text-muted-foreground">No attack paths found.</p>
            )}
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Workloads calling external AI APIs</CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          {calls.loading && (
            <div className="p-4">
              <Spinner />
            </div>
          )}
          {calls.error && <p className="p-4 text-sm text-destructive">{calls.error}</p>}
          {calls.data && (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Agent</TableHead>
                  <TableHead>Workload</TableHead>
                  <TableHead>AI service</TableHead>
                  <TableHead>Endpoint</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {callRows.map((c, i) => (
                  <TableRow key={i}>
                    <TableCell>{c.agent || '—'}</TableCell>
                    <TableCell>{c.workload}</TableCell>
                    <TableCell>
                      <Badge variant="ai">{c.ai_service}</Badge>
                    </TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      {c.endpoint}
                    </TableCell>
                  </TableRow>
                ))}
                {callRows.length === 0 && (
                  <TableRow>
                    <TableCell colSpan={4} className="text-center text-muted-foreground">
                      No external AI calls observed.
                    </TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
