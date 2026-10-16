#!/usr/bin/env bash
# Install (or refresh) the LOCAL daily Global Intelligence fetch cron job for
# the current user. Idempotent: re-running replaces the existing entry rather
# than adding a duplicate. Local dev only — this schedules the dockerized
# incremental fetch on this host; it does not provision any cloud scheduler.
#
# Usage:
#   deploy/global-intel/install-cron.sh            # default: daily at 03:30
#   deploy/global-intel/install-cron.sh "0 4 * * *"  # custom cron expression
#
# Remove it later with:
#   crontab -l | grep -v daily-fetch.sh | crontab -
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$HERE/daily-fetch.sh"
LOG="$HERE/daily-fetch.log"
SCHEDULE="${1:-30 3 * * *}"

chmod +x "$SCRIPT"

LINE="$SCHEDULE $SCRIPT >> $LOG 2>&1"

# Drop any previous daily-fetch entry, then append the current one.
( crontab -l 2>/dev/null | grep -v "daily-fetch.sh" || true; echo "$LINE" ) | crontab -

echo "installed local daily global-intel fetch:"
echo "  $LINE"
echo
echo "current crontab:"
crontab -l
