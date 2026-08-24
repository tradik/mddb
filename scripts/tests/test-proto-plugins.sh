#!/usr/bin/env bash
#
# test-proto-plugins.sh — tests for scripts/check-proto-plugins.sh.
#
# The guard exists because buf.gen.yaml claimed in a comment that its
# protocolbuffers/go plugin matched go.mod's protobuf runtime, and the two had
# silently drifted. A guard that only ever sees the agreeing case proves
# nothing, so the drift case is tested against a synthetic tree.
#
# Covers:
#   1. the real repository agrees                  -> 0
#   2. a synthetic tree that agrees                -> 0
#   3. plugin left behind by a runtime bump        -> 1
#   4. runtime left behind by a plugin bump        -> 1
#   5. missing buf.gen.yaml                        -> 1
#   6. no plugin pin in buf.gen.yaml               -> 1
#   7. --print reports both versions
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
GUARD="${REPO_ROOT}/scripts/check-proto-plugins.sh"

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

# make_tree <dir> <plugin-version> <runtime-version>
make_tree() {
	local dir="$1" plugin="$2" runtime="$3"
	mkdir -p "${dir}/scripts" "${dir}/services/mddbd"
	cp "${GUARD}" "${dir}/scripts/check-proto-plugins.sh"
	cat > "${dir}/buf.gen.yaml" <<EOF
version: v2
plugins:
  - remote: buf.build/protocolbuffers/go:v${plugin}
    out: services/mddbd/proto
EOF
	cat > "${dir}/services/mddbd/go.mod" <<EOF
module mddb

go 1.27.0

require (
	google.golang.org/protobuf v${runtime}
)
EOF
}

run_tree() {
	local dir="$1" got=0
	bash "${dir}/scripts/check-proto-plugins.sh" >/dev/null 2>&1 || got=$?
	echo "${got}"
}

TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT

# 1. the real repository
got=0; bash "${GUARD}" >/dev/null 2>&1 || got=$?
report "real repository: plugin and runtime agree" 0 "${got}"

# 2. synthetic agreeing tree
make_tree "${TMP}/ok" "1.36.12" "1.36.12"
report "synthetic tree that agrees" 0 "$(run_tree "${TMP}/ok")"

# 3. the drift this guard was written for
make_tree "${TMP}/stale-plugin" "1.36.11" "1.36.12"
report "plugin left behind by a runtime bump" 1 "$(run_tree "${TMP}/stale-plugin")"

# 4. the same drift the other way round
make_tree "${TMP}/stale-runtime" "1.37.0" "1.36.12"
report "runtime left behind by a plugin bump" 1 "$(run_tree "${TMP}/stale-runtime")"

# 5. missing buf.gen.yaml
make_tree "${TMP}/no-gen" "1.36.12" "1.36.12"
rm "${TMP}/no-gen/buf.gen.yaml"
report "missing buf.gen.yaml is an error, not a pass" 1 "$(run_tree "${TMP}/no-gen")"

# 6. buf.gen.yaml without the pin — a rename must not silently disable the guard
make_tree "${TMP}/no-pin" "1.36.12" "1.36.12"
cat > "${TMP}/no-pin/buf.gen.yaml" <<'EOF'
version: v2
plugins:
  - remote: buf.build/protocolbuffers/python:v31.1
    out: clients/python
EOF
report "no Go plugin pin is an error, not a pass" 1 "$(run_tree "${TMP}/no-pin")"

# 7. --print output
if bash "${GUARD}" --print 2>/dev/null | grep -q "protobuf runtime"; then
	echo -e "${GREEN}✓${NC} --print reports both versions"; PASS=$((PASS + 1))
else
	echo -e "${RED}✗${NC} --print does not report both versions"; FAIL=$((FAIL + 1))
fi

echo
echo "passed ${PASS}, failed ${FAIL}"
[[ "${FAIL}" -eq 0 ]]
