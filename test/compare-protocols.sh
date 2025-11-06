#!/bin/bash

# Compare HTTP vs gRPC performance

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
PURPLE='\033[0;35m'
CYAN='\033[0;36m'
NC='\033[0m'

echo -e "${BLUE}════════════════════════════════════════════════${NC}"
echo -e "${GREEN}  HTTP vs gRPC Performance Comparison${NC}"
echo -e "${BLUE}════════════════════════════════════════════════${NC}"
echo ""

# Check if server is running
echo -e "${CYAN}Checking server connectivity...${NC}"
if ! mddb-cli stats > /dev/null 2>&1; then
    echo -e "${RED}✗ Cannot connect to MDDB server${NC}"
    echo -e "${YELLOW}  Make sure the server is running: make docker-up-dev${NC}"
    exit 1
fi
echo -e "${GREEN}✓ Server is running (HTTP: 11023, gRPC: 11024)${NC}"
echo ""

# Generate Lorem Ipsum files if needed
if [ ! -f "lorem-short.md" ]; then
    echo -e "${CYAN}Generating Lorem Ipsum files...${NC}"
    ./generate-lorem.sh
    echo ""
fi

# Build gRPC test
echo -e "${CYAN}Building gRPC test client...${NC}"
if [ ! -f "go.mod" ]; then
    echo -e "${RED}✗ go.mod not found${NC}"
    exit 1
fi

# Make sure proto is generated
if [ ! -f "../services/mddbd/proto/mddb.pb.go" ]; then
    echo -e "${YELLOW}Generating proto files...${NC}"
    cd .. && make generate-proto && cd test
fi

go build -o grpc-perf-test grpc-performance-test.go
echo -e "${GREEN}✓ gRPC test client built${NC}"
echo ""

# Reduce test size for comparison
TOTAL_DOCS=1000
export TOTAL_DOCS

echo -e "${PURPLE}Running tests with ${TOTAL_DOCS} documents per size (3x = ${TOTAL_DOCS}x3 total)${NC}"
echo ""

# Run HTTP test
echo -e "${BLUE}════════════════════════════════════════════════${NC}"
echo -e "${GREEN}  Test 1: HTTP/JSON Protocol${NC}"
echo -e "${BLUE}════════════════════════════════════════════════${NC}"
echo ""

HTTP_START=$(date +%s)
./performance-test.sh > /tmp/http-results.txt 2>&1
HTTP_END=$(date +%s)
HTTP_TIME=$((HTTP_END - HTTP_START))

echo -e "${GREEN}✓ HTTP test completed in ${HTTP_TIME}s${NC}"
echo ""

# Run gRPC test
echo -e "${BLUE}════════════════════════════════════════════════${NC}"
echo -e "${GREEN}  Test 2: gRPC/Protobuf Protocol${NC}"
echo -e "${BLUE}════════════════════════════════════════════════${NC}"
echo ""

GRPC_START=$(date +%s)
./grpc-perf-test > /tmp/grpc-results.txt 2>&1
GRPC_END=$(date +%s)
GRPC_TIME=$((GRPC_END - GRPC_START))

echo -e "${GREEN}✓ gRPC test completed in ${GRPC_TIME}s${NC}"
echo ""

# Parse results
echo -e "${BLUE}════════════════════════════════════════════════${NC}"
echo -e "${GREEN}  Comparison Results${NC}"
echo -e "${BLUE}════════════════════════════════════════════════${NC}"
echo ""

# Extract throughput from results
HTTP_THROUGHPUT=$(grep "Overall throughput:" /tmp/http-results.txt | tail -1 | awk '{print $3}')
GRPC_THROUGHPUT=$(grep "Overall throughput:" /tmp/grpc-results.txt | tail -1 | awk '{print $3}')

# Extract average times
HTTP_AVG=$(grep "Average per doc:" /tmp/http-results.txt | tail -1 | awk '{print $4}')
GRPC_AVG=$(grep "Average per doc:" /tmp/grpc-results.txt | tail -1 | awk '{print $4}')

echo -e "${PURPLE}Protocol Performance:${NC}"
echo ""
echo -e "  ${CYAN}HTTP/JSON:${NC}"
echo "    Total time:    ${HTTP_TIME}s"
echo "    Avg per doc:   ${HTTP_AVG}"
echo "    Throughput:    ${HTTP_THROUGHPUT} docs/sec"
echo ""
echo -e "  ${CYAN}gRPC/Protobuf:${NC}"
echo "    Total time:    ${GRPC_TIME}s"
echo "    Avg per doc:   ${GRPC_AVG}"
echo "    Throughput:    ${GRPC_THROUGHPUT} docs/sec"
echo ""

# Calculate improvement
if [ -n "$HTTP_THROUGHPUT" ] && [ -n "$GRPC_THROUGHPUT" ]; then
    IMPROVEMENT=$(echo "scale=2; ($GRPC_THROUGHPUT - $HTTP_THROUGHPUT) / $HTTP_THROUGHPUT * 100" | bc)
    SPEEDUP=$(echo "scale=2; $GRPC_THROUGHPUT / $HTTP_THROUGHPUT" | bc)
    
    echo -e "${PURPLE}Performance Gain:${NC}"
    echo "  gRPC is ${SPEEDUP}x faster than HTTP"
    echo "  Improvement: ${IMPROVEMENT}%"
    echo ""
fi

# Payload size comparison
echo -e "${PURPLE}Payload Size Comparison (estimated):${NC}"
echo "  HTTP/JSON:      ~100% (baseline)"
echo "  gRPC/Protobuf:  ~30% (70% smaller)"
echo ""

echo -e "${BLUE}════════════════════════════════════════════════${NC}"
echo -e "${GREEN}  Recommendation${NC}"
echo -e "${BLUE}════════════════════════════════════════════════${NC}"
echo ""
echo -e "${YELLOW}For production use:${NC}"
echo "  • Use ${GREEN}gRPC${NC} for high-performance applications"
echo "  • Use ${CYAN}HTTP${NC} for debugging, curl, browser clients"
echo "  • Both protocols run simultaneously on MDDB"
echo ""

# Save comparison
COMPARISON_FILE="protocol-comparison-$(date +%Y%m%d-%H%M%S).txt"
{
    echo "HTTP vs gRPC Performance Comparison"
    echo "===================================="
    echo "Date: $(date)"
    echo "Documents tested: $((TOTAL_DOCS * 3))"
    echo ""
    echo "HTTP/JSON:"
    echo "  Time: ${HTTP_TIME}s"
    echo "  Avg: ${HTTP_AVG}"
    echo "  Throughput: ${HTTP_THROUGHPUT} docs/sec"
    echo ""
    echo "gRPC/Protobuf:"
    echo "  Time: ${GRPC_TIME}s"
    echo "  Avg: ${GRPC_AVG}"
    echo "  Throughput: ${GRPC_THROUGHPUT} docs/sec"
    echo ""
    echo "Improvement: ${IMPROVEMENT}%"
    echo "Speedup: ${SPEEDUP}x"
} > "$COMPARISON_FILE"

echo -e "${GREEN}Comparison saved to: $COMPARISON_FILE${NC}"
echo ""

# Cleanup
rm -f grpc-perf-test
