-- 003_target_gift_priority.sql
-- Fila de presentes alvos com "fura fila".
--
-- is_priority: presente marcado como fura fila (checkbox na UI).
-- priority_at: momento em que foi promovido — define a ordem entre fura fila
--              (FIFO: quem fura primeiro fica na frente).
--
-- Ordenação da fila (usada em GetRecent/GetPendingTargetGiftHistory):
--   ORDER BY is_priority DESC,
--            COALESCE(priority_at, received_at) ASC,
--            received_at ASC,
--            id ASC
--
-- Idempotente; espelha o ALTER em migratePostgres()
-- (backend/internal/database/postgres.go).
--
-- Aplicar no SQL Editor do Supabase (não há CI de migração — PRODUCAO.md).

alter table public.target_gift_history
  add column if not exists is_priority boolean not null default false;

alter table public.target_gift_history
  add column if not exists priority_at timestamptz;

-- Verificação pós-aplicação (esperado: is_priority e priority_at presentes):
-- select column_name, data_type
-- from information_schema.columns
-- where table_schema = 'public'
--   and table_name = 'target_gift_history'
--   and column_name in ('is_priority', 'priority_at');
