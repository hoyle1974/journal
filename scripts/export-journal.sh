#!/bin/bash
#
# Export all journal entries to a local archive directory.
# Usage: ./scripts/export-journal.sh <dev|prod>
# Output directory: ./jot-export-YYYY-MM-DD (in repo root)
#
set -e

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"
source "$REPO_ROOT/scripts/lib/env-confirm.sh"
require_env_and_confirm "$1"
shift

if [ -f "$ENV_FILE" ]; then
  set -a
  source "$ENV_FILE"
  set +a
else
  echo "Error: $ENV_FILE not found."
  exit 1
fi

OUTPUT="$REPO_ROOT/jot-export-$(date +%Y-%m-%d)"

if [ -d "$OUTPUT" ]; then
  echo "Error: Archive $OUTPUT already exists. Remove it or rename it before running again."
  exit 1
fi

echo "Exporting journal to $OUTPUT ..."
JOT_PROFILE="$ENV_TARGET" go run ./cmd/admin export-journal --output "$OUTPUT"
echo "Done. Archive: $OUTPUT"
