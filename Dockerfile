# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git make

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o evilginx2 main.go

# Runtime stage
FROM alpine:latest

WORKDIR /app

# Install runtime dependencies if needed
RUN apk add --no-cache ca-certificates bash

# Copy binary from builder
RUN mkdir -p /app/build
COPY --from=builder /app/evilginx2 ./build/evilginx2
RUN chmod +x ./build/evilginx2

# Copy phishlets and other resources
COPY phishlets ./phishlets
COPY redirectors ./redirectors
COPY setup ./setup
COPY .evilginx ./.evilginx
RUN chmod -R 700 ./.evilginx

# Create data directory for persistent storage
RUN mkdir -p /app/data /app/log

# Make setup scripts executable and fix line endings
RUN sed -i 's/\r$//' ./setup/build_run.sh && chmod +x ./setup/build_run.sh

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/ || exit 1

# Run using setup script
ENTRYPOINT ["bash", "./setup/build_run.sh"]
