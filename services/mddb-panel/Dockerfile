FROM node:24-alpine AS builder

WORKDIR /app

# Copy package files
COPY package*.json ./

# Install dependencies
RUN npm ci

# Copy source code
COPY . .

# Build application
RUN npm run build

# Production image
FROM node:24-alpine

WORKDIR /app

# Copy package files
COPY package*.json ./

# Install all dependencies (including vite for preview)
RUN npm ci

# Copy built application
COPY --from=builder /app/dist ./dist

# Expose port
EXPOSE 3000

# Start application
CMD ["sh", "-c", "VITE_PROD=true VITE_MDBB_SERVER=localhost:11023 npm run preview -- --host 0.0.0.0 --port 3000"]
