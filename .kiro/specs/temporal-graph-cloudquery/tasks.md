# Implementation Plan: temporal-graph-cloudquery

## Overview

This plan evolves Bellu from a stateless inventory graph into a bi-temporal,
event-sourced temporal graph, and replaces Steampipe with CloudQuery at the
collection boundary. It is sequenced to match the design's "Steampipe hard cut
after parity" plan and the two-log temporal model: de-risk CloudQuery first,
land a parity-gated CloudQuery Source and cut Steampipe, then build the observed
log + change engine, the Memgraph projection + temporal queries, the derived
lane (inference + classification), and finally the read surface, retention, and
extra providers.

Every task is incremental, builds on prior tasks, and ends with runnable tests
wherever the design's Testing Strategy and the five correctness properties
(parity, replay determinism, facts/derived separation, reference-counted
deletion, as-of/asKnownAt correctness) apply. Sub-tasks marked `*` are optional
tests that can be skipped for a faster path; core implementation is never
optional.

## Tasks

- [x] 1. Spike: verify CloudQuery PostgreSQL destination behavior (de-risk)
  - [x] 1.1 Verify the CloudQuery AWS source + PostgreSQL destination and record findings
    - Run a scoped `cloudquery sync` against a throwaway AWS fixture into a `cq_staging` schema; capture the exact `_cq_*` internal column set (`_cq_id`, `_cq_source_name`, `_cq_sync_time`, `_cq_parent_id`), the default write mode (overwrite vs append), and AWS plugin licensing/availability
    - Append the confirmed values to the "Risks / Unverified Items" register in `design.md`, moving the CloudQuery destination row from UNVERIFIED to verified with concrete findings
    - Capture a small real staging-row sample per key AWS table (e.g. `aws_iam_roles`, `aws_s3_buckets`) as a fixture under `internal/source/cloudquery/testdata/` for later parity tests
    - _Requirements: 18.3, 1.1_

- [x] 2. CloudQuery Source: staging-row to RawEvent adapter
  - [x] 2.1 Define the table-mapping Catalog and TableSpec (mirror the Steampipe catalog)
    - Add `internal/source/cloudquery/catalog.go` with `Catalog`/`TableSpec` reusing the Steampipe field names (`table`, `id_column`, `scope_column`, `region_column`, `where`, `regions`, `capability`) plus the single new `cq_table` field; `cqTable()` defaults to `Table`, `resourceType()` defaults to `Table`
    - Load/parse the YAML mapping file (reuse the Steampipe catalog loader shape); embed via a `catalog_embed.go` analog if the Steampipe source does so
    - _Requirements: 2.4, 18.1_
  - [x]* 2.2 Write unit tests for Catalog parsing and defaults
    - Assert `cq_table` defaulting, capability field parsing, and region restriction parsing
    - _Requirements: 2.4_
  - [x] 2.3 Implement `cloudquery.Source` with `mapRow` and column handling
    - Add `internal/source/cloudquery/cloudquery.go` implementing `source.Source`: `Name()` returns `cloudquery`; `Collect` reads `cq_staging` rows per table and emits one `rawevent.RawEvent` per row via `mapRow`, selecting id/scope/region columns per `TableSpec`, setting `source="cloudquery"` and `provider` from the mapping, then calling `ev.Finalize()`
    - In `mapRow`, explicitly ignore `_cq_*` internal columns and tolerate unknown columns without failing; keep all staging-schema assumptions inside this package
    - Isolate the DSN/schema (`cq_staging` default) config; no CloudQuery internals leak past this package
    - _Requirements: 2.1, 2.2, 2.3, 2.5, 18.1, 18.2_
  - [x] 2.4 Implement capability gating, skipped-table counting, and progress reporting
    - Gate tables by the connection's enabled capabilities (same semantics as the Steampipe source); exclude gated tables from the read set
    - On a missing/malformed column or absent staging table for one table, log and report that table as skipped through `source.ProgressReporter` and continue with remaining tables (per-table isolation); count and surface skipped tables so a degraded run is visible
    - _Requirements: 2.6, 2.7, 1.5, 18.2_
  - [x]* 2.5 Write unit tests for mapRow, column exclusion, and per-table isolation
    - Table-driven: a populated row maps to exactly one finalized RawEvent with correct source/provider; `_cq_*` and unknown columns ignored; one malformed table is skipped-and-counted while others still emit
    - _Requirements: 2.2, 2.3, 2.5, 2.6, 2.7, 18.2_

- [x] 3. CloudQuery job runner
  - [x] 3.1 Implement the JobRunner and per-connection Spec generation
    - Add the `JobRunner` interface and a CLI-backed implementation in `internal/source/cloudquery/` that generates a per-connection `Spec` (provider, tables from the mapping, regions, `DestSchema="cq_staging"`, `DestDSN`), launches `cloudquery sync`, and reports per-table progress through the reporter
    - Inject STS session credentials via a per-run `env` map only; never write them to the staging DB, the on-disk spec, logs, or returned errors (mirror the `DiscoveryPlan.CredentialEnv` contract)
    - A nil `JobRunner` means "staging already populated" (parity/golden tests)
    - _Requirements: 1.1, 1.2, 1.3, 16.4_
  - [x] 3.2 Implement job failure, cancellation, and no-partial-emit guarantees
    - On non-zero exit, return a job failure and emit no RawEvents from the aborted sync; on `ctx` cancellation, terminate the subprocess and return the cancellation error; discard the env map on every failure path
    - Ensure `Source.Collect` surfaces job failure on the error channel and does not stream partial inventory to the change engine
    - _Requirements: 1.6, 1.7, 16.4_
  - [x]* 3.3 Write unit tests for runner failure/cancel and credential non-persistence
    - Fake runner asserts: non-zero exit yields a failure and zero RawEvents; cancellation returns `ctx.Err()`; credentials never appear in the spec file, logs, or error strings on any path
    - _Requirements: 1.3, 1.6, 1.7, 16.4_

- [x] 4. AWS table-mapping YAML
  - [x] 4.1 Author `internal/source/cloudquery/mapping/aws.yaml`
    - Translate the existing Steampipe AWS catalog table-by-table into the mapping shape, adding `cq_table` (e.g. `aws_iam_role`→`aws_iam_roles`, `aws_s3_bucket`→`aws_s3_buckets`), preserving `id_column`, `scope_column`, `region_column`, `capability`, and per-service `regions`
    - Keep Bellu `table` names identical to the Steampipe catalog so the cut is a data migration, not a schema change
    - _Requirements: 2.4, 14.1_

- [x] 5. Steampipe/CloudQuery parity tests (gates the cutover)
  - [x] 5.1 Build a shared-fixture parity harness
    - Add `internal/source/cloudquery/parity_test.go` that drives the same inventory fixture through both the Steampipe source (via its recorder/fixture path) and `cloudquery.Source` (staging populated from the spike fixtures, nil JobRunner)
    - _Requirements: 12.1_
  - [x] 5.2 Assert byte-identical RawEvents and identical normalized output
    - Assert the two sources emit byte-identical RawEvent sets after `Finalize` (content-addressed compare); then run both through `normalize.Registry` and assert identical `normalized_entities`/`normalized_edges` (**Property 5: Steampipe/CloudQuery parity**)
    - _Requirements: 12.1, 12.2_

- [x] 6. Flip AWS to CloudQuery behind the parity gate
  - [x] 6.1 Wire the AWS adapter and discovery orchestrator to `cloudquery.Factory`
    - Replace the Steampipe source construction in the AWS connector adapter and discovery orchestrator with `cloudquery.Factory`, wiring the JobRunner and mapping; keep the flip gated on the parity tests from task 5 being green
    - Update `backend/cmd/bellu-api/main.go` wiring to construct the CloudQuery source where the Steampipe source was constructed
    - _Requirements: 1.1, 12.3_
  - [x]* 6.2 Integration test the flipped AWS discovery path
    - Drive a discovery run through the orchestrator with a fake JobRunner and assert RawEvents reach the pipeline unchanged
    - _Requirements: 1.1, 12.3_

- [x] 7. Hard-cut Steampipe removal
  - [x] 7.1 Remove the Steampipe package, catalogs, and configuration
    - Delete `internal/source/steampipe` (package + `catalog/`), remove the Steampipe compose service and the `.spc` credential sink from `deploy/`, and remove the `BELLU_STEAMPIPE_*` environment variables from config and `deploy/.env*`
    - Remove remaining Steampipe references from `backend/cmd/bellu-api/main.go` and connector adapters
    - _Requirements: 13.1, 13.2, 13.3_
  - [x] 7.2 Checkpoint - build and full suite green after the cut
    - Ensure the project builds and the full test suite passes; ask the user if questions arise.
    - _Requirements: 13.1_

- [x] 8. Observed temporal store (migration 00015)
  - [x] 8.1 Add migration `00015_observed_events.sql`
    - Create `internal/store/migrations/00015_observed_events.sql` (goose Up/Down) with the `observed_events` table (bi-temporal `event_time`/`recorded_at`, `change_type` CHECK, edge-shape CHECK, indexes) and the `entity_assertions` table, following the `00001`/`00002` conventions; `org_id` on every row
    - _Requirements: 3.1, 16.1_
  - [x]* 8.2 Migration integration test
    - Apply up/down against a test PostgreSQL and assert the schema, CHECK constraints, and indexes exist and reverse cleanly
    - _Requirements: 3.1, 16.1_

- [x] 9. Change engine (observed lane)
  - [x] 9.1 Implement diff + exactly-once classification in `internal/temporal`
    - Add `internal/temporal/engine.go` with `Change`, `Diff`, and `Engine.Apply(ctx, org, runID, source, res, evidence)`; diff the run's entities/edges against the current projection cache (`normalized_entities`/`normalized_edges`), classifying each element under exactly one change type (`entity.created`/`attribute_updated`/`state_changed`/`deleted`/`unchanged`, `relation.added`/`removed`/`attribute_changed`), with `stateField` promoting lifecycle/exposure toggles to `state_changed`
    - Set `event_time` from the run's raw-event observed time and `recorded_at` to ingest time; link each change to its justifying raw-event ids; write observed-event rows only for added/changed/removed elements (write cost bounded by change volume)
    - Persist all observed events and all `entity_assertions` updates for the run in a single transaction; on any failure roll back entirely and return the error without proceeding to projection; serialize per-org
    - _Requirements: 3.2, 3.3, 3.4, 3.5, 3.6, 3.7, 3.8, 17.1, 17.2_
  - [x]* 9.2 Write change-engine unit tests (exactly-once classification)
    - Table-driven cases: created / attribute_updated / state_changed / unchanged (coalesced, no row) and relation.added / removed / attribute_changed, each produced exactly once; assert unchanged writes no row
    - _Requirements: 3.2, 3.3, 3.4, 17.1_
  - [x]* 9.3 Write transaction-rollback unit test
    - Inject a mid-write failure and assert no partial observed events persist, assertions are unchanged, and projection is not attempted
    - _Requirements: 3.7, 3.8_
  - [x] 9.4 Implement reference-counted deletion (replaces `MarkStaleEntitiesBySource`)
    - Add `referenceCountedDeletions`: compare a source's prior `entity_assertions` to what it asserted this run; drop dropped-source rows; emit `entity.deleted` only when zero rows remain for a `canonical_id` across all sources; decrement+count+emit within the same transaction as 9.1; retain assertions and emit no deletions on a failed/aborted run
    - Replace the existing `MarkStaleEntitiesBySource` call sites
    - _Requirements: 5.1, 5.2, 5.3, 5.4, 5.5, 5.6_
  - [x]* 9.5 Write reference-counted deletion tests (**Property 3**)
    - Two sources assert one entity: drop one → no deletion; drop both → `entity.deleted` + projection marked absent; failed run → assertions retained, no deletion
    - _Requirements: 5.3, 5.4, 5.6_

- [x] 10. Wire the change engine into ingest
  - [x] 10.1 Insert the change engine after normalize in the ingest path
    - Call `Engine.Apply` after `normalize.Registry.Normalize` and before the Memgraph projection in the pipeline/ingest wiring; hand the returned changes to the projection step; wire construction in `backend/cmd/bellu-api/main.go`
    - _Requirements: 3.2, 17.1_
  - [x]* 10.2 Integration test ingest → observed_events
    - Run a fixture ingest end-to-end and assert observed events are written with correct change types, evidence links, and bi-temporal fields
    - _Requirements: 3.1, 3.5, 3.6_

- [x] 11. Memgraph projection / rebuild (`internal/temporal/replay`)
  - [x] 11.1 Implement incremental `ApplyRun` reusing `graph.Writer`
    - Add `internal/temporal/replay/rebuilder.go` with `Rebuilder.ApplyRun(ctx, org, observed, derived)`: write the run's observed changes as the base graph via the existing `graph.Writer` MERGE path, then the run's derived changes as overlay (MERGE/SET); reference-counted deletions become MarkStale/absence
    - _Requirements: 7.1, 7.3_
  - [x] 11.2 Implement full `RebuildAsOf` fold and recovery semantics
    - Implement `RebuildAsOf(ctx, org, eventTime, knownAt)`: fold observed → base nodes/edges (excluding deleted) via `SyncEntities`/`SyncEdges`, then fold derived → overlay; idempotent merge/set so re-runs produce the same projection; serve as the authoritative recovery path when incremental apply leaves a suspect state
    - On Memgraph unavailability, preserve the committed logs and support rebuild when it returns (projection failure never loses facts); set a rebuild marker if a run's projection is incomplete
    - _Requirements: 7.2, 7.3, 7.4, 7.5, 7.6_
  - [x]* 11.3 Write replay-determinism test (**Property 1**)
    - For a crafted sequence of runs assert `rebuild(logs ≤ T) == applyRuns(logs ≤ T)` and that Memgraph holds no state not derivable from the two logs
    - _Requirements: 7.3, 7.4_

- [x] 12. Temporal query layer
  - [x] 12.1 Implement `Querier.StateAt` (as-of + asKnownAt)
    - Add `internal/temporal/querier.go` with `StateAt(ctx, org, canonicalID, eventTime, knownAt)`: fold observed (and derived) events with `event_time <= eventTime AND recorded_at <= knownAt` ordered by `(event_time, recorded_at, seq)`, treating terminal `entity.deleted` as absent; `knownAt=now` gives reality view; tenant-scoped by `org_id`
    - _Requirements: 6.1, 6.2, 6.3, 6.4, 16.2_
  - [x] 12.2 Implement `History`, `RecentChanges`, and diff-between-times
    - `History` returns the ordered change stream for an entity with both lanes merged by time; `RecentChanges` returns tenant-scoped changes after a cutoff up to a limit; a between-times diff returns attributes added/removed/changed between two folded states; validate inputs and return plain errors
    - _Requirements: 6.6, 6.7, 6.8, 16.2_
  - [x]* 12.3 Write bi-temporal query tests (**Property 4**)
    - Crafted log with a backdated `event_time` learned later via `recorded_at`: assert reality vs as-of vs asKnownAt results, correct fold order, and monotonicity (fixed `event_time`, increasing `knownAt` only adds information)
    - _Requirements: 6.1, 6.2, 6.3, 6.4, 6.5_

- [x] 13. Derived lane store (migration 00016)
  - [x] 13.1 Add migration `00016_derived_events.sql`
    - Create `internal/store/migrations/00016_derived_events.sql` (goose Up/Down) with `derived_events` (bi-temporal, `kind`, `origin`, `producer`, `change_type` CHECK restricted to derived change types, shape CHECK for inferred_edge vs classification, indexes); `org_id` on every row
    - _Requirements: 4.1, 4.2, 16.1_
  - [x]* 13.2 Write lane-constraint tests (**Property 2**)
    - Assert `observed_events` rejects derived change types and `derived_events` rejects observed change types via the CHECK constraints
    - _Requirements: 4.1, 4.2_

- [x] 14. Inference engine (`internal/temporal/infer`)
  - [x] 14.1 Implement the `Rule` interface and engine
    - Add `internal/temporal/infer/engine.go` with the `Rule` interface, `InferredEdge`, and an engine that runs rules reading only observed/normalized data and writes candidate edges exclusively to `derived_events`; the engine never branches on provider type (new rules added as rules)
    - _Requirements: 8.1, 8.4, 4.3, 4.4_
  - [x] 14.2 Implement the five rule families
    - Add rules: env-var/secret-ref → workload `DEPENDS_ON`; event-source mapping → service `CALLS`; IaC+repo/CI → repo `BUILT_FROM`/`RUNS_IMAGE`; execution role → workload `AUTHENTICATES_AS`; monitor config → monitor `USES`; each emits with confidence + evidence ids, reusing existing `model.Relation` values
    - _Requirements: 8.2, 8.1_
  - [x]* 14.3 Write inference tests (facts/derived separation, **Property 2**)
    - Rule inputs → expected derived edges; assert derived output never appears in `observed_events` and rules never read derived output
    - _Requirements: 8.1, 8.2, 4.3, 4.4_

- [x] 15. Classification engine (`internal/temporal/classify`)
  - [x] 15.1 Implement the rule-based classifier (Stage A)
    - Add `internal/temporal/classify/classify.go` with the `Classifier` interface and a rule-based classifier producing `DataClass` (class, readers, writers, confidence, `classified_by`) from metadata only — naming conventions, IaC, IAM policy reader/writer derivation — never reading datastore contents; write to `derived_events`; emit an `internal`/Unknown fallback when no rule matches; keep the output shape identical so a later model stage needs no projection/schema change
    - _Requirements: 9.1, 9.2, 9.4, 4.4_
  - [x]* 15.2 Write classification tests
    - Metadata inputs → expected `DataClass`; assert no datastore content is read and Unknown fallback applies when no rule matches
    - _Requirements: 9.1, 9.4_

- [x] 16. Project the derived lane into Memgraph
  - [x] 16.1 Project inferred edges and classification properties
    - Extend the replay overlay to MERGE inferred edges with `inferred=true` + confidence + evidence, and SET namespaced node properties `data_class`, `data_class_confidence`, `classified_by` from folded classifications; wire the inference and classification engines into the ingest path after the change engine
    - _Requirements: 7.1, 8.3, 9.3_
  - [x]* 16.2 Write derived-overlay projection test
    - Assert projected inferred edges carry `inferred=true`/confidence/evidence and classified nodes carry the three namespaced props, and that a rebuild reproduces them
    - _Requirements: 8.3, 9.3_

- [x] 17. MCP query server (`internal/mcp`)
  - [x] 17.1 Implement the six read tools behind JWT + RBAC
    - Add `internal/mcp/server.go` with `Server` holding the `Querier` and `graph.Writer.Driver()`; implement `find_entities`, `get_entity`, `get_neighbors`, `entity_history`, `recent_changes`, `describe_schema`; resolve `org` from the authenticated context (ignore client-supplied org); mount behind the existing JWT + RBAC router in `backend/cmd/bellu-api/main.go`
    - _Requirements: 10.1, 10.2, 10.3, 16.2_
  - [x] 17.2 Implement input clamping, lane tagging, and error messages
    - Clamp `get_neighbors` depth to [1,5]; tag each returned edge as asserted or inferred; include entity name alongside id in outputs; return "entity not found" for missing/cross-tenant lookups and a generic "temporarily unavailable" on backend errors (log internal detail, do not return it)
    - _Requirements: 10.4, 10.5, 10.6, 10.7, 10.8_
  - [x]* 17.3 Write MCP tests (tenant isolation + contract)
    - Assert org resolved from JWT and client org ignored; depth clamped at 5; names present; plain not-found and unavailable messages; no cross-tenant existence leak
    - _Requirements: 10.2, 10.4, 10.5, 10.6, 10.7, 10.8, 16.2_

- [x] 18. Retention and heartbeat coalescing
  - [x] 18.1 Implement coalescing, baseline events, and pruning
    - In the change engine, coalesce consecutive `*.unchanged` observations (write no row; bump `entity_assertions.last_seen`); at the retention horizon write a baseline event (full snapshot, `change_type` attribute_updated, `event_time=horizon`) before pruning; prune observed/derived rows older than the horizon except baselines
    - _Requirements: 11.1, 11.2, 11.3_
  - [x]* 18.2 Write retention / as-of-after-prune test
    - Assert an as-of query targeting a time after the pruned window resolves correct state from the retained baseline
    - _Requirements: 11.2, 11.4_

- [x] 19. Additional collection providers
  - [x] 19.1 Add Azure, GCP, and Kubernetes mapping files
    - Add `internal/source/cloudquery/mapping/{azure,gcp,k8s}.yaml` in the same shape as `aws.yaml` so each provider collects through a CloudQuery job with no new pipeline mechanism; guard on plugin availability per the spike register
    - _Requirements: 14.1_
  - [x] 19.2 Add identity-provider and Datadog/Sentry config mappings
    - Add mapping files for an identity provider (inventory) and Datadog/Sentry (monitor configuration only, feeding monitor→resource edges, not log streams); gate each on plugin availability
    - _Requirements: 14.2, 14.3_
  - [x]* 19.3 Write mapping-parse tests for the new providers
    - Assert each new mapping parses and produces the expected table set under capability gating
    - _Requirements: 14.1, 14.2, 14.3_

- [x] 20. Final checkpoint
  - [x] 20.1 Checkpoint - full build and test suite green
    - Ensure the project builds and all tests pass; ask the user if questions arise.
    - _Requirements: 12.3, 13.1_

- [x] 21. (Optional) OTLP change exporter
  - [x]* 21.1 Implement a failure-isolated OTLP exporter from the observed lane
    - Add an optional exporter that, when enabled, emits observed change events as OTLP entity events; when disabled, ingest and projection complete without exporting; a failing exporter never fails ingest or projection
    - _Requirements: 15.1, 15.2_

## Notes

- Tasks marked `*` are optional test sub-tasks and can be skipped for a faster MVP; core implementation tasks are never optional.
- Each task references specific requirement sub-clauses for traceability.
- Checkpoints (7.2, 20.1) ensure incremental validation at the two highest-risk seams (the Steampipe hard cut and final integration).
- Property-based/correctness tests are placed next to the code they validate:
  Property 5 (parity) at task 5, Property 3 (reference-counted deletion) at 9.5,
  Property 1 (replay determinism) at 11.3, Property 4 (as-of/asKnownAt) at 12.3,
  Property 2 (facts/derived separation) at 13.2 and 14.3.
- Phase 0 (task 1) must land before the CloudQuery adapter is relied on in
  production, per the design's unverified-items register.

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1.1"] },
    { "id": 1, "tasks": ["2.1", "4.1", "8.1", "13.1"] },
    { "id": 2, "tasks": ["2.2", "2.3", "8.2", "13.2"] },
    { "id": 3, "tasks": ["2.4", "2.5", "3.1", "9.1"] },
    { "id": 4, "tasks": ["3.2", "3.3", "9.2", "9.3", "9.4"] },
    { "id": 5, "tasks": ["5.1", "9.5", "10.1", "14.1", "15.1"] },
    { "id": 6, "tasks": ["5.2", "10.2", "11.1", "14.2", "15.2"] },
    { "id": 7, "tasks": ["6.1", "11.2", "14.3", "18.1"] },
    { "id": 8, "tasks": ["6.2", "11.3", "12.1", "16.1", "18.2"] },
    { "id": 9, "tasks": ["7.1", "12.2", "16.2"] },
    { "id": 10, "tasks": ["12.3", "17.1"] },
    { "id": 11, "tasks": ["17.2", "19.1", "19.2"] },
    { "id": 12, "tasks": ["17.3", "19.3", "21.1"] }
  ]
}
```
