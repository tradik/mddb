# MDDB MCP Setup for WSL (Windows Subsystem for Linux)

This guide explains how to run MDDB MCP server on Windows using WSL for integration with Windsurf IDE.

## Prerequisites

1. **WSL 2** installed on Windows
2. **Docker Desktop** for Windows with WSL 2 backend enabled
3. **Windsurf IDE** installed on Windows

## Architecture Overview

```
┌─────────────────────────────────────────┐
│         Windows (Host)                  │
│                                         │
│  ┌─────────────────────────────────┐   │
│  │      Windsurf IDE               │   │
│  │  (reads mcp_config.json)        │   │
│  └──────────────┬──────────────────┘   │
│                 │                       │
│                 │ stdio via Docker      │
│                 ▼                       │
│  ┌─────────────────────────────────┐   │
│  │   Docker Desktop (WSL2 backend) │   │
│  │                                 │   │
│  │  ┌──────────────────────────┐   │   │
│  │  │  mddb-mcp (stdio mode)   │   │   │
│  │  └──────────┬───────────────┘   │   │
│  │             │                   │   │
│  │             │ gRPC/REST         │   │
│  │             ▼                   │   │
│  │  ┌──────────────────────────┐   │   │
│  │  │  mddb server             │   │   │
│  │  │  (localhost:11023/11024) │   │   │
│  │  └──────────────────────────┘   │   │
│  └─────────────────────────────────┘   │
└─────────────────────────────────────────┘
```

## Quick Start (Automated)

Use the provided script for automatic setup:

```bash
# In WSL terminal
cd /path/to/mddb/services/mddb-mcp
./start-wsl.sh
```

This script will:
- ✅ Check Docker availability
- ✅ Start MDDB server
- ✅ Pull MCP image if needed
- ✅ Display Windsurf configuration
- ✅ Show next steps

## Option 1: Docker Compose (Manual Setup)

### Step 1: Start MDDB Server

```bash
# In WSL terminal
cd /path/to/mddb
docker-compose up -d
```

This starts:
- MDDB server on `localhost:11023` (HTTP) and `localhost:11024` (gRPC)
- MDDB Panel on `localhost:3000`

### Step 2: Configure Windsurf on Windows

Create or edit `%USERPROFILE%\.codeium\windsurf\mcp_config.json`:

```json
{
  "mcpServers": {
    "mddb": {
      "command": "docker",
      "args": [
        "run",
        "-i",
        "--rm",
        "--network",
        "host",
        "-e",
        "MCP_MODE=stdio",
        "-e",
        "MDDB_GRPC_ADDRESS=localhost:11024",
        "-e",
        "MDDB_REST_BASE_URL=http://localhost:11023",
        "-e",
        "MDDB_TRANSPORT_MODE=grpc_with_rest_fallback",
        "tradik/mddb:mcp"
      ],
      "env": {}
    }
  }
}
```

### Step 3: Restart Windsurf

Close and reopen Windsurf IDE. The MCP server should appear in the MCP marketplace.

## Option 2: WSL Native Binary

### Step 1: Build in WSL

```bash
# In WSL terminal
cd /path/to/mddb/services/mddb-mcp
go build -o mddb-mcp-stdio ./cmd/mddb-mcp-stdio
```

### Step 2: Start MDDB Server

```bash
# In WSL terminal
cd /path/to/mddb
docker-compose up -d mddb
```

### Step 3: Configure Windsurf

Create or edit `%USERPROFILE%\.codeium\windsurf\mcp_config.json`:

```json
{
  "mcpServers": {
    "mddb": {
      "command": "wsl",
      "args": [
        "-e",
        "/path/to/mddb/services/mddb-mcp/mddb-mcp-stdio"
      ],
      "env": {
        "MDDB_GRPC_ADDRESS": "localhost:11024",
        "MDDB_REST_BASE_URL": "http://localhost:11023",
        "MDDB_TRANSPORT_MODE": "grpc_with_rest_fallback"
      }
    }
  }
}
```

**Note:** Replace `/path/to/mddb` with actual WSL path (e.g., `/home/username/mddb`)

## Troubleshooting

### Issue: "Cannot connect to MDDB server"

**Solution 1: Check if MDDB is running**
```bash
# In WSL
curl http://localhost:11023/v1/stats
```

**Solution 2: Use host.docker.internal (if using Docker)**
```json
{
  "env": {
    "MDDB_GRPC_ADDRESS": "host.docker.internal:11024",
    "MDDB_REST_BASE_URL": "http://host.docker.internal:11023"
  }
}
```

### Issue: "MCP marketplace not loading"

**Check Windsurf logs:**
- Windows: `%APPDATA%\Windsurf\logs\`
- Look for MCP-related errors

**Verify Docker is accessible from Windows:**
```powershell
# In PowerShell
docker ps
```

### Issue: "WSL path not found"

**Convert Windows path to WSL path:**
```bash
# In WSL
wslpath "C:\Users\YourName\Projects\mddb"
# Output: /mnt/c/Users/YourName/Projects/mddb
```

### Issue: Network connectivity between WSL and Windows

**Enable WSL networking:**
```bash
# In WSL, check if you can reach Windows host
curl http://$(cat /etc/resolv.conf | grep nameserver | awk '{print $2}'):11023/v1/stats
```

## Testing the Setup

### 1. Test MDDB Server
```bash
# In WSL
curl http://localhost:11023/v1/stats
```

### 2. Test MCP stdio binary
```bash
# In WSL
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0.0"}}}' | ./mddb-mcp-stdio
```

Expected output: JSON response with server capabilities

### 3. Test in Windsurf
1. Open Windsurf
2. Open MCP marketplace (Ctrl+Shift+P → "MCP")
3. Look for "mddb" in the list
4. Should show green status

## Performance Tips

### Use gRPC for better performance
```json
{
  "env": {
    "MDDB_TRANSPORT_MODE": "grpc_only"
  }
}
```

### Enable MDDB extreme mode
```bash
# In docker-compose.yml
environment:
  - MDDB_EXTREME=true
```

## WSL-Specific Considerations

### File Permissions
If building in WSL, ensure binary is executable:
```bash
chmod +x mddb-mcp-stdio
```

### Docker Desktop Integration
- Ensure "Use the WSL 2 based engine" is enabled in Docker Desktop settings
- Enable WSL integration for your distro in Docker Desktop → Settings → Resources → WSL Integration

### Port Forwarding
WSL 2 automatically forwards `localhost` ports to Windows, so:
- `localhost:11023` in WSL = `localhost:11023` in Windows
- No additional port forwarding needed

### Memory Limits
WSL 2 has default memory limits. To increase:

Create `%USERPROFILE%\.wslconfig`:
```ini
[wsl2]
memory=4GB
processors=2
```

Restart WSL:
```powershell
# In PowerShell (as Administrator)
wsl --shutdown
```

## Alternative: Using Windows Docker Desktop Directly

If you prefer not to use WSL for running containers:

```json
{
  "mcpServers": {
    "mddb": {
      "command": "docker",
      "args": [
        "run",
        "-i",
        "--rm",
        "--network",
        "host",
        "-e",
        "MCP_MODE=stdio",
        "-e",
        "MDDB_GRPC_ADDRESS=host.docker.internal:11024",
        "-e",
        "MDDB_REST_BASE_URL=http://host.docker.internal:11023",
        "tradik/mddb:mcp"
      ]
    }
  }
}
```

**Note:** On Windows, use `host.docker.internal` instead of `localhost` to reach host services from containers.

## Quick Start Script

Save as `start-mddb-wsl.sh` in WSL:

```bash
#!/bin/bash
set -e

echo "🚀 Starting MDDB MCP on WSL..."

# Start MDDB server
cd ~/mddb
docker-compose up -d

# Wait for MDDB to be ready
echo "⏳ Waiting for MDDB server..."
until curl -s http://localhost:11023/v1/stats > /dev/null; do
    sleep 1
done

echo "✅ MDDB server is ready!"
echo "📊 Stats: http://localhost:11023/v1/stats"
echo "🎨 Panel: http://localhost:3000"
echo ""
echo "Now configure Windsurf with the MCP config and restart the IDE."
```

Make it executable:
```bash
chmod +x start-mddb-wsl.sh
./start-mddb-wsl.sh
```

## Support

For issues specific to WSL setup:
- Check WSL logs: `wsl --status`
- Check Docker Desktop logs: Docker Desktop → Troubleshoot → View logs
- Verify network connectivity: `ping host.docker.internal` (from WSL)

For MDDB MCP issues:
- See [WINDSURF_SETUP.md](WINDSURF_SETUP.md)
- Check [README.md](README.md)
