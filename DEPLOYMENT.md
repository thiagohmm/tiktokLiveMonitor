# Implantação

O sistema roda inteiro em Docker Compose: localmente com `docker-compose.yml`
e em produção num servidor próprio (VPS) com `docker-compose.production.yml`.
Vercel e Railway foram desligados: o único deploy é o docker compose no VPS
(ver [PRODUCAO.md](PRODUCAO.md#ambientes-anteriores)).

## Estrutura

- `backend/` — API Go (SSE em `/events` + REST em `/api/*`). Porta `3001`.
- `frontend/` — UI estática (HTML/JS puro). Servida pelo nginx do container
  `frontend` ou pelo dev server local. O `frontend/config.js` define a base da
  API (vazio no build = mesma origem).

```
frontend (nginx :80) ──/api/*, /events──▶ backend Go (:3001) ──▶ PostgreSQL (compose)
                                              ├──▶ WAHA (WhatsApp)  ─┐ Fila PIX
                                              ├──▶ MinIO (comprovantes)┘
                                              └──▶ Supabase Auth + profiles (hospedado)
```

Serviços do compose:

| Serviço | Papel | Porta no host (dev) | Produção |
|---|---|---|---|
| `postgres` | PostgreSQL 17, dados operacionais | não publicada | só rede interna |
| `backend` | API Go + ponte Node | `TLM_BACKEND_PORT` (3001) | só rede interna |
| `frontend` | nginx com a UI, proxy p/ backend | `FRONTEND_PORT` (8080) | só rede interna |
| `waha` | gateway WhatsApp (Fila PIX) | `127.0.0.1:WAHA_PORT` (3210) | só rede interna |
| `minio` | storage S3 dos comprovantes PIX | `127.0.0.1:9000` / `9001` (console) | só rede interna |
| `nginx` | borda HTTPS (Let's Encrypt) | — | `80` e `443` |
| `certbot` | emissão/renovação (profile `certificates`) | — | sob demanda |

## Teste com Docker Compose

1. Copie `.env.example` para `.env`.
2. Troque `POSTGRES_PASSWORD` e use a mesma senha em `DATABASE_URL`.
3. Para um smoke test sem login, use `AUTH_ENABLED=0`.
4. Execute `docker compose up --build -d` (ou `./docker-test.sh`).
5. Abra `http://localhost:${FRONTEND_PORT:-8080}` no navegador.

O compose sobe a aplicação (backend + frontend), um PostgreSQL persistente
(volume `postgres-data`), o WAHA e o MinIO. O backend cria e migra as tabelas
automaticamente no boot. O nginx do frontend faz proxy de `/api/*` e `/events`
para o backend, mantendo tudo na mesma origem (sem CORS).

Para o Raspberry Pi existe `docker-compose.raspberry.yml` (só API + Postgres;
o frontend roda à parte).

## Desenvolvimento local (sem Docker)

Terminal 1 — backend:

```sh
cd backend
export DATABASE_URL=... SUPABASE_URL=... SUPABASE_ANON_KEY=... # ou AUTH_ENABLED=0
go run .
```

Terminal 2 — frontend (aponta para o backend em outra origem):

```sh
cd frontend
TLM_API_BASE=http://localhost:3001 npm run dev
# http://localhost:3000
```

Outra origem exige CORS liberado no backend:
`CORS_ALLOWED_ORIGINS=http://localhost:3000`.

## Banco e migrações

O banco operacional é o PostgreSQL do compose. Não há CI de migração nem passo
manual no deploy: ao iniciar, o backend executa `migratePostgres()`
(`backend/internal/database/postgres.go`), que cria as tabelas com
`CREATE TABLE IF NOT EXISTS`, adiciona colunas com `ADD COLUMN IF NOT EXISTS`,
habilita RLS e faz o backfill de `live_sessions`. Basta subir a nova imagem do
backend.

Os arquivos em `supabase/migrations/` são o histórico do tempo em que o banco
estava no Supabase. Eles citam `auth.users`, `auth.uid()` e os papéis
`anon`/`authenticated`, que não existem num PostgreSQL puro:

- `001_profiles.sql` pertence ao projeto Supabase (Auth + `profiles`) e não
  deve ser aplicado no Postgres do compose.
- `002`–`006` espelham o que o boot já faz; os `REVOKE ... from anon,
  authenticated` de `002`, `004`, `005` e `006` falham no Postgres do compose
  (papéis inexistentes). Não é preciso aplicá-los.
- `004_live_sessions_rollback.sql` só é necessário ao voltar para um backend
  anterior à 004 (ver [PRODUCAO.md](PRODUCAO.md#schema-e-rollback)).

Quando for preciso rodar SQL manualmente no Postgres do compose:

```sh
docker compose exec -T postgres psql -U tlm -d tiktok_live_monitor < arquivo.sql
# produção: docker compose -f docker-compose.production.yml exec -T postgres ...
```

## Ativar login e aprovação de clientes

A autenticação continua no **Supabase Auth hospedado**: o backend faz login,
cadastro, recuperação de senha e administração de usuários pela API do Supabase
(`/auth/v1/*`) e lê/grava a tabela `public.profiles` via REST (`/rest/v1/profiles`)
com a service role. Apenas os dados operacionais saíram do Supabase.

Para configurar um projeto Supabase novo:

1. Crie um projeto Supabase.
2. Execute `supabase/migrations/001_profiles.sql` no SQL Editor **do Supabase**
   (não no Postgres do compose).
3. Crie o primeiro usuário em Authentication > Users.
4. Execute os dois `UPDATE` do final da migração, trocando o e-mail, para
   promover esse usuário nas claims e no perfil.
5. Preencha no `.env`:
   `SUPABASE_URL`, `SUPABASE_ANON_KEY`, `SUPABASE_SERVICE_ROLE_KEY` e
   `SITE_URL` (origem pública do frontend, ex.: `https://livemonitortk.com.br`;
   usada no `redirect_to` do link de redefinição de senha — sem ela o
   `POST /api/auth/recover` responde 503). Em produção o compose deriva
   `SITE_URL` de `DOMAIN`.
   Sem `SUPABASE_URL` ou `SUPABASE_ANON_KEY` o login fica desligado.
   `SUPABASE_JWT_SECRET` é opcional: quando vazio, o backend valida os tokens
   pela API Auth do Supabase; quando preenchido, usa validação HS256 local.
6. Para a redefinição de senha ("Esqueci minha senha?"): em Authentication >
   URL Configuration > Redirect URLs, adicione
   `https://SEU_DOMINIO/reset-password.html` (e as origens de localhost que
   você usar). Sem essa allowlist, o link do e-mail não redireciona de volta
   para o app.
7. Recrie apenas o backend: `docker compose up -d --force-recreate backend`.

Novos clientes se cadastram em `/login.html` (Criar conta) e ficam em
**Aguardando pagamento**. Depois da confirmação do pagamento, o administrador
abre `/admin.html` e usa **Aprovar pagamento**. A conta só passa a entrar no
monitor depois dessa aprovação. Suspensão e validade da assinatura também são
controladas nessa tela. O backend só atende os endpoints `/api/admin/*` para
uma sessão ativa com papel `admin`.

Quem esqueceu a senha usa o link "Esqueci minha senha?" em `/login.html`:
o backend gera o link de recuperação via Supabase (`generate_link`), envia por
e-mail (Resend/SMTP) e aplica a nova senha em `PUT /auth/v1/user`.
O e-mail é sempre o mesmo para e-mail cadastrado ou não (anti-enumeração).

Nunca envie `SUPABASE_SERVICE_ROLE_KEY` ao navegador ou ao repositório.
Ela é usada somente pelo backend Go.

## Produção no VPS

O ambiente principal usa `docker-compose.production.yml` no servidor
143.95.162.200, porta SSH 22022, em /opt/tiktok-live-monitor.
Consulte [PRODUCAO.md](PRODUCAO.md) para deploy, backups, HTTPS e rollback.

Rotas: /promo é a página comercial; /login é o acesso à ferramenta; / é o painel.
O PostgreSQL operacional roda no compose do VPS. Auth e profiles continuam no Supabase.

Primeira instalação: copiar .env.production.example para .env, preencher
credenciais, apontar o domínio e www para o VPS e executar:

```sh
docker compose -f docker-compose.production.yml up -d --build
CERTBOT_EMAIL=seu-email@example.com ./deploy/enable-https.sh
```

Para atualizações, preservar o .env e deploy/nginx/default.conf remoto. O
default.conf do Git espelha o de produção, que também serve Sigmenta e agenttk:
o deploy não pode recriar o nginx de borda nem usar `down` (ver
[PRODUCAO.md — VPS compartilhado](PRODUCAO.md#vps-compartilhado)).
deploy/enable-https.sh só altera os blocos do DOMAIN, faz backup e restaura se
`nginx -t` falhar. Onde o HTTPS já existe, apenas renova (se necessário) e
recarrega. Ver [PRODUCAO.md — HTTPS e renovação](PRODUCAO.md#https-e-renovação).
A renovação periódica chama deploy/renew-https.sh pelo cron documentado.

## Vercel e Railway (desligados)

Vercel e Railway foram desligados. `frontend/vercel.json` e
`backend/.railway/railway.ts` saíram do repositório; não há deploy fora do VPS.
Detalhes em PRODUCAO.md.
