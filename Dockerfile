# Interface web + IA local (llama-server). Electron não roda aqui — use o navegador na porta 3000.
# Noble (24.04): glibc ≥ 2.38 — os artefatos ubuntu-arm64/ubuntu-x64 do llama.cpp (b8999+) pedem isso.
# Bookworm (Debian 12) tem glibc 2.36 e falha no Raspberry/arm64 com GLIBC_2.38 not found.
FROM node:22-noble-slim

# OpenMP + libs C++; slim não inclui tudo por padrão
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        libgomp1 \
        libstdc++6 \
        libatomic1 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY package*.json ./
COPY scripts/setup-llm.js scripts/setup-llm.js

RUN npm ci --omit=dev --ignore-scripts

COPY server.js ai.js moderation.js index.html renderer.js ./

COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

RUN mkdir -p models bin

ENV HOST=0.0.0.0
ENV PORT=3000

EXPOSE 3000

HEALTHCHECK --interval=30s --timeout=10s --start-period=300s --retries=5 \
    CMD node -e "require('http').get('http://127.0.0.1:'+(process.env.PORT||3000)+'/api/state',r=>process.exit(r.statusCode===200?0:1)).on('error',()=>process.exit(1))"

ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["node", "server.js"]
