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
import { ArrowRight } from 'lucide-react'
import type { BadgeProps } from '@/components/ui/badge'
import { AddonHeader, DataState } from '@/features/addons/AddonShell'
import type { DataAsset, DlpPolicy, LineageFlow } from '@/types/addons'
import type { Severity } from '@/types/foundation'

function sevVariant(s: Severity): BadgeProps['variant'] {
  if (s === 'critical' || s === 'high') return 'danger'
  if (s === 'medium') return 'warning'
  return 'secondary'
}

export default function DataSecurity() {
  const assets = useAsync<DataAsset[]>(() => client.data.assets(), [])
  const lineage = useAsync<LineageFlow[]>(() => client.data.lineage(), [])
  const dlp = useAsync<DlpPolicy[]>(() => client.data.dlp(), [])

  return (
    <div className="space-y-5">
      <AddonHeader
        title="AI Data Security"
        blurb="Track sensitive data as it flows through AI systems, and enforce DLP on prompts and responses."
      />

      <Tabs defaultValue="inventory">
        <TabsList>
          <TabsTrigger value="inventory">Data inventory</TabsTrigger>
          <TabsTrigger value="lineage">Lineage flow</TabsTrigger>
          <TabsTrigger value="dlp">DLP policies</TabsTrigger>
        </TabsList>

        <TabsContent value="inventory">
          <Card>
            <CardContent className="p-0">
              <DataState state={assets} isEmpty={(d) => d.length === 0} empty="No data assets.">
                {(rows) => (
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Asset</TableHead>
                        <TableHead>Type</TableHead>
                        <TableHead>Location</TableHead>
                        <TableHead>Owner</TableHead>
                        <TableHead>Sensitivity</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {rows.map((a) => (
                        <TableRow key={a.id}>
                          <TableCell className="font-medium">{a.name}</TableCell>
                          <TableCell>
                            <Badge variant="secondary">{a.data_type}</Badge>
                          </TableCell>
                          <TableCell className="font-mono text-xs text-muted-foreground">
                            {a.location}
                          </TableCell>
                          <TableCell>{a.owning_team}</TableCell>
                          <TableCell>
                            <Badge variant={sevVariant(a.sensitivity)}>{a.sensitivity}</Badge>
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

        <TabsContent value="lineage">
          <Card>
            <CardHeader>
              <CardTitle className="text-base">Data → AI flows</CardTitle>
            </CardHeader>
            <CardContent>
              <DataState state={lineage} isEmpty={(d) => d.length === 0} empty="No flows observed.">
                {(rows) => (
                  <div className="space-y-2">
                    {rows.map((f) => (
                      <div
                        key={f.id}
                        className={
                          'flex items-center gap-2 rounded-md border p-3 text-sm ' +
                          (f.policy_violation
                            ? 'border-destructive/40 bg-destructive/5'
                            : 'bg-muted/30')
                        }
                      >
                        <span className="font-mono text-xs">{f.source}</span>
                        <ArrowRight className="h-3.5 w-3.5 text-muted-foreground" />
                        <Badge variant="ai">{f.via}</Badge>
                        <ArrowRight className="h-3.5 w-3.5 text-muted-foreground" />
                        <span className="font-mono text-xs">{f.destination}</span>
                        {f.policy_violation && (
                          <Badge variant="danger" className="ml-auto">
                            policy violation
                          </Badge>
                        )}
                      </div>
                    ))}
                  </div>
                )}
              </DataState>
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="dlp">
          <Card>
            <CardContent className="p-0">
              <DataState state={dlp} isEmpty={(d) => d.length === 0} empty="No DLP policies.">
                {(rows) => (
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Policy</TableHead>
                        <TableHead>Pattern</TableHead>
                        <TableHead>Category</TableHead>
                        <TableHead>Action</TableHead>
                        <TableHead>Matches (24h)</TableHead>
                        <TableHead>Status</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {rows.map((p) => (
                        <TableRow key={p.id}>
                          <TableCell className="font-medium">{p.name}</TableCell>
                          <TableCell className="font-mono text-xs text-muted-foreground">
                            {p.pattern}
                          </TableCell>
                          <TableCell>{p.category}</TableCell>
                          <TableCell>
                            <Badge
                              variant={
                                p.action === 'block'
                                  ? 'danger'
                                  : p.action === 'warn'
                                    ? 'warning'
                                    : 'secondary'
                              }
                            >
                              {p.action}
                            </Badge>
                          </TableCell>
                          <TableCell className="font-mono text-xs">{p.matches_24h}</TableCell>
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
      </Tabs>
    </div>
  )
}
