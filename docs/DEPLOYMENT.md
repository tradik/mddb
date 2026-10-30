---
title: "MDDB Deployment Guide"
slug: "docs/deployment"
description: "Deploy MDDB to production: systemd service setup, reverse proxy configuration, backup strategy, monitoring, scaling and the hardening checklist."
status: publish
---

# MDDB Deployment Guide

## Production Deployment

### System Requirements

**Minimum**:
- CPU: 1 core
- RAM: 512 MB
- Disk: 1 GB + data storage
- OS: Linux, macOS, or Windows

**Recommended**:
- CPU: 2+ cores
- RAM: 2 GB
- Disk: SSD with 10 GB+ free space
- OS: Linux (Ubuntu 20.04+, Debian 11+, RHEL 8+)

### Building for Production

```bash
# Build optimized binary
cd services/mddbd
go build -ldflags="-s -w" -o mddbd .

# Or use Make
make build

# Cross-compile for Linux
GOOS=linux GOARCH=amd64 go build -o mddbd-linux .
```

### Systemd Service

Create `/etc/systemd/system/mddb.service`:

```ini
[Unit]
Description=MDDB Markdown Database Server
After=network.target

[Service]
Type=simple
User=mddb
Group=mddb
WorkingDirectory=/opt/mddb
Environment="MDDB_ADDR=:11023"
Environment="MDDB_MODE=wr"
Environment="MDDB_PATH=/var/lib/mddb/mddb.db"
ExecStart=/opt/mddb/mddbd
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=mddb

# Security settings
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/mddb

[Install]
WantedBy=multi-user.target
```

Enable and start:

```bash
# Create user and directories
sudo useradd -r -s /bin/false mddb
sudo mkdir -p /opt/mddb /var/lib/mddb
sudo chown mddb:mddb /var/lib/mddb

# Copy binary
sudo cp mddbd /opt/mddb/
sudo chown mddb:mddb /opt/mddb/mddbd
sudo chmod +x /opt/mddb/mddbd

# Enable and start service
sudo systemctl daemon-reload
sudo systemctl enable mddb
sudo systemctl start mddb

# Check status
sudo systemctl status mddb
```

### Docker Deployment

Create `Dockerfile`:

```dockerfile
FROM golang:1.27-alpine AS builder

WORKDIR /build
COPY services/mddbd/go.mod services/mddbd/go.sum ./
RUN go mod download

COPY services/mddbd/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o mddbd .

FROM alpine:latest

RUN apk --no-cache add ca-certificates
RUN addgroup -S mddb && adduser -S mddb -G mddb

WORKDIR /app
COPY --from=builder /build/mddbd .

RUN mkdir -p /data && chown mddb:mddb /data
USER mddb

EXPOSE 11023
VOLUME ["/data"]

ENV MDDB_ADDR=":11023"
ENV MDDB_MODE="wr"
ENV MDDB_PATH="/data/mddb.db"

CMD ["./mddbd"]
```

Build and run:

```bash
# Build image
docker build -t mddb:latest .

# Run container
docker run -d \
  --name mddb \
  -p 11023:11023 \
  -v mddb-data:/data \
  --restart unless-stopped \
  mddb:latest

# Check logs
docker logs -f mddb
```

### Docker Compose

Create `docker-compose.yml`:

```yaml
services:
  mddb:
    build: .
    container_name: mddb
    ports:
      - "11023:11023"
    volumes:
      - mddb-data:/data
    environment:
      - MDDB_ADDR=:11023
      - MDDB_MODE=wr
      - MDDB_PATH=/data/mddb.db
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "wget", "--quiet", "--tries=1", "--spider", "http://localhost:11023/v1/search"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 40s

volumes:
  mddb-data:
```

Run:

```bash
docker-compose up -d
```

## Reverse Proxy Setup

### Nginx

```nginx
upstream mddb {
    server localhost:11023;
}

server {
    listen 80;
    server_name mddb.example.com;

    # Redirect to HTTPS
    return 301 https://$server_name$request_uri;
}

server {
    listen 443 ssl http2;
    server_name mddb.example.com;

    ssl_certificate /etc/letsencrypt/live/mddb.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/mddb.example.com/privkey.pem;

    # Security headers
    add_header X-Frame-Options "SAMEORIGIN" always;
    add_header X-Content-Type-Options "nosniff" always;
    add_header X-XSS-Protection "1; mode=block" always;

    # Rate limiting
    limit_req_zone $binary_remote_addr zone=mddb_limit:10m rate=10r/s;
    limit_req zone=mddb_limit burst=20 nodelay;

    location / {
        proxy_pass http://mddb;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        
        # Timeouts
        proxy_connect_timeout 60s;
        proxy_send_timeout 60s;
        proxy_read_timeout 60s;
    }
}
```

### Caddy

```caddyfile
mddb.example.com {
    reverse_proxy localhost:11023
    
    # Rate limiting
    rate_limit {
        zone dynamic {
            key {remote_host}
            events 100
            window 1m
        }
    }
}
```

### Client IP behind the proxy

mddbd believes `X-Forwarded-For` and `X-Real-IP` only from peers listed in `MDDB_TRUSTED_PROXIES`. With a proxy in front, set it to the proxy's address (`MDDB_TRUSTED_PROXIES=127.0.0.1` for a proxy on the same host, or the Docker network's CIDR), otherwise mddbd's rate limiter, SSE cap and audit log see every client as the proxy. See [config.md#client-ip-and-trusted-proxies](config.md#client-ip-and-trusted-proxies).

## Persistence Guarantees

What MDDB promises about a write that returned success, and what it does not.

| Surface | On acknowledgement the write is… | Survives `kill -9`? |
|---|---|---|
| `POST /v1/add` (REST) | committed to bbolt and fsynced | **Yes** |
| gRPC `Add` | committed to bbolt and fsynced | **Yes** |
| Batch (`POST /v1/batch`, gRPC batch) | committed in one transaction and fsynced | **Yes** |
| MCP write tools | same path as REST | **Yes** |
| FTS index for the above | written in the same batch transaction | **Yes** |
| Async bulk job (`POST /v1/bulk`) | **queued, not written** — the payload lives in memory until the worker reaches it | **No** — an in-flight job is marked failed on restart |
| Embeddings | queued to a background worker after the document commits | **No** — re-embed by reindexing; the document itself is safe |

bbolt fsyncs on every commit, so durability does not depend on a graceful
shutdown. Verified rather than assumed: a document written over REST, then
`kill -9` with no shutdown hooks, is present — content and full-text index
intact — after the process restarts.

`NoFreelistSync` is enabled, which skips syncing the free-page list. That is a
recovery-time trade, not a durability one: the freelist is rebuilt from the
page tree when the file is opened.

### What breaks the guarantee

The engine fsyncs to whatever storage it was given. If that storage does not
outlive the process, neither does the data — which is what happens when a
container runs without a volume mounted at the data directory.

MDDB checks this at startup and says so, rather than accepting writes quietly:

```
{"level":"WARN","msg":"persistence warning","code":"ephemeral_storage",
 "detail":"the data directory is on tmpfs: an in-memory filesystem — its contents are lost on restart",
 "path":"/data"}
```

`GET /health` reports the same, so a probe or dashboard can see it:

```json
{
  "status": "healthy",
  "durable": false,
  "persistence": {
    "path": "/data",
    "filesystem": "overlay",
    "ephemeral": true,
    "writable": true,
    "warnings": [{"code": "ephemeral_storage", "detail": "…mount a volume at this path"}]
  }
}
```

| Warning | Meaning |
|---|---|
| `ephemeral_storage` | The data directory is on tmpfs, ramfs or a container's own overlay layer. Mount a volume. |
| `not_writable` | The directory cannot be written to — checked by creating a file, not by reading mode bits, since a read-only mount or a user mismatch looks writable otherwise. |
| `low_disk_space` | Free space is below `MDDB_DISK_MIN_FREE` (default 100 MB). Set it to `0` to disable the check. |

## Backup Strategy

### Automated Backups

```bash
#!/bin/bash
# /opt/mddb/backup.sh

BACKUP_DIR="/backups/mddb"
RETENTION_DAYS=30
DATE=$(date +%Y-%m-%d-%H%M%S)

# Create backup directory
mkdir -p ${BACKUP_DIR}

# Create backup
curl -s "http://localhost:11023/v1/backup?to=${BACKUP_DIR}/backup-${DATE}.db"

# Compress old backups
find ${BACKUP_DIR} -name "backup-*.db" -mtime +1 -exec gzip {} \;

# Remove old backups
find ${BACKUP_DIR} -name "backup-*.db.gz" -mtime +${RETENTION_DAYS} -delete

# Log
echo "$(date): Backup completed - backup-${DATE}.db" >> /var/log/mddb-backup.log
```

Add to crontab:

```bash
# Daily backup at 2 AM
0 2 * * * /opt/mddb/backup.sh
```

### Offsite Backup

```bash
#!/bin/bash
# Sync to S3
aws s3 sync /backups/mddb s3://my-bucket/mddb-backups/ \
  --storage-class STANDARD_IA \
  --exclude "*" \
  --include "backup-*.db.gz"

# Or use rsync
rsync -avz /backups/mddb/ backup-server:/backups/mddb/
```

## Monitoring

### Health Check Script

```bash
#!/bin/bash
# /opt/mddb/healthcheck.sh

ENDPOINT="http://localhost:11023/v1/search"
TIMEOUT=5

response=$(curl -s -o /dev/null -w "%{http_code}" --max-time ${TIMEOUT} \
  -X POST ${ENDPOINT} \
  -H 'Content-Type: application/json' \
  -d '{"collection":"_health","limit":1}')

if [ "$response" = "200" ] || [ "$response" = "400" ]; then
    echo "OK"
    exit 0
else
    echo "FAIL: HTTP $response"
    exit 1
fi
```

### Prometheus Metrics

`GET /metrics` serves Prometheus text format; the metrics are listed in
[TELEMETRY.md](TELEMETRY.md).

```yaml
# prometheus.yml
scrape_configs:
  - job_name: 'mddb'
    static_configs:
      - targets: ['localhost:11023']
```

## Performance Tuning

### OS Tuning

```bash
# Increase file descriptors
echo "mddb soft nofile 65536" >> /etc/security/limits.conf
echo "mddb hard nofile 65536" >> /etc/security/limits.conf

# Kernel parameters
cat >> /etc/sysctl.conf <<EOF
net.core.somaxconn = 1024
net.ipv4.tcp_max_syn_backlog = 2048
EOF

sysctl -p
```

### Database Optimization

```bash
# Regular maintenance
# Truncate old revisions weekly
curl -X POST http://localhost:11023/v1/truncate \
  -H 'Content-Type: application/json' \
  -d '{"collection":"blog","keepRevs":10,"dropCache":true}'
```

### Large Databases: Startup Time and Memory

By default bbolt does not store its freelist, so every start rebuilds it by
reading **every page of the database file**. Measured on a 908 MB database of
C code (889 file documents and 16,053 function documents, 45.6 MB of
content):

| | default | `MDDB_FREELIST_SYNC=true` (second start on) |
|---|---|---|
| opening the database | 390–425 ms | 7 ms |
| RSS after start | 938 MB | 37 MB |

The walk takes about 0.4 ms per MB when the file is in the page cache. From a
cold disk it costs a full read of the file: a 21 GB database at about 30 MB/s
takes over 10 minutes. The memory is the file's pages in the page cache. The
kernel can reclaim them, but they show up as RSS, and a container memory limit
counts them.

`MDDB_FREELIST_SYNC=true` stores the freelist with each commit, so a start
reads one page. The first start with the variable set still walks the file
once, to write the freelist. The cost is paid at write time. On tmpfs, without
fsync, a small commit went from 0.15 ms to 0.66 ms with about 16,000 free
pages. On a disk, where every commit already waits for fsync, the relative
cost is smaller. Use it for any database over a few gigabytes.

Since 2.18.1, every startup phase is logged with its duration (`startup phase
finished phase=... elapsed=...`). `/health` answers from the first moment with
`{"status":"warming_up","phase":"opening database",...}`, so a slow start can
be told apart from a hang. When opening takes longer than 30 seconds without
`MDDB_FREELIST_SYNC`, the log says so and names the setting. The Docker image's
health check allows 60 seconds before it counts failures. Raise
`start_period` further for a large database that has not been switched to
freelist sync yet.

**Since 2.18.2 a planned restart no longer walks the file.** A clean shutdown
(SIGTERM, SIGINT) stores the freelist with one commit, whatever
`MDDB_FREELIST_SYNC` says, and the next start reads it. Commits after that
start clear the stored list again, so after a crash the open still walks —
that is what keeps it correct. When it does walk, mddb first reads the file
front to back into the page cache if it fits in half the available memory
(`MDDB_PREWARM=auto`), and `/health` reports `progress` with bytes done, a
percentage and an ETA. Measured on a 2.5 GB fragmented file on WSL2 ext4,
page cache dropped before each start:

| start | opening the database |
|---|---|
| walk, cold (2.18.1) | 56–60 s |
| after a clean shutdown | 0.014 s |
| after a crash, with prewarm | 1.5 s read + 0.4 s walk |

After a crash the walk no longer blocks the start: the database opens after
reading its meta pages, reads are served at once, and the freelist is rebuilt
in the background (`MDDB_FREELIST_BACKGROUND`, on by default). Until it is,
writes answer 503 with `Retry-After`, and `/health` answers 200 with
`{"status":"degraded","reads":true,"writes":false,"phase":"rebuilding freelist","progress":{...}}`.
On the 2.5 GB file: reads 56 ms after start, writes after the 71 s walk.

`MDDB_FREELIST_SYNC` still matters where crashes are expected: it is what
spares the walk itself. It is on by default for files of 4 GiB or more. See [STORAGE-TUNING.md](STORAGE-TUNING.md)
for disks, filesystems and per-platform settings.

**Size.** For code documents, the database file came to about 20× the content
(908 MB for 45.6 MB of source). Each identifier is stored whole and in parts,
with positions for phrase search and per-field data for BM25F. Plan disk space
accordingly.

### Spinning Disks and fsync

Every write transaction ends in an fsync (see
[Persistence Guarantees](#persistence-guarantees)). On an SSD that costs well
under a millisecond; on a 7200 rpm disk, or ZFS with `sync=standard` and no
SLOG, it costs several, and a bulk load spends most of its time waiting for
them.

What a load commits:

| Work | Commits |
|---|---|
| `POST /v1/add-batch`, any size | 2 per request (documents with their meta index, then full-text) |
| Embedding, when a provider is configured | 1 per document, written by the background worker |

`mddb_db_write_transactions_total` counts them, so the rate is visible without
dtrace: divide its increase by the documents loaded over the same window.

If the database can be rebuilt from its sources — an index over a ports tree,
a mirror of a CMS — a dataset with `sync=disabled` (ZFS) or a mount without
barriers trades the last few seconds of writes on power loss for throughput.
A database that is the only copy of its data should keep fsync and get a SLOG
or an SSD instead.

## Security Hardening

### Firewall Rules

```bash
# UFW
sudo ufw allow 22/tcp
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw enable

# iptables
iptables -A INPUT -p tcp --dport 22 -j ACCEPT
iptables -A INPUT -p tcp --dport 80 -j ACCEPT
iptables -A INPUT -p tcp --dport 443 -j ACCEPT
iptables -A INPUT -j DROP
```

### API Authentication (Nginx)

```nginx
location / {
    # Basic auth
    auth_basic "MDDB API";
    auth_basic_user_file /etc/nginx/.htpasswd;
    
    proxy_pass http://mddb;
}
```

Create password file:

```bash
htpasswd -c /etc/nginx/.htpasswd admin
```

## Troubleshooting

### Check Logs

```bash
# Systemd
sudo journalctl -u mddb -f

# Docker
docker logs -f mddb

# File logs (if configured)
tail -f /var/log/mddb.log
```

### Common Issues

**Database locked**:
```bash
# Check for multiple instances
ps aux | grep mddbd

# Stop all instances
sudo systemctl stop mddb
```

**RSS grows under sustained load — is that a leak?**

Usually not, and the way to tell is to stop looking at RSS.

MDDB stores its data in a bbolt file that the process memory-maps. Those pages
count towards RSS and grow with the database, but they are file-backed and
reclaimable: the kernel drops them under pressure and reads them back from disk.
A process holding a 160 MB database will show a couple of hundred MB of RSS and
be using almost none of it as heap.

What distinguishes the two is `memoryHeap` — Go's `HeapInuse`, which counts only
what the program has allocated and not returned:

```bash
curl -s http://localhost:11023/v1/system/info | jq '{memoryHeap, memorySystem, numGoroutines}'
```

Measured over 45 minutes of mixed traffic — 1.26 million operations of adds,
updates, reads, keyword and hybrid search — against a database that grew to
160 MB:

| | Start | After 45 min |
|---|---|---|
| RSS | 190 MB | 247 MB |
| **Heap in use** | **43 MB** | **47 MB** |
| Goroutines | 38 | 42 |

RSS rose 57 MB and flattened; the heap did not move outside its normal
oscillation, and the largest single allocator grew during the first half and
*shrank* during the second — a buffer reaching steady state. The reproduction is
`tools/bench/soak` if you want to run it against your own workload.

**Watch the heap and the goroutine count, not RSS.** A heap that climbs across
successive readings hours apart, or a goroutine count that only goes up, is
worth reporting. RSS tracking the database file is the design working.

If the database itself is larger than you expect, old revisions are the usual
reason:

```bash
ls -lh /var/lib/mddb/mddb.db

curl -X POST http://localhost:11023/v1/truncate \
  -H 'Content-Type: application/json' \
  -d '{"collection":"blog","keepRevs":5}'
```

**Slow queries**:
- Add metadata indices
- Use pagination
- Optimize filters
- Consider caching layer

## Scaling

### Vertical Scaling
- Increase CPU/RAM
- Use SSD storage
- Optimize OS settings

### Horizontal Scaling
- Read replicas (file-based replication)
- Load balancer for reads
- Single write instance
- Consider sharding by collection

### Read Replicas

```bash
# On primary server
0 */6 * * * curl "http://localhost:11023/v1/backup?to=/replication/mddb.db"

# On replica servers
*/5 * * * * rsync -avz primary:/replication/mddb.db /var/lib/mddb/mddb.db
```

Run replicas in read-only mode:

```bash
MDDB_MODE="read" MDDB_ADDR=":11024" ./mddbd
```
