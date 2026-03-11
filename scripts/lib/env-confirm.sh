#!/usr/bin/env bash
#
# Shared library for scripts that target dev or prod.
# Source this file, then call require_env_and_confirm "$1" [usage_suffix].
# Sets ENV_TARGET and ENV_FILE; caller must shift, then source ENV_FILE if needed.
#
# Usage from script:
#   REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
#   cd "$REPO_ROOT"
#   source "$REPO_ROOT/scripts/lib/env-confirm.sh"
#   require_env_and_confirm "$1" " [optional-args]"   # pass script's $1 and optional usage suffix
#   shift
#   # ... source "$ENV_FILE" and continue
#

# require_env_and_confirm <env_arg> [usage_suffix]
# Validates env_arg is dev or prod, prompts to confirm, sets ENV_TARGET and ENV_FILE.
# Caller must shift after to remove the env arg from the script's argv.
require_env_and_confirm() {
  local env_arg="$1"
  local usage_suffix="${2:-}"
  if [[ "$env_arg" != "dev" && "$env_arg" != "prod" ]]; then
    echo "Usage: $0 <dev|prod>${usage_suffix}"
    echo "Environment must be explicit: dev or prod."
    exit 1
  fi
  ENV_TARGET="$env_arg"
  if [[ "$ENV_TARGET" == "prod" ]]; then
    ENV_FILE=".env.prod"
  else
    ENV_FILE=".env"
  fi
  echo ""
  echo "Target environment: $ENV_TARGET (config: $ENV_FILE)"
  read -r -p "Continue? [y/N] " resp
  if [[ ! "$resp" =~ ^[yY](es)?$ ]]; then
    echo "Aborted."
    exit 0
  fi
  echo ""
}
