package resolve

import (
	"testing"

	"github.com/bellu/bellu-ai-cyber/internal/model"
)

// Feature: cloud-attack-path-graph, Property 5: IAM/partition-insensitive ARNs
// normalize to the same key (R3.1, R3.2).
func TestNormalizeAWSARN_IAMPathAndPartitionEquivalence(t *testing.T) {
	cases := []struct {
		name string
		a    string
		b    string
	}{
		{
			name: "iam role path stripped",
			a:    "arn:aws:iam::111122223333:role/team/app-role",
			b:    "arn:aws:iam::111122223333:role/app-role",
		},
		{
			name: "iam user path stripped",
			a:    "arn:aws:iam::111122223333:user/division/dept/alice",
			b:    "arn:aws:iam::111122223333:user/alice",
		},
		{
			name: "partition canonicalized (gov)",
			a:    "arn:aws-us-gov:s3:::my-bucket",
			b:    "arn:aws:s3:::my-bucket",
		},
		{
			name: "partition canonicalized (cn)",
			a:    "arn:aws-cn:iam::111122223333:role/team/app-role",
			b:    "arn:aws:iam::111122223333:role/app-role",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			na := NormalizeKey(model.KindIdentity, "aws", c.a)
			nb := NormalizeKey(model.KindIdentity, "aws", c.b)
			if na != nb {
				t.Errorf("expected %q and %q to normalize equal; got %q vs %q", c.a, c.b, na, nb)
			}
		})
	}
}

// Feature: cloud-attack-path-graph, Property 6: ARN normalization is idempotent
// (R3.1, R3.2).
func TestNormalizeAWSARN_Idempotent(t *testing.T) {
	inputs := []string{
		"arn:aws:iam::111122223333:role/team/app-role",
		"arn:aws-cn:iam::111122223333:user/division/alice",
		"arn:aws-us-gov:s3:::My-Bucket",
		"arn:aws:ec2:us-east-1:111122223333:instance/i-0abc",
		"not-an-arn",
		"arn:too:few:segments",
	}
	for _, in := range inputs {
		once := normalizeAWSARN(in)
		twice := normalizeAWSARN(once)
		if once != twice {
			t.Errorf("normalizeAWSARN not idempotent for %q: once=%q twice=%q", in, once, twice)
		}
	}
}

// Feature: cloud-attack-path-graph, Property 7: Case-sensitive segments are
// never folded or wildcarded (R3.6).
func TestNormalizeAWSARN_PreservesResourceNameCase(t *testing.T) {
	cases := []struct {
		arn      string
		wantName string // substring that must survive verbatim
	}{
		{"arn:aws:s3:::My-Mixed-Case-Bucket", "My-Mixed-Case-Bucket"},
		{"arn:aws:iam::111122223333:role/Team/AdminRole", "AdminRole"},
		{"arn:aws:iam::111122223333:user/Alice", "Alice"},
	}
	for _, c := range cases {
		got := normalizeAWSARN(c.arn)
		if !containsExact(got, c.wantName) {
			t.Errorf("normalizeAWSARN(%q) = %q, expected to preserve %q verbatim", c.arn, got, c.wantName)
		}
	}
}

// containsExact reports whether needle appears in s as an exact (case-sensitive)
// substring.
func containsExact(s, needle string) bool {
	for i := 0; i+len(needle) <= len(s); i++ {
		if s[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
