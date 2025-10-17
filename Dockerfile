# syntax=docker/dockerfile:1.4

# Build stage
FROM golang:1.24-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /src

# Copy go modules manifests first for better caching
COPY go.mod ./
RUN go mod download && go mod verify

# Copy source code
COPY . .

# Tidy modules and build
RUN go mod tidy

# Build static binary with optimizations
ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64

RUN go build \
    -trimpath \
    -ldflags "-s -w -X main.version=$(git describe --tags --always --dirty 2>/dev/null || echo 'dev')" \
    -o /account-api \
    ./main.go

# Production stage - minimal distroless image
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app

# Copy timezone data and CA certificates from builder
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy the binary
COPY --from=builder /account-api /app/account-api

# Expose port (informational)
EXPOSE 8086

# Use non-root user (already default in distroless)
USER nonroot:nonroot

# Health check (if your app has a health endpoint)
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/app/account-api", "health"] || exit 1

ENTRYPOINT ["/app/account-api"]