import React, { useEffect, useState } from 'react'
import { api } from '../api.js'
import { useAuth } from '../auth.jsx'
import { Card, CardContent, CardHeader, CardTitle } from '../components/ui/card.jsx'
import { Input, Label, Select } from '../components/ui/input.jsx'
import { Button } from '../components/ui/button.jsx'
import { Badge, Table, THead, TBody, TR, TH, TD, Spinner } from '../components/ui/misc.jsx'

export default function UsersPage() {
  const { can } = useAuth()
  const canManage = can('users:manage')
  const [users, setUsers] = useState([])
  const [roles, setRoles] = useState([])
  const [projects, setProjects] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const [showForm, setShowForm] = useState(false)

  async function reload() {
    setLoading(true); setError(null)
    try {
      const [u, r, p] = await Promise.all([api.users(), api.roles(), api.projects()])
      setUsers(u.users || [])
      setRoles(r.roles || [])
      setProjects(p.projects || [])
    } catch (e) { setError(e.message) } finally { setLoading(false) }
  }
  useEffect(() => { reload() }, [])

  async function toggleActive(u) {
    try { await api.setUserActive(u.id, !u.active); reload() } catch (e) { setError(e.message) }
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Users</h1>
          <p className="text-sm text-muted-foreground">Manage who can access the platform and their scope.</p>
        </div>
        {canManage && <Button onClick={() => setShowForm((v) => !v)}>{showForm ? 'Close' : 'Add user'}</Button>}
      </div>

      {error && <p className="text-sm text-destructive">{error}</p>}

      {showForm && canManage && (
        <CreateUserForm roles={roles} projects={projects} onDone={() => { setShowForm(false); reload() }} />
      )}

      <Card>
        <CardContent className="p-0">
          {loading ? <div className="flex h-32 items-center justify-center"><Spinner /></div> : (
            <Table>
              <THead><TR><TH>Email</TH><TH>Name</TH><TH>Roles</TH><TH>Projects</TH><TH>Status</TH>{canManage && <TH></TH>}</TR></THead>
              <TBody>
                {users.map((u) => (
                  <TR key={u.id}>
                    <TD className="font-medium">{u.email}</TD>
                    <TD>{u.name}</TD>
                    <TD className="space-x-1">{(u.roles || []).map((r) => <Badge key={r} variant="secondary">{r}</Badge>)}</TD>
                    <TD className="space-x-1">
                      {(u.projects || []).length ? u.projects.map((p) => <Badge key={p}>{p}</Badge>) : <span className="text-xs text-muted-foreground">org-wide</span>}
                    </TD>
                    <TD>{u.active ? <Badge variant="success">active</Badge> : <Badge variant="danger">disabled</Badge>}</TD>
                    {canManage && (
                      <TD>
                        <Button variant="ghost" size="sm" onClick={() => toggleActive(u)}>
                          {u.active ? 'Disable' : 'Enable'}
                        </Button>
                      </TD>
                    )}
                  </TR>
                ))}
              </TBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  )
}

function CreateUserForm({ roles, projects, onDone }) {
  const [form, setForm] = useState({ email: '', name: '', password: '', role_key: 'security_analyst' })
  const [projectIds, setProjectIds] = useState([])
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)

  const selectedRole = roles.find((r) => r.key === form.role_key)
  const isGlobal = selectedRole?.scope_level === 'global'

  function toggleProject(id) {
    setProjectIds((cur) => (cur.includes(id) ? cur.filter((x) => x !== id) : [...cur, id]))
  }

  async function submit(e) {
    e.preventDefault()
    setBusy(true); setErr(null)
    try {
      await api.createUser({ ...form, project_ids: isGlobal ? [] : projectIds })
      onDone()
    } catch (e) { setErr(e.message) } finally { setBusy(false) }
  }

  return (
    <Card>
      <CardHeader><CardTitle className="text-base">New user</CardTitle></CardHeader>
      <CardContent>
        <form onSubmit={submit} className="grid gap-4 md:grid-cols-2">
          <div className="space-y-1.5"><Label>Email</Label><Input type="email" required value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} /></div>
          <div className="space-y-1.5"><Label>Name</Label><Input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} /></div>
          <div className="space-y-1.5"><Label>Temporary password</Label><Input type="password" required value={form.password} onChange={(e) => setForm({ ...form, password: e.target.value })} /></div>
          <div className="space-y-1.5">
            <Label>Role</Label>
            <Select value={form.role_key} onChange={(e) => setForm({ ...form, role_key: e.target.value })}>
              {roles.map((r) => <option key={r.key} value={r.key}>{r.name} ({r.scope_level})</option>)}
            </Select>
          </div>
          {!isGlobal && (
            <div className="space-y-1.5 md:col-span-2">
              <Label>Projects (scope)</Label>
              <div className="flex flex-wrap gap-2">
                {projects.length === 0 && <span className="text-xs text-muted-foreground">No projects yet — create one first.</span>}
                {projects.map((p) => (
                  <button type="button" key={p.id} onClick={() => toggleProject(p.id)}
                    className={`rounded-full border px-3 py-1 text-xs ${projectIds.includes(p.id) ? 'border-foreground bg-foreground text-background' : 'border-border text-muted-foreground'}`}>
                    {p.name}
                  </button>
                ))}
              </div>
            </div>
          )}
          {err && <p className="text-sm text-destructive md:col-span-2">{err}</p>}
          <div className="md:col-span-2"><Button type="submit" disabled={busy}>{busy ? 'Creating…' : 'Create user'}</Button></div>
        </form>
      </CardContent>
    </Card>
  )
}
