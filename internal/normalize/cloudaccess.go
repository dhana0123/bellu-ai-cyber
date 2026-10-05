package normalize

import (
	"github.com/bellu/bellu-ai-cyber/internal/model"
	"github.com/bellu/bellu-ai-cyber/internal/rawevent"
)

// accessLevel is the provider-independent classification of a role's data
// access, shared by the Azure and GCP normalizers.
type accessLevel string

const (
	accessRead  accessLevel = "read"
	accessWrite accessLevel = "write"
	accessAdmin accessLevel = "admin"
)

// cloudAccessEdge builds a CAN_READ/WRITE/ADMIN edge from an identity to a data
// store, derived from a role grant. These are approximate (role-name based,
// ignoring conditions and scope inheritance), so confidence is 0.7 and
// effective is "approx".
func cloudAccessEdge(e rawevent.RawEvent, identityKey, storeKey string, acc accessLevel, role string) model.Edge {
	rel := model.RelCanRead
	switch acc {
	case accessWrite:
		rel = model.RelCanWrite
	case accessAdmin:
		rel = model.RelCanAdmin
	}
	return model.Edge{
		Relation: rel,
		SrcKey:   identityKey,
		DstKey:   storeKey,
		Attributes: map[string]any{
			"role":      role,
			"effective": "approx",
		},
		Confidence: 0.7,
		Evidence:   EvidenceFrom(e),
	}
}
