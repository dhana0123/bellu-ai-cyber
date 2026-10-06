import React from 'react'
import { api } from '../api.js'
import { useAsync } from '../useAsync.js'

export default function Dashboard() {
  const { loading, error, data } = useAsync(() => api.stats(), [])

  if (loading) return <p className="muted">Loading stats…</p>
  if (error) return <p className="error">Failed to load stats: {error}</p>

  const cards = [
    { label: 'Raw events', value: data.raw_events },
    { label: 'Entities', value: data.entities },
    { label: 'Edges', value: data.edges },
    { label: 'Collection runs', value: data.runs },
    { label: 'Conflicts', value: data.conflicts },
  ]

  return (
    <div className="stack">
      <section className="cards">
        {cards.map((c) => (
          <div className="card" key={c.label}>
            <div className="card-value">{c.value}</div>
            <div className="card-label">{c.label}</div>
          </div>
        ))}
      </section>

      <div className="two-col">
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
    <section className="panel">
      <h2>{title}</h2>
      <div className="bars">
        {entries.map(([k, v]) => (
          <div className="bar-row" key={k}>
            <span className="bar-label">{k}</span>
            <span className="bar-track">
              <span className="bar-fill" style={{ width: `${(v / max) * 100}%` }} />
            </span>
            <span className="bar-value">{v}</span>
          </div>
        ))}
      </div>
    </section>
  )
}
