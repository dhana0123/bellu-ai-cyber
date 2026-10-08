package raw

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/quick"
)

// countObjectFiles walks the FSStore root and counts stored object files,
// ignoring directories and any leftover temp files from in-progress writes.
func countObjectFiles(t *testing.T, root string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".tmp-") {
			return nil
		}
		n++
		return nil
	})
	if err != nil {
		t.Fatalf("walk store root: %v", err)
	}
	return n
}

// TestPutReturnsSha256KeyedRef asserts that Put returns a RawRef whose Hash is
// the "sha256:"-prefixed hex digest of the content, with Source and Size set.
//
// Validates: Requirements 2.1
func TestPutReturnsSha256KeyedRef(t *testing.T) {
	store, err := NewFSStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFSStore: %v", err)
	}

	payload := []byte("hello intelligence corpus")
	ref, err := store.Put(context.Background(), "osv", payload)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	sum := sha256.Sum256(payload)
	wantHash := "sha256:" + hex.EncodeToString(sum[:])

	if ref.Hash != wantHash {
		t.Errorf("ref.Hash = %q, want %q", ref.Hash, wantHash)
	}
	if !strings.HasPrefix(ref.Hash, "sha256:") {
		t.Errorf("ref.Hash = %q, want sha256: prefix", ref.Hash)
	}
	if ref.Source != "osv" {
		t.Errorf("ref.Source = %q, want %q", ref.Source, "osv")
	}
	if ref.Size != int64(len(payload)) {
		t.Errorf("ref.Size = %d, want %d", ref.Size, len(payload))
	}
}

// TestPutIdenticalBytesSameRefNoDuplicate asserts that storing identical bytes
// twice yields the same RawRef and stores no duplicate copy on disk.
//
// Validates: Requirements 2.2
func TestPutIdenticalBytesSameRefNoDuplicate(t *testing.T) {
	root := t.TempDir()
	store, err := NewFSStore(root)
	if err != nil {
		t.Fatalf("NewFSStore: %v", err)
	}

	payload := []byte("identical payload bytes")

	ref1, err := store.Put(context.Background(), "osv", payload)
	if err != nil {
		t.Fatalf("first Put: %v", err)
	}
	ref2, err := store.Put(context.Background(), "osv", payload)
	if err != nil {
		t.Fatalf("second Put: %v", err)
	}

	if ref1 != ref2 {
		t.Errorf("second Put returned different ref: %+v vs %+v", ref1, ref2)
	}
	if got := countObjectFiles(t, root); got != 1 {
		t.Errorf("expected exactly 1 object file for identical content, got %d", got)
	}
}

// TestPutImmutableStoredBytes asserts that storing the same content again does
// not alter the stored bytes (payloads are treated as immutable).
//
// Validates: Requirements 2.3
func TestPutImmutableStoredBytes(t *testing.T) {
	root := t.TempDir()
	store, err := NewFSStore(root)
	if err != nil {
		t.Fatalf("NewFSStore: %v", err)
	}

	payload := []byte("immutable content")
	ref, err := store.Put(context.Background(), "osv", payload)
	if err != nil {
		t.Fatalf("first Put: %v", err)
	}

	// Locate the on-disk object and record its contents and modification time.
	hexSum := strings.TrimPrefix(ref.Hash, "sha256:")
	objPath := filepath.Join(root, hexSum[:2], hexSum)
	before, err := os.ReadFile(objPath)
	if err != nil {
		t.Fatalf("read object after first Put: %v", err)
	}
	beforeInfo, err := os.Stat(objPath)
	if err != nil {
		t.Fatalf("stat object after first Put: %v", err)
	}

	// Storing the same content again must be a no-op that leaves the stored
	// bytes untouched.
	if _, err := store.Put(context.Background(), "osv", payload); err != nil {
		t.Fatalf("second Put: %v", err)
	}

	after, err := os.ReadFile(objPath)
	if err != nil {
		t.Fatalf("read object after second Put: %v", err)
	}
	afterInfo, err := os.Stat(objPath)
	if err != nil {
		t.Fatalf("stat object after second Put: %v", err)
	}

	if string(before) != string(after) {
		t.Errorf("stored bytes changed after re-Put: %q -> %q", before, after)
	}
	if !beforeInfo.ModTime().Equal(afterInfo.ModTime()) {
		t.Errorf("object was rewritten: modtime changed %v -> %v", beforeInfo.ModTime(), afterInfo.ModTime())
	}
}

// TestGetRoundTripsExactBytes asserts that Get returns the exact bytes
// originally stored for a reference.
//
// Validates: Requirements 2.4
func TestGetRoundTripsExactBytes(t *testing.T) {
	store, err := NewFSStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFSStore: %v", err)
	}

	cases := [][]byte{
		[]byte(""),
		[]byte("single line"),
		[]byte("multi\nline\npayload\x00with nulls and \xff bytes"),
	}
	for _, payload := range cases {
		ref, err := store.Put(context.Background(), "osv", payload)
		if err != nil {
			t.Fatalf("Put(%q): %v", payload, err)
		}
		got, err := store.Get(context.Background(), ref)
		if err != nil {
			t.Fatalf("Get(%+v): %v", ref, err)
		}
		if string(got) != string(payload) {
			t.Errorf("round-trip mismatch: got %q, want %q", got, payload)
		}
	}
}

// TestDifferentContentDifferentRefs asserts that distinct payloads produce
// distinct references, so content addressing never collides unrelated data.
//
// Validates: Requirements 2.1
func TestDifferentContentDifferentRefs(t *testing.T) {
	store, err := NewFSStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFSStore: %v", err)
	}

	refA, err := store.Put(context.Background(), "osv", []byte("content A"))
	if err != nil {
		t.Fatalf("Put A: %v", err)
	}
	refB, err := store.Put(context.Background(), "osv", []byte("content B"))
	if err != nil {
		t.Fatalf("Put B: %v", err)
	}

	if refA.Hash == refB.Hash {
		t.Errorf("different content produced identical hash %q", refA.Hash)
	}
}

// TestPutIsIdempotentContentAddressed is a property test for correctness
// Property 3 (re-runs are idempotent) at the storage layer: for all payloads,
// storing the same bytes any number of times yields the same ref, stores no
// duplicate copy, and Get round-trips the exact bytes.
//
// Property 3: Re-runs are idempotent (content-addressed storage)
// Validates: Requirements 2.1, 2.2, 2.4
func TestPutIsIdempotentContentAddressed(t *testing.T) {
	property := func(payload []byte, repeats uint8) bool {
		root := t.TempDir()
		store, err := NewFSStore(root)
		if err != nil {
			t.Logf("NewFSStore: %v", err)
			return false
		}

		n := int(repeats)%8 + 1 // 1..8 repeated Puts of the same content

		first, err := store.Put(context.Background(), "osv", payload)
		if err != nil {
			t.Logf("Put: %v", err)
			return false
		}
		for i := 1; i < n; i++ {
			ref, err := store.Put(context.Background(), "osv", payload)
			if err != nil {
				t.Logf("Put repeat %d: %v", i, err)
				return false
			}
			if ref != first {
				t.Logf("repeat %d ref %+v != first %+v", i, ref, first)
				return false
			}
		}

		// Idempotent storage: exactly one object file regardless of repeats.
		if got := countObjectFiles(t, root); got != 1 {
			t.Logf("expected 1 object file, got %d", got)
			return false
		}

		// Hash is the sha256 identity of the content.
		sum := sha256.Sum256(payload)
		if want := "sha256:" + hex.EncodeToString(sum[:]); first.Hash != want {
			t.Logf("hash %q != want %q", first.Hash, want)
			return false
		}

		// Round-trip returns the exact bytes.
		got, err := store.Get(context.Background(), first)
		if err != nil {
			t.Logf("Get: %v", err)
			return false
		}
		return string(got) == string(payload)
	}

	if err := quick.Check(property, &quick.Config{MaxCount: 300}); err != nil {
		t.Error(err)
	}
}
