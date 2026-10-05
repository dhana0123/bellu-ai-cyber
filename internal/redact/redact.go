// Package redact removes sensitive values from RawEvent payloads before they
// are stored. It runs as a pipeline stage, so nothing sensitive ever reaches
// the evidence store.
//
// Two rule kinds are supported:
//
//   - key patterns: case-insensitive regexes matched against JSON object keys
//     anywhere in the payload; matching values are replaced with the marker.
//   - JSON paths: dotted paths (with "*" wildcard segments) whose values are
//     removed entirely.
package redact

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
)

// Marker is the value substituted for redacted fields.
const Marker = "***REDACTED***"

// Redactor applies key-pattern and JSON-path rules to event payloads.
type Redactor struct {
	keyPatterns []*regexp.Regexp
	jsonPaths   [][]string
}

// New compiles the given key patterns and JSON paths into a Redactor.
func New(keyPatterns, jsonPaths []string) (*Redactor, error) {
	r := &Redactor{}
	for _, p := range keyPatterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("redact: bad key pattern %q: %w", p, err)
		}
		r.keyPatterns = append(r.keyPatterns, re)
	}
	for _, p := range jsonPaths {
		r.jsonPaths = append(r.jsonPaths, strings.Split(p, "."))
	}
	return r, nil
}

// Apply implements pipeline.Stage. It redacts the payload and returns the event
// with a rewritten payload. The caller re-finalizes to recompute the hash/ID.
func (r *Redactor) Apply(ev rawevent.RawEvent) (rawevent.RawEvent, error) {
	if len(ev.Payload) == 0 || string(ev.Payload) == "null" {
		return ev, nil
	}
	var v any
	if err := json.Unmarshal(ev.Payload, &v); err != nil {
		// Not JSON we can walk; leave as-is.
		return ev, nil
	}

	// Remove configured JSON paths first.
	for _, path := range r.jsonPaths {
		removePath(v, path)
	}
	// Then redact by key pattern.
	v = r.redactKeys(v)

	out, err := json.Marshal(v)
	if err != nil {
		return ev, fmt.Errorf("redact: remarshal: %w", err)
	}
	ev.Payload = out
	return ev, nil
}

// redactKeys walks the value and replaces values whose key matches any key
// pattern.
func (r *Redactor) redactKeys(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if r.keyMatches(k) {
				t[k] = Marker
				continue
			}
			t[k] = r.redactKeys(val)
		}
		return t
	case []any:
		for i, val := range t {
			t[i] = r.redactKeys(val)
		}
		return t
	default:
		return v
	}
}

func (r *Redactor) keyMatches(key string) bool {
	for _, re := range r.keyPatterns {
		if re.MatchString(key) {
			return true
		}
	}
	return false
}

// removePath deletes the value at the dotted path. A "*" segment matches every
// key of a map or every element of a slice.
func removePath(v any, path []string) {
	if len(path) == 0 {
		return
	}
	seg := path[0]
	last := len(path) == 1

	switch t := v.(type) {
	case map[string]any:
		if seg == "*" {
			for k := range t {
				if last {
					delete(t, k)
				} else {
					removePath(t[k], path[1:])
				}
			}
			return
		}
		if _, ok := t[seg]; !ok {
			return
		}
		if last {
			delete(t, seg)
		} else {
			removePath(t[seg], path[1:])
		}
	case []any:
		for i := range t {
			removePath(t[i], path)
		}
	}
}
