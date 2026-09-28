-- 006_pix_value_rules.sql
-- Filtro de valores na Fila PIX: valores PIX aceitos por dono + valor extraído
-- de cada comprovante.
--
-- As tabelas também são criadas em migratePostgres()
-- (backend/internal/database/postgres.go) no boot do backend. Este arquivo
-- espelha o schema e aplica o hardening que só existe no Supabase: RLS default
-- deny + REVOKE dos grants do PostgREST.
--
-- O frontend nunca consulta estas tabelas diretamente: todo dado passa pela API
-- Go (GET/PUT /api/pix/values). O backend conecta com o papel postgres
-- (BYPASSRLS), então continua funcionando.
--
-- Aplicar no SQL Editor do Supabase (não há CI de migração — PRODUCAO.md).

create table if not exists public.pix_value_rules (
    id            bigserial primary key,
    org_id text not null,
    value_cents   bigint not null check (value_cents > 0),
    created_at    timestamptz not null default current_timestamp,
    unique (org_id, value_cents)
);

-- Valor monetário extraído do comprovante, em cents. -1 = sem extração
-- (mensagem de texto ou comprovante aceito com o filtro de valores desligado).
alter table public.pix_messages
    add column if not exists media_value_cents bigint not null default -1;

-- RLS default deny + revogação dos grants do PostgREST, espelhando
-- 005_pix_queue.sql.
alter table public.pix_value_rules enable row level security;

revoke all on public.pix_value_rules from anon, authenticated;
revoke all on sequence public.pix_value_rules_id_seq from anon, authenticated;

-- Verificação pós-aplicação (esperado: relrowsecurity = true em pix_value_rules):
-- select relname, relrowsecurity from pg_class
-- where relnamespace = 'public'::regnamespace
--   and relname = 'pix_value_rules';
