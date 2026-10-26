package store

import (
	"encoding/json"
	"math/rand"
	"strings"
	"testing"
)

// This file carries the PURE arm of the PoC-redaction property test. It needs
// no database: stripPoCScriptCode / redactPoC are pure functions over decoded
// JSON, so the property is exercised in-process over generated trees. It lives
// in-package (package store) with no build tag so it runs on every
//
//	go test ./internal/store/... -run TestStripPoCScriptCodeRedactsEveryLevel
//
// even without a container runtime. The persist-and-read-back round-trip arm
// (which does need Postgres) lives behind the integration tag in
// finding_reports_integration_property_test.go.

// weaponizedPoC is the sentinel body planted into generated section trees. The
// property asserts this exact string appears NOWHERE in the redacted output —
// a single distinctive token makes a substring scan of the whole marshaled
// result a sound leak check.
const weaponizedPoC = "WEAPONIZED::import os;os.system('rm -rf /');<<<exploit-body-DO-NOT-STORE>>>"

// plantCounter builds a nested JSON value with poc_script_code keys planted at
// arbitrary nesting (top-level, inside arrays, inside deeply nested objects,
// and in multiple places), returning the value together with a count of how
// many poc_script_code keys it planted. The count lets the property assert the
// builder actually stressed the redaction (a tree with zero planted keys would
// trivially pass), and every planted value is the weaponizedPoC sentinel so the
// output scan proves each one was removed.
//
// It is driven by a caller-supplied *rand.Rand (fixed seed upstream) so a
// failing case is reproducible, and bounded by depth so generation terminates.
func plantCounter(rng *rand.Rand, depth int) (any, int) {
	// Leaf behavior at max depth: either a plain scalar or a lone planted key.
	if depth <= 0 {
		if rng.Intn(2) == 0 {
			return map[string]any{pocScriptCodeKey: weaponizedPoC}, 1
		}
		return rng.Intn(1000), 0
	}

	switch rng.Intn(3) {
	case 0:
		// Object: a mix of sibling scalar keys, recursively built children, and
		// — with good probability — a planted poc_script_code at THIS level.
		obj := map[string]any{}
		planted := 0
		// A couple of benign sibling keys that must survive redaction.
		obj["description"] = "benign poc_description that must be preserved"
		obj["severity"] = []string{"low", "medium", "high"}[rng.Intn(3)]
		// Plant the weaponized key at this level often (but not always, so some
		// levels have it and some do not — exercising "at any level").
		if rng.Intn(2) == 0 {
			obj[pocScriptCodeKey] = weaponizedPoC
			planted++
		}
		// Recurse into a few children under distinct benign keys.
		kids := 1 + rng.Intn(3)
		for i := 0; i < kids; i++ {
			child, pc := plantCounter(rng, depth-1)
			obj["child_"+string(rune('a'+i))] = child
			planted += pc
		}
		return obj, planted
	case 1:
		// Array: recurse into each element; some elements will themselves be
		// objects carrying planted keys, exercising "inside arrays".
		n := 1 + rng.Intn(3)
		arr := make([]any, 0, n+1)
		planted := 0
		// Sometimes an array element is a bare object with only the weaponized
		// key, so redaction must reach keys nested inside array elements.
		if rng.Intn(2) == 0 {
			arr = append(arr, map[string]any{pocScriptCodeKey: weaponizedPoC})
			planted++
		}
		for i := 0; i < n; i++ {
			child, pc := plantCounter(rng, depth-1)
			arr = append(arr, child)
			planted += pc
		}
		return arr, planted
	default:
		// Scalar leaf: nothing planted, nothing to preserve beyond itself.
		return "benign-" + string(rune('A'+rng.Intn(26))), 0
	}
}

// collectKeys walks a decoded JSON value and returns every object key seen at
// any nesting level. The property uses it to assert redaction removed EVERY
// poc_script_code key (none remain) while leaving the sibling keys intact.
func collectKeys(v any, out map[string]int) {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			out[k]++
			collectKeys(child, out)
		}
	case []any:
		for _, child := range t {
			collectKeys(child, out)
		}
	}
}

// TestStripPoCScriptCodeRedactsEveryLevel is the PURE arm of
//
//	Property 6: PoC redaction
//	Validates: Requirements 16.1, 16.2, 16.3, 18.3 (store slice)
//
// The property: for any generated sections JSON that embeds a weaponized
// poc_script_code value at arbitrary nesting (top-level, inside arrays, inside
// deeply nested objects, multiple occurrences), stripPoCScriptCode
//   - removes EVERY poc_script_code key at every level,
//   - preserves all sibling keys and their values, and
//   - the weaponized value string appears NOWHERE in the output.
//
// Random nested JSON with planted poc_script_code values is built with a
// fixed-seed math/rand builder (no new dependency: pgregory.net/rapid is not in
// go.mod), so a failing case is reproducible.
func TestStripPoCScriptCodeRedactsEveryLevel(t *testing.T) {
	// Fixed seed: deterministic case generation so a failure is reproducible.
	rng := rand.New(rand.NewSource(0x90CB0D7)) // fixed seed (reproducible)

	const cases = 200
	sawPlanted := false
	for c := 0; c < cases; c++ {
		depth := 1 + rng.Intn(5)
		tree, planted := plantCounter(rng, depth)
		if planted > 0 {
			sawPlanted = true
		}

		// Capture the sibling keys present BEFORE redaction (excluding the
		// poc_script_code keys themselves) so we can assert they all survive.
		before := map[string]int{}
		collectKeys(tree, before)

		raw, err := json.Marshal(tree)
		if err != nil {
			t.Fatalf("case %d: marshal generated tree: %v", c, err)
		}

		cleaned, err := stripPoCScriptCode(raw)
		if err != nil {
			t.Fatalf("case %d: stripPoCScriptCode errored on valid json: %v", c, err)
		}

		// 1) The weaponized body string appears NOWHERE in the output.
		if strings.Contains(string(cleaned), weaponizedPoC) {
			t.Fatalf("case %d: redacted output still contains the weaponized PoC body:\n%s",
				c, string(cleaned))
		}

		// Decode the cleaned output to inspect keys structurally.
		var cleanedVal any
		if err := json.Unmarshal(cleaned, &cleanedVal); err != nil {
			t.Fatalf("case %d: redacted output is not valid json: %v", c, err)
		}
		after := map[string]int{}
		collectKeys(cleanedVal, after)

		// 2) NO poc_script_code key remains at any level.
		if n := after[pocScriptCodeKey]; n != 0 {
			t.Fatalf("case %d: %d poc_script_code key(s) survived redaction, want 0", c, n)
		}

		// 3) Every sibling key/count is preserved exactly (only the
		//    poc_script_code key is removed; everything else is untouched).
		for k, want := range before {
			if k == pocScriptCodeKey {
				continue
			}
			if got := after[k]; got != want {
				t.Fatalf("case %d: sibling key %q count changed: before=%d after=%d",
					c, k, want, got)
			}
		}
		// And no new keys were introduced by redaction.
		for k := range after {
			if _, ok := before[k]; !ok {
				t.Fatalf("case %d: redaction introduced an unexpected key %q", c, k)
			}
		}
	}

	// Guard: the generator must actually have planted weaponized keys across the
	// run, otherwise the property would be vacuously true.
	if !sawPlanted {
		t.Fatal("generator planted no poc_script_code keys across all cases; property was vacuous")
	}
}

// TestStripPoCScriptCodeEmptyAndInvalid pins the two documented edge behaviors
// of stripPoCScriptCode that the round-trip relies on: an empty/nil input
// canonicalizes to the empty object (the column default), and invalid JSON is
// rejected with an error rather than persisted verbatim so a malformed body can
// never slip an un-walked payload past the strip (Property 6, Req 16.2).
func TestStripPoCScriptCodeEmptyAndInvalid(t *testing.T) {
	// Empty/nil -> canonical "{}".
	for _, in := range []json.RawMessage{nil, {}, json.RawMessage("")} {
		got, err := stripPoCScriptCode(in)
		if err != nil {
			t.Fatalf("empty input: unexpected error: %v", err)
		}
		if string(got) != `{}` {
			t.Fatalf("empty input: got %q, want %q", string(got), `{}`)
		}
	}

	// A top-level array with a planted key deep inside is still walked.
	nested := json.RawMessage(`[{"a":{"` + pocScriptCodeKey + `":"` + weaponizedPoC + `","keep":1}}]`)
	got, err := stripPoCScriptCode(nested)
	if err != nil {
		t.Fatalf("nested array: unexpected error: %v", err)
	}
	if strings.Contains(string(got), weaponizedPoC) {
		t.Fatalf("nested array: weaponized body survived: %s", string(got))
	}
	if !strings.Contains(string(got), `"keep"`) {
		t.Fatalf("nested array: sibling key dropped: %s", string(got))
	}

	// Invalid JSON is rejected, not persisted verbatim.
	if _, err := stripPoCScriptCode(json.RawMessage(`{not json`)); err == nil {
		t.Fatal("invalid json: expected an error, got nil")
	}
}
