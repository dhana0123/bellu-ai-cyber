package aws

import (
	"context"

	"github.com/bellu/bellu-ai-cyber/internal/connector/capability"
	"github.com/bellu/bellu-ai-cyber/internal/connector/provider"
)

// ProbeFunc runs one read-only permission probe and reports whether the
// permission is granted. A non-nil error (e.g. an AccessDenied from AWS) is a
// FAIL with the error detail; a nil error is a PASS. Implementations MUST issue
// only read-only API calls.
type ProbeFunc func(ctx context.Context, creds *provider.Credentials, permission string) error

// Verifier runs read-only probes for the enabled capabilities and shapes the
// results. The probe function is injectable so the verifier is unit-testable
// without live AWS.
type Verifier struct {
	probe ProbeFunc
}

// NewVerifier builds a Verifier. When probe is nil, every probe is treated as a
// PASS (used only where the caller supplies results another way); production
// wiring always injects a real read-only probe.
func NewVerifier(probe ProbeFunc) *Verifier {
	return &Verifier{probe: probe}
}

// Verify runs the probes for each enabled capability and returns a structured
// result. Capabilities in the catalog that are not enabled on the connection
// are reported NOT_ENABLED. Overall OK is true only when every checked
// permission passed; otherwise every failing permission is listed in Failures.
func (v *Verifier) Verify(ctx context.Context, creds *provider.Credentials, enabled []string) *provider.VerifyResult {
	enabledSet := map[string]bool{}
	for _, c := range enabled {
		enabledSet[c] = true
	}

	res := &provider.VerifyResult{OK: true}

	// Iterate the catalog in a stable order (core, eks, workload, dspm) so the
	// output is deterministic.
	for _, capName := range []capability.Capability{
		capability.Core, capability.EKS, capability.Workload, capability.DSPM,
	} {
		name := string(capName)
		def := capability.Catalog[capName]

		if !enabledSet[name] {
			res.Capabilities = append(res.Capabilities, provider.CapabilityResult{
				Capability: name,
				Outcome:    provider.OutcomeNotEnabled,
			})
			continue
		}

		capRes := provider.CapabilityResult{Capability: name, Outcome: provider.OutcomePass}
		for _, p := range def.Permissions {
			pr := provider.PermissionResult{
				Capability: name,
				Permission: p,
				Outcome:    provider.OutcomePass,
			}
			if v.probe != nil {
				if err := v.probe(ctx, creds, p); err != nil {
					pr.Outcome = provider.OutcomeFail
					pr.Detail = err.Error()
				}
			}
			if pr.Outcome == provider.OutcomeFail {
				capRes.Outcome = provider.OutcomeFail
				res.OK = false
				res.Failures = append(res.Failures, pr)
			}
			capRes.Checks = append(capRes.Checks, pr)
		}
		res.Capabilities = append(res.Capabilities, capRes)
	}

	return res
}
