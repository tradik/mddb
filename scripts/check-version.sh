#!/usr/bin/env bash
#
# check-version.sh — guard against release-version drift (DOC-011).
#
# The release version is written in a dozen places, by hand, and they have
# drifted before: the CHANGELOG once described 2.10.1 while the binary still
# reported 2.10.0 and no such tag existed. This collects every one of them and
# fails if they disagree, so a release cannot ship claiming two versions.
#
# The package manifests were added after 2.12.0 found five of them — the Node
# and Python clients, the panel, and both language extensions — still declaring
# 2.11.4. They were bumped by hand at 2.11.4 and nothing bumped them since,
# because nothing was watching them. Publishing 2.11.4 from a 2.12.0 tree is
# worse than a stale number: whoever installs the client gets a package whose
# version does not name the server it was built against.
#
# Deliberately NOT checked: the git tag. A tag is created at release time,
# after this passes — checking it here would make the guard fail for the whole
# window between bumping the version and tagging it. RELEASING.md covers that
# step.
#
# Usage:
#   scripts/check-version.sh [--print]
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${ROOT}"

PRINT=0
[[ "${1:-}" == "--print" ]] && PRINT=1

version_re='[0-9]+\.[0-9]+\.[0-9]+'

declare -a sources=()
declare -A versions=()

record() {
	sources+=("$1 ($2) = $3")
	versions["$3"]="$1"
}

# extract <file> <label> <grep-pattern>
extract() {
	local file="$1" label="$2" pattern="$3"
	[[ -f "${file}" ]] || return 0
	local line ver
	line="$(grep -m1 -E "${pattern}" "${file}" 2>/dev/null || true)"
	[[ -n "${line}" ]] || return 0
	ver="$(printf '%s' "${line}" | grep -oE "${version_re}" | head -1)"
	[[ -n "${ver}" ]] && record "${file}" "${label}" "${ver}"
}

extract "services/mddbd/main.go"           "const VERSION"  "^const VERSION = \"${version_re}\""
extract "docs/openapi.yaml"                "info.version"   "^  version: \"${version_re}\""
extract "services/mddbd/snapcraft.yaml"    "snap version"   "^version: \"${version_re}\""
extract "services/mddb-cli/snapcraft.yaml" "snap version"   "^version: \"${version_re}\""
extract "services/mddb-panel/snapcraft.yaml" "snap version" "^version: \"${version_re}\""
extract ".env.example"                     "MDDB_VERSION"   "^MDDB_VERSION=${version_re}"
extract ".ssg.yaml"                        "mddbVersion"    "^  mddbVersion: \"${version_re}\""

# Package manifests that track the server's version. Deliberately NOT here:
# the integrations, the chat widget and mddb-chat, which sit at 0.1.0 and have
# never been bumped — see VERSIONING.md for which components move together.
extract "clients/nodejs/package.json"        "npm version"      "^  \"version\": \"${version_re}\""
extract "services/mddb-panel/package.json"   "npm version"      "^  \"version\": \"${version_re}\""
extract "services/php-extension/composer.json" "composer version" "^  \"version\": \"${version_re}\""
extract "clients/python/pyproject.toml"      "pypi version"     "^version = \"${version_re}\""
extract "services/python-extension/pyproject.toml" "pypi version" "^version = \"${version_re}\""

# The compose default must match too: it is what `docker compose up` pulls.
while IFS= read -r match; do
	file="${match%%:*}"
	ver="$(printf '%s' "${match#*:}" | grep -oE "${version_re}" | head -1)"
	[[ -n "${ver}" ]] && record "${file}" "compose image default" "${ver}"
done < <(grep -rHnE "image: tradik/mddb:.*MDDB_VERSION:-${version_re}" docker-compose.yml 2>/dev/null || true)

if [[ ${#sources[@]} -eq 0 ]]; then
	echo "check-version: no version sources found — nothing to verify" >&2
	exit 2
fi

if [[ ${PRINT} -eq 1 ]]; then
	printf '%s\n' "${sources[@]}" | sort
	echo "---"
fi

if [[ ${#versions[@]} -ne 1 ]]; then
	echo "✗ release version DRIFT across the repository:" >&2
	printf '  %s\n' "${sources[@]}" | sort >&2
	echo "" >&2
	echo "  Every source must state the same version before a release is tagged." >&2
	exit 1
fi

only="${!versions[*]}"
echo "✓ release version consistent across ${#sources[@]} sources: ${only}"

# The CHANGELOG is checked separately: an unreleased version legitimately sits
# under [Unreleased] until the release is cut, so a mismatch there is only an
# error once a dated section for this version exists.
if grep -qE "^## \[${only}\] - [0-9]{4}-[0-9]{2}-[0-9]{2}" CHANGELOG.md 2>/dev/null; then
	echo "  CHANGELOG has a dated [${only}] section — ready to tag"
else
	echo "  CHANGELOG still has this version under [Unreleased] — move it before tagging"
fi
