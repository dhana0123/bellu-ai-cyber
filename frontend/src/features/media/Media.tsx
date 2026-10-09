import { useState } from 'react'
import { client } from '@/lib/client'
import { useAsync } from '@/useAsync'
import { Card, CardContent } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { FileVideo, FileAudio, FileImage } from 'lucide-react'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { RadialChartCard } from '@/components/charts'
import type { BadgeProps } from '@/components/ui/badge'
import { AddonHeader, DataState } from '@/features/addons/AddonShell'
import type { MediaIntegration, MediaItem } from '@/types/addons'

function statusVariant(s: MediaItem['status']): BadgeProps['variant'] {
  return s === 'flagged' ? 'danger' : s === 'approved' ? 'success' : 'outline'
}

const KIND_ICON = { video: FileVideo, audio: FileAudio, image: FileImage } as const

export default function Media() {
  const queue = useAsync<MediaItem[]>(() => client.media.queue(), [])
  const integrations = useAsync<MediaIntegration[]>(() => client.media.integrations(), [])
  const [review, setReview] = useState<MediaItem | null>(null)

  return (
    <div className="space-y-5">
      <AddonHeader
        title="Media Authenticity"
        blurb="Inspect inbound audio, video and images for synthetic-media artifacts before they reach people."
      />

      <Tabs defaultValue="queue">
        <TabsList>
          <TabsTrigger value="queue">Media queue</TabsTrigger>
          <TabsTrigger value="integrations">Integrations</TabsTrigger>
        </TabsList>

        <TabsContent value="queue">
          <Card>
            <CardContent className="p-0">
              <DataState state={queue} isEmpty={(d) => d.length === 0} empty="Queue is empty.">
                {(rows) => (
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Type</TableHead>
                        <TableHead>Source</TableHead>
                        <TableHead>Submitted by</TableHead>
                        <TableHead>Authenticity</TableHead>
                        <TableHead>Confidence</TableHead>
                        <TableHead>Status</TableHead>
                        <TableHead />
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {rows.map((m) => {
                        const Icon = KIND_ICON[m.kind]
                        return (
                          <TableRow key={m.id}>
                            <TableCell>
                              <span className="inline-flex items-center gap-1.5">
                                <Icon className="h-4 w-4 text-muted-foreground" />
                                {m.kind}
                              </span>
                            </TableCell>
                            <TableCell>{m.source}</TableCell>
                            <TableCell className="text-muted-foreground">{m.submitted_by}</TableCell>
                            <TableCell>
                              <Badge variant={m.authenticity_score < 50 ? 'danger' : 'success'}>
                                {m.authenticity_score}/100
                              </Badge>
                            </TableCell>
                            <TableCell className="font-mono text-xs">{m.confidence_pct}%</TableCell>
                            <TableCell>
                              <Badge variant={statusVariant(m.status)}>{m.status}</Badge>
                            </TableCell>
                            <TableCell>
                              <Button variant="outline" size="sm" onClick={() => setReview(m)}>
                                Review
                              </Button>
                            </TableCell>
                          </TableRow>
                        )
                      })}
                    </TableBody>
                  </Table>
                )}
              </DataState>
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="integrations">
          <Card>
            <CardContent className="p-0">
              <DataState
                state={integrations}
                isEmpty={(d) => d.length === 0}
                empty="No integrations configured."
              >
                {(rows) => (
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Integration</TableHead>
                        <TableHead>Status</TableHead>
                        <TableHead>Scanned (24h)</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {rows.map((i) => (
                        <TableRow key={i.id}>
                          <TableCell className="font-medium">{i.name}</TableCell>
                          <TableCell>
                            <Badge
                              variant={
                                i.status === 'connected'
                                  ? 'success'
                                  : i.status === 'error'
                                    ? 'danger'
                                    : 'outline'
                              }
                            >
                              {i.status}
                            </Badge>
                          </TableCell>
                          <TableCell className="font-mono text-xs">{i.scanned_24h}</TableCell>
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

      <Dialog open={review != null} onOpenChange={(o) => !o && setReview(null)}>
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>Media authenticity review</DialogTitle>
            <DialogDescription>
              {review?.kind} from {review?.source} — demo analysis.
            </DialogDescription>
          </DialogHeader>
          {review && (
            <div className="grid gap-4 md:grid-cols-2">
              {/* Player placeholder */}
              <div className="flex aspect-video items-center justify-center rounded-md border bg-muted/40 text-sm text-muted-foreground">
                {review.kind === 'audio' ? 'Audio waveform (demo)' : 'Media preview (demo)'}
              </div>
              <RadialChartCard
                title="Authenticity"
                description={`Confidence ${review.confidence_pct}%`}
                value={review.authenticity_score}
                label="Authentic"
                color={review.authenticity_score < 50 ? 'hsl(var(--chart-5))' : 'hsl(var(--chart-1))'}
              />
              <div className="md:col-span-2">
                <h3 className="mb-2 text-sm font-medium">Suspicious segments</h3>
                {review.segments.length === 0 ? (
                  <p className="text-sm text-muted-foreground">No suspicious segments detected.</p>
                ) : (
                  <ul className="space-y-1.5">
                    {review.segments.map((s, i) => (
                      <li
                        key={i}
                        className="flex items-center gap-2 rounded-md border bg-muted/30 p-2 text-sm"
                      >
                        <Badge variant={s.suspicious ? 'danger' : 'outline'} className="font-mono">
                          {s.start_s}s–{s.end_s}s
                        </Badge>
                        {s.label}
                      </li>
                    ))}
                  </ul>
                )}
              </div>
            </div>
          )}
        </DialogContent>
      </Dialog>
    </div>
  )
}
