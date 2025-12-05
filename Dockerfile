# syntax=docker/dockerfile:1.4

# Build args (preserve service's Go version)
ARG GO_VERSION=1.23
ARG ALPINE_VERSION=3.19

# ===========================================
# Build stage
# ===========================================
FROM golang:${GO_VERSION}-alpine AS builder

# Install build dependencies
RUN apk add --no-cache \
    git \
    ca-certificates \
    tzdata \
    wget

# Use dedicated workdir
WORKDIR /src

# Copy go modules manifests first for better caching (include go.sum if present)
COPY go.mod go.sum* ./

# Download dependencies with retry logic
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download && go mod verify

# Copy source code
COPY . .

# Tidy modules
RUN go mod tidy

# Build static binary with optimizations
ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64 \
    GOFLAGS="-trimpath"

RUN --mount=type=cache,target=/root/.cache/go-build \
    go build \
    -trimpath \
    -ldflags="-s -w -X main.version=$(git describe --tags --always --dirty 2>/dev/null || echo 'dev') -X main.buildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o /app-binary \
    ./main.go

# Verify the binary was built
RUN test -f /app-binary && chmod +x /app-binary

# ===========================================
# Production stage - Alpine for health checks
# ===========================================
# Runtime image
FROM alpine:${ALPINE_VERSION}

# Install runtime dependencies
RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    wget \
    curl && \
    addgroup -g 1001 -S appuser && \
    adduser -u 1001 -S appuser -G appuser

WORKDIR /app

# Copy timezone data and CA certificates from builder
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy the binary
COPY --from=builder --chown=appuser:appuser /app-binary /app/service

# Switch to non-root user
USER appuser:appuser

# Expose port (matches docker-compose PORT=8082)
EXPOSE 8082

# Lightweight, reliable HTTP healthcheck using curl (exec form)
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["curl", "-f", "http://localhost:8082/health"]

# Set entrypoint
ENTRYPOINT ["/app/service"]