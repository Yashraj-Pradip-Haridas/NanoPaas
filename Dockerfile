# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git ca-certificates

# Copy all source code first (go mod tidy needs to see all .go files)
COPY . .

# Download dependencies and generate go.sum
RUN go mod tidy && go mod download

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o /app/nanopaas ./cmd/nanopaas

# Runtime stage
FROM alpine:3.19

# Install runtime dependencies
RUN apk add --no-cache ca-certificates tzdata git docker-cli

# Create non-root user
RUN adduser -D -u 1000 nanopaas

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/nanopaas .

# Create directories for traefik config
RUN mkdir -p /traefik/dynamic && chown -R nanopaas:nanopaas /traefik

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q --spider http://localhost:8080/health || exit 1

ENTRYPOINT ["./nanopaas"]
