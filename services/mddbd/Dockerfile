# Multi-stage build for minimal image size
# Stage 1: Build
FROM golang:1.25-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git make protobuf protobuf-dev

# Install protoc plugins
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@latest && \
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# Set working directory
WORKDIR /build

# Copy go mod files
COPY services/mddbd/go.mod services/mddbd/go.sum ./

# Download dependencies
RUN go mod download

# Copy proto files
COPY proto /proto

# Copy source code
COPY services/mddbd/ ./

# Create proto directory and generate code
RUN mkdir -p proto && \
    protoc --go_out=proto --go_opt=paths=source_relative \
    --go-grpc_out=proto --go-grpc_opt=paths=source_relative \
    -I /proto /proto/mddb.proto

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s -extldflags "-static"' \
    -a -installsuffix cgo \
    -o mddbd .

# Stage 2: Runtime
FROM alpine:3.22

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

# Expose ports
EXPOSE 11023 11024

# Environment variables with defaults
ENV MDDB_ADDR=":11023" \
    MDDB_GRPC_ADDR=":11024" \
    MDDB_MODE="wr" \
    MDDB_PATH="/app/data/mddb.db"

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:11023/health || exit 1

# Run the binary
ENTRYPOINT ["/app/mddbd"]
