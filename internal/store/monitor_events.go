package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// ErrDuplicateMonitorEvent mirrors connector.ErrDuplicateDelivery: a redelivery
// with an already-seen (org, source, delivery_id) is a no-op so the caller can
// treat the duplicate ingest as an idempotent success (Property P2). The insert
// + unique index on (org_id, source, delivery_id) is the concurrency-safe dedup
// mechanism: two concurrent inserts for the same key race on the index and
// exactly one wins; every losing caller gets this sentinel.
var ErrDuplicateMonitorEvent = errors.New("store: duplicate monitor event")

// ErrMissingDeliveryID is returned by InsertMonitorEvent when the input has an
// empty delivery id. The idempotency key cannot be empty, so such an event is
// rejected before any row is written (Req 2.4): no row is created and no run is
// launched.
var ErrMissingDeliveryID = errors.New("store: monitor event delivery id is required")

// MonitorEventInput is the org-scoped request to persist one incoming event.
// Org is supplied separately by the caller (resolved from the authenticated
// principal) and is never read from the payload (tenant isolation, P1).
type MonitorEventInput struct {
	Source     string          // github|correlate|discovery|sweep
	DeliveryID string          // per-source idempotency key
	EventKind  string          // ci|vulnerability|code_change|resource
	Payload    json.RawMessage
}

// MonitorEvent is one persisted row of the monitor_events table, which is BOTH
// the durable event log AND the work queue: the agent team assignment and run
// state live on the same row. RunID is "" until the event is claimed.
type MonitorEvent struct {
	ID         int64
	OrgID      int64
	Source     string
	DeliveryID string
	EventKind  string
	Payload    json.RawMessage
	Team       string
	State      string
	RunID      string // "" until claimed
	CreatedAt  time.Time
}

// InsertMonitorEvent persists one event in the 'pending' state (durable-first:
// the row is committed before any classification, routing, or run launch).
// Org comes from the caller (resolved from the principal), never from payload.
// On a duplicate (org, source, delivery_id) it returns ErrDuplicateMonitorEvent
// so ingest can return an idempotent no-op (Property P2). An empty delivery id
// is rejected with ErrMissingDeliveryID before any row is written (Req 2.4).
func (s *Store) InsertMonitorEvent(ctx context.Context, org int64, in MonitorEventInput) (int64, error) {
	if in.DeliveryID == "" {
		return 0, ErrMissingDeliveryID // no idempotency key => no row, no run (Req 2.4)
	}
	var id int64
	err := s.pool.QueryRow(ctx,
		`INSERT INTO monitor_events (org_id, source, delivery_id, event_kind, payload, state)
		 VALUES ($1,$2,$3,$4,$5,'pending') RETURNING id`,
		org, in.Source, in.DeliveryID, in.EventKind, in.Payload).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
		return 0, ErrDuplicateMonitorEvent // redelivery is a no-op (P2)
	}
	if err != nil {
		return 0, fmt.Errorf("store: insert monitor event: %w", err)
	}
	return id, nil
}

// SetMonitorEventRoute advances a pending row to 'routed' (or to the terminal
// 'skipped' state when team is empty, i.e. the event kind was unroutable),
// stamping the team the router chose. It is org-scoped so a route can only
// touch the caller's own rows (tenant isolation, P1).
func (s *Store) SetMonitorEventRoute(ctx context.Context, org, id int64, team string) error {
	state := "routed"
	var teamArg any = team
	if team == "" {
		state = "skipped" // unroutable kind => terminal skipped, no team (Req 3.6)
		teamArg = nil
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE monitor_events
		    SET state = $3, team = $4, updated_at = now()
		  WHERE org_id = $1 AND id = $2 AND state = 'pending'`,
		org, id, state, teamArg)
	if err != nil {
		return fmt.Errorf("store: set monitor event route: %w", err)
	}
	return nil
}

// ClaimPendingMonitorEvents atomically claims up to limit routed rows for org,
// moving them to the 'claimed' state and returning them. It is a single
// lock-and-advance CTE: the inner SELECT ... FOR UPDATE SKIP LOCKED locks the
// oldest routed rows and SKIP LOCKED means a row locked by one worker's
// transaction is invisible to every concurrent worker, so a claimed row is
// never handed to a second worker (Property P4). The org predicate keeps a
// worker bound to its single tenant, so one org never claims another's rows
// (tenant isolation, P1). The UPDATE and the lock happen in the same statement,
// so the advance to 'claimed' is atomic with the claim.
func (s *Store) ClaimPendingMonitorEvents(ctx context.Context, org int64, limit int) ([]MonitorEvent, error) {
	rows, err := s.pool.Query(ctx,
		`WITH claimable AS (
		     SELECT id FROM monitor_events
		      WHERE org_id = $1 AND state = 'routed'
		      ORDER BY created_at
		      FOR UPDATE SKIP LOCKED
		      LIMIT $2
		 )
		 UPDATE monitor_events m
		    SET state = 'claimed', updated_at = now()
		   FROM claimable c
		  WHERE m.id = c.id
		  RETURNING m.id, m.org_id, m.source, m.delivery_id, m.event_kind,
		            m.payload, COALESCE(m.team,''), m.state,
		            COALESCE(m.run_id,''), m.created_at`,
		org, limit)
	if err != nil {
		return nil, fmt.Errorf("store: claim pending monitor events: %w", err)
	}
	defer rows.Close()

	var out []MonitorEvent
	for rows.Next() {
		var e MonitorEvent
		if err := rows.Scan(&e.ID, &e.OrgID, &e.Source, &e.DeliveryID, &e.EventKind,
			&e.Payload, &e.Team, &e.State, &e.RunID, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: claim pending monitor events: scan: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: claim pending monitor events: %w", err)
	}
	return out, nil
}

// UpdateMonitorEventRun stamps the launched run_id onto a claimed event row so
// the event and its run are linked (Req 6.3). It is org-scoped so a run can
// only be stamped onto the caller's own rows (tenant isolation, P1).
func (s *Store) UpdateMonitorEventRun(ctx context.Context, org, id int64, runID string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE monitor_events
		    SET run_id = $3, updated_at = now()
		  WHERE org_id = $1 AND id = $2`,
		org, id, runID)
	if err != nil {
		return fmt.Errorf("store: update monitor event run: %w", err)
	}
	return nil
}

// UpdateMonitorEventStatus folds a run status transition onto the monitor_events
// row linked to the run by run_id (Req 7.2): a status write-back on the run also
// advances its originating event. It is org-scoped so a transition can only
// touch the caller's own rows (tenant isolation, P1).
func (s *Store) UpdateMonitorEventStatus(ctx context.Context, org int64, runID, status string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE monitor_events
		    SET state = $3, updated_at = now()
		  WHERE org_id = $1 AND run_id = $2`,
		org, runID, status)
	if err != nil {
		return fmt.Errorf("store: update monitor event status: %w", err)
	}
	return nil
}

// ListMonitorEvents returns monitor events for org, newest-first, up to limit.
// It backs the Mission Control grid read. The org predicate scopes the read to
// the caller's tenant so one org never sees another's events (tenant isolation,
// P1). COALESCE mirrors the claim read so nullable team/run_id scan into "".
func (s *Store) ListMonitorEvents(ctx context.Context, org int64, limit int) ([]MonitorEvent, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, org_id, source, delivery_id, event_kind, payload,
		        COALESCE(team,''), state, COALESCE(run_id,''), created_at
		   FROM monitor_events
		  WHERE org_id = $1
		  ORDER BY created_at DESC
		  LIMIT $2`,
		org, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list monitor events: %w", err)
	}
	defer rows.Close()

	var out []MonitorEvent
	for rows.Next() {
		var e MonitorEvent
		if err := rows.Scan(&e.ID, &e.OrgID, &e.Source, &e.DeliveryID, &e.EventKind,
			&e.Payload, &e.Team, &e.State, &e.RunID, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: list monitor events: scan: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list monitor events: %w", err)
	}
	return out, nil
}
