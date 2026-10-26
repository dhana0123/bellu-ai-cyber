package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrAgentRunNotFound is returned by GetAgentRun when no run with the given id
// exists FOR THE CALLER'S ORG. A run that belongs to another org resolves to
// this same error so a caller can never distinguish "does not exist" from
// "exists for another tenant" (tenant isolation / no existence disclosure).
var ErrAgentRunNotFound = errors.New("store: agent run not found")

// AgentRunTarget is the persisted scan target: either a graph entity (by
// canonical id) or an operator-supplied raw target (URL/IP). It is stored as
// JSONB on the run row. The server package maps its own target type onto this
// store-local shape so the store never imports the server package.
type AgentRunTarget struct {
	Kind        string `json:"kind"`                   // "entity" | "raw"
	CanonicalID string `json:"canonical_id,omitempty"` // set when Kind == "entity"
	Raw         string `json:"raw,omitempty"`          // set when Kind == "raw"
}

// AgentRunScope is the persisted active-testing authorization scope. A nil
// scope (or ActiveTesting=false) means the run is read-only graph reasoning.
type AgentRunScope struct {
	ActiveTesting bool     `json:"active_testing"`
	AllowTargets  []string `json:"allow_targets,omitempty"`
}

// AgentRunInput is the org-scoped request to launch a run. The server handler
// builds it from the validated LaunchRequest; org is supplied separately and is
// resolved from the authenticated principal, never from this input.
type AgentRunInput struct {
	Target AgentRunTarget
	Scope  *AgentRunScope
	// TriggeredBy records which monitor_event launched the run. It is empty for
	// operator launches (which fall back to the column default 'manual') and set
	// to the monitor_events id for auto-triggered claim launches.
	TriggeredBy string
}

// AgentRunStatus is the persisted status view returned to the caller.
type AgentRunStatus struct {
	RunID         string `json:"run_id"`
	Status        string `json:"status"` // queued|running|completed|failed
	FindingsCount int    `json:"findings_count"`
	ActiveTesting bool   `json:"active_testing"` // whether this run was authorized for it
}

// CreateAgentRun persists a new org-scoped run row in the queued state and
// returns its server-generated run id. The id is never taken from client input.
func (s *Store) CreateAgentRun(ctx context.Context, org int64, in AgentRunInput) (string, error) {
	runID, err := newRunID()
	if err != nil {
		return "", fmt.Errorf("store: create agent run: %w", err)
	}

	targetJSON, err := json.Marshal(in.Target)
	if err != nil {
		return "", fmt.Errorf("store: create agent run: marshal target: %w", err)
	}

	var scopeJSON []byte
	activeTesting := false
	if in.Scope != nil {
		activeTesting = in.Scope.ActiveTesting
		scopeJSON, err = json.Marshal(in.Scope)
		if err != nil {
			return "", fmt.Errorf("store: create agent run: marshal scope: %w", err)
		}
	}

	// triggered_by records the launching monitor_event; an empty TriggeredBy
	// falls back to the column default 'manual' (migration 00020) so operator
	// launches stay 'manual' and auto-triggered claims stamp the event id.
	triggeredBy := "manual"
	if in.TriggeredBy != "" {
		triggeredBy = in.TriggeredBy
	}

	_, err = s.pool.Exec(ctx,
		`INSERT INTO agent_runs (run_id, org_id, target, authz_scope, active_testing, status, triggered_by)
		 VALUES ($1, $2, $3, $4, $5, 'queued', $6)`,
		runID, org, targetJSON, scopeJSON, activeTesting, triggeredBy)
	if err != nil {
		return "", fmt.Errorf("store: create agent run: %w", err)
	}
	return runID, nil
}

// GetAgentRun returns the status of a run scoped to org. A run that does not
// exist, OR that belongs to another org, resolves to ErrAgentRunNotFound: the
// org_id predicate makes a cross-tenant read indistinguishable from a miss, so
// the handler can map both to a 404 without disclosing existence.
func (s *Store) GetAgentRun(ctx context.Context, org int64, runID string) (AgentRunStatus, error) {
	var st AgentRunStatus
	err := s.pool.QueryRow(ctx,
		`SELECT run_id, status, findings_count, active_testing
		 FROM agent_runs
		 WHERE org_id = $1 AND run_id = $2`,
		org, runID).Scan(&st.RunID, &st.Status, &st.FindingsCount, &st.ActiveTesting)
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentRunStatus{}, ErrAgentRunNotFound
	}
	if err != nil {
		return AgentRunStatus{}, fmt.Errorf("store: get agent run: %w", err)
	}
	return st, nil
}

// AgentRunDetail is the org-scoped run-detail header the run detail view
// renders (Req 13.1). It carries the lifecycle timestamps and the trace counts
// folded on by the trace write-back, plus the persisted Target and Scope so the
// handler can surface the target and its scope-rule count (len(AllowTargets)).
//
// StartedAt/EndedAt are nullable: a run that has not started reports a nil
// StartedAt, and one that has not ended reports a nil EndedAt — the handler
// reports duration as null when EndedAt is absent (Req 13.2).
type AgentRunDetail struct {
	RunID         string
	Status        string
	Target        AgentRunTarget
	Scope         *AgentRunScope // nil => read-only; AllowTargets gives the scope-rule count
	TriggeredBy   string
	StartedAt     *time.Time
	EndedAt       *time.Time
	AgentsCount   int
	HandoffsCount int
}

// GetAgentRunDetail returns the run-detail header fields for a run scoped to
// org (Req 13.1). Like GetAgentRun, a run that does not exist OR that belongs to
// another org resolves to ErrAgentRunNotFound: the org_id predicate makes a
// cross-tenant read indistinguishable from a miss, so the handler maps both to
// a 404 with no existence disclosure (tenant isolation / P1, Req 13.5, 19.2).
func (s *Store) GetAgentRunDetail(ctx context.Context, org int64, runID string) (AgentRunDetail, error) {
	var (
		d          AgentRunDetail
		targetJSON []byte
		scopeJSON  []byte
	)
	err := s.pool.QueryRow(ctx,
		`SELECT run_id, status, target, authz_scope, triggered_by,
		        started_at, ended_at, agents_count, handoffs_count
		   FROM agent_runs
		  WHERE org_id = $1 AND run_id = $2`,
		org, runID).Scan(&d.RunID, &d.Status, &targetJSON, &scopeJSON, &d.TriggeredBy,
		&d.StartedAt, &d.EndedAt, &d.AgentsCount, &d.HandoffsCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentRunDetail{}, ErrAgentRunNotFound
	}
	if err != nil {
		return AgentRunDetail{}, fmt.Errorf("store: get agent run detail: %w", err)
	}
	if len(targetJSON) > 0 {
		if err := json.Unmarshal(targetJSON, &d.Target); err != nil {
			return AgentRunDetail{}, fmt.Errorf("store: get agent run detail: unmarshal target: %w", err)
		}
	}
	// authz_scope is nullable: a NULL scope means read-only (no scope rules).
	if len(scopeJSON) > 0 {
		var sc AgentRunScope
		if err := json.Unmarshal(scopeJSON, &sc); err != nil {
			return AgentRunDetail{}, fmt.Errorf("store: get agent run detail: unmarshal scope: %w", err)
		}
		d.Scope = &sc
	}
	return d, nil
}

// SetAgentRunTraceCounts folds the trace-derived agents_count and
// handoffs_count onto the org-scoped agent_runs row after a trace write-back
// (Req 12.1). It is org-scoped: a run belonging to another org matches no row,
// so the update is a no-op and the returned bool is false, letting the handler
// return a 404 with no existence disclosure (tenant isolation / P1, Req 19.2).
func (s *Store) SetAgentRunTraceCounts(ctx context.Context, org int64, runID string, agents, handoffs int) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE agent_runs
		    SET agents_count = $3, handoffs_count = $4
		  WHERE org_id = $1 AND run_id = $2`,
		org, runID, agents, handoffs)
	if err != nil {
		return false, fmt.Errorf("store: set agent run trace counts: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// UpdateAgentRunStatus folds a reported status and findings count onto the
// org-scoped agent_runs row (Req 7.1). The org_id predicate scopes the write to
// the caller's tenant: a run belonging to another org matches no row, so the
// update is a no-op and the handler maps it to a 404 without disclosing the
// run's existence across orgs (tenant isolation / no existence disclosure).
//
// Lifecycle timestamps are stamped from the transition, not supplied by the
// caller: started_at is set the first time the run enters 'running' (COALESCE
// keeps an already-set started_at stable on re-report), and ended_at is set on
// the terminal 'completed'/'failed' states and otherwise left untouched. The
// returned bool is false when no row was updated (run not found for this org).
func (s *Store) UpdateAgentRunStatus(ctx context.Context, org int64, runID, status string, findingsCount int) (bool, error) {
	// started_at: first entry into 'running' stamps now(); COALESCE preserves an
	// earlier start on re-report. ended_at: stamped on terminal states, cleared
	// back to NULL on a (re)transition to 'running'.
	tag, err := s.pool.Exec(ctx,
		`UPDATE agent_runs
		    SET status = $3,
		        findings_count = $4,
		        started_at = CASE WHEN $3 = 'running'
		                          THEN COALESCE(started_at, now())
		                          ELSE started_at END,
		        ended_at = CASE WHEN $3 IN ('completed','failed') THEN now()
		                        WHEN $3 = 'running' THEN NULL
		                        ELSE ended_at END
		  WHERE org_id = $1 AND run_id = $2`,
		org, runID, status, findingsCount)
	if err != nil {
		return false, fmt.Errorf("store: update agent run status: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// newRunID returns an opaque, hard-to-guess run id. It is generated
// server-side so a client can never choose or predict another run's id.
func newRunID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "run_" + hex.EncodeToString(b[:]), nil
}
