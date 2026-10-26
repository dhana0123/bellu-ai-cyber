package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// tracePipeline is the fixed, ordered 11-stage pipeline vocabulary the run
// trace view renders, plus the trailing "other" bucket for unmapped agent
// names. It is defined locally in the store ON PURPOSE: the authoritative
// vocabulary and the agent-name -> stage mapper live in internal/monitor
// (internal/monitor/stages.go), but the store must NOT import internal/monitor
// — the store persists whatever stage string the caller (the trace write-back
// handler) has already mapped, and the mapping belongs on the handler side.
// Keeping the list here avoids a cross-package dependency and the task-ordering
// coupling that importing internal/monitor would create. The 11 ordered stages
// and the "other" bucket mirror the run_agents stage CHECK constraint and the
// monitor.Pipeline order (Req 11.1).
var tracePipeline = []string{
	"RECON",
	"POSTURE",
	"DISCOVERY",
	"EXPLOIT",
	"SWEEP",
	"VERIFY",
	"STABILITY",
	"SCORE",
	"FORMAT",
	"COVERAGE",
	"REPORT",
}

// StageOther is the bucket a run agent lands in when its name mapped to no
// known stage. The store never renders "other" as one of the 11 pipeline
// stages (Req 11.5 renders exactly the 11), but it accepts and persists the
// value so a leg is never dropped (Req 11.4).
const StageOther = "other"

// RunAgent is one agent instance ("leg") emitted for a run. A leg is keyed by
// (run_id, agent_id); re-emitting the same agent refreshes it in place rather
// than inserting a second row (Req 10.2). Stage is the already-mapped stage
// string the caller supplies (one of the 11 pipeline stages or "other"); the
// store persists it as given and never maps names itself.
type RunAgent struct {
	RunID     string
	AgentID   string
	AgentName string
	ParentID  string // "" when the agent is a root (nullable parent_id)
	Stage     string // one of the 11 pipeline stages or "other"
	Outcome   string // status/error text
	StartedAt *time.Time
	EndedAt   *time.Time
}

// RunHandoff is one recorded handoff edge between two agents in a run: its
// source agent id, destination agent id, and the handoff timestamp (Req 10.3).
type RunHandoff struct {
	RunID       string
	FromAgentID string
	ToAgentID   string
	At          time.Time
}

// RunTraceView is the per-stage stage-flow for a run. It always carries exactly
// one StageLegs per fixed pipeline stage, in pipeline order, so the view is
// stable across runs (Req 11.5). LegCount may be 0 for a stage with no legs.
type RunTraceView struct {
	Stages []StageLegs `json:"stages"`
}

// StageLegs is one stage of the stage-flow: the stage name, the number of legs
// mapped to it (Req 11.5), and the per-leg detail for each leg in the stage.
type StageLegs struct {
	Stage    string        `json:"stage"`
	LegCount int           `json:"leg_count"`
	Legs     []RunAgentLeg `json:"legs"`
}

// RunAgentLeg is the per-agent detail the stage-flow carries for each leg:
// the agent name, the leg duration, and the leg outcome (Req 13.3). Duration is
// computed from started_at/ended_at: DurationMS is nil (serialized as null)
// when either timestamp is absent, mirroring the run-detail duration rule
// (Req 13.2). Duration carries the same value as a time.Duration for Go callers.
type RunAgentLeg struct {
	AgentName  string        `json:"agent_name"`
	DurationMS *int64        `json:"duration_ms"`
	Duration   time.Duration `json:"-"`
	Outcome    string        `json:"outcome"`
}

// TimelineEventKind distinguishes the two kinds of run timeline events: an
// agent starting and a handoff between two agents (Req 13.4).
const (
	TimelineAgentStart = "agent_start"
	TimelineHandoff    = "handoff"
)

// RunTimelineEvent is one event on the run timeline: either an agent-start
// (Kind == "agent_start", AgentID/AgentName set) or a handoff (Kind ==
// "handoff", FromAgentID/ToAgentID set). At is the event timestamp the list is
// ordered by. AgentID also carries the deterministic tie-break key: for an
// agent-start it is the starting agent's id, and for a handoff it is the source
// agent's id, so ties at the same timestamp break deterministically by agent id
// (Req 13.4).
type RunTimelineEvent struct {
	Kind        string    `json:"kind"`
	At          time.Time `json:"at"`
	AgentID     string    `json:"agent_id,omitempty"`
	AgentName   string    `json:"agent_name,omitempty"`
	FromAgentID string    `json:"from_agent_id,omitempty"`
	ToAgentID   string    `json:"to_agent_id,omitempty"`
}

// RunTimeline returns the run's agent-start and handoff events as a single list
// ordered ascending by event timestamp, with ties broken deterministically by
// agent id (Req 13.4). Agent-start events come from run_agents.started_at (a leg
// with no start time is omitted from the timeline — it has no start event yet);
// handoff events come from run_handoffs.at.
//
// Like RunTrace it is org-scoped: a run that does not exist OR that belongs to
// another org resolves to ErrAgentRunNotFound so the handler returns a 404 with
// no existence disclosure (tenant isolation / P1, Req 13.5, 19.2).
func (s *Store) RunTimeline(ctx context.Context, org int64, runID string) ([]RunTimelineEvent, error) {
	// Existence + ownership check first: a run outside the caller's org (or a
	// nonexistent run) is a 404 with no disclosure, before any event is read.
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT true FROM agent_runs WHERE org_id = $1 AND run_id = $2`,
		org, runID).Scan(&exists)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAgentRunNotFound
		}
		return nil, fmt.Errorf("store: run timeline: %w", err)
	}

	// Agent-start and handoff events are unioned and ordered together in SQL so
	// the single ordered list (ascending by timestamp, ties by agent id) comes
	// straight from the database (Req 13.4). The tie-break key is the starting
	// agent id for a start and the source agent id for a handoff.
	rows, err := s.pool.Query(ctx,
		`SELECT kind, at, agent_id, agent_name, from_agent_id, to_agent_id
		   FROM (
		        SELECT 'agent_start'::text AS kind,
		               started_at           AS at,
		               agent_id             AS agent_id,
		               agent_name           AS agent_name,
		               ''                   AS from_agent_id,
		               ''                   AS to_agent_id
		          FROM run_agents
		         WHERE run_id = $1 AND started_at IS NOT NULL
		        UNION ALL
		        SELECT 'handoff'::text      AS kind,
		               at                   AS at,
		               from_agent_id        AS agent_id,
		               ''                   AS agent_name,
		               from_agent_id        AS from_agent_id,
		               to_agent_id          AS to_agent_id
		          FROM run_handoffs
		         WHERE run_id = $1
		   ) AS events
		  ORDER BY at, agent_id`,
		runID)
	if err != nil {
		return nil, fmt.Errorf("store: run timeline: %w", err)
	}
	defer rows.Close()

	events := make([]RunTimelineEvent, 0)
	for rows.Next() {
		var e RunTimelineEvent
		if err := rows.Scan(&e.Kind, &e.At, &e.AgentID, &e.AgentName, &e.FromAgentID, &e.ToAgentID); err != nil {
			return nil, fmt.Errorf("store: run timeline: scan: %w", err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: run timeline: %w", err)
	}
	return events, nil
}

// UpsertRunAgent records one agent leg for a run, idempotent per
// (run_id, agent_id): a first emit inserts the leg, and a re-emit for the same
// (run_id, agent_id) updates the existing row in place — refreshing its
// outcome, end time, stage, name, and parent — and never inserts a second row
// (Req 10.2).
//
// The write is org-scoped through agent_runs: the INSERT ... SELECT only
// produces a row when the target run_id belongs to org, so a leg can never be
// attached to another tenant's run (tenant isolation / P1, Req 19.2). A run in
// another org (or a nonexistent run) matches no row in the SELECT, so nothing
// is inserted or updated — the call is a silent no-op with no existence
// disclosure, matching GetAgentRun's no-disclosure approach.
func (s *Store) UpsertRunAgent(ctx context.Context, org int64, a RunAgent) error {
	var parentArg any
	if a.ParentID != "" {
		parentArg = a.ParentID
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO run_agents
		     (run_id, agent_id, agent_name, parent_id, stage, outcome, started_at, ended_at)
		 SELECT $2, $3, $4, $5, $6, $7, $8, $9
		   FROM agent_runs
		  WHERE agent_runs.run_id = $2 AND agent_runs.org_id = $1
		 ON CONFLICT (run_id, agent_id) DO UPDATE
		    SET agent_name = EXCLUDED.agent_name,
		        parent_id  = EXCLUDED.parent_id,
		        stage      = EXCLUDED.stage,
		        outcome    = EXCLUDED.outcome,
		        started_at = EXCLUDED.started_at,
		        ended_at   = EXCLUDED.ended_at`,
		org, a.RunID, a.AgentID, a.AgentName, parentArg, a.Stage, a.Outcome,
		a.StartedAt, a.EndedAt)
	if err != nil {
		return fmt.Errorf("store: upsert run agent: %w", err)
	}
	return nil
}

// InsertRunHandoff records one handoff edge for a run, storing its source agent
// id, destination agent id, and handoff timestamp (Req 10.3). Like
// UpsertRunAgent it is org-scoped through agent_runs via an INSERT ... SELECT,
// so a handoff can only attach to the caller's own run; a run in another org
// matches no row and nothing is inserted (tenant isolation / P1, Req 19.2).
func (s *Store) InsertRunHandoff(ctx context.Context, org int64, h RunHandoff) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO run_handoffs (run_id, from_agent_id, to_agent_id, at)
		 SELECT $2, $3, $4, $5
		   FROM agent_runs
		  WHERE agent_runs.run_id = $2 AND agent_runs.org_id = $1`,
		org, h.RunID, h.FromAgentID, h.ToAgentID, h.At)
	if err != nil {
		return fmt.Errorf("store: insert run handoff: %w", err)
	}
	return nil
}

// RunTrace returns the per-stage stage-flow for a run, scoped by org. A run that
// does not exist, OR that belongs to another org, resolves to
// ErrAgentRunNotFound: the org_id predicate on agent_runs makes a cross-tenant
// read indistinguishable from a miss, so the handler can map both to a 404
// without disclosing existence (tenant isolation / P1, Req 19.2), matching
// GetAgentRun.
//
// The returned view always carries exactly one StageLegs per fixed pipeline
// stage, in pipeline order, even when a stage has no legs (LegCount 0) — the
// stage-flow is stable across runs (Req 11.5). run_agents rows are grouped by
// their persisted stage; any leg whose stage is "other" (unmapped at write
// time) is not surfaced as one of the 11 pipeline stages but was still
// persisted, so no leg is lost (Req 11.4).
func (s *Store) RunTrace(ctx context.Context, org int64, runID string) (RunTraceView, error) {
	// Existence + ownership check first: a run outside the caller's org (or a
	// nonexistent run) is a 404 with no disclosure, before any leg is read.
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT true FROM agent_runs WHERE org_id = $1 AND run_id = $2`,
		org, runID).Scan(&exists)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RunTraceView{}, ErrAgentRunNotFound
		}
		return RunTraceView{}, fmt.Errorf("store: run trace: %w", err)
	}

	rows, err := s.pool.Query(ctx,
		`SELECT stage, agent_name, outcome, started_at, ended_at
		   FROM run_agents
		  WHERE run_id = $1
		  ORDER BY started_at NULLS LAST, agent_id`,
		runID)
	if err != nil {
		return RunTraceView{}, fmt.Errorf("store: run trace: %w", err)
	}
	defer rows.Close()

	// Group legs by their persisted stage string.
	byStage := make(map[string][]RunAgentLeg)
	for rows.Next() {
		var (
			stage     string
			leg       RunAgentLeg
			startedAt *time.Time
			endedAt   *time.Time
		)
		if err := rows.Scan(&stage, &leg.AgentName, &leg.Outcome, &startedAt, &endedAt); err != nil {
			return RunTraceView{}, fmt.Errorf("store: run trace: scan: %w", err)
		}
		// Duration is defined only when both endpoints are present (Req 13.2);
		// otherwise it is reported as null.
		if startedAt != nil && endedAt != nil {
			d := endedAt.Sub(*startedAt)
			leg.Duration = d
			ms := d.Milliseconds()
			leg.DurationMS = &ms
		}
		byStage[stage] = append(byStage[stage], leg)
	}
	if err := rows.Err(); err != nil {
		return RunTraceView{}, fmt.Errorf("store: run trace: %w", err)
	}

	// Emit exactly one StageLegs per fixed pipeline stage, in order, so the
	// stage-flow is stable; empty stages report a LegCount of 0 (Req 11.5).
	view := RunTraceView{Stages: make([]StageLegs, 0, len(tracePipeline))}
	for _, stage := range tracePipeline {
		legs := byStage[stage]
		view.Stages = append(view.Stages, StageLegs{
			Stage:    stage,
			LegCount: len(legs),
			Legs:     legs,
		})
	}
	return view, nil
}
