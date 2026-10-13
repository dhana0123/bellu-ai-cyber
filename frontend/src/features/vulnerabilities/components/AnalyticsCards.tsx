import { MoreVertical, Package, Plus, Server } from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { CountBadges } from './CountBadges'
import type { ExploitableComponent, VulnerableResource } from '@/types/vulnerabilities'

function CardMenu() {
  return (
    <div className="flex items-center gap-1">
      <Button variant="ghost" size="icon" className="h-7 w-7" aria-label="Add">
        <Plus className="h-4 w-4" />
      </Button>
      <Button variant="ghost" size="icon" className="h-7 w-7" aria-label="Options">
        <MoreVertical className="h-4 w-4" />
      </Button>
    </div>
  )
}

export function TopExploitableComponents({ items }: { items: ExploitableComponent[] }) {
  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between space-y-0 p-4 pb-2">
        <CardTitle className="text-base">Top Exploitable Components</CardTitle>
        <CardMenu />
      </CardHeader>
      <CardContent className="p-2">
        <div className="divide-y divide-border">
          {items.map((c) => (
            <div key={c.id} className="flex items-center gap-3 px-2 py-2">
              <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md bg-muted text-muted-foreground">
                <Package className="h-4 w-4" />
              </span>
              <span className="min-w-0 flex-1 truncate text-sm font-medium">{c.name}</span>
              <span className="shrink-0 font-mono text-xs text-muted-foreground">
                {c.version}
              </span>
              <div className="shrink-0">
                <CountBadges counts={c.counts} />
              </div>
            </div>
          ))}
        </div>
      </CardContent>
    </Card>
  )
}

export function TopVulnerableResources({ items }: { items: VulnerableResource[] }) {
  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between space-y-0 p-4 pb-2">
        <CardTitle className="text-base">Top Vulnerable Resources</CardTitle>
        <CardMenu />
      </CardHeader>
      <CardContent className="p-2">
        <div className="divide-y divide-border">
          {items.map((r) => (
            <div key={r.id} className="flex items-center gap-3 px-2 py-2">
              <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md bg-muted text-muted-foreground">
                <Server className="h-4 w-4" />
              </span>
              <span className="min-w-0 flex-1 truncate text-sm" title={r.name}>
                {r.name}
              </span>
              <div className="shrink-0">
                <CountBadges counts={r.counts} />
              </div>
            </div>
          ))}
        </div>
      </CardContent>
    </Card>
  )
}
