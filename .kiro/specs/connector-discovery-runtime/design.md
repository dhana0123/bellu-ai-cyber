# Design Document

## Overview

Two runtime improvements to connector discovery, both already proven against the
running stack:

1. **Persistent plugin cache** so Steampipe restarts are instant.
2. **Per-connection Steampipe configuration** so concurrent discoveries are
   isolated and never share credentials.

Neither changes the connect/verify/trust/launch flow. The connector still drives
the existing Steampipe service over its Postgres interface and hands off to the
existing Bellu pipeline.

## Architecture

```
bellu-api (connector)                 steampipe container
───────────────────────               ─────────────────────────────
Discover(target) builds:              reads config from shared volume
  cfg.connection_schema=aws_conn_<id>  ─┐
  env.BELLU_STEAMPIPE_CONNECTION=…      │  steampipe_config  (shared)
Credential_Sink writes:                │  <conn>.spc per session
  <shared>/aws_conn_<id>.spc  ─────────┘
  (session temp creds)
  waits until schema registered         plugins in steampipe_plugins (persisted)
source queries:
  SELECT … FROM aws_conn_<id>.aws_account
Credential_Sink cleanup: rm <conn>.spc
```

## Components and Interfaces

### Plugin cache (Requirement 1)

- `deploy/docker-compose.yml` adds a named volume `steampipe_plugins` mounted at
  `/home/steampipe/.steampipe/plugins`.
- The steampipe entrypoint installs the AWS plugin from GHCR only when the plugin
  directory is absent:
  `[ -d .../plugins/turbot/aws@latest ] || steampipe plugin install ghcr.io/turbot/steampipe/plugins/turbot/aws@latest`.
- The install guard checks the plugin directory (authoritative) rather than
  `steampipe plugin list`, whose output also prints failed/unresolved refs.

### Per-connection config (Requirement 2)

- **Steampipe source** (`internal/source/steampipe/steampipe.go`): the `Source`
  gains a `schema` field. `Factory` reads `cfg["connection_schema"]`. When set,
  `collectTable` qualifies the table as `<schema>.<table>` so the query targets
  exactly one connection and never aggregates across connections.
- **AWS adapter** (`internal/connector/provider/aws/adapter.go`): `Discover`
  derives `aws_conn_<ConnectionID>` and sets it as both `cfg["connection_schema"]`
  and `env["BELLU_STEAMPIPE_CONNECTION"]`. The catalog selector stays `aws`
  (aws.yaml); only the query schema is per-connection.
- **Credential sink** (`backend/cmd/bellu-api/main.go`,
  `steampipeConfigSink`): writes `<shared>/aws_conn_<id>.spc` defining
  `connection "aws_conn_<id>"` with the session's `access_key`/`secret_key`/
  `session_token`. Returns a cleanup that `os.Remove`s the file. After writing it
  calls `waitForSteampipeSchema` (bounded, 20s) which polls
  `information_schema.schemata` until the schema is registered.
- The sink is injected into the discovery `Orchestrator` via `Options.Sink`; the
  orchestrator already calls the sink before building the source and runs cleanup
  when the session ends.

## Data Models

No database changes. The connector persists no credentials; session credentials
live only in the per-session `.spc` file on the shared volume and are deleted at
session end.

## Error Handling

- If the Connection_Schema is not registered within the bounded wait, discovery
  proceeds and surfaces the resulting "schema does not exist" query error rather
  than hanging indefinitely.
- A plugin install failure (e.g. registry outage) leaves discovery to fail with a
  clear table/connection error; the control-panel flow is unaffected.

## Testing Strategy

- **Unit:** `Source` with `connection_schema` set qualifies table names; empty
  leaves them unqualified (table-driven).
- **Behavioral/manual (proven live):**
  - Plugin cache: restart Steampipe → ready in ~5s with no re-download; the
    `aws@latest` plugin dir persists in the volume.
  - Per-connection isolation: two distinct `aws_conn_*.spc` files register two
    independent schemas; each `aws_conn_<id>.aws_account` resolves separately.
- Property-based testing is not applicable (container/registry I/O and config
  reload timing); example and behavioral checks cover the behavior.

## Requirements Mapping

| Requirement | Design element |
|-------------|----------------|
| R1 Plugin cache | `steampipe_plugins` volume; dir-based install guard; GHCR install |
| R2.1 Per-session config file | `steampipeConfigSink` writes `aws_conn_<id>.spc` |
| R2.2 Schema-scoped query | `Source.schema` + `collectTable` qualification; `Discover` sets `connection_schema` |
| R2.3 Concurrent isolation | distinct file + schema per ConnectionID |
| R2.4 Cleanup | sink cleanup `os.Remove` on session end |
| R2.5 Schema-ready wait | `waitForSteampipeSchema` bounded poll |
| R2.6 Shared volume, no DB secrets | `steampipe_config` shared volume; credentials only in the session file |
