// Package finding is the durable record of a correlation result: a graph asset
// matched to a canonical vulnerability, carrying its computed risk score,
// severity bucket, and open/resolved lifecycle status. Findings are deduped per
// (asset_id, vuln_id) by the findings table's UNIQUE constraint (migration
// 00006_findings.sql) and persisted through the shared *store.Store pool, the
// same database access path used by the intel_* persistence layer.
//
// Lifecycle (Requirement 12): a correlation run upserts every matched candidate
// as an open finding, preserving first_seen and advancing last_seen; a later
// run that no longer matches an existing open finding transitions it to
// resolved. Re-running with an unchanged set of matches is idempotent except
// for the last_seen timestamp (correctness Property 3).
package finding

import (
	"time"

	"github.com/bellu/bellu-ai-cyber/bellu-global-intelligence/canonical"
	"github.com/bellu/bellu-ai-cyber/internal/model"
)

// Status is a finding's lifecycle state.
type Status string

const (
	// StatusOpen marks a finding whose asset still matches the vulnerability as
	// of the most recent correlation run.
	StatusOpen Status = "open"
	// StatusResolved marks a finding whose asset no longer matched in a later
	// correlation run.
	StatusResolved Status = "resolved"
)

// Finding links a graph asset to a canonical vulnerability with its risk score,
// severity, and lifecycle status. It maps one-to-one onto a row of the findings
// table and is unique per (AssetID, VulnID).
type Finding struct {
	// ID is the findings.id surrogate key (BIGSERIAL). Zero before the first
	// insert; the database assigns it.
	ID int64
	// AssetID is the graph canonical_id of the vulnerable asset.
	AssetID string
	// AssetKind is the graph node kind of the asset.
	AssetKind model.Kind
	// VulnID is the canonical intel vulnerability id (intel_vulnerabilities.id).
	VulnID string
	// IntelRef is the cited intel record id the match came from.
	IntelRef string
	// RiskScore is the computed risk in [0,100].
	RiskScore float64
	// Severity is the qualitative severity bucket, reusing the canonical
	// vocabulary shared with the risk engine.
	Severity canonical.Severity
	// Status is the open/resolved lifecycle state.
	Status Status
	// FirstSeen is when this (asset, vuln) was first recorded; preserved across
	// upserts.
	FirstSeen time.Time
	// LastSeen is when this finding was last observed in a correlation run.
	LastSeen time.Time
}
