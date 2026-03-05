#!/bin/bash
#
# Tail logs from the running Cloud Run service (Google Cloud Logging).
# Uses "gcloud logging read" in a poll loop (no log-streaming component needed).
# Requires: gcloud CLI, same GOOGLE_CLOUD_PROJECT as deploy. Optional: jq (to avoid duplicates).
#
set -e

if [ -f .env ]; then
  set -a
  # shellcheck source=.env
  source .env
  set +a
fi

PROJECT="${GOOGLE_CLOUD_PROJECT:?Set GOOGLE_CLOUD_PROJECT in .env or export GOOGLE_CLOUD_PROJECT=your-project-id}"
SERVICE_NAME="${SERVICE_NAME:-jot-api-go}"
FILTER="resource.type=\"cloud_run_revision\" AND resource.labels.service_name=\"$SERVICE_NAME\""
POLL_SEC="${LOG_TAIL_POLL_SEC:-2}"

echo "Tailing Cloud Run logs: $SERVICE_NAME (project=$PROJECT), polling every ${POLL_SEC}s" >&2
echo "Ctrl+C to stop." >&2

last_ts=""
use_jq=0
command -v jq &>/dev/null && use_jq=1

while true; do
  if [ "$use_jq" = 1 ]; then
    raw=$(gcloud logging read "$FILTER" \
      --project="$PROJECT" \
      --freshness=2m \
      --limit=200 \
      --order=asc \
      --format=json 2>/dev/null) || true
    [ -z "$raw" ] && { sleep "$POLL_SEC"; continue; }
    new_entries=$(echo "$raw" | jq -r --arg last "$last_ts" '
      if . == [] or . == null then empty
      else [.[] | select(.timestamp > $last)] | sort_by(.timestamp)[] |
        "\(.timestamp) \(.textPayload // .jsonPayload.msg // (.jsonPayload | tostring))"
      end')
    [ -n "$new_entries" ] && echo "$new_entries"
    latest=$(echo "$raw" | jq -r 'if length > 0 then .[-1].timestamp else empty end')
    [ -n "$latest" ] && last_ts="$latest"
  else
    gcloud logging read "$FILTER" --project="$PROJECT" --freshness=1m --limit=50 --order=asc \
      --format='table(timestamp, textPayload, jsonPayload.msg)' 2>/dev/null || true
  fi
  sleep "$POLL_SEC"
done
