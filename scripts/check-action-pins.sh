#!/usr/bin/env bash
#
# check-action-pins.sh — every GitHub Action must be pinned to a commit SHA
# (OPS-018).
#
# A tag is a mutable pointer. `uses: some/action@v3` runs whatever that tag
# points at when the workflow runs, and the owner — or anyone who takes over
# the account — can move it. That has happened: tj-actions/changed-files in
# March 2025 had every one of its tags repointed at a commit that dumped CI
# secrets into the build log, and thousands of repositories picked it up within
# hours without changing a line of their own.
#
# A 40-character SHA cannot be repointed. The cost is that upgrades are no
# longer silent, which is the point: Dependabot proposes them as pull requests
# and someone reads the diff.
#
# The version stays in a trailing comment (`@<sha> # v7`) so the pin is
# readable and so Dependabot keeps it current.
#
# Usage:
#   scripts/check-action-pins.sh [--print]
#
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${ROOT}"

PRINT=0
[[ "${1:-}" == "--print" ]] && PRINT=1

WORKFLOWS=".github/workflows"
if [[ ! -d "${WORKFLOWS}" ]]; then
	echo "check-action-pins: no ${WORKFLOWS} directory — nothing to verify" >&2
	exit 2
fi

unpinned=()
pinned=0

while IFS= read -r line; do
	file="${line%%:*}"
	rest="${line#*:}"
	lineno="${rest%%:*}"
	ref="$(sed -E 's/.*uses:[[:space:]]*//; s/[[:space:]]*(#.*)?$//' <<<"${rest#*:}")"

	# A local action (./path) is this repository's own code, already reviewed
	# by whoever merged it.
	[[ "${ref}" == ./* ]] && continue
	# A reusable workflow from this repository is likewise our own.
	[[ "${ref}" == .github/* ]] && continue
	# A container action names an image, not a git ref.
	[[ "${ref}" == docker://* ]] && continue

	if [[ "${ref}" =~ @[0-9a-f]{40}$ ]]; then
		pinned=$((pinned + 1))
		[[ ${PRINT} -eq 1 ]] && echo "  ok      ${file}:${lineno} ${ref}"
	else
		unpinned+=("${file}:${lineno} ${ref}")
	fi
done < <(grep -rniE "^[[:space:]]*(-[[:space:]]+)?uses:" "${WORKFLOWS}"/*.yml "${WORKFLOWS}"/*.yaml 2>/dev/null || true)

if [[ ${pinned} -eq 0 && ${#unpinned[@]} -eq 0 ]]; then
	echo "check-action-pins: no action references found — nothing to verify" >&2
	exit 2
fi

if [[ ${#unpinned[@]} -gt 0 ]]; then
	echo "✗ ${#unpinned[@]} action(s) pinned to a mutable tag:" >&2
	printf '  %s\n' "${unpinned[@]}" >&2
	cat >&2 <<'HINT'

  Pin each to the commit its tag points at today:

    gh api repos/OWNER/REPO/git/ref/tags/TAG --jq '.object.sha'
    # if that reports an annotated tag object, dereference it:
    gh api repos/OWNER/REPO/git/tags/SHA --jq '.object.sha'

  then write it as:

    uses: OWNER/REPO@<40-char-sha>  # vTAG

  keeping the version in the comment so Dependabot can update both together.
HINT
	exit 1
fi

echo "✓ all ${pinned} action references are pinned to a commit SHA"
