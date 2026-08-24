#!/usr/bin/env bash
#
# check-changelog.sh — guard the CHANGELOG's structure (DOC-014).
#
# The file is hand-edited by several people and tools, and it drifted twice in
# ways nothing caught:
#   * a second "## [Unreleased]" appeared mid-history, so recent work was
#     described between 2025 releases, and any tool that reads "the Unreleased
#     section" could pick the wrong one;
#   * an old Unreleased section was never converted into a version when the
#     work it described shipped.
#
# So: exactly one Unreleased heading, and it must be the first version heading
# in the file. Every other "## [x.y.z]" must carry a date.
#
# Usage:
#   scripts/check-changelog.sh [path]   # defaults to CHANGELOG.md at repo root
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
FILE="${1:-${ROOT}/CHANGELOG.md}"

if [[ ! -f "${FILE}" ]]; then
	echo "check-changelog: ${FILE} not found" >&2
	exit 2
fi

fail() {
	echo "✗ ${1}" >&2
	shift
	for line in "$@"; do echo "  ${line}" >&2; done
	exit 1
}

unreleased_count="$(grep -c '^## \[Unreleased\]' "${FILE}" || true)"
if [[ "${unreleased_count}" -gt 1 ]]; then
	mapfile -t where < <(grep -n '^## \[Unreleased\]' "${FILE}")
	fail "CHANGELOG has ${unreleased_count} [Unreleased] sections — there must be exactly one:" \
		"${where[@]}" \
		"" \
		"Merge the stray one into the release that shipped its content." >&2
fi

# The first version heading in the file is the only place Unreleased may sit.
first_heading="$(grep -m1 '^## \[' "${FILE}" || true)"
if [[ "${unreleased_count}" -eq 1 && "${first_heading}" != '## [Unreleased]' ]]; then
	fail "[Unreleased] is not the first section — found '${first_heading}' above it."
fi

# Dates are checked from the newest release down to the oldest git tag only.
# Sections below that predate the repository (1.0.0, 0.1.0 were written by hand
# before the first tag) carry no verifiable date and are left alone rather than
# given an invented one.
undated="$(grep -nE '^## \[[2-9]' "${FILE}" | grep -vE '^[0-9]+:## \[[0-9][^]]*\] - [0-9]{4}-[0-9]{2}-[0-9]{2}' || true)"
if [[ -n "${undated}" ]]; then
	fail "release sections without a 'YYYY-MM-DD' date:" "${undated}"
fi

released="$(grep -cE '^## \[[0-9]' "${FILE}" || true)"
echo "✓ CHANGELOG structure OK: ${unreleased_count} [Unreleased] + ${released} release sections"
