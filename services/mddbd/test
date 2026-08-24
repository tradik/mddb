#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'
BOLD='\033[1m'

PASS=0
FAIL=0
SKIP=0

step() { printf "\n${CYAN}${BOLD}▸ %s${NC}\n" "$1"; }
ok()   { printf "  ${GREEN}✓ %s${NC}\n" "$1"; PASS=$((PASS+1)); }
fail() { printf "  ${RED}✗ %s${NC}\n" "$1"; FAIL=$((FAIL+1)); }
skip() { printf "  ${YELLOW}⊘ %s (not installed)${NC}\n" "$1"; SKIP=$((SKIP+1)); }

# ── 1. Build ──────────────────────────────────────────────
step "Build"
if go build ./... 2>&1; then
    ok "go build ./..."
else
    fail "go build ./..."
fi

# ── 2. Fmt ────────────────────────────────────────────────
step "Format (go fmt)"
UNFMT=$(gofmt -l . 2>&1 || true)
if [ -z "$UNFMT" ]; then
    ok "All files formatted"
else
    fail "Unformatted files:"
    echo "$UNFMT" | while read -r f; do printf "    %s\n" "$f"; done
fi

# ── 3. Vet ────────────────────────────────────────────────
step "Vet (go vet)"
if go vet ./... 2>&1; then
    ok "go vet ./..."
else
    fail "go vet ./..."
fi

# ── 4. Tests + Coverage ──────────────────────────────────
step "Tests + Coverage"
COVER_DIR="$(mktemp -d)"
COVER_FILE="${COVER_DIR}/coverage.out"

if go test ./... -coverprofile="$COVER_FILE" -covermode=atomic -timeout 120s -count=1 2>&1; then
    ok "All tests passed"

    # Coverage summary (total only — open HTML for details)
    TOTAL=$(go tool cover -func="$COVER_FILE" | grep "^total:" | awk '{print $NF}')
    printf "  ${BOLD}Total coverage: ${CYAN}%s${NC}\n" "$TOTAL"

    # Generate HTML report
    HTML_REPORT="coverage.html"
    go tool cover -html="$COVER_FILE" -o "$HTML_REPORT" 2>/dev/null && \
        printf "  ${GREEN}HTML report: %s${NC}\n" "$HTML_REPORT"
else
    fail "Tests failed"
fi
rm -rf "$COVER_DIR"

# ── 5. Security (gosec) ──────────────────────────────────
step "Security (gosec)"
if command -v gosec &>/dev/null; then
    # Excluded rules:
    # G104: unhandled errors (too noisy for _ = patterns)
    # G114: http.ListenAndServe without timeouts (pre-existing, acceptable for internal use)
    # G115: integer overflow conversions (intentional uint64->int64 in serialization)
    # G302: file perms > 0600 (data files like binlog/wal use 0644 by design)
    # G304: file path from variable (expected for DB paths)
    GOSEC_OUT=$(gosec -quiet -exclude=G103,G104,G114,G115,G302,G304 -exclude-dir=proto ./... 2>&1 || true)
    if echo "$GOSEC_OUT" | grep -q "Severity:"; then
        ISSUES=$(echo "$GOSEC_OUT" | grep -c "Severity:" || echo "0")
        fail "gosec found $ISSUES issue(s)"
        echo "$GOSEC_OUT" | head -40
    else
        ok "No security issues"
    fi
else
    skip "gosec"
fi

# ── 6. Lint (golangci-lint) ──────────────────────────────
step "Lint (golangci-lint)"
if command -v golangci-lint &>/dev/null; then
    # golangci-lint v2 uses different flag syntax
    LINT_VER=$(golangci-lint version 2>&1 | head -1)
    if echo "$LINT_VER" | grep -q "version 2"; then
        # v2: uses run with default linters
        LINT_OUT=$(golangci-lint run --timeout 5m ./... 2>&1 || true)
    else
        # v1: explicit linter selection
        LINT_OUT=$(golangci-lint run --timeout 5m \
            --disable-all \
            --enable errcheck,govet,ineffassign,staticcheck,unused,gosimple,typecheck \
            ./... 2>&1 || true)
    fi
    # Filter: info lines, empty lines, generated proto, summary lines
    LINT_ISSUES=$(echo "$LINT_OUT" | grep -v "^level=" | grep -v "^$" | grep -v "proto/" | grep -v "^[0-9]* issues" | grep -v "^\*" || true)
    if [ -z "$LINT_ISSUES" ]; then
        ok "golangci-lint passed"
    else
        LINT_COUNT=$(echo "$LINT_ISSUES" | wc -l | tr -d ' ')
        fail "golangci-lint: $LINT_COUNT issue(s)"
        echo "$LINT_ISSUES" | head -20
    fi
else
    skip "golangci-lint"
fi

# ── 7. Staticcheck ───────────────────────────────────────
step "Staticcheck"
if command -v staticcheck &>/dev/null; then
    # Filter out version mismatch warnings from dependencies
    SC_OUT=$(staticcheck ./... 2>&1 | grep -v "requires newer Go version" || true)
    if [ -z "$SC_OUT" ]; then
        ok "staticcheck passed"
    else
        SC_COUNT=$(echo "$SC_OUT" | wc -l | tr -d ' ')
        fail "staticcheck: $SC_COUNT issue(s)"
        echo "$SC_OUT" | head -20
    fi
else
    skip "staticcheck"
fi

# ── Summary ──────────────────────────────────────────────
printf "\n${BOLD}━━━ Summary ━━━${NC}\n"
printf "  ${GREEN}Passed: %d${NC}  ${RED}Failed: %d${NC}  ${YELLOW}Skipped: %d${NC}\n\n" "$PASS" "$FAIL" "$SKIP"

if [ "$FAIL" -gt 0 ]; then
    printf "${RED}${BOLD}FAILED${NC}\n"
    exit 1
else
    printf "${GREEN}${BOLD}ALL CHECKS PASSED${NC}\n"
    exit 0
fi
