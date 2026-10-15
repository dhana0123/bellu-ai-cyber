import { useEffect, useRef, useState } from 'react'
import { CheckCircle2, XCircle, Loader2, Circle, AlertTriangle } from 'lucide-react'
import { client } from '@/lib/client'
import { cn } from '@/lib/utils'
import type { DiscoveryProgress as Progress, TableState } from '@/types/foundation'

type Phase = 'connecting' | 'running' | 'complete' | 'failed' | 'cancelled'

interface Props {
  connectionId: number | string
  /** Called when the run reaches a terminal state, so the parent can refresh. */
  onDone?: (phase: Exclude<Phase, 'connecting' | 'running'>) => void
}

// POLL_MS is the fallback poll interval used when the SSE stream is unavailable
// or errors out. The status endpoint returns the same progress shape.
const POLL_MS = 2000

/**
 * DiscoveryProgress renders live discovery progress for a connection. It opens
 * an SSE stream (EventSource) and, if that fails, falls back to polling the
 * connector status endpoint. Progress is conveyed with an ARIA progressbar plus
 * text and per-state icons, never color alone.
 */
export function DiscoveryProgress({ connectionId, onDone }: Props) {
  const [progress, setProgress] = useState<Progress | null>(null)
  const [phase, setPhase] = useState<Phase>('connecting')
  const onDoneRef = useRef(onDone)
  onDoneRef.current = onDone

  useEffect(() => {
    let closed = false
    let es: EventSource | null = null
    let pollTimer: ReturnType<typeof setInterval> | null = null

    const finish = (p: Exclude<Phase, 'connecting' | 'running'>) => {
      if (closed) return
      setPhase(p)
      onDoneRef.current?.(p)
      cleanup()
    }

    const cleanup = () => {
      closed = true
      es?.close()
      if (pollTimer) clearInterval(pollTimer)
    }

    // Derive a terminal phase from a session status, or null if still running.
    const terminalFromStatus = (s?: string): Exclude<Phase, 'connecting' | 'running'> | null => {
      if (s === 'complete') return 'complete'
      if (s === 'failed') return 'failed'
      if (s === 'cancelled') return 'cancelled'
      return null
    }

    // Fallback: poll the status endpoint until the session is terminal.
    const startPolling = () => {
      if (closed || pollTimer) return
      const tick = async () => {
        try {
          const { status } = await client.connectorStatus(connectionId)
          const sess = status.latest_session
          if (sess?.progress) setProgress(sess.progress)
          const term = terminalFromStatus(sess?.status)
          if (term) finish(term)
          else setPhase('running')
        } catch {
          /* keep polling; transient errors are expected */
        }
      }
      void tick()
      pollTimer = setInterval(tick, POLL_MS)
    }

    // Primary path: SSE stream.
    try {
      es = new EventSource(client.discoverStreamUrl(connectionId))
      es.addEventListener('progress', (e) => {
        if (closed) return
        try {
          setProgress(JSON.parse((e as MessageEvent).data) as Progress)
          setPhase('running')
        } catch {
          /* ignore malformed frame */
        }
      })
      es.addEventListener('done', (e) => {
        if (closed) return
        try {
          const p = JSON.parse((e as MessageEvent).data) as Progress | null
          if (p) setProgress(p)
        } catch {
          /* ignore */
        }
        // The stream does not carry the terminal status explicitly; confirm it
        // via one status read so we show complete vs failed vs cancelled.
        void client
          .connectorStatus(connectionId)
          .then(({ status }) =>
            finish(terminalFromStatus(status.latest_session?.status) ?? 'complete'),
          )
          .catch(() => finish('complete'))
      })
      es.onerror = () => {
        // Stream dropped or unavailable: fall back to polling. Do not treat as
        // terminal — the run continues server-side.
        es?.close()
        es = null
        startPolling()
      }
    } catch {
      startPolling()
    }

    return cleanup
  }, [connectionId])

  const total = progress?.tables_total ?? 0
  const done = progress?.tables_done ?? 0
  const pct = total > 0 ? Math.round((done / total) * 100) : phase === 'complete' ? 100 : 0
  const resources = progress?.resources_collected ?? 0
  const current = progress?.current_table
  const errorCount = (progress?.tables ?? []).filter((t) => t.state === 'error').length

  return (
    <div className="space-y-4 rounded-lg border bg-card p-5">
      <div className="flex items-start justify-between">
        <div className="flex items-center gap-2">
          <PhaseDot phase={phase} />
          <div>
            <div className="text-sm font-medium">{phaseTitle(phase)}</div>
            <div className="text-xs text-muted-foreground">
              {phase === 'connecting'
                ? 'Opening progress stream…'
                : `Scanning cloud resources via the connector`}
            </div>
          </div>
        </div>
      </div>

      {/* Overall progress bar */}
      <div className="space-y-1">
        <div className="flex items-center justify-between text-xs text-muted-foreground">
          <span>Tables scanned</span>
          <span className="font-medium text-foreground">
            {done} / {total || '—'}
          </span>
        </div>
        <div
          className="h-2 w-full overflow-hidden rounded-full bg-muted"
          role="progressbar"
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={pct}
          aria-label={`Discovery progress: ${done} of ${total} tables scanned`}
        >
          <div
            className={cn(
              'h-full rounded-full transition-all',
              phase === 'failed' ? 'bg-destructive' : 'bg-emerald-500',
            )}
            style={{ width: `${pct}%` }}
          />
        </div>
      </div>

      {/* Stat tiles */}
      <div className="grid grid-cols-3 gap-3">
        <Stat label="Resources" value={resources.toLocaleString()} />
        <Stat label="Tables" value={`${done}/${total || '—'}`} />
        <Stat
          label="Errors"
          value={String(errorCount)}
          tone={errorCount > 0 ? 'warn' : 'default'}
        />
      </div>

      {/* Current table */}
      {phase === 'running' && current && (
        <div className="flex items-center gap-2 rounded-md border bg-muted/40 px-3 py-2 text-sm">
          <Loader2 className="h-4 w-4 animate-spin text-sky-500" />
          <span className="font-mono text-xs">{current}</span>
          <span className="ml-auto text-xs text-muted-foreground">scanning…</span>
        </div>
      )}

      {/* Per-table log */}
      {progress && progress.tables.length > 0 && (
        <ul
          className="max-h-56 space-y-1 overflow-y-auto pr-1"
          aria-label="Per-table discovery status"
        >
          {progress.tables.map((t) => (
            <li key={t.table} className="flex items-center gap-2 text-sm">
              <TableIcon state={t.state} />
              <span className="font-mono text-xs">{t.table}</span>
              <span className="ml-auto text-xs text-muted-foreground">
                {t.state === 'error' ? (
                  <span className="text-destructive">{t.error || 'error'}</span>
                ) : t.state === 'done' ? (
                  t.rows.toLocaleString()
                ) : t.state === 'running' ? (
                  'scanning…'
                ) : (
                  'queued'
                )}
              </span>
            </li>
          ))}
        </ul>
      )}

      {/* Terminal summary */}
      {(phase === 'complete' || phase === 'failed' || phase === 'cancelled') && (
        <div
          className={cn(
            'rounded-md border px-3 py-2 text-sm',
            phase === 'complete'
              ? 'border-emerald-500/30 bg-emerald-500/5 text-emerald-600 dark:text-emerald-400'
              : 'border-destructive/30 bg-destructive/5 text-destructive',
          )}
        >
          {phase === 'complete'
            ? `Discovery complete — collected ${resources.toLocaleString()} resources across ${done} tables${
                errorCount > 0
                  ? ` (${errorCount} table${errorCount > 1 ? 's' : ''} had errors)`
                  : ''
              }.`
            : phase === 'cancelled'
              ? 'Discovery was cancelled.'
              : 'Discovery failed. Check the per-table errors above or retry.'}
        </div>
      )}
    </div>
  )
}

function Stat({
  label,
  value,
  tone = 'default',
}: {
  label: string
  value: string
  tone?: 'default' | 'warn'
}) {
  return (
    <div className="rounded-md border bg-background px-3 py-2">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div
        className={cn('text-lg font-semibold tabular-nums', tone === 'warn' && 'text-amber-500')}
      >
        {value}
      </div>
    </div>
  )
}

function TableIcon({ state }: { state: TableState }) {
  switch (state) {
    case 'done':
      return <CheckCircle2 className="h-4 w-4 shrink-0 text-emerald-500" aria-label="done" />
    case 'error':
      return <XCircle className="h-4 w-4 shrink-0 text-destructive" aria-label="error" />
    case 'running':
      return <Loader2 className="h-4 w-4 shrink-0 animate-spin text-sky-500" aria-label="running" />
    default:
      return <Circle className="h-4 w-4 shrink-0 text-muted-foreground/40" aria-label="pending" />
  }
}

function PhaseDot({ phase }: { phase: Phase }) {
  if (phase === 'failed' || phase === 'cancelled')
    return <AlertTriangle className="h-4 w-4 text-destructive" />
  if (phase === 'complete') return <CheckCircle2 className="h-4 w-4 text-emerald-500" />
  return <Loader2 className="h-4 w-4 animate-spin text-amber-500" />
}

function phaseTitle(phase: Phase): string {
  switch (phase) {
    case 'connecting':
      return 'Starting discovery'
    case 'running':
      return 'Discovering cloud resources'
    case 'complete':
      return 'Discovery complete'
    case 'failed':
      return 'Discovery failed'
    case 'cancelled':
      return 'Discovery cancelled'
  }
}
