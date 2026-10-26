package monitor

import "strings"

// Stage is one of the fixed 11 pipeline stages plus an "other" bucket for
// unmapped agent names. The vocabulary is fixed so the stage-flow view is
// stable: every run renders the same ordered set of stages, and a stage with
// no mapped legs simply reports a count of 0.
type Stage string

const (
	StageRecon     Stage = "RECON"
	StagePosture   Stage = "POSTURE"
	StageDiscovery Stage = "DISCOVERY"
	StageExploit   Stage = "EXPLOIT"
	StageSweep     Stage = "SWEEP"
	StageVerify    Stage = "VERIFY"
	StageStability Stage = "STABILITY"
	StageScore     Stage = "SCORE"
	StageFormat    Stage = "FORMAT"
	StageCoverage  Stage = "COVERAGE"
	StageReport    Stage = "REPORT"
	// StageOther collects any agent name that matches no known stage-name key.
	// It is deliberately NOT part of Pipeline so no leg is ever dropped: an
	// unmapped agent still lands somewhere (Req 11.4).
	StageOther Stage = "other"
)

// Pipeline is the ordered fixed 11-stage vocabulary (Req 11.1). The stage-flow
// view renders every one of these stages in this exact order; a stage with no
// mapped legs reports a count of 0. StageOther is intentionally excluded here.
//
// Pipeline order is also the precedence order used by MapStage when an agent
// name matches more than one key (Req 11.3), so it must not be reordered.
var Pipeline = []Stage{
	StageRecon, StagePosture, StageDiscovery, StageExploit, StageSweep,
	StageVerify, StageStability, StageScore, StageFormat, StageCoverage, StageReport,
}

// nameToStage maps the known lowercase stage-name keys to their Stage. The keys
// are matched case-insensitively as substrings of an agent name (Req 11.2).
// Any name matching none of these keys falls to StageOther (Req 11.4).
//
// This map is used only for lookup; the deterministic match order lives in
// Pipeline (see MapStage), so Go's randomized map iteration never affects which
// stage wins when a name matches more than one key.
var nameToStage = map[Stage]string{
	StageRecon:     "recon",
	StagePosture:   "posture",
	StageDiscovery: "discovery",
	StageExploit:   "exploit",
	StageSweep:     "sweep",
	StageVerify:    "verify",
	StageStability: "stability",
	StageScore:     "score",
	StageFormat:    "format",
	StageCoverage:  "coverage",
	StageReport:    "report",
}

// MapStage resolves an agent name to a Stage using a case-insensitive,
// whitespace-trimmed substring match (Req 11.2).
//
// When an agent name matches more than one known stage-name key, MapStage
// selects the stage of the earliest-defined matching key in the fixed Pipeline
// order, so mapping is deterministic (Req 11.3). This is why the function
// iterates Pipeline (a stable, ordered slice) and tests each stage's key in
// turn, rather than ranging over nameToStage (whose iteration order is
// randomized by Go and would make the result nondeterministic).
//
// A name matching no known key maps to StageOther so the leg is never dropped
// (Req 11.4).
func MapStage(agentName string) Stage {
	n := strings.ToLower(strings.TrimSpace(agentName))
	if n == "" {
		return StageOther
	}
	for _, st := range Pipeline {
		if strings.Contains(n, nameToStage[st]) {
			return st
		}
	}
	return StageOther
}
