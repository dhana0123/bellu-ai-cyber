package capability

import (
	"strings"
	"testing"
)

// TestCatalogContainsFourCapabilities asserts the catalog defines exactly the
// four modular capabilities.
func TestCatalogContainsFourCapabilities(t *testing.T) {
	want := []Capability{Core, EKS, Workload, DSPM}
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
	// VPC reads are ec2:Describe* actions (DescribeVpcs); assert explicitly.
	if !contains(core.Permissions, "ec2:DescribeVpcs") {
		t.Error("core capability missing ec2:DescribeVpcs (VPC read)")
	}
}

// TestNoAdministratorOrWildcard asserts no capability grants AdministratorAccess
// or a wildcard action.
func TestNoAdministratorOrWildcard(t *testing.T) {
	for name, def := range Catalog {
		for _, p := range def.Permissions {
			if p == "*" || strings.Contains(p, "*") {
				t.Errorf("capability %q contains wildcard permission %q", name, p)
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

// TestDefaultSelection asserts the default selection is exactly ["core"].
func TestDefaultSelection(t *testing.T) {
	got := DefaultSelection()
	if len(got) != 1 || got[0] != string(Core) {
		t.Errorf("DefaultSelection() = %v, want [core]", got)
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
