// Types for the Mission Control (continuous-monitoring-agents) feature. These
// mirror the Go control-plane response shapes exactly (see
// backend/server/monitor_handlers.go and agent_runs_handlers.go, and
// internal/store/run_trace.go). Field names use snake_case to match the server
// serialization — do not "normalize" them; the client reads what the server
// sends.

// One monitor event in the Mission Control grid.
// Source: monitorEventView in backend/server/monitor_handlers.go.
export interface MonitorEvent {
  id: number
  source: string
  delivery_id: string
  event_kind: string
  /** Raw source-specific payload passed through verbatim. */
  payload: unknown
  /** Routed team, omitted when the event was skipped/unrouted. */
  team?: string
  /** pending | routed | skipped | claimed | running | completed | failed */
  state: string
  /** Linked agent run id, present once an event has been claimed. */
  run_id?: string
  created_at: string
}

// The scan target of a run: either a graph entity or an operator-supplied raw
// target. Source: RunTargetRef in agent_runs_handlers.go.
export interface RunTargetRef {
  kind: string // "entity" | "raw"
  canonical_id?: string
  raw?: string
}

// One agent leg within a stage of the stage-flow.
// Source: RunAgentLeg in internal/store/run_trace.go.
export interface RunAgentLeg {
  agent_name: string
  /** Leg duration; null when either timestamp is absent. */
  duration_ms: number | null
  outcome: string
}

// One pipeline stage of the stage-flow with its legs.
// Source: StageLegs in internal/store/run_trace.go.
export interface StageLegs {
  stage: string
  leg_count: number
  legs: RunAgentLeg[]
}

// The run-detail response: header fields plus the per-stage stage-flow.
// Source: RunDetailResponse in agent_runs_handlers.go.
export interface RunDetail {
  run_id: string
  status: string // queued | running | completed | failed
  target: RunTargetRef
  /** len(authz_scope.allow_targets); 0 for a read-only run. */
  scope_rules: number
  triggered_by: string // "manual" or a monitor_events id
  started_at: string | null
  ended_at: string | null
  /** ended_at − started_at in ms; null when ended_at is absent. */
  duration_ms: number | null
  agents_count: number
  handoffs_count: number
  stage_flow: StageLegs[]
}

// One event on a run timeline: an agent start or a handoff.
// Source: RunTimelineEvent in internal/store/run_trace.go.
export interface RunTimelineEvent {
  kind: string // "agent_start" | "handoff"
  at: string
  agent_id?: string
  agent_name?: string
  from_agent_id?: string
  to_agent_id?: string
}
