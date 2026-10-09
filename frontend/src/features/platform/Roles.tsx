import { client } from '@/lib/client'
import { useAsync } from '@/useAsync'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Spinner } from '@/components/ui/spinner'
import type { RoleRow } from '@/types/foundation'

export default function RolesPage() {
  const { loading, error, data } = useAsync<{ roles: RoleRow[] }>(() => client.roles(), [])
  const roles = data?.roles ?? []

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Roles</h1>
        <p className="text-sm text-muted-foreground">
          Built-in and custom roles with their permissions.
        </p>
      </div>

      {loading && <Spinner />}
      {error && <p className="text-sm text-destructive">{error}</p>}

      <div className="grid gap-4 md:grid-cols-2">
        {roles.map((r) => (
          <Card key={r.key}>
            <CardHeader>
              <div className="flex items-center gap-2">
                <CardTitle className="text-base">{r.name}</CardTitle>
                {r.builtin ? (
                  <Badge variant="secondary">built-in</Badge>
                ) : (
                  <Badge variant="ai">custom</Badge>
                )}
                {r.scope_level && <Badge variant="outline">{r.scope_level}</Badge>}
              </div>
              <CardDescription>{r.description}</CardDescription>
            </CardHeader>
            <CardContent>
              <div className="flex flex-wrap gap-1.5">
                {(r.permissions ?? []).map((p) => (
                  <Badge key={p} variant="outline">
                    {p}
                  </Badge>
                ))}
              </div>
            </CardContent>
          </Card>
        ))}
      </div>
    </div>
  )
}
