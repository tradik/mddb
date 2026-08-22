#!/usr/bin/env bash
#
# Per-area coverage floors (TEST-002).
#
# `go test ./...` reports one number for a package of 150 files, and that number
# looked healthy while the surfaces handling file uploads, 80 MCP tools and
# automation triggers sat at 22% between them. An average over a flat package
# hides exactly the places nobody is testing.
#
# This reports coverage per source file for the areas that carry the most risk,
# and fails when one drops below its floor. The floors are ratchets: they record
# where each area stands, so a change cannot quietly take it backwards. Raise
# them when you improve an area; never lower one to make a build pass.
#
# Usage:
#   scripts/check-coverage-areas.sh [profile]     # default: coverage.out
#   scripts/check-coverage-areas.sh --print       # report, do not enforce

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PROFILE="${1:-${REPO_ROOT}/services/mddbd/coverage.out}"
PRINT_ONLY=false
[[ "${1:-}" == "--print" ]] && { PRINT_ONLY=true; PROFILE="${REPO_ROOT}/services/mddbd/coverage.out"; }

# file:floor — the percentage below which the build fails.
AREAS=(
  "upload_handler.go:70"
  "automation_trigger.go:55"
  "mcp_tools.go:49"
  "mcp_custom_tools.go:80"
  "mcp_direct_client.go:73"
  "graphql_adapter.go:92"
  "routes.go:95"
  "main.go:0"
)

if [[ ! -f "$PROFILE" ]]; then
  echo "::error::coverage profile not found at $PROFILE — run: go test -coverprofile=coverage.out ./..."
  exit 2
fi

FAILED=0
printf '%-28s %8s %8s\n' "AREA" "COVERAGE" "FLOOR"
printf '%-28s %8s %8s\n' "----" "--------" "-----"

for entry in "${AREAS[@]}"; do
  file="${entry%%:*}"
  floor="${entry##*:}"

  # Sum statements and covered statements for this file from the profile.
  read -r total covered < <(
    awk -v f="/$file:" '
      index($1, f) {
        n = $2; c = $3
        total += n
        if (c > 0) covered += n
      }
      END { printf "%d %d\n", total, covered }
    ' "$PROFILE"
  )

  if [[ "$total" -eq 0 ]]; then
    printf '%-28s %8s %8s   (not in profile)\n' "$file" "-" "$floor"
    continue
  fi

  pct=$(awk -v c="$covered" -v t="$total" 'BEGIN { printf "%.1f", 100 * c / t }')
  status=""
  if awk -v p="$pct" -v f="$floor" 'BEGIN { exit !(p < f) }'; then
    status="  BELOW FLOOR"
    FAILED=1
  fi
  printf '%-28s %7s%% %7s%%%s\n' "$file" "$pct" "$floor" "$status"
done

echo

if [[ "$PRINT_ONLY" == true ]]; then
  exit 0
fi

if [[ "$FAILED" -eq 1 ]]; then
  echo "::error::a tracked area fell below its coverage floor. Add tests for the behaviour you changed — do not lower the floor."
  exit 1
fi

echo "✓ every tracked area is at or above its floor"
