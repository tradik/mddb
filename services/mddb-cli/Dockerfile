# mddb-cli image. Build from the repository root, because go.mod replaces the
# Go client with ../../clients/go/mddb:
#
#   docker build -f services/mddb-cli/Dockerfile -t mddb-cli .
#
# Built from services/mddb-cli alone, as before 2.18.9, it could not compile:
# only main.go was copied and the client was outside the context.

# Build stage
FROM golang:1.27.3-alpine AS builder

WORKDIR /src

# The client first, then the CLI's module files, so dependencies are cached
# until one of them changes.
COPY clients/go/mddb/ clients/go/mddb/
COPY services/mddb-cli/go.mod services/mddb-cli/go.sum services/mddb-cli/

WORKDIR /src/services/mddb-cli
RUN go mod download

COPY services/mddb-cli/ ./

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /build/mddb-cli .

# Runtime stage
FROM alpine:3.24

RUN apk --no-cache add ca-certificates bash

# Create non-root user (OPS-017)
RUN addgroup -g 1000 mddb && \
    adduser -D -u 1000 -G mddb mddb

WORKDIR /app

# Copy binary
COPY --from=builder /build/mddb-cli .

# Man page
COPY services/mddb-cli/mddb-cli.1 /usr/share/man/man1/

RUN chown -R mddb:mddb /app

# Drop root (OPS-017)
USER mddb

# Set CLI as entrypoint
ENTRYPOINT ["./mddb-cli"]
CMD ["--help"]
