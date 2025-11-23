# MDDB MCP Server

Model Context Protocol (MCP) server for MDDB - provides LLM-friendly access to MDDB markdown database.

## Features

- **Dual Mode**: HTTP server + stdio mode for Windsurf/Claude Desktop
- **Dual Transport**: gRPC (default) with REST fallback
- **Full API Coverage**: All MDDB operations exposed as MCP resources and tools
- **Configurable**: YAML config with ENV override
- **Production Ready**: Health checks, graceful shutdown, error handling
- **Docker Ready**: Single image, mode selection via `MCP_MODE` env var

## Quick Start

### Option 1: Docker (Recommended)

**Pull from Docker Hub (recommended):**
```bash
# Latest version
docker pull tradik/mddb:mcp

# Specific version (same as main MDDB version)
docker pull tradik/mddb:mcp-2.0.4
```

**Or build locally:**
```bash
# Build single Docker image
docker build -t tradik/mddb:mcp -f services/mddb-mcp/Dockerfile .
```

**For Windsurf/Claude Desktop (stdio mode):**
```bash
# Run in stdio mode (set MCP_MODE=stdio)
docker run -i --rm \
  -e MCP_MODE=stdio \
  -e MDDB_GRPC_ADDRESS=localhost:11024 \
  -e MDDB_REST_BASE_URL=http://localhost:11023 \
  tradik/mddb:mcp

# Use in Windsurf - see mcp-docker-stdio.json.example
```

**For HTTP server mode:**
```bash
# Run in HTTP mode (default, or set MCP_MODE=http)
docker run -d -p 9000:9000 \
  -e MDDB_GRPC_ADDRESS=localhost:11024 \
  -e MDDB_REST_BASE_URL=http://localhost:11023 \
  tradik/mddb:mcp
```

### Option 2: Local Binary

```bash
cd services/mddb-mcp

# HTTP server mode
go build -o mddb-mcp ./cmd/mddb-mcp

# Stdio mode (for Windsurf/Claude Desktop)
go build -o mddb-mcp-stdio ./cmd/mddb-mcp-stdio

# Or use Makefile
make build-stdio  # for stdio only
make build-all    # for both
```

### Run

```bash
# With default config (config.yaml)
./mddb-mcp

# With custom config
MDDB_MCP_CONFIG=/path/to/config.yaml ./mddb-mcp

# With ENV overrides
MDDB_GRPC_ADDRESS=mddb:11024 \
MDDB_REST_BASE_URL=http://mddb:11023 \
MDDB_TRANSPORT_MODE=grpc_with_rest_fallback \
./mddb-mcp
```

## Configuration

See [docs/mddb-mcp-config.md](docs/mddb-mcp-config.md) for full configuration reference.

### Windsurf / Claude Desktop Setup

#### Option 1: Using Binary (Recommended for Development)

1. Build the stdio binary:
   ```bash
   cd services/mddb-mcp
   make build-stdio
   ```

2. Add to your MCP config file:

   **macOS/Linux:** `~/.windsurf/mcp.json` or `~/Library/Application Support/Claude/claude_desktop_config.json`

   **Windows:** `%APPDATA%\Windsurf\mcp.json` or `%APPDATA%\Claude\claude_desktop_config.json`

   ```json
   {
     "mcpServers": {
       "mddb": {
         "command": "/full/path/to/mddb-mcp-stdio",
         "env": {
           "MDDB_GRPC_ADDRESS": "localhost:11024",
           "MDDB_REST_BASE_URL": "http://localhost:11023",
           "MDDB_TRANSPORT_MODE": "grpc_with_rest_fallback"
         }
       }
     }
   }
   ```

3. Restart Windsurf/Claude Desktop

#### Option 2: Using Docker (No Build Required)

This option is easier - no need to build the binary locally!

1. Make sure MDDB server is running:
   ```bash
   docker run -d -p 11023:11023 -p 11024:11024 tradik/mddb:latest
   ```

2. Add to your MCP config file:

   **macOS/Linux:** `~/.windsurf/mcp.json`
   ```json
   {
     "mcpServers": {
       "mddb": {
         "command": "docker",
         "args": [
           "run", "-i", "--rm",
           "--network", "host",
           "-e", "MDDB_GRPC_ADDRESS=localhost:11024",
           "-e", "MDDB_REST_BASE_URL=http://localhost:11023",
           "-e", "MDDB_TRANSPORT_MODE=grpc_with_rest_fallback",
           "tradik/mddb:mcp"
         ]
       }
     }
   }
   ```

   **Windows:** `%APPDATA%\Windsurf\mcp.json`
   ```json
   {
     "mcpServers": {
       "mddb": {
         "command": "docker",
         "args": [
           "run", "-i", "--rm",
           "-e", "MDDB_GRPC_ADDRESS=host.docker.internal:11024",
           "-e", "MDDB_REST_BASE_URL=http://host.docker.internal:11023",
           "-e", "MDDB_TRANSPORT_MODE=grpc_with_rest_fallback",
           "tradik/mddb:mcp"
         ]
       }
     }
   }
   ```

3. Restart Windsurf/Claude Desktop

**Note:** Docker option requires Docker to be running. The image will be pulled automatically on first use.

See `mcp.json.example` and `mcp-docker.json.example` for more examples.

### Transport Modes

- `grpc_only` - Use only gRPC
- `rest_only` - Use only HTTP/REST
- `grpc_with_rest_fallback` - Try gRPC first, fallback to REST on error (default)
- `rest_with_grpc_fallback` - Try REST first, fallback to gRPC on error

## MCP Resources

Resources are read-only endpoints for retrieving data:

- `mddb://health` - MDDB server health status
- `mddb://stats` - Server and database statistics
- `mddb://{collection}/{key}?lang={lang}` - Get document content
- `mddb-search://{collection}?meta.{key}={value}&limit=10` - Search documents

## MCP Tools

Tools are operations that can modify state or perform tasks:

- `add_document` - Add or update a document
- `search_documents` - Search with filters and sorting
- `delete_document` - Delete a document
- `get_stats` - Get server statistics
- `add_documents_batch` - Batch add/update documents
- `delete_documents_batch` - Batch delete documents
- `export_documents` - Export documents (NDJSON/ZIP)
- `create_backup` - Create database backup
- `restore_backup` - Restore from backup

## API Endpoints

- `GET /health` - MCP server health
- `GET /mcp/resources` - List available resources
- `POST /mcp/resources/read` - Read a resource
- `GET /mcp/tools` - List available tools
- `POST /mcp/tools/call` - Call a tool

## Docker

The MCP server is available on both registries:
- **Docker Hub**: `tradik/mddb:mcp` (recommended - same repo as main server)
- **GitHub Container Registry**: `ghcr.io/tradik/mddb/mddb-mcp:latest`

```bash
# Using Docker Hub
docker run -d \
  -p 9000:9000 \
  -e MDDB_GRPC_ADDRESS=host.docker.internal:11024 \
  -e MDDB_REST_BASE_URL=http://host.docker.internal:11023 \
  tradik/mddb:mcp

# Or using GitHub Container Registry
docker run -d \
  -p 9000:9000 \
  -e MDDB_GRPC_ADDRESS=host.docker.internal:11024 \
  -e MDDB_REST_BASE_URL=http://host.docker.internal:11023 \
  ghcr.io/tradik/mddb/mddb-mcp:latest
```

## Development

```bash
# Run tests
go test ./...

# Format code
go fmt ./...

# Lint
golangci-lint run
```

## License

BSD-3-Clause (same as MDDB)
