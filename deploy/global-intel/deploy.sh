#!/usr/bin/env bash
# Build + push the global-intel image and apply the Terraform stack for a given
# environment+region. Infra config lives in Terraform; this script just wires
# the image and selects the right tfvars + backend state key.
#
# Usage:
#   deploy.sh <env> <region> [plan|apply|destroy]
# Examples:
#   deploy.sh dev us-east-1 plan
#   deploy.sh prod ap-south-1 apply
#
# Prereqs: aws CLI, docker, terraform; AWS creds for the target account; a
# versioned/encrypted Terraform state bucket set in the *.backend.hcl files.
set -euo pipefail

ENV="${1:?usage: deploy.sh <env> <region> [plan|apply|destroy]}"
REGION="${2:?usage: deploy.sh <env> <region> [plan|apply|destroy]}"
ACTION="${3:-plan}"

HERE="$(cd "$(dirname "$0")" && pwd)"
TFDIR="$HERE/terraform"
REPO_ROOT="$(cd "$HERE/../.." && pwd)"
TFVARS="environments/$ENV/$REGION.tfvars"
BACKEND="environments/$ENV/$REGION.backend.hcl"
NAME="global-intel-$ENV"

[ -f "$TFDIR/$TFVARS" ]  || { echo "missing $TFVARS"; exit 1; }
[ -f "$TFDIR/$BACKEND" ] || { echo "missing $BACKEND"; exit 1; }

ACCOUNT_ID="$(aws sts get-caller-identity --query Account --output text)"
ECR="$ACCOUNT_ID.dkr.ecr.$REGION.amazonaws.com/$NAME"

echo "==> terraform init ($ENV/$REGION)"
terraform -chdir="$TFDIR" init -reconfigure -input=false -backend-config="$BACKEND"

if [ "$ACTION" = "destroy" ]; then
  terraform -chdir="$TFDIR" destroy -var-file="$TFVARS"
  exit 0
fi

# Build + push the global-intel image to the stack's ECR repo (created by TF on
# first apply; on a brand-new account run `apply` once to create the repo, then
# re-run to push + roll the task definition to the new image).
if aws ecr describe-repositories --repository-names "$NAME" --region "$REGION" >/dev/null 2>&1; then
  TAG="$(git -C "$REPO_ROOT" rev-parse --short HEAD 2>/dev/null || date +%Y%m%d%H%M%S)"
  echo "==> building image $ECR:$TAG"
  aws ecr get-login-password --region "$REGION" | docker login --username AWS --password-stdin "${ECR%/*}"
  docker build -f "$HERE/Dockerfile.intel" -t "$ECR:$TAG" -t "$ECR:latest" "$REPO_ROOT"
  docker push "$ECR:$TAG"
  docker push "$ECR:latest"
  IMAGE_ARG=(-var "container_image=$ECR:$TAG")
else
  echo "==> ECR repo not found yet; first apply will create it. Pushing image on the next run."
  IMAGE_ARG=()
fi

echo "==> terraform $ACTION ($ENV/$REGION)"
terraform -chdir="$TFDIR" "$ACTION" -var-file="$TFVARS" "${IMAGE_ARG[@]}"
