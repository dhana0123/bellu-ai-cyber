import { client } from '@/lib/client'
import { useAsync } from '@/useAsync'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { AddonHeader, DataState } from '@/features/addons/AddonShell'
import type { AuditLogEntry, ComplianceFramework, GovernancePolicy } from '@/types/addons'

function pct(passing: number, total: number): number {
  return total === 0 ? 0 : Math.round((passing / total) * 100)
}

export default function Governance() {
  const frameworks = useAsync<ComplianceFramework[]>(() => client.governance.frameworks(), [])
  const policies = useAsync<GovernancePolicy[]>(() => client.governance.policies(), [])
  const audit = useAsync<AuditLogEntry[]>(() => client.governance.audit(), [])

  return (
    <div className="space-y-5">
      <AddonHeader
        title="Governance &amp; Compliance"
        blurb="Compliance posture, policy library and an audit trail of who changed what, across the suite."
      />

      <Tabs defaultValue="reports">
        <TabsList>
          <TabsTrigger value="reports">Compliance reports</TabsTrigger>
          <TabsTrigger value="policies">Policy library</TabsTrigger>
          <TabsTrigger value="audit">Audit log</TabsTrigger>
        </TabsList>

        <TabsContent value="reports">
          <DataState
            state={frameworks}
            isEmpty={(d) => d.length === 0}
            empty="No frameworks tracked."
          >
            {(rows) => (
              <div className="grid gap-4 md:grid-cols-2">
                {rows.map((f) => {
                  const p = pct(f.controls_passing, f.controls_total)
                  return (
                    <Card key={f.id}>
                      <CardHeader>
                        <div className="flex items-center justify-between">
                          <CardTitle className="text-base">{f.name}</CardTitle>
                          <Badge variant={p >= 80 ? 'success' : p >= 60 ? 'warning' : 'danger'}>
                            {p}%
                          </Badge>
                        </div>
                        <CardDescription>
                          {f.controls_passing} of {f.controls_total} controls passing
                        </CardDescription>
                      </CardHeader>
                      <CardContent>
                        <div className="h-2 overflow-hidden rounded-full bg-muted">
                          <div
                            className="h-full rounded-full bg-primary"
                            style={{ width: `${p}%` }}
                          />
                        </div>
                      </CardContent>
                    </Card>
                  )
                })}
              </div>
            )}
          </DataState>
        </TabsContent>

        <TabsContent value="policies">
          <Card>
            <CardContent className="p-0">
              <DataState state={policies} isEmpty={(d) => d.length === 0} empty="No policies.">
                {(rows) => (
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Policy</TableHead>
                        <TableHead>Category</TableHead>
                        <TableHead>Applies to</TableHead>
                        <TableHead>Status</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {rows.map((p) => (
                        <TableRow key={p.id}>
                          <TableCell className="font-medium">{p.name}</TableCell>
                          <TableCell>
                            <Badge variant="secondary">{p.category}</Badge>
                          </TableCell>
                          <TableCell className="text-muted-foreground">{p.applies_to}</TableCell>
                          <TableCell>
                            <Badge variant={p.enabled ? 'success' : 'outline'}>
                              {p.enabled ? 'enabled' : 'disabled'}
                            </Badge>
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                )}
              </DataState>
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="audit">
          <Card>
            <CardContent className="p-0">
              <DataState state={audit} isEmpty={(d) => d.length === 0} empty="No audit entries.">
                {(rows) => (
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Time</TableHead>
                        <TableHead>Actor</TableHead>
                        <TableHead>Module</TableHead>
                        <TableHead>Action</TableHead>
                        <TableHead>Target</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {rows.map((e) => (
                        <TableRow key={e.id}>
                          <TableCell className="font-mono text-xs text-muted-foreground">
                            {new Date(e.ts).toLocaleString()}
                          </TableCell>
                          <TableCell>{e.actor}</TableCell>
                          <TableCell>
                            <Badge variant="outline">{e.module}</Badge>
                          </TableCell>
                          <TableCell>{e.action}</TableCell>
                          <TableCell className="text-muted-foreground">{e.target}</TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                )}
              </DataState>
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
    </div>
  )
}
