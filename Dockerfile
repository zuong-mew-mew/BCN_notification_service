# Multi-stage Dockerfile: Go Backend + Node.js Runtime for Zalo Engine

# Stage 1: Build Go Binary
FROM golang:1.22-alpine AS go-builder
WORKDIR /app
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o server .

# Stage 2: Runtime Environment (Node.js 20 on Alpine)
FROM node:20-alpine
WORKDIR /app

# Copy Go server binary
COPY --from=go-builder /app/server .

# Copy frontend static files
COPY web ./web

# Copy Zalo sync engine and install production npm dependencies
COPY zalo_sync ./zalo_sync
WORKDIR /app/zalo_sync
RUN npm install --omit=dev

WORKDIR /app
ENV PORT=8080
EXPOSE 8080

CMD ["./server"]
