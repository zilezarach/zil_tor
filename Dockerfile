# Multi-stage build for smaller final image
FROM golang:1.24-alpine AS builder


# Install build dependencies
RUN apk add --no-cache git gcc musl-dev

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
# CGO_ENABLED=0 for static binary (no C dependencies)
RUN CGO_ENABLED=1 GOOS=linux go build -a -installsuffix cgo -o zil-tor-api ./cmd/server

# Final stage - minimal runtime image
FROM alpine:latest

# Install runtime dependencies
RUN apk --no-cache add ca-certificates tzdata

# Create non-root user
RUN addgroup -g 1000 torrent && \
	adduser -D -u 1000 -G torrent torrent

# Set working directory
WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/zil_tor-api .

# Copy any config files if needed
# COPY config/ ./config/

# Change ownership
RUN chown -R torrent:torrent /app

# Switch to non-root user
USER torrent

# Expose port
EXPOSE 9117

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
	CMD wget --no-verbose --tries=1 --spider http://localhost:9117/api/v1/health || exit 1

# Run the application
CMD ["./zil_tor-api"]
