package provider

// Registry maps a provider name to its CloudProvider implementation. New
// providers register themselves at construction; the Connector Manager resolves
// by name without branching on provider type, so adding GCP or Azure requires
// zero Manager changes.
type Registry struct {
	byName map[string]CloudProvider
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{byName: map[string]CloudProvider{}}
}

// Register adds a provider under its Name(). A later registration for the same
// name replaces the earlier one.
func (r *Registry) Register(p CloudProvider) {
	if p == nil {
		return
	}
	r.byName[p.Name()] = p
}

// Resolve returns the provider registered under name, and whether it was found.
func (r *Registry) Resolve(name string) (CloudProvider, bool) {
	p, ok := r.byName[name]
	return p, ok
}
