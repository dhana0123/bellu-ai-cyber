import { useState, type FormEvent } from 'react'
import { client } from '@/lib/client'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import type { EdgeDetail } from '@/types/foundation'

// Investigate: find a graph edge and inspect the evidence chain backing it.
export default function Investigate() {
  const [relation, setRelation] = useState('CAN_READ')
  const [like, setLike] = useState('')
  const [edges, setEdges] = useState<EdgeDetail[]>([])
  const [detail, setDetail] = useState<EdgeDetail | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function find(e?: FormEvent) {
    e?.preventDefault()
    setError(null)
    setBusy(true)
    try {
      const res = await client.findEdges(relation, like, 100)
      setEdges(res.edges ?? [])
      setDetail(null)
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function explain(id: number) {
    setError(null)
    try {
      setDetail(await client.explainEdge(id))
    } catch (err) {
      setError((err as Error).message)
    }
  }

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Investigate</h1>
        <p className="text-sm text-muted-foreground">Trace the evidence behind any graph edge.</p>
      </div>

      <form onSubmit={find} className="flex flex-wrap items-end gap-3">
        <div className="space-y-1.5">
          <Label>Relation</Label>
          <Input
            value={relation}
            onChange={(e) => setRelation(e.target.value)}
            placeholder="CAN_READ"
            className="w-44"
          />
        </div>
        <div className="space-y-1.5">
          <Label>Endpoint contains</Label>
          <Input
            value={like}
            onChange={(e) => setLike(e.target.value)}
            placeholder="s3, pod, …"
            className="w-44"
          />
        </div>
        <Button type="submit" disabled={busy}>
          {busy ? 'Searching…' : 'Find edges'}
        </Button>
      </form>

      {error && <p className="text-sm text-destructive">{error}</p>}

      <div className="grid gap-4 md:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Edges</CardTitle>
          </CardHeader>
          <CardContent className="max-h-[460px] space-y-1.5 overflow-auto">
            {edges.map((e) => (
              <button
                key={e.ID}
                onClick={() => explain(e.ID)}
                className="flex w-full items-center gap-2 rounded-md border bg-muted/30 px-3 py-2 text-left text-xs hover:border-primary"
              >
                <span className="font-mono text-muted-foreground">#{e.ID}</span>
                <Badge variant="secondary">{e.Relation}</Badge>
                <span className="truncate font-mono text-muted-foreground">{e.SrcID}</span>
                <span className="text-primary">→</span>
                <span className="truncate font-mono text-muted-foreground">{e.DstID}</span>
              </button>
            ))}
            {edges.length === 0 && (
              <p className="text-sm text-muted-foreground">Run a search to list edges.</p>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="text-base">Evidence chain</CardTitle>
          </CardHeader>
          <CardContent>
            {!detail && (
              <p className="text-sm text-muted-foreground">Select an edge to see its evidence.</p>
            )}
            {detail && (
              <div className="space-y-3">
                <p className="font-mono text-xs text-muted-foreground">
                  #{detail.ID} {detail.SrcID} —[{detail.Relation}]→ {detail.DstID} (confidence{' '}
                  {detail.Confidence})
                </p>
                <ul className="space-y-2">
                  {(detail.Evidence ?? []).map((ev, i) => (
                    <li key={i} className="rounded-md border bg-muted/30 p-3 text-sm">
                      <Badge variant="ai">{ev.Collector}</Badge> observed{' '}
                      <strong>{ev.ResourceType}</strong>{' '}
                      <span className="font-mono text-xs text-muted-foreground">
                        {ev.ResourceID}
                      </span>
                      <div className="mt-1 text-xs text-muted-foreground">
                        run {ev.RunID} · raw {String(ev.RawEventID).slice(0, 12)}…
                      </div>
                    </li>
                  ))}
                  {(detail.Evidence ?? []).length === 0 && (
                    <li className="text-sm text-muted-foreground">No evidence recorded.</li>
                  )}
                </ul>
              </div>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
