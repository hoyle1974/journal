#!/bin/bash
#
# Replay a local journal archive through the Jot API.
# Usage: ./scripts/replay-journal.sh <dev|prod> <archive-dir>
# JOT_API_URL and JOT_API_KEY are read from the sourced .env file.
#
set -e

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"
source "$REPO_ROOT/scripts/lib/env-confirm.sh"
require_env_and_confirm "$1" " <archive-dir>"
shift

ARCHIVE="${1:-}"
if [ -z "$ARCHIVE" ]; then
  echo "Usage: $0 <dev|prod> <archive-dir>"
  exit 1
fi

if [ -f "$ENV_FILE" ]; then
  set -a
  source "$ENV_FILE"
  set +a
else
  echo "Error: $ENV_FILE not found."
  exit 1
fi

if [ -z "${JOT_API_URL:-}" ]; then
  echo "Error: JOT_API_URL is not set in $ENV_FILE."
  exit 1
fi

if [ -z "${JOT_API_KEY:-}" ]; then
  echo "Error: JOT_API_KEY is not set in $ENV_FILE."
  exit 1
fi

if [ ! -f "$ARCHIVE/manifest.jsonl" ]; then
  echo "Error: $ARCHIVE/manifest.jsonl not found. Is this a valid export archive?"
  exit 1
fi

echo "Replaying archive $ARCHIVE to $JOT_API_URL ..."
JOT_PROFILE="$ENV_TARGET" go run ./cmd/admin replay-journal --archive "$ARCHIVE"
echo "Done."
