// Package aws implements an approximate AWS IAM policy evaluator. It parses
// identity policy documents (attached and inline), matches S3 actions and
// resources (including wildcards), and classifies the resulting access as
// read, write or admin.
//
// V1 limitation: this is approximate. It handles identity-based Allow/Deny with
// resource and wildcard matching, but ignores SCPs, permission boundaries and
// conditions. Edges derived here carry confidence < 1 and effective:"approx".
package aws

import (
	"encoding/json"
	"strings"
)

// Access is the classification of a set of allowed actions on a resource.
type Access string

const (
	AccessNone  Access = ""
	AccessRead  Access = "read"
	AccessWrite Access = "write"
	AccessAdmin Access = "admin"
)

// PolicyDocument is a parsed IAM policy. Statement fields that may be a string
// or a list are modeled as stringOrSlice.
type PolicyDocument struct {
	Version   string      `json:"Version"`
	Statement []Statement `json:"Statement"`
}

// Statement is one IAM policy statement.
type Statement struct {
	Effect    string        `json:"Effect"`
	Action    stringOrSlice `json:"Action"`
	NotAction stringOrSlice `json:"NotAction"`
	Resource  stringOrSlice `json:"Resource"`
	// Principal is populated for trust policies (assume_role_policy_std); it is
	// ignored by resource-access evaluation. See trust.go.
	Principal principalValue `json:"Principal"`
}

// ParsePolicy parses a policy document from JSON bytes. Steampipe exposes
// policy documents as already-decoded JSON objects, so this accepts either an
// object or a JSON string.
func ParsePolicy(raw json.RawMessage) (*PolicyDocument, error) {
	// If it's a JSON string, unquote first.
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "\"") {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			raw = json.RawMessage(s)
		}
	}
	var doc PolicyDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// EvaluateS3 returns the access level an identity's policy grants on a given
// S3 bucket ARN, plus the concrete allowed actions matched. It is a thin shim
// over the generalized Evaluate using the "s3" catalog, preserving the exact
// S3 behavior (and all existing S3 tests).
func EvaluateS3(doc *PolicyDocument, bucketARN string) (Access, []string) {
	return Evaluate(doc, bucketARN, Catalogs["s3"])
}

// Evaluate returns the access level an identity's policy grants on a target
// resource ARN, plus the concrete allowed actions matched, for the resource
// kind described by cat. It is resource-kind-agnostic: the matching and
// classification logic is identical for every kind; only the ActionCatalog
// data differs.
//
// Rules (same as the historical S3 evaluator):
//   - A statement grants only when its Resource matches one of the target's
//     resource forms (resourceMatchesAny / wildcard matching).
//   - NotAction statements (no Action) are treated conservatively: ignored for
//     granting.
//   - Wildcard actions are expanded via the catalog's Representative list.
//   - Explicit Deny overrides Allow (R5.5).
//   - Classification follows admin > write > read precedence (R5.2).
func Evaluate(doc *PolicyDocument, targetARN string, cat ActionCatalog) (Access, []string) {
	if doc == nil {
		return AccessNone, nil
	}
	targets := cat.ResourceForms(targetARN)

	allowed := map[string]bool{}
	denied := map[string]bool{}

	for _, st := range doc.Statement {
		if len(st.Action) == 0 {
			// NotAction statements are treated conservatively: we do not infer
			// grants from them in V1.
			continue
		}
		if !resourceMatchesAny(st.Resource, targets) {
			continue
		}
		for _, act := range st.Action {
			if !cat.matchesAction(act) {
				continue
			}
			switch strings.ToLower(st.Effect) {
			case "allow":
				for _, concrete := range cat.expandAction(act) {
					allowed[concrete] = true
				}
			case "deny":
				for _, concrete := range cat.expandAction(act) {
					denied[concrete] = true
				}
			}
		}
	}

	// Explicit deny overrides allow.
	for d := range denied {
		delete(allowed, d)
	}
	if len(allowed) == 0 {
		return AccessNone, nil
	}

	actions := make([]string, 0, len(allowed))
	for a := range allowed {
		actions = append(actions, a)
	}
	return cat.classify(allowed), actions
}

// resourceMatchesAny reports whether any policy resource pattern matches any
// target ARN. "*" matches everything; patterns may contain "*" wildcards.
func resourceMatchesAny(patterns stringOrSlice, targets []string) bool {
	if len(patterns) == 0 {
		return false
	}
	for _, p := range patterns {
		for _, t := range targets {
			if wildcardMatch(p, t) {
				return true
			}
		}
	}
	return false
}

// wildcardMatch does ARN wildcard matching with "*" (any run) and "?" (one
// char), case-sensitive as ARNs are.
func wildcardMatch(pattern, s string) bool {
	if pattern == "*" {
		return true
	}
	return globMatch(pattern, s)
}

// globMatch is a simple iterative glob matcher supporting * and ?.
func globMatch(pattern, s string) bool {
	var pi, si int
	star := -1
	var ss int
	for si < len(s) {
		if pi < len(pattern) && (pattern[pi] == '?' || pattern[pi] == s[si]) {
			pi++
			si++
		} else if pi < len(pattern) && pattern[pi] == '*' {
			star = pi
			ss = si
			pi++
		} else if star != -1 {
			pi = star + 1
			ss++
			si = ss
		} else {
			return false
		}
	}
	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}
	return pi == len(pattern)
}
