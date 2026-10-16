#!/usr/bin/env bash
# Bring up the Steampipe collector service (and the customer datastores + API)
# so `bellu run` has somewhere to collect from and the lab can query the graph.
# Reuses the existing customer compose stack's collectors profile.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$HERE/.." && pwd)"
COMPOSE="docker compose -f $REPO_ROOT/deploy/docker-compose.yml"

echo "==> bringing up datastores + API + steampipe collector"
$COMPOSE up -d postgres-customer memgraph bellu-api
$COMPOSE --profile collectors up -d steampipe

echo "==> waiting for steampipe to accept connections (:9193)"
for i in $(seq 1 30); do
  if docker exec bellu-steampipe-1 sh -c 'pg_isready -h localhost -p 9193' >/dev/null 2>&1; then
    echo "==> steampipe ready"
    exit 0
  fi
  sleep 2
done
echo "steampipe-up: timed out waiting for steampipe :9193" >&2
exit 1
