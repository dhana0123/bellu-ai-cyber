import React, { useState } from 'react'
import { api } from '../api.js'
import { useAsync } from '../useAsync.js'

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
    <div className="stack">
      <div className="toolbar">
        <label>
          Kind:
          <select value={kind} onChange={(e) => setKind(e.target.value)}>
            {KINDS.map((k) => (
              <option key={k} value={k}>
                {k || 'All kinds'}
              </option>
            ))}
          </select>
        </label>
        <span className="muted">{entities.length} shown</span>
      </div>

      {loading && <p className="muted">Loading entities…</p>}
      {error && <p className="error">{error}</p>}

      {!loading && !error && (
        <table className="table">
          <thead>
            <tr>
              <th>Kind</th>
              <th>Name</th>
              <th>Provider</th>
              <th>Canonical ID</th>
              <th>Stale</th>
            </tr>
          </thead>
          <tbody>
            {entities.map((e) => (
              <tr key={e.canonical_id}>
                <td><span className="tag">{e.kind}</span></td>
                <td>{e.name}</td>
                <td>{e.provider}</td>
                <td className="mono small">{e.canonical_id}</td>
                <td>{e.stale ? '⚠︎' : ''}</td>
              </tr>
            ))}
            {entities.length === 0 && (
              <tr><td colSpan={5} className="muted">No entities.</td></tr>
            )}
          </tbody>
        </table>
      )}
    </div>
  )
}
