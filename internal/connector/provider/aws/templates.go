package aws

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/bellu/bellu-ai-cyber/internal/connector/setup"
)

// TemplateStore publishes a generated CloudFormation template and returns an
// HTTPS URL CloudFormation can read. AWS quick-create links can only pre-load
// templates hosted in S3.
type TemplateStore interface {
	Publish(ctx context.Context, key string, body []byte) (string, error)
}

type s3PutAPI interface {
	PutObject(ctx context.Context, in *s3.PutObjectInput, opts ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

type s3PresignAPI interface {
	PresignGetObject(ctx context.Context, in *s3.GetObjectInput, opts ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error)
}

// S3TemplateStore uploads templates to a private bucket and returns a
// short-lived presigned GET URL, so the bucket never needs to be public.
type S3TemplateStore struct {
	put     s3PutAPI
	presign s3PresignAPI
	bucket  string
	ttl     time.Duration
}

// NewS3TemplateStore builds a store using the default AWS credential chain
// (env keys on-prem) in the bucket's region. ttl is the presigned link lifetime.
func NewS3TemplateStore(ctx context.Context, bucket, region string, ttl time.Duration) (*S3TemplateStore, error) {
	if bucket == "" {
		return nil, fmt.Errorf("aws: template bucket must not be empty")
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("aws: load config: %w", err)
	}
	client := s3.NewFromConfig(cfg)
	return &S3TemplateStore{put: client, presign: s3.NewPresignClient(client), bucket: bucket, ttl: ttl}, nil
}

// Publish uploads body under key and returns a presigned GET URL.
func (s *S3TemplateStore) Publish(ctx context.Context, key string, body []byte) (string, error) {
	if _, err := s.put.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(body),
		ContentType: aws.String("application/x-yaml"),
	}); err != nil {
		return "", fmt.Errorf("aws: upload template: %w", err)
	}
	req, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(s.ttl))
	if err != nil {
		return "", fmt.Errorf("aws: presign template: %w", err)
	}
	return req.URL, nil
}

// templateKey is a content-addressed object key for a template. Because the
// template body is now connection-independent (the external id is a CFN
// parameter, not embedded), identical templates collapse to the SAME key — so
// S3 stores one reusable object instead of one folder per connection.
func templateKey(body []byte) string {
	sum := sha256.Sum256(body)
	return fmt.Sprintf("templates/%s.yaml", hex.EncodeToString(sum[:16]))
}

// quickCreateURL builds a CloudFormation quick-create link that opens the
// "Quick create stack" page with the template, stack name, and the per-
// connection external id pre-filled (CFN reads param_<Name> from the URL and
// auto-fills the matching parameter field).
func quickCreateURL(region, templateURL, externalID string) string {
	if region == "" {
		region = "us-east-1"
	}
	u := fmt.Sprintf(
		"https://%s.console.aws.amazon.com/cloudformation/home?region=%s#/stacks/create/review?templateURL=%s&stackName=%s",
		region, region, url.QueryEscape(templateURL), setup.StackName)
	if externalID != "" {
		u += "&param_" + setup.ExternalIdParameter + "=" + url.QueryEscape(externalID)
	}
	return u
}

// quickUpdateURL builds a CloudFormation link for applying the regenerated
// template to the EXISTING stack (an in-place update of the same stack name).
// The console's "quick create" review page is keyed by stackName: when a stack
// with that name already exists, submitting the pre-loaded template performs a
// stack UPDATE (diff only — same role ARN and external id), not a create. This
// is the fast, non-destructive reconnect used when capabilities change. It is
// always customer-driven: the customer clicks it in their own account; Bellu
// never calls UpdateStack and holds no IAM-write in the customer account.
func quickUpdateURL(region, templateURL, externalID string) string {
	// Same shape as create: the review page resolves create-vs-update by whether
	// the stack name already exists in the account.
	return quickCreateURL(region, templateURL, externalID)
}
