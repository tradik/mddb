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
# bump <file> <anchor-regex> <replacement-template>
#
# The anchor and the replacement are separate arguments rather than one sed
# expression, so "the anchor matched nothing" and "the anchor matched a line
# already at the target version" can be told apart. They look identical to a
# before/after comparison, and only one of them is a problem: re-running a bump
# after fixing a file by hand is ordinary, and reporting it as a broken anchor
# sends the reader hunting for something that is not there.
#
# The anchor is written against the line shape check-version.sh reads, so a
# version number elsewhere in the file — a changelog entry, a docs example, a
# dependency pin — is left alone.
bump() {
	local file="$1" anchor="$2" repl="$3"
	if [[ ! -f "${file}" ]]; then
		echo "  skip ${file} (not present)"
		return 0
	fi

	if ! grep -qE "${anchor}" "${file}"; then
		echo "  ---- ${file} (no line matched — check the anchor)"
		return 1
	fi

	local before after
	before="$(cat "${file}")"
	after="$(printf '%s\n' "${before}" | sed -E "s|${anchor}|${repl}|")"

	if [[ "${before}" == "${after}" ]]; then
		echo "  same ${file} (already ${NEW})"
		return 0
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

bump "services/mddbd/main.go" \
	"^const VERSION = \"${V}\"" \
	"const VERSION = \"${NEW}\"" || FAILED=1
bump "docs/openapi.yaml" \
	"^  version: \"${V}\"" \
	"  version: \"${NEW}\"" || FAILED=1
bump "services/mddbd/snapcraft.yaml" \
	"^version: \"${V}\"" \
	"version: \"${NEW}\"" || FAILED=1
bump "services/mddb-cli/snapcraft.yaml" \
	"^version: \"${V}\"" \
	"version: \"${NEW}\"" || FAILED=1
bump "services/mddb-panel/snapcraft.yaml" \
	"^version: \"${V}\"" \
	"version: \"${NEW}\"" || FAILED=1
bump ".env.example" \
	"^MDDB_VERSION=${V}" \
	"MDDB_VERSION=${NEW}" || FAILED=1
bump ".ssg.yaml" \
	"^  mddbVersion: \"${V}\"" \
	"  mddbVersion: \"${NEW}\"" || FAILED=1
bump "clients/nodejs/package.json" \
	"^  \"version\": \"${V}\"" \
	"  \"version\": \"${NEW}\"" || FAILED=1
bump "services/mddb-panel/package.json" \
	"^  \"version\": \"${V}\"" \
	"  \"version\": \"${NEW}\"" || FAILED=1
bump "services/php-extension/composer.json" \
	"^  \"version\": \"${V}\"" \
	"  \"version\": \"${NEW}\"" || FAILED=1
bump "clients/python/pyproject.toml" \
	"^version = \"${V}\"" \
	"version = \"${NEW}\"" || FAILED=1
bump "services/python-extension/pyproject.toml" \
	"^version = \"${V}\"" \
	"version = \"${NEW}\"" || FAILED=1
bump "integrations/langchain-mddb/pyproject.toml" \
	"^version = \"${V}\"" \
	"version = \"${NEW}\"" || FAILED=1
bump "integrations/langchain-mddb/pyproject.toml" \
	"^client = \\[\"mddb-client>=${V}\"\\]" \
	"client = [\"mddb-client>=${NEW}\"]" || FAILED=1
bump "docker-compose.yml" \
	"(image: tradik/mddb:.*MDDB_VERSION:-)${V}" \
	"\\1${NEW}" || FAILED=1

if [[ "${FAILED}" -eq 1 ]]; then
	echo
	echo "bump-version: at least one anchor matched nothing — the file moved or the format changed." >&2
	echo "Fix the anchor here and in scripts/check-version.sh, which reads the same lines." >&2
	exit 1
fi

# The uv lockfiles record each project's own version, so a bump invalidates
# them and CI's `uv sync --locked` refuses to proceed. They are regenerated
# rather than edited: uv is the only thing that knows the format it will accept,
# and the version pinned here is the one CI uses.
if [[ "${DRY_RUN}" == false ]]; then
	if command -v uv >/dev/null 2>&1; then
		for dir in clients/python integrations/langchain-mddb; do
			(cd "${dir}" && uv lock >/dev/null 2>&1) && echo "  lock ${dir}/uv.lock" \
				|| echo "  ---- ${dir}/uv.lock (uv lock failed — run it by hand)"
		done
	else
		echo
		echo "uv is not installed, so the lockfiles were NOT regenerated." >&2
		echo "CI runs uv sync --locked and will refuse them. Install the version" >&2
		echo ".github/workflows/test.yml pins, then:" >&2
		echo "  (cd clients/python && uv lock)" >&2
		echo "  (cd integrations/langchain-mddb && uv lock)" >&2
	fi
fi

echo
if [[ "${DRY_RUN}" == true ]]; then
	echo "dry run: nothing written. Re-run without --dry-run, then:"
else
	echo "Now verify, and promote the CHANGELOG:"
fi
echo "  bash scripts/check-version.sh --print"
