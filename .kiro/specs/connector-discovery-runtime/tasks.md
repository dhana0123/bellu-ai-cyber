# Implementation Plan: Connector Discovery Runtime

## Overview

Two runtime improvements to connector discovery: a persistent Steampipe plugin
cache, and per-connection Steampipe configuration for isolated, parallel-safe
discovery. The implementation is complete and verified against the running
stack; tasks are recorded here for traceability.

## Tasks

- [x] 1. Persist the Steampipe plugin cache
  - Add a named volume `steampipe_plugins` mounted at
    `~/.steampipe/plugins` on the steampipe service in `deploy/docker-compose.yml`
  - Change the entrypoint install guard to check the plugin directory
    (not `steampipe plugin list`) and install from GHCR only when absent
  - _Requirements: R1.1, R1.2, R1.3, R1.4_

- [x] 2. Scope the Steampipe source to one connection
  - Add a `schema` field to `Source` in `internal/source/steampipe/steampipe.go`
  - Read `cfg["connection_schema"]` in `Factory`; qualify the table as
    `<schema>.<table>` in `collectTable` when set
  - _Requirements: R2.2_

- [x]* 2.1 Unit test schema qualification
  - Table-driven: schema set -> qualified table; empty -> unqualified
  - _Requirements: R2.2_

- [x] 3. Emit per-connection schema from the AWS adapter
  - In `Discover` (`internal/connector/provider/aws/adapter.go`), derive
    `aws_conn_<ConnectionID>` and set it as both `connection_schema` and
    `BELLU_STEAMPIPE_CONNECTION`
  - _Requirements: R2.1, R2.3_

- [x] 4. Write and clean up the per-session Steampipe config
  - In `steampipeConfigSink` (`backend/cmd/bellu-api/main.go`), write
    `<shared>/aws_conn_<id>.spc` with the session credentials, and return a
    cleanup that removes the file
  - Add `waitForSteampipeConnection` (bounded poll of `to_regclass(<schema>.aws_account)`)
    and call it after writing the config
  - _Requirements: R2.1, R2.4, R2.5, R2.6_

- [x] 5. Verify end to end
  - Build, vet, gofmt, and run the connector + steampipe unit tests
  - Confirm plugin persistence (fast restart, no re-download) and two
    independent per-connection schemas on the running stack
  - _Requirements: R1.4, R2.3_

## Notes

- Tasks marked `*` are optional test sub-tasks.
- The connect / verify / trust / one-click-launch flow is unchanged.
- Parallel org discovery across many accounts is now safe; each account's
  discovery uses its own connection config and schema.

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1", "2"] },
    { "id": 1, "tasks": ["2.1", "3"] },
    { "id": 2, "tasks": ["4"] },
    { "id": 3, "tasks": ["5"] }
  ]
}
```
