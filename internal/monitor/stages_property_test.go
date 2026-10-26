package monitor

import (
	"math/rand"
	"strings"
	"testing"
	"testing/quick"
)

// Property 8: Stage-flow leg counts.
// Validates Requirements 11.4, 11.5, 12.1.
//
// Given an arbitrary multiset of agent names, map each via MapStage and tally
// per stage. The invariant has three parts:
//
//	(a) every agent lands in exactly one stage — one of the 11 Pipeline stages
//	    or StageOther (Req 11.4: no name is dropped, 11.5: a fixed vocabulary);
//	(b) no agent is dropped — the sum of per-stage counts over the 11 pipeline
//	    stages PLUS the "other" bucket equals the number of input agents
//	    (Req 11.5, 12.1: leg counts partition the emitted agents);
//	(c) a name matching a known stage key always maps into the 11 pipeline
//	    stages, never to StageOther (Req 11.4).
//
// The generator builds names from a mix of: names containing stage keys, names
// with no key, mixed case, and surrounding/embedded whitespace, so the input
// space exercises every branch of MapStage.

// stageKeys is the set of known lowercase keys MapStage matches against. It is
// derived from nameToStage so the test tracks the production vocabulary.
func stageKeysForTest() []string {
	keys := make([]string, 0, len(nameToStage))
	for _, k := range nameToStage {
		keys = append(keys, k)
	}
	return keys
}

// pipelineSet is the set of the 11 pipeline stages for fast membership checks.
func pipelineSetForTest() map[Stage]bool {
	m := make(map[Stage]bool, len(Pipeline))
	for _, st := range Pipeline {
		m[st] = true
	}
	return m
}

// genAgentName produces one random agent name biased to cover all MapStage
// branches: pure-key, key-with-noise, multi-key, no-key, mixed case, and
// whitespace variants. It reports whether the generated name is guaranteed to
// contain at least one known stage key (used to assert invariant (c)).
func genAgentName(rng *rand.Rand) (name string, hasKey bool) {
	keys := stageKeysForTest()
	noiseAlphabet := "xyzqw-_0123456789 \t"

	randNoise := func(n int) string {
		var b strings.Builder
		for i := 0; i < n; i++ {
			b.WriteByte(noiseAlphabet[rng.Intn(len(noiseAlphabet))])
		}
		return b.String()
	}

	// Randomly vary the case of a string so the case-insensitive match (Req
	// 11.2) is exercised; this preserves any embedded key because matching is
	// also case-insensitive.
	randCase := func(s string) string {
		rs := []rune(s)
		for i, r := range rs {
			if rng.Intn(2) == 0 {
				rs[i] = []rune(strings.ToUpper(string(r)))[0]
			} else {
				rs[i] = []rune(strings.ToLower(string(r)))[0]
			}
		}
		return string(rs)
	}

	switch rng.Intn(5) {
	case 0:
		// A single key, possibly re-cased.
		return randCase(keys[rng.Intn(len(keys))]), true
	case 1:
		// A key embedded in noise with surrounding whitespace.
		k := keys[rng.Intn(len(keys))]
		s := randNoise(rng.Intn(4)) + k + randNoise(rng.Intn(4))
		s = "  " + randCase(s) + "\t"
		return s, true
	case 2:
		// Two keys concatenated (exercises multi-match precedence).
		a := keys[rng.Intn(len(keys))]
		b := keys[rng.Intn(len(keys))]
		return randCase(a + "-" + b), true
	case 3:
		// Pure noise, no key. Guard against noise accidentally containing a
		// key (the noise alphabet shares no key substring, but be defensive).
		for {
			s := randNoise(1 + rng.Intn(8))
			if !containsAnyKey(s, keys) {
				return s, false
			}
		}
	default:
		// Whitespace-only or empty: must land in StageOther.
		n := rng.Intn(4)
		return strings.Repeat(" ", n), false
	}
}

func containsAnyKey(s string, keys []string) bool {
	low := strings.ToLower(s)
	for _, k := range keys {
		if strings.Contains(low, k) {
			return true
		}
	}
	return false
}

func TestProperty8_StageFlowLegCounts(t *testing.T) {
	pipeline := pipelineSetForTest()

	// The property: for an arbitrary multiset of agent names, tallying their
	// MapStage results partitions the agents across the 11 pipeline stages and
	// the "other" bucket with no name lost or double-counted.
	property := func(seed int64, size uint8) bool {
		rng := rand.New(rand.NewSource(seed))
		n := int(size) // 0..255 agents per trial

		names := make([]string, n)
		hasKey := make([]bool, n)
		for i := 0; i < n; i++ {
			names[i], hasKey[i] = genAgentName(rng)
		}

		counts := make(map[Stage]int)
		for i, name := range names {
			st := MapStage(name)

			// (a) Every agent lands in exactly one known bucket: a pipeline
			// stage or StageOther.
			if !pipeline[st] && st != StageOther {
				t.Logf("name %q mapped to unknown stage %q", name, st)
				return false
			}

			// (c) A name containing a known key never falls to StageOther.
			if hasKey[i] && st == StageOther {
				t.Logf("name %q contains a stage key but mapped to StageOther", name)
				return false
			}

			counts[st]++
		}

		// (b) No agent dropped: sum over the 11 pipeline stages plus the
		// "other" bucket equals the number of input agents.
		total := counts[StageOther]
		for _, st := range Pipeline {
			total += counts[st]
		}
		if total != n {
			t.Logf("leg-count sum = %d, want %d (names=%v counts=%v)", total, n, names, counts)
			return false
		}

		return true
	}

	if err := quick.Check(property, &quick.Config{MaxCount: 2000}); err != nil {
		t.Fatalf("Property 8 (stage-flow leg counts) failed: %v", err)
	}
}
