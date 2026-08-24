#!/usr/bin/env bash
#
# test-source-encoding.sh — tests for scripts/check-source-encoding.sh (WIN-004).
#
# Covers every branch of the guard:
#   0. the real repository passes
#   1. UTF-16 LE (what PowerShell's `>` produces)  -> exit 1
#   2. UTF-16 BE                                   -> exit 1
#   3. UTF-32 LE                                   -> exit 1
#   4. a UTF-8 BOM in a .go file                   -> exit 1
#   5. a UTF-8 BOM in a .md file                   -> exit 0 (tolerated)
#   6. plain UTF-8, including non-ASCII            -> exit 0
#   7. a binary extension starting with BOM bytes  -> exit 0 (skipped)
#   8. an empty file                               -> exit 0
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
GUARD="${REPO_ROOT}/scripts/check-source-encoding.sh"

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

# Case 0: the repository as it stands.
expect_exit "the real repository is plain UTF-8" 0 bash "${GUARD}"

# Case 1: UTF-16 LE with a BOM — what `"x" > file.go` produces in PowerShell,
# and the exact shape that makes Go say "illegal UTF-8 encoding".
printf '\xff\xfepackage main\n' > "${TMP}/utf16le.go"
expect_exit "UTF-16 LE is rejected" 1 bash "${GUARD}" "${TMP}/utf16le.go"

# Case 2: UTF-16 BE.
printf '\xfe\xffpackage main\n' > "${TMP}/utf16be.go"
expect_exit "UTF-16 BE is rejected" 1 bash "${GUARD}" "${TMP}/utf16be.go"

# Case 3: UTF-32 LE. Its BOM begins with the UTF-16 LE one, so this also checks
# the guard reads four bytes rather than two and names the right encoding.
printf '\xff\xfe\x00\x00package main\n' > "${TMP}/utf32le.go"
expect_exit "UTF-32 LE is rejected" 1 bash "${GUARD}" "${TMP}/utf32le.go"

# Case 4: a UTF-8 BOM in Go source. The compiler tolerates it; a leading
# //go:build line does not, and the three bytes are invisible in every editor.
printf '\xef\xbb\xbf//go:build windows\n\npackage main\n' > "${TMP}/bom.go"
expect_exit "a UTF-8 BOM in Go source is rejected" 1 bash "${GUARD}" "${TMP}/bom.go"

# Case 5: the same BOM in Markdown. Ugly, harmless, and not worth failing a
# build over — plenty of tools emit it and nothing here parses build
# constraints out of prose.
printf '\xef\xbb\xbf# Title\n' > "${TMP}/bom.md"
expect_exit "a UTF-8 BOM in Markdown is tolerated" 0 bash "${GUARD}" "${TMP}/bom.md"

# Case 6: plain UTF-8 with multi-byte characters, which must not be mistaken
# for an encoding problem.
printf 'package main // zażółć gęślą jaźń — ünïcödé\n' > "${TMP}/plain.go"
expect_exit "plain UTF-8 with non-ASCII passes" 0 bash "${GUARD}" "${TMP}/plain.go"

# Case 7: a binary file whose first bytes happen to match a BOM. Skipped by
# extension, because "starts with FF FE" is meaningless outside text.
printf '\xff\xfe\x00\x01binary payload' > "${TMP}/asset.png"
expect_exit "binary extensions are skipped" 0 bash "${GUARD}" "${TMP}/asset.png"

# Case 8: an empty file has no first bytes to judge.
: > "${TMP}/empty.go"
expect_exit "an empty file passes" 0 bash "${GUARD}" "${TMP}/empty.go"

echo
echo "passed: ${PASS}, failed: ${FAIL}"
[[ "${FAIL}" -eq 0 ]]
