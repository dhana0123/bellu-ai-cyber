// Package report defines the Test_Runner result model: the PASS/FAIL outcome of
// running a scenario, together with diagnostics naming exactly which expected
// nodes, edges, or attack-path elements were absent, the reachability risk
// check, the phase a failure occurred in, and per-phase timings.
package report

import (
	"time"

	"github.com/bellu/bellu-ai-cyber/bellu-cloud-lab/internal/contract"
)

// Status is a scenario's terminal outcome.
type Status string

const (
	// StatusPass means every declared element was present with matching values.
	StatusPass Status = "PASS"
	// StatusFail means at least one declared element was missing/mismatched, or
	// a provisioning/pipeline/teardown error occurred.
	StatusFail Status = "FAIL"
)

// Phase names the stage a run reached (and, on failure, where it failed).
const (
	PhaseApply    = "apply"
	PhaseReady    = "ready"
	PhasePipeline = "pipeline"
	PhaseCompare  = "compare"
	PhaseDestroy  = "destroy"
)

// Failure reasons recorded in Result.FailureReason.
const (
	ReasonApply    = "apply"
	ReasonTimeout  = "timeout"
	ReasonPipeline = "pipeline"
	ReasonCompare  = "compare"
	ReasonDestroy  = "destroy"
	ReasonSBOM     = "sbom"
	ReasonContract = "contract"
)

// AttackPathDiag names which element of an attack-path chain was absent.
type AttackPathDiag struct {
	// Index is the position of the failing path in expected_attack_paths.
	Index int
	// MissingNode is the node reference that was absent (empty if an edge failed).
	MissingNode string
	// MissingEdge is the edge that was absent (zero if a node failed).
	MissingEdge contract.EdgeExpect
	// Detail is a human-readable description of what was missing.
	Detail string
}

// RiskDiag records the observed vs expected reachability risk relationship.
type RiskDiag struct {
	Expected float64 // expected reachable multiplier
	Observed float64 // observed reachable/unreachable score ratio
	OK       bool    // whether Observed matched Expected within tolerance
}

// Result is the full outcome of a scenario run.
type Result struct {
	Scenario string
	Status   Status

	// Diagnostics: what the comparator found missing.
	MissingNodes []contract.NodeExpect
	MissingEdges []contract.EdgeExpect
	MissingPaths []AttackPathDiag
	RiskCheck    *RiskDiag

	// Phase is the stage reached; FailureReason categorizes a failure.
	Phase         string
	FailureReason string
	// Err carries a human-readable error message when the run failed.
	Err string

	// Timings per phase (apply, ready, pipeline, compare, destroy).
	Timings map[string]time.Duration

	// ResourcesMayRemain is set when terraform destroy failed, so the caller
	// knows cloud resources might still be provisioned (Requirement 10.8).
	ResourcesMayRemain bool
}

// New returns an initialized Result for a scenario, defaulting to FAIL until a
// successful comparison flips it to PASS.
func New(scenario string) *Result {
	return &Result{
		Scenario: scenario,
		Status:   StatusFail,
		Timings:  make(map[string]time.Duration),
	}
}

// Passed reports whether the scenario passed.
func (r *Result) Passed() bool { return r.Status == StatusPass }

// ExitCode maps a result to a process exit code: 0 on PASS, 2 when destroy
// failed (resources may remain), 1 otherwise.
func (r *Result) ExitCode() int {
	switch {
	case r.ResourcesMayRemain:
		return 2
	case r.Passed():
		return 0
	default:
		return 1
	}
}
