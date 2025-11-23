# Windsurf Setup Guide for MDDB MCP

This guide shows how to connect MDDB MCP to Windsurf IDE.

## Prerequisites

1. MDDB server running (either locally or via Docker)
2. Built `mddb-mcp-stdio` binary

## Setup Options

You have two options to run MCP for Windsurf:

### Option A: Docker (Easiest - No Build Required) ⭐

**Pros:**
- No need to build anything
- Always up-to-date with published image
- Works on all platforms
- Easy to update

**Cons:**
- Requires Docker to be running
- Slightly slower startup

### Option B: Local Binary

**Pros:**
- Faster startup
- No Docker dependency
- Good for development

**Cons:**
- Need to rebuild after updates
- Platform-specific binary

---

## Option A: Docker Setup (Recommended)

### Step 1: Ensure Docker is running

```bash
docker --version
```

### Step 2: Start MDDB server

```bash
docker run -d -p 11023:11023 -p 11024:11024 tradik/mddb:latest
```

### Step 3: Configure Windsurf

**macOS/Linux:**
```bash
mkdir -p ~/.windsurf
nano ~/.windsurf/mcp.json
```

Add this configuration:
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
        "ghcr.io/tradik/mddb/mddb-mcp:latest"
      ]
    }
  }
}
```

**Windows:**
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
        "ghcr.io/tradik/mddb/mddb-mcp:latest"
      ]
    }
  }
}
```

### Step 4: Restart Windsurf

Done! The Docker image will be pulled automatically on first use.

---

## Option B: Local Binary Setup

### Step 1: Build the stdio binary

```bash
cd /Users/user/github.com/tradik/mddb/services/mddb-mcp
make build-stdio
```

This creates `mddb-mcp-stdio` binary in the current directory.

## Step 2: Start MDDB server (for Option B)

### Option A: Docker Compose (recommended)

```bash
cd /Users/user/github.com/tradik/mddb
make docker-up
```

This starts:
- MDDB server on ports 11023 (HTTP) and 11024 (gRPC)
- MDDB Panel on port 3000
- MDDB MCP on port 9000

### Option B: Local binary

```bash
cd /Users/user/github.com/tradik/mddb/services/mddbd
go run . -path /tmp/mddb.db -mode wr
```

## Step 3: Configure Windsurf

1. Open or create the MCP configuration file:

   **macOS:**
   ```bash
   mkdir -p ~/.windsurf
   nano ~/.windsurf/mcp.json
   ```

2. Add this configuration:

   ```json
   {
     "mcpServers": {
       "mddb": {
         "command": "/Users/user/github.com/tradik/mddb/services/mddb-mcp/mddb-mcp-stdio",
         "env": {
           "MDDB_GRPC_ADDRESS": "localhost:11024",
           "MDDB_REST_BASE_URL": "http://localhost:11023",
           "MDDB_TRANSPORT_MODE": "grpc_with_rest_fallback"
         }
       }
     }
   }
   ```

   **Important:** Replace the path with the actual full path to your `mddb-mcp-stdio` binary.

3. Save and close the file.

## Step 4: Restart Windsurf

Close and reopen Windsurf IDE completely.

## Step 5: Verify connection

In Windsurf, you should now see MDDB MCP available. You can test it by:

1. Opening the MCP panel (if available in UI)
2. Or asking Windsurf to use MDDB:
   - "List all documents in the 'blog' collection"
   - "Add a new document to MDDB"
   - "Search for documents with tag 'tutorial'"

## Troubleshooting

### MCP not showing up

1. Check if the binary path is correct:
   ```bash
   ls -la /Users/user/github.com/tradik/mddb/services/mddb-mcp/mddb-mcp-stdio
   ```

2. Check if MDDB server is running:
   ```bash
   curl http://localhost:11023/health
   ```

3. Check Windsurf logs (usually in `~/.windsurf/logs/`)

### Connection errors

1. Verify MDDB is accessible:
   ```bash
   # Test HTTP
   curl http://localhost:11023/v1/stats
   
   # Test gRPC (requires grpcurl)
   grpcurl -plaintext localhost:11024 mddb.MDDB/Stats
   ```

2. Try REST-only mode in `mcp.json`:
   ```json
   "env": {
     "MDDB_TRANSPORT_MODE": "rest_only",
     "MDDB_REST_BASE_URL": "http://localhost:11023"
   }
   ```

### Test stdio binary manually

```bash
cd /Users/user/github.com/tradik/mddb/services/mddb-mcp

# Start the binary
./mddb-mcp-stdio

# In another terminal, send a test request
echo '{"method":"ping"}' | ./mddb-mcp-stdio
# Should respond: {"result":"pong"}
```

## Using Docker image (alternative)

If you prefer using Docker instead of local binary:

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
        "ghcr.io/tradik/mddb/mddb-mcp:latest"
      ]
    }
  }
}
```

Note: Docker image will be available after you push to GitHub (see Publishing section in README).

## Available MCP Operations

### Resources (read-only)
- `mddb://health` - Server health status
- `mddb://stats` - Database statistics
- `mddb://{collection}/{key}?lang={lang}` - Get document
- `mddb-search://{collection}` - Search documents

### Tools (operations)
- `add_document` - Add/update document
- `search_documents` - Search with filters
- `delete_document` - Delete document
- `get_stats` - Get statistics

## Next Steps

- See `README.md` for full API documentation
- Check `mcp.json.example` for more configuration examples
- Read `docs/mddb-mcp-config.md` for advanced configuration
