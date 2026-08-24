#!/usr/bin/env bash
#
# check-source-encoding.sh — every tracked text file must be UTF-8 (WIN-004).
#
# The failure this guards against comes from Windows and is easy to hit by
# accident. PowerShell's `>` and `Out-File` default to UTF-16 LE with a byte
# order mark, so a file created that way — a generated fixture, a workflow
# step writing a config, someone redirecting output into a source file —
# arrives as UTF-16. Go then refuses to compile it:
#
#   illegal UTF-8 encoding
#
# .gitattributes normalises line endings (`* text=auto`, `*.go text eol=lf`)
# and says nothing about encoding, so nothing else in this repository would
# notice until a build broke somewhere confusing.
#
# What is checked: the first bytes of each tracked file against the UTF-16 and
# UTF-32 BOMs. A UTF-8 BOM (EF BB BF) is also rejected for Go sources — the
# compiler tolerates it, but it is invisible, it breaks `//go:build` on the
# first line, and no editor in this project produces one.
#
# Usage:
#   scripts/check-source-encoding.sh [path...]   # defaults to every tracked file
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# Binary formats legitimately start with bytes that look like a BOM, or simply
# are not text. Images, archives and fonts are excluded by extension.
is_binary_path() {
	case "${1,,}" in
	*.png | *.jpg | *.jpeg | *.gif | *.ico | *.webp | *.avif | *.pdf | \
		*.woff | *.woff2 | *.ttf | *.otf | *.eot | \
		*.zip | *.gz | *.tgz | *.xz | *.bz2 | *.7z | *.br | \
		*.wasm | *.so | *.dylib | *.dll | *.exe | *.bin | *.db | *.jar | \
		*.mp3 | *.mp4 | *.webm | *.ogg | *.wav) return 0 ;;
	esac
	return 1
}

# bom_name prints a human name for the BOM a file starts with, or nothing.
bom_name() {
	local head4
	head4="$(head -c 4 "$1" | od -An -tx1 | tr -d ' \n')"
	case "${head4}" in
	0000feff*) echo "UTF-32 BE" ;;
	fffe0000) echo "UTF-32 LE" ;;
	feff*) echo "UTF-16 BE" ;;
	fffe*) echo "UTF-16 LE" ;;
	efbbbf*) echo "UTF-8 with BOM" ;;
	esac
}

cd "${ROOT}"

if [[ $# -gt 0 ]]; then
	FILES=("$@")
else
	mapfile -t FILES < <(git ls-files)
fi

FAILED=0
CHECKED=0

for file in "${FILES[@]}"; do
	[[ -f "${file}" ]] || continue
	is_binary_path "${file}" && continue
	[[ -s "${file}" ]] || continue

	CHECKED=$((CHECKED + 1))
	bom="$(bom_name "${file}")"
	[[ -z "${bom}" ]] && continue

	# A UTF-8 BOM is only rejected in Go sources, where it silently breaks a
	# leading //go:build constraint.
	if [[ "${bom}" == "UTF-8 with BOM" && "${file}" != *.go ]]; then
		continue
	fi

	echo "::error file=${file}::${file} starts with a ${bom} byte order mark; write it as UTF-8 without one (PowerShell: Set-Content -Encoding utf8NoBOM)"
	FAILED=1
done

if [[ "${FAILED}" -eq 1 ]]; then
	echo "check-source-encoding: found files that are not plain UTF-8" >&2
	exit 1
fi

echo "✓ all ${CHECKED} tracked text files are plain UTF-8"
