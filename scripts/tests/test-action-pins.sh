#!/usr/bin/env bash
#
# test-action-pins.sh — tests for scripts/check-action-pins.sh (OPS-018 guard).
#
# Covers:
#   1. the real repository is fully pinned            -> 0
#   2. a synthetic tree that is fully pinned          -> 0
#   3. one action left on a tag                       -> 1
#   4. a short SHA is not a pin                       -> 1
#   5. local and container actions are exempt         -> 0
#   6. no workflows at all                            -> 2
#
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
GUARD="${REPO_ROOT}/scripts/check-action-pins.sh"

RED='\033[0;31m'; GREEN='\033[0;32m'; NC='\033[0m'
PASS=0; FAIL=0

expect_exit() {
	local name="$1" want="$2"; shift 2
	local got=0
	"$@" >/dev/null 2>&1 || got=$?
	if [[ "${got}" -eq "${want}" ]]; then
		echo -e "${GREEN}✓${NC} ${name} (exit ${got})"; PASS=$((PASS + 1))
	else
		echo -e "${RED}✗${NC} ${name}: expected exit ${want}, got ${got}"; FAIL=$((FAIL + 1))
	fi
}

TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT

SHA="3d3c42e5aac5ba805825da76410c181273ba90b1"

scaffold() {
	local dir="$1"
	mkdir -p "${dir}/scripts" "${dir}/.github/workflows"
	cp "${GUARD}" "${dir}/scripts/check-action-pins.sh"
}

# Case 0: the real repository.
expect_exit "real repository is fully pinned" 0 bash "${GUARD}"

# Case 1: a synthetic tree where everything is pinned.
scaffold "${TMP}/ok"
cat > "${TMP}/ok/.github/workflows/ci.yml" <<YAML
jobs:
  build:
    steps:
      - uses: actions/checkout@${SHA} # v7
      - uses: actions/setup-go@${SHA} # v7
YAML
expect_exit "synthetic tree is pinned" 0 bash "${TMP}/ok/scripts/check-action-pins.sh"

# Case 2: the OPS-018 shape — one action still on a mutable tag.
scaffold "${TMP}/tag"
cat > "${TMP}/tag/.github/workflows/ci.yml" <<YAML
jobs:
  build:
    steps:
      - uses: actions/checkout@${SHA} # v7
      - uses: some/third-party-action@v3
YAML
expect_exit "one action left on a tag is rejected" 1 bash "${TMP}/tag/scripts/check-action-pins.sh"

# Case 3: an abbreviated SHA. Git resolves it, but it is not unique forever and
# is not what the pin promises.
scaffold "${TMP}/short"
cat > "${TMP}/short/.github/workflows/ci.yml" <<YAML
jobs:
  build:
    steps:
      - uses: actions/checkout@3d3c42e # v7
YAML
expect_exit "an abbreviated SHA is not a pin" 1 bash "${TMP}/short/scripts/check-action-pins.sh"

# Case 4: references that are not third-party code at all.
scaffold "${TMP}/local"
cat > "${TMP}/local/.github/workflows/ci.yml" <<YAML
jobs:
  build:
    steps:
      - uses: ./integrations/github-action
      - uses: docker://alpine:3.20
      - uses: actions/checkout@${SHA} # v7
YAML
expect_exit "local and container actions are exempt" 0 bash "${TMP}/local/scripts/check-action-pins.sh"

# Case 5: nothing to check.
mkdir -p "${TMP}/empty/scripts"
cp "${GUARD}" "${TMP}/empty/scripts/check-action-pins.sh"
expect_exit "no workflows reports nothing-found" 2 bash "${TMP}/empty/scripts/check-action-pins.sh"

# Case 6: the failure message must tell you how to fix it, not just that you
# are wrong.
scaffold "${TMP}/hint"
cat > "${TMP}/hint/.github/workflows/ci.yml" <<YAML
jobs:
  build:
    steps:
      - uses: some/action@v1
YAML
out="$(bash "${TMP}/hint/scripts/check-action-pins.sh" 2>&1 || true)"
if grep -q "gh api repos/OWNER/REPO" <<<"${out}"; then
	echo -e "${GREEN}✓${NC} the failure explains how to pin"; PASS=$((PASS + 1))
else
	echo -e "${RED}✗${NC} expected a fix hint, got: ${out}"; FAIL=$((FAIL + 1))
fi

echo "---"
echo "Passed: ${PASS}, Failed: ${FAIL}"
[[ ${FAIL} -eq 0 ]]
