-- 004_live_sessions_rollback.sql
-- Reverte o SCHEMA da 004 para o formato anterior (pré-live_sessions).
--
-- ⚠️ Quando usar: só se você precisar voltar o binário do backend para a versão
-- ANTERIOR a esta mudança. Depois do deploy, as 9 colunas live_id estão NOT NULL:
-- o binário antigo não cita live_id nos INSERTs, então sem este rollback ele
-- falharia em toda escrita de evento (presentes, mensagens, curtidas...).
--
-- O que este script NÃO faz: apagar dados. As colunas live_id, a tabela
-- live_sessions e as linhas continuam no banco (o binário antigo simplesmente as
-- ignora). Se quiser removê-las depois, é uma limpeza separada e explícita.
--
-- Pré-requisito para o passo 2: o binário antigo usa
-- `INSERT INTO room_like_totals ... ON CONFLICT (live_name)`, que precisa de um
-- índice único em live_name. Com o escopo por sessão a tabela passou a ter uma
-- linha por sessão, então as duplicatas precisam sair antes (passo 1 — mantém a
-- linha de maior total, que é o que a leitura antiga mostrava via MAX(total)).
--
-- Aplicar no SQL Editor do Supabase. A ordem dos passos importa.

-- 1) Remove as linhas duplicadas por live_name (mantém a de maior total).
delete from public.room_like_totals a
 using public.room_like_totals b
 where a.live_name = b.live_name
   and a.live_id <> b.live_id
   and (a.total < b.total or (a.total = b.total and a.live_id > b.live_id));

-- 2) Volta a PK de room_like_totals para live_name.
alter table public.room_like_totals drop constraint if exists room_like_totals_pkey;
alter table public.room_like_totals
    add constraint room_like_totals_pkey primary key (live_name);
drop index if exists public.room_like_totals_live_id_key;

-- 3) Libera a escrita do binário antigo nas 9 tabelas.
alter table public.user_messages        alter column live_id drop not null;
alter table public.gifts                alter column live_id drop not null;
alter table public.shares               alter column live_id drop not null;
alter table public.likes                alter column live_id drop not null;
alter table public.pinned_comments      alter column live_id drop not null;
alter table public.anomaly_logs         alter column live_id drop not null;
alter table public.target_gift_history  alter column live_id drop not null;
alter table public.gift_goals           alter column live_id drop not null;
alter table public.room_like_totals     alter column live_id drop not null;

-- 4) Restaura os índices no formato antigo (dedup e pin globais, não por sessão).
drop index concurrently if exists public.idx_user_messages_dedup;
create index concurrently if not exists idx_user_messages_dedup
    on public.user_messages (lower("uniqueId"), lower(message));

drop index concurrently if exists public.idx_pinned_comments_pin;
create unique index concurrently if not exists idx_pinned_comments_pin
    on public.pinned_comments (live_name, pin_id)
    where pin_id is not null and pin_id <> '';

-- 5) O binário antigo volta a atender POST /api/admin/lives/delete (hoje responde
--    410). Nada a fazer no banco: é código. Confirme que o rollback de binário
--    veio junto, senão o front novo continuará chamando /session/delete (404 no
--    binário antigo) e deixará de apagar qualquer live.
