# Build stage
FROM node:26.8-alpine3.24 AS builder

WORKDIR /app

COPY services/mddb-chat-widget/package.json services/mddb-chat-widget/package-lock.json* ./

RUN npm install

COPY services/mddb-chat-widget/ .

RUN npm run build

# Runtime stage — serve static files via the unprivileged nginx image.
# OPS-006: nginxinc/nginx-unprivileged runs as uid 101 (non-root) and listens
# on 8080 by default, so the container needs no root.
FROM nginxinc/nginx-unprivileged:1.31.5-alpine3.24

COPY --from=builder /app/dist/ /usr/share/nginx/html/

EXPOSE 8080

# OPS-006: liveness probe so compose can detect a hung/unready server.
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD wget -q --spider http://localhost:8080/ || exit 1

CMD ["nginx", "-g", "daemon off;"]
