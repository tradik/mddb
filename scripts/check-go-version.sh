#!/usr/bin/env bash
#
# check-go-version.sh — guard against Go toolchain version drift (DOC-003).
#
# A security bump of the Go toolchain must touch EVERY place that pins a
# patch version, otherwise local workspace builds, CI, and the shipped
# Docker images silently diverge (e.g. go.work lagging at go1.26.4 while
# every go.mod is on go1.26.4 — the exact drift that motivated this guard).
#
# This script collects every pinned `toolchain goX.Y.Z` directive, every
# `GO_VERSION:` workflow env, and every `golang:X.Y.Z` Docker base image,
# then fails if they are not all identical.
#
# Usage:
#   scripts/check-go-version.sh            # verify consistency (exit 1 on drift)
#   scripts/check-go-version.sh --print    # also print the collected table
#
# Exit codes: 0 = consistent, 1 = drift detected, 2 = nothing found.
#
set -euo pipefail

# Resolve repo root from this script's location so it works from any CWD.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${ROOT}"

PRINT=0
[[ "${1:-}" == "--print" ]] && PRINT=1

# version_re matches a semantic patch version like 1.26.4
version_re='[0-9]+\.[0-9]+\.[0-9]+'

declare -a sources=()  # "file:label=version" rows for reporting
declare -A versions=() # unique version -> first source seen
declare -a snap_pins=() # "file=major.minor" rows — snap channels pin no patch

record() {
	local file="$1" label="$2" version="$3"
	sources+=("${file} (${label}) = ${version}")
	versions["${version}"]="${file}"
}

# 1) toolchain directives in go.work + every go.mod (found recursively;
#    vendored/third-party trees are excluded).
mapfile -t gomod_files < <(find . -name go.mod -not -path '*/vendor/*' -not -path '*/node_modules/*' | sort)
while IFS= read -r match; do
	file="${match%%:*}"
	ver="$(printf '%s' "${match#*:}" | grep -oE "${version_re}")"
	[[ -n "${ver}" ]] && record "${file}" "toolchain" "${ver}"
done < <(grep -rHnE '^[[:space:]]*toolchain[[:space:]]+go'"${version_re}" go.work "${gomod_files[@]}" 2>/dev/null || true)

# 2) GO_VERSION env in GitHub workflows
while IFS= read -r match; do
	file="${match%%:*}"
	ver="$(printf '%s' "${match#*:}" | grep -oE "${version_re}")"
	[[ -n "${ver}" ]] && record "${file}" "GO_VERSION" "${ver}"
done < <(grep -rHnE 'GO_VERSION:[[:space:]]*["'\'']?'"${version_re}" .github/workflows 2>/dev/null || true)

# 3) golang:X.Y.Z Docker base images
while IFS= read -r match; do
	file="${match%%:*}"
	ver="$(printf '%s' "${match#*:}" | grep -oE "golang:${version_re}" | grep -oE "${version_re}")"
	[[ -n "${ver}" ]] && record "${file}" "FROM golang" "${ver}"
done < <(grep -rHnE 'FROM[[:space:]]+golang:'"${version_re}" services 2>/dev/null || true)

# 4) go/X.Y/stable snap channels in snapcraft.yaml. Snap channels track a
#    major.minor track and carry no patch component, so they are collected
#    separately and compared against the canonical version's track below —
#    a snap built on an older Go than the rest is the same class of drift.
while IFS= read -r match; do
	file="${match%%:*}"
	track="$(printf '%s' "${match#*:}" | grep -oE 'go/[0-9]+\.[0-9]+/' | grep -oE '[0-9]+\.[0-9]+')"
	[[ -n "${track}" ]] && snap_pins+=("${file}=${track}")
done < <(grep -rHnE 'go/[0-9]+\.[0-9]+/(stable|candidate|beta|edge)' --include='snapcraft.yaml' . 2>/dev/null || true)

if [[ ${#sources[@]} -eq 0 ]]; then
	echo "check-go-version: no Go version pins found — nothing to verify" >&2
	exit 2
fi

if [[ ${PRINT} -eq 1 ]]; then
	printf '%s\n' "${sources[@]}" | sort
	for pin in "${snap_pins[@]+"${snap_pins[@]}"}"; do
		echo "${pin%%=*} (snap go track) = ${pin##*=}"
	done
	echo "---"
fi

if [[ ${#versions[@]} -ne 1 ]]; then
	echo "✗ Go toolchain version DRIFT detected across the monorepo:" >&2
	printf '  %s\n' "${sources[@]}" | sort >&2
	echo "" >&2
	echo "  All toolchain / GO_VERSION / golang base-image pins must match." >&2
	echo "  Fix every diverging file, then re-run: scripts/check-go-version.sh" >&2
	exit 1
fi

only_version="${!versions[*]}"

# Snap channels must sit on the same major.minor track as the pinned toolchain.
expected_track="${only_version%.*}"
declare -a snap_drift=()
for pin in "${snap_pins[@]+"${snap_pins[@]}"}"; do
	[[ "${pin##*=}" != "${expected_track}" ]] && snap_drift+=("${pin%%=*} pins go/${pin##*=} — expected go/${expected_track}")
done

if [[ ${#snap_drift[@]} -gt 0 ]]; then
	echo "✗ Go snap channel DRIFT detected (toolchain is go${only_version}):" >&2
	printf '  %s\n' "${snap_drift[@]}" >&2
	echo "" >&2
	echo "  Update the 'go/X.Y/stable' build-snap in every snapcraft.yaml." >&2
	exit 1
fi

echo "✓ Go toolchain consistent across $(( ${#sources[@]} + ${#snap_pins[@]} )) pins: go${only_version}"
