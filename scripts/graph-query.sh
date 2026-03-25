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

DOT_FILE="$(mktemp /tmp/jot-graph-XXXXXX.dot)"
PNG_FILE="${DOT_FILE%.dot}.png"

JOT_PROFILE="$ENV_TARGET" go run ./cmd/admin graph-query -dot-file="$DOT_FILE" "$@"

if [ -s "$DOT_FILE" ] && command -v dot &>/dev/null && command -v imgcat &>/dev/null; then
  dot -Tpng "$DOT_FILE" -o "$PNG_FILE" 2>/dev/null 
  imgcat --width $(tput cols) "$PNG_FILE"
  echo "(graph saved to $PNG_FILE)"
fi

rm -f "$DOT_FILE"
