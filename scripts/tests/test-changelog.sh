#!/usr/bin/env bash
#
# test-changelog.sh — tests for scripts/check-changelog.sh (DOC-014 guard).
#
# Covers every branch of the guard:
#   1. the real CHANGELOG passes
#   2. two [Unreleased] sections      -> exit 1 (the DOC-014 regression)
#   3. [Unreleased] below a release   -> exit 1
#   4. an undated 2.x release         -> exit 1
#   5. undated pre-tag history (1.0.0/0.1.0) is tolerated -> exit 0
#   6. a missing file                 -> exit 2
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
GUARD="${REPO_ROOT}/scripts/check-changelog.sh"

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

write() { printf '%s\n' "$@" > "${TMP}/${1##*/}"; }

# Case 0: the real file must pass.
expect_exit "real CHANGELOG is well-formed" 0 bash "${GUARD}"

# Case 1: a well-formed synthetic file.
cat > "${TMP}/ok.md" <<'EOF'
# Changelog

## [Unreleased]

### Added
- something

## [2.1.0] - 2026-01-02

### Added
- shipped
EOF
expect_exit "synthetic well-formed file" 0 bash "${GUARD}" "${TMP}/ok.md"

# Case 2: the DOC-014 regression — a second Unreleased buried in the history.
cat > "${TMP}/dup.md" <<'EOF'
# Changelog

## [Unreleased]
- current work

## [2.1.0] - 2026-01-02
- shipped

## [Unreleased]
- work that shipped long ago but was never versioned

## [2.0.0] - 2025-11-07
- older
EOF
expect_exit "second [Unreleased] is rejected" 1 bash "${GUARD}" "${TMP}/dup.md"

# Case 3: Unreleased present but not first.
cat > "${TMP}/order.md" <<'EOF'
# Changelog

## [2.1.0] - 2026-01-02
- shipped

## [Unreleased]
- current work
EOF
expect_exit "[Unreleased] below a release is rejected" 1 bash "${GUARD}" "${TMP}/order.md"

# Case 4: a 2.x release with no date.
cat > "${TMP}/undated.md" <<'EOF'
# Changelog

## [Unreleased]
- current work

## [2.1.0]
- shipped, but when?
EOF
expect_exit "undated 2.x release is rejected" 1 bash "${GUARD}" "${TMP}/undated.md"

# Case 5: pre-tag history without dates is tolerated — those sections predate
# the repository and inventing dates for them would be worse than leaving them.
cat > "${TMP}/legacy.md" <<'EOF'
# Changelog

## [Unreleased]
- current work

## [2.0.0] - 2025-11-07
- shipped

## [1.0.0] - Initial Release
- hand-written history

## [0.1.0] - 2024-11-06
- older still
EOF
expect_exit "undated pre-tag history is tolerated" 0 bash "${GUARD}" "${TMP}/legacy.md"

# Case 6: missing file.
expect_exit "missing file reports not-found" 2 bash "${GUARD}" "${TMP}/nope.md"

echo "---"
echo "Passed: ${PASS}, Failed: ${FAIL}"
[[ "${FAIL}" -eq 0 ]]
