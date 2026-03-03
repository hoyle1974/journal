#!/bin/bash
#
# Run Jot Cloud Function locally for testing (Go version)
#
# Usage:
#   ./test-local.sh           # Start server on port 8080
#   ./test-local.sh 8081      # Start on custom port
#
set -e

PORT=${1:-8080}

# Colors
YELLOW='\033[1;33m'
GREEN='\033[0;32m'
CYAN='\033[0;36m'
NC='\033[0m'

echo -e "${YELLOW}Starting Jot API locally (Go)${NC}"
echo "Port: ${PORT}"
echo ""

# Get absolute path to script directory
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# Load environment variables from .env if it exists
if [ -f "$SCRIPT_DIR/.env" ]; then
    echo -e "${CYAN}Loading environment from .env${NC}"
    export $(grep -v '^#' "$SCRIPT_DIR/.env" | xargs)
fi

echo ""
echo -e "${GREEN}API running at: http://localhost:${PORT}${NC}"
echo ""
echo "Test commands:"
echo ""
echo "  # Health check"
echo "  curl http://localhost:${PORT}/health"
echo ""
echo "  # Log entry"
echo "  curl -X POST http://localhost:${PORT}/log \\"
echo "    -H 'Content-Type: application/json' \\"
echo "    -d '{\"content\": \"Test entry\", \"source\": \"test\"}'"
echo ""
echo "  # Query"
echo "  curl -X POST http://localhost:${PORT}/query \\"
echo "    -H 'Content-Type: application/json' \\"
echo "    -d '{\"question\": \"What did I do recently?\"}'"
echo ""
echo "  # List entries"
echo "  curl http://localhost:${PORT}/entries?limit=5"
echo ""
echo "  # Dream / Janitor"
echo "  curl -X POST http://localhost:${PORT}/dream"
echo "  curl -X POST http://localhost:${PORT}/janitor"
echo ""
echo "  # Sync Google Doc"
echo "  curl -X POST http://localhost:${PORT}/sync"
echo ""
echo "Press Ctrl+C to stop"
echo ""
echo "---"

# Build and run the server (plain HTTP, loads .env from project root)
cd "$SCRIPT_DIR"
go build -o jot-local ./cmd/local
PORT=$PORT ./jot-local
