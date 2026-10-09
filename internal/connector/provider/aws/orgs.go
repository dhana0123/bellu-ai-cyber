package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/organizations"

	"github.com/bellu/bellu-ai-cyber/internal/connector/provider"
)

// OrganizationsAPI is the subset of the Organizations client used to list
// member accounts, so listing can be unit-tested with a fake.
type OrganizationsAPI interface {
	organizations.ListAccountsAPIClient
}

// OrgClientFactory builds an Organizations client that calls AWS as the
// assumed management-account role.
type OrgClientFactory func(creds *provider.Credentials) OrganizationsAPI

// defaultOrgClient calls the Organizations API (a us-east-1 global endpoint)
// with the assumed-role credentials.
func defaultOrgClient(c *provider.Credentials) OrganizationsAPI {
	return organizations.New(organizations.Options{
		Region: "us-east-1",
		Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
			return aws.Credentials{
				AccessKeyID:     c.AccessKeyID,
				SecretAccessKey: c.SecretAccessKey,
				SessionToken:    c.SessionToken,
				Expires:         c.Expiry,
			}, nil
		}),
	})
}

// ListOrganizationAccounts assumes the management-account role (through the
// session cache) and lists every account in the organization. It requires the
// management role's organizations:ListAccounts permission, which the
// organization template grants.
func (a *Adapter) ListOrganizationAccounts(ctx context.Context, t provider.Target) ([]provider.OrgAccount, error) {
	creds, err := a.GetCredentials(ctx, t)
	if err != nil {
		return nil, err
	}
	client := a.orgClient(creds)
	var out []provider.OrgAccount
	p := organizations.NewListAccountsPaginator(client, &organizations.ListAccountsInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("aws: list organization accounts: %w", err)
		}
		for _, acct := range page.Accounts {
			out = append(out, provider.OrgAccount{
				ID:    aws.ToString(acct.Id),
				Name:  aws.ToString(acct.Name),
				State: string(acct.State),
			})
		}
	}
	return out, nil
}

// MemberRoleARN returns the Bellu role ARN the StackSet created in a member
// account.
func (a *Adapter) MemberRoleARN(accountID string) string {
	return fmt.Sprintf("arn:aws:iam::%s:role/%s", accountID, DefaultRoleName)
}

var (
	_ provider.OrganizationLister = (*Adapter)(nil)
	_ provider.MemberRoleARNer    = (*Adapter)(nil)
)
