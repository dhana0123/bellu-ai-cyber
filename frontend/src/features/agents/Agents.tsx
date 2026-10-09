import { client } from '@/lib/client'
import { useAsync } from '@/useAsync'
import { Card, CardContent } from '@/components/ui/card'
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
import type { BadgeProps } from '@/components/ui/badge'
import { AddonHeader, DataState } from '@/features/addons/AddonShell'
import type { Agent, AgentAuditEntry, ToolAuthorization } from '@/types/addons'

function riskVariant(score: number): BadgeProps['variant'] {
  if (score >= 70) return 'danger'
  if (score >= 40) return 'warning'
  return 'success'
}

export default function Agents() {
  const agents = useAsync<Agent[]>(() => client.agents.list(), [])
  const tools = useAsync<ToolAuthorization[]>(() => client.agents.tools(), [])
  const audit = useAsync<AgentAuditEntry[]>(() => client.agents.audit(), [])

  return (
    <div className="space-y-5">
      <AddonHeader
        title="Agent Security"
        blurb="Treat AI agents as identities: inventory them, scope their tools, and audit their actions."
      />

      <Tabs defaultValue="inventory">
        <TabsList>
          <TabsTrigger value="inventory">Agent inventory</TabsTrigger>
          <TabsTrigger value="tools">Tool authorization</TabsTrigger>
          <TabsTrigger value="audit">Audit log</TabsTrigger>
        </TabsList>

        <TabsContent value="inventory">
          <Card>
            <CardContent className="p-0">
              <DataState state={agents} isEmpty={(d) => d.length === 0} empty="No agents discovered.">
                {(rows) => (
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Agent</TableHead>
                        <TableHead>Creator</TableHead>
                        <TableHead>Status</TableHead>
                        <TableHead>Actions (24h)</TableHead>
                        <TableHead>Risk</TableHead>
                        <TableHead>Allowed tools</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {rows.map((a) => (
                        <TableRow key={a.id}>
                          <TableCell className="font-medium">{a.name}</TableCell>
                          <TableCell>{a.creator}</TableCell>
                          <TableCell>
                            <Badge
                              variant={
                                a.status === 'active'
                                  ? 'success'
                                  : a.status === 'revoked'
                                    ? 'danger'
                                    : 'outline'
                              }
                            >
                              {a.status}
                            </Badge>
                          </TableCell>
                          <TableCell className="font-mono text-xs">{a.actions_24h}</TableCell>
                          <TableCell>
                            <Badge variant={riskVariant(a.risk_score)}>{a.risk_score}</Badge>
                          </TableCell>
                          <TableCell className="text-xs text-muted-foreground">
                            {a.allowed_tools.join(', ')}
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

        <TabsContent value="tools">
          <Card>
            <CardContent className="p-0">
              <DataState state={tools} isEmpty={(d) => d.length === 0} empty="No tools registered.">
                {(rows) => (
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Tool</TableHead>
                        <TableHead>Scope</TableHead>
                        <TableHead>Agents</TableHead>
                        <TableHead>Authorization</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {rows.map((t) => (
                        <TableRow key={t.id}>
                          <TableCell className="font-medium">{t.tool}</TableCell>
                          <TableCell className="font-mono text-xs">{t.scope}</TableCell>
                          <TableCell className="font-mono text-xs">{t.agents}</TableCell>
                          <TableCell>
                            <Badge variant={t.allowed ? 'success' : 'danger'}>
                              {t.allowed ? 'allowed' : 'blocked'}
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
                        <TableHead>Agent</TableHead>
                        <TableHead>Tool</TableHead>
                        <TableHead>Action</TableHead>
                        <TableHead>Outcome</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {rows.map((e) => (
                        <TableRow key={e.id}>
                          <TableCell className="font-mono text-xs text-muted-foreground">
                            {new Date(e.ts).toLocaleString()}
                          </TableCell>
                          <TableCell>{e.agent}</TableCell>
                          <TableCell className="font-mono text-xs">{e.tool}</TableCell>
                          <TableCell>{e.action}</TableCell>
                          <TableCell>
                            <Badge variant={e.outcome === 'allowed' ? 'success' : 'danger'}>
                              {e.outcome}
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
      </Tabs>
    </div>
  )
}
