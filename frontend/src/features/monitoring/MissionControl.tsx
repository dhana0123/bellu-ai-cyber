import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import { RefreshCw, Radar, ArrowRight } from 'lucide-react'

import { client } from '@/lib/client'
import { useAsync } from '@/useAsync'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Spinner } from '@/components/ui/spinner'
import { Badge, type BadgeProps } from '@/components/ui/badge'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetDescription,
} from '@/components/ui/sheet'
import type {
  MonitorEvent,
  RunDetail,
  RunTimelineEvent,
} from '@/types/monitoring'

// State/status → badge variant. Covers both the monitor-event lifecycle states
// and the run statuses so the same mapping drives the grid and the detail panel.
function stateVariant(state: string): BadgeProps['variant'] {
  switch (state) {
    case 'completed':
      return 'success'
    case 'failed':
      return 'danger'
    case 'running':
    case 'claimed':
      return 'warning'
    case 'routed':
      return 'ai'
    case 'skipped':
      return 'outline'
    default: // pending / queued / unknown
      return 'secondary'
  }
}

// Human-friendly relative time for the created_at / timeline timestamps.
function relativeTime(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime()
  const s = Math.floor(diff / 1000)
  if (s < 60) return `${s}s ago`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ago`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ago`
  const d = Math.floor(h / 24)
  return `${d}d ago`
}

// Format a millisecond duration compactly (e.g. 1.2s, 350ms, 2m 5s).
function formatDuration(ms: number | null): string {
  if (ms == null) return '—'
  if (ms < 1000) return `${ms}ms`
  const s = ms / 1000
  if (s < 60) return `${s.toFixed(1)}s`
  const m = Math.floor(s / 60)
  const rem = Math.round(s % 60)
  return `${m}m ${rem}s`
}

// The scan target of a run, rendered as a short label.
function targetLabel(t: RunDetail['target']): string {
  if (t.kind === 'entity' && t.canonical_id) return t.canonical_id
  if (t.raw) return t.raw
  return t.kind || 'unknown'
}

export default function MissionControl(): JSX.Element {
  const [limit] = useState(100)
  const [query, setQuery] = useState('')
  const [selectedRunId, setSelectedRunId] = useState<string | null>(null)
  // Bump to force a refetch of the events list (manual refresh).
  const [nonce, setNonce] = useState(0)

  const { loading, error, data } = useAsync<{ events: MonitorEvent[] }>(
    () => client.monitorEvents(limit),
    [limit, nonce],
  )

  const events = useMemo(() => {
    const all = data?.events ?? []
    const q = query.trim().toLowerCase()
    if (!q) return all
    return all.filter(
      (e) =>
        e.source.toLowerCase().includes(q) ||
        e.event_kind.toLowerCase().includes(q) ||
        (e.team ?? '').toLowerCase().includes(q) ||
        e.state.toLowerCase().includes(q) ||
        (e.run_id ?? '').toLowerCase().includes(q),
    )
  }, [data, query])

  return (
    <div className="space-y-4">
      {/* Header */}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <Radar className="h-5 w-5 text-muted-foreground" />
          <h1 className="text-2xl font-semibold tracking-tight">Mission Control</h1>
        </div>
        <Button variant="outline" size="sm" onClick={() => setNonce((n) => n + 1)}>
          <RefreshCw className="h-3.5 w-3.5" /> Refresh
        </Button>
      </div>
      <p className="text-sm text-muted-foreground">
        Continuous-monitoring events and the agent runs they trigger. Click an event with a run to
        inspect its stage-flow and timeline.
      </p>

      {/* Filter bar */}
      <div className="flex flex-wrap items-center gap-2">
        <Input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Filter by source, kind, team, state, or run"
          className="h-8 w-72 text-xs"
        />
        <span className="ml-auto text-xs text-muted-foreground">{events.length} events</span>
      </div>

      {/* Events grid */}
      <div className="rounded-lg border bg-card">
        {loading ? (
          <div className="flex h-40 items-center justify-center">
            <Spinner className="h-6 w-6" />
          </div>
        ) : error ? (
          <p className="p-4 text-sm text-destructive">Failed to load monitor events: {error}</p>
        ) : (
          <div className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Source</TableHead>
                  <TableHead>Event kind</TableHead>
                  <TableHead>Team</TableHead>
                  <TableHead>State</TableHead>
                  <TableHead>Run</TableHead>
                  <TableHead>Created</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {events.map((e) => {
                  const hasRun = Boolean(e.run_id)
                  return (
                    <TableRow
                      key={e.id}
                      className={cn(hasRun && 'cursor-pointer hover:bg-accent/40')}
                      onClick={() => hasRun && setSelectedRunId(e.run_id ?? null)}
                    >
                      <TableCell className="font-medium">{e.source}</TableCell>
                      <TableCell className="text-sm text-muted-foreground">{e.event_kind}</TableCell>
                      <TableCell className="text-sm">
                        {e.team ? (
                          <Badge variant="outline">{e.team}</Badge>
                        ) : (
                          <span className="text-muted-foreground">—</span>
                        )}
                      </TableCell>
                      <TableCell>
                        <Badge variant={stateVariant(e.state)}>{e.state}</Badge>
                      </TableCell>
                      <TableCell className="font-mono text-xs">
                        {hasRun ? (
                          <span className="inline-flex items-center gap-1 text-foreground">
                            {e.run_id}
                            <ArrowRight className="h-3 w-3 text-muted-foreground" />
                          </span>
                        ) : (
                          <span className="text-muted-foreground">—</span>
                        )}
                      </TableCell>
                      <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
                        {relativeTime(e.created_at)}
                      </TableCell>
                    </TableRow>
                  )
                })}
                {events.length === 0 && (
                  <TableRow>
                    <TableCell colSpan={6} className="py-10 text-center text-muted-foreground">
                      No monitor events match the current filter.
                    </TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
          </div>
        )}
      </div>

      {/* Run drill-in */}
      <RunDetailSheet
        runId={selectedRunId}
        onClose={() => setSelectedRunId(null)}
      />
    </div>
  )
}

// RunDetailSheet is the slide-over that loads and renders a single run's detail
// header, per-stage stage-flow, and timeline. It fetches both the detail and
// the timeline when a runId is set, and clears on close.
function RunDetailSheet({
  runId,
  onClose,
}: {
  runId: string | null
  onClose: () => void
}): JSX.Element {
  const [detail, setDetail] = useState<RunDetail | null>(null)
  const [timeline, setTimeline] = useState<RunTimelineEvent[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async (id: string) => {
    setLoading(true)
    setError(null)
    try {
      const [d, t] = await Promise.all([
        client.agentRunDetail(id),
        client.agentRunTimeline(id).catch(() => ({ events: [] as RunTimelineEvent[] })),
      ])
      setDetail(d)
      setTimeline(t.events ?? [])
    } catch (e) {
      setError(e instanceof Error ? e.message : 'failed to load run')
      setDetail(null)
      setTimeline([])
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    if (runId) void load(runId)
    else {
      setDetail(null)
      setTimeline([])
      setError(null)
    }
  }, [runId, load])

  return (
    <Sheet open={runId !== null} onOpenChange={(o) => !o && onClose()}>
      <SheetContent className="w-full overflow-y-auto sm:max-w-xl">
        <SheetHeader>
          <SheetTitle className="font-mono text-base">{runId ?? 'Run'}</SheetTitle>
          <SheetDescription>Agent run detail, stage-flow, and timeline.</SheetDescription>
        </SheetHeader>

        {loading ? (
          <div className="flex h-40 items-center justify-center">
            <Spinner className="h-6 w-6" />
          </div>
        ) : error ? (
          <p className="mt-4 text-sm text-destructive">{error}</p>
        ) : detail ? (
          <div className="mt-4 space-y-6">
            {/* Header facts */}
            <dl className="grid grid-cols-2 gap-x-4 gap-y-3 text-sm">
              <Fact label="Status">
                <Badge variant={stateVariant(detail.status)}>{detail.status}</Badge>
              </Fact>
              <Fact label="Triggered by">
                {detail.triggered_by === 'manual'
                  ? 'Manual'
                  : `Monitor event #${detail.triggered_by}`}
              </Fact>
              <Fact label="Target">
                <span className="break-all font-mono text-xs">{targetLabel(detail.target)}</span>
              </Fact>
              <Fact label="Scope rules">{detail.scope_rules}</Fact>
              <Fact label="Agents">{detail.agents_count}</Fact>
              <Fact label="Handoffs">{detail.handoffs_count}</Fact>
              <Fact label="Duration">{formatDuration(detail.duration_ms)}</Fact>
              <Fact label="Started">
                {detail.started_at ? relativeTime(detail.started_at) : '—'}
              </Fact>
            </dl>

            {/* Stage-flow */}
            <section className="space-y-2">
              <h3 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                Stage flow
              </h3>
              <ol className="space-y-1.5">
                {detail.stage_flow.map((s, i) => (
                  <li
                    key={`${s.stage}-${i}`}
                    className={cn(
                      'rounded-md border px-3 py-2',
                      s.leg_count === 0 ? 'opacity-50' : 'bg-accent/30',
                    )}
                  >
                    <div className="flex items-center justify-between">
                      <span className="text-sm font-medium">{s.stage}</span>
                      <Badge variant={s.leg_count === 0 ? 'outline' : 'default'}>
                        {s.leg_count} {s.leg_count === 1 ? 'leg' : 'legs'}
                      </Badge>
                    </div>
                    {s.legs.length > 0 && (
                      <ul className="mt-1.5 space-y-1">
                        {s.legs.map((leg, j) => (
                          <li
                            key={`${leg.agent_name}-${j}`}
                            className="flex items-center justify-between text-xs text-muted-foreground"
                          >
                            <span className="truncate">{leg.agent_name}</span>
                            <span className="ml-2 flex shrink-0 items-center gap-2">
                              <span className="tabular-nums">{formatDuration(leg.duration_ms)}</span>
                              {leg.outcome && (
                                <Badge variant={stateVariant(leg.outcome)}>{leg.outcome}</Badge>
                              )}
                            </span>
                          </li>
                        ))}
                      </ul>
                    )}
                  </li>
                ))}
              </ol>
            </section>

            {/* Timeline */}
            <section className="space-y-2">
              <h3 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                Timeline
              </h3>
              {timeline.length === 0 ? (
                <p className="text-sm text-muted-foreground">No timeline events recorded.</p>
              ) : (
                <ol className="space-y-2 border-l border-border pl-4">
                  {timeline.map((ev, i) => (
                    <li key={i} className="relative">
                      <span className="absolute -left-[21px] top-1 h-2 w-2 rounded-full bg-muted-foreground" />
                      <div className="flex items-baseline justify-between gap-2">
                        <span className="text-sm">
                          {ev.kind === 'handoff' ? (
                            <span className="inline-flex items-center gap-1">
                              <span className="font-mono text-xs">{ev.from_agent_id}</span>
                              <ArrowRight className="h-3 w-3 text-muted-foreground" />
                              <span className="font-mono text-xs">{ev.to_agent_id}</span>
                            </span>
                          ) : (
                            <span>
                              <span className="text-muted-foreground">started </span>
                              <span className="font-medium">{ev.agent_name || ev.agent_id}</span>
                            </span>
                          )}
                        </span>
                        <span className="shrink-0 text-xs text-muted-foreground">
                          {relativeTime(ev.at)}
                        </span>
                      </div>
                    </li>
                  ))}
                </ol>
              )}
            </section>
          </div>
        ) : null}
      </SheetContent>
    </Sheet>
  )
}

// Fact is one labeled value in the run-detail header grid.
function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div>
      <dt className="text-xs uppercase tracking-wide text-muted-foreground">{label}</dt>
      <dd className="mt-0.5 flex items-center gap-1 font-medium">{children}</dd>
    </div>
  )
}
