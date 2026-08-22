#!/usr/bin/env bash
#
# Guards the protobuf plugin/runtime lockstep.
#
# buf.gen.yaml pins the `protocolbuffers/go` plugin that generates
# services/mddbd/proto/*.pb.go; services/mddbd/go.mod pins the
# google.golang.org/protobuf runtime that code links against. buf.gen.yaml says
# in a comment that the two match — and they had already drifted (plugin
# v1.36.11, runtime v1.36.12) because a dependency bump touched go.mod alone.
#
# Generated code and its runtime disagreeing is the kind of mismatch that
# surfaces as an unmarshalling bug in production rather than a build error, so
# the comment becomes a CI gate here.
#
# Usage: scripts/check-proto-plugins.sh [--print]

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GEN_FILE="${REPO_ROOT}/buf.gen.yaml"
GO_MOD="${REPO_ROOT}/services/mddbd/go.mod"

fail() { echo "::error::$*" >&2; exit 1; }

[[ -f "$GEN_FILE" ]] || fail "buf.gen.yaml not found at $GEN_FILE"
[[ -f "$GO_MOD"   ]] || fail "go.mod not found at $GO_MOD"

# buf.build/protocolbuffers/go:v1.36.12 -> 1.36.12
PLUGIN_VERSION="$(grep -oE 'buf\.build/protocolbuffers/go:v[0-9]+\.[0-9]+\.[0-9]+' "$GEN_FILE" \
  | head -1 | sed 's|.*:v||')"
[[ -n "$PLUGIN_VERSION" ]] || fail "no buf.build/protocolbuffers/go pin found in buf.gen.yaml"

# 	google.golang.org/protobuf v1.36.12 -> 1.36.12
RUNTIME_VERSION="$(grep -oE '^[[:space:]]*google\.golang\.org/protobuf v[0-9]+\.[0-9]+\.[0-9]+' "$GO_MOD" \
  | head -1 | sed 's|.* v||')"
[[ -n "$RUNTIME_VERSION" ]] || fail "no google.golang.org/protobuf requirement found in go.mod"

if [[ "${1:-}" == "--print" ]]; then
  echo "buf.gen.yaml (protocolbuffers/go plugin) = ${PLUGIN_VERSION}"
  echo "services/mddbd/go.mod (protobuf runtime) = ${RUNTIME_VERSION}"
  echo "---"
fi

if [[ "$PLUGIN_VERSION" != "$RUNTIME_VERSION" ]]; then
  fail "protobuf plugin/runtime drift: buf.gen.yaml pins the protocolbuffers/go plugin at v${PLUGIN_VERSION}, but services/mddbd/go.mod requires the runtime at v${RUNTIME_VERSION}. Set both to the same version, run 'buf generate', and commit the result."
fi

echo "✓ protobuf plugin and runtime agree: v${PLUGIN_VERSION}"
