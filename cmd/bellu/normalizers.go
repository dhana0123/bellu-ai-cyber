package main

import (
	"github.com/bellu/bellu-ai-cyber/internal/normalize"
	"github.com/bellu/bellu-ai-cyber/internal/resolve"
)

// buildNormalizers wires the normalizer registry. Cloud/k8s/zeek normalizers
// are added in later tasks.
func buildNormalizers() *normalize.Registry {
	r := normalize.NewRegistry()
	r.Register(normalize.CycloneDX{})
	r.Register(normalize.AWS{})
	r.Register(normalize.Azure{})
	r.Register(normalize.GCP{})
	r.Register(normalize.K8s{})
	return r
}

// buildResolver returns the active resolver. Task 10 swaps the pass-through for
// the deterministic multi-key resolver.
func buildResolver() resolve.Resolver {
	return resolve.PassThrough{}
}
