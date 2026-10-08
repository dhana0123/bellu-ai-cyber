package raw

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// hashPrefix is the algorithm prefix used in RawRef.Hash.
const hashPrefix = "sha256:"

// FSStore is a filesystem-backed, content-addressed RawStore. Payloads are
// written to files named by the sha256 hex digest of their content, so
// identical bytes map to the same file and storing them again is a no-op.
// Stored files are treated as immutable: Put never overwrites an existing
// object once its content hash is present.
type FSStore struct {
	// root is the base directory under which payloads are stored.
	root string
}

// NewFSStore returns a filesystem RawStore rooted at dir, creating the
// directory if it does not already exist.
func NewFSStore(dir string) (*FSStore, error) {
	if dir == "" {
		return nil, fmt.Errorf("raw: fsstore root directory must not be empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("raw: create fsstore root: %w", err)
	}
	return &FSStore{root: dir}, nil
}

// Put stores payload and returns a reference keyed by the sha256 hash of its
// content. If an object with the same content hash already exists it is left
// untouched (no-op) and the same reference is returned.
func (s *FSStore) Put(ctx context.Context, source string, payload []byte) (RawRef, error) {
	if err := ctx.Err(); err != nil {
		return RawRef{}, err
	}

	sum := sha256.Sum256(payload)
	hexSum := hex.EncodeToString(sum[:])
	ref := RawRef{
		Source: source,
		Hash:   hashPrefix + hexSum,
		Size:   int64(len(payload)),
	}

	path := s.objectPath(hexSum)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return RawRef{}, fmt.Errorf("raw: create object dir: %w", err)
	}

	// Immutable + no-op for identical bytes: if the object already exists,
	// leave it in place and return the existing reference.
	if _, err := os.Stat(path); err == nil {
		return ref, nil
	} else if !os.IsNotExist(err) {
		return RawRef{}, fmt.Errorf("raw: stat object: %w", err)
	}

	// Write atomically via a temp file + rename so a partial write never leaves
	// a corrupt object at the content-addressed path.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-"+hexSum+"-*")
	if err != nil {
		return RawRef{}, fmt.Errorf("raw: create temp object: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once renamed

	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		return RawRef{}, fmt.Errorf("raw: write object: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return RawRef{}, fmt.Errorf("raw: close object: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		// A concurrent writer may have created the object first; that is fine
		// since the content is identical.
		if _, statErr := os.Stat(path); statErr == nil {
			return ref, nil
		}
		return RawRef{}, fmt.Errorf("raw: finalize object: %w", err)
	}

	return ref, nil
}

// Get returns the exact bytes originally stored for ref.
func (s *FSStore) Get(ctx context.Context, ref RawRef) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	hexSum, err := hexFromHash(ref.Hash)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(s.objectPath(hexSum))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("raw: object %s not found", ref.Hash)
		}
		return nil, fmt.Errorf("raw: read object: %w", err)
	}
	return data, nil
}

// objectPath returns the on-disk path for an object with the given sha256 hex
// digest. Objects are sharded by their first two hex characters to avoid a
// single directory with an unbounded number of entries.
func (s *FSStore) objectPath(hexSum string) string {
	return filepath.Join(s.root, hexSum[:2], hexSum)
}

// hexFromHash extracts the sha256 hex digest from a RawRef.Hash value,
// validating the expected "sha256:" prefix.
func hexFromHash(hash string) (string, error) {
	if !strings.HasPrefix(hash, hashPrefix) {
		return "", fmt.Errorf("raw: unsupported hash %q (want %s-prefixed)", hash, strings.TrimSuffix(hashPrefix, ":"))
	}
	hexSum := strings.TrimPrefix(hash, hashPrefix)
	if _, err := hex.DecodeString(hexSum); err != nil || len(hexSum) != 2*sha256.Size {
		return "", fmt.Errorf("raw: invalid sha256 digest %q", hexSum)
	}
	return hexSum, nil
}

// Ensure FSStore satisfies RawStore.
var _ RawStore = (*FSStore)(nil)
