# Multi-stage build: Go backend
# Stage 1: Build Go binary
FROM golang:1.26-bookworm AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Run tests before building
RUN CGO_ENABLED=1 go test ./internal/... -count=1 -timeout 60s

RUN CGO_ENABLED=1 go build -o /tiktok-live-monitor ./cmd/tiktok-live-monitor/

# Stage 2: Minimal runtime
FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        libgomp1 \
        libstdc++6 \
        libatomic1 \
        curl \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=builder /tiktok-live-monitor /app/tiktok-live-monitor
COPY web/ ./web/
COPY model-config.json ./

RUN mkdir -p models bin

ENV HOST=0.0.0.0
ENV PORT=3000

EXPOSE 3000

HEALTHCHECK --interval=30s --timeout=10s --start-period=30s --retries=5 \
    CMD curl -sf "http://127.0.0.1:${PORT:-3000}/api/state" || exit 1

ENTRYPOINT ["/app/tiktok-live-monitor"]
