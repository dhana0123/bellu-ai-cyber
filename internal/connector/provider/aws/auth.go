package aws

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/bellu/bellu-ai-cyber/internal/connector"
	"github.com/bellu/bellu-ai-cyber/internal/connector/provider"
)

// STSAPI is the subset of the STS client the auth layer uses, so the adapter
// can be unit-tested with a fake without live AWS. It mirrors the narrow-
// interface pattern used by global-intel/raw.s3API.
type STSAPI interface {
	AssumeRole(ctx context.Context, in *sts.AssumeRoleInput, opts ...func(*sts.Options)) (*sts.AssumeRoleOutput, error)
	GetCallerIdentity(ctx context.Context, in *sts.GetCallerIdentityInput, opts ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

var (
	// roleARNRe matches an IAM role ARN: arn:aws[-partition]:iam::<12 digits>:role/<name>.
	roleARNRe = regexp.MustCompile(`^arn:aws(?:-[a-z-]+)?:iam::\d{12}:role/.+$`)
	// accountIDRe matches a 12-digit AWS account id.
	accountIDRe = regexp.MustCompile(`^\d{12}$`)
	// roleARNAccountRe captures the 12-digit account id embedded in a role ARN.
	roleARNAccountRe = regexp.MustCompile(`^arn:aws(?:-[a-z-]+)?:iam::(\d{12}):role/.+$`)
)

// AccountIDFromRoleARN extracts the 12-digit AWS account id embedded in a role
// ARN. It returns an ErrInvalidValue error naming role_arn when the ARN is not
// a well-formed IAM role ARN.
func AccountIDFromRoleARN(arn string) (string, error) {
	m := roleARNAccountRe.FindStringSubmatch(strings.TrimSpace(arn))
	if m == nil {
		return "", fmt.Errorf("%w: role_arn", connector.ErrInvalidValue)
	}
	return m[1], nil
}

// AuthLayer validates connection identifiers and obtains temporary credentials
// via STS AssumeRole. It uses zero customer long-lived access keys.
type AuthLayer struct {
	sts STSAPI
}

// NewAuthLayer builds an AuthLayer over the given STS client.
func NewAuthLayer(client STSAPI) *AuthLayer {
	return &AuthLayer{sts: client}
}

// GenerateExternalID returns a unique, hard-to-guess per-connection external id
// used in both the role trust policy condition and every AssumeRole request.
func GenerateExternalID() (string, error) {
	var b [20]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("aws: generate external id: %w", err)
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:])
	return "BELLU-" + enc, nil
}

// Authenticate validates the role ARN and account id, then calls STS AssumeRole
// with the connection's external id. On validation failure it reports the
// offending field and makes zero STS calls; on an STS error it returns no
// credentials.
func (l *AuthLayer) Authenticate(ctx context.Context, t provider.Target) (*provider.Credentials, error) {
	if !roleARNRe.MatchString(t.RoleARN) {
		return nil, fmt.Errorf("%w: role_arn", connector.ErrInvalidValue)
	}
	if !accountIDRe.MatchString(t.AccountID) {
		return nil, fmt.Errorf("%w: account_id", connector.ErrInvalidValue)
	}

	out, err := l.sts.AssumeRole(ctx, &sts.AssumeRoleInput{
		RoleArn:         aws.String(t.RoleARN),
		RoleSessionName: aws.String(fmt.Sprintf("bellu-connector-%d", t.ConnectionID)),
		ExternalId:      aws.String(t.ExternalID),
	})
	if err != nil {
		return nil, fmt.Errorf("aws: assume role: %w", err)
	}
	if out == nil || out.Credentials == nil {
		return nil, fmt.Errorf("aws: assume role returned no credentials")
	}
	c := out.Credentials
	creds := &provider.Credentials{
		AccessKeyID:     aws.ToString(c.AccessKeyId),
		SecretAccessKey: aws.ToString(c.SecretAccessKey),
		SessionToken:    aws.ToString(c.SessionToken),
		Expiry:          aws.ToTime(c.Expiration),
	}

	// Identity validation: confirm the assumed role actually lives in the
	// account named by the ARN. GetCallerIdentity (called with the assumed-role
	// credentials) returns the account of the active identity; a mismatch means
	// the ARN or account is spoofed/misconfigured, so the connection is rejected.
	wantAccount, _ := AccountIDFromRoleARN(t.RoleARN)
	ident, ierr := l.sts.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{}, withCredentials(creds))
	if ierr != nil {
		return nil, fmt.Errorf("aws: get caller identity: %w", ierr)
	}
	gotAccount := aws.ToString(ident.Account)
	if wantAccount != "" && gotAccount != "" && gotAccount != wantAccount {
		return nil, fmt.Errorf("%w: role_arn account %s does not match assumed identity account %s",
			connector.ErrInvalidValue, wantAccount, gotAccount)
	}
	return creds, nil
}

// withCredentials returns an STS option that uses the given temporary
// credentials for the request (so GetCallerIdentity reflects the assumed role,
// not Bellu's own principal). A fake STS in tests can ignore options.
func withCredentials(c *provider.Credentials) func(*sts.Options) {
	return func(o *sts.Options) {
		o.Credentials = aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
			return aws.Credentials{
				AccessKeyID:     c.AccessKeyID,
				SecretAccessKey: c.SecretAccessKey,
				SessionToken:    c.SessionToken,
				Expires:         c.Expiry,
			}, nil
		})
	}
}

// ValidateTarget reports a validation error (naming the field) without making
// any STS call, for callers that want to pre-check identifiers.
func ValidateTarget(t provider.Target) error {
	switch {
	case !roleARNRe.MatchString(t.RoleARN):
		return fmt.Errorf("%w: role_arn", connector.ErrInvalidValue)
	case !accountIDRe.MatchString(strings.TrimSpace(t.AccountID)):
		return fmt.Errorf("%w: account_id", connector.ErrInvalidValue)
	}
	return nil
}
