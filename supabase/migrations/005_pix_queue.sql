-- 005_pix_queue.sql
-- Fila PIX (WhatsApp/WAHA): sessões por usuário, contatos, tickets e mensagens.
--
-- As tabelas são criadas em migratePostgres() (backend/internal/database/postgres.go)
-- no boot do backend. Este arquivo espelha o schema e aplica o hardening que só
-- existe no Supabase: RLS default deny + REVOKE dos grants do PostgREST.
--
-- O frontend nunca consulta estas tabelas diretamente: todo dado passa pela API Go.
-- O backend conecta com o papel postgres (BYPASSRLS), então continua funcionando.
--
-- org_id é TEXT (UUID do Supabase Auth) sem FK para auth.users, para que
-- o Postgres local do docker-compose também aceite o schema.
--
-- Aplicar no SQL Editor do Supabase (não há CI de migração — PRODUCAO.md).

create table if not exists public.pix_whatsapp_sessions (
    id            bigserial primary key,
    org_id text not null unique,
    session_name  text not null unique,
    status        text not null default 'disconnected',
    me_phone      text not null default '',
    me_jid        text not null default '',
    connected_at  timestamptz,
    created_at    timestamptz not null default current_timestamp,
    updated_at    timestamptz not null default current_timestamp
);

create table if not exists public.pix_contacts (
    id               bigserial primary key,
    org_id    text not null,
    phone_e164       text not null default '',
    whatsapp_jid     text not null,
    push_name        text not null default '',
    first_contact_at timestamptz not null,
    last_contact_at  timestamptz not null,
    unique (org_id, whatsapp_jid)
);

create table if not exists public.pix_tickets (
    id                 bigserial primary key,
    org_id      text not null,
    contact_id         bigint not null references public.pix_contacts(id) on delete cascade,
    status             text not null default 'pending',
    has_receipt        boolean not null default false,
    auto_reply_sent_at timestamptz,
    received_at        timestamptz not null,
    last_message_at    timestamptz not null,
    answered_at        timestamptz,
    answered_by        text not null default ''
);

create index if not exists idx_pix_tickets_owner_status_received
    on public.pix_tickets (org_id, status, received_at asc, id asc);

create unique index if not exists idx_pix_tickets_one_pending_per_contact
    on public.pix_tickets (contact_id) where status = 'pending';

create table if not exists public.pix_messages (
    id                  bigserial primary key,
    org_id       text not null,
    ticket_id           bigint not null references public.pix_tickets(id) on delete cascade,
    contact_id          bigint not null references public.pix_contacts(id) on delete cascade,
    whatsapp_message_id text not null,
    direction           text not null,
    type                text not null,
    body                text not null default '',
    media_path          text not null default '',
    media_mime          text not null default '',
    media_filename      text not null default '',
    media_deleted_at    timestamptz,
    media_delete_reason text not null default '',
    created_at          timestamptz not null default current_timestamp,
    unique (org_id, whatsapp_message_id)
);

create index if not exists idx_pix_messages_ticket_created
    on public.pix_messages (ticket_id, created_at asc, id asc);

create index if not exists idx_pix_messages_media_active
    on public.pix_messages (org_id, id)
    where media_path != '' and media_deleted_at is null;

-- RLS default deny + revogação dos grants do PostgREST, espelhando
-- 002_rls_operational_tables.sql.
alter table public.pix_whatsapp_sessions enable row level security;
alter table public.pix_contacts          enable row level security;
alter table public.pix_tickets           enable row level security;
alter table public.pix_messages          enable row level security;

revoke all on public.pix_whatsapp_sessions,
              public.pix_contacts,
              public.pix_tickets,
              public.pix_messages
  from anon, authenticated;

revoke all on sequence public.pix_whatsapp_sessions_id_seq,
                      public.pix_contacts_id_seq,
                      public.pix_tickets_id_seq,
                      public.pix_messages_id_seq
  from anon, authenticated;

-- Verificação pós-aplicação (esperado: relrowsecurity = true nas 4):
-- select relname, relrowsecurity from pg_class
-- where relnamespace = 'public'::regnamespace
--   and relname in ('pix_whatsapp_sessions','pix_contacts','pix_tickets','pix_messages');
