# Development stage
FROM node:26.7-alpine3.24 AS development

WORKDIR /app

# Copy package files
COPY services/mddb-panel/package*.json ./

# Install the exact locked dependency graph without executing package lifecycle scripts.
RUN npm ci --ignore-scripts

# Copy source code
COPY services/mddb-panel/ .

# Copy shared scripts for build-time import
COPY scripts/mddb_model.py ./scripts/mddb_model.py

# Expose port
EXPOSE 3000

# Start dev server with hot reload
CMD ["npm", "run", "dev", "--", "--host", "0.0.0.0"]

# Builder stage
FROM node:26.7-alpine3.24 AS builder

WORKDIR /app

# Copy package files
COPY services/mddb-panel/package*.json ./

# Install dependencies without executing package lifecycle scripts.
RUN npm ci --ignore-scripts

# Copy source code
COPY services/mddb-panel/ .

# Copy shared scripts for build-time import
COPY scripts/mddb_model.py ./scripts/mddb_model.py

# Build application
RUN npm run build

# Production image
FROM node:26.7-alpine3.24 AS production

WORKDIR /app

# Copy package files and production server
COPY services/mddb-panel/package*.json ./
COPY services/mddb-panel/server.js ./

# Install production dependencies only (express, http-proxy-middleware),
# without executing package lifecycle scripts.
RUN npm ci --omit=dev --ignore-scripts

# Copy built application
COPY --from=builder /app/dist ./dist

# Expose port
EXPOSE 3000

# OPS-006: drop root — the official node image ships an unprivileged `node`
# user. The proxy binds the non-privileged port 3000, so no root is needed.
USER node

# OPS-006: liveness probe so compose can gate dependents (service_healthy) and
# restart a hung process. Try IPv6 first, then fall back to IPv4; both targets
# are loopback addresses inside the container.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["node", "-e", "const net=require('net');const check=(host,next)=>{const socket=net.connect(3000,host,()=>{socket.end();process.exit(0)});socket.setTimeout(3000);socket.once('error',next);socket.once('timeout',()=>{socket.destroy();next()})};check('::1',()=>check('127.0.0.1',()=>process.exit(1)))"]

# Start production server with reverse proxy
CMD ["node", "server.js"]
