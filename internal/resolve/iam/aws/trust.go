package aws

import (
	"encoding/json"
	"strings"
)

// AnyIdentityKey is the stable synthetic principal key emitted for a wildcard
// ("*") trust principal (R4.4). It is the trust analog of the synthetic
// internet NetworkEndpoint and must resolve to a single synthetic Identity node
// emitted by the normalizer.
const AnyIdentityKey = "aws:any-identity"

// principalValue models the Principal element of a trust-policy statement. It
// may be the string "*", or an object with "AWS" / "Service" / "Federated"
// keys whose values are a string or a list of strings.
type principalValue struct {
	Wildcard  bool
	AWS       []string
	Service   []string
	Federated []string
}

// UnmarshalJSON implements json.Unmarshaler for the several Principal shapes.
func (p *principalValue) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "null" {
		return nil
	}
	// Bare string, e.g. "*" or a single ARN.
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		if s == "*" {
			p.Wildcard = true
		} else {
			p.AWS = []string{s}
		}
		return nil
	}
	// Object form: {"AWS": ...}, {"Service": ...}, {"Federated": ...}.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	for k, v := range obj {
		vals := rawToStrings(v)
		switch strings.ToLower(k) {
		case "aws":
			for _, val := range vals {
				if val == "*" {
					p.Wildcard = true
				} else {
					p.AWS = append(p.AWS, val)
				}
			}
		case "service":
			p.Service = append(p.Service, vals...)
		case "federated":
			p.Federated = append(p.Federated, vals...)
		}
	}
	return nil
}

// rawToStrings coerces a JSON value that may be a string or a list of strings.
func rawToStrings(v json.RawMessage) []string {
	var single string
	if err := json.Unmarshal(v, &single); err == nil {
		return []string{single}
	}
	var list []string
	if err := json.Unmarshal(v, &list); err == nil {
		return list
	}
	return nil
}

// AssumeGrant is one derived CAN_ASSUME grant from a trust policy. The analyzer
// produces only the principal key + metadata; the normalizer turns these into
// CAN_ASSUME edges (SrcKey=PrincipalKey, DstKey=role ARN).
type AssumeGrant struct {
	// PrincipalKey is the resolved source key: a named IAM user/role ARN, an
	// account CloudAccount key, or the synthetic AnyIdentityKey.
	PrincipalKey string
	// Confidence is 1.0 for named-principal grants, 0.7 for account-root and
	// wildcard grants.
	Confidence float64
	// Attributes carries provider-neutral trust metadata (account_grant,
	// unscoped) to stamp on the edge.
	Attributes map[string]any
}

// supported assume actions (R4.1).
var assumeActions = map[string]bool{
	"sts:assumerole":                true,
	"sts:assumerolewithsaml":        true,
	"sts:assumerolewithwebidentity": true,
}

// statementHasAssume reports whether a statement's Action set includes a
// supported assume action (or a wildcard that covers it).
func statementHasAssume(st Statement) bool {
	for _, a := range st.Action {
		la := strings.ToLower(a)
		if la == "*" || la == "sts:*" || assumeActions[la] {
			return true
		}
		// Prefix wildcard like sts:AssumeRole* also qualifies.
		if strings.HasSuffix(la, "*") {
			pfx := strings.TrimSuffix(la, "*")
			for act := range assumeActions {
				if strings.HasPrefix(act, pfx) {
					return true
				}
			}
			if strings.HasPrefix("sts:", pfx) || strings.HasPrefix(pfx, "sts:") {
				return true
			}
		}
	}
	return false
}

// accountKeyForTrust mirrors the normalizer's accountKey so account-root grants
// resolve to the same CloudAccount node.
func accountKeyForTrust(accountID string) string { return "aws-account:" + accountID }

// AnalyzeTrustPolicy parses a role's assume_role_policy_std document and returns
// the CAN_ASSUME grants it implies, applying the R4 rules:
//
//   - named IAM user/role ARN principal -> grant from that ARN, confidence 1.0 (R4.1)
//   - account root ARN or bare 12-digit account id -> grant from the account's
//     CloudAccount key, confidence 0.7, attr account_grant:true (R4.2)
//   - service principal (*.amazonaws.com) -> excluded (R4.3)
//   - wildcard "*" -> grant from AnyIdentityKey, confidence 0.7, attr unscoped:true (R4.4)
//   - a principal denied an assume action is excluded even if also allowed (R4.5)
//
// roleARN is accepted for symmetry/future use; the role is the edge destination
// and is supplied by the caller. An unparseable document yields no grants.
func AnalyzeTrustPolicy(roleARN string, doc *PolicyDocument) []AssumeGrant {
	if doc == nil {
		return nil
	}

	// First pass: compute the deny set (principal keys denied an assume action).
	denied := map[string]bool{}
	for _, st := range doc.Statement {
		if strings.ToLower(st.Effect) != "deny" || !statementHasAssume(st) {
			continue
		}
		for _, g := range grantsForPrincipal(st.Principal) {
			denied[g.PrincipalKey] = true
		}
	}

	// Second pass: collect allow grants, excluding denied principals (R4.5).
	seen := map[string]bool{}
	var out []AssumeGrant
	for _, st := range doc.Statement {
		if strings.ToLower(st.Effect) != "allow" || !statementHasAssume(st) {
			continue
		}
		for _, g := range grantsForPrincipal(st.Principal) {
			if denied[g.PrincipalKey] || seen[g.PrincipalKey] {
				continue
			}
			seen[g.PrincipalKey] = true
			out = append(out, g)
		}
	}
	return out
}

// grantsForPrincipal maps one statement's Principal element to zero or more
// grants, independent of effect. Service and federated principals yield none.
func grantsForPrincipal(p principalValue) []AssumeGrant {
	var out []AssumeGrant
	if p.Wildcard {
		out = append(out, AssumeGrant{
			PrincipalKey: AnyIdentityKey,
			Confidence:   0.7,
			Attributes:   map[string]any{"unscoped": true},
		})
	}
	// Service principals (*.amazonaws.com) are excluded (R4.3). The Service
	// field is simply never turned into a grant.
	for _, awsP := range p.AWS {
		if g, ok := grantForAWSPrincipal(awsP); ok {
			out = append(out, g)
		}
	}
	return out
}

// grantForAWSPrincipal classifies a single "AWS" principal value.
func grantForAWSPrincipal(v string) (AssumeGrant, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return AssumeGrant{}, false
	}
	// Bare 12-digit account id (R4.2).
	if len(v) == 12 && isDigits(v) {
		return AssumeGrant{
			PrincipalKey: accountKeyForTrust(v),
			Confidence:   0.7,
			Attributes:   map[string]any{"account_grant": true},
		}, true
	}
	if strings.HasPrefix(v, "arn:") {
		parts := strings.SplitN(v, ":", 6)
		if len(parts) == 6 {
			acct := parts[4]
			resource := parts[5]
			// Account root ARN: arn:aws:iam::ACCT:root (R4.2).
			if resource == "root" {
				return AssumeGrant{
					PrincipalKey: accountKeyForTrust(acct),
					Confidence:   0.7,
					Attributes:   map[string]any{"account_grant": true},
				}, true
			}
			// Named IAM user/role ARN (R4.1), confidence 1.0.
			return AssumeGrant{PrincipalKey: v, Confidence: 1.0}, true
		}
	}
	return AssumeGrant{}, false
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}
