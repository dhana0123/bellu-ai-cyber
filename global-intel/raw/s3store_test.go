package raw

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// fakeS3 is an in-memory s3API that mimics the content-addressed semantics the
// store relies on: objects keyed by their key, IfNoneMatch "*" rejecting an
// existing key with a 412, and NoSuchKey on a missing Get. It records call
// counts so tests can assert the no-op (dedup) behavior.
type fakeS3 struct {
	objects map[string][]byte
	puts    int
}

func newFakeS3() *fakeS3 { return &fakeS3{objects: map[string][]byte{}} }

type apiErr struct{ code string }

func (e apiErr) Error() string                 { return e.code }
func (e apiErr) ErrorCode() string             { return e.code }
func (e apiErr) ErrorMessage() string          { return e.code }
func (e apiErr) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }

func (f *fakeS3) PutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	f.puts++
	key := aws.ToString(in.Key)
	if in.IfNoneMatch != nil && aws.ToString(in.IfNoneMatch) == "*" {
		if _, exists := f.objects[key]; exists {
			return nil, apiErr{code: "PreconditionFailed"}
		}
	}
	body, _ := io.ReadAll(in.Body)
	f.objects[key] = body
	return &s3.PutObjectOutput{}, nil
}

func (f *fakeS3) HeadObject(_ context.Context, in *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	if _, ok := f.objects[aws.ToString(in.Key)]; ok {
		return &s3.HeadObjectOutput{}, nil
	}
	return nil, &types.NotFound{}
}

func (f *fakeS3) GetObject(_ context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	body, ok := f.objects[aws.ToString(in.Key)]
	if !ok {
		return nil, &types.NoSuchKey{}
	}
	return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(body))}, nil
}

func newTestS3Store(t *testing.T, fake *fakeS3, opts ...S3Option) *S3Store {
	t.Helper()
	all := append([]S3Option{WithS3Client(fake)}, opts...)
	st, err := NewS3Store(context.Background(), "test-bucket", all...)
	if err != nil {
		t.Fatalf("NewS3Store: %v", err)
	}
	return st
}

// TestS3PutReturnsSha256KeyedRef: the ref hash is the sha256 of the content and
// the object lands at the content-addressed, source-namespaced, sharded key.
func TestS3PutReturnsSha256KeyedRef(t *testing.T) {
	fake := newFakeS3()
	st := newTestS3Store(t, fake, WithPrefix("intelligence"))

	payload := []byte("hello intelligence corpus")
	ref, err := st.Put(context.Background(), "osv", payload)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	sum := sha256.Sum256(payload)
	hexSum := hex.EncodeToString(sum[:])
	if ref.Hash != "sha256:"+hexSum {
		t.Errorf("ref.Hash = %q, want sha256:%s", ref.Hash, hexSum)
	}
	if ref.Source != "osv" || ref.Size != int64(len(payload)) {
		t.Errorf("unexpected ref %+v", ref)
	}
	wantKey := "intelligence/osv/" + hexSum[:2] + "/" + hexSum
	if _, ok := fake.objects[wantKey]; !ok {
		keys := make([]string, 0, len(fake.objects))
		for k := range fake.objects {
			keys = append(keys, k)
		}
		t.Fatalf("object not stored at %q; have %v", wantKey, keys)
	}
}

// TestS3PutIdenticalBytesIsNoOp: storing identical bytes twice yields the same
// ref and does not overwrite (the second put hits the 412 no-op path), so the
// stored object is immutable.
func TestS3PutIdenticalBytesIsNoOp(t *testing.T) {
	fake := newFakeS3()
	st := newTestS3Store(t, fake)

	payload := []byte("identical payload bytes")
	ref1, err := st.Put(context.Background(), "osv", payload)
	if err != nil {
		t.Fatalf("first Put: %v", err)
	}
	ref2, err := st.Put(context.Background(), "osv", payload)
	if err != nil {
		t.Fatalf("second Put: %v", err)
	}
	if ref1 != ref2 {
		t.Errorf("refs differ: %+v vs %+v", ref1, ref2)
	}
	if len(fake.objects) != 1 {
		t.Errorf("expected exactly 1 stored object, got %d", len(fake.objects))
	}
	if fake.puts != 2 {
		t.Errorf("expected 2 Put calls (second a 412 no-op), got %d", fake.puts)
	}
}

// TestS3GetRoundTrips: Get returns the exact bytes stored, across empty, text,
// and binary payloads.
func TestS3GetRoundTrips(t *testing.T) {
	fake := newFakeS3()
	st := newTestS3Store(t, fake)

	cases := [][]byte{
		[]byte(""),
		[]byte("single line"),
		[]byte("multi\nline\x00binary\xff"),
	}
	for _, payload := range cases {
		ref, err := st.Put(context.Background(), "kev", payload)
		if err != nil {
			t.Fatalf("Put(%q): %v", payload, err)
		}
		got, err := st.Get(context.Background(), ref)
		if err != nil {
			t.Fatalf("Get(%+v): %v", ref, err)
		}
		if !bytes.Equal(got, payload) {
			t.Errorf("round-trip mismatch: got %q want %q", got, payload)
		}
	}
}

// TestS3GetMissingIsNotFound: Get on an unknown ref surfaces a not-found error.
func TestS3GetMissingIsNotFound(t *testing.T) {
	st := newTestS3Store(t, newFakeS3())
	_, err := st.Get(context.Background(), RawRef{Source: "osv", Hash: "sha256:" + strings.Repeat("a", 64)})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

// TestS3DifferentContentDifferentKeys: distinct payloads produce distinct refs
// and distinct stored objects.
func TestS3DifferentContentDifferentKeys(t *testing.T) {
	fake := newFakeS3()
	st := newTestS3Store(t, fake)

	a, err := st.Put(context.Background(), "osv", []byte("content A"))
	if err != nil {
		t.Fatalf("Put A: %v", err)
	}
	b, err := st.Put(context.Background(), "osv", []byte("content B"))
	if err != nil {
		t.Fatalf("Put B: %v", err)
	}
	if a.Hash == b.Hash {
		t.Errorf("different content produced identical hash %q", a.Hash)
	}
	if len(fake.objects) != 2 {
		t.Errorf("expected 2 distinct objects, got %d", len(fake.objects))
	}
}

// TestS3EmptyBucketRejected guards the constructor.
func TestS3EmptyBucketRejected(t *testing.T) {
	if _, err := NewS3Store(context.Background(), "  ", WithS3Client(newFakeS3())); err == nil {
		t.Fatal("expected error for empty bucket")
	}
}
