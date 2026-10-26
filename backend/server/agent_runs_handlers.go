package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/bellu/bellu-ai-cyber/internal/monitor"
	"github.com/bellu/bellu-ai-cyber/internal/store"
)

// This file implements Seam 3 of the agents plane: the Runs seam
// (POST /api/agent/runs, GET /api/agent/runs/:id). Route registration lives in
// server.go (task 5); this file owns only the request/response types and the
// two handlers. Every org is resolved from the authenticated principal via the
// mcpOrg pattern and NEVER read from the request body, so a caller cannot act
// as, or read, another tenant (tenant isolation).

// RunTargetRef is the Runs-seam scan target: either a graph entity (addressed
// by canonical id) or an operator-supplied raw target (URL/IP).
//
// NOTE: a broader `TargetRef` is introduced elsewhere in this package by the
// graph-context seam (task 2.1). To stay independent of task-landing order and
// avoid a duplicate-type compile error, this file deliberately declares its own
// distinct `RunTargetRef` rather than referencing or redeclaring `TargetRef`.
type RunTargetRef struct {
	Kind        string `json:"kind"`                   // "entity" | "raw"
	CanonicalID string `json:"canonical_id,omitempty"` // kind:provider:native-id
	Raw         string `json:"raw,omitempty"`          // URL/IP for raw targets
}

// LaunchRequest is the body of POST /api/agent/runs. It carries no org field by
// design: the tenant is resolved from the authenticated principal.
type LaunchRequest struct {
	Target     RunTargetRef `json:"target"`
	AuthzScope *AuthzScope  `json:"authz_scope,omitempty"` // omitted => read-only reasoning only
}

// AuthzScope authorizes active testing for a run. An absent scope, or one with
// ActiveTesting=false, means active testing is OFF and the run is read-only
// graph reasoning. When ActiveTesting is true, AllowTargets must be non-empty.
type AuthzScope struct {
	ActiveTesting bool     `json:"active_testing"`
	AllowTargets  []string `json:"allow_targets"` // exact hosts/IPs/CIDRs the sandbox may hit
}

// RunStatus is the response of GET /api/agent/runs/:id.
type RunStatus struct {
	RunID         string `json:"run_id"`
	Status        string `json:"status"` // queued|running|completed|failed
	FindingsCount int    `json:"findings_count"`
	ActiveTesting bool   `json:"active_testing"` // whether this run was authorized for it
}

// handleAgentLaunch accepts a scan launch request, persists an org-scoped run
// row, and returns 202 with the new run id. Active testing is opt-in: a scope
// that enables it MUST carry a non-empty allow list, otherwise the launch is
// rejected with 400 before anything is persisted.
func (s *Server) handleAgentLaunch(c *gin.Context) {
	org, ok := mcpOrg(c) // org from principal, NEVER from body
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	var req LaunchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	// Opt-in active testing: active_testing=true with an empty allow list is a
	// bad request. An absent scope => read-only graph reasoning only.
	if req.AuthzScope != nil && req.AuthzScope.ActiveTesting && len(req.AuthzScope.AllowTargets) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "active testing requires allow_targets"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	runID, err := s.store.CreateAgentRun(ctx, org, launchToInput(req)) // persists org-scoped run row
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "temporarily unavailable"})
		return
	}
	// Hand off to the agents plane out-of-band (queue/notify); do not block.
	c.JSON(http.StatusAccepted, gin.H{"run_id": runID, "status": "queued"})
}

// handleAgentRunStatus returns a run's status scoped to the caller's org. A run
// belonging to another tenant, or a nonexistent run, both return 404 with the
// same message so the endpoint never discloses a run's existence across orgs.
func (s *Server) handleAgentRunStatus(c *gin.Context) {
	org, ok := mcpOrg(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	// GetAgentRun scopes by org: a run from another tenant is a not-found, so a
	// caller cannot read or probe another org's run (tenant isolation).
	st, err := s.store.GetAgentRun(ctx, org, c.Param("id"))
	if err != nil {
		if errors.Is(err, store.ErrAgentRunNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
			return
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "temporarily unavailable"})
		return
	}
	c.JSON(http.StatusOK, RunStatus{
		RunID:         st.RunID,
		Status:        st.Status,
		FindingsCount: st.FindingsCount,
		ActiveTesting: st.ActiveTesting,
	})
}

// StatusWriteRequest is the body of PUT /api/agent/runs/:id/status. It carries
// no org field by design: the tenant is resolved from the authenticated
// principal via mcpOrg, never read from the body (tenant isolation, P1).
type StatusWriteRequest struct {
	Status        string `json:"status"`         // running|completed|failed
	FindingsCount int    `json:"findings_count"` // count folded onto the run row
}

// handleAgentRunStatusWrite folds a worker's status write-back onto both the
// org-scoped agent_runs row and its linked monitor_events row. The status must
// be one of running|completed|failed; any other value is rejected with 400 and
// no row changes (Req 7.3). On success the run's status + findings count are
// folded on, with started_at/ended_at stamped from the transition (started on
// 'running', ended on the terminal 'completed'/'failed' states), and the same
// transition is folded onto the linked monitor_events row (Req 7.1, 7.2).
//
// A run belonging to another tenant (or a nonexistent run) matches no org-scoped
// row, so the write is a no-op and we return the same 404 as a true miss: the
// endpoint never discloses a run's existence across orgs (mirrors
// handleAgentRunStatus / GetAgentRun; Req 19.2).
//
// Note: auto-triggered findings carry status "Unvalidated"; that labeling
// happens at finding ingest, not here. This handler only folds run/event state.
func (s *Server) handleAgentRunStatusWrite(c *gin.Context) {
	org, ok := mcpOrg(c) // org from principal, NEVER from body (P1)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	var req StatusWriteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	// Validate before touching any row: an out-of-set status changes nothing
	// (Req 7.3).
	switch req.Status {
	case "running", "completed", "failed":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid status"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	runID := c.Param("id")

	// Fold onto the org-scoped run row first. UpdateAgentRunStatus returns false
	// when no row matched for this org: a cross-org or nonexistent run is a 404
	// with no existence disclosure (mirrors GetAgentRun's not-found handling).
	found, err := s.store.UpdateAgentRunStatus(ctx, org, runID, req.Status, req.FindingsCount)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "temporarily unavailable"})
		return
	}
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
		return
	}

	// Fold the same transition onto the linked monitor_events row (Req 7.2).
	// Org-scoped and keyed by run_id; a manual run with no linked event matches
	// nothing, which is a harmless no-op.
	if err := s.store.UpdateMonitorEventStatus(ctx, org, runID, req.Status); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "temporarily unavailable"})
		return
	}

	c.JSON(http.StatusOK, RunStatus{
		RunID:         runID,
		Status:        req.Status,
		FindingsCount: req.FindingsCount,
	})
}

// TraceWriteRequest is the body of POST /api/agent/runs/:id/trace: a batched
// set of agent lifecycle events plus handoffs the worker emits for a run. Like
// StatusWriteRequest it carries no org field — the tenant is resolved from the
// authenticated principal via mcpOrg, never from the body (tenant isolation, P1).
type TraceWriteRequest struct {
	Agents   []TraceAgentEvent `json:"agents"`
	Handoffs []TraceHandoff    `json:"handoffs"`
}

// TraceAgentEvent is one agent's lifecycle event in a trace batch. AgentName is
// mapped to a fixed pipeline Stage by the handler (via monitor.MapStage) before
// the leg is persisted — the store never maps names itself. Status carries the
// leg outcome; both "outcome" and "status" are accepted as its JSON key so the
// plane can emit either.
type TraceAgentEvent struct {
	AgentID   string     `json:"agent_id"`
	ParentID  string     `json:"parent_id"`
	AgentName string     `json:"agent_name"`
	Outcome   string     `json:"outcome"`
	Status    string     `json:"status"`
	StartedAt *time.Time `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at"`
}

// TraceHandoff is one handoff edge in a trace batch (Req 10.3). At defaults to
// now() at persist time when the plane omits it.
type TraceHandoff struct {
	FromAgentID string     `json:"from_agent_id"`
	ToAgentID   string     `json:"to_agent_id"`
	At          *time.Time `json:"at"`
}

// handleAgentRunTrace folds a worker's batched trace write-back onto the
// org-scoped run: each agent name is mapped to a fixed pipeline Stage (via
// monitor.MapStage — the mapping lives on the handler side because the store
// deliberately does not import internal/monitor), each leg is upserted
// idempotently per (run_id, agent_id), and each handoff is inserted. Afterward
// the run's agents_count (distinct (run_id, agent_id) legs) and handoffs_count
// are folded onto the row (Req 12.1).
//
// Tenant isolation (Req 12.3, 19.2): the run is first checked for existence in
// the caller's org via GetAgentRun. A run in another org (or a nonexistent run)
// returns the same 404 as a true miss with no existence disclosure, and because
// the check runs before any leg/handoff write nothing is persisted for a
// cross-org run. The store writes are themselves org-scoped as defense in depth.
//
// A rejected batch item (an agent with no id, or a handoff missing an endpoint)
// is skipped and the remaining items continue (Req 21.4). The write-back is
// best-effort on the plane's side (Req 12.2); this endpoint only persists.
func (s *Server) handleAgentRunTrace(c *gin.Context) {
	org, ok := mcpOrg(c) // org from principal, NEVER from body (P1)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	var req TraceWriteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	runID := c.Param("id")

	// Validate the run belongs to the caller's org up front so a cross-org (or
	// nonexistent) run persists nothing and returns a no-disclosure 404 before
	// any leg/handoff is written (Req 12.3).
	if _, err := s.store.GetAgentRun(ctx, org, runID); err != nil {
		if errors.Is(err, store.ErrAgentRunNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
			return
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "temporarily unavailable"})
		return
	}

	// Upsert each agent leg, mapping its name to a stage. Skip items with no
	// agent id (a leg must be keyed by (run_id, agent_id)); continue past them
	// so one bad item never drops the rest of the batch (Req 21.4). Distinct
	// agent ids give the agents_count.
	seenAgents := make(map[string]struct{})
	for _, a := range req.Agents {
		if a.AgentID == "" {
			continue // rejected item: no idempotency key; keep processing (Req 21.4)
		}
		outcome := a.Outcome
		if outcome == "" {
			outcome = a.Status
		}
		stage := string(monitor.MapStage(a.AgentName)) // name->stage on the handler side
		if err := s.store.UpsertRunAgent(ctx, org, store.RunAgent{
			RunID:     runID,
			AgentID:   a.AgentID,
			AgentName: a.AgentName,
			ParentID:  a.ParentID,
			Stage:     stage,
			Outcome:   outcome,
			StartedAt: a.StartedAt,
			EndedAt:   a.EndedAt,
		}); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "temporarily unavailable"})
			return
		}
		seenAgents[a.AgentID] = struct{}{}
	}

	// Insert each handoff. Skip items missing an endpoint and continue past them
	// (Req 21.4). Count every accepted handoff for handoffs_count.
	handoffCount := 0
	for _, h := range req.Handoffs {
		if h.FromAgentID == "" || h.ToAgentID == "" {
			continue // rejected item: incomplete edge; keep processing (Req 21.4)
		}
		at := time.Now()
		if h.At != nil {
			at = *h.At
		}
		if err := s.store.InsertRunHandoff(ctx, org, store.RunHandoff{
			RunID:       runID,
			FromAgentID: h.FromAgentID,
			ToAgentID:   h.ToAgentID,
			At:          at,
		}); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "temporarily unavailable"})
			return
		}
		handoffCount++
	}

	// Fold the counts onto the run: agents_count is the number of distinct
	// (run_id, agent_id) legs in this batch, handoffs_count the accepted handoffs
	// (Req 12.1).
	if _, err := s.store.SetAgentRunTraceCounts(ctx, org, runID, len(seenAgents), handoffCount); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "temporarily unavailable"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"run_id":         runID,
		"agents_count":   len(seenAgents),
		"handoffs_count": handoffCount,
	})
}

// RunDetailResponse is the response of GET /api/agent/runs/:id/detail: the run
// header (Req 13.1) plus the per-stage stage-flow (Req 13.3).
type RunDetailResponse struct {
	RunID         string            `json:"run_id"`
	Status        string            `json:"status"`
	Target        RunTargetRef      `json:"target"`
	ScopeRules    int               `json:"scope_rules"` // len(authz_scope.allow_targets)
	TriggeredBy   string            `json:"triggered_by"`
	StartedAt     *time.Time        `json:"started_at"`
	EndedAt       *time.Time        `json:"ended_at"`
	DurationMS    *int64            `json:"duration_ms"` // null when ended_at absent (Req 13.2)
	AgentsCount   int               `json:"agents_count"`
	HandoffsCount int               `json:"handoffs_count"`
	StageFlow     []store.StageLegs `json:"stage_flow"`
}

// handleAgentRunDetail returns the run-detail header and the stage-flow for a
// run scoped to the caller's org. The header carries the run id, status, target
// with its scope-rule count (len(authz_scope.allow_targets)), triggered_by, the
// lifecycle timestamps, the duration, and the trace counts (Req 13.1). Duration
// is ended_at − started_at when both are present and null when ended_at is
// absent (Req 13.2). The stage-flow lists, for each of the 11 pipeline stages,
// that stage's leg count and each leg's name/duration/outcome (Req 13.3).
//
// A run in another org (or a nonexistent run) returns the same 404 as a true
// miss with no existence disclosure (Req 13.5, 19.2): both GetAgentRunDetail and
// RunTrace are org-scoped and map a cross-org read to ErrAgentRunNotFound.
func (s *Server) handleAgentRunDetail(c *gin.Context) {
	org, ok := mcpOrg(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	runID := c.Param("id")

	d, err := s.store.GetAgentRunDetail(ctx, org, runID)
	if err != nil {
		if errors.Is(err, store.ErrAgentRunNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
			return
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "temporarily unavailable"})
		return
	}

	trace, err := s.store.RunTrace(ctx, org, runID)
	if err != nil {
		if errors.Is(err, store.ErrAgentRunNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
			return
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "temporarily unavailable"})
		return
	}

	// Duration is defined only when both endpoints are present (Req 13.2);
	// otherwise it is reported as null.
	var durationMS *int64
	if d.StartedAt != nil && d.EndedAt != nil {
		ms := d.EndedAt.Sub(*d.StartedAt).Milliseconds()
		durationMS = &ms
	}

	// scope-rule count is len(authz_scope.allow_targets); a nil scope (read-only
	// run) has zero rules.
	scopeRules := 0
	if d.Scope != nil {
		scopeRules = len(d.Scope.AllowTargets)
	}

	c.JSON(http.StatusOK, RunDetailResponse{
		RunID:  d.RunID,
		Status: d.Status,
		Target: RunTargetRef{
			Kind:        d.Target.Kind,
			CanonicalID: d.Target.CanonicalID,
			Raw:         d.Target.Raw,
		},
		ScopeRules:    scopeRules,
		TriggeredBy:   d.TriggeredBy,
		StartedAt:     d.StartedAt,
		EndedAt:       d.EndedAt,
		DurationMS:    durationMS,
		AgentsCount:   d.AgentsCount,
		HandoffsCount: d.HandoffsCount,
		StageFlow:     trace.Stages,
	})
}

// handleAgentRunTimeline returns the run's agent-start and handoff events as a
// single list ordered ascending by timestamp, ties broken deterministically by
// agent id (Req 13.4). A run in another org (or a nonexistent run) returns the
// same 404 as a true miss with no existence disclosure (Req 13.5, 19.2):
// RunTimeline is org-scoped and maps a cross-org read to ErrAgentRunNotFound.
func (s *Server) handleAgentRunTimeline(c *gin.Context) {
	org, ok := mcpOrg(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	events, err := s.store.RunTimeline(ctx, org, c.Param("id"))
	if err != nil {
		if errors.Is(err, store.ErrAgentRunNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
			return
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "temporarily unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"events": events})
}

// launchToInput maps the validated HTTP launch request onto the store-local
// input shape. Keeping the mapping here lets the store stay free of any
// dependency on the server package's request types.
func launchToInput(req LaunchRequest) store.AgentRunInput {
	in := store.AgentRunInput{
		Target: store.AgentRunTarget{
			Kind:        req.Target.Kind,
			CanonicalID: req.Target.CanonicalID,
			Raw:         req.Target.Raw,
		},
	}
	if req.AuthzScope != nil {
		in.Scope = &store.AgentRunScope{
			ActiveTesting: req.AuthzScope.ActiveTesting,
			AllowTargets:  req.AuthzScope.AllowTargets,
		}
	}
	return in
}
