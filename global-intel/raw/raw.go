// Package raw provides content-addressed storage of unmodified fetched
// intelligence payloads, addressed by the sha256 hash of their content.
//
// This file defines the storage interface and reference type that the fetch
// and parser packages depend on. Concrete implementations (filesystem for dev,
// an S3-compatible store for later) are provided elsewhere in this package.
package raw

import "context"

// RawStore stores unmodified fetched payloads addressed by content hash.
//
// Storing identical bytes twice yields the same RawRef and is a no-op (the
// hash is the identity), so re-fetching unchanged data does not duplicate
// storage. Stored payloads are immutable.
type RawStore interface {
	// Put stores bytes and returns a reference keyed by the sha256 of the
	// content. Storing identical bytes twice yields the same RawRef and is a
	// no-op.
	Put(ctx context.Context, source string, payload []byte) (RawRef, error)
	// Get returns the exact bytes originally stored for a reference.
	Get(ctx context.Context, ref RawRef) ([]byte, error)
}

// RawRef is a content-addressed reference to a stored payload. The Hash is the
// identity of the payload.
type RawRef struct {
	Source string // e.g. "osv"
	Hash   string // e.g. "sha256:..."
	Size   int64
}
