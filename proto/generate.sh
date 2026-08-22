#!/bin/bash

# Generate gRPC code for all languages from shared protobuf definitions.
#
# This is a thin wrapper around `buf generate`. It's kept for backwards
# compatibility — CI and local developers should prefer calling
# `buf generate` directly from the repository root.
#
# Run from the repository root.
#
# Requirements:
#   - buf CLI >= 1.72.0 (https://buf.build/docs/installation)
#     Install the exact version CI uses:
#       go install github.com/bufbuild/buf/cmd/buf@v1.72.0
#
# Fallback: if buf is not installed, falls through to the legacy
# protoc-based script (proto/generate-legacy.sh), which uses locally
# installed language plugins and is less reproducible.

set -e

echo "🔧 Generating gRPC code from shared protobuf definitions..."

if command -v buf &> /dev/null; then
    BUF_VERSION=$(buf --version 2>/dev/null | head -1)
    echo "  Using buf CLI (${BUF_VERSION}) with pinned plugin versions from buf.gen.yaml"
    echo ""

    # Lint proto files before generating — catch style issues early
    buf lint

    # Generate code for all languages from buf.gen.yaml
    buf generate

    # Sync the source .proto file to the Node.js client directory.
    # @grpc/proto-loader loads it at runtime from this path, so it MUST
    # be a real file (not a symlink — npm publish doesn't follow symlinks
    # reliably). CI catches drift via `git diff --exit-code`.
    echo ""
    echo "📎 Syncing proto/mddb.proto → clients/nodejs/proto/mddb.proto"
    cp proto/mddb.proto clients/nodejs/proto/mddb.proto

    echo ""
    echo "═══════════════════════════════════════════════════════════"
    echo "✅ Code generation complete (via buf)!"
    echo ""
    echo "Generated for:"
    echo "  • Go (server)          → services/mddbd/proto/"
    echo "  • Python (client)      → clients/python/mddb_client/"
    echo "  • Node.js (client)     → clients/nodejs/proto/"
    echo "  • PHP (extension)      → services/php-extension/proto/"
    echo ""
    echo "Source proto file:  proto/mddb.proto"
    echo "Buf config:         buf.yaml"
    echo "Plugin versions:    buf.gen.yaml (pinned)"
    echo "═══════════════════════════════════════════════════════════"
    exit 0
fi

echo "  ⚠️  buf CLI not found. Falling back to legacy protoc-based generation."
echo "     For reproducible builds, install buf: https://buf.build/docs/installation"
echo ""

# Fallback to legacy protoc-based script
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec "${SCRIPT_DIR}/generate-legacy.sh"
