-- 002_rls_operational_tables.sql
-- Corrige o alerta crítico "Table publicly accessible" (rls_disabled_in_public).
--
-- As tabelas operacionais são criadas pelo backend em migratePostgres()
-- (backend/internal/database/postgres.go) sem RLS. O backend as acessa via
-- DATABASE_URL (pooler do Supabase = papel postgres, BYPASSRLS) e, para
-- profiles, via service_role. O frontend (anon/authenticated) NÃO consulta
-- estas tabelas diretamente: todo dado passa pela API Go.
--
-- Portanto: habilita RLS e NÃO cria políticas para clientes (default deny).
-- anon/authenticated passam a receber 0 linhas; postgres/service_role
-- continuam funcionando.
--
-- Aplicar no SQL Editor do Supabase (não há CI de migração — PRODUCAO.md).

alter table public.false_positives      enable row level security;
alter table public.anomaly_logs         enable row level security;
alter table public.gifts                enable row level security;
alter table public.shares               enable row level security;
alter table public.likes                enable row level security;
alter table public.room_like_totals     enable row level security;
alter table public.user_messages        enable row level security;
alter table public.target_gift_history  enable row level security;
alter table public.gift_goals           enable row level security;
alter table public.pinned_comments      enable row level security;
alter table public.settings             enable row level security;

-- Hardening: remove os GRANTs padrão de PostgREST de vez.
-- Com RLS + sem política já não há linhas visíveis; revogar também impede
-- qualquer acesso via PostgREST. Seguro porque o backend usa o papel
-- postgres (não anon/authenticated).
revoke all on public.false_positives,
             public.anomaly_logs,
             public.gifts,
             public.shares,
             public.likes,
             public.room_like_totals,
             public.user_messages,
             public.target_gift_history,
             public.gift_goals,
             public.pinned_comments,
             public.settings
  from anon, authenticated;
revoke all on sequence public.false_positives_id_seq,
                      public.anomaly_logs_id_seq,
                      public.gifts_id_seq,
                      public.shares_id_seq,
                      public.likes_id_seq,
                      public.user_messages_id_seq,
                      public.target_gift_history_id_seq,
                      public.gift_goals_id_seq,
                      public.pinned_comments_id_seq
  from anon, authenticated;

-- Verificação pós-aplicação (esperado: relrowsecurity = true em todas):
-- select relname, relrowsecurity
-- from pg_class
-- where relnamespace = 'public'::regnamespace
--   and relname in (
--     'false_positives','anomaly_logs','gifts','shares','likes',
--     'room_like_totals','user_messages','target_gift_history',
--     'gift_goals','pinned_comments','settings'
--   );
