#!/bin/bash

# MDDB Folder Loader Script
# Loads all markdown files from a folder into MDDB database
# Usage: ./load-md-folder.sh <folder_path> <collection> [options]

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
PURPLE='\033[0;35m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Default configuration
SERVER_URL="${MDDB_SERVER:-http://localhost:11023}"
CLI="${MDDB_CLI:-mddb-cli}"
DEFAULT_LANG="en_US"
RECURSIVE=false
VERBOSE=false
DRY_RUN=false
BATCH_SIZE=10

# Function to show usage
show_usage() {
    cat << EOF
${BLUE}════════════════════════════════════════════════${NC}
${GREEN}  MDDB Folder Loader${NC}
${BLUE}════════════════════════════════════════════════${NC}

${CYAN}Usage:${NC}
  $0 <folder_path> <collection> [options]

${CYAN}Arguments:${NC}
  folder_path       Path to folder containing .md files
  collection        MDDB collection name

${CYAN}Options:${NC}
  -l, --lang LANG          Language code (default: en_US)
  -r, --recursive          Process subfolders recursively
  -m, --meta KEY=VALUE     Add metadata (can be used multiple times)
  -s, --server URL         MDDB server URL (default: http://localhost:11023)
  -v, --verbose            Verbose output
  -d, --dry-run            Show what would be done without executing
  -b, --batch-size N       Progress update every N files (default: 10)
  -h, --help               Show this help message

${CYAN}Examples:${NC}
  # Load all .md files from docs folder
  $0 ./docs blog

  # Load recursively with custom language
  $0 ./content articles -r -l pl_PL

  # Add custom metadata
  $0 ./posts blog -m "author=John Doe" -m "status=published"

  # Dry run to see what would be loaded
  $0 ./docs blog -d

${CYAN}Environment Variables:${NC}
  MDDB_SERVER    Server URL (default: http://localhost:11023)
  MDDB_CLI       CLI command (default: mddb-cli)

EOF
}

# Function to check if server is running
check_server() {
    echo -e "${CYAN}Checking server connectivity...${NC}"
    if ! $CLI stats > /dev/null 2>&1; then
        echo -e "${RED}✗ Cannot connect to MDDB server at $SERVER_URL${NC}"
        echo -e "${YELLOW}  Make sure the server is running${NC}"
        exit 1
    fi
    echo -e "${GREEN}✓ Server is running${NC}"
}

# Function to generate key from filename
generate_key() {
    local filepath=$1
    local filename=$(basename "$filepath" .md)
    # Convert to lowercase and replace spaces/special chars with hyphens
    echo "$filename" | tr '[:upper:]' '[:lower:]' | sed 's/[^a-z0-9]/-/g' | sed 's/--*/-/g' | sed 's/^-//;s/-$//'
}

# Function to extract metadata from frontmatter (if exists)
extract_frontmatter() {
    local file=$1
    local meta=""
    
    # Check if file starts with ---
    if head -n 1 "$file" | grep -q "^---$"; then
        # Extract frontmatter between first and second ---
        local frontmatter=$(awk '/^---$/{if(++n==2) exit; next} n==1' "$file")
        
        # Parse YAML-like key: value pairs
        while IFS=: read -r key value; do
            if [ -n "$key" ] && [ -n "$value" ]; then
                key=$(echo "$key" | xargs)
                value=$(echo "$value" | xargs | sed 's/^["'\'']\|["'\'']$//g')
                if [ -n "$meta" ]; then
                    meta="$meta,$key=$value"
                else
                    meta="$key=$value"
                fi
            fi
        done <<< "$frontmatter"
    fi
    
    echo "$meta"
}

# Function to load a single file
load_file() {
    local filepath=$1
    local collection=$2
    local lang=$3
    local extra_meta=$4
    
    local key=$(generate_key "$filepath")
    local file_meta=$(extract_frontmatter "$filepath")
    
    # Combine metadata
    local meta="source=folder-import,filename=$(basename "$filepath")"
    [ -n "$file_meta" ] && meta="$meta,$file_meta"
    [ -n "$extra_meta" ] && meta="$meta,$extra_meta"
    
    if [ "$DRY_RUN" = true ]; then
        echo -e "${YELLOW}[DRY RUN]${NC} Would load: $filepath"
        echo -e "  Collection: $collection"
        echo -e "  Key: $key"
        echo -e "  Lang: $lang"
        echo -e "  Meta: $meta"
        return 0
    fi
    
    if [ "$VERBOSE" = true ]; then
        echo -e "${CYAN}Loading:${NC} $filepath"
        echo -e "  Key: $key"
        echo -e "  Meta: $meta"
    fi
    
    # Load file using CLI
    if cat "$filepath" | $CLI add "$collection" "$key" "$lang" -m "$meta" > /dev/null 2>&1; then
        return 0
    else
        echo -e "${RED}✗ Failed to load: $filepath${NC}"
        return 1
    fi
}

# Parse command line arguments
FOLDER_PATH=""
COLLECTION=""
LANG="$DEFAULT_LANG"
EXTRA_META=""

while [[ $# -gt 0 ]]; do
    case $1 in
        -h|--help)
            show_usage
            exit 0
            ;;
        -l|--lang)
            LANG="$2"
            shift 2
            ;;
        -r|--recursive)
            RECURSIVE=true
            shift
            ;;
        -m|--meta)
            if [ -n "$EXTRA_META" ]; then
                EXTRA_META="$EXTRA_META,$2"
            else
                EXTRA_META="$2"
            fi
            shift 2
            ;;
        -s|--server)
            SERVER_URL="$2"
            shift 2
            ;;
        -v|--verbose)
            VERBOSE=true
            shift
            ;;
        -d|--dry-run)
            DRY_RUN=true
            shift
            ;;
        -b|--batch-size)
            BATCH_SIZE="$2"
            shift 2
            ;;
        -*)
            echo -e "${RED}Unknown option: $1${NC}"
            show_usage
            exit 1
            ;;
        *)
            if [ -z "$FOLDER_PATH" ]; then
                FOLDER_PATH="$1"
            elif [ -z "$COLLECTION" ]; then
                COLLECTION="$1"
            else
                echo -e "${RED}Too many arguments${NC}"
                show_usage
                exit 1
            fi
            shift
            ;;
    esac
done

# Validate arguments
if [ -z "$FOLDER_PATH" ] || [ -z "$COLLECTION" ]; then
    echo -e "${RED}Error: Missing required arguments${NC}"
    echo ""
    show_usage
    exit 1
fi

if [ ! -d "$FOLDER_PATH" ]; then
    echo -e "${RED}Error: Folder does not exist: $FOLDER_PATH${NC}"
    exit 1
fi

# Print header
echo -e "${BLUE}════════════════════════════════════════════════${NC}"
echo -e "${GREEN}  MDDB Folder Loader${NC}"
echo -e "${BLUE}════════════════════════════════════════════════${NC}"
echo ""

# Check server connectivity (skip in dry-run mode)
if [ "$DRY_RUN" = false ]; then
    check_server
    echo ""
fi

# Configuration summary
echo -e "${CYAN}Configuration:${NC}"
echo "  Folder:     $FOLDER_PATH"
echo "  Collection: $COLLECTION"
echo "  Language:   $LANG"
echo "  Server:     $SERVER_URL"
echo "  Recursive:  $RECURSIVE"
[ -n "$EXTRA_META" ] && echo "  Metadata:   $EXTRA_META"
[ "$DRY_RUN" = true ] && echo -e "  ${YELLOW}Mode:       DRY RUN${NC}"
echo ""

# Find all .md files
echo -e "${CYAN}Scanning for markdown files...${NC}"
if [ "$RECURSIVE" = true ]; then
    FILES=$(find "$FOLDER_PATH" -type f -name "*.md")
else
    FILES=$(find "$FOLDER_PATH" -maxdepth 1 -type f -name "*.md")
fi

FILE_COUNT=$(echo "$FILES" | grep -c "^" || echo "0")

if [ "$FILE_COUNT" -eq 0 ]; then
    echo -e "${YELLOW}No markdown files found in $FOLDER_PATH${NC}"
    exit 0
fi

echo -e "${GREEN}Found $FILE_COUNT markdown file(s)${NC}"
echo ""

# Load files
echo -e "${BLUE}════════════════════════════════════════════════${NC}"
echo -e "${GREEN}  Loading Files${NC}"
echo -e "${BLUE}════════════════════════════════════════════════${NC}"
echo ""

SUCCESS_COUNT=0
FAIL_COUNT=0
CURRENT=0

START_TIME=$(date +%s)

while IFS= read -r file; do
    [ -z "$file" ] && continue
    
    CURRENT=$((CURRENT + 1))
    
    if load_file "$file" "$COLLECTION" "$LANG" "$EXTRA_META"; then
        SUCCESS_COUNT=$((SUCCESS_COUNT + 1))
    else
        FAIL_COUNT=$((FAIL_COUNT + 1))
    fi
    
    # Progress indicator
    if [ $((CURRENT % BATCH_SIZE)) -eq 0 ] || [ $CURRENT -eq $FILE_COUNT ]; then
        PROGRESS=$((CURRENT * 100 / FILE_COUNT))
        printf "\r${CYAN}Progress:${NC} [%-50s] %d%% (%d/%d files)" \
            $(printf '#%.0s' $(seq 1 $((PROGRESS / 2)))) \
            $PROGRESS $CURRENT $FILE_COUNT
    fi
done <<< "$FILES"

echo ""
echo ""

END_TIME=$(date +%s)
DURATION=$((END_TIME - START_TIME))

# Print summary
echo -e "${BLUE}════════════════════════════════════════════════${NC}"
echo -e "${GREEN}  Summary${NC}"
echo -e "${BLUE}════════════════════════════════════════════════${NC}"
echo ""
echo -e "${CYAN}Results:${NC}"
echo "  Total files:    $FILE_COUNT"
echo -e "  ${GREEN}Successful:     $SUCCESS_COUNT${NC}"
[ $FAIL_COUNT -gt 0 ] && echo -e "  ${RED}Failed:         $FAIL_COUNT${NC}"
echo "  Duration:       ${DURATION}s"
if [ $DURATION -gt 0 ]; then
    echo "  Throughput:     $(echo "scale=2; $SUCCESS_COUNT / $DURATION" | bc) files/sec"
fi
echo ""

if [ "$DRY_RUN" = true ]; then
    echo -e "${YELLOW}This was a dry run. No files were actually loaded.${NC}"
    echo -e "${YELLOW}Run without -d/--dry-run to perform the actual import.${NC}"
    echo ""
fi

if [ $FAIL_COUNT -eq 0 ]; then
    echo -e "${GREEN}✓ All files loaded successfully!${NC}"
    exit 0
else
    echo -e "${YELLOW}⚠ Some files failed to load${NC}"
    exit 1
fi
