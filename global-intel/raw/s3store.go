package raw

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// S3Store is an S3-backed, content-addressed RawStore. Payloads are stored
// under a key derived from the sha256 of their content, so identical bytes map
// to the same object and re-storing them is a no-op. Objects are written once
// and never overwritten (immutable), matching the FSStore contract and the raw
// bucket's Object Lock. It is used by the Fargate task (BELLU_RAW_BUCKET set);
// local dev falls back to FSStore.
//
// Credentials and region come from the AWS default chain, so on Fargate the
// task role is used automatically with no static keys.
type S3Store struct {
	client s3API
	bucket string
	prefix string
}

// s3API is the subset of the S3 client S3Store uses, so the store can be
// unit-tested with a fake without a live bucket.
type s3API interface {
	PutObject(ctx context.Context, in *s3.PutObjectInput, opts ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	HeadObject(ctx context.Context, in *s3.HeadObjectInput, opts ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	GetObject(ctx context.Context, in *s3.GetObjectInput, opts ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

// S3Option customizes an S3Store.
type S3Option func(*S3Store)

// WithPrefix sets an optional key prefix under which all objects are stored
// (e.g. "intelligence"). A trailing slash is not required.
func WithPrefix(prefix string) S3Option {
	return func(s *S3Store) { s.prefix = strings.Trim(prefix, "/") }
}

// WithS3Client injects a preconfigured S3 client (or a fake, in tests).
func WithS3Client(c s3API) S3Option {
	return func(s *S3Store) { s.client = c }
}

// NewS3Store builds an S3-backed RawStore for the given bucket. Unless a client
// is injected via WithS3Client, it loads the AWS default config (region +
// credentials from the environment / task role) and constructs an S3 client.
func NewS3Store(ctx context.Context, bucket string, opts ...S3Option) (*S3Store, error) {
	if strings.TrimSpace(bucket) == "" {
		return nil, fmt.Errorf("raw: s3 bucket must not be empty")
	}
	st := &S3Store{bucket: bucket}
	for _, opt := range opts {
		opt(st)
	}
	if st.client == nil {
		cfg, err := awsconfig.LoadDefaultConfig(ctx)
		if err != nil {
			return nil, fmt.Errorf("raw: load aws config: %w", err)
		}
		st.client = s3.NewFromConfig(cfg)
	}
	return st, nil
}

// Put stores payload at its content-addressed key and returns a sha256-keyed
// reference. If an object already exists at that key it is left untouched (the
// content is identical by construction) and the same reference is returned, so
// Put is idempotent and never overwrites — preserving immutability.
func (s *S3Store) Put(ctx context.Context, source string, payload []byte) (RawRef, error) {
	if err := ctx.Err(); err != nil {
		return RawRef{}, err
	}

	sum := sha256.Sum256(payload)
	hexSum := hex.EncodeToString(sum[:])
	key := s.objectKey(source, hexSum)
	ref := RawRef{Source: source, Hash: hashPrefix + hexSum, Size: int64(len(payload))}

	// Conditional write: IfNoneMatch "*" makes S3 reject the put if the object
	// already exists, so identical content is a cheap no-op and an existing
	// immutable object is never overwritten. S3 returns 412 PreconditionFailed
	// in that case, which we treat as success.
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(payload),
		IfNoneMatch: aws.String("*"),
		ContentType: aws.String("application/octet-stream"),
		Metadata:    map[string]string{"source": source, "sha256": hexSum},
	})
	if err == nil {
		return ref, nil
	}
	if isPreconditionFailed(err) {
		// Object already present — no-op, same ref.
		return ref, nil
	}
	// Some S3-compatible backends do not support IfNoneMatch. Fall back to an
	// existence check, then an unconditional put, preserving the no-op contract.
	if isNotImplementedConditional(err) {
		if s.exists(ctx, key) {
			return ref, nil
		}
		if _, perr := s.client.PutObject(ctx, &s3.PutObjectInput{
			Bucket:      aws.String(s.bucket),
			Key:         aws.String(key),
			Body:        bytes.NewReader(payload),
			ContentType: aws.String("application/octet-stream"),
			Metadata:    map[string]string{"source": source, "sha256": hexSum},
		}); perr != nil {
			return RawRef{}, fmt.Errorf("raw: s3 put %s: %w", key, perr)
		}
		return ref, nil
	}
	return RawRef{}, fmt.Errorf("raw: s3 put %s: %w", key, err)
}

// Get returns the exact bytes stored for ref.
func (s *S3Store) Get(ctx context.Context, ref RawRef) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	hexSum, err := hexFromHash(ref.Hash)
	if err != nil {
		return nil, err
	}
	key := s.objectKey(ref.Source, hexSum)
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			return nil, fmt.Errorf("raw: object %s not found", ref.Hash)
		}
		return nil, fmt.Errorf("raw: s3 get %s: %w", key, err)
	}
	defer out.Body.Close()
	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, fmt.Errorf("raw: s3 read %s: %w", key, err)
	}
	return data, nil
}

// exists reports whether an object is already present at key.
func (s *S3Store) exists(ctx context.Context, key string) bool {
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	return err == nil
}

// objectKey builds the content-addressed object key, sharded by the first two
// hex characters (like FSStore) and namespaced by source, under the optional
// prefix. e.g. "intelligence/osv/ab/abcd...".
func (s *S3Store) objectKey(source, hexSum string) string {
	parts := make([]string, 0, 4)
	if s.prefix != "" {
		parts = append(parts, s.prefix)
	}
	if source != "" {
		parts = append(parts, source)
	}
	parts = append(parts, hexSum[:2], hexSum)
	return strings.Join(parts, "/")
}

// isPreconditionFailed reports whether err is an S3 412 (object already exists
// under the IfNoneMatch "*" conditional put).
func isPreconditionFailed(err error) bool {
	var ae smithy.APIError
	if errors.As(err, &ae) {
		switch ae.ErrorCode() {
		case "PreconditionFailed", "412":
			return true
		}
	}
	return false
}

// isNotImplementedConditional reports whether a backend rejected the
// IfNoneMatch conditional as unsupported (some S3-compatible stores).
func isNotImplementedConditional(err error) bool {
	var ae smithy.APIError
	if errors.As(err, &ae) {
		switch ae.ErrorCode() {
		case "NotImplemented", "InvalidRequest":
			return true
		}
	}
	return false
}

// Ensure S3Store satisfies RawStore.
var _ RawStore = (*S3Store)(nil)
