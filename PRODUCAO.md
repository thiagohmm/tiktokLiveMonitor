# Produção — TikTok Live Monitor

Ambiente migrado em 15/09/2026. Nginx de borda compartilhado desde 03/10/2026.
Desenvolvimento local: [DEPLOYMENT.md](DEPLOYMENT.md).

## Endereços

| Uso | Endereço |
|---|---|
| Página comercial | https://livemonitortk.com.br/promo |
| Login da ferramenta | https://livemonitortk.com.br/login |
| Painel (exige sessão) | https://livemonitortk.com.br/ |
| Administração | https://livemonitortk.com.br/admin |
| Recuperação de senha | https://livemonitortk.com.br/reset-password.html |
| Readiness | https://livemonitortk.com.br/api/readiness |
| SSH | ssh -p 22022 root@143.95.162.200 |
| Diretório remoto | /opt/tiktok-live-monitor |
| Nginx de borda | /opt/vps-edge-nginx (repo `thiagohmm/vps-edge-nginx`) |
| Branch | lite-sem-ia |

/login.html, /admin.html e /index.html continuam válidos. A raiz abre o painel;
a apresentação comercial fica em /promo. O registro A aponta para 143.95.162.200;
www é CNAME do domínio principal.

## Arquitetura

```text
Navegador HTTPS → vps-edge-nginx :443
  ├─ estáticos em /opt/vps-edge-nginx/static/livemonitortk
  └─ /api/* e /events → backend Go :3001
       → PostgreSQL Docker :5432 (dados operacionais + DB Sigmenta)
       → autenticação local (users, auth_sessions, auth_action_tokens no PostgreSQL)
       → WAHA / MinIO (Fila PIX, opcional via COMPOSE_PROFILES)
```

O mesmo nginx de borda atende `sigmenta.com.br` (Prontuário) e `/agenttk/`
(AgentTK). Configuração versionada em `edge-nginx/` (git submodule) e no
clone canônico `/opt/vps-edge-nginx`.

`docker-compose.production.yml` gerencia postgres, backend e (por profile)
WAHA/MinIO. Não publica portas no host. O Postgres também entra na rede
`sigmenta_data` com alias `db-primary` para a API do Sigmenta.

Usuários, autenticação e dados operacionais passam a residir no VPS. A primeira
publicação exige a [migração de identidades](docs/auth-local.md). Preservar o
backup e o projeto antigo durante a validação, sem fallback de autenticação.

## Configuração e persistência

O .env remoto possui permissão 600 e não é versionado. O modelo é
.env.production.example; não copiá-lo sobre um ambiente existente.

- DATABASE_URL aponta para host postgres, banco tiktok_live_monitor, usuário tlm.
- DOMAIN=livemonitortk.com.br. O Compose define SITE_URL e CORS nessa origem HTTPS.
- Fila PIX: `COMPOSE_PROFILES=pix` (WAHA) e MinIO externo via
  `deploy/compose.minio-externo.yml` ou profile `pix-minio`.

Volumes: `tiktok-live-monitor_postgres-data`, `tiktok-live-monitor_waha_sessions`.
Certificados Let's Encrypt permanecem nos volumes
`tiktok-live-monitor_letsencrypt` e `tiktok-live-monitor_certbot-www`,
montados pelo edge. Não executar `docker compose down -v` em produção.

## HTTPS e renovação

Cron do root:

```cron
17 3 * * * /opt/vps-edge-nginx/scripts/renew-https.sh >> /var/log/vps-edge-nginx-renew.log 2>&1
```

`deploy/renew-https.sh` delega ao script do edge.

## Publicar uma versão

1. Backup do Postgres (ver seção Backups).
2. Enviar arquivos do commit preservando `.env` e `backups/`:

```sh
git archive HEAD | ssh -p 22022 root@143.95.162.200 \
  'tar -xf - -C /opt/tiktok-live-monitor'
```

3. No VPS:

```sh
cd /opt/tiktok-live-monitor
docker compose -f docker-compose.production.yml config --quiet
docker compose -f docker-compose.production.yml up -d --build --wait
./deploy/publish-frontend.sh
docker compose -f docker-compose.production.yml ps
```

4. Verificar /promo, /login e /api/readiness.

## Submodule do nginx

```sh
git submodule update --init edge-nginx
```

## Backups

```sh
cd /opt/tiktok-live-monitor
umask 077
mkdir -p backups
docker compose -f docker-compose.production.yml exec -T postgres \
  pg_dump -U tlm -d tiktok_live_monitor -Fc \
  > "backups/predeploy-$(date -u +%Y%m%dT%H%M%SZ).dump"
```

## Ambientes anteriores

- Vercel/Railway são legados; ver histórico em commits anteriores.
- Antes do primeiro deploy local: exportar/importar identidades, validar o admin
  e preparar os links de definição de senha. Nunca voltar automaticamente à
  autenticação antiga após usuários começarem a alterar dados.
