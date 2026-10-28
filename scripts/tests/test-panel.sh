#!/bin/bash
#
# MDDB Panel (React UI) Authentication Test
#
# This script starts the MDDB server with auth and the Panel dev server,
# then provides instructions for manual testing of the login UI
#

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

print_header() {
    echo -e "\n${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${BLUE}$1${NC}"
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}\n"
}

print_success() {
    echo -e "${GREEN}✓ $1${NC}"
}

print_info() {
    echo -e "  $1"
}

print_warning() {
    echo -e "${YELLOW}⚠ $1${NC}"
}

cleanup() {
    echo -e "\n${YELLOW}Cleaning up...${NC}"
    pkill -f mddb-server 2>/dev/null || true
    pkill -f "vite" 2>/dev/null || true
    rm -f services/mddbd/mddb.db services/mddbd/mddb.db.lock
    echo -e "${GREEN}Cleanup complete${NC}"
}

trap cleanup EXIT

print_header "MDDB Panel Authentication Test"

# Check if we're in the right directory
if [ ! -f "services/mddb-panel/package.json" ]; then
    echo -e "${RED}Error: Please run this script from the MDDB root directory${NC}"
    exit 1
fi

# Build the server
print_header "Building MDDB Server"
cd services/mddbd
if go build -o mddb-server . > /tmp/mddb-build.log 2>&1; then
    print_success "Server built successfully"
else
    echo -e "${RED}Server build failed${NC}"
    cat /tmp/mddb-build.log
    exit 1
fi

# Start server with auth enabled
print_header "Starting MDDB Server with Authentication"
export MDDB_AUTH_ENABLED=true
export MDDB_AUTH_JWT_SECRET=$(openssl rand -hex 32)
export MDDB_AUTH_ADMIN_USERNAME=admin
export MDDB_AUTH_ADMIN_PASSWORD=changeme

./mddb-server > /tmp/mddb-server.log 2>&1 &
SERVER_PID=$!
print_info "Server PID: $SERVER_PID"

sleep 3

if ! ps -p $SERVER_PID > /dev/null; then
    echo -e "${RED}Server failed to start${NC}"
    cat /tmp/mddb-server.log
    exit 1
fi
print_success "Server is running on http://localhost:11023"
print_info "Auth enabled: admin / changeme"

cd ../..

# Check if node_modules exists
if [ ! -d "services/mddb-panel/node_modules" ]; then
    print_header "Installing Panel Dependencies"
    cd services/mddb-panel
    npm ci --ignore-scripts
    cd ../..
fi

# Start Panel dev server
print_header "Starting Panel Dev Server"
cd services/mddb-panel

# Start vite in background
npm run dev > /tmp/panel-dev.log 2>&1 &
PANEL_PID=$!
print_info "Panel PID: $PANEL_PID"

# Wait for vite to start
print_info "Waiting for Panel to start..."
sleep 5

if ! ps -p $PANEL_PID > /dev/null; then
    echo -e "${RED}Panel dev server failed to start${NC}"
    cat /tmp/panel-dev.log
    exit 1
fi

cd ../..

# Test if server is accessible
print_header "Verification"

# Test 1: Server responds to unauthenticated request with 401
HTTP_CODE=$(curl -s -w "%{http_code}" -o /dev/null http://localhost:11023/v1/stats)
if [ "$HTTP_CODE" = "401" ]; then
    print_success "Server authentication is active (returns 401)"
else
    print_warning "Server returned $HTTP_CODE instead of 401"
fi

# Test 2: Panel is accessible
if curl -s http://localhost:5173 > /dev/null 2>&1; then
    print_success "Panel dev server is accessible"
else
    print_warning "Panel dev server might not be ready yet"
fi

# Instructions
print_header "Manual Testing Instructions"

echo -e "${GREEN}✓ Setup Complete!${NC}\n"

echo -e "${BLUE}MDDB Server:${NC}"
echo -e "  URL: ${GREEN}http://localhost:11023${NC}"
echo -e "  Logs: ${YELLOW}tail -f /tmp/mddb-server.log${NC}\n"

echo -e "${BLUE}Panel Dev Server:${NC}"
echo -e "  URL: ${GREEN}http://localhost:5173${NC}"
echo -e "  Logs: ${YELLOW}tail -f /tmp/panel-dev.log${NC}\n"

echo -e "${BLUE}Test Credentials:${NC}"
echo -e "  Username: ${GREEN}admin${NC}"
echo -e "  Password: ${GREEN}changeme${NC}\n"

echo -e "${YELLOW}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${YELLOW}  Manual Test Checklist:${NC}"
echo -e "${YELLOW}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}\n"

echo -e "1. Open ${GREEN}http://localhost:5173${NC} in your browser"
echo -e "2. Verify login form is displayed"
echo -e "3. Try logging in with wrong credentials (should fail)"
echo -e "4. Login with ${GREEN}admin / changeme${NC} (should succeed)"
echo -e "5. Verify you see the MDDB Panel dashboard"
echo -e "6. Verify you see the ${GREEN}Logout${NC} button in the header"
echo -e "7. Click ${YELLOW}Logout${NC} - should redirect to login"
echo -e "8. Login again - should work\n"

echo -e "${BLUE}Expected Behavior:${NC}"
echo -e "  ✓ Login form appears automatically"
echo -e "  ✓ Wrong credentials show error message"
echo -e "  ✓ Correct credentials log you in"
echo -e "  ✓ Token is stored in localStorage"
echo -e "  ✓ Logout clears token and shows login form"
echo -e "  ✓ All API calls include Authorization header\n"

echo -e "${YELLOW}Press Ctrl+C when done testing to cleanup${NC}\n"

# Wait for user interrupt
wait $PANEL_PID
