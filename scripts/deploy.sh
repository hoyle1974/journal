#!/bin/bash
#
# Deploy Jot Cloud Function to GCP (Go version)
# Usage: ./scripts/deploy.sh <dev|prod> [container|source]
# Environment must be explicit (no default). Script will confirm before continuing.
#
set -e

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"
source "$REPO_ROOT/scripts/lib/env-confirm.sh"
require_env_and_confirm "$1" " [container|source]"
shift

if [ "$ENV_TARGET" == "prod" ]; then
  echo -e "\033[1;33mTargeting PRODUCTION environment (.env.prod)\033[0m"
else
  echo -e "\033[1;33mTargeting DEVELOPMENT environment (.env)\033[0m"
fi

if [ -f "$ENV_FILE" ]; then
  set -a
  source "$ENV_FILE"
  set +a
else
  echo -e "\033[0;31mError: $ENV_FILE not found. Create it with your project settings.\033[0m"
  exit 1
fi

PROJECT="${GOOGLE_CLOUD_PROJECT:?Set GOOGLE_CLOUD_PROJECT in $ENV_FILE}"
REGION="us-central1"
FUNCTION_NAME="jot-api-go"
IMAGE="$REGION-docker.pkg.dev/$PROJECT/jot/$FUNCTION_NAME"

# Resource configuration (use max of query/dream so POST /dream can run up to DreamSeconds)
QUERY_TIMEOUT=$(grep 'QuerySeconds = ' internal/timeout/timeout.go | sed 's/.*= \([0-9]*\).*/\1/')
DREAM_TIMEOUT=$(grep 'DreamSeconds = ' internal/timeout/timeout.go | sed 's/.*= \([0-9]*\).*/\1/')
RUN_TIMEOUT=$QUERY_TIMEOUT
if [ -n "$DREAM_TIMEOUT" ] && [ "$DREAM_TIMEOUT" -gt "$RUN_TIMEOUT" ] 2>/dev/null; then
  RUN_TIMEOUT=$DREAM_TIMEOUT
fi
CPU="1"
# gen2 (required for Prometheus sidecar) has a minimum of 512 Mi total memory
MEMORY="512Mi"
CONCURRENCY="80"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo "Project: $PROJECT"
echo "Region: $REGION"
echo ""

# Rebuild CLI binary
echo -e "${YELLOW}Rebuilding CLI binary...${NC}"
go build -o jot ./cmd/jot
echo -e "${GREEN}CLI binary rebuilt: ./jot${NC}"
echo ""

if ! command -v gcloud &> /dev/null; then
    echo -e "${RED}Error: gcloud CLI not found.${NC}"
    exit 1
fi

gcloud config set project $PROJECT 2>/dev/null

# Deploy Firestore indexes
echo -e "${YELLOW}Deploying Firestore indexes...${NC}"
if command -v firebase &> /dev/null; then
  firebase use "$PROJECT" 2>/dev/null || true
  firebase deploy --only firestore:indexes --project "$PROJECT" --non-interactive
else
  echo -e "${YELLOW}Firebase CLI not found, skipping index deploy. Ensure indexes match firestore.indexes.json.${NC}"
fi
echo ""

# Parse deployment mode (default to container)
MODE="${1:-container}"

case "$MODE" in
  container|fast)
    echo -e "${YELLOW}Running tests...${NC}"
    go test ./...
    
    echo -e "${YELLOW}Cross-compiling server for Linux...${NC}"
    VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
    COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "none")
    DEPLOY_REV="${COMMIT}-$(date -u +%s)"
    IMAGE_TAG="sha-${COMMIT}"
    LDFLAGS="-s -w -X github.com/jackstrohm/jot/internal/infra.Version=$VERSION -X github.com/jackstrohm/jot/internal/infra.Commit=$COMMIT"
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="$LDFLAGS" -o server ./cmd/server

    echo -e "${YELLOW}Building and pushing container...${NC}"
    docker build --platform linux/amd64 -t "$IMAGE:$IMAGE_TAG" -t "$IMAGE:latest" .
    docker push "$IMAGE:$IMAGE_TAG"
    docker push "$IMAGE:latest"
    rm -f server

    EXISTING_URL=$(gcloud run services describe "$FUNCTION_NAME" --region="$REGION" --format='value(status.url)' 2>/dev/null) || true
    ENV_VARS="FUNCTION_TARGET=JotAPI,GOOGLE_CLOUD_PROJECT=$PROJECT,LOG_LEVEL=debug,DREAMER_MODEL=gemini-2.5-flash"
    if [ -n "$EXISTING_URL" ]; then
      ENV_VARS="$ENV_VARS,JOT_API_URL=${EXISTING_URL},SYNC_GDOC_URL=${EXISTING_URL}/sync"
    fi

    # Build env block for YAML (Prometheus sidecar deploy)
    ENV_BLOCK_FILE="$REPO_ROOT/scripts/.env-block.yaml"
    : > "$ENV_BLOCK_FILE"
    while IFS= read -r pair; do
      [ -z "$pair" ] && continue
      key="${pair%%=*}"
      val="${pair#*=}"
      printf '        - name: %s\n          value: "%s"\n' "$key" "$val" >> "$ENV_BLOCK_FILE"
    done < <(echo "$ENV_VARS" | tr ',' '\n')

    # Generate Cloud Run service YAML (app + GMP Prometheus sidecar).
    # Use unique image tag (sha-COMMIT) so Cloud Run always creates a new revision and runs the new code.
    RUN_YAML="$REPO_ROOT/scripts/cloud-run-service.yaml"
    sed -e "s|__SERVICE_NAME__|$FUNCTION_NAME|g" \
        -e "s|__REGION__|$REGION|g" \
        -e "s|__IMAGE__|$IMAGE:$IMAGE_TAG|g" \
        -e "s|__DEPLOY_REV__|$DEPLOY_REV|g" \
        -e "s|__CPU__|$CPU|g" \
        -e "s|__MEMORY__|$MEMORY|g" \
        -e "s|__CONCURRENCY__|$CONCURRENCY|g" \
        -e "s|__TIMEOUT__|$RUN_TIMEOUT|g" \
        -e "s|__MAX_INSTANCES__|1|g" \
        "$REPO_ROOT/scripts/cloud-run-service.yaml.template" | \
    awk -v envfile="$ENV_BLOCK_FILE" '
      /__ENV_BLOCK__/ {
        while ((getline line < envfile) > 0) print line
        close(envfile)
        next
      }
      { print }
    ' > "$RUN_YAML"
    rm -f "$ENV_BLOCK_FILE"

    echo -e "${YELLOW}Deploying to Cloud Run (with Prometheus sidecar for /metrics)...${NC}"
    gcloud run services replace "$RUN_YAML" --region="$REGION" --project="$PROJECT" --verbosity=debug

    # Allow unauthenticated invocations (replace does not set IAM)
    gcloud run services add-iam-policy-binding "$FUNCTION_NAME" --region="$REGION" --member="allUsers" --role="roles/run.invoker" 2>/dev/null || true

    DEPLOYED_BASE_URL=$(gcloud run services describe "$FUNCTION_NAME" --region="$REGION" --format='value(status.url)' --project="$PROJECT")
    ;;
  *)
    echo "Unsupported mode: $MODE (use container)"
    exit 1
    ;;
esac

echo -e "${GREEN}Deployment to $ENV_TARGET successful!${NC}"
