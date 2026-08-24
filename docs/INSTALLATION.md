---
title: "Installation Guide"
slug: "docs/installation"
description: "Install MDDB on Linux, macOS and Windows: binary releases, Homebrew, Docker and building from source, plus first-run configuration."
status: publish
---

# Installation Guide

Complete installation instructions for MDDB on all supported platforms.

## Docker (Recommended)

The easiest way to run MDDB is with Docker.

### Pull and Run

```bash
# Pull latest version
docker pull tradik/mddb:latest

# Run server
docker run -d \
  --name mddb \
  -p 11023:11023 \
  -p 11024:11024 \
  -v mddb-data:/data \
  tradik/mddb:latest

# Test it
curl http://localhost:11023/health
```

### Docker Compose

```bash
# Download docker-compose.yml
curl -O https://raw.githubusercontent.com/tradik/mddb/main/docker-compose.yml

# Start services
docker-compose up -d

# View logs
docker-compose logs -f

# Stop services
docker-compose down
```

**Docker Hub:** https://hub.docker.com/r/tradik/mddb

---

## Linux

### Ubuntu / Debian

**Server:**
```bash
# Download .deb package
wget https://github.com/tradik/mddb/releases/latest/download/mddbd-latest-linux-amd64.deb

# Install
sudo dpkg -i mddbd-latest-linux-amd64.deb

# Start service
sudo systemctl start mddbd
sudo systemctl enable mddbd

# Check status
sudo systemctl status mddbd
```

**Client:**
```bash
# Download CLI
wget https://github.com/tradik/mddb/releases/latest/download/mddb-cli-latest-linux-amd64.deb

# Install
sudo dpkg -i mddb-cli-latest-linux-amd64.deb

# Test
mddb-cli --help
man mddb-cli
```

### RHEL / CentOS / Fedora

**Server:**
```bash
# Download .rpm package
wget https://github.com/tradik/mddb/releases/latest/download/mddbd-latest-linux-amd64.rpm

# Install
sudo rpm -i mddbd-latest-linux-amd64.rpm

# Start service
sudo systemctl start mddbd
sudo systemctl enable mddbd

# Check status
sudo systemctl status mddbd
```

**Client:**
```bash
# Download CLI
wget https://github.com/tradik/mddb/releases/latest/download/mddb-cli-latest-linux-amd64.rpm

# Install
sudo rpm -i mddb-cli-latest-linux-amd64.rpm

# Test
mddb-cli --help
```

### Arch Linux (Manual)

```bash
# Download tarball
wget https://github.com/tradik/mddb/releases/latest/download/mddbd-latest-linux-amd64.tar.gz

# Extract
tar xzf mddbd-latest-linux-amd64.tar.gz

# Install binary
sudo mv mddbd-latest-linux-amd64/mddbd /usr/local/bin/
sudo chmod +x /usr/local/bin/mddbd

# Run manually or create systemd service (see below)
mddbd
```

### systemd Service (Manual Setup)

Create `/etc/systemd/system/mddbd.service`:

```ini
[Unit]
Description=MDDB - Markdown Database Server
After=network.target

[Service]
Type=simple
User=mddb
Group=mddb
WorkingDirectory=/var/lib/mddb
ExecStart=/usr/local/bin/mddbd
Restart=on-failure
RestartSec=5s

Environment=MDDB_DB_PATH=/var/lib/mddb/mddb.db
Environment=MDDB_HTTP_PORT=11023
Environment=MDDB_GRPC_PORT=11024

[Install]
WantedBy=multi-user.target
```

Enable and start:

```bash
# Create user and directory
sudo useradd -r -s /bin/false mddb
sudo mkdir -p /var/lib/mddb
sudo chown mddb:mddb /var/lib/mddb

# Enable and start service
sudo systemctl daemon-reload
sudo systemctl enable mddbd
sudo systemctl start mddbd

# Check logs
sudo journalctl -u mddbd -f
```

---

## macOS

### Homebrew (Coming Soon)

```bash
brew tap tradik/mddb
brew install mddbd mddb-cli

# Run server
mddbd

# Or as service
brew services start mddbd
```

### Manual Installation

**Intel Mac (amd64):**
```bash
# Download
wget https://github.com/tradik/mddb/releases/latest/download/mddbd-latest-darwin-amd64.tar.gz

# Extract
tar xzf mddbd-latest-darwin-amd64.tar.gz

# Install
sudo mv mddbd-latest-darwin-amd64/mddbd /usr/local/bin/
sudo chmod +x /usr/local/bin/mddbd

# Run
mddbd
```

**Apple Silicon (arm64):**
```bash
# Download
wget https://github.com/tradik/mddb/releases/latest/download/mddbd-latest-darwin-arm64.tar.gz

# Extract
tar xzf mddbd-latest-darwin-arm64.tar.gz

# Install
sudo mv mddbd-latest-darwin-arm64/mddbd /usr/local/bin/
sudo chmod +x /usr/local/bin/mddbd

# Run
mddbd
```

**Client (CLI):**
```bash
# Intel
wget https://github.com/tradik/mddb/releases/latest/download/mddb-cli-latest-darwin-amd64.tar.gz
tar xzf mddb-cli-latest-darwin-amd64.tar.gz
sudo mv mddb-cli-latest-darwin-amd64/mddb-cli /usr/local/bin/

# Apple Silicon
wget https://github.com/tradik/mddb/releases/latest/download/mddb-cli-latest-darwin-arm64.tar.gz
tar xzf mddb-cli-latest-darwin-arm64.tar.gz
sudo mv mddb-cli-latest-darwin-arm64/mddb-cli /usr/local/bin/

# Test
mddb-cli --help
```

---

## FreeBSD

```bash
# Download
wget https://github.com/tradik/mddb/releases/latest/download/mddbd-latest-freebsd-amd64.tar.gz

# Extract
tar xzf mddbd-latest-freebsd-amd64.tar.gz

# Install
sudo mv mddbd-latest-freebsd-amd64/mddbd /usr/local/bin/
sudo chmod +x /usr/local/bin/mddbd

# Run
mddbd
```

---

## Windows

Two options, and they are not equivalent yet.

**WSL2 is the supported route.** It runs the same Linux binary that every
release is built and tested against, so it inherits the full test suite.

**A native Windows build exists but is experimental.** As of 2.13 the server
and CLI compile and link for `windows/amd64` from source. There are no Windows
release artifacts, and no CI job runs the test suite on Windows yet, so nothing
has proven the binaries behave correctly once they start — only that they
build. Treat it as something to try, not something to run a database on.

Two areas are known to differ and are not yet addressed: replacing a file that
another process holds open behaves differently on Windows than on Unix, and
Unix domain sockets compile but are not exercised. Configure a TCP listener.

```powershell
git clone https://github.com/tradik/mddb.git
cd mddb
go build -o mddbd.exe ./services/mddbd
go build -o mddb-cli.exe ./services/mddb-cli
```

Cross-compiling from Linux or macOS produces the same pair:

```bash
make build-windows      # → dist/windows-amd64/{mddbd.exe,mddb-cli.exe}
```

`windows/arm64` is not built.

### Install WSL2

```powershell
# In PowerShell (admin)
wsl --install
```

### Install MDDB in WSL2

```bash
# Inside WSL2 Ubuntu
wget https://github.com/tradik/mddb/releases/latest/download/mddbd-latest-linux-amd64.deb
sudo dpkg -i mddbd-latest-linux-amd64.deb
sudo systemctl start mddbd
```

Access from Windows: `http://localhost:11023`

**See also:** [MCP Configuration](/docs/mcp/) for setting up MDDB MCP on WSL

---

## Build from Source

### Prerequisites

- **Go 1.27+** - [Download](https://golang.org/dl/)
- **Make** - Optional, for Makefile commands
- **Git** - For cloning repository

### Clone and Build

```bash
# Clone repository
git clone https://github.com/tradik/mddb.git
cd mddb

# Build server
cd services/mddbd
go build -o mddbd .

# Build CLI
cd ../mddb-cli
go build -o mddb-cli .

# Run server
cd ../mddbd
./mddbd
```

### Using Makefile

```bash
# Clone repository
git clone https://github.com/tradik/mddb.git
cd mddb

# Build all binaries
make build

# Build and install CLI + man page
make build-cli
make install-all

# Run server
make run

# Run in development mode with hot reload
make install-dev-tools
make dev

# Run tests
make test

# Format code
make fmt

# Run linter
make lint
```

### Generate gRPC Code (if modifying proto files)

```bash
# Install protoc and Go plugins
make install-grpc-tools

# Generate code
make generate-proto
```

---

## Post-Installation

### Verify Installation

```bash
# Check server is running
curl http://localhost:11023/health

# Expected output:
# {"status":"ok","timestamp":1709395200}

# Get statistics
curl http://localhost:11023/v1/stats

# Test CLI
mddb-cli stats
```

### Configure

**Environment Variables:**

Create `/etc/mddb/mddb.env`:

```bash
MDDB_DB_PATH=/var/lib/mddb/mddb.db
MDDB_HTTP_PORT=11023
MDDB_GRPC_PORT=11024
MDDB_GRAPHQL_ENABLED=true
MDDB_EXTREME=true

# Vector search (optional)
MDDB_EMBEDDING_PROVIDER=openai
MDDB_EMBEDDING_API_KEY=sk-...
MDDB_EMBEDDING_MODEL=text-embedding-3-small
MDDB_EMBEDDING_DIMENSIONS=1536

# Authentication (optional)
MDDB_AUTH_ENABLED=true
MDDB_JWT_SECRET=your-secret-key-here
```

**CLI Configuration:**

Create `~/.mddb-cli.yaml`:

```yaml
server_url: http://localhost:11023
grpc_address: localhost:11024
api_mode: rest  # or graphql
token: ""
api_key: ""
```

### Firewall Rules

```bash
# Ubuntu/Debian
sudo ufw allow 11023/tcp comment 'MDDB HTTP API'
sudo ufw allow 11024/tcp comment 'MDDB gRPC API'

# CentOS/RHEL/Fedora
sudo firewall-cmd --permanent --add-port=11023/tcp
sudo firewall-cmd --permanent --add-port=11024/tcp
sudo firewall-cmd --reload
```

### Upgrade

**Docker:**
```bash
docker pull tradik/mddb:latest
docker-compose down
docker-compose up -d
```

**Debian/Ubuntu:**
```bash
wget https://github.com/tradik/mddb/releases/latest/download/mddbd-latest-linux-amd64.deb
sudo dpkg -i mddbd-latest-linux-amd64.deb
sudo systemctl restart mddbd
```

**RHEL/CentOS/Fedora:**
```bash
wget https://github.com/tradik/mddb/releases/latest/download/mddbd-latest-linux-amd64.rpm
sudo rpm -Uvh mddbd-latest-linux-amd64.rpm
sudo systemctl restart mddbd
```

**Manual:**
```bash
# Backup database first!
cp /var/lib/mddb/mddb.db /var/lib/mddb/mddb.db.backup

# Download new binary
wget https://github.com/tradik/mddb/releases/latest/download/mddbd-latest-linux-amd64.tar.gz
tar xzf mddbd-latest-linux-amd64.tar.gz
sudo mv mddbd-latest-linux-amd64/mddbd /usr/local/bin/

# Restart service
sudo systemctl restart mddbd
```

---

## Upgrading

### mddb-cli updates itself

```bash
mddb-cli self-update --check    # report only
mddb-cli self-update            # download, verify and install
```

The release is fetched from a pinned GitHub URL, its SHA-256 checked against
the release's `checksums.txt`, and only then written. Nothing on disk is touched
until the download has been read to the end and matched, so an interrupted or
corrupted download leaves a working binary in place. The replacement is a
rename within the same directory, which is atomic — the path never holds half
a file — and the previous binary is kept as `mddb-cli.bak`.

**What the checksum does and does not prove.** It proves the download arrived
intact and was not swapped in transit. It does **not** prove the release itself
is genuine: whoever can publish a release can publish a matching checksum.
Closing that needs artifact signing, which this project has not set up. Said
plainly here rather than left to be inferred from the absence of a `.sig` file.

A binary installed by a package manager is not replaced:

```
mddb-cli installed as a snap — update with: sudo snap refresh mddb
```

Overwriting a file the manager owns leaves its records disagreeing with the
disk, and the next refresh undoes the update anyway.

### mddbd never updates itself

The daemon is a data server; an unexpected restart is an incident, not a
convenience. It reports and stops there:

```bash
mddbd --check-update
```

Exit codes: `0` up to date, `10` an update exists, `1` the check failed — so a
cron job or a CI step can act on the difference without parsing prose.

The same check runs once in the background at startup, after the server is
ready, and its answer is cached in `GET /health`:

```json
{ "status": "healthy",
  "update": { "current": "2.11.4", "latest": "v2.12.0", "available": true } }
```

An **absent** `update` field means the check is disabled or has not finished,
which is how a monitoring system tells "no update" from "we have not looked".

`MDDB_UPDATE_CHECK=0` turns the startup check off. It is one GET to a pinned
URL and carries nothing about the installation beyond the request itself — no
identifier, no collection names, no counts — but a deployment where any
outbound request needs a reason should be able to say no.

## Troubleshooting

### Port Already in Use

```bash
# Check what's using the port
sudo lsof -i :11023

# Kill the process or change MDDB port
export MDDB_HTTP_PORT=12023
mddbd
```

### Permission Denied

```bash
# Check file permissions
ls -l /var/lib/mddb/

# Fix ownership
sudo chown -R mddb:mddb /var/lib/mddb/
```

### Service Won't Start

```bash
# Check logs
sudo journalctl -u mddbd -n 50 --no-pager

# Check status
sudo systemctl status mddbd

# Restart service
sudo systemctl restart mddbd
```

### Binary Not Found

```bash
# Check PATH
echo $PATH

# Add to PATH in ~/.bashrc or ~/.zshrc
export PATH="/usr/local/bin:$PATH"
source ~/.bashrc
```

---

## Uninstall

**Docker:**
```bash
docker-compose down -v
docker rmi tradik/mddb
```

**Debian/Ubuntu:**
```bash
sudo systemctl stop mddbd
sudo systemctl disable mddbd
sudo dpkg -r mddbd mddb-cli
sudo rm -rf /var/lib/mddb
```

**RHEL/CentOS/Fedora:**
```bash
sudo systemctl stop mddbd
sudo systemctl disable mddbd
sudo rpm -e mddbd mddb-cli
sudo rm -rf /var/lib/mddb
```

**Manual:**
```bash
sudo systemctl stop mddbd
sudo systemctl disable mddbd
sudo rm /usr/local/bin/mddbd
sudo rm /usr/local/bin/mddb-cli
sudo rm -rf /var/lib/mddb
sudo rm /etc/systemd/system/mddbd.service
sudo systemctl daemon-reload
```

---

**[← Back to README](../README.md)** | **[Next: Quick Start →](QUICKSTART.md)**
