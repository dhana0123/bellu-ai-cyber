# Requirements Document

## Introduction

This feature evolves Bellu from a stateless cloud-inventory graph into a bi-temporal,
event-sourced temporal graph, and replaces Steampipe with CloudQuery at the collection
boundary. It reuses the existing `Source → RawEvent → Normalizer → store → graph.Writer`
pipeline rather than replacing it.

Two append-only logs in the customer PostgreSQL become the source of truth:
`observed_events` (asserted facts) and `derived_events` (inference and classification).
Memgraph becomes a rebuildable projection of those logs. Bi-temporal columns
(`event_time`, `recorded_at`) provide time travel, history, and an audit view.

On top of the facts log, a separate derived lane adds cross-source inferred-dependency
edges and rule-based datastore classification, surfaced on the projection without being
mixed into the facts. A tenant-scoped MCP server exposes read tools over both the
projection and the logs.

The requirements below are ordered so that the design's five correctness properties can
cite them directly: replay determinism (Requirements 1-2, 7), facts/derived separation
(Requirements 4, 8, 9), reference-counted deletion (Requirement 5), as-of/asKnownAt
correctness (Requirement 6), and Steampipe/CloudQuery parity (Requirements 10-12).

## Glossary

- **Observed_Lane**: The append-only `observed_events` log; stores only asserted facts about entities and edges.
- **Derived_Lane**: The append-only `derived_events` log; stores inference (cross-source edges) and classification output, never mixed with the observed lane.
- **Event_Time**: The time a fact became true in the world; the default axis for reality and as-of queries.
- **Recorded_At**: The time Bellu learned a fact (ingest time); the axis for the audit view.
- **AsKnownAt**: An audit query that reconstructs what Bellu knew at a given time, by adding a `recorded_at` cutoff.
- **As_Of**: A query that reconstructs state as of a given `event_time`, folding all events up to that cutoff.
- **Reference_Counted_Deletion**: Marking an entity deleted only when no source still asserts it, tracked by the `entity_assertions` set, replacing the single `stale` flag.
- **Projection**: The Memgraph current-state graph, rebuildable entirely by replaying the two logs; it holds no state that cannot be reconstructed.
- **Inferred_Edge**: A cross-source dependency edge produced by the inference engine into the derived lane, projected with property `inferred=true` plus confidence and evidence.
- **Data_Class**: A classification label (e.g. pii, secrets, logs, public, internal) assigned to a DataStore from metadata only, surfaced as namespaced node properties.
- **Change_Engine**: The component that diffs a normalized run against the current projection and emits exactly-once-classified observed events.
- **Replay_Engine**: The component that rebuilds the Memgraph projection from the two logs (observed base, derived overlay).
- **Bellu_System**: The overall Bellu discovery, temporal-store, projection, and query system described by this document.

## Requirements

### Requirement 1: CloudQuery Collection Job

**User Story:** As a platform operator, I want cloud inventory collected by a per-discovery CloudQuery job, so that discovery runs on CloudQuery instead of Steampipe without changing the downstream pipeline.

#### Acceptance Criteria

1. WHEN a discovery run starts for a connection, THE Bellu_System SHALL generate a per-connection CloudQuery specification selecting the configured provider and tables and a PostgreSQL destination targeting the `cq_staging` schema.
2. WHEN a CloudQuery job runs, THE Bellu_System SHALL inject the run's STS session credentials as per-run environment variables.
3. THE Bellu_System SHALL exclude STS session credentials from the staging database, the on-disk specification file, logs, and API responses.
4. WHILE a CloudQuery job is executing, THE Bellu_System SHALL report per-table collection progress through the discovery progress reporter.
5. WHERE a capability is not enabled for a connection, THE Bellu_System SHALL exclude tables gated by that capability from the generated specification.
6. IF a CloudQuery job exits with a non-zero status, THEN THE Bellu_System SHALL return a job failure and emit no RawEvents from the aborted synchronization.
7. IF the discovery run context is cancelled, THEN THE Bellu_System SHALL terminate the CloudQuery job and return a cancellation error.

### Requirement 2: CloudQuery to RawEvent Adapter

**User Story:** As a pipeline maintainer, I want a `cloudquery` Source that maps staging rows into RawEvents through a table-mapping file, so that every existing normalizer continues to work unchanged.

#### Acceptance Criteria

1. THE Cloudquery_Source SHALL implement the existing `source.Source` interface and return the source name `cloudquery`.
2. WHEN the Cloudquery_Source reads a populated `cq_staging` row, THE Cloudquery_Source SHALL emit exactly one RawEvent for that row using the table mapping's id, scope, and region columns and then finalize the RawEvent.
3. THE Cloudquery_Source SHALL set the `source` field of every emitted RawEvent to `cloudquery` and set the `provider` from the table mapping.
4. THE Table_Mapping SHALL use the same field shape as the existing Steampipe catalog, adding a single field that maps a Bellu table to its CloudQuery staging table when the names differ.
5. WHERE a staging column name is a CloudQuery-internal column, THE Cloudquery_Source SHALL ignore that column when building a RawEvent.
6. IF a staging column present in the mapping is absent or malformed for one table, THEN THE Cloudquery_Source SHALL log and report that table as skipped and continue reading the remaining tables.
7. THE Bellu_System SHALL count and surface skipped tables so a degraded run is visibly degraded rather than silently partial.

### Requirement 3: Observed Event Log and Change Classification

**User Story:** As a security analyst, I want every change a run asserts recorded in an append-only facts log, so that the graph has a complete, replayable history of what was observed.

#### Acceptance Criteria

1. THE Observed_Lane SHALL store each change as an append-only row carrying `org_id`, `run_id`, `source`, `element_kind`, `change_type`, element identity, attributes, diff, confidence, evidence identifiers, `event_time`, and `recorded_at`.
2. WHEN the Change_Engine diffs a run against the current projection, THE Change_Engine SHALL classify each entity under exactly one of `entity.created`, `entity.attribute_updated`, `entity.state_changed`, `entity.deleted`, or `entity.unchanged`.
3. WHEN the Change_Engine diffs a run against the current projection, THE Change_Engine SHALL classify each edge under exactly one of `relation.added`, `relation.removed`, or `relation.attribute_changed`.
4. WHEN an entity's lifecycle or exposure attribute changes, THE Change_Engine SHALL classify the change as `entity.state_changed` rather than `entity.attribute_updated`.
5. WHEN the Change_Engine writes a change, THE Change_Engine SHALL link the change to the raw-event identifiers that justify it.
6. WHEN the Change_Engine writes a change, THE Change_Engine SHALL set `event_time` from the observed time of the run's raw events and set `recorded_at` to the ingest time.
7. WHEN the Change_Engine persists a run, THE Change_Engine SHALL write all observed events and all entity-assertion updates for that run in a single transaction.
8. IF any write in a run's change transaction fails, THEN THE Change_Engine SHALL roll back the entire transaction, persist no partial change set, and return the error without proceeding to projection.

### Requirement 4: Facts and Derived Lane Separation

**User Story:** As a system architect, I want asserted facts and derivations kept in separate lanes, so that inference and classification can never corrupt the facts log.

#### Acceptance Criteria

1. THE Observed_Lane SHALL store only asserted facts and SHALL reject rows whose change type belongs to the derived lane.
2. THE Derived_Lane SHALL store only inference and classification output and SHALL reject rows whose change type belongs to the observed lane.
3. WHEN an inference rule computes a candidate edge, THE Bellu_System SHALL derive it only from observed or normalized data and never from derived output.
4. THE Bellu_System SHALL write inferred edges and classifications exclusively to the Derived_Lane.

### Requirement 5: Reference-Counted Deletion

**User Story:** As a security analyst, I want an entity deleted only when no source still reports it, so that one source's absence does not erase a resource another source still sees.

#### Acceptance Criteria

1. THE Bellu_System SHALL track, per tenant and per source, which entities each source currently asserts in an `entity_assertions` set.
2. WHEN a run completes for a source, THE Change_Engine SHALL remove the assertion rows for entities that source previously asserted but did not assert in this run.
3. WHEN removing an entity's assertion leaves zero assertion rows for that entity across all sources, THE Change_Engine SHALL emit an `entity.deleted` observed event and mark the entity absent in the projection.
4. WHILE at least one source still asserts an entity, THE Bellu_System SHALL NOT mark that entity deleted.
5. WHEN the Change_Engine decrements an assertion, checks the remaining count, and emits a deletion, THE Change_Engine SHALL perform those steps within the same transaction.
6. IF a run fails or aborts for a source, THEN THE Bellu_System SHALL retain that source's existing assertions and emit no reference-counted deletions for that run.

### Requirement 6: Temporal Queries

**User Story:** As a security analyst, I want to query the graph as of any point in time and audit what Bellu knew, so that I can answer historical and audit questions.

#### Acceptance Criteria

1. WHEN a reality-view state query is requested, THE Bellu_System SHALL fold all events with `event_time` at or before the current time.
2. WHEN an as-of state query is requested for `event_time` T, THE Bellu_System SHALL fold all events with `event_time` at or before T.
3. WHEN an asKnownAt query is requested for `event_time` T and `recorded_at` K, THE Bellu_System SHALL fold events with `event_time` at or before T and `recorded_at` at or before K.
4. WHEN folding an element's events, THE Bellu_System SHALL order them by `event_time`, then `recorded_at`, then sequence, and treat a terminal `entity.deleted` as absent.
5. WHILE `event_time` T is held fixed and `recorded_at` K is increased, THE Bellu_System SHALL only add information to the asKnownAt result and never remove a previously known fact.
6. WHEN an entity history is requested, THE Bellu_System SHALL return the ordered change stream for that entity with both lanes merged by time.
7. WHEN recent changes are requested since a cutoff, THE Bellu_System SHALL return tenant-scoped changes recorded after that cutoff up to the requested limit.
8. WHEN a diff between two times is requested for an element, THE Bellu_System SHALL return the attributes added, removed, and changed between the two folded states.

### Requirement 7: Memgraph Projection and Rebuild

**User Story:** As a platform operator, I want Memgraph rebuildable entirely from the two logs, so that the projection is always recoverable and never the source of truth.

#### Acceptance Criteria

1. WHEN a run's changes are applied incrementally, THE Replay_Engine SHALL write the run's observed changes as the base graph and then the run's derived changes as the overlay.
2. WHEN a full rebuild is requested for a point in time, THE Replay_Engine SHALL fold the observed lane into base nodes and edges and then fold the derived lane into inferred edges and classification properties.
3. THE Replay_Engine SHALL use idempotent merge-and-set writes so that re-running a rebuild or re-applying a run produces the same projection.
4. THE Bellu_System SHALL ensure the Memgraph projection holds no state that cannot be reconstructed by replaying the two logs.
5. IF the Memgraph projection is in a suspect state after an incremental apply, THEN THE Replay_Engine SHALL support a full rebuild from the logs as the authoritative recovery path.
6. IF Memgraph is unavailable during projection, THEN THE Bellu_System SHALL preserve the committed observed and derived events and rebuild the projection when Memgraph returns.

### Requirement 8: Derived Dependency Inference

**User Story:** As a security analyst, I want hidden cross-source dependencies inferred and shown as edges, so that I can see relationships that no single collector reports directly.

#### Acceptance Criteria

1. WHEN observed facts match an inference rule, THE Inference_Engine SHALL emit a candidate inferred edge into the Derived_Lane with a relation drawn from the existing model relations, a confidence value, and evidence identifiers.
2. THE Inference_Engine SHALL support rules for repository-to-workload `BUILT_FROM` and `RUNS_IMAGE`, workload-to-identity `AUTHENTICATES_AS`, service-to-service `CALLS` and `DEPENDS_ON`, and monitor-to-resource `USES` edges.
3. WHEN an inferred edge is projected into Memgraph, THE Replay_Engine SHALL mark that edge with property `inferred=true` and carry its confidence and evidence.
4. THE Inference_Engine SHALL add new rules without branching the engine on provider type.

### Requirement 9: Data Classification

**User Story:** As a security analyst, I want datastores classified by likely content and access, so that I can prioritize risk without the system reading stored data.

#### Acceptance Criteria

1. WHEN a DataStore is classified, THE Classification_Engine SHALL derive its class, reader set, and writer set from metadata only, including naming conventions, infrastructure-as-code definitions, and IAM policy, without reading the datastore's contents.
2. WHEN the Classification_Engine produces a classification, THE Classification_Engine SHALL write it to the Derived_Lane with a confidence value and the producer identifier.
3. WHEN a classification is projected into Memgraph, THE Replay_Engine SHALL set the namespaced node properties `data_class`, `data_class_confidence`, and `classified_by`.
4. WHERE a model-based classifier is enabled, THE Classification_Engine SHALL produce the same classification shape as the rule-based classifier so that no projection or schema change is required to adopt it.

### Requirement 10: MCP Query Server

**User Story:** As an integration developer, I want tenant-scoped MCP read tools over the graph and logs, so that agents can query inventory, relationships, and history safely.

#### Acceptance Criteria

1. THE MCP_Server SHALL expose the tools `find_entities`, `get_entity`, `get_neighbors`, `entity_history`, `recent_changes`, and `describe_schema`.
2. WHEN an MCP tool is invoked, THE MCP_Server SHALL resolve the tenant from the authenticated context and ignore any client-supplied organization.
3. THE MCP_Server SHALL serve requests only behind the existing JWT and RBAC controls.
4. WHEN `get_neighbors` is invoked with a requested depth above 5, THE MCP_Server SHALL clamp the depth to 5.
5. WHEN `get_neighbors` returns adjacency, THE MCP_Server SHALL tag each edge with its lane as asserted or inferred.
6. WHEN an MCP tool returns an entity, THE MCP_Server SHALL include the entity name alongside its identifier.
7. IF a requested entity does not exist for the authenticated tenant, THEN THE MCP_Server SHALL return an "entity not found" message rather than leaking cross-tenant existence.
8. IF a backend store is unavailable, THEN THE MCP_Server SHALL return a generic "temporarily unavailable" message and log the internal error without returning it.

### Requirement 11: Retention and Heartbeat Coalescing

**User Story:** As a platform operator, I want unchanged observations coalesced and baselines preserved, so that the logs stay bounded without breaking as-of queries.

#### Acceptance Criteria

1. WHILE an element remains unchanged across consecutive runs, THE Change_Engine SHALL write no observed-event row and instead update the element's last-seen assertion timestamp.
2. WHEN the retention horizon is reached for an element, THE Bellu_System SHALL write a baseline event with a full attribute snapshot and `event_time` set to the horizon before pruning older rows.
3. WHEN pruning old rows, THE Bellu_System SHALL delete observed and derived rows older than the retention horizon except baseline events.
4. WHEN an as-of query targets a time after the pruned window, THE Bellu_System SHALL resolve correct state from the retained baseline events.

### Requirement 12: Steampipe to CloudQuery Parity

**User Story:** As a pipeline maintainer, I want proven parity between CloudQuery and Steampipe output, so that the collector swap does not change downstream results.

#### Acceptance Criteria

1. WHEN the same inventory fixture is fed through both the Cloudquery_Source and the Steampipe source, THE Bellu_System SHALL produce byte-identical RawEvent sets after finalization.
2. WHEN identical RawEvent sets are normalized, THE Bellu_System SHALL produce identical normalized entities and normalized edges.
3. THE Bellu_System SHALL gate the Steampipe-to-CloudQuery cutover on the parity tests passing.

### Requirement 13: Steampipe Removal After Parity

**User Story:** As a pipeline maintainer, I want Steampipe fully removed once parity is proven, so that the system has a single collector and no dead configuration.

#### Acceptance Criteria

1. WHEN parity is proven and the orchestrator and provider adapters are flipped to CloudQuery, THE Bellu_System SHALL remove the Steampipe source package and its catalogs.
2. WHEN Steampipe is removed, THE Bellu_System SHALL remove the Steampipe compose service and the `.spc` credential sink.
3. WHEN Steampipe is removed, THE Bellu_System SHALL remove the `BELLU_STEAMPIPE_*` environment variables from configuration.

### Requirement 14: Additional Collection Providers

**User Story:** As a platform operator, I want additional providers added through the same mapping format, so that coverage grows without new pipeline mechanisms.

#### Acceptance Criteria

1. WHERE an Azure, GCP, or Kubernetes provider is configured, THE Bellu_System SHALL collect its inventory through a CloudQuery job and the same table-mapping format used for AWS.
2. WHERE a Datadog or Sentry config source is configured, THE Bellu_System SHALL read monitor configuration through the same mapping format and contribute monitor-to-resource edges rather than ingesting log streams.
3. WHERE an identity provider source is configured, THE Bellu_System SHALL collect its inventory through the same mapping format.

### Requirement 15: OTLP Change Export

**User Story:** As a platform operator, I want observed changes optionally exported as OTLP entity events, so that external observability tooling can consume the change feed.

#### Acceptance Criteria

1. WHERE the OTLP exporter is enabled, THE Bellu_System SHALL export observed change events as OTLP entity events.
2. WHILE the OTLP exporter is disabled, THE Bellu_System SHALL complete ingest and projection without exporting OTLP events.

### Requirement 16: Tenant Isolation and Credential Handling

**User Story:** As a security engineer, I want tenant isolation and strict credential handling enforced across the feature, so that data and secrets never cross tenant or persistence boundaries.

#### Acceptance Criteria

1. THE Bellu_System SHALL scope every observed-event and derived-event row to a tenant by `org_id`.
2. WHEN any log or projection query runs, THE Bellu_System SHALL restrict results to the authenticated tenant's `org_id`.
3. THE Bellu_System SHALL keep the customer-plane stores and the global-intel plane separated so that `cq_staging` lives in the customer PostgreSQL only.
4. THE Bellu_System SHALL exclude collection credentials from durable storage, specification files, logs, and API responses on every code path including failure paths.

### Requirement 17: Change-Engine Write-Path Cost

**User Story:** As a platform operator, I want the change engine's write cost tied to actual change volume, so that large stable inventories do not inflate the logs or write load.

#### Acceptance Criteria

1. WHEN a run is diffed against the current projection, THE Change_Engine SHALL write observed-event rows only for added, changed, or removed elements.
2. THE Change_Engine SHALL bound its per-run write volume by the count of actual changes rather than by total inventory size.

### Requirement 18: Unverified CloudQuery Behavior Guard

**User Story:** As a pipeline maintainer, I want unverified CloudQuery destination behavior isolated and flagged, so that a surprise in the staging schema is contained and recorded.

#### Acceptance Criteria

1. THE Bellu_System SHALL isolate all `cq_staging` schema assumptions, including CloudQuery-internal columns and write mode, inside the `cloudquery` Source package.
2. WHEN the staging schema contains unknown columns, THE Cloudquery_Source SHALL tolerate them without failing the run.
3. THE Bellu_System SHALL record the CloudQuery PostgreSQL destination write modes, internal column set, plugin licensing, and plugin availability for later providers as explicit unverified assumptions requiring a spike before production reliance.
