package search

import (
	"context"

	"github.com/bellu/bellu-ai-cyber/global-intel/canonical"
)

// CorpusReader is the read side of the canonical store the index projects from.
// database.IntelStore satisfies it via AllVulns, so the index is always
// rebuildable from the authoritative Postgres corpus without the search package
// depending on the database package.
type CorpusReader interface {
	AllVulns(ctx context.Context) ([]canonical.CanonicalVuln, error)
}

// StoreSource adapts a CorpusReader into a DocumentSource by projecting every
// canonical vulnerability into an index Document. It is what `update`/Rebuild
// pass to the indexer so indexing and rebuilds share one projection path.
type StoreSource struct {
	Reader CorpusReader
}

// NewStoreSource wraps a CorpusReader as a DocumentSource.
func NewStoreSource(r CorpusReader) StoreSource { return StoreSource{Reader: r} }

// Documents returns the full corpus projected for indexing.
func (s StoreSource) Documents(ctx context.Context) ([]Document, error) {
	vulns, err := s.Reader.AllVulns(ctx)
	if err != nil {
		return nil, err
	}
	return FromCanonicalMany(vulns), nil
}

// compile-time assertion.
var _ DocumentSource = StoreSource{}
