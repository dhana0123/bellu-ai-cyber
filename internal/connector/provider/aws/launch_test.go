package aws

import (
	"context"
	"net/url"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	orgtypes "github.com/aws/aws-sdk-go-v2/service/organizations/types"

	"github.com/bellu/bellu-ai-cyber/internal/connector/provider"
)

type fakeStore struct {
	key  string
	body []byte
}

func (f *fakeStore) Publish(ctx context.Context, key string, body []byte) (string, error) {
	f.key, f.body = key, body
	return "https://bellu-templates.s3.us-east-1.amazonaws.com/" + key + "?X-Amz-Signature=abc&X-Amz-Expires=3600", nil
}

func setupIn(scope string) provider.SetupInput {
	return provider.SetupInput{
		ConnectionID: 7, ExternalID: "BELLU-X", Region: "eu-west-1",
		Scope: scope, DeploymentMethod: "cloudformation", Capabilities: []string{"core"},
	}
}

func TestCreateSetupPublishesAndBuildsQuickCreateLink(t *testing.T) {
	store := &fakeStore{}
	a := New(Options{STS: &fakeSTS{}, Templates: store, BelluPrincipalARN: "arn:aws:iam::001018342000:user/bellu-connector"})
	for _, scope := range []string{"account", "organization"} {
		s, err := a.CreateSetup(context.Background(), setupIn(scope))
		if err != nil {
			t.Fatalf("%s: %v", scope, err)
		}
		// Key is now content-addressed (connection-independent) so identical
		// templates share one S3 object: templates/<hash>.yaml.
		if string(store.body) != s.Template ||
			!strings.HasPrefix(store.key, "templates/") ||
			!strings.HasSuffix(store.key, ".yaml") ||
			strings.Contains(strings.TrimSuffix(strings.TrimPrefix(store.key, "templates/"), ".yaml"), "/") {
			t.Errorf("%s: template not published as returned (key %q)", scope, store.key)
		}
		u, err := url.Parse(s.LaunchURL)
		if err != nil {
			t.Fatalf("%s: bad launch url: %v", scope, err)
		}
		if u.Host != "eu-west-1.console.aws.amazon.com" || !strings.HasPrefix(u.Fragment, "/stacks/create/review?") {
			t.Errorf("%s: not a quick-create link: %s", scope, s.LaunchURL)
		}
		q, _ := url.ParseQuery(strings.TrimPrefix(u.EscapedFragment(), "/stacks/create/review?"))
		if q.Get("templateURL") != s.TemplateURL || q.Get("stackName") != "BelluConnector" {
			t.Errorf("%s: templateURL/stackName not encoded correctly: %v", scope, q)
		}
		// The external id must ride in the quick-create URL so it auto-fills.
		if q.Get("param_ExternalId") != "BELLU-X" {
			t.Errorf("%s: launch url must prefill param_ExternalId, got %q", scope, q.Get("param_ExternalId"))
		}
		if !strings.Contains(s.Template, "arn:aws:iam::001018342000:user/bellu-connector") {
			t.Errorf("%s: trust policy must name the Bellu principal", scope)
		}
	}
}

func TestCreateSetupWithoutStoreHasNoLaunchLink(t *testing.T) {
	a := New(Options{STS: &fakeSTS{}})
	s, err := a.CreateSetup(context.Background(), setupIn("account"))
	if err != nil {
		t.Fatal(err)
	}
	if s.LaunchURL != "" || s.TemplateURL != "" {
		t.Errorf("no store configured: want empty launch link, got %q", s.LaunchURL)
	}
	if s.Template == "" {
		t.Error("template must still be returned for download")
	}
}

type fakeOrg struct{ pages [][]orgtypes.Account }

func (f *fakeOrg) ListAccounts(ctx context.Context, in *organizations.ListAccountsInput, _ ...func(*organizations.Options)) (*organizations.ListAccountsOutput, error) {
	i := 0
	if in.NextToken != nil {
		i = 1
	}
	out := &organizations.ListAccountsOutput{Accounts: f.pages[i]}
	if i+1 < len(f.pages) {
		out.NextToken = awssdk.String("next")
	}
	return out, nil
}

func TestListOrganizationAccountsPaginates(t *testing.T) {
	org := &fakeOrg{pages: [][]orgtypes.Account{
		{{Id: awssdk.String("111111111111"), Name: awssdk.String("mgmt"), State: orgtypes.AccountStateActive}},
		{{Id: awssdk.String("222222222222"), Name: awssdk.String("prod"), State: orgtypes.AccountStateActive}},
	}}
	a := New(Options{STS: &fakeSTS{returnCreds: true}, OrgClient: func(*provider.Credentials) OrganizationsAPI { return org }})
	got, err := a.ListOrganizationAccounts(context.Background(), validTarget())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].ID != "222222222222" || got[1].State != "ACTIVE" {
		t.Errorf("unexpected accounts: %+v", got)
	}
	if a.MemberRoleARN("222222222222") != "arn:aws:iam::222222222222:role/BelluConnectorRole" {
		t.Error("bad member role ARN")
	}
}
