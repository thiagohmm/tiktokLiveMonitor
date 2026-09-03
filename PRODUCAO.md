# Produção — onde o app está e como é publicado

Documento de referência sobre os ambientes de publicação do TikTok Live
Monitor e o fluxo de deploy de cada componente. Para subir localmente ou
entender o Docker Compose, veja `DEPLOYMENT.md`.

## Componentes

- `frontend/` — UI estática (HTML/JS puro). Empacotada com `npm run build`
  (`build.mjs`) para `frontend/dist/`.
- `backend/` — API Go: REST em `/api/*` e SSE em `/events` (porta `3001`).
- Supabase — PostgreSQL (dados) + Auth (login de clientes e admin).

## URLs de produção (verificadas em 03/09/2026)

| Camada | Onde está | URL |
|---|---|---|
| Frontend (produção) | Vercel | https://tiktok-live-monitor-two.vercel.app |
| Backend API (produção) | Railway | https://backend-production-9986.up.railway.app |
| Banco de dados + login | Supabase | https://vcbvctmhwnurdnfssjfj.supabase.co |
| Código-fonte | GitHub | https://github.com/thiagohmm/tiktokLiveMonitor (branch ativa: `lite-sem-ia`) |

> Atenção: https://tiktok-live-monitor.vercel.app é um app antigo (prototype
> com WebSocket local) e **não** é a publicação atual. A publicação atual é
> o domínio com sufixo `-two`.

## Arquitetura de rede

```
Navegador
   │  https://tiktok-live-monitor-two.vercel.app
   ▼
Vercel — arquivos estáticos de frontend/dist/ (index.html, login.html,
         admin.html, JS, vendor)
   │  rewrites do vercel.json: /api/:path* e /events
   ▼
Railway — serviço "backend" (API Go, SSE + REST)
   │  https://backend-production-9986.up.railway.app
   ▼
Supabase — PostgreSQL (pooler) + Auth
```

O frontend roda em produção com `TLM_API_BASE` vazio (same-origin): todas as
chamadas `/api/*` e `/events` vão para a própria origem e o `vercel.json` faz
proxy para o backend no Railway. Por isso o frontend não precisa de CORS.

## Como cada componente é publicado

### 1. Backend → Railway

- Serviço/projeto no Railway: `tiktok-live-monitor` / serviço `backend`.
- Deploy conectado ao GitHub, definido em `backend/.railway/railway.ts`:
  - repositório `thiagohmm/tiktokLiveMonitor`;
  - branch `lite-sem-ia`;
  - root directory `/backend`;
  - healthcheck `GET /api/readiness` (timeout 300s) para o Railway saber que
    o serviço subiu.
- **Deploy automático**: cada `git push` no branch `lite-sem-ia` publica uma
  nova versão do backend.
- Variáveis de ambiente ficam **no painel do Railway** (não no repositório):
  `DATABASE_URL` (pooler do Supabase), `SUPABASE_URL`, `SUPABASE_ANON_KEY`,
  `SUPABASE_SERVICE_ROLE_KEY`, `AUTH_ENABLED=1`,
  `CORS_ALLOWED_ORIGINS=https://tiktok-live-monitor-two.vercel.app` etc. No
  `railway.ts` essas variáveis usam `preserve()` para o deploy não sobrescrevê-las.
- A API escuta na porta fornecida pelo Railway (variável `PORT`, ver
  `backend/main.go`).

### 2. Frontend → Vercel

- Projeto na Vercel: `tiktok-live-monitor`, domínio de produção
  `tiktok-live-monitor-two.vercel.app`.
- Configuração do projeto: root directory `frontend`, build
  `npm run build` (gera `dist/`), output directory `dist`.
- Configuração de rotas/headers em `frontend/vercel.json`:
  - `/api/:path*` → `https://backend-production-9986.up.railway.app/api/:path*`;
  - `/events` → `https://backend-production-9986.up.railway.app/events`;
  - header `X-Frame-Options: DENY` em todas as respostas.
- **Deploy de produção é manual via CLI**:
  ```sh
  cd frontend
  npx vercel --prod
  ```
  O deploy de produção atual foi feito por CLI. Pushs em `lite-sem-ia`
  geram apenas *preview deployments* (URLs `*-thiagohmm-8924.vercel.app`,
  protegidas por login da Vercel), **não** publicam produção.
- O projeto Vercel está linkado ao GitHub com `productionBranch: main`, mas o
  branch `main` está desatualizado; por isso a produção não é atualizada
  automaticamente por push. Se quiser deploy automático de produção, aponte o
  `productionBranch` para `lite-sem-ia` (ou publique o frontend por `main`).

### 3. Banco + login → Supabase

- Projeto `https://vcbvctmhwnurdnfssjfj.supabase.co` hospeda o PostgreSQL
  (acessado pelo backend via pooler em `DATABASE_URL`) e o Auth (e-mail/senha).
- A migração `supabase/migrations/001_profiles.sql` foi aplicada manualmente
  no SQL Editor (não há CI de migração — mudanças no banco são manuais).
- O backend valida os tokens consultando a API Auth do Supabase
  (`SUPABASE_JWT_SECRET` vazio). A `SUPABASE_SERVICE_ROLE_KEY` é usada
  somente pelo backend (admin); nunca vai para o navegador/Vercel.

## Ciclo de publicação de uma nova versão

1. `git push origin lite-sem-ia`
   - Railway publica o backend automaticamente (healthcheck em
     `/api/readiness`);
   - Vercel cria um preview para testar.
2. Teste no preview (URL `*-thiagohmm-8924.vercel.app`).
3. Publica o frontend em produção:
   ```sh
   cd frontend
   npx vercel --prod
   ```
4. Migrações/alterações no Supabase: aplicar manualmente no SQL Editor quando
   existirem.

## Outros modos de execução (não são produção)

- **Docker Compose local** (`docker-compose.yml` + `./docker-test.sh`):
  backend + frontend (nginx) + PostgreSQL local, para teste na própria
  máquina.
- **Raspberry Pi** (`docker-compose.raspberry.yml`): auto-hospedagem para
  rodar o monitor em hardware próprio.
