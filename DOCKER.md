# MDDB Docker Development Guide

Quick guide to running MDDB with Docker for development.

## 🚀 Quick Start

### Option 1: Using Make (Recommended)

```bash
# Start all services
make dev-start

# View logs
make dev-logs

# Stop services
make dev-stop
```

### Option 2: Using Docker Compose Directly

```bash
# Start services
docker-compose -f docker-compose.dev.yml up -d

# View logs
docker-compose -f docker-compose.dev.yml logs -f

# Stop services
docker-compose -f docker-compose.dev.yml down
```

## 📦 Available Services

After starting with `make dev-start`, you'll have:

| Service | URL | Description |
|---------|-----|-------------|
| MDDB Server | http://localhost:11023 | HTTP API + MCP (via `/mcp/*` endpoints) |
| MCP Server | http://localhost:9000 | Model Context Protocol (MCP) |
| MDDB Panel | http://localhost:3000 | Admin web interface |
| gRPC | localhost:11024 | gRPC endpoint |
| Ollama* | http://localhost:11434 | Vector embeddings (optional) |

> **Note:** MCP is built into mddbd but runs on its own port (`:9000` by default). Each protocol (HTTP, gRPC, MCP) can be toggled on/off and assigned a custom port via env vars, CLI flags, or config file.

*Start with `make dev-start-with-ollama` to include Ollama

## 🔑 Default Credentials

- **Username:** `admin`
- **Password:** `admin123`

⚠️ **Change these in production!**

## 📊 Viewing the Admin Panel

1. Start services: `make dev-start`
2. Open browser: http://localhost:3000
3. Login with default credentials
4. Explore the new admin sections:
   - **System Info** - Server metrics, CPU, memory
   - **Configuration** - View server settings
   - **MCP Config** - Export MCP YAML
   - **API Endpoints** - Browse HTTP/gRPC/MCP APIs
   - **Users** - User management
   - **Groups** - Group permissions (NEW!)

## 🛠️ Make Commands

```bash
make help                  # Show all available commands
make dev-start            # Start all services
make dev-start-with-ollama # Start with Ollama for embeddings
make dev-stop             # Stop all services
make dev-restart          # Restart services
make dev-logs             # View all logs
make dev-logs-server      # View server logs only
make dev-logs-panel       # View panel logs only
make dev-logs-mcp         # View MCP logs only
make dev-build            # Rebuild Docker images
make dev-clean            # Stop and remove volumes
make dev-shell-server     # Open shell in server container
make dev-shell-panel      # Open shell in panel container
make test                 # Run all tests
make test-coverage        # Run tests with coverage
make lint                 # Run linter
```

## 🔧 Configuration

### Environment Variables

Copy `.env.example` to `.env` and customize:

```bash
cp .env.example .env
```

Key settings:

```bash
# Enable/disable authentication
MDDB_AUTH_ENABLED=true

# Set admin credentials
MDDB_AUTH_ADMIN_USERNAME=admin
MDDB_AUTH_ADMIN_PASSWORD=changeme

# Panel mode: internal (default, CORS enabled) or external (panel proxies, no CORS)
# MDDB_PANEL_MODE=external

# Configure vector embeddings
MDDB_EMBEDDING_PROVIDER=ollama  # or openai, voyage
MDDB_EMBEDDING_MODEL=nomic-embed-text

# Embedding chunking (auto-splits long documents for better embedding coverage)
MDDB_EMBEDDING_CHUNK_ENABLED=true   # default: true
MDDB_EMBEDDING_CHUNK_SIZE=1500      # default: 1500 characters
```

### Using Vector Embeddings

To use vector search with Ollama:

```bash
# Start with Ollama
make dev-start-with-ollama

# Pull the embedding model
docker exec mddb-ollama ollama pull nomic-embed-text

# Now vector search is available!
```

For OpenAI embeddings:

```bash
# In .env or docker-compose.dev.yml
MDDB_EMBEDDING_PROVIDER=openai
MDDB_EMBEDDING_API_KEY=your-api-key
MDDB_EMBEDDING_MODEL=text-embedding-3-small
```

## 🗄️ Data Persistence

Data is stored in Docker volumes:

- `mddb-data` - Database and application data
- `ollama-data` - Ollama models and cache

To completely reset:

```bash
make dev-clean  # Removes volumes and all data
```

### The volume is the guarantee

MDDB fsyncs every commit, so an acknowledged write survives a crash — but only
to the storage it was given. A container started **without** a volume at the
data directory writes to its own overlay layer, which is deleted with the
container: writes are accepted, fsynced, and gone.

The server checks this at startup and refuses to be quiet about it. Watch for:

```
{"level":"WARN","msg":"persistence warning","code":"ephemeral_storage", ...}
```

and check `GET /health`, which reports `"durable": false` in that state. See
[Persistence Guarantees](docs/DEPLOYMENT.md#persistence-guarantees) for what is
and is not covered.

## 🐛 Troubleshooting

### Services won't start

```bash
# Check logs
make dev-logs

# Rebuild images
make dev-build
make dev-start
```

### Port conflicts

Edit `docker-compose.dev.yml` to change ports:

```yaml
ports:
  - "11023:11023"  # Change first number (host port)
```

### Database locked

```bash
# Stop all services and clean
make dev-clean

# Restart
make dev-start
```

### Panel can't connect to server

Check that `MDDB_SERVER` is set on the panel container:

```yaml
# In docker-compose.dev.yml
environment:
  - MDDB_SERVER=http://mddbd:11023
```

## 📝 Development Workflow

### Making Code Changes

The development setup includes hot reload:

**Backend (Go):**
- Changes auto-rebuild with Air
- No restart needed

**Frontend (React):**
- Changes auto-reload with Vite HMR
- Instant updates

**MCP (built into mddbd):**
- MCP runs on port 9000 by default (separate from HTTP API)
- For stdio mode: `docker run -i --rm -e MDDB_MCP_STDIO=true tradik/mddb:latest`

### Running Tests

```bash
# Inside container
make dev-shell-server
go test -v ./...

# Or from host
make test
```

### Viewing Metrics

Prometheus metrics available at:
- http://localhost:11023/metrics

## 🔐 Security Notes

For development:
- ✅ Simple passwords are OK
- ✅ HTTP is fine
- ✅ Default ports are convenient

For production:
- ❌ Change all default passwords
- ❌ Use HTTPS/TLS
- ❌ Change default ports
- ❌ Enable firewall rules
- ❌ Use strong JWT secrets (32+ chars)

## 📚 Next Steps

1. **Explore the API:** http://localhost:11023/v1/endpoints
2. **Try MCP Tools:** Connect to http://localhost:9000/tools
3. **Read the docs:** See main README.md
4. **Create groups:** Use the new Groups management UI
5. **Set permissions:** Assign collection access to groups

## 🆘 Getting Help

- Check logs: `make dev-logs`
- View API docs: http://localhost:3000 → API Endpoints
- Report issues: https://github.com/tradik/mddb/issues
