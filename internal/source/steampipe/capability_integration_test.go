package steampipe

import (
	"context"
	"testing"

	"github.com/bellu/bellu-ai-cyber/internal/connector/provider"
	awsprov "github.com/bellu/bellu-ai-cyber/internal/connector/provider/aws"
)

// TestIntegration_AdapterToFactoryToCollectorGatesTables wires the real path a
// discovery run takes: the AWS adapter's Discover builds the Steampipe config
// map from a connection's enabled capabilities, steampipe.Factory consumes that
// map, and Collect gates table queries accordingly. A recording connection is
// injected in place of a live Steampipe service so the test observes exactly
// which tables are queried without any AWS or Postgres dependency.
//
// This is the end-to-end regression guard for the bug: a connection granted
// only core+eks must never query the workload/ai tables (which would produce
// AccessDenied against the customer account).
func TestIntegration_AdapterToFactoryToCollectorGatesTables(t *testing.T) {
	adapter := awsprov.New(awsprov.Options{})

	target := provider.Target{
		ConnectionID: 42,
		AccountID:    "111122223333",
		RoleARN:      "arn:aws:iam::111122223333:role/bellu",
		ExternalID:   "ext-1",
		Region:       "us-east-1",
		Capabilities: []string{"core", "eks"},
	}
	creds := &provider.Credentials{
		AccessKeyID:     "AKIA_TEST",
		SecretAccessKey: "secret",
		SessionToken:    "token",
	}

	plan, err := adapter.Discover(context.Background(), target, creds)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if plan.SteampipeConfig == nil {
		t.Fatalf("Discover returned nil SteampipeConfig")
	}

	// The adapter must have threaded the enabled capabilities into the config.
	if _, ok := plan.SteampipeConfig["capabilities"]; !ok {
		t.Fatalf("SteampipeConfig missing \"capabilities\" key; adapter did not thread Target.Capabilities")
	}

	// Build the real Source from the plan's config (resolves embedded aws.yaml),
	// then swap only the connection for a recorder.
	src, err := Factory(plan.SteampipeConfig)
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	s := src.(*Source)
	rec := &recordingQuerier{}
	s.connect = func(context.Context) (querier, func(), error) { return rec, func() {}, nil }

	_, _ = drain(t, s)

	// Enabled (core + eks): these real aws.yaml tables must be queried.
	mustQuery := []string{
		"aws_account",     // core
		"aws_iam_role",    // core
		"aws_s3_bucket",   // core
		"aws_eks_cluster", // eks (enabled)
	}
	for _, tbl := range mustQuery {
		if !rec.queried(tbl) {
			t.Errorf("table %q must be queried for capabilities core+eks, but it was skipped", tbl)
		}
	}

	// Not enabled (workload, ai): these real aws.yaml tables must be skipped so
	// no AccessDenied is produced against the customer account.
	mustSkip := []string{
		"aws_ecs_task_definition", // workload
		"aws_ecr_image",           // workload
		"aws_lambda_function",     // workload
		"aws_bedrock_custom_model", // ai
		"aws_sagemaker_model",      // ai
	}
	for _, tbl := range mustSkip {
		if rec.queried(tbl) {
			t.Errorf("table %q must be skipped for capabilities core+eks (ungranted), but it was queried", tbl)
		}
	}
}

// TestIntegration_NoCapabilitiesCollectsEverything confirms the backward-
// compatible fallback through the real adapter path: a Target with no enabled
// capabilities leaves gating off, so every aws.yaml table is queried (the
// pre-fix behavior, preserved).
func TestIntegration_NoCapabilitiesCollectsEverything(t *testing.T) {
	adapter := awsprov.New(awsprov.Options{})
	target := provider.Target{ConnectionID: 7, Capabilities: nil}
	creds := &provider.Credentials{AccessKeyID: "AKIA", SecretAccessKey: "s", SessionToken: "t"}

	plan, err := adapter.Discover(context.Background(), target, creds)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	src, err := Factory(plan.SteampipeConfig)
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	s := src.(*Source)
	rec := &recordingQuerier{}
	s.connect = func(context.Context) (querier, func(), error) { return rec, func() {}, nil }

	_, _ = drain(t, s)

	// Every table in the real catalog must have been queried.
	for _, spec := range s.catalog.Tables {
		if !rec.queried(spec.Table) {
			t.Errorf("with no capabilities set, table %q must be queried (no-gating fallback), but it was skipped", spec.Table)
		}
	}
}
