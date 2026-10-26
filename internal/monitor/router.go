// Package monitor holds the Go control-plane building blocks for continuous,
// event-triggered monitoring: classification of incoming signals into a
// canonical event kind and the static routing of that kind to an agent team.
//
// This package is deliberately self-contained — it reads raw payloads and maps
// strings, and does not depend on the store or any transport layer — so it can
// be exercised in isolation and reused from every ingest seam.
package monitor

import (
	"encoding/json"
	"errors"
)

// Team is the agent team a routed monitor event targets.
type Team string

const (
	// TeamRedTeam handles CI signals (offensive follow-up on build/test results).
	TeamRedTeam Team = "red_team"
	// TeamBlueTeam handles newly-opened vulnerabilities (defensive triage).
	TeamBlueTeam Team = "blue_team"
	// TeamPRReviewer handles code changes (push / pull-request review).
	TeamPRReviewer Team = "pr_reviewer"
	// TeamTriage handles newly discovered resources (asset triage).
	TeamTriage Team = "triage"
)

// Canonical event kinds. Classify derives exactly one of these, or none.
const (
	kindCI            = "ci"
	kindVulnerability = "vulnerability"
	kindCodeChange    = "code_change"
	kindResource      = "resource"
)

// Event sources. Classify is the only reader of source-specific payload shapes.
const (
	sourceGitHub    = "github"
	sourceCorrelate = "correlate"
	sourceDiscovery = "discovery"
	sourceSweep     = "sweep"
)

// routingTable is the STATIC built-in mapping from event kind to team:
// ci -> red_team, vulnerability -> blue_team, code_change -> pr_reviewer,
// resource -> triage. Any kind absent from the table is unroutable and routes
// to "" (which the store folds into the terminal 'skipped' state).
var routingTable = map[string]Team{
	kindCI:            TeamRedTeam,
	kindVulnerability: TeamBlueTeam,
	kindCodeChange:    TeamPRReviewer,
	kindResource:      TeamTriage,
}

// Route returns the team for an event kind, or "" when the kind is unknown or
// unroutable (the caller then moves the event to the terminal 'skipped' state
// and launches no run). Route is a pure lookup over the static routing table.
func Route(eventKind string) Team { return routingTable[eventKind] }

// ErrUnclassifiable is returned by Classify when a payload cannot be mapped to
// one of the canonical event kinds. The ingest seam turns this into a 400 and
// persists no row.
var ErrUnclassifiable = errors.New("monitor: unclassifiable event")

// Classify derives the canonical event_kind from a source and a raw payload. It
// is the ONLY place that reads source-specific payload shapes, so the router
// (Route) stays a static string->team lookup.
//
// The returned kind is exactly one of "ci", "vulnerability", "code_change", or
// "resource". When the payload cannot be classified, Classify returns an empty
// kind and ErrUnclassifiable (wrapped), and the ingest seam persists no row.
//
//   - github:    distinguishes "ci" (check-run / workflow events) from
//     "code_change" (push / pull-request events) by inspecting the payload.
//   - correlate: always "vulnerability" (the finding-delta tap).
//   - discovery: always "resource" (the discovery-completion tap).
//
// The "sweep" source re-runs correlation and emits via the correlate tap, so a
// bare sweep payload carries no classifiable signal of its own.
func Classify(source string, payload json.RawMessage) (kind string, err error) {
	switch source {
	case sourceCorrelate:
		return kindVulnerability, nil
	case sourceDiscovery:
		return kindResource, nil
	case sourceGitHub:
		return classifyGitHub(payload)
	default:
		return "", wrapUnclassifiable(source)
	}
}

// classifyGitHub inspects a GitHub webhook payload and returns "ci" for
// check-run / workflow events and "code_change" for push / pull-request events.
//
// GitHub carries the delivered event in the X-GitHub-Event header, not the body,
// so taps forward it into the payload under an "event_type" (or "event") field;
// classifyGitHub honors that discriminator first. When it is absent, the
// function falls back to the payload's structural shape (the top-level object a
// given event type carries), so classification is robust to either form.
func classifyGitHub(payload json.RawMessage) (string, error) {
	var p struct {
		EventType   string          `json:"event_type"`
		Event       string          `json:"event"`
		CheckRun    json.RawMessage `json:"check_run"`
		CheckSuite  json.RawMessage `json:"check_suite"`
		Workflow    json.RawMessage `json:"workflow"`
		WorkflowRun json.RawMessage `json:"workflow_run"`
		WorkflowJob json.RawMessage `json:"workflow_job"`
		PullRequest json.RawMessage `json:"pull_request"`
		Ref         *string         `json:"ref"`
		Commits     json.RawMessage `json:"commits"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return "", wrapUnclassifiable(sourceGitHub)
	}

	// Explicit event-type discriminator (forwarded X-GitHub-Event) takes priority.
	switch evt := p.EventType; {
	case evt == "":
		// no explicit discriminator; fall through to the "event" alias / shape
	case isCIEvent(evt):
		return kindCI, nil
	case isCodeChangeEvent(evt):
		return kindCodeChange, nil
	}
	if isCIEvent(p.Event) {
		return kindCI, nil
	}
	if isCodeChangeEvent(p.Event) {
		return kindCodeChange, nil
	}

	// Structural fallback: match on the top-level object the event type carries.
	switch {
	case len(p.CheckRun) > 0 || len(p.CheckSuite) > 0 ||
		len(p.Workflow) > 0 || len(p.WorkflowRun) > 0 || len(p.WorkflowJob) > 0:
		return kindCI, nil
	case len(p.PullRequest) > 0:
		return kindCodeChange, nil
	case p.Ref != nil || len(p.Commits) > 0:
		// A push carries a ref and a commits array and none of the above objects.
		return kindCodeChange, nil
	default:
		return "", wrapUnclassifiable(sourceGitHub)
	}
}

// isCIEvent reports whether a GitHub event type is a CI signal (check-run or
// workflow family).
func isCIEvent(event string) bool {
	switch event {
	case "check_run", "check_suite", "workflow_run", "workflow_job", "workflow_dispatch":
		return true
	default:
		return false
	}
}

// isCodeChangeEvent reports whether a GitHub event type is a code change (push
// or pull-request family).
func isCodeChangeEvent(event string) bool {
	switch event {
	case "push", "pull_request", "pull_request_review", "pull_request_target":
		return true
	default:
		return false
	}
}

// wrapUnclassifiable builds the error returned for a payload that cannot be
// classified, wrapping ErrUnclassifiable so callers can match it with errors.Is
// while still seeing which source failed.
func wrapUnclassifiable(source string) error {
	return &unclassifiableError{source: source}
}

type unclassifiableError struct{ source string }

func (e *unclassifiableError) Error() string {
	return "monitor: unclassifiable " + e.source + " event"
}

func (e *unclassifiableError) Unwrap() error { return ErrUnclassifiable }
