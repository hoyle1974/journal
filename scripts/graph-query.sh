#!/bin/bash
#
# Search the knowledge graph by keyword or phrase and print the subgraph.
# Usage: ./scripts/graph-query.sh <dev|prod> [-depth=N] [-limit=N] [-limit-per-edge=N] <query>
#
# Examples:
#   ./scripts/graph-query.sh dev "Gloria birthday"
#   ./scripts/graph-query.sh dev -depth=2 -limit=5 "work projects"
#   ./scripts/graph-query.sh prod -depth=3 "running"
#
set -e

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"
source "$REPO_ROOT/scripts/lib/env-confirm.sh"
require_env_and_confirm "$1" " [-depth=N] [-limit=N] [-limit-per-edge=N] <query>"
shift

if [ -f "$ENV_FILE" ]; then
  set -a
  source "$ENV_FILE"
  set +a
else
  echo "Error: $ENV_FILE not found."
  exit 1
fi

JOT_PROFILE="$ENV_TARGET" go run ./cmd/admin graph-query "$@"
