#!/usr/bin/env bash
#
# test-version.sh — tests for scripts/check-version.sh (DOC-011 guard).
#
# Covers:
#   1. the real repository agrees                    -> 0
#   2. a synthetic tree that agrees                  -> 0
#   3. one source left behind (the DOC-011 case)     -> 1
#   4. a stale package manifest (the 2.12.0 case)    -> 1
#   5. nothing to check                              -> 2
#   6. CHANGELOG state is reported, not enforced
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
GUARD="${REPO_ROOT}/scripts/check-version.sh"

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

# scaffold <dir> <version> [openapi-version]
scaffold() {
	local dir="$1" ver="$2" openapi="${3:-$2}"
	mkdir -p "${dir}/scripts" "${dir}/services/mddbd" "${dir}/docs"
	cp "${GUARD}" "${dir}/scripts/check-version.sh"
	printf 'package main\n\nconst VERSION = "%s"\n' "${ver}" > "${dir}/services/mddbd/main.go"
	printf 'info:\n  version: "%s"\n' "${openapi}" > "${dir}/docs/openapi.yaml"
	printf 'version: "%s"\n' "${ver}" > "${dir}/services/mddbd/snapcraft.yaml"
	printf 'MDDB_VERSION=%s\n' "${ver}" > "${dir}/.env.example"
	printf 'vars:\n  mddbVersion: "%s"\n' "${ver}" > "${dir}/.ssg.yaml"

	# Package manifests. Added after 2.12.0 found five of them still declaring
	# 2.11.4 because nothing was watching them.
	mkdir -p "${dir}/clients/nodejs" "${dir}/clients/python" \
		"${dir}/services/mddb-panel" "${dir}/services/php-extension" \
		"${dir}/services/python-extension"
	printf '{\n  "name": "@tradik/mddb-client",\n  "version": "%s"\n}\n' "${ver}" \
		> "${dir}/clients/nodejs/package.json"
	printf '{\n  "name": "mddb-panel",\n  "version": "%s"\n}\n' "${ver}" \
		> "${dir}/services/mddb-panel/package.json"
	printf '{\n  "name": "tradik/mddb",\n  "version": "%s"\n}\n' "${ver}" \
		> "${dir}/services/php-extension/composer.json"
	printf '[project]\nversion = "%s"\n' "${ver}" > "${dir}/clients/python/pyproject.toml"
	printf '[project]\nversion = "%s"\n' "${ver}" > "${dir}/services/python-extension/pyproject.toml"
}

# Case 0: the real repository.
expect_exit "real repository agrees" 0 bash "${GUARD}"

# Case 1: a consistent synthetic tree.
scaffold "${TMP}/ok" "3.1.4"
expect_exit "synthetic tree agrees" 0 bash "${TMP}/ok/scripts/check-version.sh"

# Case 2: the DOC-011 shape — one source left behind during a bump.
scaffold "${TMP}/drift" "3.1.4" "3.1.3"
expect_exit "one stale source is rejected" 1 bash "${TMP}/drift/scripts/check-version.sh"

# Case 3: a package manifest left behind. This is the 2.12.0 shape — the
# server, the docs and the snaps all move together while an npm or PyPI
# manifest keeps the previous release's number, and the package is published
# claiming a version that does not name the server it was built against.
scaffold "${TMP}/manifest" "3.1.4"
printf '{\n  "name": "@tradik/mddb-client",\n  "version": "3.1.3"\n}\n' \
	> "${TMP}/manifest/clients/nodejs/package.json"
expect_exit "a stale package manifest is rejected" 1 bash "${TMP}/manifest/scripts/check-version.sh"

scaffold "${TMP}/pypi" "3.1.4"
printf '[project]\nversion = "3.1.3"\n' > "${TMP}/pypi/clients/python/pyproject.toml"
expect_exit "a stale pyproject is rejected" 1 bash "${TMP}/pypi/scripts/check-version.sh"

# Case 4: nothing to check at all.
mkdir -p "${TMP}/empty/scripts"
cp "${GUARD}" "${TMP}/empty/scripts/check-version.sh"
expect_exit "empty tree reports nothing-found" 2 bash "${TMP}/empty/scripts/check-version.sh"

# Case 4: the CHANGELOG state is reported but never fails the guard — a version
# legitimately sits under [Unreleased] until the release is cut.
scaffold "${TMP}/nochangelog" "3.1.4"
out="$(bash "${TMP}/nochangelog/scripts/check-version.sh" 2>&1)"
if grep -q "move it before tagging" <<<"${out}"; then
	echo -e "${GREEN}✓${NC} an unreleased version is reported, not rejected"; PASS=$((PASS + 1))
else
	echo -e "${RED}✗${NC} expected the CHANGELOG hint, got: ${out}"; FAIL=$((FAIL + 1))
fi

printf 'version: "3.1.4"\n' > "${TMP}/nochangelog/services/mddb-cli-snapcraft-unused.yaml"
cat > "${TMP}/nochangelog/CHANGELOG.md" <<'EOF'
# Changelog

## [3.1.4] - 2026-08-22
- released
EOF
out="$(bash "${TMP}/nochangelog/scripts/check-version.sh" 2>&1)"
if grep -q "ready to tag" <<<"${out}"; then
	echo -e "${GREEN}✓${NC} a dated CHANGELOG section is recognised"; PASS=$((PASS + 1))
else
	echo -e "${RED}✗${NC} expected the ready-to-tag hint, got: ${out}"; FAIL=$((FAIL + 1))
fi

echo "---"
echo "Passed: ${PASS}, Failed: ${FAIL}"
[[ "${FAIL}" -eq 0 ]]
