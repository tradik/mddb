#!/usr/bin/env bash
#
# Builds `deadcode` with the fix for golang/go#80973 applied (GO-021).
#
# No released version of x/tools can analyse this repository:
#
#   v0.49.0 (current)  parses Go 1.27 but panics inside rta.Analyze
#   v0.46.0 - v0.48.0  cannot type-check the Go 1.27 standard library at all
#
# The panic is upstream, not ours: Go 1.27 introduced generic methods —
# (*math/rand/v2.Rand).N is the first in the standard library — and
# rta.addRuntimeType passes Program.MethodValue's result to addReachable
# without checking it, while MethodValue documents that it returns nil for
# exactly that case. Anything that boxes a *rand.Rand triggers it.
#
# Fix proposed upstream as https://go.dev/cl/818580, unmerged as of 2026-08-22.
# This applies the same one-line guard to a local copy so GO-021 is not blocked
# on someone else's review queue.
#
# DELETE THIS SCRIPT once the CL lands and a release carries it. Check with:
#   go install golang.org/x/tools/cmd/deadcode@latest && deadcode ./...
#
# Usage: scripts/build-deadcode.sh [output-dir]   (default: ./bin)

set -euo pipefail

XTOOLS_VERSION="v0.49.0"
OUT_DIR="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/bin}"
WORK="$(mktemp -d)"
trap 'rm -rf "${WORK}"' EXIT

echo "Fetching golang.org/x/tools@${XTOOLS_VERSION}…"
GOFLAGS=-mod=mod GOWORK=off go mod download "golang.org/x/tools@${XTOOLS_VERSION}" 2>/dev/null || true
MODCACHE="$(go env GOMODCACHE)/golang.org/x/tools@${XTOOLS_VERSION}"
if [[ ! -d "$MODCACHE" ]]; then
  echo "::error::x/tools ${XTOOLS_VERSION} is not in the module cache" >&2
  exit 1
fi

cp -r "$MODCACHE" "${WORK}/xtools"
chmod -R u+w "${WORK}/xtools"

RTA="${WORK}/xtools/go/callgraph/rta/rta.go"
if ! grep -q 'r.addReachable(r.prog.MethodValue(sel), true)' "$RTA"; then
  echo "::error::rta.go does not contain the expected call — the patch is stale, recheck golang/go#80973" >&2
  exit 1
fi

python3 - "$RTA" <<'PATCH'
import sys
p = sys.argv[1]
s = open(p).read()
old = "\t\t\t\t\tr.addReachable(r.prog.MethodValue(sel), true)"
new = ("\t\t\t\t\t// MethodValue returns nil for interface and generic\n"
       "\t\t\t\t\t// methods (golang/go#80973, go.dev/cl/818580).\n"
       "\t\t\t\t\tif mv := r.prog.MethodValue(sel); mv != nil {\n"
       "\t\t\t\t\t\tr.addReachable(mv, true)\n"
       "\t\t\t\t\t}")
assert s.count(old) == 1, "expected exactly one call site, found %d" % s.count(old)
open(p, "w").write(s.replace(old, new, 1))
PATCH

mkdir -p "${WORK}/build" "${OUT_DIR}"
cd "${WORK}/build"
cat > go.mod <<EOF
module deadcodebuild

go 1.27.0
EOF
GOWORK=off go mod edit -require="golang.org/x/tools@${XTOOLS_VERSION}" -replace="golang.org/x/tools=${WORK}/xtools"
GOWORK=off GOFLAGS=-mod=mod go get golang.org/x/tools/cmd/deadcode >/dev/null 2>&1
GOBIN="${OUT_DIR}" GOWORK=off GOFLAGS=-mod=mod go install golang.org/x/tools/cmd/deadcode

echo "✓ ${OUT_DIR}/deadcode built with the golang/go#80973 fix applied"
