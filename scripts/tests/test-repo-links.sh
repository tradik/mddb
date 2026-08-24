#!/usr/bin/env bash
#
# test-repo-links.sh — tests for scripts/check-repo-links.sh.
#
# Covers:
#   1. the real repository resolves            -> 0
#   2. a synthetic tree that resolves          -> 0
#   3. a link to a missing file                -> 1
#   4. schemes and anchors are out of scope    -> 0
#   5. site-absolute paths are out of scope    -> 0
#   6. nothing to check                        -> 2
#
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
GUARD="${REPO_ROOT}/scripts/check-repo-links.sh"

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

# The guard walks `git ls-files`, so a synthetic tree needs to be a repository.
scaffold() {
	local dir="$1"
	mkdir -p "${dir}/scripts/tests" "${dir}/docs"
	cp "${GUARD}" "${dir}/scripts/check-repo-links.sh"
	git -C "${dir}" init -q 2>/dev/null
	git -C "${dir}" config user.email t@example.test
	git -C "${dir}" config user.name test
}

commit() {
	git -C "$1" add -A >/dev/null 2>&1
	git -C "$1" -c commit.gpgsign=false commit -qm fixture >/dev/null 2>&1
}

# Case 0: the real repository.
expect_exit "real repository resolves" 0 bash "${GUARD}"

# Case 1: a synthetic tree where every link resolves.
scaffold "${TMP}/ok"
printf '# Docs\n' > "${TMP}/ok/docs/GUIDE.md"
printf '# Readme\n\nSee [the guide](docs/GUIDE.md).\n' > "${TMP}/ok/README.md"
commit "${TMP}/ok"
expect_exit "synthetic tree resolves" 0 bash "${TMP}/ok/scripts/check-repo-links.sh"

# Case 2: the README.md shape — a link to a page that was never written.
scaffold "${TMP}/dead"
printf '# Readme\n\nSee [performance](docs/PERFORMANCE.md).\n' > "${TMP}/dead/README.md"
commit "${TMP}/dead"
expect_exit "a link to a missing file is rejected" 1 bash "${TMP}/dead/scripts/check-repo-links.sh"

# Case 3: things that are not paths into this tree.
scaffold "${TMP}/schemes"
printf '# Docs\n' > "${TMP}/schemes/docs/GUIDE.md"
cat > "${TMP}/schemes/README.md" <<'MD'
# Readme

- [external](https://example.test/page)
- [mail](mailto:someone@example.test)
- [an anchor](#section)
- [a blocked scheme](javascript:alert(1))
- [the guide](docs/GUIDE.md)
MD
commit "${TMP}/schemes"
expect_exit "schemes and anchors are out of scope" 0 bash "${TMP}/schemes/scripts/check-repo-links.sh"

# Case 4: site-absolute paths belong to the published site, not this tree.
scaffold "${TMP}/site"
printf '# Docs\n' > "${TMP}/site/docs/GUIDE.md"
printf '# Readme\n\n[on the site](/docs/mcp/) and [here](docs/GUIDE.md)\n' > "${TMP}/site/README.md"
commit "${TMP}/site"
expect_exit "site-absolute paths are out of scope" 0 bash "${TMP}/site/scripts/check-repo-links.sh"

# Case 5: nothing to check.
scaffold "${TMP}/empty"
printf '# Readme\n\nNo links here.\n' > "${TMP}/empty/README.md"
commit "${TMP}/empty"
expect_exit "no relative links reports nothing-found" 2 bash "${TMP}/empty/scripts/check-repo-links.sh"

# Case 6: the failure has to name the offender.
out="$(bash "${TMP}/dead/scripts/check-repo-links.sh" 2>&1 || true)"
if grep -q "docs/PERFORMANCE.md" <<<"${out}"; then
	echo -e "${GREEN}✓${NC} the failure names the dead link"; PASS=$((PASS + 1))
else
	echo -e "${RED}✗${NC} expected the dead link in the output, got: ${out}"; FAIL=$((FAIL + 1))
fi

echo "---"
echo "Passed: ${PASS}, Failed: ${FAIL}"
[[ ${FAIL} -eq 0 ]]
