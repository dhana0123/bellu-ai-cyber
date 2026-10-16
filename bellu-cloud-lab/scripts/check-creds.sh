#!/usr/bin/env bash
# Verify ambient AWS credentials are usable before a scenario tries to apply.
# No secrets are read or written; this only confirms the caller identity.
set -euo pipefail

if ! command -v aws >/dev/null 2>&1; then
  echo "check-creds: aws CLI not found on PATH" >&2
  exit 1
fi

echo "==> aws sts get-caller-identity"
if ! aws sts get-caller-identity --output text; then
  cat >&2 <<'EOF'
check-creds: no usable AWS credentials.
Authenticate natively (no secrets in files), e.g.:
  export AWS_PROFILE=my-test-account
  # or
  export AWS_ACCESS_KEY_ID=... AWS_SECRET_ACCESS_KEY=... AWS_REGION=us-east-1
EOF
  exit 1
fi

echo "==> credentials OK"
