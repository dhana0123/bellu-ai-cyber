package raw

import (
	"context"
	"errors"
)

// S3Store is an interface-satisfying skeleton for an S3-compatible,
// content-addressed RawStore. It is reserved for a later phase: the real
// implementation (bucket, client, credentials) is intentionally deferred so
// that Phase 1 adds no S3 runtime dependency. All methods currently return
// errNotImplemented.
type S3Store struct {
	// Bucket is the target S3 bucket name. Reserved for the later
	// implementation.
	Bucket string
	// Prefix is an optional key prefix under which objects are stored.
	// Reserved for the later implementation.
	Prefix string
}

// errNotImplemented is returned by every S3Store method until the S3 backend
// is implemented in a later phase.
var errNotImplemented = errors.New("raw: S3Store is not implemented yet")

// Put is not yet implemented.
func (s *S3Store) Put(ctx context.Context, source string, payload []byte) (RawRef, error) {
	return RawRef{}, errNotImplemented
}

// Get is not yet implemented.
func (s *S3Store) Get(ctx context.Context, ref RawRef) ([]byte, error) {
	return nil, errNotImplemented
}

// Ensure S3Store satisfies RawStore even as a stub, so the later
// implementation drops in without interface drift.
var _ RawStore = (*S3Store)(nil)
