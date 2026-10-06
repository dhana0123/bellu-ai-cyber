import React from 'react'
import { api } from '../api.js'
import { useAsync } from '../useAsync.js'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '../components/ui/card.jsx'
import { Badge, Spinner } from '../components/ui/misc.jsx'

export default function RolesPage() {
  const { loading, error, data } = useAsync(() => api.roles(), [])
  const roles = data?.roles || []

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Roles</h1>
        <p className="text-sm text-muted-foreground">Built-in and custom roles with their permissions.</p>
      </div>

      {loading && <Spinner />}
      {error && <p className="text-sm text-destructive">{error}</p>}

      <div className="grid gap-4 md:grid-cols-2">
        {roles.map((r) => (
          <Card key={r.id}>
            <CardHeader>
              <div className="flex items-center gap-2">
                <CardTitle className="text-base">{r.name}</CardTitle>
                {r.builtin ? <Badge variant="secondary">built-in</Badge> : <Badge variant="ai">custom</Badge>}
                <Badge variant="outline">{r.scope_level}</Badge>
              </div>
              <CardDescription>{r.description}</CardDescription>
            </CardHeader>
            <CardContent>
              <div className="flex flex-wrap gap-1.5">
                {(r.permissions || []).map((p) => <Badge key={p} variant="outline">{p}</Badge>)}
              </div>
            </CardContent>
          </Card>
        ))}
      </div>
    </div>
  )
}
