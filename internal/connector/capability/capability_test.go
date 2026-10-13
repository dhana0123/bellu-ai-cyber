package capability

import (
	"strings"
	"testing"
)

// TestCatalogContainsExpectedCapabilities asserts the catalog defines exactly
// the modular capabilities (core, eks, workload, dspm, ai).
func TestCatalogContainsFourCapabilities(t *testing.T) {
	want := []Capability{Core, EKS, Workload, DSPM, AI}
	if len(Catalog) != len(want) {
		t.Fatalf("catalog has %d capabilities, want %d", len(Catalog), len(want))
	}
	for _, c := range want {
		if _, ok := Catalog[c]; !ok {
			t.Errorf("catalog missing capability %q", c)
		}
	}
}

// TestCoreCoversReadDomains asserts the core capability covers account, regions,
// EC2, VPC, IAM, and S3 reads.
func TestCoreCoversReadDomains(t *testing.T) {
	core := Catalog[Core]
	needPrefix := []string{"sts:", "account:", "ec2:", "iam:", "s3:"}
	for _, pfx := range needPrefix {
		found := false
		for _, p := range core.Permissions {
			if strings.HasPrefix(p, pfx) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("core capability missing a %s read permission", pfx)
		}
	}
	// VPC reads come from the ec2:Describe* family (covers DescribeVpcs);
	// assert the family is present.
	if !contains(core.Permissions, "ec2:Describe*") {
		t.Error("core capability missing ec2:Describe* (covers VPC/EC2 reads)")
	}
}

// TestNoAdministratorOrWildcard asserts no capability grants AdministratorAccess
// or a wildcard action.
func TestNoAdministratorOrWildcard(t *testing.T) {
	for name, def := range Catalog {
		for _, p := range def.Permissions {
			// A bare "*" or a full-service wildcard like "s3:*" grants more than
			// read. Service-scoped read wildcards (e.g. "ec2:Describe*",
			// "iam:List*") are standard least-privilege read grants and allowed.
			if p == "*" || strings.HasSuffix(p, ":*") {
				t.Errorf("capability %q contains over-broad wildcard permission %q", name, p)
			}
			if !isReadAction(p) {
				t.Errorf("capability %q has non-read action %q", name, p)
			}
			if strings.EqualFold(p, "AdministratorAccess") {
				t.Errorf("capability %q grants AdministratorAccess", name)
			}
		}
	}
}

// TestProbesMatchPermissions asserts init populated one probe per permission.
func TestProbesMatchPermissions(t *testing.T) {
	for name, def := range Catalog {
		if len(def.Probes) != len(def.Permissions) {
			t.Errorf("capability %q has %d probes, want %d", name, len(def.Probes), len(def.Permissions))
		}
	}
}

// TestDefaultSelection asserts the default selection is core plus the read-only
// compute-visibility capabilities (eks, workload), with DSPM left opt-in.
func TestDefaultSelection(t *testing.T) {
	got := DefaultSelection()
	want := map[string]bool{
		string(Core):     true,
		string(EKS):      true,
		string(Workload): true,
		string(AI):       true,
	}
	if len(got) != len(want) {
		t.Fatalf("DefaultSelection() = %v, want %d capabilities", got, len(want))
	}
	for _, c := range got {
		if !want[c] {
			t.Errorf("DefaultSelection() returned unexpected capability %q", c)
		}
		delete(want, c)
	}
	if len(want) != 0 {
		t.Errorf("DefaultSelection() missing capabilities: %v", want)
	}
}

func TestKnownAndLookup(t *testing.T) {
	if !Known("core") || Known("bogus") {
		t.Error("Known did not classify core/bogus correctly")
	}
	if _, ok := Lookup("eks"); !ok {
		t.Error("Lookup(eks) not found")
	}
	if _, ok := Lookup("bogus"); ok {
		t.Error("Lookup(bogus) unexpectedly found")
	}
}

func contains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}

// isReadAction reports whether an IAM action is read-only (its verb starts with
// a known read prefix). This keeps the capability catalog read-only.
func isReadAction(action string) bool {
	i := strings.IndexByte(action, ':')
	if i < 0 || i+1 >= len(action) {
		return false
	}
	verb := action[i+1:]
	for _, pfx := range []string{"Describe", "List", "Get", "GenerateCredentialReport", "GenerateServiceLastAccessedDetails"} {
		if strings.HasPrefix(verb, pfx) {
			return true
		}
	}
	return false
}
