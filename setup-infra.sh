#!/bin/bash
#
# Set up GCP infrastructure for Jot Cloud Functions
#
# This script creates:
# - Cloud Tasks queue for debounced sync
# - Cloud Scheduler jobs for daily dream, weekly janitor
# - Sets up Drive Watch for Google Doc changes (optional)
#
set -e

PROJECT="${GOOGLE_CLOUD_PROJECT:?Set GOOGLE_CLOUD_PROJECT (e.g. export GOOGLE_CLOUD_PROJECT=your-project-id)}"
REGION="us-central1"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

echo -e "${YELLOW}Jot Infrastructure Setup${NC}"
echo "Project: $PROJECT"
echo "Region: $REGION"
echo ""

# Check gcloud
if ! command -v gcloud &> /dev/null; then
    echo -e "${RED}Error: gcloud CLI not found${NC}"
    exit 1
fi

gcloud config set project $PROJECT 2>/dev/null

# =============================================================================
# 1. Enable required APIs
# =============================================================================
echo -e "${CYAN}Enabling required APIs...${NC}"
gcloud services enable \
    cloudfunctions.googleapis.com \
    cloudtasks.googleapis.com \
    cloudscheduler.googleapis.com \
    firestore.googleapis.com \
    docs.googleapis.com \
    drive.googleapis.com \
    --quiet

echo -e "${GREEN}APIs enabled${NC}"
echo ""

# =============================================================================
# 2. Create Cloud Tasks Queue
# =============================================================================
echo -e "${CYAN}Creating Cloud Tasks queue...${NC}"

QUEUE_NAME="jot-sync-queue"

# Check if queue exists
if gcloud tasks queues describe $QUEUE_NAME --location=$REGION 2>/dev/null; then
    echo -e "${YELLOW}Queue $QUEUE_NAME already exists${NC}"
else
    gcloud tasks queues create $QUEUE_NAME \
        --location=$REGION \
        --max-dispatches-per-second=1 \
        --max-concurrent-dispatches=1 \
        --quiet

    echo -e "${GREEN}Queue $QUEUE_NAME created${NC}"
fi
echo ""

# =============================================================================
# 3. Create Cloud Scheduler Jobs for Dream (daily) and Janitor (weekly)
# =============================================================================
echo -e "${CYAN}Creating Cloud Scheduler jobs...${NC}"

BASE_URL="https://${REGION}-${PROJECT}.cloudfunctions.net/jot-api-go"

# Dream: daily at 2 AM UTC
DREAM_JOB="jot-daily-dream"
if gcloud scheduler jobs describe $DREAM_JOB --location=$REGION 2>/dev/null; then
    echo -e "${YELLOW}Scheduler job $DREAM_JOB already exists. Updating...${NC}"
    gcloud scheduler jobs update http $DREAM_JOB \
        --location=$REGION \
        --schedule="0 2 * * *" \
        --uri="${BASE_URL}/dream" \
        --http-method=POST \
        --quiet
else
    gcloud scheduler jobs create http $DREAM_JOB \
        --location=$REGION \
        --schedule="0 2 * * *" \
        --uri="${BASE_URL}/dream" \
        --http-method=POST \
        --time-zone="UTC" \
        --quiet
    echo -e "${GREEN}Dream job created: runs daily at 2 AM UTC${NC}"
fi

# Janitor: weekly Sunday 3 AM UTC
JANITOR_JOB="jot-weekly-janitor"
if gcloud scheduler jobs describe $JANITOR_JOB --location=$REGION 2>/dev/null; then
    echo -e "${YELLOW}Scheduler job $JANITOR_JOB already exists. Updating...${NC}"
    gcloud scheduler jobs update http $JANITOR_JOB \
        --location=$REGION \
        --schedule="0 3 * * 0" \
        --uri="${BASE_URL}/janitor" \
        --http-method=POST \
        --quiet
else
    gcloud scheduler jobs create http $JANITOR_JOB \
        --location=$REGION \
        --schedule="0 3 * * 0" \
        --uri="${BASE_URL}/janitor" \
        --http-method=POST \
        --time-zone="UTC" \
        --quiet
    echo -e "${GREEN}Janitor job created: runs weekly Sunday 3 AM UTC${NC}"
fi
echo ""

# =============================================================================
# 4. Output Configuration
# =============================================================================
echo -e "${GREEN}Infrastructure setup complete!${NC}"
echo ""
echo "Configuration to add to your Cloud Functions environment:"
echo ""
echo "  CLOUD_TASKS_QUEUE=$QUEUE_NAME"
echo "  CLOUD_TASKS_LOCATION=$REGION"
echo "  SYNC_GDOC_URL=https://${REGION}-${PROJECT}.cloudfunctions.net/jot-api-go/sync"
echo ""
echo -e "${YELLOW}Next steps:${NC}"
echo "1. Deploy functions: ./deploy.sh"
echo "2. Set environment variables in Cloud Console or via gcloud"
echo "3. Test locally: ./test-local.sh dream"
echo ""
echo -e "${CYAN}To set up Drive Watch for auto-sync (run after deploy):${NC}"
echo "  python setup_drive_watch.py"
echo ""
