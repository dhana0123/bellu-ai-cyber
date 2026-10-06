import React, { useState } from 'react'
import { api } from '../api.js'

// Explain lets the user find an edge (by relation / endpoint substring) and
// inspect the evidence chain backing it: which collector observed each hop.
export default function Explain() {
  const [relation, setRelation] = useState('CAN_READ')
  const [like, setLike] = useState('')
  const [edges, setEdges] = useState([])
  const [detail, setDetail] = useState(null)
  const [error, setError] = useState(null)
  const [busy, setBusy] = useState(false)

  async function find(e) {
    e?.preventDefault()
    setError(null)
    setBusy(true)
    try {
      const res = await api.findEdges(relation, like, 100)
      setEdges(res.edges || [])
      setDetail(null)
    } catch (err) {
      setError(err.message)
    } finally {
      setBusy(false)
    }
  }

  async function explain(id) {
    setError(null)
    try {
      setDetail(await api.explainEdge(id))
    } catch (err) {
      setError(err.message)
    }
  }

  return (
    <div className="stack">
      <form className="toolbar" onSubmit={find}>
        <label>
          Relation:
          <input value={relation} onChange={(e) => setRelation(e.target.value)} placeholder="CAN_READ" />
        </label>
        <label>
          Endpoint contains:
          <input value={like} onChange={(e) => setLike(e.target.value)} placeholder="s3, pod, …" />
        </label>
        <button type="submit" disabled={busy}>{busy ? 'Searching…' : 'Find edges'}</button>
      </form>

      {error && <p className="error">{error}</p>}

      <div className="two-col">
        <section className="panel">
          <h2>Edges</h2>
          <div className="edge-list">
            {edges.map((e) => (
              <button key={e.ID} className="edge-item" onClick={() => explain(e.ID)}>
                <span className="mono">#{e.ID}</span>
                <span className="tag">{e.Relation}</span>
                <span className="small mono">{e.SrcID}</span>
                <span className="arrow-mini">→</span>
                <span className="small mono">{e.DstID}</span>
              </button>
            ))}
            {edges.length === 0 && <p className="muted">Run a search to list edges.</p>}
          </div>
        </section>

        <section className="panel">
          <h2>Evidence chain</h2>
          {!detail && <p className="muted">Select an edge to see its evidence.</p>}
          {detail && (
            <div>
              <p className="mono small">
                #{detail.ID} {detail.SrcID} —[{detail.Relation}]→ {detail.DstID}{' '}
                (confidence {detail.Confidence})
              </p>
              <ul className="evidence">
                {(detail.Evidence || []).map((ev, i) => (
                  <li key={i}>
                    <span className="tag tag-ai">{ev.Collector}</span>
                    observed <strong>{ev.ResourceType}</strong>{' '}
                    <span className="mono small">{ev.ResourceID}</span>
                    <div className="muted small">run {ev.RunID} · raw {ev.RawEventID.slice(0, 12)}…</div>
                  </li>
                ))}
                {(detail.Evidence || []).length === 0 && (
                  <li className="muted">No evidence recorded.</li>
                )}
              </ul>
            </div>
          )}
        </section>
      </div>
    </div>
  )
}
