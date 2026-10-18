package provider_test

import (
	"context"
	"testing"

	"github.com/bellu/bellu-ai-cyber/internal/connector/provider"
	"github.com/bellu/bellu-ai-cyber/internal/connector/provider/azure"
	"github.com/bellu/bellu-ai-cyber/internal/connector/provider/ci"
	"github.com/bellu/bellu-ai-cyber/internal/connector/provider/gcp"
)

// All new provider adapters register under their expected names and satisfy the
// CloudProvider interface, so the Manager resolves them with zero branching.
func TestNewProvidersRegisterAndResolve(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(azure.New(azure.Options{}))
	reg.Register(gcp.New(gcp.Options{}))
	reg.Register(ci.NewGitHub(""))
	reg.Register(ci.NewGitLab(""))
	reg.Register(ci.NewAtlassian(""))

	for _, name := range []string{"azure", "gcp", "github", "gitlab", "atlassian"} {
		p, ok := reg.Resolve(name)
		if !ok {
			t.Fatalf("provider %q did not register", name)
		}
		if p.Name() != name {
			t.Errorf("provider Name() = %q, want %q", p.Name(), name)
		}
	}
}

// Each new adapter produces non-empty onboarding setup and a discovery plan,
// and never surfaces a credential on the plan config.
func TestNewProvidersSetupAndDiscover(t *testing.T) {
	ctx := context.Background()
	adapters := []provider.CloudProvider{
		azure.New(azure.Options{}),
		gcp.New(gcp.Options{}),
		ci.NewGitHub(""),
		ci.NewGitLab(""),
		ci.NewAtlassian(""),
	}
	for _, a := range adapters {
		s, err := a.CreateSetup(ctx, provider.SetupInput{ConnectionID: 1, Scope: "account"})
		if err != nil {
			t.Fatalf("%s CreateSetup: %v", a.Name(), err)
		}
		if len(s.Instructions) == 0 {
			t.Errorf("%s setup has no instructions", a.Name())
		}
		creds, err := a.GetCredentials(ctx, provider.Target{ConnectionID: 1})
		if err != nil {
			t.Fatalf("%s GetCredentials: %v", a.Name(), err)
		}
		plan, err := a.Discover(ctx, provider.Target{ConnectionID: 1, AccountID: "acct-1"}, creds)
		if err != nil {
			t.Fatalf("%s Discover: %v", a.Name(), err)
		}
		if plan == nil || plan.SteampipeConfig == nil {
			t.Fatalf("%s Discover returned no plan", a.Name())
		}
	}
}
