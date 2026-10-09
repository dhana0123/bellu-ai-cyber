import { client } from '@/lib/client'
import { useAsync } from '@/useAsync'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
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
import type { ChatMonitorMessage, GuardrailRule, RuntimeAlert } from '@/types/addons'
import type { Severity } from '@/types/foundation'

function sevVariant(s: Severity): BadgeProps['variant'] {
  if (s === 'critical' || s === 'high') return 'danger'
  if (s === 'medium') return 'warning'
  return 'secondary'
}

export default function Runtime() {
  const alerts = useAsync<RuntimeAlert[]>(() => client.runtime.alerts(), [])
  const guardrails = useAsync<GuardrailRule[]>(() => client.runtime.guardrails(), [])
  const chat = useAsync<ChatMonitorMessage[]>(() => client.runtime.chat(), [])

  return (
    <div className="space-y-5">
      <AddonHeader
        title="Runtime Defense"
        blurb="Live protection for AI interactions: prompt-injection, data-exfil and jailbreak detection."
      />

      <Tabs defaultValue="alerts">
        <TabsList>
          <TabsTrigger value="alerts">Runtime alerts</TabsTrigger>
          <TabsTrigger value="guardrails">Guardrail rules</TabsTrigger>
          <TabsTrigger value="monitor">Live chat monitor</TabsTrigger>
        </TabsList>

        <TabsContent value="alerts" className="space-y-3">
          <Card>
            <CardContent className="p-0">
              <DataState state={alerts} isEmpty={(d) => d.length === 0} empty="No runtime alerts.">
                {(rows) => (
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Severity</TableHead>
                        <TableHead>Agent</TableHead>
                        <TableHead>Event</TableHead>
                        <TableHead>Summary</TableHead>
                        <TableHead>Status</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {rows.map((a) => (
                        <TableRow key={a.id}>
                          <TableCell>
                            <Badge variant={sevVariant(a.severity)}>{a.severity}</Badge>
                          </TableCell>
                          <TableCell className="font-medium">{a.agent}</TableCell>
                          <TableCell className="font-mono text-xs">{a.event_type}</TableCell>
                          <TableCell className="max-w-md">
                            <div>{a.summary}</div>
                            <div className="mt-1 truncate font-mono text-xs text-muted-foreground">
                              {a.prompt_excerpt}
                            </div>
                          </TableCell>
                          <TableCell>
                            <Badge variant={a.status === 'resolved' ? 'success' : 'outline'}>
                              {a.status}
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

        <TabsContent value="guardrails">
          <Card>
            <CardContent className="p-0">
              <DataState
                state={guardrails}
                isEmpty={(d) => d.length === 0}
                empty="No guardrail rules."
              >
                {(rows) => (
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Rule</TableHead>
                        <TableHead>Condition</TableHead>
                        <TableHead>Action</TableHead>
                        <TableHead>Hits (24h)</TableHead>
                        <TableHead>Status</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {rows.map((r) => (
                        <TableRow key={r.id}>
                          <TableCell className="font-medium">{r.name}</TableCell>
                          <TableCell className="font-mono text-xs text-muted-foreground">
                            {r.condition}
                          </TableCell>
                          <TableCell>
                            <Badge
                              variant={
                                r.action === 'deny'
                                  ? 'danger'
                                  : r.action === 'notify'
                                    ? 'warning'
                                    : 'secondary'
                              }
                            >
                              {r.action}
                            </Badge>
                          </TableCell>
                          <TableCell className="font-mono text-xs">{r.hits_24h}</TableCell>
                          <TableCell>
                            <Badge variant={r.enabled ? 'success' : 'outline'}>
                              {r.enabled ? 'enabled' : 'disabled'}
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

        <TabsContent value="monitor">
          <Card>
            <CardHeader>
              <CardTitle className="text-base">Live session feed</CardTitle>
            </CardHeader>
            <CardContent>
              <DataState state={chat} isEmpty={(d) => d.length === 0} empty="No active sessions.">
                {(rows) => (
                  <div className="space-y-2">
                    {rows.map((m) => (
                      <div
                        key={m.id}
                        className={
                          'rounded-md border p-3 text-sm ' +
                          (m.flagged ? 'border-destructive/40 bg-destructive/5' : 'bg-muted/30')
                        }
                      >
                        <div className="mb-1 flex items-center gap-2">
                          <Badge variant={m.role === 'user' ? 'secondary' : 'ai'}>{m.role}</Badge>
                          <span className="font-mono text-xs text-muted-foreground">
                            {m.session_id}
                          </span>
                          {m.flagged && <Badge variant="danger">flagged</Badge>}
                        </div>
                        {m.text}
                      </div>
                    ))}
                  </div>
                )}
              </DataState>
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
    </div>
  )
}
