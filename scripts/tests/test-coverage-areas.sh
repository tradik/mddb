#!/usr/bin/env bash
#
# test-coverage-areas.sh — tests for scripts/check-coverage-areas.sh (TEST-002).
#
# A guard that only ever sees the passing case proves nothing, so the failing
# case is exercised against a synthetic profile.
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
GUARD="${REPO_ROOT}/scripts/check-coverage-areas.sh"

RED='\033[0;31m'; GREEN='\033[0;32m'; NC='\033[0m'
PASS=0; FAIL=0

report() {
	local name="$1" want="$2" got="$3"
	if [[ "${got}" -eq "${want}" ]]; then
		echo -e "${GREEN}✓${NC} ${name}"; PASS=$((PASS + 1))
	else
		echo -e "${RED}✗${NC} ${name} (exit ${got}, want ${want})"; FAIL=$((FAIL + 1))
	fi
}

TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT

# write_profile <file> <covered-statements> <uncovered-statements>
# Emits a Go coverage profile for upload_handler.go with the given split.
write_profile() {
	local out="$1" covered="$2" uncovered="$3"
	echo "mode: set" > "$out"
	local i
	for ((i = 0; i < covered; i++)); do
		echo "mddb/upload_handler.go:$((i + 1)).1,$((i + 1)).9 1 1" >> "$out"
	done
	for ((i = 0; i < uncovered; i++)); do
		echo "mddb/upload_handler.go:$((100 + i)).1,$((100 + i)).9 1 0" >> "$out"
	done
}

# 1. well above the floor
write_profile "${TMP}/high.out" 95 5
got=0; bash "${GUARD}" "${TMP}/high.out" >/dev/null 2>&1 || got=$?
report "an area above its floor passes" 0 "${got}"

# 2. below the floor — the case the guard exists for
write_profile "${TMP}/low.out" 10 90
got=0; bash "${GUARD}" "${TMP}/low.out" >/dev/null 2>&1 || got=$?
report "an area below its floor fails" 1 "${got}"

# 3. exactly at the floor is not a failure
write_profile "${TMP}/edge.out" 70 30
got=0; bash "${GUARD}" "${TMP}/edge.out" >/dev/null 2>&1 || got=$?
report "an area exactly at its floor passes" 0 "${got}"

# 4. a missing profile must be an error, not a silent pass
got=0; bash "${GUARD}" "${TMP}/does-not-exist.out" >/dev/null 2>&1 || got=$?
report "a missing profile is an error, not a pass" 2 "${got}"

# 5. the failure message must say which area and that lowering is not the fix
write_profile "${TMP}/msg.out" 10 90
# Captured rather than piped: the guard exits non-zero here, and under
# `set -o pipefail` that status would mask grep's.
output="$(bash "${GUARD}" "${TMP}/msg.out" 2>&1 || true)"
if grep -q "BELOW FLOOR" <<< "$output"; then
	echo -e "${GREEN}✓${NC} the report names the failing area"; PASS=$((PASS + 1))
else
	echo -e "${RED}✗${NC} the report does not name the failing area"; FAIL=$((FAIL + 1))
fi

# 6. --print reports without enforcing
got=0; bash "${GUARD}" --print >/dev/null 2>&1 || got=$?
report "--print does not enforce" 0 "${got}"

echo
echo "passed ${PASS}, failed ${FAIL}"
[[ "${FAIL}" -eq 0 ]]
