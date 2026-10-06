import React from 'react'
import { api } from '../api.js'
import { useAsync } from '../useAsync.js'
import { Card, CardContent, CardHeader, CardTitle } from '../components/ui/card.jsx'
import { Spinner } from '../components/ui/misc.jsx'

export default function Dashboard() {
  const { loading, error, data } = useAsync(() => api.stats(), [])

  if (loading) return <Centered><Spinner className="h-6 w-6" /></Centered>
  if (error) return <p className="text-sm text-destructive">Failed to load stats: {error}</p>

  const cards = [
    { label: 'Raw events', value: data.raw_events },
    { label: 'Entities', value: data.entities },
    { label: 'Edges', value: data.edges },
    { label: 'Collection runs', value: data.runs },
    { label: 'Conflicts', value: data.conflicts },
  ]

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Dashboard</h1>
        <p className="text-sm text-muted-foreground">Overview of the AI security graph.</p>
      </div>

      <div className="grid grid-cols-2 gap-3 md:grid-cols-5">
        {cards.map((c) => (
          <Card key={c.label}>
            <CardContent className="p-5 text-center">
              <div className="text-3xl font-bold text-primary">{c.value}</div>
              <div className="mt-1 text-xs text-muted-foreground">{c.label}</div>
            </CardContent>
          </Card>
        ))}
      </div>

      <div className="grid gap-4 md:grid-cols-2">
        <Breakdown title="Entities by kind" obj={data.entities_by_kind} />
        <Breakdown title="Edges by relation" obj={data.edges_by_relation} />
      </div>
    </div>
  )
}

function Breakdown({ title, obj }) {
  const entries = Object.entries(obj || {}).sort((a, b) => b[1] - a[1])
  const max = entries.reduce((m, [, v]) => Math.max(m, v), 0) || 1
  return (
    <Card>
      <CardHeader><CardTitle className="text-base">{title}</CardTitle></CardHeader>
      <CardContent className="space-y-2">
        {entries.map(([k, v]) => (
          <div key={k} className="grid grid-cols-[140px_1fr_32px] items-center gap-3">
            <span className="truncate text-xs text-muted-foreground">{k}</span>
            <div className="h-2 overflow-hidden rounded-full bg-muted">
              <div className="h-full rounded-full bg-primary" style={{ width: `${(v / max) * 100}%` }} />
            </div>
            <span className="text-right text-xs tabular-nums">{v}</span>
          </div>
        ))}
      </CardContent>
    </Card>
  )
}

function Centered({ children }) {
  return <div className="flex h-40 items-center justify-center">{children}</div>
}
