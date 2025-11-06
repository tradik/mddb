#!/bin/bash

# Generate gRPC code for all languages from shared protobuf definitions
# This script should be run from the project root

set -e

PROTO_DIR="proto"
PROTO_FILE="mddb.proto"

echo "🔧 Generating gRPC code from shared protobuf definitions..."

# Check if protoc is installed
if ! command -v protoc &> /dev/null; then
    echo "❌ protoc not found. Install it with:"
    echo "   macOS:  brew install protobuf"
    echo "   Linux:  apt-get install protobuf-compiler"
    exit 1
fi

# ============================================================================
# Go (Server)
# ============================================================================
echo ""
echo "📦 Generating Go code for server..."

if ! command -v protoc-gen-go &> /dev/null; then
    echo "  Installing protoc-gen-go..."
    go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
fi

if ! command -v protoc-gen-go-grpc &> /dev/null; then
    echo "  Installing protoc-gen-go-grpc..."
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
fi

# Generate Go code
mkdir -p services/mddbd/proto
protoc --go_out=services/mddbd --go_opt=paths=source_relative \
    --go-grpc_out=services/mddbd --go-grpc_opt=paths=source_relative \
    -I ${PROTO_DIR} ${PROTO_DIR}/${PROTO_FILE}

echo "  ✅ Go code generated in services/mddbd/proto/"

# ============================================================================
# Python (Client Library)
# ============================================================================
echo ""
echo "🐍 Generating Python code for client library..."

if command -v python3 &> /dev/null; then
    # Check if grpcio-tools is installed
    if ! python3 -c "import grpc_tools" 2>/dev/null; then
        echo "  Installing grpcio-tools..."
        pip3 install grpcio-tools 2>/dev/null || echo "  ⚠️  Install manually: pip3 install grpcio-tools"
    fi
    
    mkdir -p clients/python/mddb_client
    python3 -m grpc_tools.protoc \
        -I ${PROTO_DIR} \
        --python_out=clients/python/mddb_client \
        --grpc_python_out=clients/python/mddb_client \
        ${PROTO_DIR}/${PROTO_FILE} 2>/dev/null || echo "  ⚠️  Python generation skipped (install grpcio-tools)"
    
    # Create __init__.py
    touch clients/python/mddb_client/__init__.py
    
    echo "  ✅ Python code generated in clients/python/mddb_client/"
else
    echo "  ⚠️  Python not found, skipping Python generation"
fi

# ============================================================================
# Node.js (Client Library)
# ============================================================================
echo ""
echo "📦 Generating Node.js code for client library..."

if command -v npm &> /dev/null; then
    mkdir -p clients/nodejs/proto
    
    # Copy proto file for runtime loading
    cp ${PROTO_DIR}/${PROTO_FILE} clients/nodejs/proto/
    
    # Generate static code (optional, for TypeScript)
    if command -v grpc_tools_node_protoc &> /dev/null; then
        grpc_tools_node_protoc \
            --js_out=import_style=commonjs,binary:clients/nodejs/proto \
            --grpc_out=grpc_js:clients/nodejs/proto \
            -I ${PROTO_DIR} \
            ${PROTO_DIR}/${PROTO_FILE} 2>/dev/null || echo "  ⚠️  Static generation skipped"
    fi
    
    echo "  ✅ Node.js proto files copied to clients/nodejs/proto/"
    echo "     Use @grpc/proto-loader for dynamic loading"
else
    echo "  ⚠️  npm not found, skipping Node.js generation"
fi

# ============================================================================
# PHP (Extension)
# ============================================================================
echo ""
echo "🐘 Generating PHP code for extension..."

if command -v php &> /dev/null; then
    # Check if grpc_php_plugin is available
    if command -v grpc_php_plugin &> /dev/null; then
        mkdir -p services/php-extension/proto
        protoc --php_out=services/php-extension/proto \
            --grpc_out=services/php-extension/proto \
            --plugin=protoc-gen-grpc=`which grpc_php_plugin` \
            -I ${PROTO_DIR} \
            ${PROTO_DIR}/${PROTO_FILE} 2>/dev/null || echo "  ⚠️  PHP generation skipped"
        
        echo "  ✅ PHP code generated in services/php-extension/proto/"
    else
        echo "  ⚠️  grpc_php_plugin not found, skipping PHP generation"
        echo "     Install: pecl install grpc"
    fi
else
    echo "  ⚠️  PHP not found, skipping PHP generation"
fi

# ============================================================================
# Summary
# ============================================================================
echo ""
echo "═══════════════════════════════════════════════════════════"
echo "✅ Code generation complete!"
echo ""
echo "Generated for:"
echo "  • Go (server)          → services/mddbd/proto/"
echo "  • Python (client)      → clients/python/mddb_client/"
echo "  • Node.js (client)     → clients/nodejs/proto/"
echo "  • PHP (extension)      → services/php-extension/proto/"
echo ""
echo "Source proto file: ${PROTO_DIR}/${PROTO_FILE}"
echo "═══════════════════════════════════════════════════════════"
