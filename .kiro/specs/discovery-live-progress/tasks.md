# Implementation Plan: Discovery Live Progress

## Overview

Add a live progress experience to connector discovery: run discovery
asynchronously, capture the per-table progress the collector already computes,
stream it to the browser over Server-Sent Events, and render a progress panel
with a polling fallback. No change to credential minting, Steampipe config/sink,
capability gating, or Bedrock region filtering.

Tasks marked `*` are optional test sub-tasks. Each task lists the requirements it
satisfies.

## Tasks

- [x] 1. Define the progress model and reporter seam
  - Add `TableState`, `TableProgress`, and `Progress` types in the `discovery`
    package; add `Progress *Progress` to `Session` (`session.go`)
  - Define the `ProgressReporter` interface next to `source.Run` and add an
    optional nil-safe `Reporter` field to `source.Run`
  - _Requirements: R2.1, R2.2, R2.3, R2.4, R2.5_

- [x]* 1.1 Unit test the progress reducer
  - Table-driven: planned/started/done/error sequences yield the expected
    counts, current table, per-table states, and cumulative resources
  - _Requirements: R2.1, R2.2, R2.3, R2.4, R2.5_

- [x] 2. Report progress from the Steampipe collector
  - In `Source.Collect` (`internal/source/steampipe/steampipe.go`): compute the
    gated table count and call `TablesPlanned`; call `TableStarted` before each
    table; call `TableDone` with the row count `n` currently dropped at `_ = n`;
    call `TableError` on table error (keep `errs <-` and the continue)
  - All calls nil-safe so a nil reporter preserves current behavior exactly
  - _Requirements: R2.1, R2.2, R2.3, R2.4, R2.7_

- [x]* 2.1 Unit test collector reporting
  - Fake querier + recording reporter: assert planned count, start+done per gated
    table, row count flows through, table error reports `TableError` and
    collection continues; and that `Reporter == nil` behaves as before
  - _Requirements: R2.3, R2.4, R2.7_

- [x] 3. Thread progress updates through the orchestrator under lock
  - In the orchestrator (`internal/connector/discovery/orchestrator.go`): build a
    reporter that updates `sess.Progress` under the existing mutex, deep-copying
    snapshots for readers; wire it into `source.Run`
  - Add `Subscribe(connID)` returning a coalescing buffered channel + cancel, fed
    by the progress writer; drop to latest snapshot for slow readers
  - _Requirements: R2.6, R3.3_

- [x] 4. Make discovery asynchronous in the manager
  - Add `DiscoverAsync` / retry equivalent in `internal/connector/manager.go`:
    reject with `ErrConflict` if a session is already running; transition to
    `DISCOVERY_RUNNING`; run the orchestrator in a goroutine on a
    background-rooted cancellable context; on terminal state record the session
    and transition status as the sync path did
  - _Requirements: R1.1, R1.3, R1.4, R1.5_

- [x]* 4.1 Unit test async manager
  - Returns immediately with a running session; second concurrent call ->
    `ErrConflict`; terminal state records session + transitions status
  - _Requirements: R1.1, R1.4, R1.5_

- [x] 5. Return session identity from the discover handler
  - Update `handleDiscover` (`backend/server/connector_handlers.go`) to call the
    async entry and respond immediately with the session id/run id and running
    status (retry path preserved)
  - _Requirements: R1.1, R1.2_

- [x] 6. Accept a query-param token for EventSource auth
  - In `auth.Authenticate()`: when no `Authorization: Bearer` header is present,
    read and validate the token from the `access_token` query parameter the same
    way; reject missing/invalid before proceeding
  - _Requirements: R4.2, R4.3_

- [x]* 6.1 Unit test the auth adapter
  - Query-param token authenticates when the header is absent; invalid token is
    rejected; header path unchanged
  - _Requirements: R4.2, R4.3_

- [x] 7. Add the SSE progress stream endpoint
  - Register `GET /:id/discover/stream` in `registerConnectorRoutes`; implement
    `handleDiscoverStream`: authorize via `connectionForRequest`; 404 when no
    session; set `text/event-stream` + flusher; send current snapshot first; emit
    `progress` events on each change; emit terminal `done` and close; return on
    client disconnect without touching the run; emit no secrets
  - _Requirements: R3.1, R3.2, R3.3, R3.4, R3.5, R3.6, R3.7, R4.1, R4.4_

- [x]* 7.1 Unit test the SSE handler
  - Stub session/subscription: content type is `text/event-stream`; first event
    is a `progress` snapshot; terminal `done` emitted; no-session returns 404
  - _Requirements: R3.1, R3.2, R3.4, R3.6_

- [x] 8. Expose progress on the status endpoint (poll fallback)
  - Confirm `StatusView.LatestSession` serializes `Progress`; ensure nil
    progress is omitted; verify the stream and poll emit the same `Progress`
    shape
  - _Requirements: R5.1, R5.2, R5.3_

- [x] 9. Frontend API + types for progress
  - In `src/lib/api.ts`: extend the discover response type with session id/run id;
    add `discoverStreamUrl(id)` with the `access_token` query param from
    `getToken()`
  - In `src/types/foundation.ts`: add `DiscoveryProgress`/`TableProgress`; extend
    `DiscoverySession` with optional `progress`
  - _Requirements: R1.2, R5.1_

- [x] 10. Build the DiscoveryProgress component
  - New `src/features/platform/DiscoveryProgress.tsx`: open `EventSource`, parse
    `progress`/`done`; render ARIA progressbar + text (tables done/total),
    resources counter, current table, and a per-table log with lucide
    icons + text for pending/running/done/error; show terminal outcome and stop
    activity; on `onerror` fall back to polling `connectorStatus(id)` until
    terminal
  - _Requirements: R6.2, R6.3, R6.4, R6.5, R6.6, R6.7_

- [x]* 10.1 Component test DiscoveryProgress
  - Fake `EventSource` progress->terminal renders bar/counter/table rows; error
    triggers polling fallback
  - _Requirements: R6.4, R6.5, R6.6_

- [x] 11. Wire the panel into the AWS connector flow
  - In `ConnectAws.tsx` `startDiscovery()`: fire `discoverConnector`, then mount
    `DiscoveryProgress` for the connection in the final pane instead of only
    toggling `busy`
  - _Requirements: R6.1_

- [x] 12. Verify end to end
  - `go build ./...`, `go vet ./...`, `gofmt`, and run the connector + steampipe
    + server unit tests; build the frontend
  - Manual on the live stack: start discovery, watch tables advance and resources
    increment, confirm the Bedrock table shows as an isolated table error (not a
    run failure), see the terminal summary, then kill the stream and confirm the
    polling fallback continues
  - _Requirements: R1.5, R2.4, R2.7, R3.4, R3.5, R6.5, R6.6_

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1", "6"] },
    { "id": 1, "tasks": ["1.1", "2", "6.1"] },
    { "id": 2, "tasks": ["2.1", "3"] },
    { "id": 3, "tasks": ["4"] },
    { "id": 4, "tasks": ["4.1", "5", "7"] },
    { "id": 5, "tasks": ["7.1", "8", "9"] },
    { "id": 6, "tasks": ["10"] },
    { "id": 7, "tasks": ["10.1", "11"] },
    { "id": 8, "tasks": ["12"] }
  ]
}
```
