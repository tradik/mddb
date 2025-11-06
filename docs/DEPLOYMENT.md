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
FROM golang:1.25-alpine AS builder

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

### Prometheus Metrics (Future)

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

**High memory usage**:
```bash
# Check database size
ls -lh /var/lib/mddb/mddb.db

# Truncate old revisions
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
