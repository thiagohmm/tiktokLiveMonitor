# Frontend estático do TikTok Live Monitor (nginx).
#
#   docker build -t tiktok-live-monitor-frontend .
#   docker run -p 8080:80 -e BACKEND_URL=http://localhost:3001 tiktok-live-monitor-frontend
#
# O nginx serve a UI e faz proxy de /api/* e /events para o backend Go,
# mantendo tudo na mesma origem (sem CORS). Em produção na Vercel, o
# vercel.json faz o mesmo papel (substitua https://SEU_BACKEND).

FROM nginx:1.27-alpine

# BACKEND_URL (padrão: serviço `backend` da rede do compose).
ARG BACKEND_URL=http://backend:3001
ENV BACKEND_URL=${BACKEND_URL}

COPY nginx.conf /etc/nginx/templates/default.conf.template
WORKDIR /usr/share/nginx/html
COPY index.html admin.html login.html config.js auth.js renderer.js admin.js ./
COPY vendor/ ./vendor/

# Mesma origem: a base da API fica vazia (o nginx faz o proxy para o backend).
RUN sed -i 's/"__API_BASE__"/""/' config.js

# O entrypoint do image (20-envsubst-on-startup) processa
# /etc/nginx/templates/*.template com as variáveis de ambiente.

EXPOSE 80

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -q -O /dev/null "http://127.0.0.1/" || exit 1
