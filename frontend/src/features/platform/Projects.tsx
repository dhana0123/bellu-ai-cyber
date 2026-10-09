import { useEffect, useState, type FormEvent } from 'react'
import { client } from '@/lib/client'
import { useAuth } from '@/auth'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Button } from '@/components/ui/button'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Spinner } from '@/components/ui/spinner'
import type { ProjectRow } from '@/types/foundation'

export default function ProjectsPage() {
  const { can } = useAuth()
  const canManage = can('projects:manage')
  const [projects, setProjects] = useState<ProjectRow[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [form, setForm] = useState({ key: '', name: '' })
  const [busy, setBusy] = useState(false)

  async function reload() {
    setLoading(true)
    setError(null)
    try {
      setProjects((await client.projects()).projects ?? [])
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => {
    reload()
  }, [])

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await client.createProject(form.key, form.name)
      setForm({ key: '', name: '' })
      reload()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Projects</h1>
        <p className="text-sm text-muted-foreground">
          Projects group resources and scope who can see them.
        </p>
      </div>

      {error && <p className="text-sm text-destructive">{error}</p>}

      {canManage && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">New project</CardTitle>
          </CardHeader>
          <CardContent>
            <form onSubmit={submit} className="flex flex-wrap items-end gap-3">
              <div className="space-y-1.5">
                <Label>Key</Label>
                <Input
                  required
                  value={form.key}
                  onChange={(e) => setForm({ ...form, key: e.target.value })}
                  placeholder="prod-ai"
                  className="w-44"
                />
              </div>
              <div className="space-y-1.5">
                <Label>Name</Label>
                <Input
                  required
                  value={form.name}
                  onChange={(e) => setForm({ ...form, name: e.target.value })}
                  placeholder="Production AI"
                  className="w-56"
                />
              </div>
              <Button type="submit" disabled={busy}>
                {busy ? 'Creating…' : 'Create project'}
              </Button>
            </form>
          </CardContent>
        </Card>
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
                  <TableHead>Key</TableHead>
                  <TableHead>Name</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {projects.map((p) => (
                  <TableRow key={p.id}>
                    <TableCell className="font-mono text-xs">{p.key}</TableCell>
                    <TableCell>{p.name}</TableCell>
                  </TableRow>
                ))}
                {projects.length === 0 && (
                  <TableRow>
                    <TableCell colSpan={2} className="text-center text-muted-foreground">
                      No projects yet.
                    </TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
