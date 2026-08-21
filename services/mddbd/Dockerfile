# Multi-stage build for minimal image size
# Stage 1: Build
FROM golang:1.27.0-alpine AS builder

# Install build dependencies (git for VCS stamping, make for Makefile targets)
RUN apk add --no-cache git make

# Set working directory
WORKDIR /build

# Copy go mod files first for better layer caching
COPY services/mddbd/go.mod services/mddbd/go.sum ./

# Download dependencies
RUN go mod download

# Copy source code — INCLUDING the committed services/mddbd/proto/*.pb.go
# files. Proto code generation happens OUTSIDE the Docker build via
# `buf generate` (see buf.gen.yaml at repo root), with plugin versions
# pinned for reproducibility. CI enforces that committed .pb.go matches
# what buf would produce via `git diff --exit-code` after regeneration.
# Regenerating inside the Docker build with `go install @latest` plugins
# would defeat those pinned versions.
COPY services/mddbd/ ./

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s -extldflags "-static"' \
    -a -installsuffix cgo \
    -o mddbd .

# Stage 2: Runtime
FROM alpine:3.24

# Add ca-certificates for HTTPS and timezone data
RUN apk --no-cache add ca-certificates tzdata

# Create non-root user
RUN addgroup -g 1000 mddb && \
    adduser -D -u 1000 -G mddb mddb

# Set working directory
WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/mddbd /app/mddbd

# Create data directory
RUN mkdir -p /app/data && \
    chown -R mddb:mddb /app

# Switch to non-root user
USER mddb

# Expose ports (HTTP API, gRPC, MCP, HTTP/3)
EXPOSE 11023 11024 9000 11443

# Environment variables with defaults
ENV MDDB_ADDR=":11023" \
    MDDB_GRPC_ADDR=":11024" \
    MDDB_LOG_FORMAT="json" \
    MDDB_LOG_LEVEL="info" \
    MDDB_MODE="wr" \
    MDDB_PATH="/app/data/mddb.db" \
    MDDB_HTTP_ENABLED="true" \
    MDDB_GRPC_ENABLED="true" \
    MDDB_MCP_ENABLED="true" \
    MDDB_MCP_ADDR=":9000" \
    MDDB_MCP_STDIO="false" \
    MDDB_MCP_CONFIG="" \
    MDDB_HTTP3_ENABLED="false" \
    MDDB_HTTP3_ADDR=":11443" \
    MDDB_AUTH_ENABLED="false" \
    MDDB_AUTH_JWT_SECRET="" \
    MDDB_AUTH_JWT_EXPIRY="24h" \
    MDDB_AUTH_ADMIN_USERNAME="admin" \
    MDDB_AUTH_ADMIN_PASSWORD=""

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:11023/health || exit 1

# Run the binary
ENTRYPOINT ["/app/mddbd"]
