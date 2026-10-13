# Bugfix Requirements Document

## Introduction

The Steampipe collector (`internal/source/steampipe/steampipe.go`) queries every table listed in its catalog (`internal/source/steampipe/catalog/aws.yaml`) unconditionally, regardless of which capabilities a connection was granted. Capability-gated tables such as `aws_eks_cluster` require IAM permissions (e.g. `eks:ListClusters`) that are only present when the matching capability (`eks`) is enabled for the connection.

When a connection's granted capability set does not include a given capability, the assumed `BelluConnectorRole` lacks the corresponding permissions, so Steampipe returns an `AccessDeniedException` at runtime. For example, a connection without the `eks` capability still triggers a query against `aws_eks_cluster`, producing:

```
table aws_eks_cluster: ERROR: rpc error: code = Unknown desc = aws_conn_37: operation error EKS: ListClusters, StatusCode: 403, api error AccessDeniedException: User: arn:aws:sts::975050280723:assumed-role/BelluConnectorRole/bellu-connector-37 is not authorized to perform: eks:ListClusters on resource: arn:aws:eks:ap-south-1:975050280723:cluster/* because no identity-based policy allows the eks:ListClusters action
```

The capability catalog already exists at `internal/connector/capability/capability.go`, mapping capabilities to IAM permissions. As of this revision the catalog defines **five** capabilities: `core` (always enabled), `eks`, `workload`, `dspm`, and `ai`. The fix associates each Steampipe table with the capability that grants it and skips any table whose capability is not enabled for the connection, so no `AccessDenied` error is produced for ungranted capabilities.

## Bug Analysis

### Current Behavior (Defect)

The collector queries all catalog tables regardless of the connection's enabled capability set.

1.1 WHEN a connection does not have the `eks` capability enabled THEN the collector queries the `aws_eks_cluster` table and Steampipe returns an `AccessDeniedException` (403) for `eks:ListClusters`
1.2 WHEN a connection does not have the `workload` capability enabled THEN the collector queries workload tables (`aws_ecs_task_definition`, `aws_ecr_image`, `aws_lambda_function`) and Steampipe returns an `AccessDeniedException` for the corresponding ungranted actions
1.3 WHEN a connection does not have the `dspm` capability enabled THEN the collector queries DSPM-gated tables and Steampipe returns an `AccessDeniedException` for the corresponding ungranted actions
1.4 WHEN a connection does not have the `ai` capability enabled THEN the collector queries AI-service tables (`aws_bedrock_custom_model`, `aws_sagemaker_model`) and Steampipe returns an `AccessDeniedException` for the corresponding ungranted actions (e.g. `bedrock:ListCustomModels`)
1.5 WHEN a capability-gated table errors with `AccessDeniedException` THEN the error is surfaced on the collector's error channel even though the capability was never granted for the connection

### Expected Behavior (Correct)

The collector queries only the tables whose required capability is in the connection's enabled/granted capability set.

2.1 WHEN a connection does not have the `eks` capability enabled THEN the collector SHALL skip the `aws_eks_cluster` table so no query runs and no `AccessDeniedException` occurs
2.2 WHEN a connection does not have the `workload` capability enabled THEN the collector SHALL skip the workload-gated tables (`aws_ecs_task_definition`, `aws_ecr_image`, `aws_lambda_function`) so no query runs and no `AccessDeniedException` occurs
2.3 WHEN a connection does not have the `dspm` capability enabled THEN the collector SHALL skip the DSPM-gated tables so no query runs and no `AccessDeniedException` occurs
2.4 WHEN a connection does not have the `ai` capability enabled THEN the collector SHALL skip the AI-service-gated tables (`aws_bedrock_custom_model`, `aws_sagemaker_model`) so no query runs and no `AccessDeniedException` occurs
2.5 WHEN a table has an associated capability that is NOT in the connection's enabled capability set THEN the collector SHALL NOT emit a query for that table and SHALL NOT surface an error for it

### Unchanged Behavior (Regression Prevention)

Granted capabilities and core (always-on) tables must continue to be collected exactly as before.

3.1 WHEN a connection has the `eks` capability enabled THEN the collector SHALL CONTINUE TO query the `aws_eks_cluster` table and emit its rows as RawEvents
3.2 WHEN a table is associated with the `core` capability (`aws_account`, `aws_iam_role`, `aws_iam_user`, `aws_iam_policy`, `aws_s3_bucket`, `aws_ec2_instance`, `aws_vpc_security_group`) THEN the collector SHALL CONTINUE TO query it for every connection, since `core` is always enabled
3.3 WHEN a connection has a capability enabled (`eks`, `workload`, `dspm`, or `ai`) THEN the collector SHALL CONTINUE TO query all tables associated with that capability and emit their rows unchanged
3.4 WHEN a table encounters a non-capability error (e.g. timeout, decode failure, transient connection error) for an enabled capability THEN the collector SHALL CONTINUE TO isolate the error per table and proceed with the remaining tables
3.5 WHEN the catalog YAML is loaded THEN the collector SHALL CONTINUE TO validate it (provider present, non-empty tables, unique table names, id_column present) and emit RawEvents with the same scope, region, resource type, and id columns as before
3.6 WHEN no `capabilities` configuration is supplied to the Steampipe `Source` (e.g. an existing caller that predates this fix) THEN the collector SHALL CONTINUE TO query every table in the catalog, preserving current behavior for callers that do not pass an enabled capability set

## Bug Condition

**Key definitions:**
- **F**: the original collector — queries every catalog table unconditionally.
- **F'**: the fixed collector — queries only tables whose required capability is in the connection's enabled set.
- **X**: a (table spec, enabled capability set) pair processed during a collection run.

### Bug Condition Function

```pascal
FUNCTION isBugCondition(X)
  INPUT: X = (table, enabledCapabilities) where table has an associated capability
  OUTPUT: boolean

  // The bug is triggered when a table's required capability is NOT granted,
  // yet the collector still issues a query for it.
  RETURN table.capability NOT IN enabledCapabilities
END FUNCTION
```

### Property Specification (Fix Checking)

```pascal
// Property: a table whose capability is not enabled must never be queried.
FOR ALL X WHERE isBugCondition(X) DO
  result <- F'(X)
  ASSERT no_query_issued(result, X.table)
      AND no_access_denied_error(result, X.table)
END FOR
```

### Preservation Goal (Preservation Checking)

```pascal
// Property: for every table whose capability IS enabled (including always-on
// core tables), the fixed collector behaves identically to the original.
FOR ALL X WHERE NOT isBugCondition(X) DO
  ASSERT F(X) = F'(X)   // same query issued, same RawEvents emitted,
                        // same per-table error isolation
END FOR
```
