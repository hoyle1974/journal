#!/bin/bash
#
# Read Cloud Run logs (same source as tail.sh) and output only lines containing LLM_CONTEXT_AUDIT.
# One-shot read over a time window; no tail/poll.
# Usage: ./scripts/grep-context-audit.sh <dev|prod> [--freshness=DURATION] [--limit=N]
#   Environment must be explicit (no default). Script will confirm before continuing.
#   --freshness  gcloud freshness (default: 1d). e.g. 1h, 24h, 7d
#   --limit      max log entries to fetch (default: 1000)
#
set -e

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"
source "$REPO_ROOT/scripts/lib/env-confirm.sh"
require_env_and_confirm "$1" " [--freshness=DURATION] [--limit=N]"
shift

if [ -f "$ENV_FILE" ]; then
  set -a
  # shellcheck source=.env
  source "$ENV_FILE"
  set +a
fi

PROJECT="${GOOGLE_CLOUD_PROJECT:?Set GOOGLE_CLOUD_PROJECT in $ENV_FILE or export GOOGLE_CLOUD_PROJECT=your-project-id}"
SERVICE_NAME="${SERVICE_NAME:-jot-api-go}"
REGION="${REGION:-us-central1}"
FILTER="resource.type=\"cloud_run_revision\" AND resource.labels.service_name=\"$SERVICE_NAME\" AND resource.labels.location=\"$REGION\""

FRESHNESS="${LOG_GREP_FRESHNESS:-7d}"
LIMIT="${LOG_GREP_LIMIT:-1000}"
while [ $# -gt 0 ]; do
  case "$1" in
    --freshness=*) FRESHNESS="${1#--freshness=}"; shift ;;
    --limit=*)     LIMIT="${1#--limit=}"; shift ;;
    *) echo "Usage: $0 <dev|prod> [--freshness=DURATION] [--limit=N]" >&2; exit 1 ;;
  esac
done

use_jq=0
command -v jq &>/dev/null && use_jq=1

echo "Reading Cloud Run logs (project=$PROJECT, service=$SERVICE_NAME, freshness=$FRESHNESS, limit=$LIMIT), filtering for LLM_CONTEXT_AUDIT..." >&2

if [ "$use_jq" = 1 ]; then
  # Fetch newest-first so recent logs (e.g. from minutes ago) are in the limit; we reverse for chronological output
  raw=$(gcloud logging read "$FILTER" \
    --project="$PROJECT" \
    --freshness="$FRESHNESS" \
    --limit="$LIMIT" \
    --order=desc \
    --format=json) || true

  if [ -z "$raw" ]; then
    echo "No log entries found (or gcloud failed; check errors above)." >&2
    exit 0
  fi
  if [ "${raw:0:1}" != "[" ]; then
    echo "gcloud may have failed (output is not JSON). First line:" >&2
    echo "$raw" | head -1 >&2
    exit 1
  fi
  if [ "$raw" = "[]" ]; then
    echo "No log entries found." >&2
    exit 0
  fi

  total=$(echo "$raw" | jq -r 'length')
  # Match by message text or by structured event field (slog logs event=LLM_CONTEXT_AUDIT); reverse so output is chronological
  out=$(echo "$raw" | jq -r '
    if . == [] or . == null then empty
    else reverse | .[] |
      (if .jsonPayload then
        (.jsonPayload.level // .jsonPayload.severity // "INFO") as $lev |
        (.jsonPayload.msg // .jsonPayload.message // "") as $msg |
        (.jsonPayload.event // "") as $evt |
        (if (($msg != "" and ($msg | test("LLM_CONTEXT_AUDIT"))) or ($evt == "LLM_CONTEXT_AUDIT")) then
          (if $msg != "" then "\(.timestamp) [\($lev)] \($msg)" else "\(.timestamp) [\($lev)] event=\($evt) " + (.jsonPayload | to_entries | map("\(.key)=\(.value)") | join(" ")) end) + (if .jsonPayload.trace_id then " trace_id=\(.jsonPayload.trace_id)" else "" end) + (if .jsonPayload.path then " path=\(.jsonPayload.path)" else "" end)
        else empty end)
      else
        (.textPayload // "") as $t |
        (if $t != "" and $t != null and ($t | test("LLM_CONTEXT_AUDIT")) then
          (if ($t | length) > 400 then .timestamp + " [long] " + ($t | .[0:200]) + "..." else .timestamp + " " + $t end)
        else empty end)
      end)
    end')
  echo "$out"
  if [ -z "$out" ]; then
    echo "No log lines contained LLM_CONTEXT_AUDIT ($total entries read). Try --freshness=7d or --limit=5000 if you expect older logs." >&2
  fi
else
  gcloud logging read "$FILTER" \
    --project="$PROJECT" \
    --freshness="$FRESHNESS" \
    --limit="$LIMIT" \
    --order=desc \
    --format='table(timestamp, jsonPayload.level, jsonPayload.msg)' | grep 'LLM_CONTEXT_AUDIT' || true
fi
