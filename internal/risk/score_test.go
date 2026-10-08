package risk

import (
	"math"
	"math/rand"
	"reflect"
	"testing"
	"testing/quick"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intelligence/canonical"
)

// This file validates design correctness Property 4: risk scoring is
// deterministic, monotonic, and bounded.
//
// Properties:
//   - Bounds        -> Requirement 11.1 (score always in [0,100])
//   - Determinism   -> Requirement 11.2 (identical inputs -> identical output)
//   - Monotonicity  -> Requirement 11.3 (raising any single factor never lowers the score)
//
// It also table-tests the severity buckets (Requirement 11.4) and the KEV
// floor-to-high rule (Requirement 11.5).

const quickMaxCount = 500

// validContext is a RiskContext whose numeric fields are guaranteed to lie in
// their valid ranges. It implements quick.Generator so testing/quick produces
// in-range inputs for the bounds/determinism properties.
type validContext struct {
	RiskContext
}

// Generate builds a RiskContext with CVSS in [0,10], EPSS and AICriticality in
// [0,1], and random bools.
func (validContext) Generate(rnd *rand.Rand, _ int) reflect.Value {
	c := validContext{RiskContext{
		CVSS:           rnd.Float64() * 10,
		EPSS:           rnd.Float64(),
		KnownExploited: rnd.Intn(2) == 0,
		Reachable:      rnd.Intn(2) == 0,
		AICriticality:  rnd.Float64(),
	}}
	return reflect.ValueOf(c)
}

// anyContext may contain deliberately out-of-range numeric fields (negative,
// >10, >1). It exercises the clamping logic so that out-of-range signals still
// yield a bounded score.
type anyContext struct {
	RiskContext
}

func (anyContext) Generate(rnd *rand.Rand, _ int) reflect.Value {
	// Spread values across and beyond the valid ranges, including negatives.
	c := anyContext{RiskContext{
		CVSS:           rnd.Float64()*40 - 15, // roughly [-15, 25]
		EPSS:           rnd.Float64()*4 - 1.5, // roughly [-1.5, 2.5]
		KnownExploited: rnd.Intn(2) == 0,
		Reachable:      rnd.Intn(2) == 0,
		AICriticality:  rnd.Float64()*4 - 1.5, // roughly [-1.5, 2.5]
	}}
	return reflect.ValueOf(c)
}

func inRange(score float64) bool {
	return !math.IsNaN(score) && score >= 0 && score <= 100
}

// TestProperty_Bounds covers Requirement 11.1: Compute always returns a value
// in [0,100], both for in-range inputs and for deliberately out-of-range inputs
// (clamping).
//
// Validates: Requirements 11.1
func TestProperty_Bounds(t *testing.T) {
	t.Run("in-range inputs stay bounded", func(t *testing.T) {
		f := func(c validContext) bool {
			return inRange(Compute(c.RiskContext))
		}
		if err := quick.Check(f, &quick.Config{MaxCount: quickMaxCount}); err != nil {
			t.Error(err)
		}
	})

	t.Run("out-of-range inputs are clamped and stay bounded", func(t *testing.T) {
		f := func(c anyContext) bool {
			return inRange(Compute(c.RiskContext))
		}
		if err := quick.Check(f, &quick.Config{MaxCount: quickMaxCount}); err != nil {
			t.Error(err)
		}
	})
}

// TestProperty_Determinism covers Requirement 11.2: identical inputs always
// yield the same score and the same severity, across repeated calls.
//
// Validates: Requirements 11.2
func TestProperty_Determinism(t *testing.T) {
	f := func(c anyContext) bool {
		s1 := Compute(c.RiskContext)
		s2 := Compute(c.RiskContext)
		s3 := Compute(c.RiskContext)
		if s1 != s2 || s2 != s3 {
			return false
		}
		return Bucket(s1, c.KnownExploited) == Bucket(s3, c.KnownExploited)
	}
	if err := quick.Check(f, &quick.Config{MaxCount: quickMaxCount}); err != nil {
		t.Error(err)
	}
}

// monoCase is a base RiskContext (in valid range) plus small positive
// increments bounded so that base+increment stays within the valid range.
type monoCase struct {
	base  RiskContext
	dCVSS float64 // >= 0, base.CVSS+dCVSS <= 10
	dEPSS float64 // >= 0, base.EPSS+dEPSS <= 1
	dAI   float64 // >= 0, base.AICriticality+dAI <= 1
}

func (monoCase) Generate(rnd *rand.Rand, _ int) reflect.Value {
	cvss := rnd.Float64() * 10
	epss := rnd.Float64()
	ai := rnd.Float64()
	mc := monoCase{
		base: RiskContext{
			CVSS:           cvss,
			EPSS:           epss,
			KnownExploited: rnd.Intn(2) == 0,
			Reachable:      rnd.Intn(2) == 0,
			AICriticality:  ai,
		},
		// Increment occupies part of the remaining headroom to each upper bound.
		dCVSS: rnd.Float64() * (10 - cvss),
		dEPSS: rnd.Float64() * (1 - epss),
		dAI:   rnd.Float64() * (1 - ai),
	}
	return reflect.ValueOf(mc)
}

// TestProperty_Monotonicity covers Requirement 11.3: raising any single factor
// while holding the others constant never lowers the score.
//
// Validates: Requirements 11.3
func TestProperty_Monotonicity(t *testing.T) {
	f := func(mc monoCase) bool {
		base := Compute(mc.base)

		// Increase CVSS.
		up := mc.base
		up.CVSS = mc.base.CVSS + mc.dCVSS
		if Compute(up) < base {
			return false
		}

		// Increase EPSS.
		up = mc.base
		up.EPSS = mc.base.EPSS + mc.dEPSS
		if Compute(up) < base {
			return false
		}

		// Flip KnownExploited false -> true (only meaningful when currently false;
		// flipping true->false would be a decrease, which is not a single-factor
		// increase, so we only test the increasing direction).
		if !mc.base.KnownExploited {
			up = mc.base
			up.KnownExploited = true
			if Compute(up) < base {
				return false
			}
		}

		// Flip Reachable false -> true.
		if !mc.base.Reachable {
			up = mc.base
			up.Reachable = true
			if Compute(up) < base {
				return false
			}
		}

		// Increase AICriticality.
		up = mc.base
		up.AICriticality = mc.base.AICriticality + mc.dAI
		if Compute(up) < base {
			return false
		}

		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: quickMaxCount}); err != nil {
		t.Error(err)
	}
}

// TestBucket_Thresholds covers Requirement 11.4: severity bucket boundaries.
func TestBucket_Thresholds(t *testing.T) {
	cases := []struct {
		name  string
		score float64
		want  canonical.Severity
	}{
		{"exactly 90 is critical", 90, canonical.SevCritical},
		{"above 90 is critical", 95.5, canonical.SevCritical},
		{"just below 90 is high", 89.999, canonical.SevHigh},
		{"exactly 70 is high", 70, canonical.SevHigh},
		{"just below 70 is medium", 69.999, canonical.SevMedium},
		{"exactly 40 is medium", 40, canonical.SevMedium},
		{"just below 40 is low", 39.999, canonical.SevLow},
		{"exactly 10 is low", 10, canonical.SevLow},
		{"just below 10 is info", 9.999, canonical.SevInfo},
		{"zero is info", 0, canonical.SevInfo},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Bucket(tc.score, false); got != tc.want {
				t.Errorf("Bucket(%v,false) = %q, want %q", tc.score, got, tc.want)
			}
		})
	}
}

// TestBucket_KEVFloor covers Requirement 11.5: a KEV known-exploited
// vulnerability is floored at least high regardless of the computed score, and
// never downgrades an already-higher severity.
func TestBucket_KEVFloor(t *testing.T) {
	cases := []struct {
		name  string
		score float64
		want  canonical.Severity
	}{
		{"info score floored to high", 0, canonical.SevHigh},
		{"low score floored to high", 15, canonical.SevHigh},
		{"medium score floored to high", 45, canonical.SevHigh},
		{"high score stays high", 72, canonical.SevHigh},
		{"critical score stays critical", 95, canonical.SevCritical},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Bucket(tc.score, true); got != tc.want {
				t.Errorf("Bucket(%v,true) = %q, want %q", tc.score, got, tc.want)
			}
		})
	}
}
