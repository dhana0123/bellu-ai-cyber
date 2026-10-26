package monitor

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestRoute(t *testing.T) {
	cases := []struct {
		kind string
		want Team
	}{
		{"ci", TeamRedTeam},
		{"vulnerability", TeamBlueTeam},
		{"code_change", TeamPRReviewer},
		{"resource", TeamTriage},
		{"", ""},        // empty kind is unroutable
		{"unknown", ""}, // unknown kind is unroutable (-> skipped)
	}
	for _, tc := range cases {
		if got := Route(tc.kind); got != tc.want {
			t.Errorf("Route(%q) = %q, want %q", tc.kind, got, tc.want)
		}
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name    string
		source  string
		payload string
		want    string
		wantErr bool
	}{
		{"correlate is vulnerability", "correlate", `{}`, "vulnerability", false},
		{"discovery is resource", "discovery", `{}`, "resource", false},
		{"github explicit check_run -> ci", "github", `{"event_type":"check_run"}`, "ci", false},
		{"github explicit workflow_run -> ci", "github", `{"event":"workflow_run"}`, "ci", false},
		{"github explicit push -> code_change", "github", `{"event_type":"push"}`, "code_change", false},
		{"github explicit pull_request -> code_change", "github", `{"event_type":"pull_request"}`, "code_change", false},
		{"github shape check_run -> ci", "github", `{"check_run":{"id":1}}`, "ci", false},
		{"github shape workflow_run -> ci", "github", `{"workflow_run":{"id":1}}`, "ci", false},
		{"github shape pull_request -> code_change", "github", `{"pull_request":{"number":7}}`, "code_change", false},
		{"github shape push (ref+commits) -> code_change", "github", `{"ref":"refs/heads/main","commits":[]}`, "code_change", false},
		{"github unclassifiable", "github", `{"installation":{"id":1}}`, "", true},
		{"github malformed json", "github", `not json`, "", true},
		{"unknown source", "slack", `{}`, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Classify(tc.source, json.RawMessage(tc.payload))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Classify(%q, %s) = %q, want error", tc.source, tc.payload, got)
				}
				if !errors.Is(err, ErrUnclassifiable) {
					t.Errorf("error %v is not ErrUnclassifiable", err)
				}
				if got != "" {
					t.Errorf("Classify returned kind %q alongside error, want empty", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Classify(%q, %s) unexpected error: %v", tc.source, tc.payload, err)
			}
			if got != tc.want {
				t.Errorf("Classify(%q, %s) = %q, want %q", tc.source, tc.payload, got, tc.want)
			}
			// Every classified kind must route to a non-empty team.
			if Route(got) == "" {
				t.Errorf("classified kind %q routes to no team", got)
			}
		})
	}
}
