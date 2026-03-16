# Build stage
FROM golang:1.26.1-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o worker ./cmd/worker

# Production stage
FROM alpine:3.23.3

# Security update - CACHE_BUST is set by CI to force fresh apk upgrade
ARG CACHE_BUST
RUN apk upgrade --no-cache && apk add --no-cache ca-certificates tzdata

# Create non-root user
RUN addgroup -g 1000 app && adduser -D -u 1000 -G app app

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/worker .

# Set ownership
RUN chown -R app:app /app

# Switch to non-root user
USER app

# No EXPOSE - worker has no HTTP server

# Basic healthcheck - verify process is running
# For queue workers, actual health is monitored via RabbitMQ connection and logs
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD pgrep -x worker || exit 1

# Run the binary
CMD ["./worker"]
