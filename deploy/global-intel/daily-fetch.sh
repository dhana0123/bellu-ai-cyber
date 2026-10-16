#!/usr/bin/env bash
# LOCAL DEVELOPMENT daily Global Intelligence fetch.
#
# Runs the global-intel pipeline inside the already-running local dev container
# (deploy/global-intel/docker-compose.yml), against the LOCAL postgres-intel and
# LOCAL OpenSearch. Nothing here touches the cloud — no S3, no ECS/Fargate, no
# EventBridge. This is the localhost counterpart to the Fargate scheduled task
# used in production (deploy/global-intel/terraform).
#
# Modes:
#   incremental (default) — resume each source from its stored cursor; fast,
#                           sends If-Modified-Since so unchanged archives are
#                           304 no-ops. Use this for the daily schedule.
#   daily                 — full fetch of every source (empty cursor). Heavier;
#                           use for the first load or a forced full refresh.
#
# Usage:
#   deploy/global-intel/daily-fetch.sh [incremental|daily]
#
# Schedule it locally with cron (see the comment block at the bottom), e.g.
# every day at 03:30:
#   30 3 * * * /home/ubuntu/projects/bellu-ai-cyber/deploy/global-intel/daily-fetch.sh >> /home/ubuntu/projects/bellu-ai-cyber/deploy/global-intel/daily-fetch.log 2>&1
set -euo pipefail

MODE="${1:-incremental}"
case "$MODE" in
  incremental|daily) ;;
  *) echo "usage: $(basename "$0") [incremental|daily]" >&2; exit 2 ;;
esac

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$HERE/../.." && pwd)"
COMPOSE_FILE="$HERE/docker-compose.yml"
SERVICE="global-intel"

echo "==> $(date -u +%Y-%m-%dT%H:%M:%SZ) global-intel fetch (mode=$MODE, local dev)"

# Ensure the local intel stack is up (postgres-intel + opensearch + the CLI
# container). `up -d` is a no-op when everything is already running.
docker compose -f "$COMPOSE_FILE" up -d >/dev/null

# Apply migrations idempotently so a fresh/upgraded intel DB has its schema.
docker compose -f "$COMPOSE_FILE" exec -T "$SERVICE" global-intel migrate

# Run the fetch -> parse -> normalize -> correlate (-> index) pipeline.
docker compose -f "$COMPOSE_FILE" exec -T "$SERVICE" global-intel run --mode "$MODE"

echo "==> $(date -u +%Y-%m-%dT%H:%M:%SZ) global-intel fetch complete"

# ---------------------------------------------------------------------------
# To schedule this once per day on the local dev host, add a user crontab entry
# (run `crontab -e` and paste), e.g. daily at 03:30 local time:
#
#   30 3 * * * /home/ubuntu/projects/bellu-ai-cyber/deploy/global-intel/daily-fetch.sh >> /home/ubuntu/projects/bellu-ai-cyber/deploy/global-intel/daily-fetch.log 2>&1
#
# The install-cron.sh helper next to this script sets that up for you.
# ---------------------------------------------------------------------------
