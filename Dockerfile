# Builder stage
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build a static binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o zil_tor-api ./cmd/server

# Final stage
FROM alpine:latest

# Install runtime deps + Chromium
RUN apk --no-cache add \
    ca-certificates \
    tzdata \
    chromium \
    nss \
    freetype \
    harfbuzz \
    ttf-freefont \
    wget

# Tell your Go app where Chrome is
ENV CHROME_BIN=/usr/bin/chromium-browser

WORKDIR /app

COPY --from=builder /app/zil_tor-api .

RUN addgroup -g 1000 torrent && \
    adduser -D -u 1000 -G torrent torrent && \
    chown -R torrent:torrent /app

USER torrent

EXPOSE 9117

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:9117/api/v1/health || exit 1

CMD ["./zil_tor-api"]


