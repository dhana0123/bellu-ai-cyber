# Requirements Document

## Introduction

The Cloud Connector drives discovery by querying a shared Steampipe service. Two
runtime weaknesses were addressed: (1) the Steampipe AWS plugin was lost on every
`docker compose down`, forcing a slow re-download on the next boot; and (2) all
discoveries shared a single `aws.spc` connection file, so concurrent discoveries
across different accounts would overwrite each other's credentials.

This spec documents the two improvements that make connector discovery robust at
runtime: a persistent plugin cache, and per-connection Steampipe configuration so
discoveries are isolated and can run in parallel. It does not change the connect,
verify, trust, or one-click-launch flows.

## Glossary

- **Steampipe_Service**: The Steampipe container exposing a Postgres interface on
  port 9193 that the connector queries for cloud resources.
- **Plugin_Cache**: The persisted `~/.steampipe/plugins` directory holding the
  installed AWS plugin.
- **Discovery_Session**: A single run of discovery for one Connection.
- **Connection_Config**: A per-session Steampipe `.spc` file defining a uniquely
  named connection with that session's temporary AWS credentials.
- **Connection_Schema**: The Postgres schema Steampipe exposes for a connection,
  named after the connection (e.g. `aws_conn_<id>`), used to scope queries.
- **Credential_Sink**: The connector component that writes a Connection_Config
  before a Discovery_Session and removes it when the session ends.

## Requirements

### Requirement 1: Persistent plugin cache

**User Story:** As an operator, I want the Steampipe AWS plugin to persist across
restarts, so that bringing the stack up is fast and does not re-download the
plugin every time.

#### Acceptance Criteria

1. THE Steampipe_Service SHALL store its installed plugins in a persistent Docker
   volume mounted at `~/.steampipe/plugins`.
2. WHEN the Steampipe_Service starts and the AWS plugin is already present in the
   Plugin_Cache, THE Steampipe_Service SHALL skip installation.
3. WHEN the Steampipe_Service starts and the AWS plugin is absent, THE
   Steampipe_Service SHALL install the AWS plugin from the GHCR registry.
4. WHEN the stack is recreated with `docker compose down` followed by `up`, THE
   Steampipe_Service SHALL reuse the cached plugin without re-downloading it.

### Requirement 2: Per-connection Steampipe configuration

**User Story:** As an operator, I want each discovery to use its own Steampipe
connection, so that running discovery for multiple accounts at the same time does
not mix up credentials.

#### Acceptance Criteria

1. WHEN a Discovery_Session begins, THE Credential_Sink SHALL write a
   Connection_Config file named for that session's connection
   (`aws_conn_<id>.spc`), defining a connection named `aws_conn_<id>` with that
   session's temporary AWS credentials.
2. WHEN a Discovery_Session runs, THE connector SHALL query the session's
   Connection_Schema (`aws_conn_<id>.<table>`) and SHALL NOT query an
   unqualified table that aggregates across connections.
3. WHEN two Discovery_Sessions for different Connections run concurrently, THE
   connector SHALL keep their credentials and results isolated by using distinct
   Connection_Config files and Connection_Schemas.
4. WHEN a Discovery_Session ends, THE Credential_Sink SHALL delete that session's
   Connection_Config file so credentials do not linger.
5. WHEN a Connection_Config is newly written, THE connector SHALL wait, up to a
   bounded timeout, until a representative table in the Connection_Schema resolves before querying it.
6. THE Credential_Sink SHALL write the Connection_Config to a volume shared with
   the Steampipe_Service and SHALL write zero credentials to the connector
   database.
