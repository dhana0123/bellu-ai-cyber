package aws

import "strings"

// InstanceProfileMapper resolves an EC2 instance profile ARN to the IAM role
// ARN(s) the profile actually contains, using collected
// aws_iam_instance_profile data (R3.3-R3.5). When no collected mapping exists,
// it falls back to the historical name-substitution heuristic and signals the
// caller to lower the edge confidence.
type InstanceProfileMapper struct {
	// profiles maps a normalized instance-profile ARN to its contained role
	// ARNs.
	profiles map[string][]string
}

// NewInstanceProfileMapper builds a mapper from a profile ARN -> role ARNs
// index. Keys should be normalized by the caller (partition/path) so lookups
// tolerate ARN variants.
func NewInstanceProfileMapper(profiles map[string][]string) *InstanceProfileMapper {
	if profiles == nil {
		profiles = map[string][]string{}
	}
	return &InstanceProfileMapper{profiles: profiles}
}

// Resolve returns the role ARN(s) an instance profile grants, the confidence to
// stamp on the resulting USES edge(s), and whether the result came from the
// name-substitution fallback.
//
//   - When collected data maps the profile to >=1 role, those roles are
//     returned with confidence 1.0 and fallback=false (one USES edge per role).
//   - Otherwise the profile ARN is rewritten to a role ARN by substituting
//     ":instance-profile/" with ":role/" and returned with confidence 0.7 and
//     fallback=true.
func (m *InstanceProfileMapper) Resolve(profileARN string) (roles []string, confidence float64, fallback bool) {
	if profileARN == "" {
		return nil, 0, false
	}
	key := NormalizeProfileARN(profileARN)
	if rs, ok := m.profiles[key]; ok && len(rs) > 0 {
		return rs, 1.0, false
	}
	// Fallback: assume the role shares the profile's name (historical heuristic).
	role := strings.Replace(profileARN, ":instance-profile/", ":role/", 1)
	if role == profileARN {
		// Not an instance-profile ARN we can rewrite; nothing to link.
		return nil, 0, false
	}
	return []string{role}, 0.7, true
}

// NormalizeProfileARN canonicalizes an instance-profile ARN partition so
// lookups tolerate aws / aws-cn / aws-us-gov variants. It does not strip IAM
// path, since instance-profile membership is keyed by the full ARN as collected.
func NormalizeProfileARN(arn string) string {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 6 || parts[0] != "arn" {
		return arn
	}
	parts[1] = "aws"
	return strings.Join(parts, ":")
}
