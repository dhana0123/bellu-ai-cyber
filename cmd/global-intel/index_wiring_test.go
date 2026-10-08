package main

import (
	"context"
	"errors"
	"testing"

	"github.com/bellu/bellu-ai-cyber/global-intel/canonical"
	"github.com/bellu/bellu-ai-cyber/global-intel/search"
)

// fakePipelineIndexer records EnsureIndex/Index calls and can be made to fail,
// so the folded-indexing step can be unit-tested without a live OpenSearch.
type fakePipelineIndexer struct {
	ensured     int
	indexedDocs [][]search.Document
	ensureErr   error
	indexErr    error
}

func (f *fakePipelineIndexer) EnsureIndex(context.Context) error {
	f.ensured++
	return f.ensureErr
}

func (f *fakePipelineIndexer) Index(_ context.Context, docs []search.Document) error {
	if f.indexErr != nil {
		return f.indexErr
	}
	f.indexedDocs = append(f.indexedDocs, docs)
	return nil
}

func sampleVulns() []canonical.CanonicalVuln {
	return []canonical.CanonicalVuln{
		{
			ID:       "CVE-2024-0001",
			Aliases:  []string{"CVE-2024-0001"},
			Summary:  "example",
			Severity: canonical.SevHigh,
			Affected: []canonical.AffectedPackage{{
				Purl: "pkg:pypi/requests", Ecosystem: "PyPI",
				Ranges: []canonical.SemverRange{{Introduced: "0", Fixed: "2.31.0"}},
			}},
		},
	}
}

// TestIndexCorpusEnsuresThenIndexes: a successful index step ensures the index
// then upserts the projected documents for the persisted set.
func TestIndexCorpusEnsuresThenIndexes(t *testing.T) {
	f := &fakePipelineIndexer{}
	pl := &pipeline{indexer: f}

	if err := pl.indexCorpus(context.Background(), sampleVulns()); err != nil {
		t.Fatalf("indexCorpus: %v", err)
	}
	if f.ensured != 1 {
		t.Fatalf("EnsureIndex called %d times, want 1", f.ensured)
	}
	if len(f.indexedDocs) != 1 || len(f.indexedDocs[0]) != 1 {
		t.Fatalf("expected 1 Index call with 1 doc, got %+v", f.indexedDocs)
	}
	if got := f.indexedDocs[0][0]; got.ID != "CVE-2024-0001" || len(got.Purls) != 1 || got.Purls[0] != "pkg:pypi/requests" {
		t.Fatalf("indexed doc not projected correctly: %+v", got)
	}
}

// TestIndexCorpusEnsureFailurePropagates: an EnsureIndex failure is returned
// and no Index call is made.
func TestIndexCorpusEnsureFailurePropagates(t *testing.T) {
	f := &fakePipelineIndexer{ensureErr: errors.New("cluster down")}
	pl := &pipeline{indexer: f}
	err := pl.indexCorpus(context.Background(), sampleVulns())
	if err == nil || len(f.indexedDocs) != 0 {
		t.Fatalf("expected ensure failure to short-circuit; err=%v indexed=%+v", err, f.indexedDocs)
	}
}

// TestIndexCorpusIndexFailurePropagates: an Index failure surfaces an error so
// run() can tag it INDEX_FAILED.
func TestIndexCorpusIndexFailurePropagates(t *testing.T) {
	f := &fakePipelineIndexer{indexErr: errors.New("bulk rejected")}
	pl := &pipeline{indexer: f}
	if err := pl.indexCorpus(context.Background(), sampleVulns()); err == nil {
		t.Fatal("expected index failure to propagate")
	}
}
