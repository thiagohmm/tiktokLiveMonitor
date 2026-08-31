# Multi-stage build: Go backend (lite, sem IA)
# Multi-arch: linux/amd64 (servidores) e linux/arm64 (Raspberry Pi 4 64-bit, Mac Apple Silicon).
#
#   Build nativo (na Pi, no Mac, ou num servidor):
#       docker build .                (ou: docker compose build)
#
#   Cross-build (ex.: montar no Mac/PC p/ transferir à Pi):
#       docker buildx build --platform linux/arm64 -t tiktok-live-monitor .
#       docker save tiktok-live-monitor | ssh pi 'docker load'
#
#   Dica para Pi 4: --build-arg SKIP_TESTS=1 deixa o build mais rápido.

ARG TARGETPLATFORM
FROM --platform=$TARGETPLATFORM golang:1.26-bookworm AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Etapa de testes; pula com --build-arg SKIP_TESTS=1 (recomendado na Pi 4).
# Os testes que tocam o banco exigem TEST_DATABASE_URL (PostgreSQL descartável).
ARG SKIP_TESTS=0
RUN if [ "$SKIP_TESTS" = "1" ]; then echo "SKIP_TESTS=1: pulando testes"; \
    else go test ./internal/... -count=1 -timeout 120s; fi

# Sem CGO desde a migração para PostgreSQL (pgx puro Go): cross-build rápido.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -o /tiktok-live-monitor .

# Stage 2: Minimal runtime (multi-arch). Sem Python/llama/GGUF: apenas o
# backend Go + a bridge Node (tiktok-live-connector) + a UI estática.
FROM --platform=$TARGETPLATFORM node:22-bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=builder /tiktok-live-monitor /app/tiktok-live-monitor
COPY web/ ./web/
COPY internal/monitor/ ./internal/monitor/
COPY package.json package-lock.json ./

RUN mkdir -p data \
    && npm ci --omit=dev

ENV HOST=0.0.0.0
ENV PORT=3001

EXPOSE 3001

HEALTHCHECK --interval=30s --timeout=10s --start-period=30s --retries=5 \
    CMD curl -sf "http://127.0.0.1:${PORT:-3001}/api/readiness" || exit 1

ENTRYPOINT ["/app/tiktok-live-monitor"]
