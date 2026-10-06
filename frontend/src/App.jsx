import React, { useEffect, useState } from 'react'
import { api } from './api.js'
import Dashboard from './components/Dashboard.jsx'
import Entities from './components/Entities.jsx'
import AttackPaths from './components/AttackPaths.jsx'
import Explain from './components/Explain.jsx'

const TABS = [
  { id: 'dashboard', label: 'Dashboard' },
  { id: 'entities', label: 'Entities' },
  { id: 'paths', label: 'Attack Paths' },
  { id: 'explain', label: 'Explain Edge' },
]

export default function App() {
  const [tab, setTab] = useState('dashboard')
  const [health, setHealth] = useState(null)
  const [error, setError] = useState(null)

  useEffect(() => {
    api
      .health()
      .then(setHealth)
      .catch((e) => setError(e.message))
  }, [])

  return (
    <div className="app">
      <header className="topbar">
        <div className="brand">
          <span className="logo">◆</span>
          <div>
            <h1>Bellu</h1>
            <p>AI Security Graph — Control Panel</p>
          </div>
        </div>
        <div className="health">
          {error ? (
            <span className="pill pill-bad">API unreachable</span>
          ) : health ? (
            <>
              <span className="pill pill-ok">API ok</span>
              <span className={`pill ${health.graph ? 'pill-ok' : 'pill-warn'}`}>
                graph {health.graph ? 'connected' : 'offline'}
              </span>
            </>
          ) : (
            <span className="pill">connecting…</span>
          )}
        </div>
      </header>

      <nav className="tabs">
        {TABS.map((t) => (
          <button
            key={t.id}
            className={tab === t.id ? 'tab active' : 'tab'}
            onClick={() => setTab(t.id)}
          >
            {t.label}
          </button>
        ))}
      </nav>

      <main className="content">
        {tab === 'dashboard' && <Dashboard />}
        {tab === 'entities' && <Entities />}
        {tab === 'paths' && <AttackPaths />}
        {tab === 'explain' && <Explain />}
      </main>

      <footer className="footer">
        Evidence-backed graph of multi-cloud, Kubernetes, SBOM/AI-BOM and network data.
      </footer>
    </div>
  )
}
