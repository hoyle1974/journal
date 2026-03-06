#!/bin/bash
#
# Deploy Jot Cloud Function to GCP (Go version)
# Uses container-based deployment for speed
#
set -e

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

# Load .env if present (GOOGLE_CLOUD_PROJECT and other vars)
if [ -f .env ]; then
  set -a
  # shellcheck source=.env
  source .env
  set +a
fi

PROJECT="${GOOGLE_CLOUD_PROJECT:?Set GOOGLE_CLOUD_PROJECT in .env or export GOOGLE_CLOUD_PROJECT=your-project-id}"
REGION="us-central1"
FUNCTION_NAME="jot-api-go"
IMAGE="$REGION-docker.pkg.dev/$PROJECT/jot/$FUNCTION_NAME"

# Resource configuration (QuerySeconds from internal/timeout/timeout.go)
QUERY_TIMEOUT=$(grep 'QuerySeconds = ' internal/timeout/timeout.go | sed 's/.*= \([0-9]*\).*/\1/')
CPU="1"
MEMORY="128Mi"
CONCURRENCY="80"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo -e "${YELLOW}Jot API Deployment (Go)${NC}"
echo "Project: $PROJECT"
echo "Region: $REGION"
echo ""

# Rebuild CLI binary
echo -e "${YELLOW}Rebuilding CLI binary...${NC}"
go build -o jot ./cmd/jot
echo -e "${GREEN}CLI binary rebuilt: ./jot${NC}"
echo ""

# Check if gcloud is installed
if ! command -v gcloud &> /dev/null; then
    echo -e "${RED}Error: gcloud CLI not found. Please install Google Cloud SDK.${NC}"
    exit 1
fi

# Set project
gcloud config set project $PROJECT 2>/dev/null

# Deploy Firestore indexes from firestore.indexes.json (ensure existing indexes are updated).
# Includes Janitor index: knowledge_nodes (last_recalled_at, significance_weight).
echo -e "${YELLOW}Deploying Firestore indexes...${NC}"
if command -v firebase &> /dev/null; then
  # Prefer Firebase CLI: syncs indexes from file (adds missing, leaves existing)
  # Use project so we don't rely on default; show errors if one index fails
  firebase use "$PROJECT" 2>/dev/null || true
  if firebase deploy --only firestore:indexes --project "$PROJECT" --non-interactive; then
    echo -e "${GREEN}Firestore indexes deployed via Firebase CLI.${NC}"
  else
    echo -e "${YELLOW}Firebase deploy had errors (see above). Check Firebase Console > Firestore > Indexes for status.${NC}"
  fi
elif command -v jq &> /dev/null && [ -f firestore.indexes.json ]; then
  # Fallback: create each index via gcloud with inline --field-config=... (gcloud does not accept file path)
  INDEXES_JSON="firestore.indexes.json"
  N=0
  CREATED=0
  while true; do
    CG=$(jq -r --argjson n "$N" '.indexes[$n].collectionGroup // empty' "$INDEXES_JSON")
    [ -z "$CG" ] && break
    QS=$(jq -r --argjson n "$N" '.indexes[$n].queryScope // "COLLECTION"' "$INDEXES_JSON" | tr '[:upper:]' '[:lower:]')
    # Build one --field-config=... per field. Vector: vector-config={dimension=N,flat} (flat inside braces)
    FCONFIG_ARGS=()
    while IFS= read -r line; do
      [ -z "$line" ] && continue
      FCONFIG_ARGS+=(--field-config="$line")
    done < <(jq -r --argjson n "$N" '
      .indexes[$n].fields[]
      | if .vectorConfig then
          "field-path=\(.fieldPath),vector-config={dimension=\(.vectorConfig.dimension),flat}"
        else
          "field-path=\(.fieldPath),order=\(.order | ascii_downcase)"
        end
    ' "$INDEXES_JSON")
    TMPF=$(mktemp)
    if gcloud firestore indexes composite create \
      --project="$PROJECT" \
      --collection-group="$CG" \
      --query-scope="$QS" \
      "${FCONFIG_ARGS[@]}" \
      --async \
      --quiet 2>"$TMPF.err"; then
      echo -e "  Created (building): $CG"
      CREATED=$((CREATED + 1))
    else
      ERR=$(cat "$TMPF.err" 2>/dev/null)
      if echo "$ERR" | grep -qi "already exists\|RESOURCE_ALREADY_EXISTS"; then
        echo -e "  Exists: $CG"
      else
        echo -e "${YELLOW}  Failed: $CG${NC}"
        echo "$ERR" | head -5
      fi
    fi
    rm -f "$TMPF" "$TMPF.err"
    N=$((N + 1))
  done
  echo -e "${GREEN}Processed $N index definitions ($CREATED created).${NC}"
else
  echo -e "${YELLOW}Install Firebase CLI (firebase-tools) or jq to deploy indexes from firestore.indexes.json.${NC}"
  echo -e "${YELLOW}Otherwise create indexes manually in the GCP Console or via gcloud.${NC}"
fi
echo ""

# Parse arguments
MODE="${1:-container}"

case "$MODE" in
  container|fast)
    echo -e "${YELLOW}Running tests...${NC}"
    if ! go test ./...; then
      echo -e "${RED}Tests failed. Aborting deployment.${NC}"
      exit 1
    fi
    echo -e "${GREEN}Tests passed!${NC}"
    echo ""

    echo -e "${YELLOW}Cross-compiling server for Linux...${NC}"
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o server ./cmd/server

    # Copy ca-certificates for the container
    if [ ! -f ca-certificates.crt ]; then
      echo -e "${YELLOW}Extracting CA certificates...${NC}"
      # Extract from system or download
      if [ -f /etc/ssl/certs/ca-certificates.crt ]; then
        cp /etc/ssl/certs/ca-certificates.crt .
      elif [ -f /etc/ssl/cert.pem ]; then
        cp /etc/ssl/cert.pem ca-certificates.crt
      else
        # macOS: extract from system keychain
        security find-certificate -a -p /System/Library/Keychains/SystemRootCertificates.keychain > ca-certificates.crt 2>/dev/null || \
        curl -sS https://curl.se/ca/cacert.pem -o ca-certificates.crt
      fi
    fi

    echo -e "${YELLOW}Building container image...${NC}"
    docker build --platform linux/amd64 -t "$IMAGE:latest" .

    echo -e "${YELLOW}Pushing to registry...${NC}"
    docker push "$IMAGE:latest"

    # Clean up build artifacts
    rm -f server

    # If the service already exists, we know its URL and can set JOT_API_URL/SYNC_GDOC_URL in one deploy.
    EXISTING_URL=$(gcloud run services describe "$FUNCTION_NAME" --region="$REGION" --format='value(status.url)' 2>/dev/null) || true
    ENV_VARS="FUNCTION_TARGET=JotAPI,GOOGLE_CLOUD_PROJECT=$PROJECT,LOG_LEVEL=debug,DREAMER_MODEL=gemini-2.5-flash"
    if [ -n "$EXISTING_URL" ]; then
      ENV_VARS="$ENV_VARS,JOT_API_URL=${EXISTING_URL},SYNC_GDOC_URL=${EXISTING_URL}/sync"
    fi

    echo -e "${YELLOW}Deploying container to Cloud Run...${NC}"
    time gcloud beta run deploy "$FUNCTION_NAME" \
      --region="$REGION" \
      --image="$IMAGE:latest" \
      --cpu="$CPU" \
      --memory="$MEMORY" \
      --concurrency="$CONCURRENCY" \
      --timeout="$QUERY_TIMEOUT" \
      --max-instances=1 \
      --allow-unauthenticated \
      --execution-environment=gen1 \
      --update-env-vars="$ENV_VARS" \
      --quiet

    DEPLOYED_BASE_URL=$(gcloud run services describe "$FUNCTION_NAME" --region="$REGION" --format='value(status.url)' 2>/dev/null)
    # First deploy: service had no URL before; set JOT_API_URL and SYNC_GDOC_URL now.
    if [ -n "$DEPLOYED_BASE_URL" ] && [ -z "$EXISTING_URL" ]; then
      gcloud run services update "$FUNCTION_NAME" --region="$REGION" \
        --update-env-vars="JOT_API_URL=${DEPLOYED_BASE_URL},SYNC_GDOC_URL=${DEPLOYED_BASE_URL}/sync" \
        --quiet
    fi
    ;;

  source|slow)
    echo -e "${YELLOW}Running tests...${NC}"
    if ! go test ./...; then
      echo -e "${RED}Tests failed. Aborting deployment.${NC}"
      exit 1
    fi
    echo -e "${GREEN}Tests passed!${NC}"
    echo ""

    echo -e "${YELLOW}Deploying from source (slower)...${NC}"
    gcloud functions deploy "$FUNCTION_NAME" \
      --gen2 \
      --runtime=go126 \
      --region="$REGION" \
      --source="." \
      --entry-point=JotAPI \
      --trigger-http \
      --allow-unauthenticated \
      --cpu="$CPU" \
      --memory="$MEMORY" \
      --concurrency="$CONCURRENCY" \
      --timeout="${QUERY_TIMEOUT}s" \
      --max-instances=1 \
      --set-env-vars="FUNCTION_TARGET=JotAPI,GOOGLE_CLOUD_PROJECT=$PROJECT,LOG_LEVEL=debug,JOT_API_URL=https://${REGION}-${PROJECT}.cloudfunctions.net/${FUNCTION_NAME},SYNC_GDOC_URL=https://${REGION}-${PROJECT}.cloudfunctions.net/${FUNCTION_NAME}/sync,DREAMER_MODEL=gemini-2.5-flash" \
      --quiet
    ;;

  *)
    echo "Usage: $0 [container|source]"
    echo "  container  - Build and deploy container image (fast, default)"
    echo "  source     - Deploy from source via Cloud Build (slow)"
    exit 1
    ;;
esac

# Use Cloud Run URL when we just deployed a container; otherwise Cloud Functions URL for source deploy.
if [ -n "${DEPLOYED_BASE_URL:-}" ]; then
  BASE_URL="$DEPLOYED_BASE_URL"
else
  BASE_URL="https://${REGION}-${PROJECT}.cloudfunctions.net/${FUNCTION_NAME}"
fi

echo ""
echo -e "${GREEN}Deployment successful!${NC}"
echo ""

# Reset Drive watch so the Google Doc webhook subscription is fresh (expires in 7 days)
echo -e "${YELLOW}Resetting Drive watch (Google Doc)...${NC}"
export GDOC_WEBHOOK_URL="${BASE_URL}/webhook"
DRIVE_WATCH_VENV=".venv-drive-watch"
if [ ! -d "$DRIVE_WATCH_VENV" ]; then
  echo "  Creating venv for Drive watch ($DRIVE_WATCH_VENV)..."
  python3 -m venv "$DRIVE_WATCH_VENV"
fi
"$DRIVE_WATCH_VENV/bin/pip" install -q -r requirements-drive-watch.txt
"$DRIVE_WATCH_VENV/bin/python" setup_drive_watch.py stop 2>/dev/null || true
if "$DRIVE_WATCH_VENV/bin/python" setup_drive_watch.py; then
  echo -e "${GREEN}Drive watch reset.${NC}"
else
  echo -e "${YELLOW}Drive watch skipped or failed (set DOCUMENT_ID and SERVICE_ACCOUNT_FILE in .env to enable).${NC}"
fi
echo ""

echo "Base URL: $BASE_URL"
echo ""
echo "Endpoints:"
echo "  GET  $BASE_URL/health"
echo "  POST $BASE_URL/log"
echo "  POST $BASE_URL/query"
echo "  POST $BASE_URL/plan"
echo "  GET  $BASE_URL/entries"
echo "  POST $BASE_URL/sync"
echo "  POST $BASE_URL/dream"
echo "  POST $BASE_URL/janitor"
echo "  POST $BASE_URL/webhook"
echo "  POST $BASE_URL/sms"
echo ""
echo "Test:"
echo "  curl $BASE_URL/health"
