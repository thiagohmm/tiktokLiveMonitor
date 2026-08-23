# Multi-stage build: Go backend
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

# Etapa de testes; pula com --build-arg SKIP_TESTS=1 (recomendado na Pi 4)
ARG SKIP_TESTS=0
RUN if [ "$SKIP_TESTS" = "1" ]; then echo "SKIP_TESTS=1: pulando testes"; \
    else CGO_ENABLED=1 go test ./internal/... -count=1 -timeout 60s; fi

# CGO (go-sqlite3) exige toolchain nativa da arquitetura alvo:
# build nativo = rápida; via buildx/QEMU = mais lenta mas funcional.
RUN CGO_ENABLED=1 GOOS=linux GOARCH=$TARGETARCH go build -o /tiktok-live-monitor .

# Stage 2: Minimal runtime (multi-arch)
FROM --platform=$TARGETPLATFORM debian:trixie-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        libgomp1 \
        libstdc++6 \
        libatomic1 \
        curl \
        python3 \
        python3-pip \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=builder /tiktok-live-monitor /app/tiktok-live-monitor
COPY web/ ./web/
COPY model-config.json ./
COPY agent/ ./agent/
COPY requirements.txt ./

RUN mkdir -p models bin \
    && pip3 install --break-system-packages --no-cache-dir -r requirements.txt

ENV HOST=0.0.0.0
ENV PORT=3000

EXPOSE 3000

HEALTHCHECK --interval=30s --timeout=10s --start-period=30s --retries=5 \
    CMD curl -sf "http://127.0.0.1:${PORT:-3000}/api/state" || exit 1

ENTRYPOINT ["/app/tiktok-live-monitor"]