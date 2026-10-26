package monitor

import "testing"

func TestPipelineIsThe11OrderedStages(t *testing.T) {
	want := []Stage{
		StageRecon, StagePosture, StageDiscovery, StageExploit, StageSweep,
		StageVerify, StageStability, StageScore, StageFormat, StageCoverage, StageReport,
	}
	if len(Pipeline) != len(want) {
		t.Fatalf("len(Pipeline) = %d, want %d", len(Pipeline), len(want))
	}
	for i := range want {
		if Pipeline[i] != want[i] {
			t.Errorf("Pipeline[%d] = %q, want %q", i, Pipeline[i], want[i])
		}
	}
	// StageOther must not be part of the rendered pipeline (Req 11.1).
	for i, st := range Pipeline {
		if st == StageOther {
			t.Errorf("Pipeline[%d] = StageOther, but 'other' must not be in Pipeline", i)
		}
	}
}

func TestMapStage(t *testing.T) {
	cases := []struct {
		name  string
		agent string
		want  Stage
	}{
		// Plain exact keys.
		{"recon", "recon", StageRecon},
		{"report", "report", StageReport},
		// Case-insensitive, whitespace-trimmed substring match (Req 11.2).
		{"uppercase substring", "  EXPLOIT-Agent  ", StageExploit},
		{"mixed case substring", "BlueVerifyWorker", StageVerify},
		{"embedded key", "stage-discovery-01", StageDiscovery},
		// Earliest-in-Pipeline-order wins on multi-match (Req 11.3):
		// RECON precedes REPORT in Pipeline, so a name containing both maps to RECON.
		{"recon before report", "recon-then-report", StageRecon},
		// EXPLOIT precedes VERIFY, so a name containing both maps to EXPLOIT.
		{"exploit before verify", "exploit-verify", StageExploit},
		// Unmapped names fall to 'other' and are never dropped (Req 11.4).
		{"unknown", "mystery-agent", StageOther},
		{"empty", "", StageOther},
		{"whitespace only", "   ", StageOther},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MapStage(tc.agent); got != tc.want {
				t.Errorf("MapStage(%q) = %q, want %q", tc.agent, got, tc.want)
			}
		})
	}
}

// TestMapStageDeterministic guards Req 11.3: a name matching several keys must
// resolve to the same stage on every call (no reliance on map iteration order).
func TestMapStageDeterministic(t *testing.T) {
	const multi = "recon-posture-discovery"
	first := MapStage(multi)
	for i := 0; i < 100; i++ {
		if got := MapStage(multi); got != first {
			t.Fatalf("MapStage(%q) nondeterministic: got %q then %q", multi, first, got)
		}
	}
	if first != StageRecon {
		t.Errorf("MapStage(%q) = %q, want %q (earliest in Pipeline)", multi, first, StageRecon)
	}
}
