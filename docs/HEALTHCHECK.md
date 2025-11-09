# Health Check Guide

MDDB provides health check endpoints for monitoring and orchestration tools like Docker, Kubernetes, and load balancers.

## Health Check Endpoints

### `/health`
Simple health check endpoint that verifies database connectivity.

**Response (Healthy):**
```json
{
  "status": "healthy",
  "mode": "wr"
}
```

**Response (Unhealthy):**
```json
{
  "status": "unhealthy",
  "error": "database connection error"
}
```

**HTTP Status Codes:**
- `200 OK` - Service is healthy
- `503 Service Unavailable` - Service is unhealthy

### `/v1/health`
Alias for `/health` endpoint (same functionality).

### `/v1/stats`
Detailed statistics endpoint (can also be used for health checks, but returns more data).

## Docker Health Checks

### Docker Run

```bash
docker run -d \
  --name mddb \
  -p 11023:11023 \
  -p 11024:11024 \
  --health-cmd="wget --no-verbose --tries=1 --spider http://localhost:11023/health || exit 1" \
  --health-interval=30s \
  --health-timeout=3s \
  --health-retries=3 \
  --health-start-period=5s \
  tradik/mddb:latest
```

### Docker Compose

```yaml
services:
  mddb:
    image: tradik/mddb:latest
    ports:
      - "11023:11023"
      - "11024:11024"
    healthcheck:
      test: ["CMD", "wget", "--no-verbose", "--tries=1", "--spider", "http://localhost:11023/health"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 10s
```

### Check Container Health

```bash
# Check health status
docker ps

# View health check logs
docker inspect --format='{{json .State.Health}}' mddb | jq

# Wait for container to be healthy
docker-compose up -d
docker-compose ps
```

## Kubernetes Health Checks

### Liveness and Readiness Probes

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: mddb
spec:
  containers:
  - name: mddb
    image: tradik/mddb:latest
    ports:
    - containerPort: 11023
      name: http
    - containerPort: 11024
      name: grpc
    env:
    - name: MDDB_EXTREME
      value: "true"
    livenessProbe:
      httpGet:
        path: /health
        port: 11023
      initialDelaySeconds: 10
      periodSeconds: 30
      timeoutSeconds: 3
      failureThreshold: 3
    readinessProbe:
      httpGet:
        path: /health
        port: 11023
      initialDelaySeconds: 5
      periodSeconds: 10
      timeoutSeconds: 3
      failureThreshold: 3
```

### Kubernetes Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: mddb
  labels:
    app: mddb
spec:
  replicas: 1
  selector:
    matchLabels:
      app: mddb
  template:
    metadata:
      labels:
        app: mddb
    spec:
      containers:
      - name: mddb
        image: tradik/mddb:latest
        ports:
        - containerPort: 11023
          name: http
          protocol: TCP
        - containerPort: 11024
          name: grpc
          protocol: TCP
        env:
        - name: MDDB_PATH
          value: "/data/mddb.db"
        - name: MDDB_MODE
          value: "wr"
        - name: MDDB_EXTREME
          value: "true"
        volumeMounts:
        - name: data
          mountPath: /data
        livenessProbe:
          httpGet:
            path: /health
            port: 11023
          initialDelaySeconds: 10
          periodSeconds: 30
          timeoutSeconds: 3
          failureThreshold: 3
        readinessProbe:
          httpGet:
            path: /health
            port: 11023
          initialDelaySeconds: 5
          periodSeconds: 10
          timeoutSeconds: 3
          failureThreshold: 3
        resources:
          requests:
            memory: "128Mi"
            cpu: "100m"
          limits:
            memory: "512Mi"
            cpu: "500m"
      volumes:
      - name: data
        persistentVolumeClaim:
          claimName: mddb-data
---
apiVersion: v1
kind: Service
metadata:
  name: mddb
spec:
  selector:
    app: mddb
  ports:
  - name: http
    port: 11023
    targetPort: 11023
  - name: grpc
    port: 11024
    targetPort: 11024
  type: ClusterIP
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: mddb-data
spec:
  accessModes:
  - ReadWriteOnce
  resources:
    requests:
      storage: 10Gi
```

## Manual Health Checks

### Using curl

```bash
# Simple health check
curl http://localhost:11023/health

# With verbose output
curl -v http://localhost:11023/health

# Check HTTP status code
curl -s -o /dev/null -w "%{http_code}" http://localhost:11023/health
```

### Using wget

```bash
# Simple health check
wget -q -O- http://localhost:11023/health

# Spider mode (no download)
wget --no-verbose --tries=1 --spider http://localhost:11023/health
```

### Using httpie

```bash
# Simple health check
http GET http://localhost:11023/health
```

## Load Balancer Configuration

### Nginx

```nginx
upstream mddb_backend {
    server mddb1:11023 max_fails=3 fail_timeout=30s;
    server mddb2:11023 max_fails=3 fail_timeout=30s;
}

server {
    listen 80;
    server_name mddb.example.com;

    location /health {
        access_log off;
        proxy_pass http://mddb_backend/health;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
    }

    location / {
        proxy_pass http://mddb_backend;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

### HAProxy

```haproxy
frontend mddb_frontend
    bind *:80
    default_backend mddb_backend

backend mddb_backend
    balance roundrobin
    option httpchk GET /health
    http-check expect status 200
    server mddb1 mddb1:11023 check inter 30s fall 3 rise 2
    server mddb2 mddb2:11023 check inter 30s fall 3 rise 2
```

### Traefik (Docker Labels)

```yaml
services:
  mddb:
    image: tradik/mddb:latest
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.mddb.rule=Host(`mddb.example.com`)"
      - "traefik.http.services.mddb.loadbalancer.server.port=11023"
      - "traefik.http.services.mddb.loadbalancer.healthcheck.path=/health"
      - "traefik.http.services.mddb.loadbalancer.healthcheck.interval=30s"
      - "traefik.http.services.mddb.loadbalancer.healthcheck.timeout=3s"
```

## Monitoring Integration

### Prometheus

```yaml
scrape_configs:
  - job_name: 'mddb'
    metrics_path: '/health'
    static_configs:
      - targets: ['mddb:11023']
```

### Grafana Alert

```yaml
- alert: MDDBUnhealthy
  expr: up{job="mddb"} == 0
  for: 1m
  labels:
    severity: critical
  annotations:
    summary: "MDDB instance is down"
    description: "MDDB instance {{ $labels.instance }} has been down for more than 1 minute."
```

## Health Check Best Practices

### Timing Configuration

**Docker/Docker Compose:**
- `interval`: 30s (how often to check)
- `timeout`: 3-10s (max time to wait for response)
- `retries`: 3 (failures before marking unhealthy)
- `start_period`: 5-10s (grace period on startup)

**Kubernetes:**
- `initialDelaySeconds`: 5-10s (wait before first check)
- `periodSeconds`: 10-30s (how often to check)
- `timeoutSeconds`: 3s (max time to wait)
- `failureThreshold`: 3 (failures before restart)

### Recommendations

1. **Use `/health` for orchestration** - Lightweight, fast response
2. **Use `/v1/stats` for monitoring** - Detailed metrics for dashboards
3. **Set appropriate timeouts** - Balance between responsiveness and false positives
4. **Configure start period** - Allow time for database initialization
5. **Monitor health check logs** - Track failures and patterns
6. **Test health checks** - Verify configuration before deployment

## Troubleshooting

### Health Check Fails Immediately

```bash
# Check if service is running
docker ps

# Check service logs
docker logs mddb

# Test health endpoint manually
curl http://localhost:11023/health
```

### Health Check Times Out

```bash
# Increase timeout in health check configuration
# Check database file permissions
# Verify database path is correct
# Check available disk space
```

### Container Keeps Restarting

```bash
# Check health check configuration
docker inspect mddb | jq '.[] | .Config.Healthcheck'

# View health check logs
docker inspect mddb | jq '.[] | .State.Health'

# Increase start_period or retries
```

### False Positives

```bash
# Increase interval between checks
# Increase failure threshold
# Check system resources (CPU, memory, disk I/O)
# Review application logs for intermittent issues
```

## See Also

- [Docker Documentation](../docs/DOCKER.md)
- [Deployment Guide](../docs/DEPLOYMENT.md)
- [API Documentation](../docs/API.md)
