# global-intel — local development

Run the Global Intelligence plane on your laptop against local Postgres +
OpenSearch. Everything is driven by the shared `deploy/docker-compose.yml` and
`make` targets; the same `global-intel` CLI binary you run locally is what the
Fargate task runs in production.

## What it needs

- **PostgreSQL** — the canonical system of record (shared with the main stack).
- **OpenSearch** — the search/index layer. Optional: with no
  `BELLU_SEARCH_ENDPOINT`, the pipeline simply skips indexing.
- Go 1.27+ and Docker. No AWS account or credentials required for local dev
  (the raw store falls back to the filesystem when `BELLU_RAW_BUCKET` is unset).

## Quick start

```bash
# 1. Start Postgres + OpenSearch (+ Dashboards) for the global-intel plane.
make gi-up
#   postgres :5432   opensearch http://localhost:9200   dashboards http://localhost:5601

# 2. Apply the intel_* / findings migrations (idempotent).
make gi-migrate

# 3. Run the pipeline: fetch -> parse -> normalize -> correlate -> index.
#    Indexing into OpenSearch is folded in automatically (BELLU_SEARCH_ENDPOINT
#    is set by the target). Raw payloads go to out/intel-raw on disk.
make gi-run                 # full daily run
make gi-run MODE=incremental # cursor-based incremental run

# 4. Inspect the search index.
make gi-search Q='pkg:pypi/requests'
#   or browse http://localhost:5601 (OpenSearch Dashboards)

# 5. Rebuild the index from Postgres (drop + reindex the whole corpus).
make gi-update

# Tear down (keeps named volumes; add `-v` yourself to wipe data).
make gi-down
```

> Per-source isolation: if one upstream (OSV/KEV/EPSS) is unreachable, the
> others still ingest and `run` exits non-zero listing the failed sources — the
> successful sources are persisted and indexed. Check `intel_fetch_runs` for the
> per-source status/audit trail.

## Running the CLI directly (without make)

```bash
export BELLU_POSTGRES_DSN='postgres://bellu:bellu@localhost:5432/bellu?sslmode=disable'
export BELLU_SEARCH_ENDPOINT='http://localhost:9200'   # omit to skip indexing

go run ./cmd/global-intel run --mode daily --raw-dir out/intel-raw
go run ./cmd/global-intel update --rebuild
go run ./cmd/global-intel --help
```

Config precedence: flags > env (`BELLU_POSTGRES_DSN`, `BELLU_SEARCH_ENDPOINT`,
`BELLU_RAW_BUCKET`, `BELLU_RAW_PREFIX`) > `bellu.yaml`.

## Developing inside the compose network

`make gi-shell` starts a long-running `global-intel` container (on the compose
network, so it reaches `postgres` and `opensearch` by service name) and drops
you into a shell:

```bash
make gi-shell
# inside the container:
global-intel run --mode daily --raw-dir /app/intel-raw
global-intel update --rebuild
```

## Environment variables

| Variable                 | Purpose                                             | Local default                         |
|--------------------------|-----------------------------------------------------|---------------------------------------|
| `BELLU_POSTGRES_DSN`     | Postgres DSN (system of record)                     | from `bellu.yaml`                     |
| `BELLU_SEARCH_ENDPOINT`  | OpenSearch endpoint; empty disables indexing        | `http://localhost:9200` (make target) |
| `BELLU_RAW_BUCKET`       | S3 raw bucket; empty uses the filesystem raw store  | unset (FS store under `--raw-dir`)    |
| `BELLU_RAW_PREFIX`       | optional S3 key prefix                              | unset                                 |

## Tests

```bash
make test               # unit tests (no Docker)
make test-integration   # testcontainers: spins up ephemeral Postgres/OpenSearch
```

The integration suite is independent of `make gi-up` — it manages its own
throwaway containers.

## Notes

- OpenSearch here runs single-node with the security plugin disabled (DEV ONLY);
  the index shows `yellow` health because there are no replicas, which is normal.
- This stack is the global-intel plane only. The customer graph (Memgraph) and
  control-panel API/UI come up via `make up` / `make panel` and are unaffected.
