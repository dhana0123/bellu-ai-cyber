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
import { AreaChartCard } from '@/components/charts'
import type { BadgeProps } from '@/components/ui/badge'
import { AddonHeader, DataState } from '@/features/addons/AddonShell'
import type {
  ApiKey,
  GatewayLogEntry,
  GatewayStats,
  GatewayTimePoint,
  RoutingPolicy,
} from '@/types/addons'

const PROVIDER_SERIES = [
  { key: 'openai', label: 'OpenAI' },
  { key: 'anthropic', label: 'Anthropic' },
  { key: 'local', label: 'Local' },
]

function Kpi({ label, value }: { label: string; value: string | number }) {
  return (
    <Card>
      <CardContent className="p-5">
        <div className="text-2xl font-bold text-primary">{value}</div>
        <div className="mt-1 text-xs text-muted-foreground">{label}</div>
      </CardContent>
    </Card>
  )
}

function logVariant(status: GatewayLogEntry['status']): BadgeProps['variant'] {
  return status === 'success' ? 'success' : status === 'blocked' ? 'warning' : 'danger'
}

export default function Gateway() {
  const stats = useAsync<GatewayStats>(() => client.gateway.stats(), [])
  const series = useAsync<GatewayTimePoint[]>(() => client.gateway.series(), [])
  const policies = useAsync<RoutingPolicy[]>(() => client.gateway.policies(), [])
  const keys = useAsync<ApiKey[]>(() => client.gateway.keys(), [])
  const logs = useAsync<GatewayLogEntry[]>(() => client.gateway.logs(), [])

  return (
    <div className="space-y-5">
      <AddonHeader
        title="AI Gateway"
        blurb="A control plane for AI traffic: routing, budgets, failover and audit across providers."
      />

      <Tabs defaultValue="dashboard">
        <TabsList>
          <TabsTrigger value="dashboard">Dashboard</TabsTrigger>
          <TabsTrigger value="policies">Routing policies</TabsTrigger>
          <TabsTrigger value="keys">Budgets &amp; keys</TabsTrigger>
          <TabsTrigger value="logs">Usage logs</TabsTrigger>
        </TabsList>

        <TabsContent value="dashboard" className="space-y-4">
          <DataState state={stats}>
            {(s) => (
              <div className="grid grid-cols-2 gap-3 md:grid-cols-3 lg:grid-cols-6">
                <Kpi label="Calls (24h)" value={s.total_calls_24h.toLocaleString()} />
                <Kpi label="Avg latency" value={`${s.avg_latency_ms}ms`} />
                <Kpi label="Error rate" value={`${s.error_rate_pct}%`} />
                <Kpi label="Instances" value={s.active_instances} />
                <Kpi label="Failovers (24h)" value={s.failover_events_24h} />
                <Kpi label="Cache hit" value={`${s.cache_hit_rate_pct}%`} />
              </div>
            )}
          </DataState>
          <DataState state={series}>
            {(data) => (
              <AreaChartCard
                title="API calls by provider"
                description="DEMO — gateway throughput over the last 24h"
                data={data}
                series={PROVIDER_SERIES}
              />
            )}
          </DataState>
        </TabsContent>

        <TabsContent value="policies">
          <Card>
            <CardContent className="p-0">
              <DataState state={policies} isEmpty={(d) => d.length === 0} empty="No routing policies.">
                {(rows) => (
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Priority</TableHead>
                        <TableHead>Name</TableHead>
                        <TableHead>Source</TableHead>
                        <TableHead>Model match</TableHead>
                        <TableHead>Destination</TableHead>
                        <TableHead>Status</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {rows
                        .slice()
                        .sort((a, b) => a.priority - b.priority)
                        .map((p) => (
                          <TableRow key={p.id}>
                            <TableCell className="font-mono text-xs">{p.priority}</TableCell>
                            <TableCell className="font-medium">{p.name}</TableCell>
                            <TableCell>{p.source}</TableCell>
                            <TableCell className="font-mono text-xs">{p.model_match}</TableCell>
                            <TableCell className="font-mono text-xs">{p.destination}</TableCell>
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

        <TabsContent value="keys">
          <Card>
            <CardContent className="p-0">
              <DataState state={keys} isEmpty={(d) => d.length === 0} empty="No API keys.">
                {(rows) => (
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Key</TableHead>
                        <TableHead>Owner</TableHead>
                        <TableHead>Budget</TableHead>
                        <TableHead>Spent</TableHead>
                        <TableHead>Rate limit</TableHead>
                        <TableHead>Status</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {rows.map((k) => (
                        <TableRow key={k.id}>
                          <TableCell className="font-medium">{k.name}</TableCell>
                          <TableCell>{k.owner}</TableCell>
                          <TableCell>${k.monthly_budget_usd.toLocaleString()}</TableCell>
                          <TableCell>
                            <span
                              className={
                                k.spent_usd / k.monthly_budget_usd > 0.9 ? 'text-amber-400' : ''
                              }
                            >
                              ${k.spent_usd.toLocaleString()}
                            </span>
                          </TableCell>
                          <TableCell className="font-mono text-xs">{k.rate_limit_rpm} rpm</TableCell>
                          <TableCell>
                            <Badge variant={k.active ? 'success' : 'outline'}>
                              {k.active ? 'active' : 'inactive'}
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

        <TabsContent value="logs">
          <Card>
            <CardContent className="p-0">
              <DataState state={logs} isEmpty={(d) => d.length === 0} empty="No usage logged.">
                {(rows) => (
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Time</TableHead>
                        <TableHead>Key</TableHead>
                        <TableHead>Provider</TableHead>
                        <TableHead>Prompt tokens</TableHead>
                        <TableHead>Latency</TableHead>
                        <TableHead>Status</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {rows.map((l) => (
                        <TableRow key={l.id}>
                          <TableCell className="font-mono text-xs text-muted-foreground">
                            {new Date(l.ts).toLocaleTimeString()}
                          </TableCell>
                          <TableCell>{l.api_key}</TableCell>
                          <TableCell>{l.provider}</TableCell>
                          <TableCell className="font-mono text-xs">{l.prompt_tokens}</TableCell>
                          <TableCell className="font-mono text-xs">{l.latency_ms}ms</TableCell>
                          <TableCell>
                            <Badge variant={logVariant(l.status)}>{l.status}</Badge>
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
