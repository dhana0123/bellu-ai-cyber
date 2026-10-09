import { useEffect, useState, type FormEvent } from 'react'
import { client } from '@/lib/client'
import { useAuth } from '@/auth'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Button } from '@/components/ui/button'
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import type { ProjectRow, RoleRow, UserRow } from '@/types/foundation'

export default function UsersPage() {
  const { can } = useAuth()
  const canManage = can('users:manage')
  const [users, setUsers] = useState<UserRow[]>([])
  const [roles, setRoles] = useState<RoleRow[]>([])
  const [projects, setProjects] = useState<ProjectRow[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [showForm, setShowForm] = useState(false)

  async function reload() {
    setLoading(true)
    setError(null)
    try {
      const [u, r, p] = await Promise.all([client.users(), client.roles(), client.projects()])
      setUsers(u.users ?? [])
      setRoles(r.roles ?? [])
      setProjects(p.projects ?? [])
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => {
    reload()
  }, [])

  async function toggleActive(u: UserRow) {
    try {
      await client.setUserActive(u.id, !u.active)
      reload()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Users</h1>
          <p className="text-sm text-muted-foreground">
            Manage who can access the platform and their scope.
          </p>
        </div>
        {canManage && (
          <Button onClick={() => setShowForm((v) => !v)}>{showForm ? 'Close' : 'Add user'}</Button>
        )}
      </div>

      {error && <p className="text-sm text-destructive">{error}</p>}

      {showForm && canManage && (
        <CreateUserForm
          roles={roles}
          projects={projects}
          onDone={() => {
            setShowForm(false)
            reload()
          }}
        />
      )}

      <Card>
        <CardContent className="p-0">
          {loading ? (
            <div className="flex h-32 items-center justify-center">
              <Spinner />
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Email</TableHead>
                  <TableHead>Name</TableHead>
                  <TableHead>Roles</TableHead>
                  <TableHead>Projects</TableHead>
                  <TableHead>Status</TableHead>
                  {canManage && <TableHead />}
                </TableRow>
              </TableHeader>
              <TableBody>
                {users.map((u) => (
                  <TableRow key={u.id}>
                    <TableCell className="font-medium">{u.email}</TableCell>
                    <TableCell>{u.name}</TableCell>
                    <TableCell className="space-x-1">
                      {(u.roles ?? []).map((r) => (
                        <Badge key={r} variant="secondary">
                          {r}
                        </Badge>
                      ))}
                    </TableCell>
                    <TableCell className="space-x-1">
                      {(u.projects ?? []).length ? (
                        u.projects!.map((p) => <Badge key={p}>{p}</Badge>)
                      ) : (
                        <span className="text-xs text-muted-foreground">org-wide</span>
                      )}
                    </TableCell>
                    <TableCell>
                      {u.active ? (
                        <Badge variant="success">active</Badge>
                      ) : (
                        <Badge variant="danger">disabled</Badge>
                      )}
                    </TableCell>
                    {canManage && (
                      <TableCell>
                        <Button variant="ghost" size="sm" onClick={() => toggleActive(u)}>
                          {u.active ? 'Disable' : 'Enable'}
                        </Button>
                      </TableCell>
                    )}
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  )
}

function CreateUserForm({
  roles,
  projects,
  onDone,
}: {
  roles: RoleRow[]
  projects: ProjectRow[]
  onDone: () => void
}) {
  const [form, setForm] = useState({
    email: '',
    name: '',
    password: '',
    role_key: 'security_analyst',
  })
  const [projectIds, setProjectIds] = useState<number[]>([])
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  const selectedRole = roles.find((r) => r.key === form.role_key)
  const isGlobal = selectedRole?.scope_level === 'global'

  function toggleProject(id: number) {
    setProjectIds((cur) => (cur.includes(id) ? cur.filter((x) => x !== id) : [...cur, id]))
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      await client.createUser({ ...form, project_ids: isGlobal ? [] : projectIds })
      onDone()
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">New user</CardTitle>
      </CardHeader>
      <CardContent>
        <form onSubmit={submit} className="grid gap-4 md:grid-cols-2">
          <div className="space-y-1.5">
            <Label>Email</Label>
            <Input
              type="email"
              required
              value={form.email}
              onChange={(e) => setForm({ ...form, email: e.target.value })}
            />
          </div>
          <div className="space-y-1.5">
            <Label>Name</Label>
            <Input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
          </div>
          <div className="space-y-1.5">
            <Label>Temporary password</Label>
            <Input
              type="password"
              required
              value={form.password}
              onChange={(e) => setForm({ ...form, password: e.target.value })}
            />
          </div>
          <div className="space-y-1.5">
            <Label>Role</Label>
            <Select value={form.role_key} onValueChange={(v) => setForm({ ...form, role_key: v })}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {roles.map((r) => (
                  <SelectItem key={r.key} value={r.key}>
                    {r.name} ({r.scope_level})
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          {!isGlobal && (
            <div className="space-y-1.5 md:col-span-2">
              <Label>Projects (scope)</Label>
              <div className="flex flex-wrap gap-2">
                {projects.length === 0 && (
                  <span className="text-xs text-muted-foreground">
                    No projects yet — create one first.
                  </span>
                )}
                {projects.map((p) => (
                  <button
                    type="button"
                    key={p.id}
                    onClick={() => toggleProject(p.id)}
                    className={`rounded-full border px-3 py-1 text-xs ${
                      projectIds.includes(p.id)
                        ? 'border-foreground bg-foreground text-background'
                        : 'border-border text-muted-foreground'
                    }`}
                  >
                    {p.name}
                  </button>
                ))}
              </div>
            </div>
          )}
          {err && <p className="text-sm text-destructive md:col-span-2">{err}</p>}
          <div className="md:col-span-2">
            <Button type="submit" disabled={busy}>
              {busy ? 'Creating…' : 'Create user'}
            </Button>
          </div>
        </form>
      </CardContent>
    </Card>
  )
}
