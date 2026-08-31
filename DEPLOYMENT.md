# Implantação

## Estrutura

- `backend/` — API Go (SSE em `/events` + REST em `/api/*`). Porta `3001`.
- `frontend/` — UI estática (HTML/JS puro). Servida por nginx (Docker),
  Vercel ou dev server local. O `frontend/config.js` define a base da API.

```
frontend (nginx :80) ──/api/*, /events──▶ backend Go (:3001) ──▶ PostgreSQL
```

## Teste com Docker Compose

1. Copie `.env.example` para `.env`.
2. Troque `POSTGRES_PASSWORD` e use a mesma senha em `DATABASE_URL`.
3. Para um smoke test sem login, use `AUTH_ENABLED=0`.
4. Execute `docker compose up --build -d` (ou `./docker-test.sh`).
5. Abra `http://localhost:${FRONTEND_PORT:-8080}` no navegador.

O compose sobe a aplicação (backend + frontend) e um PostgreSQL persistente.
O backend cria as tabelas operacionais automaticamente. O nginx do frontend
faz proxy de `/api/*` e `/events` para o backend, mantendo tudo na mesma
origem (sem CORS).

## Desenvolvimento local (sem Docker)

Terminal 1 — backend:

```sh
cd backend
export DATABASE_URL=... SUPABASE_JWT_SECRET=... # ou AUTH_ENABLED=0
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

## Ativar login e aprovação de clientes

1. Crie um projeto Supabase.
2. Execute `supabase/migrations/001_profiles.sql` no SQL Editor.
3. Crie o primeiro usuário em Authentication > Users.
4. Execute os dois `UPDATE` do final da migração, trocando o e-mail, para
   promover esse usuário nas claims e no perfil.
5. Preencha no `.env`:
   `SUPABASE_URL`, `SUPABASE_ANON_KEY`, `SUPABASE_JWT_SECRET` e
   `SUPABASE_SERVICE_ROLE_KEY`.
6. Recrie apenas o backend: `docker compose up -d --force-recreate backend`.

Novos clientes se cadastram em `/login.html` (Criar conta) e ficam em
**Aguardando pagamento**. Depois da confirmação do pagamento, o administrador
abre `/admin.html` e usa **Aprovar pagamento**. A conta só passa a entrar no
monitor depois dessa aprovação. Suspensão e validade da assinatura também são
controladas nessa tela. O backend só atende os endpoints `/api/admin/*` para
uma sessão ativa com papel `admin`.

Nunca envie `SUPABASE_SERVICE_ROLE_KEY` ao navegador, à Vercel como variável
pública ou ao repositório. Ela é usada somente pelo backend Go.

## Vercel

O backend mantém conexão longa com o TikTok e SSE, portanto deve continuar em
um servidor/container persistente. A Vercel hospeda os arquivos de
`frontend/` e usa as regras de `frontend/vercel.json` como proxy para a URL
pública HTTPS do backend. Substitua `https://SEU_BACKEND` antes do deploy e
defina `CORS_ALLOWED_ORIGINS` com a URL da Vercel no `.env` do backend.

Na Vercel/Supabase, use a URL do pooler PostgreSQL em `DATABASE_URL`. Não use
o hostname `postgres`/`db`, que existe somente na rede do Docker Compose.
