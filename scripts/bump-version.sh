#!/usr/bin/env bash
#
# bump-version.sh — set the release version in every source check-version.sh
# guards.
#
# The guard knows where the version lives in thirteen files and says when they
# disagree. Until now nothing set them, so a release meant thirteen hand edits
# and a re-run of the guard to find the one that was missed. That is the same
# shape as the bug the Makefile had, where `make version` printed 2.11.4 through
# two releases because it held a fourteenth copy nothing checked.
#
# The anchors below are deliberately the same as check-version.sh's. If a source
# is added there and not here, the guard fails the next release, which is the
# right way round.
#
# Usage:
#   scripts/bump-version.sh 2.13.0          # rewrite
#   scripts/bump-version.sh --dry-run 2.13.0
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

DRY_RUN=false
if [[ "${1:-}" == "--dry-run" ]]; then
	DRY_RUN=true
	shift
fi

NEW="${1:-}"
if [[ ! "${NEW}" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
	echo "usage: $0 [--dry-run] <x.y.z>" >&2
	exit 2
fi

cd "${ROOT}"

# file : sed expression. Each anchors on the same line shape the guard reads, so
# a version number appearing elsewhere in the file — a changelog entry, a docs
# example, a dependency pin — is not touched.
bump() {
	local file="$1" expr="$2"
	if [[ ! -f "${file}" ]]; then
		echo "  skip ${file} (not present)"
		return 0
	fi
	local before after
	before="$(cat "${file}")"
	after="$(printf '%s\n' "${before}" | sed -E "${expr}")"
	if [[ "${before}" == "${after}" ]]; then
		echo "  ---- ${file} (no line matched — check the anchor)"
		return 1
	fi
	if [[ "${DRY_RUN}" == true ]]; then
		echo "  would ${file}"
	else
		printf '%s\n' "${after}" > "${file}"
		echo "  bump ${file}"
	fi
}

V='[0-9]+\.[0-9]+\.[0-9]+'
FAILED=0

bump "services/mddbd/main.go"                 "s|^const VERSION = \"${V}\"|const VERSION = \"${NEW}\"|" || FAILED=1
bump "docs/openapi.yaml"                      "s|^  version: \"${V}\"|  version: \"${NEW}\"|" || FAILED=1
bump "services/mddbd/snapcraft.yaml"          "s|^version: \"${V}\"|version: \"${NEW}\"|" || FAILED=1
bump "services/mddb-cli/snapcraft.yaml"       "s|^version: \"${V}\"|version: \"${NEW}\"|" || FAILED=1
bump "services/mddb-panel/snapcraft.yaml"     "s|^version: \"${V}\"|version: \"${NEW}\"|" || FAILED=1
bump ".env.example"                           "s|^MDDB_VERSION=${V}|MDDB_VERSION=${NEW}|" || FAILED=1
bump ".ssg.yaml"                              "s|^  mddbVersion: \"${V}\"|  mddbVersion: \"${NEW}\"|" || FAILED=1
bump "clients/nodejs/package.json"            "s|^  \"version\": \"${V}\"|  \"version\": \"${NEW}\"|" || FAILED=1
bump "services/mddb-panel/package.json"       "s|^  \"version\": \"${V}\"|  \"version\": \"${NEW}\"|" || FAILED=1
bump "services/php-extension/composer.json"   "s|^  \"version\": \"${V}\"|  \"version\": \"${NEW}\"|" || FAILED=1
bump "clients/python/pyproject.toml"          "s|^version = \"${V}\"|version = \"${NEW}\"|" || FAILED=1
bump "services/python-extension/pyproject.toml" "s|^version = \"${V}\"|version = \"${NEW}\"|" || FAILED=1
bump "docker-compose.yml"                     "s|(image: tradik/mddb:.*MDDB_VERSION:-)${V}|\\1${NEW}|" || FAILED=1

if [[ "${FAILED}" -eq 1 ]]; then
	echo
	echo "bump-version: at least one anchor matched nothing — the file moved or the format changed." >&2
	echo "Fix the anchor here and in scripts/check-version.sh, which reads the same lines." >&2
	exit 1
fi

echo
if [[ "${DRY_RUN}" == true ]]; then
	echo "dry run: nothing written. Re-run without --dry-run, then:"
else
	echo "Now verify, and promote the CHANGELOG:"
fi
echo "  bash scripts/check-version.sh --print"
