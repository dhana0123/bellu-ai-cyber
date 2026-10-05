package main

import (
	"github.com/bellu/bellu-ai-cyber/internal/source"
	"github.com/bellu/bellu-ai-cyber/internal/source/cyclonedx"
	"github.com/bellu/bellu-ai-cyber/internal/source/fixture"
)

// buildRegistry wires all available sources. New sources are registered here
// as later tasks add them (steampipe, zeek).
func buildRegistry() *source.Registry {
	r := source.NewRegistry()
	r.Register("fixture", fixture.Factory)
	r.Register("cyclonedx", cyclonedx.Factory)
	return r
}
