package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/bellu/bellu-ai-cyber/internal/monitor"
	"github.com/bellu/bellu-ai-cyber/internal/store"
)

// This file implements the Phase 1 Mission Control seam of continuous
// monitoring: the durable-first ingest endpoint (POST /api/monitor/events) and
// the org-scoped events read (GET /api/monitor/events). Route registration is
// wired separately in monitor_routes.go (task 5); this file owns only the
// request/response handling and the two handler methods.
//
// Every org is resolved from the authenticated principal via the mcpOrg pattern
// and is NEVER read from the request body, query, or path, so a caller can
// neither act as nor read another tenant (tenant isolation, Requirement 19.1).

const (
	// defaultMonitorEventsLimit is the page size applied to GET /api/monitor/events
	// when the caller supplies no limit (Requirement 8.2).
	defaultMonitorEventsLimit = 100
	// maxMonitorEventsLimit caps a caller-supplied limit so a single read can
	// never pull an unbounded page (Requirement 8.2).
	maxMonitorEventsLimit = 500
)

// monitorIngestRequest is the body of POST /api/monitor/events. It carries no
// org field by design: the tenant is resolved from the authenticated principal
// and any org in the body is ignored (Requirement 19.1). source selects the
// payload shape Classify reads; payload is the raw source-specific body.
type monitorIngestRequest struct {
	Source     string          `json:"source"`
	DeliveryID string          `json:"delivery_id"`
	Payload    json.RawMessage `json:"payload"`
}

// handleMonitorIngest is the durable-first, idempotent ingest seam. It resolves
// the org from the principal, classifies the raw payload, persists a pending
// row BEFORE any routing, then advances the row via the static router.
//
// Error discipline (Requirement 21): an unparseable or unclassifiable body is a
// 400 with no row persisted; a duplicate (org, source, delivery_id) is an
// idempotent success no-op rather than an error (Property P2); and any
// unexpected store failure collapses to a generic 503 that leaks no backend
// detail.
func (s *Server) handleMonitorIngest(c *gin.Context) {
	org, ok := mcpOrg(c) // org from principal, NEVER from body (Requirement 19.1)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	var req monitorIngestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	// Classify reads the source-specific payload shape. An unclassifiable body
	// is a 400 and persists NO row (Requirement 21.1).
	kind, err := monitor.Classify(req.Source, req.Payload)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unclassifiable event"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	// Durable-first: persist the pending row before any routing (Requirement 1.1).
	id, err := s.store.InsertMonitorEvent(ctx, org, store.MonitorEventInput{
		Source:     req.Source,
		DeliveryID: req.DeliveryID,
		EventKind:  kind,
		Payload:    req.Payload,
	})
	if err != nil {
		// A redelivery of an already-seen key is an idempotent no-op: report the
		// existing row semantics as a success rather than an error (Property P2).
		if errors.Is(err, store.ErrDuplicateMonitorEvent) {
			c.JSON(http.StatusOK, gin.H{"state": "duplicate", "duplicate": true})
			return
		}
		// A missing delivery id is a caller error, not a backend failure.
		if errors.Is(err, store.ErrMissingDeliveryID) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "delivery_id is required"})
			return
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "temporarily unavailable"})
		return
	}

	// Static route: advance pending -> routed, or -> skipped when the kind is
	// unroutable (team == ""). The event is already durably persisted, so a
	// routing failure never loses the event — it just stays pending.
	team := monitor.Route(kind)
	state := "routed"
	if team == "" {
		state = "skipped" // unroutable kind => terminal skipped (Requirement 3.6)
	}
	if err := s.store.SetMonitorEventRoute(ctx, org, id, string(team)); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "temporarily unavailable"})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"id": id, "state": state})
}

// monitorEventView is the serialized shape of one monitor event in the Mission
// Control read. It mirrors the store's MonitorEvent, exposing only the fields
// the grid needs; the payload is passed through verbatim.
type monitorEventView struct {
	ID         int64           `json:"id"`
	Source     string          `json:"source"`
	DeliveryID string          `json:"delivery_id"`
	EventKind  string          `json:"event_kind"`
	Payload    json.RawMessage `json:"payload"`
	Team       string          `json:"team,omitempty"`
	State      string          `json:"state"`
	RunID      string          `json:"run_id,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

// handleMonitorEventsList backs the Mission Control grid. It returns the
// requesting principal's own events, newest-first (Requirement 8.1). A caller
// may supply a limit; when absent the configured default page size applies, and
// any supplied limit is capped at the configured maximum (Requirement 8.2). The
// store scopes the read to the caller's org, so one tenant never sees another's
// events (tenant isolation, Requirement 19.1).
func (s *Server) handleMonitorEventsList(c *gin.Context) {
	org, ok := mcpOrg(c) // org from principal, NEVER from query (Requirement 19.1)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	// queryInt returns the default for a missing/invalid/non-positive limit; cap
	// any caller-supplied value at the configured maximum (Requirement 8.2).
	limit := queryInt(c, "limit", defaultMonitorEventsLimit)
	if limit > maxMonitorEventsLimit {
		limit = maxMonitorEventsLimit
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	events, err := s.store.ListMonitorEvents(ctx, org, limit)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "temporarily unavailable"})
		return
	}

	views := make([]monitorEventView, 0, len(events))
	for _, e := range events {
		views = append(views, monitorEventView{
			ID:         e.ID,
			Source:     e.Source,
			DeliveryID: e.DeliveryID,
			EventKind:  e.EventKind,
			Payload:    e.Payload,
			Team:       e.Team,
			State:      e.State,
			RunID:      e.RunID,
			CreatedAt:  e.CreatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"events": views})
}

// defaultClaimBatch is the number of routed rows a single claim pulls when the
// caller supplies no explicit limit. It is a small batch so a worker drains the
// queue steadily without holding a large hot set (Requirement 6.1).
const defaultClaimBatch = 4

// maxClaimBatch caps a caller-supplied claim limit so one claim can never pull
// an unbounded page of work in a single transaction.
const maxClaimBatch = 64

// monitorClaimRequest is the optional body of POST /api/agent/monitor/claim. It
// carries no org field by design: the tenant is resolved from the authenticated
// principal and any org in the body is ignored (Requirement 19.1). limit is the
// claim batch size; when absent (<= 0) defaultClaimBatch applies.
type monitorClaimRequest struct {
	Limit int `json:"limit"`
}

// monitorClaimView is the serialized shape of one claimed event in the claim
// response. It mirrors monitorEventView but always includes run_id, since a
// claimed row has just had its launched run stamped onto it (Requirement 6.3).
type monitorClaimView struct {
	ID         int64           `json:"id"`
	Source     string          `json:"source"`
	DeliveryID string          `json:"delivery_id"`
	EventKind  string          `json:"event_kind"`
	Payload    json.RawMessage `json:"payload"`
	Team       string          `json:"team,omitempty"`
	State      string          `json:"state"`
	RunID      string          `json:"run_id"`
	CreatedAt  time.Time       `json:"created_at"`
}

// handleMonitorClaim is the agents-plane claim seam. It resolves the org from
// the principal (never the body), atomically claims up to a batch of routed
// events via FOR UPDATE SKIP LOCKED, and launches a read-only agent run for
// each claimed row.
//
// Safety invariant (Property P5 / Req 9.1, 9.2): auto-triggered runs are
// strictly read-only. The AgentRunInput built here NEVER carries a scope — Scope
// stays nil, so authz_scope is persisted as NULL and active_testing as false.
// There is no input path by which a claim can request active_testing=true or a
// non-nil scope, so the read-only guarantee is structural rather than a runtime
// check: the ActiveTestingGate stays the only path to active testing (Req 9.4),
// and it is left entirely untouched here.
//
// For each claimed row the launched run_id is stamped back onto the event
// (UpdateMonitorEventRun, Req 6.3) and triggered_by records the launching event
// id. Org is resolved from the principal throughout (Req 19.1).
func (s *Server) handleMonitorClaim(c *gin.Context) {
	org, ok := mcpOrg(c) // org from principal, NEVER from body (Requirement 19.1)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	// The body is optional; an absent or unparseable limit falls back to the
	// default batch. A caller may also pass ?limit= as a query param.
	var req monitorClaimRequest
	_ = c.ShouldBindJSON(&req)
	limit := req.Limit
	if limit <= 0 {
		limit = queryInt(c, "limit", defaultClaimBatch)
	}
	if limit <= 0 {
		limit = defaultClaimBatch
	}
	if limit > maxClaimBatch {
		limit = maxClaimBatch
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	// Atomic SKIP LOCKED claim: a claimed row is never handed to a second worker
	// (Property P4). The store scopes the claim to the caller's org (Req 19.1).
	events, err := s.store.ClaimPendingMonitorEvents(ctx, org, limit)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "temporarily unavailable"})
		return
	}

	claims := make([]monitorClaimView, 0, len(events))
	for i := range events {
		ev := &events[i]

		// Build a strictly read-only run input. Scope is deliberately left nil:
		// authz_scope persists as NULL and active_testing as false (Property P5 /
		// Req 9.1). triggered_by records the launching event id.
		in := store.AgentRunInput{
			Target:      targetFromEvent(*ev),
			TriggeredBy: strconv.FormatInt(ev.ID, 10),
		}
		// Assert the read-only invariant has not been violated by any future
		// edit to targetFromEvent or this block: an auto-triggered claim must
		// never carry a scope (Req 9.2). This guard launches no scoped run.
		if in.Scope != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "auto-triggered run must be read-only"})
			return
		}

		runID, err := s.store.CreateAgentRun(ctx, org, in)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "temporarily unavailable"})
			return
		}

		// Stamp the launched run back onto the event and advance the linkage
		// (Req 6.3). Org-scoped so only the caller's own row is touched.
		if err := s.store.UpdateMonitorEventRun(ctx, org, ev.ID, runID); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "temporarily unavailable"})
			return
		}
		ev.RunID = runID

		claims = append(claims, monitorClaimView{
			ID:         ev.ID,
			Source:     ev.Source,
			DeliveryID: ev.DeliveryID,
			EventKind:  ev.EventKind,
			Payload:    ev.Payload,
			Team:       ev.Team,
			State:      ev.State,
			RunID:      ev.RunID,
			CreatedAt:  ev.CreatedAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{"claims": claims})
}

// targetFromEvent derives the scan target for an auto-triggered run from the
// monitor event's payload. For v1 it extracts a canonical entity id or a raw
// URL/IP from the common payload shapes; when neither is present it falls back
// to a minimal raw target carrying the event's delivery id so the run still has
// a stable, non-empty target reference. It NEVER constructs a scope — the
// read-only invariant lives entirely with the caller (handleMonitorClaim).
func targetFromEvent(ev store.MonitorEvent) store.AgentRunTarget {
	// Best-effort decode of the common target-bearing fields. A payload that
	// doesn't carry any of these simply falls through to the raw fallback.
	var p struct {
		CanonicalID string `json:"canonical_id"`
		AssetID     string `json:"asset_id"`
		Target      string `json:"target"`
		URL         string `json:"url"`
		Host        string `json:"host"`
	}
	if len(ev.Payload) > 0 {
		_ = json.Unmarshal(ev.Payload, &p)
	}

	switch {
	case p.CanonicalID != "":
		return store.AgentRunTarget{Kind: "entity", CanonicalID: p.CanonicalID}
	case p.AssetID != "":
		return store.AgentRunTarget{Kind: "entity", CanonicalID: p.AssetID}
	case p.Target != "":
		return store.AgentRunTarget{Kind: "raw", Raw: p.Target}
	case p.URL != "":
		return store.AgentRunTarget{Kind: "raw", Raw: p.URL}
	case p.Host != "":
		return store.AgentRunTarget{Kind: "raw", Raw: p.Host}
	default:
		// Minimal fallback: a stable raw reference derived from the event so the
		// run is never launched with an empty target.
		return store.AgentRunTarget{Kind: "raw", Raw: ev.Source + ":" + ev.DeliveryID}
	}
}
