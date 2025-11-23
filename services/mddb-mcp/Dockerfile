# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /build

# Copy entire project for go.mod replace directive
COPY . .

# Change to MCP directory
WORKDIR /build/services/mddb-mcp

# Download dependencies
RUN go mod download

# Build both binaries
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o mddb-mcp ./cmd/mddb-mcp
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o mddb-mcp-stdio ./cmd/mddb-mcp-stdio

# Runtime stage - supports both HTTP and stdio modes
FROM alpine:3.22

RUN apk --no-cache add ca-certificates

WORKDIR /app

# Copy both binaries, config, and entrypoint
COPY --from=builder /build/services/mddb-mcp/mddb-mcp .
COPY --from=builder /build/services/mddb-mcp/mddb-mcp-stdio .
COPY --from=builder /build/services/mddb-mcp/config.yaml .
COPY --from=builder /build/services/mddb-mcp/entrypoint.sh .

# Make entrypoint executable
RUN chmod +x entrypoint.sh

# Expose MCP port (only used in HTTP mode)
EXPOSE 9000

# Health check (only works in HTTP mode)
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD [ "$MCP_MODE" = "stdio" ] || wget --no-verbose --tries=1 --spider http://localhost:9000/health || exit 1

# Use entrypoint to select mode via MCP_MODE env var
# Default: http mode
# Set MCP_MODE=stdio for stdio mode
ENTRYPOINT ["./entrypoint.sh"]
