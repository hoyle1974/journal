#!/bin/bash
#
# Run Jot Cloud Function locally for testing (Go version)
#
# Usage:
#   ./scripts/test-local.sh           # Start server on port 8080
#   ./scripts/test-local.sh 8081      # Start on custom port
#
set -e

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

PORT=${1:-8080}

# Colors
YELLOW='\033[1;33m'
GREEN='\033[0;32m'
CYAN='\033[0;36m'
NC='\033[0m'

echo -e "${YELLOW}Starting Jot API locally (Go)${NC}"
echo "Port: ${PORT}"
echo ""

# Load environment variables from .env if it exists
if [ -f .env ]; then
    echo -e "${CYAN}Loading environment from .env${NC}"
    set -a
    # shellcheck source=.env
    source .env
    set +a
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
go build -o jot-local ./cmd/server
PORT=$PORT RUN_LOCAL=1 ./jot-local
