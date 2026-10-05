# AGENTS.md — TikTok Live Monitor

## Preferências do projeto

### Reviews: usar sempre DeepSeek
Toda review de código, plano, segurança ou diff deve ser delegada a um subagente
rodando com o modelo **`deepseek/deepseek-v4-pro`** (ex.: agente `reviewer` com
`model: "deepseek/deepseek-v4-pro"`). Não usar o modelo do session atual para reviews.

## Contexto rápido
- Tudo roda em servidor próprio (VPS) com `docker-compose.production.yml`: `postgres`, `backend`,
  `waha` e `minio`. Frontend e TLS usam o nginx de borda compartilhado (80/443, Let's Encrypt).
  Dev local: `docker-compose.yml` (UI em `FRONTEND_PORT`, padrão 8080). Ver `DEPLOYMENT.md` e `PRODUCAO.md`
- Backend Go (`backend/`, API pura SSE + REST)
- Frontend (`frontend/`, HTML/JS vanilla) — servido pelo nginx de borda, mesma origem da API
  (proxy de `/api/*` e `/events`); todo dado passa pela API Go
- Banco operacional: PostgreSQL 17 do compose (volume `postgres-data`). Backend conecta via
  `DATABASE_URL` com o usuário **`tlm_app`**, dono das tabelas do banco, **sem**
  `SUPERUSER`/`BYPASSRLS`/`CREATEDB`/`CREATEROLE` (não alcança nem o banco de outros produtos).
  `tlm` continua como *bootstrap superuser* do cluster, usado apenas para administração
  (e o PostgreSQL recusa remover `SUPERUSER` dele: é o bootstrap do cluster)
- **Autenticação local no VPS**: usuários, hashes Argon2id, sessões revogáveis e
  recuperação de senha ficam no PostgreSQL. Cookies HttpOnly + CSRF; nenhum
  JWT externo, SDK ou chamada de identidade remota. SMTP/Resend só entrega e-mails.
- Schema: o backend cria/migra as tabelas no boot (`migratePostgres()` em
  `backend/internal/database/postgres.go`). Não há CI de migração
- **Isolamento multi-tenant é feito EXCLUSIVAMENTE na aplicação** (`WHERE org_id`). O RLS está
  habilitado nas 29 tabelas, mas **não há nenhuma política criada** (`pg_policies` vazio) e o dono
  das tabelas ignora RLS com `force=false` — ou seja, **RLS NÃO é fronteira de segurança hoje**.
  Para torná-lo fronteira seria preciso `FORCE ROW LEVEL SECURITY` + política por tabela
  (`USING (org_id = current_setting('app.org_id'))`) + `SET LOCAL app.org_id` em toda transação.
  Ver `docs/security/RELATORIO.md` (achado P1-CRIT) e `docs/security/remediacao.md`
- Migrações locais no boot: `postgres.go` e `local_identity.go`; scripts legados
  foram retirados. Importação de identidades mantém IDs e exige novas senhas.
- Tabelas operacionais (12, incluindo `live_sessions`) e Fila PIX (WhatsApp/WAHA + MinIO: `pix_*`
  + `pix_value_rules`) — ver `docs/fila-pix.md`
- **Multi-tenant** (tenant = organização): cada usuário pertence a UMA org (`organizations` /
  `organization_members`, papéis `owner` | `operator`). Todo dado é isolado por org: eventos via
  `live_sessions.org_id` (constante `orgSessions` em `database/driver.go`), settings em
  `settings['app:<org>']`, Fila PIX via `org_id`. Um monitor por (org, live), com limite por org
  (`organizations.max_lives`) e global (`MAX_MONITORS`). SSE só entrega à org do evento
  (`publishSSE`). Tenant em `internal/tenant`, resolvido por `tenantMiddleware` (`view/tenant.go`).
  Admin da plataforma (role `admin` em `users`) cria orgs; o dono gerencia a equipe
  (`/api/org/members`) e é o único que conecta/desconecta o WhatsApp da Fila PIX.
  Dados anteriores ao multi-tenant sem dono ficam na org de legado
  (`model.DefaultOrgID`), que não aceita membros (só admin da plataforma) e cujas lives o admin
 move para a org de um cliente (`POST /api/admin/lives/assign`, só a partir do legado); identidades e vínculos são importados ou atribuídos explicitamente; reiniciar
  nunca recria uma organização para uma conta revogada.
- Equipes: dono principal + 2 vagas incluídas; outros donos consomem vagas.
  Ajudantes são somente leitura. Convites reservam vagas; extras têm mensalidade
  manual e vencimento. Regras ativadas após regularização de cada organização.
  Só o admin geral cadastra usernames TikTok permitidos. Ver `docs/auth-local.md`.
- Auth fail-closed: sem `DATABASE_URL` o backend não sobe; `AUTH_ENABLED=0` só
  para dev local, recusado quando `SITE_URL` usa HTTPS.
- Vercel e Railway foram desligados; o único deploy é o docker compose no VPS (ver `PRODUCAO.md`)
