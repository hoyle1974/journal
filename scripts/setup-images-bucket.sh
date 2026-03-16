#!/bin/bash
#
# Create GCS bucket for Jot journal image uploads and grant the Cloud Run
# service account write access. Sets up JOT_IMAGES_BUCKET.
#
# Usage: ./scripts/setup-images-bucket.sh <dev|prod> [--yes]
# Environment must be explicit (dev or prod). Use --yes to skip confirmation.
#
set -e

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

SKIP_CONFIRM=""
if [[ "${2:-}" == "--yes" ]]; then
  SKIP_CONFIRM=1
fi

if [[ -z "$SKIP_CONFIRM" ]]; then
  source "$REPO_ROOT/scripts/lib/env-confirm.sh"
  require_env_and_confirm "$1"
  shift
else
  case "${1:-}" in
    dev)  ENV_FILE=".env" ;;
    prod) ENV_FILE=".env.prod" ;;
    *) echo "Usage: $0 <dev|prod> [--yes]"; exit 1 ;;
  esac
  ENV_TARGET="$1"
  shift
fi

if [[ -f "$ENV_FILE" ]]; then
  set -a
  source "$ENV_FILE"
  set +a
else
  echo "Error: $ENV_FILE not found. Create it with GOOGLE_CLOUD_PROJECT=your-project-id"
  exit 1
fi

PROJECT="${GOOGLE_CLOUD_PROJECT:?Set GOOGLE_CLOUD_PROJECT in $ENV_FILE}"
REGION="${REGION:-us-central1}"

# Bucket name: lowercase, globally unique (GCS requirement). Prefer env override.
BUCKET_NAME="${JOT_IMAGES_BUCKET:-$(echo "${PROJECT}-jot-images" | tr '[:upper:]' '[:lower:]')}"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

echo -e "${YELLOW}Jot images bucket setup${NC}"
echo "Project: $PROJECT"
echo "Bucket:  $BUCKET_NAME"
echo ""

if ! command -v gcloud &> /dev/null; then
  echo -e "${RED}Error: gcloud CLI not found${NC}"
  exit 1
fi

gcloud config set project "$PROJECT" 2>/dev/null

echo -e "${CYAN}Enabling Cloud Storage API...${NC}"
gcloud services enable storage.googleapis.com --quiet
echo -e "${GREEN}API enabled${NC}"
echo ""

if gcloud storage buckets describe "gs://${BUCKET_NAME}" 2>/dev/null; then
  echo -e "${YELLOW}Bucket gs://${BUCKET_NAME} already exists${NC}"
else
  echo -e "${CYAN}Creating bucket gs://${BUCKET_NAME} (location: $REGION)...${NC}"
  gcloud storage buckets create "gs://${BUCKET_NAME}" --location="$REGION"
  echo -e "${GREEN}Bucket created${NC}"
fi
echo ""

echo -e "${CYAN}Granting Cloud Run service account write access to the bucket...${NC}"
PROJECT_NUM=$(gcloud projects describe "$PROJECT" --format='value(projectNumber)' 2>/dev/null) || true
SERVICE_ACCOUNT="${CLOUD_RUN_SERVICE_ACCOUNT:-${PROJECT_NUM:?Could not get project number}-compute@developer.gserviceaccount.com}"

gcloud storage buckets add-iam-policy-binding "gs://${BUCKET_NAME}" \
  --member="serviceAccount:${SERVICE_ACCOUNT}" \
  --role="roles/storage.objectCreator" \
  --quiet

echo -e "${GREEN}Access granted to ${SERVICE_ACCOUNT}${NC}"
echo ""

echo -e "${GREEN}Images bucket setup complete.${NC}"
echo ""
echo -e "${CYAN}Add to $ENV_FILE and redeploy:${NC}"
echo "JOT_IMAGES_BUCKET=$BUCKET_NAME"
echo ""
echo -e "${YELLOW}Optional: store bucket name in Secret Manager and reference from Cloud Run (e.g. JOT_IMAGES_BUCKET secret).${NC}"
echo ""
