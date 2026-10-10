# EKS Capability Gating Fix Bugfix Design

## Overview

The Steampipe collector queries every table in its catalog (`internal/source/steampipe/catalog/aws.yaml`) unconditionally. Capability-gated tables such as `aws_eks_cluster` require IAM permissions that only exist when the matching capability (`eks`, `workload`, `dspm`) is granted for the connection. When a capability is not granted, the assumed `BelluConnectorRole` lacks the permission and Steampipe returns an `AccessDeniedException` (403) at query time, which the collector then surfaces on its error channel.

The fix associates every catalog table with the capability that grants it, threads the connection's enabled capability set from the AWS adapter's `Discover` plan into the Steampipe `Source`, and skips any table whose required capability is not in the enabled set. Tables with no associated capability (or the always-on `core` capability) continue to be collected for every connection. The fix is minimal and additive: it adds one YAML field, one `TableSpec` field, one config key, one `Source` field, and one guard clause inside the existing per-table collection loop. No query is issued for a skipped table, so no `AccessDenied` error is produced for an ungranted capability, while every enabled-capability table behaves exactly as it does today.

The capability names and their meaning already exist in `internal/connector/capability/capability.go` (`core`, `eks`, `workload`, `dspm`). This design reuses those exact string values as the table-to-capability association so the catalog and the capability catalog stay in lockstep.

## Glossary

- **Bug_Condition (C)**: The condition that triggers the bug — the collector issues a query for a table whose required capability is NOT in the connection's enabled capability set.
- **Property (P)**: The desired behavior for a bug-condition input — the collector skips the table so no query runs and no `AccessDeniedException` is surfaced.
- **Preservation**: The existing behavior for tables whose capability IS enabled (including always-on `core` tables and tables with no associated capability) — same query issued, same `RawEvent`s emitted, same per-table error isolation.
- **`Collect`**: The method in `internal/source/steampipe/steampipe.go` that connects to the Steampipe service and loops over `s.catalog.Tables`, running `collectTable` for each.
- **`TableSpec`**: The struct in `internal/source/steampipe/catalog.go` describing how to collect one Steampipe table (table name, id/scope/region columns, optional `where`). The fix adds a `Capability` field here.
- **`Catalog`**: The provider's set of `TableSpec`s loaded from `aws.yaml` in `internal/source/steampipe/catalog.go`.
- **`Discover`**: The AWS adapter method in `internal/connector/provider/aws/adapter.go` that builds the `provider.DiscoveryPlan.SteampipeConfig` map handed to `steampipe.Factory`. It has `t.Capabilities` in scope.
- **`Target.Capabilities`**: The connection's enabled capability set (`[]string`) carried on `provider.Target` (`internal/connector/provider/provider.go`), e.g. `["core"]` or `["core","eks"]`.
- **Enabled capability set**: The set of capability strings granted to the connection. `core` is always enabled (`capability.DefaultSelection()` returns `["core"]`).

## Bug Details

### Bug Condition

The bug manifests when the collector processes a table whose associated capability is not in the connection's enabled capability set, yet still issues a SQL query for it. In the current code `Collect` iterates `s.catalog.Tables` and calls `collectTable` for every table without consulting the connection's capabilities (the `Source` has no capability field, and the capabilities are not passed from `Discover` into the Steampipe config map). The ungranted query reaches AWS through the assumed role, which lacks the permission, and Steampipe returns an `AccessDeniedException`.

**Formal Specification:**
```
FUNCTION isBugCondition(input)
  INPUT: input of type (TableSpec table, Set<string> enabledCapabilities)
  OUTPUT: boolean

  // A table is gated when it declares a capability other than the
  // always-on "core". A table with no capability, or capability "core",
  // is never gated.
  RETURN table.Capability != ""
         AND table.Capability != "core"
         AND table.Capability NOT IN enabledCapabilities
         AND queryIssuedFor(table)   // the collector still runs the query
END FUNCTION
```

### Examples

- **EKS not granted**: Connection with `enabledCapabilities = ["core"]`; table `aws_eks_cluster` (capability `eks`). Expected: table skipped, no query, no error. Actual: query runs, Steampipe returns `AccessDeniedException` on `eks:ListClusters` and the error is surfaced on the error channel.
- **Workload not granted**: Connection with `["core"]`; tables `aws_ecs_task_definition`, `aws_ecr_image`, `aws_lambda_function` (capability `workload`). Expected: all three skipped. Actual: queries run and each returns `AccessDeniedException` for its ungranted action.
- **DSPM not granted**: Connection with `["core"]`; DSPM-gated tables. Expected: skipped. Actual: queries run and return `AccessDeniedException`.
- **EKS granted (not a bug)**: Connection with `["core","eks"]`; table `aws_eks_cluster`. Expected and actual: query runs, rows emitted as `RawEvent`s. `isBugCondition` is false.
- **Core table (edge case, not a bug)**: `aws_account` (capability `core`) with any enabled set. `core` is always enabled, so `isBugCondition` is false and the table is always queried.

## Expected Behavior

### Preservation Requirements

**Unchanged Behaviors:**
- Tables associated with the `core` capability (`aws_account`, `aws_iam_role`, `aws_iam_user`, `aws_iam_policy`, `aws_s3_bucket`, `aws_ec2_instance`, `aws_vpc_security_group`) are queried for every connection, since `core` is always enabled (Requirement 3.2).
- Tables whose associated capability IS in the enabled set are queried and emit their rows as `RawEvent`s unchanged, with the same `Scope`, `Region`, `ResourceType`, and `ResourceID` columns as today (Requirements 3.1, 3.3, 3.5).
- Per-table error isolation: a non-capability error (timeout, decode failure, transient connection error) on an enabled table is still recorded on the error channel and the loop proceeds to the next table (Requirement 3.4).
- Catalog validation is unchanged (provider present, non-empty tables, unique table names, `id_column` present) and still runs on load (Requirement 3.5).

**Scope:**
All inputs where `isBugCondition` is false must be completely unaffected by this fix. This includes:
- Core tables (capability `core`) under any enabled set.
- Any table whose associated capability is present in the enabled set.
- Non-capability errors on enabled tables (still isolated per table).
- Catalog loading, validation, and the shape of every emitted `RawEvent`.

**Note:** The expected correct behavior for the bug condition itself (skip the table, no query, no error) is defined in the Correctness Properties section (Property 1).

## Hypothesized Root Cause

Based on the bug description and the current code, the root cause is a missing capability gate, decomposed as follows:

1. **No capability association on tables**: `TableSpec` in `internal/source/steampipe/catalog.go` has no field recording which capability a table requires, and `aws.yaml` declares none. The collector therefore cannot tell a gated table from a core table.

2. **Enabled capability set never reaches the collector**: The connection's `Target.Capabilities` is available in the AWS adapter's `Discover` (`internal/connector/provider/aws/adapter.go`) but is not placed into the `SteampipeConfig` map, so `steampipe.Factory` and the resulting `Source` never learn which capabilities are enabled. `Source` has no field to hold them.

3. **Unconditional query loop**: `Collect` in `internal/source/steampipe/steampipe.go` iterates `s.catalog.Tables` and calls `collectTable` for every table with no gating check, so an ungranted gated table is queried and returns `AccessDeniedException`.

The combination of (1), (2), and (3) is the complete cause: there is no data to gate on and no gate. The fix supplies the association (1), threads the enabled set (2), and adds the guard (3).

## Correctness Properties

Property 1: Bug Condition - Ungranted capability tables are skipped

_For any_ input where the bug condition holds (`isBugCondition` returns true — the table's associated capability is set, is not `core`, and is not in the connection's enabled capability set), the fixed collector SHALL skip the table so that no SQL query is issued for it and no `AccessDeniedException` (nor any other error) for that table is placed on the error channel.

**Validates: Requirements 2.1, 2.2, 2.3, 2.4**

Property 2: Preservation - Enabled and core tables behave identically

_For any_ input where the bug condition does NOT hold (`isBugCondition` returns false — the table has no associated capability, or its capability is `core`, or its capability is in the connection's enabled set), the fixed collector SHALL produce the same result as the original collector: the same query is issued, the same `RawEvent`s are emitted (same scope, region, resource type, and id), and per-table error isolation is preserved.

**Validates: Requirements 3.1, 3.2, 3.3, 3.4, 3.5**

## Fix Implementation

### Changes Required

Assuming the root cause analysis is correct, the fix touches five files with small, additive changes.

**File**: `internal/source/steampipe/catalog.go`

**Struct**: `TableSpec`

1. **Add a `Capability` field**: Add `Capability string `yaml:"capability"`` to `TableSpec`. Empty means "no gating" (always collected); a non-empty value names the capability that grants the table (`core`, `eks`, `workload`, `dspm`). Document that `core` is treated as always-on.
   - Do NOT add this field to `Catalog.Validate()` as a required field — an empty capability is valid and means ungated — so Requirement 3.5 (unchanged validation) holds.

**File**: `internal/source/steampipe/catalog/aws.yaml`

2. **Annotate each table with its capability**: Add a `capability:` key per table, derived from `internal/connector/capability/capability.go`:
   - `core`: `aws_account`, `aws_iam_role`, `aws_iam_user`, `aws_iam_policy`, `aws_s3_bucket`, `aws_ec2_instance`, `aws_vpc_security_group`.
   - `eks`: `aws_eks_cluster`.
   - `workload`: `aws_ecs_task_definition`, `aws_ecr_image`, `aws_lambda_function`.
   - `dspm`: data-store tables gated by DSPM (per the DSPM permission bundle: `rds`/`dynamodb` and DSPM-specific `s3` tagging reads). The AI/data tables currently in the catalog (`aws_bedrock_custom_model`, `aws_sagemaker_model`) are not covered by any of the four capability permission bundles; annotate them with the capability whose IAM bundle actually grants them, or leave them ungated (empty) if no bundle grants them so current behavior is preserved. The exact assignment is finalized during task execution by matching each table's required IAM action to a capability's `Permissions` list in `capability.go`.

**File**: `internal/source/steampipe/steampipe.go`

**Struct**: `Source`

3. **Add an enabled-capabilities field**: Add `enabledCaps map[string]bool` (a set for O(1) lookup) to `Source`. A `nil`/empty set means "no gating configured" and MUST preserve current behavior (collect everything) so callers that do not pass capabilities are unaffected.

**Function**: `Factory`

4. **Read capabilities from config**: Read a new config key (e.g. `capabilities` as `[]string` or `[]any`) via a small helper and populate `src.enabledCaps`. Always treat `core` as enabled (add it to the set) so core tables are never gated out even if a caller omits it.

**Function**: `Collect` (the per-table loop)

5. **Add the gate before querying**: Inside `for _, spec := range s.catalog.Tables`, before creating the per-table timeout context and calling `collectTable`, add:
   ```
   if !s.capabilityEnabled(spec.Capability) {
       continue   // skip: no query, no error on the error channel
   }
   ```
   where `capabilityEnabled` returns true when: the spec capability is empty, or equals `core`, or `s.enabledCaps` is empty (no gating configured), or `s.enabledCaps[spec.Capability]` is true. This is a pure read, placed before `collectTable`, so a skipped table issues no query and surfaces no error, while every non-skipped table flows through the existing code path unchanged.

**File**: `internal/connector/provider/aws/adapter.go`

**Function**: `Discover`

6. **Thread the enabled set into the plan**: Add `t.Capabilities` to the `cfg` map, e.g. `"capabilities": t.Capabilities`, so the orchestrator's call to `steampipe.Factory(plan.SteampipeConfig)` receives the connection's enabled capability set. No orchestrator change is required because it passes `SteampipeConfig` straight to the factory.

**File**: `internal/connector/capability/capability.go`

7. **No functional change**: This file is the source of truth for capability names and permission bundles. The fix references its capability string values (`core`, `eks`, `workload`, `dspm`) when annotating `aws.yaml` and may optionally expose/reuse those constants, but requires no behavioral change here.

## Testing Strategy

### Validation Approach

The strategy is two-phase: first surface counterexamples that demonstrate the bug on the unfixed collector (a gated table is queried when its capability is not enabled), then verify the fix skips ungranted tables and leaves enabled/core tables untouched. Because the real bug surfaces as a live AWS `AccessDeniedException`, tests target the deterministic, observable seam instead: which tables the collector issues a query for, given a `(catalog, enabled capability set)` pair. A fake Steampipe query layer (or a recording of attempted table names) lets tests assert "query issued" vs "skipped" without a live AWS account.

### Exploratory Bug Condition Checking

**Goal**: Surface counterexamples that demonstrate the bug BEFORE implementing the fix, and confirm the root cause (unconditional query loop with no capability data). If the collector does NOT query the gated table on unfixed code, the root cause is refuted and must be re-hypothesized.

**Test Plan**: Drive `Collect` with a catalog containing a gated table and an enabled set that excludes that capability, using a fake/recording query layer that captures every table name the collector attempts to query. Assert the gated table IS attempted on the unfixed code (demonstrating the bug), then after the fix assert it is NOT attempted.

**Test Cases**:
1. **EKS not granted**: enabled `["core"]`, catalog includes `aws_eks_cluster` (capability `eks`) — gated table is queried on unfixed code (will fail after fix is expected to skip it).
2. **Workload not granted**: enabled `["core"]`, catalog includes `aws_ecs_task_definition`/`aws_ecr_image`/`aws_lambda_function` (capability `workload`) — queried on unfixed code.
3. **DSPM not granted**: enabled `["core"]`, catalog includes DSPM-gated tables — queried on unfixed code.
4. **Out-of-set edge case**: enabled set names a capability the catalog does not use, and a gated table whose capability is absent — queried on unfixed code.

**Expected Counterexamples**:
- The collector attempts a query against a gated table whose capability is not in the enabled set.
- Root cause confirmed: `Collect` has no gate and `Source` carries no enabled-capability data.

### Fix Checking

**Goal**: Verify that for all inputs where the bug condition holds, the fixed collector issues no query and surfaces no error for the gated table.

**Pseudocode:**
```
FOR ALL input WHERE isBugCondition(input) DO
  result := Collect_fixed(input.catalog, input.enabledCapabilities)
  ASSERT no_query_issued(result, input.table)
      AND no_error_on_channel(result, input.table)
END FOR
```

### Preservation Checking

**Goal**: Verify that for all inputs where the bug condition does NOT hold, the fixed collector produces the same result as the original: same query issued, same `RawEvent`s, same per-table error isolation.

**Pseudocode:**
```
FOR ALL input WHERE NOT isBugCondition(input) DO
  ASSERT Collect_original(input) = Collect_fixed(input)
END FOR
```

**Testing Approach**: Property-based testing is recommended for preservation because it generates many `(catalog, enabled set)` combinations across the input domain, exercising core tables, ungated tables, and enabled gated tables, and catches edge cases (empty enabled set, `core` always-on, unknown capability strings) that fixed unit tests might miss.

**Test Plan**: Record which tables the unfixed collector queries and the `RawEvent`s it emits for non-bug inputs, then write property-based tests asserting the fixed collector queries the same set of tables and emits identical events.

**Test Cases**:
1. **Core always collected**: enabled `["core"]` (and any superset) — all `core` tables are queried, matching unfixed behavior.
2. **Enabled gated table collected**: enabled `["core","eks"]` — `aws_eks_cluster` is queried and emits the same `RawEvent`s as unfixed.
3. **Ungated table collected**: a table with empty `capability` is always queried regardless of enabled set.
4. **Error isolation preserved**: a non-capability error (e.g. decode failure) on an enabled table is still recorded per table and the loop proceeds, matching unfixed behavior.
5. **No-gating-configured fallback**: when no `capabilities` key is supplied, every table is queried (back-compat with callers that do not pass capabilities).

### Unit Tests

- `capabilityEnabled` returns true for empty capability, for `core`, and for a capability present in the enabled set; false for a non-`core` capability absent from a non-empty enabled set.
- `Collect` skips a gated table whose capability is not enabled (no query attempted, no error emitted).
- `Collect` queries a gated table whose capability is enabled and emits rows unchanged.
- `Factory` populates `enabledCaps` from the `capabilities` config key and always includes `core`.
- Catalog validation still passes for specs with and without a `capability` field (empty capability is valid).

### Property-Based Tests

- Generate random `(enabled capability set, catalog of tables with random capability annotations)` pairs; assert every queried table satisfies `NOT isBugCondition` and every skipped table satisfies `isBugCondition`.
- Generate random enabled sets always containing `core`; assert all `core` and ungated tables are always queried (preservation).
- Generate random non-capability errors on enabled tables; assert per-table isolation holds for every generated case.

### Integration Tests

- End-to-end through `aws/adapter.Discover` -> `steampipe.Factory` -> `Collect`: a `Target` with `Capabilities: ["core"]` produces a plan whose `SteampipeConfig` carries the capability set, and the collector skips `aws_eks_cluster`.
- A `Target` with `Capabilities: ["core","eks"]` collects `aws_eks_cluster` and emits its `RawEvent`s.
- Switching the enabled set between runs changes exactly which gated tables are queried, with core tables queried in every run.
