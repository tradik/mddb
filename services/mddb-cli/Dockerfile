# Build stage
FROM golang:1.26.5-alpine AS builder

WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY main.go ./

# Build binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -ldflags="-s -w" -o mddb-cli .

# Runtime stage
FROM alpine:3.24

RUN apk --no-cache add ca-certificates bash

# Create non-root user (OPS-017)
RUN addgroup -g 1000 mddb && \
    adduser -D -u 1000 -G mddb mddb

WORKDIR /app

# Copy binary
COPY --from=builder /build/mddb-cli .

# Copy man page if needed
COPY mddb-cli.1 /usr/share/man/man1/

RUN chown -R mddb:mddb /app

# Drop root (OPS-017)
USER mddb

# Set CLI as entrypoint
ENTRYPOINT ["./mddb-cli"]
CMD ["--help"]
