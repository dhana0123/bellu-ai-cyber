# Implementation Plan

- [ ] 1. Write bug condition exploration test (BEFORE implementing the fix)
  - **Property 1: Bug Condition** - Ungranted-capability tables are queried on unfixed code
  - **CRITICAL**: This test MUST FAIL on unfixed code - failure confirms the bug exists
  - **DO NOT attempt to fix the test or the code when it fails**
  - **NOTE**: This test encodes the expected behavior (skip the gated table, no query, no error) - it will validate the fix when it passes after implementation
  - **GOAL**: Surface counterexamples demonstrating the collector issues a query for a table whose required capability is not in the connection's enabled set
  - **Scoped PBT Approach**: Scope the property to concrete gated cases from the design examples so the counterexamples are reproducible: `aws_eks_cluster` (capability `eks`), `aws_ecs_task_definition`/`aws_ecr_image`/`aws_lambda_function` (capability `workload`), and DSPM-gated tables, each with enabled set `["core"]`
  - In `internal/source/steampipe/steampipe_test.go`, drive `Collect` with a fake/recording Steampipe query layer that captures every table name the collector attempts to query (no live AWS)
  - Build a catalog containing a gated table (e.g. a `TableSpec` with `Capability: "eks"`) plus core/ungated tables, and an enabled set `["core"]` that excludes the gated capability (threaded via the `capabilities` config key, which does not yet gate anything)
  - Assert the property: for all `(table, enabledCapabilities)` where `isBugCondition` holds (`table.Capability != "" && table.Capability != "core" && table.Capability NOT IN enabledCapabilities`), the gated table name appears in the recorded "queried" set on UNFIXED code
  - Cover the four design test cases: EKS not granted, workload not granted, DSPM not granted, and an out-of-set edge case where the enabled set names a capability the catalog does not use
  - Run test on UNFIXED code
  - **EXPECTED OUTCOME**: Test FAILS (the gated table is queried / the "skip" assertion does not hold) - this is correct and proves the bug exists
  - Document counterexamples found (e.g. "collector attempted a query for `aws_eks_cluster` with enabled set `[core]` instead of skipping it") and confirm the root cause: `Collect` has no gate and `Source` carries no enabled-capability data
  - Mark task complete when the test is written, run, and the failure is documented
  - _Requirements: 1.1, 1.2, 1.3, 1.4_

- [ ] 2. Write preservation property tests (BEFORE implementing the fix)
  - **Property 2: Preservation** - Enabled, core, and ungated tables behave identically
  - **IMPORTANT**: Follow the observation-first methodology - run the UNFIXED collector first, record what it does, then write property-based tests asserting that recorded behavior
  - Observe on UNFIXED code with the recording query layer: which tables are queried and the `RawEvent`s emitted (same `Scope`, `Region`, `ResourceType`, `ResourceID` columns) for non-bug inputs
  - Write property-based tests (generate random `(enabled capability set, catalog of tables with random capability annotations)` pairs) asserting, for all inputs where `isBugCondition` is false:
    - Core always collected: enabled `["core"]` and any superset - all `core` tables (`aws_account`, `aws_iam_role`, `aws_iam_user`, `aws_iam_policy`, `aws_s3_bucket`, `aws_ec2_instance`, `aws_vpc_security_group`) are queried (Requirement 3.2)
    - Enabled gated table collected: enabled `["core","eks"]` - `aws_eks_cluster` is queried and emits the same `RawEvent`s (Requirements 3.1, 3.3)
    - Ungated table collected: a table with empty `capability` is always queried regardless of enabled set
    - Error isolation preserved: a non-capability error (e.g. decode failure) on an enabled table is still recorded per table and the loop proceeds to the next table (Requirement 3.4)
    - No-gating-configured fallback: when no `capabilities` key is supplied, every table is queried (back-compat with callers that do not pass capabilities)
    - Catalog validation unchanged: loading passes for specs with and without a `capability` field, and emitted `RawEvent`s keep the same scope, region, resource type, and id columns (Requirement 3.5)
  - Generate random non-capability errors on enabled tables and assert per-table isolation holds for every generated case
  - Run tests on UNFIXED code
  - **EXPECTED OUTCOME**: Tests PASS (this confirms the baseline behavior to preserve)
  - Mark task complete when the tests are written, run, and passing on unfixed code
  - _Requirements: 3.1, 3.2, 3.3, 3.4, 3.5_

- [ ] 3. Fix for ungranted capability-gated tables being queried

  - [ ] 3.1 Add the `Capability` field to `TableSpec` and annotate the catalog
    - In `internal/source/steampipe/catalog.go`, add `Capability string ` + "`yaml:\"capability\"`" + ` to `TableSpec`; empty means ungated (always collected), a non-empty value names the granting capability (`core`, `eks`, `workload`, `dspm`); document that `core` is treated as always-on
    - Do NOT add `Capability` as a required field in `Catalog.Validate()` - an empty capability is valid and means ungated - so validation stays unchanged
    - In `internal/source/steampipe/catalog/aws.yaml`, add a `capability:` key per table, derived from `internal/connector/capability/capability.go`:
      - `core`: `aws_account`, `aws_iam_role`, `aws_iam_user`, `aws_iam_policy`, `aws_s3_bucket`, `aws_ec2_instance`, `aws_vpc_security_group`
      - `eks`: `aws_eks_cluster`
      - `workload`: `aws_ecs_task_definition`, `aws_ecr_image`, `aws_lambda_function`
      - `dspm`: data-store tables gated by DSPM (match each table's required IAM action to the `dspm` `Permissions` list in `capability.go`)
      - For tables not covered by any capability bundle (e.g. `aws_bedrock_custom_model`, `aws_sagemaker_model`): assign the capability whose IAM bundle actually grants them, or leave `capability` empty if no bundle grants them so current behavior is preserved
    - _Bug_Condition: isBugCondition((table, enabledCapabilities)) where table.Capability != "" && table.Capability != "core" && table.Capability NOT IN enabledCapabilities_
    - _Expected_Behavior: Property 1 - gated table is skipped (no query, no error) when its capability is not enabled_
    - _Preservation: Catalog validation unchanged (empty capability valid); core/ungated tables unaffected (Requirement 3.5)_
    - _Requirements: 2.4, 3.2, 3.5_

  - [ ] 3.2 Thread `Target.Capabilities` through `aws/adapter.Discover` into `SteampipeConfig`
    - In `internal/connector/provider/aws/adapter.go`, in `Discover`, add `t.Capabilities` to the `cfg` map (e.g. `"capabilities": t.Capabilities`) so the plan's `SteampipeConfig` carries the connection's enabled capability set
    - No orchestrator change required: it passes `plan.SteampipeConfig` straight to `steampipe.Factory`
    - _Bug_Condition: isBugCondition((table, enabledCapabilities)) - enabled set must reach the collector to be gated on_
    - _Expected_Behavior: Property 1 - enabled set available to the collector so ungranted tables can be skipped_
    - _Preservation: Property 2 - callers/targets that pass capabilities see no behavior change for enabled tables_
    - _Requirements: 2.1, 2.2, 2.3, 2.4_

  - [ ] 3.3 Read capabilities in `steampipe.Factory` into `Source.enabledCaps` and add the gate in `Collect`
    - In `internal/source/steampipe/steampipe.go`, add `enabledCaps map[string]bool` to `Source` (a set for O(1) lookup); a nil/empty set means "no gating configured" and MUST preserve current behavior (collect everything)
    - In `Factory`, read the new `capabilities` config key (`[]string`/`[]any`) via a small helper and populate `src.enabledCaps`; ALWAYS add `core` to the set so core tables are never gated out even if a caller omits it
    - Add a `capabilityEnabled(cap string) bool` helper returning true when: the capability is empty, OR equals `core`, OR `s.enabledCaps` is empty (no gating configured), OR `s.enabledCaps[cap]` is true
    - In `Collect`, inside `for _, spec := range s.catalog.Tables`, BEFORE creating the per-table timeout context and calling `collectTable`, add `if !s.capabilityEnabled(spec.Capability) { continue }` so a skipped table issues no query and surfaces no error on the error channel; every non-skipped table flows through the existing code path unchanged
    - _Bug_Condition: isBugCondition((table, enabledCapabilities)) where table.Capability != "" && table.Capability != "core" && table.Capability NOT IN enabledCapabilities_
    - _Expected_Behavior: Property 1 - `capabilityEnabled(spec.Capability)` is false for the bug condition, so `continue` skips the table (no query, no error)_
    - _Preservation: Property 2 - empty/`core`/enabled capability and empty `enabledCaps` all return true, preserving the existing query loop and per-table error isolation (Requirements 3.1-3.5)_
    - _Requirements: 2.1, 2.2, 2.3, 2.4, 3.1, 3.2, 3.3, 3.4, 3.5_

  - [ ] 3.4 Add unit tests for the gate and factory wiring
    - `capabilityEnabled` returns true for empty capability, for `core`, and for a capability present in the enabled set; false for a non-`core` capability absent from a non-empty enabled set
    - `Collect` skips a gated table whose capability is not enabled (no query attempted, no error emitted) and queries a gated table whose capability is enabled, emitting rows unchanged
    - `Factory` populates `enabledCaps` from the `capabilities` config key and always includes `core`
    - Catalog validation still passes for specs with and without a `capability` field (empty capability is valid)
    - _Requirements: 2.1, 2.2, 2.3, 2.4, 3.2, 3.5_

  - [ ] 3.5 Verify bug condition exploration test now passes
    - **Property 1: Expected Behavior** - Ungranted-capability tables are skipped
    - **IMPORTANT**: Re-run the SAME test from task 1 - do NOT write a new test
    - The test from task 1 encodes the expected behavior; when it passes it confirms gated tables are skipped (no query, no `AccessDeniedException` on the error channel)
    - Run the bug condition exploration test from step 1
    - **EXPECTED OUTCOME**: Test PASSES (confirms the bug is fixed)
    - _Requirements: 2.1, 2.2, 2.3, 2.4_

  - [ ] 3.6 Verify preservation tests still pass
    - **Property 2: Preservation** - Enabled, core, and ungated tables behave identically
    - **IMPORTANT**: Re-run the SAME tests from task 2 - do NOT write new tests
    - Run the preservation property tests from step 2
    - **EXPECTED OUTCOME**: Tests PASS (confirms no regressions - core always collected, enabled gated tables collected, ungated tables collected, error isolation preserved, no-gating fallback intact)
    - _Requirements: 3.1, 3.2, 3.3, 3.4, 3.5_

- [ ] 4. Add integration tests end-to-end through adapter, factory, and collector
  - In `internal/source/steampipe/steampipe_integration_test.go` (or an adapter-level test), drive `aws/adapter.Discover` -> `steampipe.Factory` -> `Collect` with the recording query layer
  - A `Target` with `Capabilities: ["core"]` produces a plan whose `SteampipeConfig` carries the capability set, and the collector skips `aws_eks_cluster`
  - A `Target` with `Capabilities: ["core","eks"]` collects `aws_eks_cluster` and emits its `RawEvent`s
  - Switching the enabled set between runs changes exactly which gated tables are queried, with core tables queried in every run
  - _Requirements: 2.1, 2.4, 3.1, 3.2, 3.3_

- [ ] 5. Checkpoint - Ensure all tests pass
  - Run the full test suite (`go test ./...` or the project's `make test`) and confirm the exploration test (Property 1) and all preservation and integration tests pass with no regressions
  - Ensure all tests pass; ask the user if questions arise
