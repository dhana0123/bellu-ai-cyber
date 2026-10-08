// Package risk computes a transparent, bounded, deterministic risk score for a
// correlated finding and buckets it into a severity. The scoring function is a
// pure function of its inputs (RiskContext): the same inputs always yield the
// same score (determinism), the score is bounded to [0,100], it is monotonic in
// every risk factor (raising CVSS, EPSS, reachability, or AI criticality never
// lowers the score), and a KEV known-exploited vulnerability is floored at
// least "high" regardless of the computed score.
package risk

import (
	"context"
	"math"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intel/canonical"
)

// Severity reuses the canonical intelligence severity type so that risk
// severities and intel severities share one vocabulary
// (critical/high/medium/low/info).
type Severity = canonical.Severity

// RiskScorer turns a finding's risk context into a bounded risk score plus a
// severity bucket.
//
// NOTE (deviation from the design's interface sketch): the design shows
//
//	Score(ctx context.Context, c Candidate, ctx RiskContext) (RiskResult, error)
//
// That sketch has two problems we resolve here. First, it uses the name "ctx"
// for both parameters, which does not compile. Second, the Candidate type lives
// in internal/intelcorrelate, which depends on this package; importing it here
// would create a dependency cycle and an ordering problem. The Candidate is not
// needed to compute the score — scoring is a pure function of the RiskContext —
// so we drop it. The matcher attaches the returned score to its Candidate. The
// context parameter is named ctx and the risk context is named rctx.
type RiskScorer interface {
	Score(ctx context.Context, rctx RiskContext) (RiskResult, error)
}

// RiskContext is the full set of signals the scorer consumes.
type RiskContext struct {
	CVSS           float64 // base score 0..10
	EPSS           float64 // exploit probability 0..1
	KnownExploited bool    // KEV: present in CISA Known Exploited catalog
	Reachable      bool    // an attack path reaches the vulnerable asset
	AICriticality  float64 // 0..1, higher for agents/models with broad access
}

// RiskResult is the scorer output: a bounded score and its severity bucket.
type RiskResult struct {
	Score    float64 // 0..100
	Severity Severity
}

// Scorer is the default, stateless RiskScorer implementation. Being stateless
// is what makes scoring deterministic and re-runnable.
type Scorer struct{}

// NewScorer returns the default risk scorer.
func NewScorer() Scorer { return Scorer{} }

// Score implements RiskScorer. It never returns an error; the signature keeps
// the error for interface stability and future validation hooks.
func (Scorer) Score(_ context.Context, rctx RiskContext) (RiskResult, error) {
	s := Compute(rctx)
	return RiskResult{Score: s, Severity: Bucket(s, rctx.KnownExploited)}, nil
}

// Compute evaluates the design's Risk Scoring Model formula and returns a score
// clamped to [0,100]:
//
//	base     = (CVSS / 10) * 100
//	exploit  = max(EPSS, KnownExploited ? 0.95 : 0)
//	exposure = Reachable ? 1.0 : 0.4
//	aiBoost  = 1 + 0.25 * AICriticality
//	score    = clamp( base * (0.5 + 0.5*exploit) * exposure * aiBoost, 0, 100 )
//
// Inputs are clamped to their valid ranges first so that out-of-range signals
// cannot push the score outside [0,100] or break monotonicity.
func Compute(rctx RiskContext) float64 {
	cvss := clamp(rctx.CVSS, 0, 10)
	epss := clamp(rctx.EPSS, 0, 1)
	ai := clamp(rctx.AICriticality, 0, 1)

	base := (cvss / 10) * 100

	exploit := epss
	if rctx.KnownExploited && 0.95 > exploit {
		exploit = 0.95
	}

	exposure := 0.4
	if rctx.Reachable {
		exposure = 1.0
	}

	aiBoost := 1 + 0.25*ai

	score := base * (0.5 + 0.5*exploit) * exposure * aiBoost
	return clamp(score, 0, 100)
}

// Bucket maps a score in [0,100] to a severity bucket:
// >=90 critical, >=70 high, >=40 medium, >=10 low, else info. A KEV
// known-exploited vulnerability is additionally floored at least "high"
// regardless of the computed score.
func Bucket(score float64, knownExploited bool) Severity {
	sev := bucketScore(score)
	if knownExploited && severityRank(sev) < severityRank(canonical.SevHigh) {
		sev = canonical.SevHigh
	}
	return sev
}

func bucketScore(score float64) Severity {
	switch {
	case score >= 90:
		return canonical.SevCritical
	case score >= 70:
		return canonical.SevHigh
	case score >= 40:
		return canonical.SevMedium
	case score >= 10:
		return canonical.SevLow
	default:
		return canonical.SevInfo
	}
}

// severityRank orders severities so the KEV floor ("at least high") can be
// expressed as a comparison; higher rank is more severe.
func severityRank(s Severity) int {
	switch s {
	case canonical.SevCritical:
		return 4
	case canonical.SevHigh:
		return 3
	case canonical.SevMedium:
		return 2
	case canonical.SevLow:
		return 1
	default:
		return 0
	}
}

func clamp(v, lo, hi float64) float64 {
	if math.IsNaN(v) {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
