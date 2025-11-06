# Docker Deployment Guide

> **Note**: The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD", "SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this document are to be interpreted as described in [RFC 2119](https://www.ietf.org/rfc/rfc2119.txt).

MDDB provides optimized Docker images based on Alpine Linux for minimal size and maximum security.

## Table of Contents

- [Quick Start](#quick-start)
- [Docker Images](#docker-images)
- [Production Deployment](#production-deployment)
- [Development Setup](#development-setup)
- [Configuration](#configuration)
- [Volumes and Persistence](#volumes-and-persistence)
- [Networking](#networking)
- [Health Checks](#health-checks)
- [Security](#security)
- [Troubleshooting](#troubleshooting)

## Quick Start

### Using Docker Compose (Recommended)

```bash
# Start production server
make docker-up

# Or manually
docker compose up -d
```

Access:
- **HTTP API**: http://localhost:11023
- **gRPC API**: localhost:11024

### Using Docker CLI

```bash
# Build image
make docker-build

# Run container
docker run -d \
  --name mddb \
  -p 11023:11023 \
  -p 11024:11024 \
  -v mddb-data:/app/data \
  mddb:latest
```

## Docker Images

### Production Image

**Base**: Alpine Linux 3.20  
**Size**: ~15 MB (compressed)  
**User**: Non-root (uid: 1000)

**Features**:
- Multi-stage build for minimal size
- Static binary (no dependencies)
- Health checks included
- Security hardened

**Build**:
```bash
make docker-build
# or
docker build -t mddb:latest -f services/mddbd/Dockerfile services/mddbd
```

### Development Image

**Base**: golang:1.25-alpine  
**Size**: ~500 MB  
**Features**: Hot reload with Air

**Build**:
```bash
make docker-build-dev
# or
docker build -t mddb:dev -f services/mddbd/Dockerfile.dev services/mddbd
```

## Production Deployment

### Docker Compose

**File**: `docker-compose.yml`

```yaml
services:
  mddb:
    image: mddb:latest
    container_name: mddb-server
    restart: unless-stopped
    ports:
      - "11023:11023"  # HTTP
      - "11024:11024"  # gRPC
    volumes:
      - mddb-data:/app/data
      - ./backups:/app/backups
    environment:
      - MDDB_ADDR=:11023
      - MDDB_GRPC_ADDR=:11024
      - MDDB_MODE=wr
      - MDDB_PATH=/app/data/mddb.db
      - TZ=UTC
    networks:
      - mddb-network
    healthcheck:
      test: ["CMD", "wget", "--spider", "http://localhost:11023/v1/stats"]
      interval: 30s
      timeout: 3s
      retries: 3
```

### Commands

```bash
# Start
make docker-up

# Stop
make docker-down

# View logs
make docker-logs

# Shell access
make docker-shell

# Clean up
make docker-clean
```

## Development Setup

### With Hot Reload

```bash
# Start development server
make docker-up-dev

# View logs
make docker-logs-dev
```

**Features**:
- Automatic rebuild on code changes
- Source code mounted as volume
- Air hot reload
- Go module cache persisted

### Development Workflow

```bash
# 1. Start dev server
make docker-up-dev

# 2. Edit code in services/mddbd/
# Changes are automatically detected and server reloads

# 3. View logs
make docker-logs-dev

# 4. Stop when done
make docker-down
```

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `MDDB_ADDR` | `:11023` | HTTP API address |
| `MDDB_GRPC_ADDR` | `:11024` | gRPC API address |
| `MDDB_MODE` | `wr` | Access mode (read/write/wr) |
| `MDDB_PATH` | `/app/data/mddb.db` | Database file path |
| `TZ` | `UTC` | Timezone |

### Custom Configuration

Create `.env` file:

```bash
MDDB_ADDR=:8080
MDDB_GRPC_ADDR=:8081
MDDB_MODE=read
TZ=Europe/Warsaw
```

Use with Docker Compose:

```yaml
services:
  mddb:
    env_file: .env
    # ... rest of config
```

## Volumes and Persistence

### Named Volumes (Recommended)

```yaml
volumes:
  mddb-data:
    driver: local
    name: mddb-data
```

**Advantages**:
- Managed by Docker
- Portable
- Easy backup

**Commands**:
```bash
# List volumes
docker volume ls

# Inspect volume
docker volume inspect mddb-data

# Backup volume
docker run --rm \
  -v mddb-data:/data \
  -v $(pwd):/backup \
  alpine tar czf /backup/mddb-backup.tar.gz -C /data .

# Restore volume
docker run --rm \
  -v mddb-data:/data \
  -v $(pwd):/backup \
  alpine tar xzf /backup/mddb-backup.tar.gz -C /data
```

### Bind Mounts

```yaml
volumes:
  - ./data:/app/data
  - ./backups:/app/backups
```

**Advantages**:
- Direct file access
- Easy development
- Simple backup

## Networking

### Default Network

```yaml
networks:
  mddb-network:
    driver: bridge
    name: mddb-network
```

### Multiple Services

```yaml
services:
  mddb:
    networks:
      - mddb-network
  
  app:
    networks:
      - mddb-network
    environment:
      - MDDB_HTTP_URL=http://mddb:11023
      - MDDB_GRPC_URL=mddb:11024
```

### External Network

```bash
# Create network
make docker-setup-network

# Use in compose
networks:
  mddb-network:
    external: true
    name: mddb-network
```

## Health Checks

### Built-in Health Check

```dockerfile
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --spider http://localhost:11023/v1/stats || exit 1
```

### Check Status

```bash
# View health status
docker ps

# Detailed health info
docker inspect mddb-server | jq '.[0].State.Health'
```

### Custom Health Check

```yaml
healthcheck:
  test: ["CMD", "wget", "--spider", "http://localhost:11023/v1/stats"]
  interval: 10s
  timeout: 5s
  retries: 5
  start_period: 10s
```

## Security

### Non-Root User

Container runs as user `mddb` (uid: 1000):

```dockerfile
RUN addgroup -g 1000 mddb && \
    adduser -D -u 1000 -G mddb mddb
USER mddb
```

### Read-Only Filesystem

```yaml
services:
  mddb:
    read_only: true
    tmpfs:
      - /tmp
      - /app/tmp
    volumes:
      - mddb-data:/app/data:rw
```

### Resource Limits

```yaml
services:
  mddb:
    deploy:
      resources:
        limits:
          cpus: '1.0'
          memory: 512M
        reservations:
          cpus: '0.5'
          memory: 256M
```

### Security Options

```yaml
services:
  mddb:
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    cap_add:
      - NET_BIND_SERVICE
```

## Advanced Configuration

### Behind Reverse Proxy

#### Nginx

```nginx
upstream mddb_http {
    server localhost:11023;
}

upstream mddb_grpc {
    server localhost:11024;
}

server {
    listen 80;
    server_name api.example.com;

    location / {
        proxy_pass http://mddb_http;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
    }
}

server {
    listen 443 ssl http2;
    server_name grpc.example.com;

    location / {
        grpc_pass grpc://mddb_grpc;
    }
}
```

#### Traefik

```yaml
services:
  mddb:
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.mddb.rule=Host(`api.example.com`)"
      - "traefik.http.services.mddb.loadbalancer.server.port=11023"
```

### Multi-Container Setup

```yaml
services:
  mddb-primary:
    image: mddb:latest
    environment:
      - MDDB_MODE=wr
    volumes:
      - mddb-primary:/app/data

  mddb-replica:
    image: mddb:latest
    environment:
      - MDDB_MODE=read
    volumes:
      - mddb-replica:/app/data
```

## Troubleshooting

### Container Won't Start

```bash
# Check logs
docker logs mddb-server

# Check health
docker inspect mddb-server | jq '.[0].State'

# Test manually
docker run --rm -it mddb:latest sh
```

### Permission Issues

```bash
# Fix volume permissions
docker run --rm \
  -v mddb-data:/data \
  alpine chown -R 1000:1000 /data
```

### Database Corruption

```bash
# Stop container
make docker-down

# Backup current database
docker run --rm \
  -v mddb-data:/data \
  -v $(pwd):/backup \
  alpine cp /data/mddb.db /backup/mddb.db.backup

# Restore from backup
docker run --rm \
  -v mddb-data:/data \
  -v $(pwd):/backup \
  alpine cp /backup/backup-xxx.db /data/mddb.db

# Start container
make docker-up
```

### Performance Issues

```bash
# Check resource usage
docker stats mddb-server

# Increase limits
docker update --memory=1g --cpus=2 mddb-server
```

### Network Issues

```bash
# Test connectivity
docker exec mddb-server wget -O- http://localhost:11023/v1/stats

# Check network
docker network inspect mddb-network

# Recreate network
docker network rm mddb-network
make docker-setup-network
```

## Best Practices

### Production Checklist

- ✅ Use named volumes for data persistence
- ✅ Set resource limits
- ✅ Enable health checks
- ✅ Run as non-root user
- ✅ Use read-only filesystem where possible
- ✅ Configure proper logging
- ✅ Set up monitoring
- ✅ Regular backups
- ✅ Use secrets for sensitive data
- ✅ Keep images updated

### Backup Strategy

```bash
#!/bin/bash
# Daily backup script

DATE=$(date +%Y-%m-%d)
BACKUP_DIR="/backups"

# Create backup
docker exec mddb-server wget -O- \
  "http://localhost:11023/v1/backup?to=backup-${DATE}.db"

# Copy to host
docker cp mddb-server:/app/data/backup-${DATE}.db \
  ${BACKUP_DIR}/

# Keep last 7 days
find ${BACKUP_DIR} -name "backup-*.db" -mtime +7 -delete
```

### Monitoring

```yaml
services:
  mddb:
    labels:
      - "prometheus.scrape=true"
      - "prometheus.port=11023"
      - "prometheus.path=/v1/stats"
```

## See Also

- [Deployment Guide](DEPLOYMENT.md) - General deployment instructions
- [API Documentation](API.md) - API reference
- [Architecture](ARCHITECTURE.md) - System architecture

## License

MIT License - see [LICENSE](../LICENSE) for details.
