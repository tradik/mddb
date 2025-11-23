#!/bin/sh
set -e

# Default mode is http
MODE=${MCP_MODE:-http}

case "$MODE" in
  stdio)
    echo "Starting MCP in stdio mode..." >&2
    exec ./mddb-mcp-stdio
    ;;
  http)
    echo "Starting MCP in HTTP mode on ${MCP_LISTEN_ADDRESS:-0.0.0.0:9000}..." >&2
    exec ./mddb-mcp
    ;;
  *)
    echo "Error: Invalid MCP_MODE='$MODE'. Use 'http' or 'stdio'" >&2
    exit 1
    ;;
esac
