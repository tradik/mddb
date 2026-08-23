#!/usr/bin/env bash
#
# check-repo-links.sh — relative Markdown links in the repository must point at
# files that exist.
#
# scripts/check-docs-links.py checks the *built site*, which is the right check
# for what visitors follow and covers nothing outside docs/. README.md,
# CONTRIBUTING.md, RELEASING.md and the audit notes are read on GitHub, where a
# dead link is a 404 with no build to fail.
#
# It found six on its first run — README.md linked docs/PERFORMANCE.md,
# docs/AUTH.md, docs/FTS.md, docs/CLIENTS.md, docs/WEBHOOKS.md and
# docs/API_QUICK_REFERENCE.md, none of which have ever existed.
#
# Deliberately not checked: absolute URLs (that is a network call and a
# different kind of flake), site-absolute paths like /docs/mcp/ (URLs on the
# published site — check-docs-links.py validates those against the build), and
# anchors within a file (heading slugs differ between renderers).
#
# Usage:
#   scripts/check-repo-links.sh [--print]
#
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${ROOT}"

PRINT=0
[[ "${1:-}" == "--print" ]] && PRINT=1

broken=()
checked=0

# Only files git tracks: node_modules, build output and vendored trees are not
# ours to police.
while IFS= read -r file; do
	dir="$(dirname "${file}")"

	# Markdown inline links: [text](target). Strip any anchor and any title.
	while IFS= read -r target; do
		[[ -z "${target}" ]] && continue
		# Absolute URLs, mail, anchors within the page, and template
		# placeholders are out of scope.
		# Any scheme, not just http: the CHANGELOG documents blocking
		# `javascript:` links in the widget, and that example is a scheme,
		# not a path into this repository.
		[[ "${target}" =~ ^[a-zA-Z][a-zA-Z0-9+.-]*: ]] && continue
		[[ "${target}" =~ ^(#|\{\{) ]] && continue
		# Site-absolute paths (/docs/mcp/) are URLs on the published site, not
		# files in this tree. scripts/check-docs-links.py validates those
		# against the build, which is where they either resolve or 404.
		[[ "${target}" == /* ]] && continue

		clean="${target%%#*}"
		[[ -z "${clean}" ]] && continue

		resolved="$(cd "${dir}" 2>/dev/null && printf '%s' "$(realpath -m --relative-to="${ROOT}" "${clean}" 2>/dev/null)")"
		[[ -z "${resolved}" ]] && resolved="${dir}/${clean}"

		checked=$((checked + 1))
		if [[ -e "${ROOT}/${resolved}" ]]; then
			[[ ${PRINT} -eq 1 ]] && echo "  ok    ${file} → ${target}"
		else
			broken+=("${file} → ${target}")
		fi
	done < <(grep -oE '\]\([^)"'"'"' ]+\)' "${file}" 2>/dev/null | sed -E 's/^\]\(//; s/\)$//')
done < <(git ls-files '*.md' | grep -vE '^(integrations/[^/]+/node_modules/|clients/nodejs/node_modules/)')

if [[ ${checked} -eq 0 ]]; then
	echo "check-repo-links: no relative links found — nothing to verify" >&2
	exit 2
fi

if [[ ${#broken[@]} -gt 0 ]]; then
	echo "✗ ${#broken[@]} relative link(s) point at nothing:" >&2
	printf '  %s\n' "${broken[@]}" >&2
	echo "" >&2
	echo "  Either the file was renamed, or the link names a page that was never written." >&2
	exit 1
fi

echo "✓ all ${checked} relative Markdown links resolve"
