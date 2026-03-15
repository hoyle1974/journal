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
    if [ -n "${TELEGRAM_BOT_TOKEN:-}" ]; then
      ENV_VARS="$ENV_VARS,TELEGRAM_BOT_TOKEN=$TELEGRAM_BOT_TOKEN"
    fi
    if [ -n "${TELEGRAM_SECRET_TOKEN:-}" ]; then
      ENV_VARS="$ENV_VARS,TELEGRAM_SECRET_TOKEN=$TELEGRAM_SECRET_TOKEN"
    fi
    if [ -n "${ALLOWED_TELEGRAM_USER_ID:-}" ]; then
      ENV_VARS="$ENV_VARS,ALLOWED_TELEGRAM_USER_ID=$ALLOWED_TELEGRAM_USER_ID"
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
    DEPLOY_OUT="$REPO_ROOT/scripts/.deploy-out.txt"
    set +e
    gcloud run services replace "$RUN_YAML" --region="$REGION" --project="$PROJECT" --verbosity=debug 2>&1 | tee "$DEPLOY_OUT"
    REPLACE_RC=$?
    set -e
    [ $REPLACE_RC -ne 0 ] && exit $REPLACE_RC

    # Allow unauthenticated invocations (replace does not set IAM)
    gcloud run services add-iam-policy-binding "$FUNCTION_NAME" --region="$REGION" --member="allUsers" --role="roles/run.invoker" 2>/dev/null || true

    # Use the URL printed by deploy (e.g. https://SERVICE-NUM.REGION.run.app); describe can return a different host (e.g. uc.a.run.app) that may 404 for Telegram.
    # Prefer the last run.app URL in replace output (the "URL: https://..." line); fall back to describe.
    DEPLOYED_BASE_URL=$(grep -oE 'https://[a-zA-Z0-9.-]+\.run\.app' "$DEPLOY_OUT" 2>/dev/null | tail -1)
    if [ -z "$DEPLOYED_BASE_URL" ]; then
      DEPLOYED_BASE_URL=$(gcloud run services describe "$FUNCTION_NAME" --region="$REGION" --format='value(status.url)' --project="$PROJECT")
      echo -e "${YELLOW}Deploy: using URL from describe (could not parse from replace output).${NC}"
    fi
    rm -f "$DEPLOY_OUT"
    ;;
  *)
    echo "Unsupported mode: $MODE (use container)"
    exit 1
    ;;
esac

echo -e "${GREEN}Deployment to $ENV_TARGET successful!${NC}"
echo ""

# Set Telegram webhook if bot token is available (from env file)
if [ -n "${TELEGRAM_BOT_TOKEN:-}" ] && [ -n "${DEPLOYED_BASE_URL:-}" ]; then
  TELEGRAM_WEBHOOK_URL="${DEPLOYED_BASE_URL%/}/telegram"
  echo -e "${YELLOW}Telegram webhook:${NC} DEPLOYED_BASE_URL=$DEPLOYED_BASE_URL"
  echo -e "${YELLOW}Telegram webhook:${NC} URL=$TELEGRAM_WEBHOOK_URL"

  # Verify bot token with getMe (Telegram returns 404 for invalid token)
  GETME=$(curl -s -w "\n%{http_code}" "https://api.telegram.org/bot${TELEGRAM_BOT_TOKEN}/getMe")
  GETME_CODE=$(echo "$GETME" | tail -n1)
  GETME_BODY=$(echo "$GETME" | sed '$d')
  if [ "$GETME_CODE" != "200" ] || ! echo "$GETME_BODY" | grep -q '"ok":true'; then
    echo -e "${RED}Telegram bot token invalid (getMe returned HTTP $GETME_CODE). Fix TELEGRAM_BOT_TOKEN in $ENV_FILE — use the token from @BotFather, not the secret_token.${NC}"
    echo -e "${YELLOW}Telegram webhook:${NC} getMe response=$GETME_BODY"
  else
    if [ -n "${TELEGRAM_SECRET_TOKEN:-}" ]; then
      echo -e "${YELLOW}Telegram webhook:${NC} secret_token is set (will be sent in payload)"
      PAYLOAD=$(printf '{"url":"%s","secret_token":"%s"}' "$TELEGRAM_WEBHOOK_URL" "$TELEGRAM_SECRET_TOKEN")
    else
      echo -e "${YELLOW}Telegram webhook:${NC} no secret_token (optional)"
      PAYLOAD=$(printf '{"url":"%s"}' "$TELEGRAM_WEBHOOK_URL")
    fi
    echo -e "${YELLOW}Telegram webhook:${NC} calling setWebhook ..."
    RESP=$(curl -s -w "\n%{http_code}" -X POST "https://api.telegram.org/bot${TELEGRAM_BOT_TOKEN}/setWebhook" \
      -H "Content-Type: application/json" \
      -d "$PAYLOAD")
    HTTP_CODE=$(echo "$RESP" | tail -n1)
    BODY=$(echo "$RESP" | sed '$d')
    echo -e "${YELLOW}Telegram webhook:${NC} HTTP status=$HTTP_CODE response=$BODY"
    if echo "$BODY" | grep -q '"ok":true'; then
      echo -e "${GREEN}Telegram webhook set successfully.${NC}"
    else
      echo -e "${RED}Telegram setWebhook failed (HTTP $HTTP_CODE). Full response: $BODY${NC}"
    fi
  fi
  echo ""
elif [ -n "${DEPLOYED_BASE_URL:-}" ]; then
  echo -e "${YELLOW}Tip: Add TELEGRAM_BOT_TOKEN to $ENV_FILE and re-deploy to auto-set the Telegram webhook, or run setWebhook manually (see docs/telegram-setup.md).${NC}"
  echo ""
fi
