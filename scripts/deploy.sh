#!/bin/bash
#
# Deploy Jot Cloud Function to GCP (Go version)
# Usage: ./scripts/deploy.sh [dev|prod] [container|source]
#
set -e

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

# 1. Determine environment and load variables
ENV_TARGET="dev"
if [[ "$1" == "prod" ]] || [[ "$1" == "dev" ]]; then
    ENV_TARGET="$1"
    shift # Remove the env arg so $1 becomes 'container' or 'source'
fi

ENV_FILE=".env"
if [ "$ENV_TARGET" == "prod" ]; then
    ENV_FILE=".env.prod"
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

# Resource configuration
QUERY_TIMEOUT=$(grep 'QuerySeconds = ' internal/timeout/timeout.go | sed 's/.*= \([0-9]*\).*/\1/')
CPU="1"
MEMORY="128Mi"
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
    LDFLAGS="-s -w -X github.com/jackstrohm/jot/pkg/infra.Version=$VERSION -X github.com/jackstrohm/jot/pkg/infra.Commit=$COMMIT"
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="$LDFLAGS" -o server ./cmd/server

    echo -e "${YELLOW}Building and pushing container...${NC}"
    docker build --platform linux/amd64 -t "$IMAGE:latest" .
    docker push "$IMAGE:latest"
    rm -f server

    EXISTING_URL=$(gcloud run services describe "$FUNCTION_NAME" --region="$REGION" --format='value(status.url)' 2>/dev/null) || true
    ENV_VARS="FUNCTION_TARGET=JotAPI,GOOGLE_CLOUD_PROJECT=$PROJECT,LOG_LEVEL=debug,DREAMER_MODEL=gemini-2.5-flash"
    if [ -n "$EXISTING_URL" ]; then
      ENV_VARS="$ENV_VARS,JOT_API_URL=${EXISTING_URL},SYNC_GDOC_URL=${EXISTING_URL}/sync"
    fi

    echo -e "${YELLOW}Deploying to Cloud Run...${NC}"
    gcloud beta run deploy "$FUNCTION_NAME" \
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

    DEPLOYED_BASE_URL=$(gcloud run services describe "$FUNCTION_NAME" --region="$REGION" --format='value(status.url)' --project="$PROJECT")

    echo "Ensuring Cloud Run environment has JOT_API_URL..."
    gcloud run services update "$FUNCTION_NAME" \
      --region="$REGION" \
      --project="$PROJECT" \
      --update-env-vars="JOT_API_URL=${DEPLOYED_BASE_URL},SYNC_GDOC_URL=${DEPLOYED_BASE_URL}/sync" \
      --quiet
    ;;
  *)
    echo "Unsupported mode: $MODE (use container)"
    exit 1
    ;;
esac

echo -e "${GREEN}Deployment to $ENV_TARGET successful!${NC}"
