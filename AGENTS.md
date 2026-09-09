# AGENTS.md — TikTok Live Monitor

## Preferências do projeto

### Reviews: usar sempre DeepSeek
Toda review de código, plano, segurança ou diff deve ser delegada a um subagente
rodando com o modelo **`deepseek/deepseek-v4-pro`** (ex.: agente `reviewer` com
`model: "deepseek/deepseek-v4-pro"`). Não usar o modelo do session atual para reviews.

## Contexto rápido
- Backend Go (`backend/`, deploy Railway) — API pura (SSE + REST), auth via Supabase Auth
- Frontend (`frontend/`, deploy Vercel) — nunca acessa o PostgREST diretamente; todo dado passa pela API Go
- Banco: Supabase (Postgres + Auth). Backend conecta via `DATABASE_URL` (pooler = papel `postgres`, BYPASSRLS)
- Migrações SQL em `supabase/migrations/` (aplicadas manualmente no SQL Editor — não há CI de migração)
- Tabelas operacionais (11) têm RLS habilitado sem políticas de cliente (default deny) — ver `supabase/migrations/002_rls_operational_tables.sql`
