#!/usr/bin/env bash
#
# test-govulncheck-gate.sh — tests for scripts/govulncheck-gate.py.
#
# Covers:
#   1. no findings                                   -> 0
#   2. a reachable finding with no ignore entry      -> 3
#   3. the same finding ignored at its exact version -> 0
#   4. ignored at another version                    -> 3
#   5. a finding only in a required module           -> 0 (not reachable)
#   6. the repository's own ignore file parses       -> 0
#   7. an ignore entry without a reason              -> 1
#
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
GATE="${REPO_ROOT}/scripts/govulncheck-gate.py"
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT

RED='\033[0;31m'; GREEN='\033[0;32m'; NC='\033[0m'
PASS=0; FAIL=0

check() {
	local name="$1" want="$2" input="$3" ignores="$4" got=0
	python3 "$GATE" "$ignores" <"$input" >/dev/null 2>&1 || got=$?
	if [ "$got" = "$want" ]; then
		printf "${GREEN}PASS${NC} %s\n" "$name"; PASS=$((PASS + 1))
	else
		printf "${RED}FAIL${NC} %s (exit %s, want %s)\n" "$name" "$got" "$want"; FAIL=$((FAIL + 1))
	fi
}

finding() { # osv module version level(function|package)
	printf '{"finding":{"osv":"%s","trace":[{"module":"%s","version":"%s","%s":"x"}]}}\n' "$1" "$2" "$3" "$4"
}

: >"$TMP/empty.json"
finding GO-1 example.com/m v1.0.0 function >"$TMP/called.json"
finding GO-1 example.com/m v1.0.0 package >"$TMP/imported.json"
: >"$TMP/none.txt"
echo 'GO-1 example.com/m v1.0.0  # tested' >"$TMP/exact.txt"
echo 'GO-1 example.com/m v1.0.1  # tested' >"$TMP/other.txt"
echo 'GO-1 example.com/m v1.0.0' >"$TMP/noreason.txt"

check "no findings"                         0 "$TMP/empty.json"    "$TMP/none.txt"
check "reachable, not ignored"              3 "$TMP/called.json"   "$TMP/none.txt"
check "ignored at its exact version"        0 "$TMP/called.json"   "$TMP/exact.txt"
check "ignored at another version"          3 "$TMP/called.json"   "$TMP/other.txt"
check "required but not reachable"          0 "$TMP/imported.json" "$TMP/none.txt"
check "repository ignore file parses"       0 "$TMP/empty.json"    "${REPO_ROOT}/.github/govulncheck-ignore.txt"
check "entry without a reason is refused"   1 "$TMP/empty.json"    "$TMP/noreason.txt"

echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ]
