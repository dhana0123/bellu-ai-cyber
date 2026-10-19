package classify

import (
	"context"
	"reflect"
	"testing"

	"github.com/bellu/bellu-ai-cyber/internal/model"
)

// store is a small helper for building a DataStore entity from metadata only
// (name + attributes). It deliberately never carries any field representing the
// store's stored contents — the classifier contract (Req 9.1) is that
// classification is derived from metadata alone.
func store(id, name string, attrs map[string]any) model.Entity {
	return model.Entity{
		Kind:        model.KindDataStore,
		CanonicalID: id,
		Name:        name,
		Provider:    "aws",
		Attributes:  attrs,
	}
}

// identity is a non-DataStore entity used as the source of access edges. The
// classifier only ever classifies DataStore entities; identities exist to be
// resolved into reader/writer sets.
func identity(id, name string) model.Entity {
	return model.Entity{
		Kind:        model.KindIdentity,
		CanonicalID: id,
		Name:        name,
		Provider:    "aws",
	}
}

// accessEdge builds an IAM access edge from an identity to a datastore.
func accessEdge(rel model.Relation, src, dst string) model.Edge {
	return model.Edge{
		Relation:       rel,
		SrcCanonicalID: src,
		DstCanonicalID: dst,
		Confidence:     1.0,
	}
}

// mustClassifyOne runs the classifier and returns the single DataClass result,
// failing the test if the count is not exactly one.
func mustClassifyOne(t *testing.T, in ClassifyInput) DataClass {
	t.Helper()
	c := NewRuleClassifier()
	got, err := c.Classify(context.Background(), in)
	if err != nil {
		t.Fatalf("Classify returned error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 DataClass, got %d: %+v", len(got), got)
	}
	return got[0]
}

// TestClassify_ClassSelection drives metadata inputs through the classifier and
// asserts the expected class and confidence tier. It covers name-based
// classification for every token family, IaC/attribute hints, public exposure,
// and the Unknown fallback to ClassInternal (Req 9.4).
//
// Validates: Requirements 9.1, 9.4
func TestClassify_ClassSelection(t *testing.T) {
	cases := []struct {
		name       string
		entity     model.Entity
		wantClass  string
		wantConf   float64
		wantByRule string
	}{
		// --- name-based classification: one representative token per family ---
		{
			name:      "name secrets token",
			entity:    store("ds:secrets", "prod-vault-secrets", nil),
			wantClass: ClassSecrets,
			wantConf:  0.6,
		},
		{
			name:      "name pii token",
			entity:    store("ds:pii", "customer-profile-store", nil),
			wantClass: ClassPII,
			wantConf:  0.6,
		},
		{
			name:      "name logs token",
			entity:    store("ds:logs", "app-audit-log", nil),
			wantClass: ClassLogs,
			wantConf:  0.6,
		},
		{
			name:      "name backups token",
			entity:    store("ds:backups", "nightly-snapshot-archive", nil),
			wantClass: ClassBackups,
			wantConf:  0.6,
		},
		{
			name:      "name public token",
			entity:    store("ds:public", "www-static-assets", nil),
			wantClass: ClassPublic,
			wantConf:  0.6,
		},
		// --- IaC/attribute hint classification wins over naming heuristics ---
		{
			name:      "iac attribute hint overrides name",
			entity:    store("ds:iac", "app-logs-bucket", map[string]any{"data_class": "pii"}),
			wantClass: ClassPII,
			wantConf:  0.9,
		},
		{
			name:      "iac classification synonym normalized",
			entity:    store("ds:iac2", "generic-bucket", map[string]any{"classification": "credentials"}),
			wantClass: ClassSecrets,
			wantConf:  0.9,
		},
		// --- public-exposure classification from attributes ---
		{
			name:      "public exposure attribute",
			entity:    store("ds:exposed", "generic-bucket", map[string]any{"publicly_accessible": true}),
			wantClass: ClassPublic,
			wantConf:  0.7,
		},
		{
			name:      "exposure string public",
			entity:    store("ds:exposed2", "generic-bucket", map[string]any{"exposure": "public"}),
			wantClass: ClassPublic,
			wantConf:  0.7,
		},
		// --- Unknown fallback: no rule matches -> ClassInternal (Req 9.4) ---
		{
			name:      "unknown fallback to internal",
			entity:    store("ds:unknown", "misc-bucket-42", nil),
			wantClass: ClassInternal,
			wantConf:  0.3,
		},
		{
			name:      "unknown with unrelated attributes",
			entity:    store("ds:unknown2", "another-bucket", map[string]any{"region": "us-east-1", "size": 1024}),
			wantClass: ClassInternal,
			wantConf:  0.3,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dc := mustClassifyOne(t, ClassifyInput{Entities: []model.Entity{tc.entity}})

			if dc.CanonicalID != tc.entity.CanonicalID {
				t.Errorf("CanonicalID = %q, want %q", dc.CanonicalID, tc.entity.CanonicalID)
			}
			if dc.Class != tc.wantClass {
				t.Errorf("Class = %q, want %q", dc.Class, tc.wantClass)
			}
			if dc.Confidence != tc.wantConf {
				t.Errorf("Confidence = %v, want %v", dc.Confidence, tc.wantConf)
			}
			// Stage A always stamps the rule producer as classified_by.
			if dc.ClassifiedBy != "rule:classify-stage-a" {
				t.Errorf("ClassifiedBy = %q, want %q", dc.ClassifiedBy, "rule:classify-stage-a")
			}
		})
	}
}

// TestClassify_ReaderWriterDerivation asserts reader/writer sets are derived
// from IAM access edges only, with CAN_ADMIN implying both read and write.
//
// Validates: Requirements 9.1
func TestClassify_ReaderWriterDerivation(t *testing.T) {
	ds := store("ds:1", "customer-pii-store", nil)
	entities := []model.Entity{
		ds,
		identity("id:reader", "read-role"),
		identity("id:writer", "write-role"),
		identity("id:admin", "admin-role"),
	}
	edges := []model.Edge{
		accessEdge(model.RelCanRead, "id:reader", "ds:1"),
		accessEdge(model.RelCanWrite, "id:writer", "ds:1"),
		accessEdge(model.RelCanAdmin, "id:admin", "ds:1"),
	}

	dc := mustClassifyOne(t, ClassifyInput{Entities: entities, Edges: edges})

	// Admin implies read, so readers = {reader, admin}; writers = {writer, admin}.
	wantReaders := []string{"id:admin", "id:reader"}
	wantWriters := []string{"id:admin", "id:writer"}
	if !reflect.DeepEqual(dc.Readers, wantReaders) {
		t.Errorf("Readers = %v, want %v", dc.Readers, wantReaders)
	}
	if !reflect.DeepEqual(dc.Writers, wantWriters) {
		t.Errorf("Writers = %v, want %v", dc.Writers, wantWriters)
	}
	// Name-based classification still applies alongside access derivation.
	if dc.Class != ClassPII {
		t.Errorf("Class = %q, want %q", dc.Class, ClassPII)
	}
}

// TestClassify_AccessEdgesIgnoredForNonStores asserts edges whose destination is
// not a DataStore do not pollute any reader/writer set, confirming derivation is
// scoped to the store being classified.
//
// Validates: Requirements 9.1
func TestClassify_AccessEdgesIgnoredForNonStores(t *testing.T) {
	entities := []model.Entity{
		store("ds:1", "misc-bucket", nil),
		identity("id:a", "role-a"),
		identity("id:b", "role-b"),
	}
	edges := []model.Edge{
		accessEdge(model.RelCanRead, "id:a", "ds:1"),
		// Edge to a non-store destination must be ignored entirely.
		accessEdge(model.RelCanWrite, "id:b", "id:a"),
	}

	dc := mustClassifyOne(t, ClassifyInput{Entities: entities, Edges: edges})

	if want := []string{"id:a"}; !reflect.DeepEqual(dc.Readers, want) {
		t.Errorf("Readers = %v, want %v", dc.Readers, want)
	}
	if dc.Writers != nil {
		t.Errorf("Writers = %v, want nil (no write edge targets the store)", dc.Writers)
	}
}

// TestClassify_OnlyDataStoresClassified asserts the classifier emits one result
// per DataStore and skips all other entity kinds — the input carries no notion
// of datastore contents, so classification is purely a function of the
// entity/edge metadata shape (Req 9.1).
//
// Validates: Requirements 9.1
func TestClassify_OnlyDataStoresClassified(t *testing.T) {
	entities := []model.Entity{
		store("ds:a", "audit-logs", nil),
		store("ds:b", "backup-archive", nil),
		identity("id:x", "role-x"),
		{Kind: model.KindCompute, CanonicalID: "cmp:1", Name: "logs-worker"},
	}

	c := NewRuleClassifier()
	got, err := c.Classify(context.Background(), ClassifyInput{Entities: entities})
	if err != nil {
		t.Fatalf("Classify error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 DataClass results, got %d: %+v", len(got), got)
	}
	// Deterministic order by canonical id.
	if got[0].CanonicalID != "ds:a" || got[1].CanonicalID != "ds:b" {
		t.Errorf("unexpected order/ids: %q, %q", got[0].CanonicalID, got[1].CanonicalID)
	}
}

// TestClassify_MetadataOnly_ContentsIrrelevant demonstrates Req 9.1 structurally:
// ClassifyInput exposes no field for datastore contents, so two runs with
// identical entity/edge metadata necessarily produce identical classifications.
// Any hypothetical difference in a store's stored data cannot reach the
// classifier because the input type cannot represent it. We prove this by
// classifying the same metadata twice and asserting an identical result.
//
// Validates: Requirements 9.1
func TestClassify_MetadataOnly_ContentsIrrelevant(t *testing.T) {
	// Guard the structural contract: the only inputs are Entities and Edges.
	// If a contents-bearing field were ever added to ClassifyInput, this test
	// should be revisited.
	fields := reflect.TypeOf(ClassifyInput{}).NumField()
	if fields != 2 {
		t.Fatalf("ClassifyInput has %d fields; expected exactly 2 (Entities, Edges) so no datastore contents can be passed", fields)
	}

	in := ClassifyInput{
		Entities: []model.Entity{
			store("ds:1", "customer-pii-store", map[string]any{"region": "us-east-1"}),
			identity("id:r", "reader"),
		},
		Edges: []model.Edge{accessEdge(model.RelCanRead, "id:r", "ds:1")},
	}

	c := NewRuleClassifier()
	first, err := c.Classify(context.Background(), in)
	if err != nil {
		t.Fatalf("first Classify error: %v", err)
	}
	second, err := c.Classify(context.Background(), in)
	if err != nil {
		t.Fatalf("second Classify error: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("classification differed across identical-metadata runs:\n first=%+v\nsecond=%+v", first, second)
	}
	if len(first) != 1 || first[0].Class != ClassPII {
		t.Fatalf("expected single pii classification, got %+v", first)
	}
}

// TestClassify_EvidenceDerivedFromRawEvents asserts evidence raw-event ids are
// carried through from the entity's Evidence (metadata), de-duplicated and
// sorted, and that nothing beyond raw-event ids is read.
//
// Validates: Requirements 9.1
func TestClassify_EvidenceDerivedFromRawEvents(t *testing.T) {
	e := store("ds:1", "audit-logs", nil)
	e.Evidence = []model.Evidence{
		{RawEventID: "re-2"},
		{RawEventID: "re-1"},
		{RawEventID: "re-2"}, // duplicate
		{RawEventID: ""},     // empty ignored
	}

	dc := mustClassifyOne(t, ClassifyInput{Entities: []model.Entity{e}})

	if want := []string{"re-1", "re-2"}; !reflect.DeepEqual(dc.Evidence, want) {
		t.Errorf("Evidence = %v, want %v", dc.Evidence, want)
	}
}
