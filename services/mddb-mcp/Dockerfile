# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /build

# Copy entire project for go.mod replace directive
COPY . .

# Change to MCP directory
WORKDIR /build/services/mddb-mcp

# Download dependencies
RUN go mod download

# Build binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o mddb-mcp ./cmd/mddb-mcp

# Runtime stage
FROM alpine:3.22

RUN apk --no-cache add ca-certificates

WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/services/mddb-mcp/mddb-mcp .
COPY --from=builder /build/services/mddb-mcp/config.yaml .

# Expose MCP port
EXPOSE 9000

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:9000/health || exit 1

CMD ["./mddb-mcp"]
