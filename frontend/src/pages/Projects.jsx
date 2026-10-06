import React, { useEffect, useState } from 'react'
import { api } from '../api.js'
import { useAuth } from '../auth.jsx'
import { Card, CardContent, CardHeader, CardTitle } from '../components/ui/card.jsx'
import { Input, Label } from '../components/ui/input.jsx'
import { Button } from '../components/ui/button.jsx'
import { Table, THead, TBody, TR, TH, TD, Spinner } from '../components/ui/misc.jsx'

export default function ProjectsPage() {
  const { can } = useAuth()
  const canManage = can('projects:manage')
  const [projects, setProjects] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const [form, setForm] = useState({ key: '', name: '' })
  const [busy, setBusy] = useState(false)

  async function reload() {
    setLoading(true); setError(null)
    try { setProjects((await api.projects()).projects || []) }
    catch (e) { setError(e.message) } finally { setLoading(false) }
  }
  useEffect(() => { reload() }, [])

  async function submit(e) {
    e.preventDefault()
    setBusy(true); setError(null)
    try { await api.createProject(form.key, form.name); setForm({ key: '', name: '' }); reload() }
    catch (e) { setError(e.message) } finally { setBusy(false) }
  }

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Projects</h1>
        <p className="text-sm text-muted-foreground">Projects group resources and scope who can see them.</p>
      </div>

      {error && <p className="text-sm text-destructive">{error}</p>}

      {canManage && (
        <Card>
          <CardHeader><CardTitle className="text-base">New project</CardTitle></CardHeader>
          <CardContent>
            <form onSubmit={submit} className="flex flex-wrap items-end gap-3">
              <div className="space-y-1.5"><Label>Key</Label><Input required value={form.key} onChange={(e) => setForm({ ...form, key: e.target.value })} placeholder="prod-ai" className="w-44" /></div>
              <div className="space-y-1.5"><Label>Name</Label><Input required value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="Production AI" className="w-56" /></div>
              <Button type="submit" disabled={busy}>{busy ? 'Creating…' : 'Create project'}</Button>
            </form>
          </CardContent>
        </Card>
      )}

      <Card>
        <CardContent className="p-0">
          {loading ? <div className="flex h-32 items-center justify-center"><Spinner /></div> : (
            <Table>
              <THead><TR><TH>Key</TH><TH>Name</TH></TR></THead>
              <TBody>
                {projects.map((p) => <TR key={p.id}><TD className="font-mono text-xs">{p.key}</TD><TD>{p.name}</TD></TR>)}
                {projects.length === 0 && <TR><TD colSpan={2} className="text-center text-muted-foreground">No projects yet.</TD></TR>}
              </TBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
