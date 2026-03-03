#!/bin/bash
# Backfill entry embeddings via API. Loads JOT_API_URL, JOT_API_KEY from .env.
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

if [ -f .env ]; then
  export $(grep -v '^#' .env | grep -v '^$' | xargs)
fi

: "${JOT_API_URL:?Set JOT_API_URL (e.g. https://us-central1-YOUR_PROJECT.cloudfunctions.net/jot-api-go)}"
: "${BACKFILL_EMBEDDING_LIMIT:=20}"

if [ -z "$JOT_API_KEY" ]; then
  echo "Error: JOT_API_KEY not set in .env"
  exit 1
fi

echo "Backfilling embeddings (limit=$BACKFILL_EMBEDDING_LIMIT per batch)..."
total=0

while true; do
  resp=$(curl -s -X POST \
    -H "X-API-Key: $JOT_API_KEY" \
    -H "Content-Type: application/json" \
    "${JOT_API_URL}/backfill-embeddings?limit=${BACKFILL_EMBEDDING_LIMIT}")

  if echo "$resp" | grep -q '"error"'; then
    echo "Error: $(echo "$resp" | grep -o '"error":"[^"]*"' | cut -d'"' -f4)"
    exit 1
  fi

  processed=$(echo "$resp" | grep -o '"processed":[0-9]*' | cut -d: -f2)
  total=$((total + processed))
  echo "  Processed $processed (total $total)"

  [ "$processed" -eq 0 ] && break
  sleep 35  # Rate limit 2/min
done

echo "Done. Backfilled $total entries."
