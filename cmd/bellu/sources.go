package main

import (
	"github.com/bellu/bellu-ai-cyber/internal/source"
	"github.com/bellu/bellu-ai-cyber/internal/source/fixture"
)

// buildRegistry wires all available sources. New sources are registered here
// as later tasks add them (cyclonedx, steampipe, zeek).
func buildRegistry() *source.Registry {
	r := source.NewRegistry()
	r.Register("fixture", fixture.Factory)
	return r
}
