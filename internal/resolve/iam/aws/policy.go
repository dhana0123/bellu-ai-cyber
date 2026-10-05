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
// S3 bucket ARN, plus the concrete allowed actions matched. Explicit Deny
// overrides Allow. NotAction is treated conservatively (ignored for granting).
func EvaluateS3(doc *PolicyDocument, bucketARN string) (Access, []string) {
	if doc == nil {
		return AccessNone, nil
	}
	// Resources covered by this bucket: the bucket and its objects.
	targets := []string{bucketARN, bucketARN + "/*"}

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
			if !isS3Action(act) {
				continue
			}
			switch strings.ToLower(st.Effect) {
			case "allow":
				for _, concrete := range expandS3Action(act) {
					allowed[concrete] = true
				}
			case "deny":
				for _, concrete := range expandS3Action(act) {
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
	return classify(allowed), actions
}

// classify maps a set of allowed S3 actions to an access level.
func classify(allowed map[string]bool) Access {
	// Admin: full control / policy / ACL management.
	for a := range allowed {
		la := strings.ToLower(a)
		if la == "s3:*" || strings.Contains(la, "putbucketpolicy") ||
			strings.Contains(la, "putbucketacl") || strings.Contains(la, "deletebucket") ||
			la == "s3:putaccountpublicaccessblock" {
			return AccessAdmin
		}
	}
	// Write: any mutating object/bucket action.
	for a := range allowed {
		if isWriteAction(a) {
			return AccessWrite
		}
	}
	// Otherwise read.
	return AccessRead
}

func isWriteAction(a string) bool {
	la := strings.ToLower(a)
	for _, w := range []string{"put", "delete", "create", "write", "restore", "replicate"} {
		if strings.Contains(la, w) {
			return true
		}
	}
	return false
}

// expandS3Action expands a wildcard like "s3:*" or "s3:Get*" into the set of
// representative actions we track. For non-wildcard actions it returns the
// action itself.
func expandS3Action(act string) []string {
	if !strings.Contains(act, "*") {
		return []string{act}
	}
	// Representative S3 action catalog used for classification.
	catalog := []string{
		"s3:GetObject", "s3:GetObjectVersion", "s3:ListBucket", "s3:GetBucketLocation",
		"s3:PutObject", "s3:DeleteObject", "s3:RestoreObject",
		"s3:PutBucketPolicy", "s3:PutBucketAcl", "s3:DeleteBucket",
	}
	if act == "s3:*" {
		return append(catalog, "s3:*")
	}
	// Prefix wildcard, e.g. s3:Get*
	prefix := strings.TrimSuffix(act, "*")
	var out []string
	for _, c := range catalog {
		if strings.HasPrefix(strings.ToLower(c), strings.ToLower(prefix)) {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		out = []string{act}
	}
	return out
}

func isS3Action(act string) bool {
	la := strings.ToLower(act)
	return la == "*" || strings.HasPrefix(la, "s3:")
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
