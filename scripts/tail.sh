#!/bin/bash
#
# Tail Cloud Run logs for the Jot service.
# Usage: ./scripts/tail.sh <dev|prod>
# Environment must be explicit (no default). Script will confirm before continuing.
#
set -e

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"
source "$REPO_ROOT/scripts/lib/env-confirm.sh"
require_env_and_confirm "$1"
shift

if [ "$ENV_TARGET" == "prod" ]; then
  echo -e "\033[1;33mTailing PRODUCTION logs...\033[0m"
else
  echo -e "\033[1;33mTailing DEVELOPMENT logs...\033[0m"
fi

if [ -f "$ENV_FILE" ]; then
  set -a
  source "$ENV_FILE"
  set +a
fi

PROJECT="${GOOGLE_CLOUD_PROJECT:?Set GOOGLE_CLOUD_PROJECT in $ENV_FILE}"
SERVICE_NAME="${SERVICE_NAME:-jot-api-go}"
REGION="${REGION:-us-central1}"
# ... rest of the existing script ...
# Match deploy.sh: same region and service so we read logs from the revision that serves requests.
FILTER="resource.type=\"cloud_run_revision\" AND resource.labels.service_name=\"$SERVICE_NAME\" AND resource.labels.location=\"$REGION\""
POLL_SEC="${LOG_TAIL_POLL_SEC:-2}"

# Start from current time so we only show new logs (no dump of recent history).
last_ts=$(date -u +"%Y-%m-%dT%H:%M:%S.000000000Z")

echo "Tailing Cloud Run logs: $SERVICE_NAME (project=$PROJECT, region=$REGION) from now, polling every ${POLL_SEC}s" >&2
echo "Run 'jot log hello' or 'jot query something' then watch for request_started, FOH, and handler logs." >&2
echo "Ctrl+C to stop." >&2

use_jq=0
command -v jq &>/dev/null && use_jq=1

while true; do
  CURRENT_FILTER="$FILTER AND timestamp>\"$last_ts\""
  if [ "$use_jq" = 1 ]; then
    raw=$(gcloud logging read "$CURRENT_FILTER" \
      --project="$PROJECT" \
      --limit=200 \
      --order=asc \
      --format=json 2>/dev/null) || true
    [ -z "$raw" ] && { sleep "$POLL_SEC"; continue; }
    # Format each entry (API already returned only new logs when last_ts was set).
    new_entries=$(echo "$raw" | jq -r '
      if . == [] or . == null then empty
      else .[] |
        (if .jsonPayload then
          (.jsonPayload.level // .jsonPayload.severity // "INFO") as $lev |
          (.jsonPayload.msg // .jsonPayload.message // "") as $msg |
          (if $msg != "" then "\(.timestamp) [\($lev)] \($msg)" + (if .jsonPayload.trace_id then " trace_id=\(.jsonPayload.trace_id)" else "" end) + (if .jsonPayload.path then " path=\(.jsonPayload.path)" else "" end)
          else empty
          end)
        else
          (.textPayload // "") as $t |
          (if $t == "" or $t == null then empty
          elif ($t | length) > 400 or ($t | test("<!DOCTYPE|<html|function[[:space:]]*\\(")) then .timestamp + " [long/noise] " + ($t | .[0:80]) + "..."
          elif ($t | startswith("{")) then
            (.timestamp) as $ts | (try ($t | fromjson) catch null) as $j |
            (if $j and ($j.msg // $j.message) then $ts + " [\($j.level // $j.severity // "INFO")] \($j.msg // $j.message)" else .timestamp + " " + ($t | .[0:200]) end)
          else .timestamp + " " + $t
          end)
        end)
      end')
    [ -n "$new_entries" ] && echo "$new_entries"
    latest=$(echo "$raw" | jq -r 'if length > 0 then .[-1].timestamp else empty end')
    [ -n "$latest" ] && last_ts="$latest"
  else
    gcloud logging read "$CURRENT_FILTER" --project="$PROJECT" --limit=50 --order=asc \
      --format='table(timestamp, jsonPayload.level, jsonPayload.msg)' 2>/dev/null || true
    latest=$(gcloud logging read "$CURRENT_FILTER" --project="$PROJECT" --limit=1 --order=desc --format='value(timestamp)' 2>/dev/null) || true
    [ -n "$latest" ] && last_ts="$latest"
  fi
  sleep "$POLL_SEC"
done
