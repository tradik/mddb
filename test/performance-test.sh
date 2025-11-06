#!/bin/bash

# MDDB Performance Test Script
# Tests bulk document insertion and measures performance

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
PURPLE='\033[0;35m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Configuration
COLLECTION="perftest"
TOTAL_DOCS=10000
BATCH_SIZE=100
SERVER_URL="${MDDB_SERVER:-http://localhost:11023}"
CLI="${MDDB_CLI:-mddb-cli}"

# Check if server is running
echo -e "${BLUE}════════════════════════════════════════════════${NC}"
echo -e "${GREEN}  MDDB Performance Test${NC}"
echo -e "${BLUE}════════════════════════════════════════════════${NC}"
echo ""

echo -e "${CYAN}Checking server connectivity...${NC}"
if ! $CLI stats > /dev/null 2>&1; then
    echo -e "${RED}✗ Cannot connect to MDDB server at $SERVER_URL${NC}"
    echo -e "${YELLOW}  Make sure the server is running: make docker-up-dev${NC}"
    exit 1
fi
echo -e "${GREEN}✓ Server is running${NC}"
echo ""

# Generate Lorem Ipsum files if they don't exist
if [ ! -f "lorem-short.md" ]; then
    echo -e "${CYAN}Generating Lorem Ipsum files...${NC}"
    ./generate-lorem.sh
    echo ""
fi

# Get file sizes
SHORT_SIZE=$(wc -c < lorem-short.md)
MEDIUM_SIZE=$(wc -c < lorem-medium.md)
LONG_SIZE=$(wc -c < lorem-long.md)

echo -e "${CYAN}Test Configuration:${NC}"
echo "  Collection: $COLLECTION"
echo "  Total documents: $TOTAL_DOCS"
echo "  Batch size: $BATCH_SIZE"
echo "  Server: $SERVER_URL"
echo ""
echo -e "${CYAN}Document sizes:${NC}"
echo "  Short:  $SHORT_SIZE bytes"
echo "  Medium: $MEDIUM_SIZE bytes"
echo "  Long:   $LONG_SIZE bytes"
echo ""

# Clean up old test data
echo -e "${CYAN}Cleaning up old test data...${NC}"
# Note: MDDB doesn't have a delete endpoint, so we'll just overwrite
echo -e "${GREEN}✓ Ready${NC}"
echo ""

# Function to add document and measure time
add_document() {
    local key=$1
    local file=$2
    local meta=$3
    local lang=${4:-en_US}
    
    start=$(date +%s%N)
    cat "$file" | $CLI add "$COLLECTION" "$key" "$lang" -m "$meta" > /dev/null 2>&1
    end=$(date +%s%N)
    
    # Return time in milliseconds
    echo $(( (end - start) / 1000000 ))
}

# Arrays to store times
declare -a times_short
declare -a times_medium
declare -a times_long

echo -e "${BLUE}════════════════════════════════════════════════${NC}"
echo -e "${GREEN}  Starting Performance Test${NC}"
echo -e "${BLUE}════════════════════════════════════════════════${NC}"
echo ""

# Test 1: Short documents
echo -e "${PURPLE}Test 1: Adding $TOTAL_DOCS short documents${NC}"
echo -e "${CYAN}Progress: ${NC}"

total_time_short=0
for i in $(seq 1 $TOTAL_DOCS); do
    key="doc-short-$i"
    meta="category=test,size=short,batch=$((i / BATCH_SIZE))"
    
    time=$(add_document "$key" "lorem-short.md" "$meta")
    times_short+=($time)
    total_time_short=$((total_time_short + time))
    
    # Progress indicator
    if [ $((i % BATCH_SIZE)) -eq 0 ]; then
        progress=$((i * 100 / TOTAL_DOCS))
        printf "\r  [%-50s] %d%% (%d/%d docs, avg: %dms)" \
            $(printf '#%.0s' $(seq 1 $((progress / 2)))) \
            $progress $i $TOTAL_DOCS \
            $((total_time_short / i))
    fi
done
echo ""
echo -e "${GREEN}✓ Completed${NC}"
echo ""

# Test 2: Medium documents
echo -e "${PURPLE}Test 2: Adding $TOTAL_DOCS medium documents${NC}"
echo -e "${CYAN}Progress: ${NC}"

total_time_medium=0
for i in $(seq 1 $TOTAL_DOCS); do
    key="doc-medium-$i"
    meta="category=test,size=medium,batch=$((i / BATCH_SIZE))"
    
    time=$(add_document "$key" "lorem-medium.md" "$meta")
    times_medium+=($time)
    total_time_medium=$((total_time_medium + time))
    
    if [ $((i % BATCH_SIZE)) -eq 0 ]; then
        progress=$((i * 100 / TOTAL_DOCS))
        printf "\r  [%-50s] %d%% (%d/%d docs, avg: %dms)" \
            $(printf '#%.0s' $(seq 1 $((progress / 2)))) \
            $progress $i $TOTAL_DOCS \
            $((total_time_medium / i))
    fi
done
echo ""
echo -e "${GREEN}✓ Completed${NC}"
echo ""

# Test 3: Long documents
echo -e "${PURPLE}Test 3: Adding $TOTAL_DOCS long documents${NC}"
echo -e "${CYAN}Progress: ${NC}"

total_time_long=0
for i in $(seq 1 $TOTAL_DOCS); do
    key="doc-long-$i"
    meta="category=test,size=long,batch=$((i / BATCH_SIZE))"
    
    time=$(add_document "$key" "lorem-long.md" "$meta")
    times_long+=($time)
    total_time_long=$((total_time_long + time))
    
    if [ $((i % BATCH_SIZE)) -eq 0 ]; then
        progress=$((i * 100 / TOTAL_DOCS))
        printf "\r  [%-50s] %d%% (%d/%d docs, avg: %dms)" \
            $(printf '#%.0s' $(seq 1 $((progress / 2)))) \
            $progress $i $TOTAL_DOCS \
            $((total_time_long / i))
    fi
done
echo ""
echo -e "${GREEN}✓ Completed${NC}"
echo ""

# Calculate statistics
calc_stats() {
    local arr=("$@")
    local sum=0
    local min=999999999
    local max=0
    local count=${#arr[@]}
    
    for time in "${arr[@]}"; do
        sum=$((sum + time))
        [ $time -lt $min ] && min=$time
        [ $time -gt $max ] && max=$time
    done
    
    local avg=$((sum / count))
    
    # Calculate median (simple approximation)
    local sorted=($(printf '%s\n' "${arr[@]}" | sort -n))
    local median=${sorted[$((count / 2))]}
    
    echo "$avg $min $max $median"
}

stats_short=($(calc_stats "${times_short[@]}"))
stats_medium=($(calc_stats "${times_medium[@]}"))
stats_long=($(calc_stats "${times_long[@]}"))

# Get final server stats
echo -e "${CYAN}Fetching server statistics...${NC}"
server_stats=$($CLI stats -j 2>/dev/null || echo "{}")
echo ""

# Print results
echo -e "${BLUE}════════════════════════════════════════════════${NC}"
echo -e "${GREEN}  Performance Test Results${NC}"
echo -e "${BLUE}════════════════════════════════════════════════${NC}"
echo ""

print_results() {
    local label=$1
    local size=$2
    local stats=("${!3}")
    local total=$4
    
    echo -e "${PURPLE}$label ($size bytes):${NC}"
    echo "  Documents:      $TOTAL_DOCS"
    echo "  Total time:     ${total}ms ($(echo "scale=2; $total/1000" | bc)s)"
    echo "  Average:        ${stats[0]}ms per document"
    echo "  Min:            ${stats[1]}ms"
    echo "  Max:            ${stats[2]}ms"
    echo "  Median:         ${stats[3]}ms"
    echo "  Throughput:     $(echo "scale=2; $TOTAL_DOCS * 1000 / $total" | bc) docs/sec"
    echo ""
}

print_results "Short Documents" "$SHORT_SIZE" stats_short[@] "$total_time_short"
print_results "Medium Documents" "$MEDIUM_SIZE" stats_medium[@] "$total_time_medium"
print_results "Long Documents" "$LONG_SIZE" stats_long[@] "$total_time_long"

# Overall statistics
total_docs=$((TOTAL_DOCS * 3))
total_time=$((total_time_short + total_time_medium + total_time_long))
avg_time=$((total_time / total_docs))

echo -e "${PURPLE}Overall Statistics:${NC}"
echo "  Total documents:    $total_docs"
echo "  Total time:         ${total_time}ms ($(echo "scale=2; $total_time/1000" | bc)s)"
echo "  Average per doc:    ${avg_time}ms"
echo "  Overall throughput: $(echo "scale=2; $total_docs * 1000 / $total_time" | bc) docs/sec"
echo ""

# Server stats
if [ "$server_stats" != "{}" ]; then
    echo -e "${PURPLE}Server Statistics:${NC}"
    echo "$server_stats" | jq -r '
        "  Database size:      \(.databaseSize / 1024 / 1024 | floor)MB",
        "  Total documents:    \(.totalDocuments)",
        "  Total revisions:    \(.totalRevisions)",
        "  Collections:        \(.collections | length)"
    ' 2>/dev/null || echo "  (stats not available)"
    echo ""
fi

echo -e "${BLUE}════════════════════════════════════════════════${NC}"
echo -e "${GREEN}  Test completed successfully!${NC}"
echo -e "${BLUE}════════════════════════════════════════════════${NC}"
echo ""

# Save results to file
RESULTS_FILE="performance-results-$(date +%Y%m%d-%H%M%S).txt"
{
    echo "MDDB Performance Test Results"
    echo "=============================="
    echo "Date: $(date)"
    echo "Server: $SERVER_URL"
    echo ""
    echo "Configuration:"
    echo "  Total documents: $total_docs"
    echo "  Documents per size: $TOTAL_DOCS"
    echo ""
    echo "Short Documents ($SHORT_SIZE bytes):"
    echo "  Average: ${stats_short[0]}ms"
    echo "  Min: ${stats_short[1]}ms"
    echo "  Max: ${stats_short[2]}ms"
    echo "  Median: ${stats_short[3]}ms"
    echo ""
    echo "Medium Documents ($MEDIUM_SIZE bytes):"
    echo "  Average: ${stats_medium[0]}ms"
    echo "  Min: ${stats_medium[1]}ms"
    echo "  Max: ${stats_medium[2]}ms"
    echo "  Median: ${stats_medium[3]}ms"
    echo ""
    echo "Long Documents ($LONG_SIZE bytes):"
    echo "  Average: ${stats_long[0]}ms"
    echo "  Min: ${stats_long[1]}ms"
    echo "  Max: ${stats_long[2]}ms"
    echo "  Median: ${stats_long[3]}ms"
    echo ""
    echo "Overall:"
    echo "  Total time: ${total_time}ms"
    echo "  Average: ${avg_time}ms"
    echo "  Throughput: $(echo "scale=2; $total_docs * 1000 / $total_time" | bc) docs/sec"
} > "$RESULTS_FILE"

echo -e "${GREEN}Results saved to: $RESULTS_FILE${NC}"
