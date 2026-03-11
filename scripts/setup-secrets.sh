#!/bin/bash
#
# Jot secrets setup (Secret Manager, API keys, Twilio, IAM).
# Usage: ./scripts/setup-secrets.sh <dev|prod>
# Environment must be explicit (no default). Script will confirm before continuing.
#
set -e

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"
source "$REPO_ROOT/scripts/lib/env-confirm.sh"
require_env_and_confirm "$1"
shift

# LOAD TARGET ENV ONLY
if [ -f "$ENV_FILE" ]; then
  echo -e "Targeting $ENV_TARGET using $ENV_FILE"
  set -a
  source "$ENV_FILE"
  set +a
else
  if [ "$ENV_TARGET" == "prod" ]; then
    echo "Error: .env.prod not found. Create it with GOOGLE_CLOUD_PROJECT=your-prod-id"
    exit 1
  fi
fi

PROJECT="${GOOGLE_CLOUD_PROJECT:?Set GOOGLE_CLOUD_PROJECT}"
REGION="us-central1"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

echo -e "${YELLOW}Jot Secrets Setup${NC}"
echo "Project: $PROJECT"
echo ""

# Check gcloud
if ! command -v gcloud &> /dev/null; then
    echo -e "${RED}Error: gcloud CLI not found${NC}"
    exit 1
fi

gcloud config set project $PROJECT 2>/dev/null

# Enable Secret Manager API
echo -e "${CYAN}Enabling Secret Manager API...${NC}"
gcloud services enable secretmanager.googleapis.com --quiet
echo -e "${GREEN}API enabled${NC}"
echo ""

# Function to create or update a secret
create_secret() {
    local secret_name=$1
    local secret_value=$2

    # Check if secret exists
    if gcloud secrets describe $secret_name 2>/dev/null; then
        echo -e "${YELLOW}Secret $secret_name exists. Adding new version...${NC}"
        echo -n "$secret_value" | gcloud secrets versions add $secret_name --data-file=-
    else
        echo -e "${CYAN}Creating secret $secret_name...${NC}"
        echo -n "$secret_value" | gcloud secrets create $secret_name --data-file=- --replication-policy="automatic"
    fi
    echo -e "${GREEN}Secret $secret_name configured${NC}"
}

# Generate a random API key if not provided
generate_api_key() {
    openssl rand -base64 32 | tr -d '/+=' | head -c 32
}

# Get GEMINI_API_KEY from .env (already sourced above) or prompt
if [ -z "$GEMINI_API_KEY" ]; then
    echo -e "${YELLOW}Enter your GEMINI_API_KEY:${NC}"
    read -s GEMINI_API_KEY
    echo ""
fi

if [ -z "$GEMINI_API_KEY" ]; then
    echo -e "${RED}Error: GEMINI_API_KEY is required${NC}"
    exit 1
fi

# Create/update GEMINI_API_KEY secret
create_secret "GEMINI_API_KEY" "$GEMINI_API_KEY"
echo ""

# Generate or use existing JOT_API_KEY
if [ -z "$JOT_API_KEY" ]; then
    JOT_API_KEY=$(generate_api_key)
    echo -e "${CYAN}Generated new JOT_API_KEY${NC}"
fi

# Create/update JOT_API_KEY secret
create_secret "JOT_API_KEY" "$JOT_API_KEY"
echo ""

# Twilio secrets (optional - skip if not using SMS)
if [ -z "$TWILIO_ACCOUNT_SID" ] || [ -z "$TWILIO_AUTH_TOKEN" ]; then
    echo -e "${YELLOW}Twilio (optional): Enter TWILIO_ACCOUNT_SID or press Enter to skip:${NC}"
    read -r twilio_sid
    if [ -n "$twilio_sid" ]; then
        TWILIO_ACCOUNT_SID="$twilio_sid"
        echo -e "${YELLOW}Enter TWILIO_AUTH_TOKEN:${NC}"
        read -s TWILIO_AUTH_TOKEN
        echo ""
    fi
fi
if [ -n "$TWILIO_ACCOUNT_SID" ] && [ -n "$TWILIO_AUTH_TOKEN" ]; then
    create_secret "TWILIO_ACCOUNT_SID" "$TWILIO_ACCOUNT_SID"
    create_secret "TWILIO_AUTH_TOKEN" "$TWILIO_AUTH_TOKEN"
    create_secret "TWILIO_PHONE_NUMBER" "${TWILIO_PHONE_NUMBER:?Set TWILIO_PHONE_NUMBER for Twilio SMS}"
    create_secret "ALLOWED_PHONE_NUMBER" "${ALLOWED_PHONE_NUMBER:?Set ALLOWED_PHONE_NUMBER for Twilio SMS}"
    echo ""
fi

# Grant Cloud Run service account access to secrets
echo -e "${CYAN}Granting Cloud Run access to secrets...${NC}"
# Default Compute Engine service account for the project (derive from project number or set CLOUD_RUN_SERVICE_ACCOUNT)
PROJECT_NUM=$(gcloud projects describe "$PROJECT" --format='value(projectNumber)' 2>/dev/null) || true
SERVICE_ACCOUNT="${CLOUD_RUN_SERVICE_ACCOUNT:-${PROJECT_NUM:?Could not get project number}-compute@developer.gserviceaccount.com}"

gcloud secrets add-iam-policy-binding GEMINI_API_KEY \
    --member="serviceAccount:$SERVICE_ACCOUNT" \
    --role="roles/secretmanager.secretAccessor" \
    --quiet 2>/dev/null || true

gcloud secrets add-iam-policy-binding JOT_API_KEY \
    --member="serviceAccount:$SERVICE_ACCOUNT" \
    --role="roles/secretmanager.secretAccessor" \
    --quiet 2>/dev/null || true

for secret in TWILIO_ACCOUNT_SID TWILIO_AUTH_TOKEN TWILIO_PHONE_NUMBER ALLOWED_PHONE_NUMBER; do
    if gcloud secrets describe $secret 2>/dev/null; then
        gcloud secrets add-iam-policy-binding $secret \
            --member="serviceAccount:$SERVICE_ACCOUNT" \
            --role="roles/secretmanager.secretAccessor" \
            --quiet 2>/dev/null || true
    fi
done

echo -e "${GREEN}Access granted${NC}"
echo ""

echo -e "${GREEN}Secrets setup complete!${NC}"
echo ""
echo -e "${YELLOW}Your JOT_API_KEY:${NC}"
echo "$JOT_API_KEY"
echo ""
echo -e "${CYAN}Add this to your local .env file:${NC}"
echo "JOT_API_KEY=$JOT_API_KEY"
echo ""
echo -e "${YELLOW}Next steps:${NC}"
echo "1. Add JOT_API_KEY to your .env file"
echo "2. For Twilio SMS: add TWILIO_ACCOUNT_SID, TWILIO_AUTH_TOKEN, TWILIO_PHONE_NUMBER, ALLOWED_PHONE_NUMBER to .env and re-run this script"
echo "3. Redeploy: ./scripts/deploy.sh <dev|prod>"
echo "4. Test: curl -H 'X-API-Key: \$JOT_API_KEY' https://...cloudrun.app/..."
echo ""
