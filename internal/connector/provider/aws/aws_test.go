package aws

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"

	"github.com/bellu/bellu-ai-cyber/internal/connector"
	"github.com/bellu/bellu-ai-cyber/internal/connector/provider"
)

// fakeSTS is a counting fake STSAPI for auth tests.
type fakeSTS struct {
	calls           int
	lastInput       *sts.AssumeRoleInput
	err             error
	returnCreds     bool
	identityAccount string // overrides the account GetCallerIdentity returns
}

func (f *fakeSTS) AssumeRole(ctx context.Context, in *sts.AssumeRoleInput, opts ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
	f.calls++
	f.lastInput = in
	if f.err != nil {
		return nil, f.err
	}
	if !f.returnCreds {
		return &sts.AssumeRoleOutput{}, nil
	}
	return &sts.AssumeRoleOutput{
		Credentials: &ststypes.Credentials{
			AccessKeyId:     awssdk.String("AKIAFAKE"),
			SecretAccessKey: awssdk.String("fakesecret"),
			SessionToken:    awssdk.String("faketoken"),
			Expiration:      awssdk.Time(time.Now().Add(time.Hour)),
		},
	}, nil
}

func (f *fakeSTS) GetCallerIdentity(ctx context.Context, in *sts.GetCallerIdentityInput, opts ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	acct := f.identityAccount
	if acct == "" {
		if f.lastInput != nil && f.lastInput.RoleArn != nil {
			if a, err := AccountIDFromRoleARN(awssdk.ToString(f.lastInput.RoleArn)); err == nil {
				acct = a
			}
		}
	}
	return &sts.GetCallerIdentityOutput{Account: awssdk.String(acct)}, nil
}

const (
	validARN     = "arn:aws:iam::123456789012:role/BelluConnectorRole"
	validAccount = "123456789012"
)

func validTarget() provider.Target {
	return provider.Target{
		ConnectionID: 42,
		AccountID:    validAccount,
		RoleARN:      validARN,
		ExternalID:   "BELLU-TESTEXT",
		Capabilities: []string{"core"},
	}
}

// TestAuthenticateValidation asserts invalid ARN/account are rejected, name the
// field, and make zero STS calls.
func TestAuthenticateValidation(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(*provider.Target)
		wantField string
	}{
		{"bad arn", func(tg *provider.Target) { tg.RoleARN = "not-an-arn" }, "role_arn"},
		{"arn wrong account digits", func(tg *provider.Target) { tg.RoleARN = "arn:aws:iam::123:role/x" }, "role_arn"},
		{"bad account", func(tg *provider.Target) { tg.AccountID = "12345" }, "account_id"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeSTS{returnCreds: true}
			l := NewAuthLayer(f)
			tg := validTarget()
			c.mutate(&tg)
			_, err := l.Authenticate(context.Background(), tg)
			if !errors.Is(err, connector.ErrInvalidValue) {
				t.Fatalf("want ErrInvalidValue, got %v", err)
			}
			if !strings.Contains(err.Error(), c.wantField) {
				t.Errorf("error %q does not name field %q", err, c.wantField)
			}
			if f.calls != 0 {
				t.Errorf("STS called %d times on invalid input, want 0", f.calls)
			}
		})
	}
}

// TestAuthenticateSuccess asserts valid input calls STS with the external id
// and returns in-memory credentials.
func TestAuthenticateSuccess(t *testing.T) {
	f := &fakeSTS{returnCreds: true}
	l := NewAuthLayer(f)
	tg := validTarget()

	creds, err := l.Authenticate(context.Background(), tg)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if f.calls != 1 {
		t.Errorf("STS calls = %d, want 1", f.calls)
	}
	if got := awssdk.ToString(f.lastInput.ExternalId); got != tg.ExternalID {
		t.Errorf("ExternalId passed to STS = %q, want %q", got, tg.ExternalID)
	}
	if creds.AccessKeyID != "AKIAFAKE" || creds.SessionToken != "faketoken" {
		t.Errorf("unexpected creds %+v", creds)
	}
}

// TestAuthenticateSTSError asserts an STS error yields no credentials.
func TestAuthenticateSTSError(t *testing.T) {
	f := &fakeSTS{err: errors.New("access denied")}
	l := NewAuthLayer(f)
	creds, err := l.Authenticate(context.Background(), validTarget())
	if err == nil {
		t.Fatal("want error from STS failure")
	}
	if creds != nil {
		t.Errorf("want nil creds on STS error, got %+v", creds)
	}
}

// TestBuildSetupPolicy asserts the trust policy binds the external id and the
// permission policy excludes AdministratorAccess and wildcard full access.
func TestBuildSetupPolicy(t *testing.T) {
	in := provider.SetupInput{
		ConnectionID: 1,
		AccountID:    validAccount,
		ExternalID:   "BELLU-EXT123",
		Capabilities: []string{"core", "eks"},
	}
	setup, err := buildSetup("arn:aws:iam::999999999999:role/BelluService", in)
	if err != nil {
		t.Fatalf("buildSetup: %v", err)
	}

	if setup.ExternalID != in.ExternalID {
		t.Errorf("setup external id = %q, want %q", setup.ExternalID, in.ExternalID)
	}
	// Trust policy external id must equal the stored value.
	cond := setup.TrustPolicy["Statement"].([]map[string]any)[0]["Condition"].(map[string]any)
	gotExt := cond["StringEquals"].(map[string]any)["sts:ExternalId"].(string)
	if gotExt != in.ExternalID {
		t.Errorf("trust policy ExternalId = %q, want %q", gotExt, in.ExternalID)
	}

	// Permission policy must not contain AdministratorAccess or "*" action on "*".
	stmts := setup.PermissionPolicy["Statement"].([]map[string]any)
	if len(stmts) != 2 {
		t.Fatalf("expected 2 statements (core, eks), got %d", len(stmts))
	}
	for _, s := range stmts {
		actions, _ := s["Action"].([]string)
		for _, a := range actions {
			if a == "*" {
				t.Error("permission policy contains wildcard action")
			}
			if strings.EqualFold(a, "AdministratorAccess") {
				t.Error("permission policy references AdministratorAccess")
			}
		}
	}
}

// TestVerifierShaping asserts PASS/FAIL/NOT_ENABLED shaping and OK semantics.
func TestVerifierShaping(t *testing.T) {
	// Fail only s3:ListAllMyBuckets; everything else passes.
	probe := func(ctx context.Context, creds *provider.Credentials, perm string) error {
		if perm == "s3:ListAllMyBuckets" {
			return errors.New("AccessDenied")
		}
		return nil
	}
	v := NewVerifier(probe)
	res := v.Verify(context.Background(), &provider.Credentials{}, []string{"core"})

	if res.OK {
		t.Error("OK should be false when a core permission fails")
	}
	if len(res.Failures) != 1 || res.Failures[0].Permission != "s3:ListAllMyBuckets" {
		t.Errorf("failures = %+v, want one s3:ListAllMyBuckets", res.Failures)
	}
	// eks is not enabled -> NOT_ENABLED.
	var eks *provider.CapabilityResult
	for i := range res.Capabilities {
		if res.Capabilities[i].Capability == "eks" {
			eks = &res.Capabilities[i]
		}
	}
	if eks == nil || eks.Outcome != provider.OutcomeNotEnabled {
		t.Errorf("eks capability should be NOT_ENABLED, got %+v", eks)
	}
}

// TestVerifierAllPass asserts OK is true when every enabled probe passes.
func TestVerifierAllPass(t *testing.T) {
	v := NewVerifier(func(context.Context, *provider.Credentials, string) error { return nil })
	res := v.Verify(context.Background(), &provider.Credentials{}, []string{"core"})
	if !res.OK {
		t.Errorf("OK should be true when all pass; failures=%+v", res.Failures)
	}
}

// TestDiscoverBuildsPlan asserts Discover builds a steampipe config and
// session-only credential env without provider-specific queries.
func TestDiscoverBuildsPlan(t *testing.T) {
	a := New(Options{STS: &fakeSTS{returnCreds: true}, SteampipeConn: "aws", SteampipePassword: "pw"})
	creds := &provider.Credentials{AccessKeyID: "AK", SecretAccessKey: "SK", SessionToken: "TK"}
	tg := validTarget()
	tg.Region = "us-east-1"

	plan, err := a.Discover(context.Background(), tg, creds)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if plan.SteampipeConfig["connection"] != "aws" {
		t.Errorf("steampipe connection = %v, want aws", plan.SteampipeConfig["connection"])
	}
	if plan.CredentialEnv["AWS_ACCESS_KEY_ID"] != "AK" || plan.CredentialEnv["AWS_SESSION_TOKEN"] != "TK" {
		t.Errorf("credential env not populated: %+v", plan.CredentialEnv)
	}
	if plan.CredentialEnv["AWS_REGION"] != "us-east-1" {
		t.Errorf("AWS_REGION = %q, want us-east-1", plan.CredentialEnv["AWS_REGION"])
	}
}

// TestGenerateExternalIDUnique asserts generated ids are prefixed and distinct.
func TestGenerateExternalIDUnique(t *testing.T) {
	a, err := GenerateExternalID()
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	b, _ := GenerateExternalID()
	if !strings.HasPrefix(a, "BELLU-") {
		t.Errorf("external id %q missing BELLU- prefix", a)
	}
	if a == b {
		t.Error("two generated external ids should differ")
	}
}

// TestAuthenticateRejectsIdentityMismatch asserts that when GetCallerIdentity
// returns an account different from the role ARN's account, authentication is
// rejected and no credentials are returned.
func TestAuthenticateRejectsIdentityMismatch(t *testing.T) {
	f := &fakeSTS{returnCreds: true, identityAccount: "999999999999"} // != ARN account
	l := NewAuthLayer(f)
	creds, err := l.Authenticate(context.Background(), validTarget())
	if !errors.Is(err, connector.ErrInvalidValue) {
		t.Fatalf("want ErrInvalidValue on identity mismatch, got %v", err)
	}
	if creds != nil {
		t.Errorf("want nil creds on identity mismatch, got %+v", creds)
	}
}

// TestAccountIDFromRoleARN pins the ARN account extractor.
func TestAccountIDFromRoleARN(t *testing.T) {
	acct, err := AccountIDFromRoleARN("arn:aws:iam::210987654321:role/BelluConnectorRole")
	if err != nil || acct != "210987654321" {
		t.Errorf("extract = (%q,%v), want 210987654321", acct, err)
	}
	if _, err := AccountIDFromRoleARN("garbage"); !errors.Is(err, connector.ErrInvalidValue) {
		t.Errorf("malformed ARN should error with ErrInvalidValue, got %v", err)
	}
}
