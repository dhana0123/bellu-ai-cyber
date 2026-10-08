package main

import (
	"testing"
	"time"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intel/parser"
	epssparse "github.com/bellu/bellu-ai-cyber/bellu-global-intel/parser/epss"
	kevparse "github.com/bellu/bellu-ai-cyber/bellu-global-intel/parser/kev"
)

// TestIsFullFetch pins the cursor-mode selection logic: daily (and any
// unexpected mode) is a full fetch with an empty cursor (Requirement 7.3),
// while incremental resumes from the stored cursor (Requirement 7.4).
func TestIsFullFetch(t *testing.T) {
	cases := []struct {
		mode string
		want bool
	}{
		{modeDaily, true},
		{modeIncremental, false},
		{"", true},          // unknown mode defaults to a safe full fetch
		{"bootstrap", true}, // future modes are not cursor-based here
	}
	for _, tc := range cases {
		if got := isFullFetch(tc.mode); got != tc.want {
			t.Errorf("isFullFetch(%q) = %v, want %v", tc.mode, got, tc.want)
		}
	}
}

// TestEpssScoredDate verifies the scored date is extracted from EPSS rows and
// that non-EPSS records yield the zero time (which suppresses EPSS history).
func TestEpssScoredDate(t *testing.T) {
	want := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	t.Run("bare date", func(t *testing.T) {
		recs := []parser.SourceRecord{
			epssparse.Row{CVE: "CVE-2024-0001", EPSS: 0.5, ScoredOn: "2024-01-02"},
		}
		if got := epssScoredDate(recs); !got.Equal(want) {
			t.Errorf("epssScoredDate = %v, want %v", got, want)
		}
	})

	t.Run("timestamp prefix", func(t *testing.T) {
		recs := []parser.SourceRecord{
			epssparse.Row{CVE: "CVE-2024-0001", EPSS: 0.5, ScoredOn: "2024-01-02T00:00:00+0000"},
		}
		if got := epssScoredDate(recs); !got.Equal(want) {
			t.Errorf("epssScoredDate = %v, want %v", got, want)
		}
	})

	t.Run("no epss rows yields zero time", func(t *testing.T) {
		recs := []parser.SourceRecord{
			kevparse.Entry{CveID: "CVE-2024-0001"},
		}
		if got := epssScoredDate(recs); !got.IsZero() {
			t.Errorf("epssScoredDate = %v, want zero time", got)
		}
	})

	t.Run("empty scored date yields zero time", func(t *testing.T) {
		recs := []parser.SourceRecord{
			epssparse.Row{CVE: "CVE-2024-0001", EPSS: 0.5, ScoredOn: ""},
		}
		if got := epssScoredDate(recs); !got.IsZero() {
			t.Errorf("epssScoredDate = %v, want zero time", got)
		}
	})
}
