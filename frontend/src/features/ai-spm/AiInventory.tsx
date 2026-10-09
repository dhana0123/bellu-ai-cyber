import { useState } from 'react'
import { client } from '@/lib/client'
import { useAsync } from '@/useAsync'
import { Card, CardContent } from '@/components/ui/card'
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
import { ENTITY_KINDS, type EntityRow } from '@/types/foundation'

// Radix Select uses a non-empty sentinel for "all" (empty string is reserved).
const ALL = '__all__'

export default function AiInventory() {
  const [kind, setKind] = useState<string>(ALL)
  const effectiveKind = kind === ALL ? '' : kind
  const { loading, error, data } = useAsync<{ entities: EntityRow[] }>(
    () => client.entities(effectiveKind, 300),
    [effectiveKind],
  )
  const entities = data?.entities ?? []

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">AI Inventory</h1>
        <p className="text-sm text-muted-foreground">
          Canonical nodes in the graph — models, agents, data stores, workloads and more.
        </p>
      </div>

      <div className="flex items-center gap-3">
        <Select value={kind} onValueChange={setKind}>
          <SelectTrigger className="w-56">
            <SelectValue placeholder="All kinds" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL}>All kinds</SelectItem>
            {ENTITY_KINDS.map((k) => (
              <SelectItem key={k} value={k}>
                {k}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <span className="text-xs text-muted-foreground">{entities.length} shown</span>
      </div>

      <Card>
        <CardContent className="p-0">
          {loading ? (
            <div className="flex h-32 items-center justify-center">
              <Spinner />
            </div>
          ) : error ? (
            <p className="p-4 text-sm text-destructive">{error}</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Kind</TableHead>
                  <TableHead>Name</TableHead>
                  <TableHead>Provider</TableHead>
                  <TableHead>Canonical ID</TableHead>
                  <TableHead>Stale</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {entities.map((e) => (
                  <TableRow key={e.canonical_id}>
                    <TableCell>
                      <Badge variant={e.kind?.startsWith('AI') ? 'ai' : 'secondary'}>
                        {e.kind}
                      </Badge>
                    </TableCell>
                    <TableCell>{e.name}</TableCell>
                    <TableCell className="text-muted-foreground">{e.provider}</TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      {e.canonical_id}
                    </TableCell>
                    <TableCell>{e.stale ? <Badge variant="warning">stale</Badge> : ''}</TableCell>
                  </TableRow>
                ))}
                {entities.length === 0 && (
                  <TableRow>
                    <TableCell colSpan={5} className="text-center text-muted-foreground">
                      No entities.
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
