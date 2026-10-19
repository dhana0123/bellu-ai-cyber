package mcp

import (
	"reflect"
	"strings"
	"testing"
)

// The tool-input and tool-output types are the public contract of the six MCP
// tools. These structural tests pin two tenant-isolation and ergonomics
// guarantees that cannot regress silently:
//
//   - No tool input carries an organization field, so a client has no channel
//     to name a tenant. The org is always the one the mount resolves from the
//     authenticated principal (Req 10.2, Req 16.2). This is checked by
//     reflection rather than by a comment so adding an Org/OrgID field to any
//     input type fails the build's test.
//   - Every entity-shaped output carries a Name alongside its identifier
//     (Req 10.6), so a caller never has to resolve the name separately.
//
// They run without a database or a server instance because the contract lives
// in the type definitions themselves.

// orgLikeFieldNames are the field names that would let a client smuggle a tenant
// in through a tool input. None may appear on any input type (Req 10.2).
var orgLikeFieldNames = map[string]bool{
	"org":            true,
	"orgid":          true,
	"org_id":         true,
	"organization":   true,
	"organizationid": true,
	"tenant":         true,
	"tenantid":       true,
}

// TestToolInputsCarryNoOrgField asserts, by reflection, that not one of the six
// tools' input types exposes an org/tenant field. The mount resolves the tenant
// from the authenticated principal and passes it as the org argument to the
// tool method; a client that puts an organization in its JSON body has nowhere
// for it to land, so it is structurally ignored (Req 10.2, Req 16.2).
func TestToolInputsCarryNoOrgField(t *testing.T) {
	inputs := []any{
		FindEntitiesInput{},
		GetEntityInput{},
		GetNeighborsInput{},
		EntityHistoryInput{},
		RecentChangesInput{},
	}
	for _, in := range inputs {
		rt := reflect.TypeOf(in)
		t.Run(rt.Name(), func(t *testing.T) {
			for i := 0; i < rt.NumField(); i++ {
				f := rt.Field(i)
				// Check both the Go field name and the json tag so neither a
				// renamed field nor an org-named json key can sneak in.
				goName := strings.ToLower(f.Name)
				jsonName := strings.ToLower(jsonTagName(f.Tag.Get("json")))
				if orgLikeFieldNames[goName] {
					t.Fatalf("%s has org-like field %q; tenant must come from the authenticated principal, never the client (Req 10.2)", rt.Name(), f.Name)
				}
				if jsonName != "" && orgLikeFieldNames[jsonName] {
					t.Fatalf("%s field %q has org-like json tag %q; tenant must come from the authenticated principal, never the client (Req 10.2)", rt.Name(), f.Name, jsonName)
				}
			}
		})
	}
}

// TestToolMethodsResolveOrgAsFirstBusinessArg confirms the tenant-isolation seam
// at the method level: every projection/log tool method takes org int64 as its
// first business argument (after the context). That signature is what forces the
// mount to supply the resolved tenant and leaves a client no way to influence it
// (Req 10.2, Req 16.2). A method that dropped the org argument would fail here.
func TestToolMethodsResolveOrgAsFirstBusinessArg(t *testing.T) {
	st := reflect.TypeOf(&Server{})
	// method name -> whether it is one of the org-scoped tools.
	orgScoped := []string{
		"FindEntities",
		"GetEntity",
		"GetNeighbors",
		"EntityHistory",
		"RecentChanges",
		"DescribeSchema",
	}
	for _, name := range orgScoped {
		m, ok := st.MethodByName(name)
		if !ok {
			t.Fatalf("Server has no method %q", name)
		}
		// Signature is func(*Server, context.Context, int64, ...): receiver at 0,
		// context at 1, org at 2.
		ft := m.Func.Type()
		if ft.NumIn() < 3 {
			t.Fatalf("%s has too few arguments to carry an org", name)
		}
		if got := ft.In(2); got.Kind() != reflect.Int64 {
			t.Fatalf("%s second argument is %s, want int64 org (the resolved tenant) (Req 16.2)", name, got)
		}
	}
}

// TestEntityOutputsCarryName asserts every entity-shaped output type exposes a
// Name field alongside its canonical id (Req 10.6), so a tool result always
// carries the human-readable name and a caller never has to resolve it with a
// second lookup.
func TestEntityOutputsCarryName(t *testing.T) {
	withName := []any{
		EntityRef{},
		EntityDetail{},
	}
	for _, v := range withName {
		rt := reflect.TypeOf(v)
		t.Run(rt.Name(), func(t *testing.T) {
			if _, ok := rt.FieldByName("Name"); !ok {
				t.Fatalf("%s has no Name field; name must travel alongside the id (Req 10.6)", rt.Name())
			}
			if _, ok := rt.FieldByName("CanonicalID"); !ok {
				t.Fatalf("%s has no CanonicalID field; the id anchors the name (Req 10.6)", rt.Name())
			}
		})
	}
}

// TestNeighborsNodesCarryName asserts the adjacency result's neighbor nodes are
// the name-carrying EntityRef, so get_neighbors surfaces each neighbor's name
// alongside its id (Req 10.6) just like find_entities does.
func TestNeighborsNodesCarryName(t *testing.T) {
	nodesField, ok := reflect.TypeOf(Neighbors{}).FieldByName("Nodes")
	if !ok {
		t.Fatal("Neighbors has no Nodes field")
	}
	if nodesField.Type.Kind() != reflect.Slice {
		t.Fatalf("Neighbors.Nodes is %s, want a slice", nodesField.Type.Kind())
	}
	elem := nodesField.Type.Elem()
	if _, ok := elem.FieldByName("Name"); !ok {
		t.Fatalf("Neighbors.Nodes element %s has no Name field; neighbors must carry names (Req 10.6)", elem.Name())
	}
}

// jsonTagName returns the name portion of a struct json tag, dropping any
// ",omitempty" style options. An empty or "-" tag yields "".
func jsonTagName(tag string) string {
	if tag == "" || tag == "-" {
		return ""
	}
	if i := strings.IndexByte(tag, ','); i >= 0 {
		return tag[:i]
	}
	return tag
}
