-- 004_live_sessions.sql
-- Corrige o delete da administração: antes, "Deletar" uma linha da tabela de
-- lives apagava TODAS as lives do streamer, porque live_name é o username do
-- streamer (não identifica uma sessão) e DeleteLive não filtrava por dia.
--
-- A partir daqui toda live conectada tem um id próprio (live_sessions.id) e o
-- delete da administração apaga somente as linhas daquele id.
--
-- ESCOPO DESTE ARQUIVO: apenas o que é rápido e não bloqueia escrita —
-- a tabela nova, as 9 colunas e o hardening de acesso. Backfill, índices,
-- SET NOT NULL e a troca da PK de room_like_totals rodam em migratePostgres()
-- (backend/internal/database/postgres.go), no boot do backend, com estratégias
-- próprias para banco em produção:
--   • CREATE INDEX CONCURRENTLY (não bloqueia escrita durante o scan);
--   • backfill em lotes de linhas, uma transação por lote;
--   • SET NOT NULL via CHECK ... NOT VALID + VALIDATE (sem varredura sob lock);
--   • PK anexada a um índice único concorrente (ADD CONSTRAINT ... USING INDEX);
--   • lock_timeout de 30s para falhar rápido em vez de enfileirar escritas.
--
-- Por isso este arquivo NÃO cria índices com live_id: fazê-lo aqui rodaria um
-- scan bloqueando escrita na produção. Idempotente — pode ser reaplicado.
--
-- Aplicar no SQL Editor do Supabase (não há CI de migração — PRODUCAO.md).

-- Sessões de live: uma linha por conexão de monitor.
create table if not exists public.live_sessions (
    id           text primary key,
    live_name    text not null,
    day          date not null,
    started_at   timestamptz not null default current_timestamp,
    last_seen_at timestamptz not null default current_timestamp,
    ended_at     timestamptz
);

-- Tabela nova e pequena: o índice pode ser criado aqui sem risco de lock longo.
create index if not exists idx_live_sessions_name_day
    on public.live_sessions (live_name, day desc);

-- Coluna live_id nas tabelas operacionais que guardam live_name.
-- ADD COLUMN sem default é operação de catálogo: não reescreve a tabela.
alter table public.user_messages        add column if not exists live_id text;
alter table public.gifts                add column if not exists live_id text;
alter table public.shares               add column if not exists live_id text;
alter table public.likes                add column if not exists live_id text;
alter table public.pinned_comments      add column if not exists live_id text;
alter table public.anomaly_logs         add column if not exists live_id text;
alter table public.target_gift_history  add column if not exists live_id text;
alter table public.gift_goals           add column if not exists live_id text;
alter table public.room_like_totals     add column if not exists live_id text;

-- RLS default deny + revogação dos grants do PostgREST, espelhando
-- 002_rls_operational_tables.sql. Os papéis anon/authenticated existem no
-- Supabase; por isso o REVOKE fica só aqui (migratePostgres só habilita RLS,
-- porque bancos de teste descartáveis não têm esses papéis).
alter table public.live_sessions enable row level security;

revoke all on public.live_sessions from anon, authenticated;

-- Conferência após o backend subir (deve retornar 0 em todas as 9):
--   select count(*) from public.gifts where live_id is null;
--   select count(*) from public.room_like_totals where live_id is null;
-- E a PK de room_like_totals deve ser live_id:
--   select kcu.column_name from information_schema.table_constraints tc
--     join information_schema.key_column_usage kcu
--       on kcu.constraint_name = tc.constraint_name
--      and kcu.table_schema = tc.table_schema
--    where tc.table_schema = 'public' and tc.table_name = 'room_like_totals'
--      and tc.constraint_type = 'PRIMARY KEY';
