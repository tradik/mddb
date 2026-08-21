# Build stage
FROM rust:1.98-slim-bookworm AS builder

RUN apt-get update && apt-get install -y protobuf-compiler libprotobuf-dev && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Copy proto definitions
COPY proto/ /proto/

# Copy source
COPY services/mddb-chat/ .

# Update proto path for Docker context
RUN sed -i 's|../../proto/|/proto/|g' build.rs

RUN cargo build --release

# Runtime stage
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y ca-certificates wget && rm -rf /var/lib/apt/lists/* && \
    groupadd -r mddb && useradd -r -g mddb mddb

COPY --from=builder /app/target/release/mddb-chat /usr/local/bin/mddb-chat

USER mddb

EXPOSE 11030

HEALTHCHECK --interval=15s --timeout=3s --start-period=5s \
  CMD wget -q --spider http://localhost:11030/health || exit 1

ENTRYPOINT ["mddb-chat"]
