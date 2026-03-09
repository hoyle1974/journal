#!/bin/bash
#
# Tail Cloud Run logs and output only lines containing LLM_CONTEXT_AUDIT.
# Same log source and poll loop as scripts/tail.sh; filters to context-audit lines only.
# Requires: gcloud CLI, GOOGLE_CLOUD_PROJECT in .env. Optional: jq (for best display).
#
set -e

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

if [ -f .env ]; then
  set -a
  # shellcheck source=.env
  source .env
  set +a
fi

PROJECT="${GOOGLE_CLOUD_PROJECT:?Set GOOGLE_CLOUD_PROJECT in .env or export GOOGLE_CLOUD_PROJECT=your-project-id}"
SERVICE_NAME="${SERVICE_NAME:-jot-api-go}"
REGION="${REGION:-us-central1}"
FILTER="resource.type=\"cloud_run_revision\" AND resource.labels.service_name=\"$SERVICE_NAME\" AND resource.labels.location=\"$REGION\""
POLL_SEC="${LOG_TAIL_POLL_SEC:-2}"

last_ts=$(date -u +"%Y-%m-%dT%H:%M:%S.000000000Z")

echo "Tailing Cloud Run logs (LLM_CONTEXT_AUDIT only): $SERVICE_NAME (project=$PROJECT, region=$REGION), polling every ${POLL_SEC}s" >&2
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
    new_entries=$(echo "$raw" | jq -r '
      if . == [] or . == null then empty
      else .[] |
        (if .jsonPayload then
          (.jsonPayload.level // .jsonPayload.severity // "INFO") as $lev |
          (.jsonPayload.msg // .jsonPayload.message // "") as $msg |
          (if $msg != "" and ($msg | test("LLM_CONTEXT_AUDIT")) then
            "\(.timestamp) [\($lev)] \($msg)" + (if .jsonPayload.trace_id then " trace_id=\(.jsonPayload.trace_id)" else "" end) + (if .jsonPayload.path then " path=\(.jsonPayload.path)" else "" end)
          else empty end)
        else
          (.textPayload // "") as $t |
          (if $t != "" and $t != null and ($t | test("LLM_CONTEXT_AUDIT")) then
            (if ($t | length) > 400 then .timestamp + " [long] " + ($t | .[0:200]) + "..." else .timestamp + " " + $t end)
          else empty end)
        end)
      end')
    [ -n "$new_entries" ] && echo "$new_entries"
    latest=$(echo "$raw" | jq -r 'if length > 0 then .[-1].timestamp else empty end')
    [ -n "$latest" ] && last_ts="$latest"
  else
    gcloud logging read "$CURRENT_FILTER" --project="$PROJECT" --limit=50 --order=asc \
      --format='table(timestamp, jsonPayload.level, jsonPayload.msg)' 2>/dev/null | grep 'LLM_CONTEXT_AUDIT' || true
    latest=$(gcloud logging read "$CURRENT_FILTER" --project="$PROJECT" --limit=1 --order=desc --format='value(timestamp)' 2>/dev/null) || true
    [ -n "$latest" ] && last_ts="$latest"
  fi
  sleep "$POLL_SEC"
done
