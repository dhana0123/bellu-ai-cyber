import { client } from '@/lib/client'
import { useAsync } from '@/useAsync'
import { Card, CardContent } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Check, X } from 'lucide-react'
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
import type { ModelRegistryEntry, ProvenanceRecord, ScanResult } from '@/types/addons'
import type { Severity } from '@/types/foundation'

function sevVariant(s: Severity): BadgeProps['variant'] {
  if (s === 'critical' || s === 'high') return 'danger'
  if (s === 'medium') return 'warning'
  return 'secondary'
}

export default function SupplyChain() {
  const models = useAsync<ModelRegistryEntry[]>(() => client.supplyChain.models(), [])
  const scans = useAsync<ScanResult[]>(() => client.supplyChain.scans(), [])
  const provenance = useAsync<ProvenanceRecord[]>(() => client.supplyChain.provenance(), [])

  return (
    <div className="space-y-5">
      <AddonHeader
        title="AI Supply Chain"
        blurb="Inventory models, scan their dependencies for CVEs, and verify provenance and signatures."
      />

      <Tabs defaultValue="registry">
        <TabsList>
          <TabsTrigger value="registry">Model registry</TabsTrigger>
          <TabsTrigger value="scans">Scan results</TabsTrigger>
          <TabsTrigger value="provenance">Provenance</TabsTrigger>
        </TabsList>

        <TabsContent value="registry">
          <Card>
            <CardContent className="p-0">
              <DataState
                state={models}
                isEmpty={(d) => d.length === 0}
                empty="No models registered."
              >
                {(rows) => (
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Model</TableHead>
                        <TableHead>Framework</TableHead>
                        <TableHead>Source</TableHead>
                        <TableHead>Hash</TableHead>
                        <TableHead>Criticality</TableHead>
                        <TableHead>Signed</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {rows.map((m) => (
                        <TableRow key={m.id}>
                          <TableCell className="font-medium">{m.name}</TableCell>
                          <TableCell>{m.framework}</TableCell>
                          <TableCell>
                            <Badge variant={m.source === 'internal' ? 'secondary' : 'outline'}>
                              {m.source}
                            </Badge>
                          </TableCell>
                          <TableCell className="font-mono text-xs text-muted-foreground">
                            {m.hash}
                          </TableCell>
                          <TableCell>
                            <Badge variant={sevVariant(m.criticality)}>{m.criticality}</Badge>
                          </TableCell>
                          <TableCell>
                            {m.signed ? (
                              <Check className="h-4 w-4 text-emerald-500" />
                            ) : (
                              <X className="h-4 w-4 text-destructive" />
                            )}
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

        <TabsContent value="scans">
          <Card>
            <CardContent className="p-0">
              <DataState state={scans} isEmpty={(d) => d.length === 0} empty="No scan findings.">
                {(rows) => (
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Severity</TableHead>
                        <TableHead>Artifact</TableHead>
                        <TableHead>Component</TableHead>
                        <TableHead>CVE</TableHead>
                        <TableHead>Fixed in</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {rows.map((s) => (
                        <TableRow key={s.id}>
                          <TableCell>
                            <Badge variant={sevVariant(s.severity)}>{s.severity}</Badge>
                          </TableCell>
                          <TableCell className="font-medium">{s.artifact}</TableCell>
                          <TableCell className="font-mono text-xs">{s.component}</TableCell>
                          <TableCell className="font-mono text-xs">{s.cve}</TableCell>
                          <TableCell className="font-mono text-xs text-muted-foreground">
                            {s.fixed_version ?? '—'}
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

        <TabsContent value="provenance">
          <Card>
            <CardContent className="p-0">
              <DataState
                state={provenance}
                isEmpty={(d) => d.length === 0}
                empty="No provenance records."
              >
                {(rows) => (
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Model</TableHead>
                        <TableHead>Built by</TableHead>
                        <TableHead>Built at</TableHead>
                        <TableHead>Dataset</TableHead>
                        <TableHead>Signature</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {rows.map((p) => (
                        <TableRow key={p.id}>
                          <TableCell className="font-medium">{p.model}</TableCell>
                          <TableCell>{p.built_by}</TableCell>
                          <TableCell className="font-mono text-xs text-muted-foreground">
                            {new Date(p.built_at).toLocaleDateString()}
                          </TableCell>
                          <TableCell className="font-mono text-xs">{p.dataset_ref}</TableCell>
                          <TableCell>
                            <Badge variant={p.signature_verified ? 'success' : 'danger'}>
                              {p.signature_verified ? 'verified' : 'unverified'}
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
