package redact

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
)

func mustRedactor(t *testing.T) *Redactor {
	t.Helper()
	r, err := New(
		[]string{"(?i)secret", "(?i)password", "(?i)access[-_]?key"},
		[]string{"environment.variables", "data"},
	)
	if err != nil {
		t.Fatalf("new redactor: %v", err)
	}
	return r
}

func TestRedactKeyPattern(t *testing.T) {
	r := mustRedactor(t)
	ev := rawevent.RawEvent{Payload: json.RawMessage(`{"name":"ok","AWS_SECRET_ACCESS_KEY":"AKIAEXAMPLE","nested":{"password":"hunter2"}}`)}
	out, err := r.Apply(ev)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out.Payload)
	if strings.Contains(s, "AKIAEXAMPLE") {
		t.Fatalf("secret not redacted: %s", s)
	}
	if strings.Contains(s, "hunter2") {
		t.Fatalf("nested password not redacted: %s", s)
	}
	if !strings.Contains(s, "ok") {
		t.Fatalf("non-secret value lost: %s", s)
	}
	if strings.Count(s, Marker) != 2 {
		t.Fatalf("expected 2 redactions, got: %s", s)
	}
}

func TestRedactJSONPath(t *testing.T) {
	r := mustRedactor(t)
	ev := rawevent.RawEvent{Payload: json.RawMessage(`{"environment":{"variables":{"DB_PASS":"x","OK":"y"}},"keep":"z"}`)}
	out, err := r.Apply(ev)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out.Payload, &m); err != nil {
		t.Fatal(err)
	}
	env := m["environment"].(map[string]any)
	if _, ok := env["variables"]; ok {
		t.Fatalf("environment.variables not removed: %s", out.Payload)
	}
	if m["keep"] != "z" {
		t.Fatalf("unrelated key lost: %s", out.Payload)
	}
}

func TestRedactWildcardPath(t *testing.T) {
	r, err := New(nil, []string{"items.*.secret"})
	if err != nil {
		t.Fatal(err)
	}
	ev := rawevent.RawEvent{Payload: json.RawMessage(`{"items":{"a":{"secret":"1","ok":"2"},"b":{"secret":"3"}}}`)}
	out, err := r.Apply(ev)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out.Payload), `"1"`) || strings.Contains(string(out.Payload), `"3"`) {
		t.Fatalf("wildcard path not removed: %s", out.Payload)
	}
	if !strings.Contains(string(out.Payload), `"2"`) {
		t.Fatalf("sibling key lost: %s", out.Payload)
	}
}

func TestRedactNonJSONPassthrough(t *testing.T) {
	r := mustRedactor(t)
	ev := rawevent.RawEvent{Payload: json.RawMessage(`"just a string"`)}
	out, err := r.Apply(ev)
	if err != nil {
		t.Fatal(err)
	}
	if string(out.Payload) != `"just a string"` {
		t.Fatalf("string payload altered: %s", out.Payload)
	}
}
