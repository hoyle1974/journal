#!/bin/bash
#
# Convenience script: run infrastructure and secrets setup for production.
# Usage: ./setup-prod.sh
# Script will confirm before continuing (each sub-script also confirms).
#
set -e

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$REPO_ROOT"

echo ""
echo "This will run setup for PRODUCTION (setup-infra.sh prod, setup-secrets.sh prod)."
read -r -p "Continue? [y/N] " resp
if [[ ! "$resp" =~ ^[yY](es)?$ ]]; then
  echo "Aborted."
  exit 0
fi
echo ""

# Set gcloud to prod
gcloud config set project journal-prod-489717

./scripts/setup-infra.sh prod
./scripts/setup-secrets.sh prod
