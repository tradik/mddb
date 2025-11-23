#!/bin/bash
# Quick start script for MDDB MCP on WSL
# Usage: ./start-wsl.sh

set -e

echo "🚀 MDDB MCP - WSL Quick Start"
echo "=============================="
echo ""

# Colors
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Check if Docker is available
if ! command -v docker &> /dev/null; then
    echo -e "${YELLOW}⚠️  Docker not found. Please install Docker Desktop for Windows with WSL2 backend.${NC}"
    exit 1
fi

# Check if docker-compose is available
if ! command -v docker-compose &> /dev/null; then
    echo -e "${YELLOW}⚠️  docker-compose not found. Installing...${NC}"
    sudo apt-get update && sudo apt-get install -y docker-compose
fi

# Navigate to project root (assuming script is in services/mddb-mcp)
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
PROJECT_ROOT="$( cd "$SCRIPT_DIR/../.." && pwd )"

echo -e "${BLUE}📁 Project root: $PROJECT_ROOT${NC}"
echo ""

# Start MDDB server
echo -e "${BLUE}🐳 Starting MDDB server...${NC}"
cd "$PROJECT_ROOT"

if [ -f "docker-compose.yml" ]; then
    docker-compose up -d mddb
else
    echo -e "${YELLOW}⚠️  docker-compose.yml not found. Starting MDDB manually...${NC}"
    docker run -d \
        --name mddb \
        -p 11023:11023 \
        -p 11024:11024 \
        -v mddb-data:/data \
        -e MDDB_EXTREME=true \
        tradik/mddb:latest
fi

# Wait for MDDB to be ready
echo -e "${BLUE}⏳ Waiting for MDDB server to be ready...${NC}"
MAX_RETRIES=30
RETRY_COUNT=0

while [ $RETRY_COUNT -lt $MAX_RETRIES ]; do
    if curl -s http://localhost:11023/v1/stats > /dev/null 2>&1; then
        echo -e "${GREEN}✅ MDDB server is ready!${NC}"
        break
    fi
    RETRY_COUNT=$((RETRY_COUNT+1))
    echo -n "."
    sleep 1
done

if [ $RETRY_COUNT -eq $MAX_RETRIES ]; then
    echo -e "${YELLOW}⚠️  MDDB server did not start in time. Check logs with: docker logs mddb${NC}"
    exit 1
fi

echo ""

# Display server info
echo -e "${GREEN}📊 MDDB Server Information:${NC}"
echo "   HTTP API:  http://localhost:11023"
echo "   gRPC API:  localhost:11024"
echo "   Stats:     http://localhost:11023/v1/stats"
echo ""

# Check if MCP image is available
echo -e "${BLUE}🔍 Checking MCP Docker image...${NC}"
if docker image inspect tradik/mddb:mcp > /dev/null 2>&1; then
    echo -e "${GREEN}✅ MCP image found: tradik/mddb:mcp${NC}"
else
    echo -e "${YELLOW}⚠️  MCP image not found locally. Pulling...${NC}"
    docker pull tradik/mddb:mcp
fi

echo ""

# Display Windsurf configuration
echo -e "${GREEN}🎯 Windsurf Configuration:${NC}"
echo ""
echo "Create or edit: %USERPROFILE%\.codeium\windsurf\mcp_config.json"
echo ""
echo "Add this configuration:"
echo ""
cat << 'EOF'
{
  "mcpServers": {
    "mddb": {
      "command": "docker",
      "args": [
        "run",
        "-i",
        "--rm",
        "--network",
        "host",
        "-e",
        "MCP_MODE=stdio",
        "-e",
        "MDDB_GRPC_ADDRESS=localhost:11024",
        "-e",
        "MDDB_REST_BASE_URL=http://localhost:11023",
        "-e",
        "MDDB_TRANSPORT_MODE=grpc_with_rest_fallback",
        "tradik/mddb:mcp"
      ],
      "env": {}
    }
  }
}
EOF

echo ""
echo -e "${GREEN}✅ Setup complete!${NC}"
echo ""
echo -e "${BLUE}Next steps:${NC}"
echo "1. Copy the configuration above to Windsurf mcp_config.json"
echo "2. Restart Windsurf IDE"
echo "3. Open MCP marketplace (Ctrl+Shift+P → 'MCP')"
echo "4. Look for 'mddb' with green status"
echo ""
echo -e "${BLUE}Useful commands:${NC}"
echo "  Stop MDDB:     docker-compose down"
echo "  View logs:     docker logs mddb -f"
echo "  Check stats:   curl http://localhost:11023/v1/stats"
echo "  Test MCP:      docker run -i --rm tradik/mddb:mcp -e MCP_MODE=stdio"
echo ""
echo -e "${BLUE}Documentation:${NC}"
echo "  WSL Guide:     services/mddb-mcp/WSL_SETUP.md"
echo "  Windsurf:      services/mddb-mcp/WINDSURF_SETUP.md"
echo "  README:        services/mddb-mcp/README.md"
echo ""
