package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

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
}

// AgentRunStatus is the persisted status view returned to the caller.
type AgentRunStatus struct {
	RunID         string `json:"run_id"`
	Status        string `json:"status"`         // queued|running|completed|failed
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

	_, err = s.pool.Exec(ctx,
		`INSERT INTO agent_runs (run_id, org_id, target, authz_scope, active_testing, status)
		 VALUES ($1, $2, $3, $4, $5, 'queued')`,
		runID, org, targetJSON, scopeJSON, activeTesting)
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

// newRunID returns an opaque, hard-to-guess run id. It is generated
// server-side so a client can never choose or predict another run's id.
func newRunID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "run_" + hex.EncodeToString(b[:]), nil
}
