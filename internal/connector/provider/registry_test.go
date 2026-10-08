package provider

import (
	"context"
	"testing"
)

// stubProvider is a minimal CloudProvider for registry tests.
type stubProvider struct{ name string }

func (s stubProvider) Name() string                                            { return s.name }
func (s stubProvider) CreateSetup(context.Context, SetupInput) (*Setup, error) { return nil, nil }
func (s stubProvider) VerifyConnection(context.Context, Target) (*VerifyResult, error) {
	return nil, nil
}
func (s stubProvider) Authenticate(context.Context, Target) (*Credentials, error) { return nil, nil }
func (s stubProvider) GetCredentials(context.Context, Target) (*Credentials, error) {
	return nil, nil
}
func (s stubProvider) CheckPermissions(context.Context, Target, *Credentials, []string) (*VerifyResult, error) {
	return nil, nil
}
func (s stubProvider) Discover(context.Context, Target, *Credentials) (*DiscoveryPlan, error) {
	return nil, nil
}

func TestRegistryRegisterResolve(t *testing.T) {
	r := NewRegistry()
	r.Register(stubProvider{name: "aws"})

	p, ok := r.Resolve("aws")
	if !ok {
		t.Fatal("Resolve(aws) not found after Register")
	}
	if p.Name() != "aws" {
		t.Errorf("resolved provider name = %q, want aws", p.Name())
	}
}

func TestRegistryResolveUnknown(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.Resolve("gcp"); ok {
		t.Error("Resolve of unregistered provider should report not found")
	}
}

func TestRegistryRegisterNilIgnored(t *testing.T) {
	r := NewRegistry()
	r.Register(nil) // must not panic
	if _, ok := r.Resolve(""); ok {
		t.Error("nil register should not create an entry")
	}
}
