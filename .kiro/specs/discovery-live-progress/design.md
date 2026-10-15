# Design Document

## Overview

Add a live progress experience to connector discovery. The work has four layers,
each built on a seam that already exists in the codebase:

1. **Async discovery** — `handleDiscover` fires the run in a goroutine and
   returns the session identity immediately, instead of blocking on the whole
   scan.
2. **Progress capture** — a `Progress_Reporter` threaded from the pipeline runner
   into the Steampipe collector turns per-table start/finish/row-count/error
   signals (which the collector already computes but currently discards) into
   run-level `Progress_State` on the `discovery.Session`.
3. **SSE stream** — a new `GET /api/connectors/:id/discover/stream` endpoint emits
   `Progress_Event`s as the state changes and a terminal event at the end.
4. **UI** — a `DiscoveryProgress` React component consumes the stream via
   `EventSource`, renders overall/table/resource progress, and falls back to
   polling the status endpoint.

No credential, sink, capability-gating, or Bedrock-region behavior changes.

## Architecture

```
Browser (ConnectAws.tsx)                 bellu-api (Gin)                         Orchestrator / Collector
────────────────────────                 ──────────────────────                 ─────────────────────────────
POST /connectors/:id/discover  ───────▶  handleDiscover
                                           starts goroutine ───────────────────▶ Orchestrator.Discover(ctx,…)
  { session_id, run_id }  ◀────────────── returns immediately                     sess.Progress initialised
                                                                                   runner.Run(ctx, src, sess)
EventSource(                                                                        └─ Source.Collect(ctx, run, reporter)
  /connectors/:id/discover/stream        handleDiscoverStream                          per table:
  ?token=<jwt>)             ───────────▶   text/event-stream                            reporter.TableStarted(name,i,total)
                                           subscribe to session                         reporter.TableDone(name,rows)
  progress event  ◀───────────────────── flush Progress_Event  ◀──────────────────────  reporter.TableError(name,err)
  progress event  ◀─────────────────────   on each change                              (updates sess.Progress under mutex,
  done event      ◀─────────────────────   terminal + close                             notifies subscribers)

GET /connectors/:id/status  ──────────▶  handleConnectionStatus
  { status, latest_session {             StatusView now carries
    progress {…} } }        ◀──────────── Progress_State (poll fallback)
```

## Components and Interfaces

### Progress model (Requirement 2, 5)

New types in the `discovery` package (`internal/connector/discovery/`),
serialized as part of the `Session` JSON so both the stream and the status poll
share one shape:

```go
type TableState string
const (
    TablePending TableState = "pending"
    TableRunning TableState = "running"
    TableDone    TableState = "done"
    TableError   TableState = "error"
)

type TableProgress struct {
    Table string     `json:"table"`
    State TableState `json:"state"`
    Rows  int        `json:"rows"`
    Error string     `json:"error,omitempty"`
}

type Progress struct {
    TablesTotal        int             `json:"tables_total"`
    TablesDone         int             `json:"tables_done"`
    ResourcesCollected int             `json:"resources_collected"`
    CurrentTable       string          `json:"current_table,omitempty"`
    Tables             []TableProgress `json:"tables"`
}
```

`Session` (`session.go`) gains `Progress *Progress` (nil until collection
starts). All reads/writes of `Progress` go through the orchestrator mutex that
already guards `latest` — no new lock, no torn snapshots (R2.6). Snapshots handed
to subscribers are deep-copied so a reader never aliases live state.

### Progress reporter seam (Requirement 2.2–2.4, 2.7)

A minimal interface, defined where `source.Run` lives so the collector can accept
it without importing the connector package:

```go
type ProgressReporter interface {
    TablesPlanned(total int)
    TableStarted(table string, index int)
    TableDone(table string, rows int)
    TableError(table string, err error)
}
```

- `source.Run` gains an optional `Reporter ProgressReporter` field (nil-safe: a
  nil reporter means "no progress", preserving every existing call site and
  test).
- `steampipe.Source.Collect` (`internal/source/steampipe/steampipe.go`):
  - after capability gating is known, call `TablesPlanned(countEnabled)` — the
    number of tables that pass the gate, not the raw catalog length;
  - before each `collectTable`, call `TableStarted(spec.Table, i)`;
  - on success, call `TableDone(spec.Table, n)` using the row count `n` that is
    **currently discarded at `_ = n`**;
  - on error, call `TableError(spec.Table, err)` alongside the existing
    `errs <- …`.
  This is the entire change to the collector: four nil-safe calls; it adds no
  queries (R2.7).

### Async discovery (Requirement 1)

- `Manager` (`internal/connector/manager.go`) gains `DiscoverAsync` (and the
  retry equivalent) that:
  - rejects with `ErrConflict` if a session is already running for the connection
    (R1.4);
  - transitions the connection to `DISCOVERY_RUNNING`, initialises
    `sess.Progress`, and starts the orchestrator run in a goroutine with a
    background-rooted, cancellable context (not the HTTP request context, so the
    run outlives the request);
  - on terminal state, records the session and transitions the connection exactly
    as the synchronous path did (R1.5).
- The orchestrator wires the reporter into `source.Run` for the run and keeps its
  existing `latest`-map/`cancel` bookkeeping, so `Cancel` and `Latest` keep
  working.
- `handleDiscover` (`backend/server/connector_handlers.go`) calls the async entry
  and returns `{ "session": { id, run_id, status: "running" } }` immediately
  (R1.1, R1.2).

### SSE stream endpoint (Requirement 3, 4)

- New route in `registerConnectorRoutes`:
  `conns.GET("/:id/discover/stream", s.handleDiscoverStream)`.
- `handleDiscoverStream`:
  - authorizes via the shared `connectionForRequest` path (org scope + project
    RBAC), using the query-param token adapter below (R4.1);
  - if no session exists, responds `404`/no-session rather than hanging (R3.6);
  - sets `Content-Type: text/event-stream`, `Cache-Control: no-cache`,
    `Connection: keep-alive`, obtains the `http.Flusher`;
  - sends the current `Progress` snapshot as the first `event: progress` (R3.2);
  - subscribes to session updates (buffered channel registered on the session);
    on each update writes `event: progress\ndata: <json>\n\n` and flushes (R3.3,
    R3.7);
  - on terminal state writes `event: done\ndata: <json outcome>\n\n`, flushes,
    and returns, closing the stream (R3.4);
  - returns on `c.Request.Context().Done()` (client disconnect) without touching
    the session (R3.5).
- Subscription mechanism on the orchestrator: `Subscribe(connID) (<-chan
  Progress, cancel func(), ok bool)` registers a buffered channel the progress
  writer fans out to under the mutex; slow/absent readers drop to the latest
  snapshot (coalescing) so a stuck client can't block the run.

#### Auth adapter for EventSource (Requirement 4.2, 4.3)

`EventSource` cannot set headers. The auth middleware (`auth.Authenticate()`)
gains a fallback: when no `Authorization: Bearer` header is present, read the
token from the `access_token` query parameter and validate it identically. This
is scoped behavior in one middleware; the bearer path is unchanged. A missing or
invalid token is rejected before any event is written (R4.3). No event ever
contains credentials (R4.4) — `Progress` carries only names, counts, and error
strings.

### Status endpoint fields (Requirement 5)

`StatusView` (`manager.go`) already embeds `LatestSession *discovery.Session`;
since `Session` now carries `Progress`, the poll response gains progress for free
(R5.1). Nil progress serializes as absent (R5.2). The stream and poll emit the
same `Progress` type, guaranteeing consistency (R5.3).

### Frontend (Requirement 6)

- `src/lib/api.ts`:
  - `discoverConnector(id)` stays `POST /discover` but its response type gains
    `session_id`/`run_id`;
  - add `discoverStreamUrl(id)` returning
    `${API_BASE}/api/connectors/${id}/discover/stream?access_token=<jwt>` built
    from `getToken()`.
- `src/types/foundation.ts`: add `DiscoveryProgress` / `TableProgress` and extend
  `DiscoverySession` with optional `progress`.
- New component `src/features/platform/DiscoveryProgress.tsx`:
  - opens an `EventSource` on mount, parses `progress` and `done` events into
    state;
  - renders: an ARIA `progressbar` (tables done/total) with a text label
    (R6.2, R6.7), a resources-collected counter, the current table (R6.3), and a
    scrolling per-table list using lucide icons + text for pending/running/
    done/error (R6.4, color is never the sole signal);
  - on terminal `done`, shows the outcome and stops the activity indicator (R6.5);
  - on `EventSource` `onerror`, closes it and starts polling `connectorStatus(id)`
    on an interval, mapping `progress` the same way, until terminal (R6.6).
- `ConnectAws.tsx` `startDiscovery()`: fire `discoverConnector`, then mount
  `DiscoveryProgress` for the connection in the final pane instead of only
  toggling `busy` (R6.1). The connector list/detail can reuse the same component
  later; this spec wires the wizard.

## Data Models

No database schema changes. `Progress` lives only in the in-memory `Session`;
only the terminal session outcome is persisted via the existing
`repo.RecordSession`, exactly as today. No credentials are added to any payload.

## Error Handling

- **Table error:** captured as `TableError`, surfaced in `Table_Progress` and the
  per-table UI row; collection continues (error isolation preserved). The Bedrock
  region 404, once it occurs, shows as a single table error, not a run failure.
- **Run failure / cancel:** the terminal `done` event carries status
  `failed`/`cancelled` and the error; the panel renders it and stops.
- **Stream unavailable / dropped:** client falls back to polling (R6.6); the run
  is unaffected (R3.5).
- **No session:** stream returns no-session instead of hanging (R3.6).
- **Slow client:** coalescing subscription drops intermediate snapshots to the
  latest, so a stuck reader cannot stall the collector.

## Testing Strategy

- **Unit (Go):**
  - Progress reducer: `TablesPlanned`/`TableStarted`/`TableDone`/`TableError`
    produce the expected `Progress` (counts, current table, per-table states,
    cumulative resources); table-driven.
  - Collector reporting: run `Source.Collect` with a fake querier and a recording
    reporter; assert it reports planned count, one start+done per gated table, the
    discarded row count now flows through, and a table error reports `TableError`
    while collection continues.
  - Nil reporter: `Collect` with `Reporter == nil` behaves exactly as before (no
    panic, same events) — guards backward compatibility.
  - Async manager: `DiscoverAsync` returns immediately with a running session;
    a second concurrent call returns `ErrConflict`; terminal state records the
    session and transitions status (reuse existing fakes).
  - SSE handler: with a stub session/subscription, assert `text/event-stream`
    content type, an immediate first `progress` event, and a terminal `done`
    event; assert no-session returns 404.
  - Auth adapter: `access_token` query param authenticates when the header is
    absent; invalid token is rejected.
- **Frontend:** component test for `DiscoveryProgress` driven by a fake
  `EventSource` (progress → terminal) and the polling fallback on error; assert
  the progressbar role, text labels, and per-table rows render.
- **Behavioral/manual:** run discovery on the live stack against a connector;
  confirm the panel shows tables advancing, resources incrementing, the Bedrock
  table reported as an isolated error, and a terminal summary; kill the stream
  and confirm polling takes over.

## Requirements Mapping

| Requirement | Design element |
|-------------|----------------|
| R1.1–R1.2 Async start + identity | `handleDiscover` → `Manager.DiscoverAsync`, returns session id/run id |
| R1.3 Observable while running | goroutine run + `latest` map read by status/stream |
| R1.4 No concurrent duplicate | `DiscoverAsync` conflict check → `ErrConflict` |
| R1.5 No persistence regression | terminal `recordSession` + status transitions unchanged |
| R2.1 Tables total | `TablesPlanned(countEnabled)` from collector |
| R2.2 Current/running table | `TableStarted` sets `CurrentTable` + `TableRunning` |
| R2.3 Done + row count | `TableDone(n)` using previously discarded `n` |
| R2.4 Error isolation | `TableError` + continue loop |
| R2.5 Cumulative resources | reducer sums row counts into `ResourcesCollected` |
| R2.6 No torn snapshot | updates under orchestrator mutex; deep-copied snapshots |
| R2.7 No new API calls | reporter only surfaces existing counts |
| R3.1 SSE endpoint | `GET /:id/discover/stream`, `text/event-stream` |
| R3.2 Immediate first event | send current snapshot on connect |
| R3.3 Event on change | fan-out subscription writes on each update |
| R3.4 Terminal + close | `done` event then return |
| R3.5 Disconnect safe | return on request ctx done; run untouched |
| R3.6 No session | 404/no-session response |
| R3.7 Flush per event | `http.Flusher.Flush()` after each write |
| R4.1 Authorized | `connectionForRequest` org+project RBAC |
| R4.2 Query-param token | `access_token` fallback in `auth.Authenticate` |
| R4.3 Reject invalid | validated before first event |
| R4.4 No secrets | `Progress` carries no credentials |
| R5.1–R5.3 Poll fallback fields | `StatusView.LatestSession.Progress`, same type |
| R6.1 Panel on start | `ConnectAws.startDiscovery` mounts `DiscoveryProgress` |
| R6.2 Overall + resources | progressbar + counter |
| R6.3 Current table | current-table row |
| R6.4 Per-table log | per-table list with state icons + text |
| R6.5 Terminal outcome | `done` event renders summary/error |
| R6.6 Stream fallback | `onerror` → poll `connectorStatus` |
| R6.7 Design system + a11y | Radix/Tailwind/lucide, ARIA progressbar, text not color |
