#!/usr/bin/env bash
#
# test-sonar.sh — tests for the SonarCloud scan's helpers (OPS-021):
# scripts/check-sonar-exclusions.py, scripts/sonar-coverage-paths.py and
# scripts/sonar-scan-owner.py.
#
# Exclusion lists:
#   1. the repository's two configurations are in step
#   2. a pattern in one file only is reported, from either side
#   3. an Auto Analysis exclusion may be a scanner issue-ignore resourceKey
#   4. a cpd pattern in one file only is reported
# Coverage paths:
#   5. Go import paths map to their module directory, longest module first
#   6. a Go file from a module not in the checkout is refused
#   7. LCOV SF: relative and foreign-absolute paths land in the project
#   8. cobertura <source> is moved into the checkout, filenames untouched
# Scan owner:
#   9. the real test.yml parses, and a server change is scanned by Tests
#  10. a docs- or plugin-only change, or another base branch, by sonarcloud.yml
#  11. a mixed change is scanned once, by Tests
#  12. glob rules: `*` stops at `/`, `**` does not; unsupported syntax refused
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
CHECK="${REPO_ROOT}/scripts/check-sonar-exclusions.py"
PATHS="${REPO_ROOT}/scripts/sonar-coverage-paths.py"
OWNER="${REPO_ROOT}/scripts/sonar-scan-owner.py"

RED='\033[0;31m'; GREEN='\033[0;32m'; NC='\033[0m'
PASS=0; FAIL=0
ok() { echo -e "${GREEN}✓${NC} $1"; PASS=$((PASS + 1)); }
bad() { echo -e "${RED}✗${NC} $1"; FAIL=$((FAIL + 1)); }

TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT
# The scripts accept only paths inside the repository or the working
# directory (scripts/safepath.py), so the cases run from their own directory.
cd "${TMP}"

# 1: the real files.
if [[ "$(python3 "${CHECK}" --print 2>&1)" == *"in step"* ]]; then ok "--print reports the lists in step"; else bad "--print: $(python3 "${CHECK}" --print 2>&1)"; fi
if python3 "${CHECK}" one-file >/dev/null 2>&1; then bad "one properties file accepted"; else ok "a single properties argument is refused"; fi
if python3 "${CHECK}" >/dev/null 2>&1; then ok "sonar-project.properties and .sonarcloud.properties in step"; else bad "the repository's Sonar exclusion lists differ: $(python3 "${CHECK}" 2>&1)"; fi

# 2–4: fixtures.
cat >"${TMP}/scanner" <<'EOF'
# comment
sonar.exclusions=\
  a/**,\
  b/**
sonar.cpd.exclusions=x/**
sonar.issue.ignore.multicriteria=r1
sonar.issue.ignore.multicriteria.r1.ruleKey=python:S100
sonar.issue.ignore.multicriteria.r1.resourceKey=c/**/*.py
EOF
cat >"${TMP}/auto" <<'EOF'
sonar.exclusions=\
  a/**,\
  b/**,\
  c/**/*.py
sonar.cpd.exclusions=x/**
EOF
if python3 "${CHECK}" "${TMP}/scanner" "${TMP}/auto" >/dev/null 2>&1; then ok "an issue-ignore resourceKey stands in for an Auto Analysis exclusion"; else bad "issue-ignore resourceKey not accepted"; fi

{ cat "${TMP}/auto"; echo 'sonar.exclusions=a/**,b/**,c/**/*.py,d/**'; } >"${TMP}/auto-extra"   # a later key wins
out="$(python3 "${CHECK}" "${TMP}/scanner" "${TMP}/auto-extra" 2>&1)" && rc=0 || rc=$?
if [[ ${rc} -eq 1 && "${out}" == *"d/** only in .sonarcloud.properties"* ]]; then ok "an Auto-only pattern is reported"; else bad "Auto-only pattern: rc=${rc} ${out}"; fi

sed 's/^sonar.cpd.exclusions=x\/\*\*$/sonar.cpd.exclusions=x\/**,y\/**/' "${TMP}/scanner" >"${TMP}/scanner-cpd"
out="$(python3 "${CHECK}" "${TMP}/scanner-cpd" "${TMP}/auto" 2>&1)" && rc=0 || rc=$?
if [[ ${rc} -eq 1 && "${out}" == *"sonar.cpd.exclusions: y/** only in sonar-project.properties"* ]]; then ok "a scanner-only cpd pattern is reported"; else bad "scanner-only cpd: rc=${rc} ${out}"; fi

{ cat "${TMP}/auto"; echo 'sonar.cpd.exclusions=x/**,z/**'; } >"${TMP}/auto-cpd"
out="$(python3 "${CHECK}" "${TMP}/scanner" "${TMP}/auto-cpd" 2>&1)" && rc=0 || rc=$?
if [[ ${rc} -eq 1 && "${out}" == *"sonar.cpd.exclusions: z/** only in .sonarcloud.properties"* ]]; then ok "an Auto-only cpd pattern is reported"; else bad "Auto-only cpd: rc=${rc} ${out}"; fi

grep -v '^sonar.issue' "${TMP}/scanner" >"${TMP}/scanner-noignore"
out="$(python3 "${CHECK}" "${TMP}/scanner-noignore" "${TMP}/auto" 2>&1)" && rc=0 || rc=$?
if [[ ${rc} -eq 1 && "${out}" == *"c/**/*.py only in .sonarcloud.properties"* ]]; then ok "without the issue-ignore the Auto-only pattern is reported"; else bad "missing issue-ignore: rc=${rc} ${out}"; fi

printf 'sonar.exclusions=a/**\nsonar.cpd.exclusions=x/**\n' >"${TMP}/scanner-short"
out="$(python3 "${CHECK}" "${TMP}/auto" "${TMP}/scanner-short" 2>&1)" && rc=0 || rc=$?
if [[ ${rc} -eq 1 && "${out}" == *"b/** only in sonar-project.properties"* ]]; then ok "a scanner-only pattern is reported"; else bad "scanner-only pattern: rc=${rc} ${out}"; fi

# 5–6: Go profiles.
ROOT="${TMP}/repo"
mkdir -p "${ROOT}/services/mddbd/internal/fts" "${ROOT}/services/mddb-cli" "${ROOT}/clients/go/mddb" "${ROOT}/services/mddbd/node_modules/x"
echo 'module mddb' >"${ROOT}/services/mddbd/go.mod"
printf 'module mddb-cli\n\ngo 1.27\n' >"${ROOT}/services/mddb-cli/go.mod"
echo 'module github.com/tradik/mddb/clients/go/mddb/v2' >"${ROOT}/clients/go/mddb/go.mod"
echo 'module mddb' >"${ROOT}/services/mddbd/node_modules/x/go.mod"   # skipped
cat >"${TMP}/profile" <<'EOF'
mode: atomic
mddb/util.go:1.1,2.2 1 3
mddb/internal/fts/query.go:10.5,12.2 2 0
mddb-cli/main.go:3.1,4.2 1 1
github.com/tradik/mddb/clients/go/mddb/v2/admin.go:14.2,15.1 1 1
EOF
python3 "${PATHS}" go "${ROOT}" "${TMP}/profile" "${TMP}/profile.out"
want="mode: atomic
${ROOT}/services/mddbd/util.go:1.1,2.2 1 3
${ROOT}/services/mddbd/internal/fts/query.go:10.5,12.2 2 0
${ROOT}/services/mddb-cli/main.go:3.1,4.2 1 1
${ROOT}/clients/go/mddb/admin.go:14.2,15.1 1 1"
if [[ "$(cat "${TMP}/profile.out")" == "${want}" ]]; then ok "Go import paths map to module directories (mddb vs mddb-cli kept apart)"; else bad "Go profile: $(cat "${TMP}/profile.out")"; fi

printf 'mode: set\nexample.com/other/x.go:1.1,2.2 1 1\n' >"${TMP}/foreign"
if python3 "${PATHS}" go "${ROOT}" "${TMP}/foreign" "${TMP}/foreign.out" 2>/dev/null; then bad "a module outside the checkout was accepted"; else ok "a module outside the checkout is refused"; fi

# 7: LCOV.
printf 'TN:\nSF:src/lib/a.js\nDA:1,1\nend_of_record\nSF:/home/runner/work/mddb/mddb/services/mddb-panel/src/b.jsx\nend_of_record\nSF:/elsewhere/c.js\n' >"${TMP}/lcov"
python3 "${PATHS}" lcov "${ROOT}" "${TMP}/lcov" "${TMP}/lcov.out" services/mddb-panel
want="TN:
SF:${ROOT}/services/mddb-panel/src/lib/a.js
DA:1,1
end_of_record
SF:${ROOT}/services/mddb-panel/src/b.jsx
end_of_record
SF:/elsewhere/c.js"
if [[ "$(cat "${TMP}/lcov.out")" == "${want}" ]]; then ok "LCOV SF: relative and runner-absolute paths land in the project"; else bad "LCOV: $(cat "${TMP}/lcov.out")"; fi
if python3 "${PATHS}" lcov "${ROOT}" "${TMP}/lcov" "${TMP}/x" 2>/dev/null; then bad "lcov without PROJECT_DIR accepted"; else ok "lcov without PROJECT_DIR refused"; fi
if python3 "${PATHS}" jacoco "${ROOT}" "${TMP}/lcov" "${TMP}/x" p 2>/dev/null; then bad "an unknown format accepted"; else ok "an unknown format refused"; fi

# 8: cobertura.
cat >"${TMP}/cov.xml" <<'EOF'
<?xml version="1.0" ?>
<coverage version="7.17.0"><sources><source>/home/runner/work/mddb/mddb/clients/python/mddb_client</source></sources>
<packages><package name="."><classes><class name="client.py" filename="client.py"><lines><line number="1" hits="1"/></lines></class></classes></package></packages></coverage>
EOF
python3 "${PATHS}" cobertura "${ROOT}" "${TMP}/cov.xml" "${TMP}/cov.out.xml" clients/python
if grep -q "<source>${ROOT}/clients/python/mddb_client</source>" "${TMP}/cov.out.xml" && grep -q 'filename="client.py"' "${TMP}/cov.out.xml"; then ok "cobertura <source> moved into the checkout, filenames kept"; else bad "cobertura: $(cat "${TMP}/cov.out.xml")"; fi
sed 's|/clients/python/mddb_client<|/clients/python<|' "${TMP}/cov.xml" >"${TMP}/cov-root.xml"
python3 "${PATHS}" cobertura "${ROOT}" "${TMP}/cov-root.xml" "${TMP}/cov-root.out.xml" clients/python
if grep -q "<source>${ROOT}/clients/python</source>" "${TMP}/cov-root.out.xml"; then ok "a <source> that is the project directory itself"; else bad "cobertura project root: $(cat "${TMP}/cov-root.out.xml")"; fi

# 9–11: the real test.yml.
owner() { python3 "${OWNER}" "$1" "$2"; }
if [[ "$(printf 'services/mddbd/util.go\n' | owner pull_request main)" == tests ]]; then ok "a server change is scanned by Tests"; else bad "server change"; fi
if [[ "$(printf 'scripts/sonar-scan-owner.py\n' | owner push main)" == tests ]]; then ok "a change to this script runs Tests"; else bad "script change"; fi
if [[ "$(printf 'docs/SEARCH.md\nintegrations/wordpress-plugin/mddb.php\n' | owner pull_request main)" == sonarcloud ]]; then ok "a docs/plugin change is scanned by sonarcloud.yml"; else bad "docs/plugin change"; fi
if [[ "$(printf 'services/mddbd/util.go\n' | owner pull_request release/x)" == sonarcloud ]]; then ok "a base branch Tests does not run for is scanned by sonarcloud.yml"; else bad "other base branch"; fi
if [[ "$(printf 'docs/a.md\nclients/nodejs/index.js\n' | owner pull_request main)" == tests ]]; then ok "a mixed change is scanned once, by Tests"; else bad "mixed change"; fi
if [[ "$(printf 'x\n' | owner workflow_dispatch main)" == sonarcloud ]]; then ok "an event Tests does not listen to falls to sonarcloud.yml"; else bad "unknown event"; fi

# 12: glob rules, against a fixture workflow.
cat >"${TMP}/wf.yml" <<'EOF'
name: T
on:
  pull_request:
    branches: [main]
    paths:
      - 'a/*.go'   # one level
      - "b/**"
      - 'go.work'
      - '**/README.md'
  push:
    branches: [main]
jobs: {}
EOF
o() { python3 "${OWNER}" pull_request main "${TMP}/wf.yml"; }
if [[ "$(printf 'a/x.go\n' | o)" == tests && "$(printf 'a/y/x.go\n' | o)" == sonarcloud ]]; then ok "* does not cross /"; else bad "* crossed /"; fi
if [[ "$(printf 'b/y/z/x.go\n' | o)" == tests && "$(printf 'go.work\n' | o)" == tests && "$(printf 'go.workx\n' | o)" == sonarcloud ]]; then ok "** crosses /, a plain name matches exactly"; else bad "** or exact name"; fi
if [[ "$(printf 'README.md\n' | o)" == tests && "$(printf 'x/y/README.md\n' | o)" == tests ]]; then ok "**/ matches no directory or any"; else bad "**/ prefix"; fi
if [[ "$(printf 'anything\n' | python3 "${OWNER}" push main "${TMP}/wf.yml")" == tests ]]; then ok "an event without a path filter runs for every change"; else bad "event without paths"; fi
if python3 "${OWNER}" push </dev/null >/dev/null 2>&1; then bad "a missing BRANCH accepted"; else ok "a missing BRANCH is refused"; fi
sed 's/"b\/\*\*"/"!b\/x"/' "${TMP}/wf.yml" >"${TMP}/wf-neg.yml"
if printf 'a/x.go\n' | python3 "${OWNER}" pull_request main "${TMP}/wf-neg.yml" >/dev/null 2>&1; then bad "a negated pattern was accepted"; else ok "a negated pattern is refused"; fi
sed 's/    paths:/    paths-ignore:/' "${TMP}/wf.yml" >"${TMP}/wf-ign.yml"
if printf 'a/x.go\n' | python3 "${OWNER}" pull_request main "${TMP}/wf-ign.yml" >/dev/null 2>&1; then bad "paths-ignore was accepted"; else ok "paths-ignore is refused"; fi

# Confinement (scripts/safepath.py): a path outside the repository and the
# working directory is refused by every script, before anything is read.
OUTSIDE="$(mktemp -d)"
trap 'rm -rf "${TMP}" "${OUTSIDE}"' EXIT
cp "${TMP}/scanner" "${OUTSIDE}/scanner"
out="$(python3 "${CHECK}" "${OUTSIDE}/scanner" "${TMP}/auto" 2>&1)" && rc=0 || rc=$?
if [[ ${rc} -ne 0 && "${out}" == *"outside the repository and the working directory"* ]]; then ok "check-sonar-exclusions refuses a file outside"; else bad "check outside: rc=${rc} ${out}"; fi
out="$(python3 "${PATHS}" go "${OUTSIDE}" "${OUTSIDE}/p.out" "${TMP}/q.out" 2>&1)" && rc=0 || rc=$?
if [[ ${rc} -ne 0 && "${out}" == *"outside the repository"* ]]; then ok "sonar-coverage-paths refuses a root outside"; else bad "paths outside: rc=${rc} ${out}"; fi

echo
echo "passed: ${PASS}, failed: ${FAIL}"
[[ "${FAIL}" -eq 0 ]]
