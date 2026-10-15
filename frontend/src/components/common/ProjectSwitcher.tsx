import { useState } from 'react'
import { Check, ChevronsUpDown, FolderKanban, Plus } from 'lucide-react'
import { useProject } from '@/project'
import { useAuth } from '@/auth'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
} from '@/components/ui/dropdown-menu'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'

// Derive a URL-safe project key from a display name (lowercase, dashes).
function slugify(name: string): string {
  return name
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 40)
}

// ProjectSwitcher is the global project selector in the app header. It lists the
// org's projects, marks the active one, and (for users who can manage projects)
// offers "Create new project" which opens an inline dialog. The selection is the
// app-wide active project used by project-scoped views.
export function ProjectSwitcher() {
  const { projects, current, select, create, loading } = useProject()
  const { can } = useAuth()
  const canManage = can('projects:manage')

  const [dialogOpen, setDialogOpen] = useState(false)
  const [name, setName] = useState('')
  const [key, setKey] = useState('')
  const [keyEdited, setKeyEdited] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const onNameChange = (v: string) => {
    setName(v)
    if (!keyEdited) setKey(slugify(v))
  }

  const submit = async () => {
    const k = key.trim()
    const n = name.trim()
    if (!k || !n) {
      setError('Name and key are required.')
      return
    }
    setBusy(true)
    setError(null)
    try {
      await create(k, n)
      setDialogOpen(false)
      setName('')
      setKey('')
      setKeyEdited(false)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const label = current ? current.name : loading ? 'Loading…' : 'No project'

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            variant="outline"
            size="sm"
            className="h-8 max-w-[220px] gap-2"
            aria-label="Switch project"
          >
            <FolderKanban className="h-4 w-4 shrink-0 text-muted-foreground" />
            <span className="truncate">{label}</span>
            <ChevronsUpDown className="ml-auto h-3.5 w-3.5 shrink-0 opacity-60" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="w-64">
          <DropdownMenuLabel className="text-xs text-muted-foreground">Projects</DropdownMenuLabel>
          {projects.length === 0 && (
            <div className="px-2 py-1.5 text-sm text-muted-foreground">No projects yet.</div>
          )}
          {projects.map((p) => (
            <DropdownMenuItem
              key={p.id}
              onSelect={() => select(p.id)}
              className="flex items-center gap-2"
            >
              <Check
                className={cn('h-4 w-4', current?.id === p.id ? 'opacity-100' : 'opacity-0')}
              />
              <span className="truncate">{p.name}</span>
              <span className="ml-auto font-mono text-[10px] text-muted-foreground">{p.key}</span>
            </DropdownMenuItem>
          ))}
          {canManage && (
            <>
              <DropdownMenuSeparator />
              <DropdownMenuItem
                onSelect={(e) => {
                  // Keep the menu from stealing focus from the dialog.
                  e.preventDefault()
                  setDialogOpen(true)
                }}
                className="gap-2"
              >
                <Plus className="h-4 w-4" />
                Create new project
              </DropdownMenuItem>
            </>
          )}
        </DropdownMenuContent>
      </DropdownMenu>

      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Create project</DialogTitle>
            <DialogDescription>
              Projects scope connections and discovered inventory. Everything created under a
              project is visible only within it.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4 py-2">
            <div className="space-y-1.5">
              <Label htmlFor="project-name">Name</Label>
              <Input
                id="project-name"
                value={name}
                onChange={(e) => onNameChange(e.target.value)}
                placeholder="Production"
                autoFocus
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="project-key">Key</Label>
              <Input
                id="project-key"
                value={key}
                onChange={(e) => {
                  setKey(e.target.value)
                  setKeyEdited(true)
                }}
                placeholder="production"
                className="font-mono"
              />
              <p className="text-xs text-muted-foreground">
                A short, URL-safe identifier. Auto-filled from the name.
              </p>
            </div>
            {error && <p className="text-sm text-destructive">{error}</p>}
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDialogOpen(false)} disabled={busy}>
              Cancel
            </Button>
            <Button onClick={submit} disabled={busy}>
              {busy ? 'Creating…' : 'Create project'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
