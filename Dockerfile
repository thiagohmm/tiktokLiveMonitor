# Interface web + IA local (llama-server). Electron não roda aqui — use o navegador na porta 3000.
FROM node:22-bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
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

HEALTHCHECK --interval=30s --timeout=10s --start-period=180s --retries=5 \
    CMD node -e "require('http').get('http://127.0.0.1:'+(process.env.PORT||3000)+'/api/state',r=>process.exit(r.statusCode===200?0:1)).on('error',()=>process.exit(1))"

ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["node", "server.js"]
