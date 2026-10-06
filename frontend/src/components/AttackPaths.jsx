import React from 'react'
import { api } from '../api.js'
import { useAsync } from '../useAsync.js'

export default function AttackPaths() {
  const paths = useAsync(() => api.attackPaths(), [])
  const calls = useAsync(() => api.aiCalls(), [])

  return (
    <div className="stack">
      <section className="panel">
        <h2>Agent → Identity → Data store</h2>
        <p className="muted">
          AI agents whose workload identity can read a sensitive data store.
        </p>
        {paths.loading && <p className="muted">Loading…</p>}
        {paths.error && <p className="error">{paths.error}</p>}
        {paths.data && (
          <div className="chains">
            {(paths.data.paths || []).map((p, i) => (
              <div className="chain" key={i}>
                <Node kind="AIAgent" label={p.agent} />
                <Arrow label="RUNS_IN" />
                <Node kind="Workload" label={p.workload} />
                <Arrow label="USES" />
                <Node kind="Identity" label={p.service_account} />
                <Arrow label="AUTHENTICATES_AS" />
                <Node kind="Identity" label={p.cloud_identity} />
                <Arrow label={p.access} />
                <Node kind="DataStore" label={p.datastore} />
              </div>
            ))}
            {(paths.data.paths || []).length === 0 && (
              <p className="muted">No agent → data store paths found.</p>
            )}
          </div>
        )}
      </section>

      <section className="panel">
        <h2>Workloads calling external AI APIs</h2>
        {calls.loading && <p className="muted">Loading…</p>}
        {calls.error && <p className="error">{calls.error}</p>}
        {calls.data && (
          <table className="table">
            <thead>
              <tr><th>Agent</th><th>Workload</th><th>AI service</th><th>Endpoint</th></tr>
            </thead>
            <tbody>
              {(calls.data.calls || []).map((c, i) => (
                <tr key={i}>
                  <td>{c.agent || '—'}</td>
                  <td>{c.workload}</td>
                  <td><span className="tag tag-ai">{c.ai_service}</span></td>
                  <td className="mono small">{c.endpoint}</td>
                </tr>
              ))}
              {(calls.data.calls || []).length === 0 && (
                <tr><td colSpan={4} className="muted">No external AI calls observed.</td></tr>
              )}
            </tbody>
          </table>
        )}
      </section>
    </div>
  )
}

function Node({ kind, label }) {
  return (
    <div className="gnode">
      <span className="gnode-kind">{kind}</span>
      <span className="gnode-label">{label}</span>
    </div>
  )
}

function Arrow({ label }) {
  return (
    <div className="garrow">
      <span className="garrow-label">{label}</span>
      <span className="garrow-line">→</span>
    </div>
  )
}
