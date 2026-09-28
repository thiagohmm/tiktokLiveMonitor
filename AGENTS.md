# AGENTS.md — TikTok Live Monitor

## Preferências do projeto

### Reviews: usar sempre DeepSeek
Toda review de código, plano, segurança ou diff deve ser delegada a um subagente
rodando com o modelo **`deepseek/deepseek-v4-pro`** (ex.: agente `reviewer` com
`model: "deepseek/deepseek-v4-pro"`). Não usar o modelo do session atual para reviews.

## Contexto rápido
- Tudo roda em servidor próprio (VPS) com `docker-compose.production.yml`: `postgres`, `backend`,
  `frontend` (nginx interno), `nginx` de borda (80/443, Let's Encrypt), `waha` e `minio`.
  Dev local: `docker-compose.yml` (UI em `FRONTEND_PORT`, padrão 8080). Ver `DEPLOYMENT.md` e `PRODUCAO.md`
- Backend Go (`backend/`, API pura SSE + REST)
- Frontend (`frontend/`, HTML/JS vanilla) — servido pelo nginx do compose, mesma origem da API
  (proxy de `/api/*` e `/events`); todo dado passa pela API Go
- Banco operacional: PostgreSQL 17 do compose (volume `postgres-data`). Backend conecta via
  `DATABASE_URL` (derivada de `POSTGRES_*`) com o usuário dono/superusuário `tlm`, que ignora RLS
- **Auth ainda é Supabase hospedado**: login, usuários, claims e a tabela `public.profiles`
  continuam no projeto Supabase (backend usa `SUPABASE_URL`, `SUPABASE_ANON_KEY`,
  `SUPABASE_SERVICE_ROLE_KEY`). Não excluir o projeto Supabase
- Schema: o backend cria/migra as tabelas no boot (`migratePostgres()` em
  `backend/internal/database/postgres.go`), inclusive RLS default deny (exceto `pix_value_rules`,
  que só tem RLS em `006_pix_value_rules.sql`). Não há CI de migração
- `supabase/migrations/*.sql` são o histórico do Supabase: citam `auth.users`, `anon`/`authenticated`
  e não devem ser aplicados às cegas no Postgres do compose (`001_profiles.sql` é só do Supabase)
- Tabelas operacionais (12, incluindo `live_sessions`) e Fila PIX (WhatsApp/WAHA + MinIO: `pix_*`
  + `pix_value_rules`) — ver `docs/fila-pix.md`
- **Multi-tenant** (tenant = organização): cada usuário pertence a UMA org (`organizations` /
  `organization_members`, papéis `owner` | `operator`). Todo dado é isolado por org: eventos via
  `live_sessions.org_id` (constante `orgSessions` em `database/driver.go`), settings em
  `settings['app:<org>']`, Fila PIX via `org_id`. Um monitor por (org, live), com limite por org
  (`organizations.max_lives`) e global (`MAX_MONITORS`). SSE só entrega à org do evento
  (`publishSSE`). Tenant em `internal/tenant`, resolvido por `tenantMiddleware` (`view/tenant.go`).
  Admin da plataforma (role `admin` no Supabase) cria orgs; o dono gerencia a equipe
  (`/api/org/members`) e é o único que conecta/desconecta o WhatsApp da Fila PIX.
  Dados anteriores ao multi-tenant sem dono ficam na org de legado
  (`model.DefaultOrgID`), que não aceita membros (só admin da plataforma) e cujas lives o admin
 move para a org de um cliente (`POST /api/admin/lives/assign`, só a partir do legado); cada conta
  existente ganha a própria org no primeiro boot (`view/org_bootstrap.go`). Esquema: `supabase/migrations/007_organizations.sql`
- Auth fail-closed: sem `SUPABASE_URL`/`SUPABASE_ANON_KEY` o backend não sobe, a menos que
  `AUTH_ENABLED=0` (só dev local)
- Vercel e Railway foram desligados; o único deploy é o docker compose no VPS (ver `PRODUCAO.md`)
