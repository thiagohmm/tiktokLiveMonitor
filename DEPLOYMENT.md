# Deploy e ambientes

## Desenvolvimento local

Stack local: `docker compose up --build` (ver `docker-compose.yml`).
Frontend em `frontend/` (HTML/JS); backend Go em `backend/`.

Nunca envie `SUPABASE_SERVICE_ROLE_KEY` ao navegador, à Vercel como variável
pública ou ao repositório. Ela é usada somente pelo backend Go.

## Produção no VPS

O ambiente principal usa `docker-compose.production.yml` (postgres + backend,
WAHA/MinIO opcionais) no servidor 143.95.162.200, porta SSH 22022, em
`/opt/tiktok-live-monitor`.

TLS e páginas estáticas ficam no nginx de borda compartilhado
`/opt/vps-edge-nginx` (submodule `edge-nginx/`, repo `thiagohmm/vps-edge-nginx`).
Consulte [PRODUCAO.md](PRODUCAO.md) para deploy, backups, HTTPS e rollback.

Rotas: /promo é a página comercial; /login é o acesso à ferramenta; / é o painel.
Auth e profiles continuam no Supabase.

```sh
git submodule update --init edge-nginx
docker compose -f docker-compose.production.yml up -d --build
./deploy/publish-frontend.sh
```

Para atualizações, preservar o `.env` remoto e republicar estáticos com
`./deploy/publish-frontend.sh`. A renovação periódica chama
`/opt/vps-edge-nginx/scripts/renew-https.sh` (cron do root).

O Postgres de produção também serve o database `prontuario` do Sigmenta via
alias `db-primary` na rede `sigmenta_data` — não remova essa rede/alias.

## Vercel e Railway (legados)

O endereço antigo da Vercel redireciona a raiz para o login no VPS.
Outras rotas ainda têm rewrites antigos para Railway. A configuração e os
cuidados com deploys automáticos legados estão em PRODUCAO.md.
