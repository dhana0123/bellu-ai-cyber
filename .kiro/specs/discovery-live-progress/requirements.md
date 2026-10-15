# Requirements Document

## Introduction

Cloud discovery scans a customer's account by querying a Steampipe service table
by table, emitting one resource per row. Today the operator gets no feedback
while this runs: the `POST /api/connectors/:id/discover` request blocks
synchronously until the entire scan finishes, the frontend "Start Discovery"
button simply spins, and the rich progress signals the collector already
produces (total table count, per-table row counts, per-table completion,
per-table errors) are discarded (`_ = n`, events drained into `_`).

This spec adds a live progress experience for discovery. It makes discovery run
asynchronously, captures per-table progress into run-level state, streams that
state to the browser over Server-Sent Events (SSE), and renders a progress panel
showing overall completion, resources collected, the table currently being
scanned, and a per-table log with success/error status. A polling fallback over
the existing status endpoint is retained for when the stream is unavailable.

This spec does not change how credentials are minted, how the Steampipe
connection/config is written, the capability gating, or the Bedrock region
filtering. It consumes the existing collector signals; it does not add new cloud
API calls.

## Glossary

- **Discovery_Session**: A single run of discovery for one Connection, tracked by
  the orchestrator with an ID, run ID, status, and (new) progress fields.
- **Progress_State**: The run-level, in-memory snapshot of a Discovery_Session's
  progress: tables total, tables done, resources collected, current table, and a
  per-table list of row counts and errors.
- **Progress_Event**: One Server-Sent Event carrying a Progress_State snapshot or
  a terminal outcome (complete / failed / cancelled).
- **Progress_Stream**: The SSE endpoint
  (`GET /api/connectors/:id/discover/stream`) that emits Progress_Events for the
  latest Discovery_Session of a Connection until it reaches a terminal state.
- **Table_Progress**: Per-table progress for one Steampipe table: table name,
  state (pending / running / done / error), row count, and optional error
  message.
- **Progress_Reporter**: The seam threaded from the run loop into the collector
  that turns per-table start/finish/row-count/error signals into Progress_State
  updates.
- **Status_Poll**: The existing `GET /api/connectors/:id/status` endpoint,
  extended to include Progress_State so the UI can fall back to polling.

## Requirements

### Requirement 1: Asynchronous discovery

**User Story:** As an operator, I want starting discovery to return immediately
and run in the background, so that I can watch its progress instead of waiting on
a blocked request.

#### Acceptance Criteria

1. WHEN an operator calls `POST /api/connectors/:id/discover`, THE connector
   SHALL start the Discovery_Session in the background and respond without
   waiting for the scan to finish.
2. WHEN discovery is started, THE response SHALL identify the started
   Discovery_Session (its session ID and run ID) so a client can subscribe to its
   progress.
3. WHILE a Discovery_Session is running, THE connector SHALL allow
   `GET /api/connectors/:id/status` and the Progress_Stream to observe it.
4. WHEN a Discovery_Session for a Connection is already running, THE connector
   SHALL NOT start a second concurrent session for the same Connection and SHALL
   report a conflict.
5. WHEN an asynchronous Discovery_Session reaches a terminal state, THE connector
   SHALL persist the session outcome exactly as the synchronous path did
   previously (no regression to session recording or connection status
   transitions).

### Requirement 2: Progress tracking in run state

**User Story:** As an operator, I want discovery to track how far it has
progressed, so that the system can tell me how much work is done and what remains.

#### Acceptance Criteria

1. WHEN a Discovery_Session begins collection, THE Progress_State SHALL record the
   total number of tables that will be scanned (after capability gating).
2. WHEN the collector starts a table, THE Progress_State SHALL record that table
   as the current table and mark its Table_Progress state as running.
3. WHEN the collector finishes a table successfully, THE Progress_State SHALL mark
   that table's Table_Progress as done, record its row count, and increment tables
   done.
4. WHEN the collector finishes a table with an error, THE Progress_State SHALL
   mark that table's Table_Progress as error with the error message, increment
   tables done, and continue with remaining tables (error isolation preserved).
5. WHILE collection runs, THE Progress_State SHALL maintain a cumulative count of
   resources collected across all completed tables.
6. THE Progress_State SHALL be updated under the orchestrator's existing
   synchronization so concurrent readers (Status_Poll and Progress_Stream) never
   observe a torn snapshot.
7. THE Progress_Reporter SHALL add zero new cloud API calls; it SHALL only surface
   counts and states the collector already produces.

### Requirement 3: Server-Sent Events progress stream

**User Story:** As an operator, I want to receive discovery progress in real time,
so that I see updates as they happen without manually refreshing.

#### Acceptance Criteria

1. THE connector SHALL expose `GET /api/connectors/:id/discover/stream` that
   responds with `Content-Type: text/event-stream` and streams Progress_Events for
   the latest Discovery_Session of the Connection.
2. WHEN a client connects to the Progress_Stream, THE connector SHALL send the
   current Progress_State immediately as the first event, so a late subscriber is
   not blank.
3. WHILE the Discovery_Session is running, THE connector SHALL emit a
   Progress_Event whenever the Progress_State changes (table started, table
   finished, resource counts updated).
4. WHEN the Discovery_Session reaches a terminal state (complete, failed, or
   cancelled), THE connector SHALL emit a terminal Progress_Event carrying that
   outcome and THEN close the stream.
5. WHEN the client disconnects from the Progress_Stream, THE connector SHALL stop
   streaming for that client and SHALL NOT cancel or affect the running
   Discovery_Session.
6. IF no Discovery_Session exists for the Connection, THEN THE Progress_Stream
   SHALL respond that none is available rather than hanging open.
7. THE Progress_Stream SHALL flush each Progress_Event to the client as it is
   produced rather than buffering until completion.

### Requirement 4: Progress stream authorization

**User Story:** As a security-conscious operator, I want the progress stream to be
authorized to the same standard as the rest of the connector API, so that
progress data is not exposed to unauthorized callers.

#### Acceptance Criteria

1. THE Progress_Stream SHALL require a valid authenticated principal and SHALL
   authorize the request against the Connection's project, consistent with the
   other connector read endpoints.
2. WHERE the client cannot send an `Authorization` header (browser `EventSource`),
   THE Progress_Stream SHALL accept the authentication token via a query parameter
   and validate it the same way as a bearer token.
3. WHEN the token is missing or invalid, THE Progress_Stream SHALL reject the
   request before streaming any Progress_Event.
4. THE Progress_Stream SHALL return no cloud credentials or secrets in any
   Progress_Event.

### Requirement 5: Status endpoint progress fields (polling fallback)

**User Story:** As an operator on a network that drops long-lived connections, I
want progress available by polling, so that I still see progress when SSE is
unavailable.

#### Acceptance Criteria

1. THE `GET /api/connectors/:id/status` response SHALL include the current
   Progress_State of the latest Discovery_Session when one exists.
2. WHEN no Discovery_Session has started for a Connection, THE Status_Poll SHALL
   omit Progress_State without error.
3. THE Progress_State returned by the Status_Poll SHALL be consistent with the
   snapshots emitted by the Progress_Stream (same fields, same meaning).

### Requirement 6: Live progress UI

**User Story:** As an operator, I want a progress panel while discovery runs, so
that I can see how much discovery is happening and whether anything failed.

#### Acceptance Criteria

1. WHEN an operator starts discovery from the AWS connector flow, THE UI SHALL
   display a progress panel instead of only a spinning button.
2. THE progress panel SHALL show overall progress as tables done out of tables
   total and a cumulative resources-collected count.
3. THE progress panel SHALL show the table currently being scanned while a table
   is running.
4. THE progress panel SHALL show a per-table log listing each table with a
   visual state (pending, running, done, error) and, where applicable, its row
   count or error.
5. WHEN the Discovery_Session reaches a terminal state, THE progress panel SHALL
   present the final outcome (success summary, or failure/cancel with the error)
   and stop indicating activity.
6. IF the Progress_Stream connection fails or is unavailable, THEN THE UI SHALL
   fall back to polling the Status_Poll and continue showing progress.
7. THE progress panel SHALL use the existing frontend design system (Radix/Tailwind
   components, lucide icons, toasts) and SHALL be accessible: progress conveyed by
   an ARIA progress role with text, not color alone.

## Out of Scope

- Multi-instance / horizontally-scaled progress (the Progress_State is in-memory
  in one process; multi-instance fan-out via a shared store or sticky routing is
  a future concern).
- Persisting per-table progress history to the database (only the terminal
  session outcome is persisted, as today).
- Streaming the actual discovered resource payloads to the browser (only counts
  and states are streamed).
- Changes to credential minting, Steampipe config/sink, capability gating, or
  Bedrock region filtering.
