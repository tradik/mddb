#!/usr/bin/env bash
#
# test-test-shards.sh — tests for scripts/go-test-shard.py and
# scripts/merge-coverprofiles.sh, which split the services/mddbd suite across
# CI jobs and put its coverage back together.
#
# Sharding:
#   1. every name lands in exactly one shard, for 1 to 5 shards
#   2. names that are prefixes of other names (TestA, TestAB) stay disjoint
#   3. shards are within one group of an even split
#   4. a pattern for 3,000 names stays far below Windows' 32 KB command line
#   5. a bad shard index is refused
# Merging:
#   6. a block from two shards is counted once, covered if either covered it
#   7. profiles in different modes are refused
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
SHARD="${REPO_ROOT}/scripts/go-test-shard.py"
MERGE="${REPO_ROOT}/scripts/merge-coverprofiles.sh"

RED='\033[0;31m'; GREEN='\033[0;32m'; NC='\033[0m'
PASS=0; FAIL=0
ok() { echo -e "${GREEN}✓${NC} $1"; PASS=$((PASS + 1)); }
bad() { echo -e "${RED}✗${NC} $1"; FAIL=$((FAIL + 1)); }

TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT

# Names shaped like the real suite: shared prefixes, a name that is a prefix
# of others, fuzz and example functions, and a list some shards outnumber.
{
	echo TestA; echo TestAB; echo TestABC
	for i in $(seq 1 3000); do echo "TestCase${i}"; done
	for p in Alpha Beta Gamma Delta; do for i in $(seq 1 40); do echo "Test${p}${i}"; done; done
	echo FuzzLoadDoc; echo ExampleClient
	echo "ok  	mddb	0.1s"   # go test -list prints this; it is not a test
} >"${TMP}/names"
grep -cE '^(Test|Fuzz|Example)' "${TMP}/names" >"${TMP}/total"

# 1–3: partition and balance, checked with Python's regex engine (RE2 agrees
# for these patterns: no back-references, no look-around).
for n in 1 2 3 4 5; do
	if python3 - "${SHARD}" "${TMP}/names" "${n}" <<'EOF'
import re, subprocess, sys
shard, names_file, n = sys.argv[1], sys.argv[2], int(sys.argv[3])
names = [l.strip() for l in open(names_file) if re.match(r"^(Test|Fuzz|Example)\w*$", l.strip())]
pats = []
for i in range(1, n + 1):
    out = subprocess.run([sys.executable, shard, str(n), str(i)], stdin=open(names_file),
                         capture_output=True, text=True, check=True).stdout.strip()
    pats.append(re.compile(out))
counts = [sum(1 for x in names if p.match(x)) for p in pats]
wrong = [x for x in names if sum(1 for p in pats if p.match(x)) != 1]
if wrong:
    sys.exit(f"{len(wrong)} names in != 1 shard, e.g. {wrong[:3]}")
if sum(counts) != len(names):
    sys.exit(f"counts {counts} do not add up to {len(names)}")
if max(counts) - min(counts) > max(1, len(names) // (n * 4)):
    sys.exit(f"unbalanced: {counts}")
EOF
	then ok "${n} shard(s): every name in exactly one shard, balanced"
	else bad "${n} shard(s): partition or balance"; fi
done

# 4: pattern length.
longest=0
for i in 1 2 3; do
	p="$(python3 "${SHARD}" 3 "${i}" <"${TMP}/names")"
	(( ${#p} > longest )) && longest=${#p}
done
if (( longest < 4096 )); then ok "longest pattern ${longest} bytes"; else bad "pattern ${longest} bytes"; fi

# 5: bad arguments.
if python3 "${SHARD}" 3 4 <"${TMP}/names" >/dev/null 2>&1; then bad "index 4 of 3 accepted"; else ok "index 4 of 3 refused"; fi
if python3 "${SHARD}" x 1 <"${TMP}/names" >/dev/null 2>&1; then bad "non-numeric shards accepted"; else ok "non-numeric shards refused"; fi
if [[ "$(python3 "${SHARD}" 3 3 </dev/null)" == '^$' ]]; then ok "an empty list selects nothing"; else bad "empty list"; fi

# 6: merging.
printf 'mode: atomic\nmddb/a.go:1.1,2.2 1 0\nmddb/a.go:3.1,4.2 2 5\n' >"${TMP}/p1"
printf 'mode: atomic\nmddb/a.go:1.1,2.2 1 3\nmddb/a.go:3.1,4.2 2 0\nmddb/b.go:1.1,2.2 1 0\n' >"${TMP}/p2"
bash "${MERGE}" "${TMP}/merged" "${TMP}/p1" "${TMP}/p2"
want=$'mode: atomic\nmddb/a.go:1.1,2.2 1 3\nmddb/a.go:3.1,4.2 2 5\nmddb/b.go:1.1,2.2 1 0'
if [[ "$(cat "${TMP}/merged")" == "${want}" ]]; then ok "blocks counted once, covered if any shard covered them"; else bad "merged profile: $(cat "${TMP}/merged")"; fi

# 7: mixed modes.
printf 'mode: set\nmddb/a.go:1.1,2.2 1 1\n' >"${TMP}/p3"
if bash "${MERGE}" "${TMP}/m2" "${TMP}/p1" "${TMP}/p3" 2>/dev/null; then bad "mixed modes merged"; else ok "mixed modes refused"; fi
if bash "${MERGE}" "${TMP}/m3" 2>/dev/null; then bad "no inputs accepted"; else ok "no inputs refused"; fi

echo
echo "passed: ${PASS}, failed: ${FAIL}"
[[ "${FAIL}" -eq 0 ]]
