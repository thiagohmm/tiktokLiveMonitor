-- 007_organizations.sql
-- Multi-tenant: cada usuário pertence a UMA organização e só enxerga os dados
-- dela (lives, eventos, configurações, metas e Fila PIX).
--
-- O banco operacional (Postgres do compose) é migrado pelo próprio backend no
-- boot (migratePostgres() em backend/internal/database/postgres.go), que
-- aplica exatamente este esquema. No primeiro boot, o backend também dá a cada
-- conta do Supabase Auth (exceto admins da plataforma) uma organização
-- própria (1 conta = 1 organização, a conta como dona — view/org_bootstrap.go). Este arquivo é o histórico/referência do
-- esquema e só precisa ser executado manualmente num banco operacional que
-- rode no próprio Supabase.
--
-- Isolamento: o escopo dos eventos passa por live_sessions.org_id (cada
-- evento aponta para uma sessão de live). Nenhuma tabela ganha política para
-- anon/authenticated: RLS default deny, como em 002/005.
--
-- Idempotente: pode ser executado mais de uma vez.

begin;

-- ── Organizações e membros ─────────────────────────────────────────────
create table if not exists public.organizations (
  id         text primary key,
  name       text not null,
  max_lives  integer not null default 3 check (max_lives >= 1),
  active     boolean not null default true,
  created_at timestamptz not null default current_timestamp
);

-- user_id = auth.users.id (texto, sem FK: o Postgres local não tem o schema auth).
-- PK em user_id: um usuário pertence a exatamente uma organização.
create table if not exists public.organization_members (
  user_id    text primary key,
  org_id     text not null references public.organizations (id) on delete cascade,
  email      text not null default '',
  role       text not null default 'operator' check (role in ('owner', 'operator')),
  created_at timestamptz not null default current_timestamp
);
create index if not exists idx_organization_members_org
  on public.organization_members (org_id);

-- Organização de legado: guarda os dados anteriores ao multi-tenant que não
-- têm dono identificável (antes tudo era global). NÃO aceita membros: só o
-- admin da plataforma a acessa, então esses dados nunca vazam para um cliente.
insert into public.organizations (id, name, max_lives)
values ('00000000-0000-0000-0000-000000000001', 'Legado (somente admin da plataforma)', 10)
on conflict (id) do nothing;
update public.organizations set name = 'Legado (somente admin da plataforma)'
 where id = '00000000-0000-0000-0000-000000000001' and name = 'Organização padrão';
delete from public.organization_members where org_id = '00000000-0000-0000-0000-000000000001';

-- Cada conta existente (exceto admins da plataforma) sem organização ganha a
-- sua (1 conta = 1 organização, a conta como dona). Nunca uma organização
-- compartilhada: clientes diferentes não podem ver os dados uns dos outros.
-- Só roda onde public.profiles existe (Supabase).
do $$
declare p record; new_org text;
begin
  if to_regclass('public.profiles') is null then return; end if;
  for p in select pr.id::text as id, pr.email, pr.display_name from public.profiles pr
           where pr.role <> 'admin'
             and not exists (select 1 from public.organization_members m where m.user_id = pr.id::text)
  loop
    new_org := gen_random_uuid()::text;
    insert into public.organizations (id, name, max_lives)
    values (new_org, left(coalesce(nullif(p.display_name, ''), nullif(p.email, ''), 'Organização ' || p.id), 80), 3);
    insert into public.organization_members (user_id, org_id, email, role)
    values (p.id, new_org, p.email, 'owner');
  end loop;
end $$;

-- ── Sessões de live por organização ────────────────────────────────────
alter table if exists public.live_sessions
  add column if not exists org_id text not null default '';
update public.live_sessions
   set org_id = '00000000-0000-0000-0000-000000000001'
 where org_id = '';
create index if not exists idx_live_sessions_org_name_day
  on public.live_sessions (org_id, live_name, day desc);

-- Configurações: o blob global 'app' vira o da organização de legado.
insert into public.settings (key, value)
select 'app:00000000-0000-0000-0000-000000000001', value
from public.settings where key = 'app'
on conflict (key) do nothing;

-- ── Fila PIX: owner_user_id (usuário) → org_id (organização) ───────────
-- Só age em bancos onde 005/006 foram aplicados na versão antiga. Todo dono
-- sem organização ganha uma própria ANTES de migrar as linhas; linhas sem
-- dono vão para a organização de legado.
do $$
declare t text; o text; new_org text;
begin
  foreach t in array array['pix_whatsapp_sessions','pix_contacts','pix_tickets','pix_messages','pix_value_rules'] loop
    if exists (select 1 from information_schema.columns
               where table_schema = 'public' and table_name = t and column_name = 'owner_user_id') then
      for o in execute format('select distinct owner_user_id from public.%I where owner_user_id <> %L', t, '') loop
        if not exists (select 1 from public.organization_members m where m.user_id = o) then
          new_org := gen_random_uuid()::text;
          insert into public.organizations (id, name, max_lives) values (new_org, 'Organização ' || left(o, 8), 3);
          insert into public.organization_members (user_id, org_id, role) values (o, new_org, 'owner');
        end if;
      end loop;
    end if;
  end loop;
  foreach t in array array['pix_whatsapp_sessions','pix_contacts','pix_tickets','pix_messages','pix_value_rules'] loop
    if exists (select 1 from information_schema.columns
               where table_schema = 'public' and table_name = t and column_name = 'owner_user_id') then
      execute format('alter table public.%I rename column owner_user_id to org_id', t);
      execute format(
        'update public.%I p set org_id = coalesce((select m.org_id from public.organization_members m where m.user_id = p.org_id), %L)',
        t, '00000000-0000-0000-0000-000000000001');
    end if;
  end loop;
end $$;

-- ── RLS default deny + sem GRANTs para o PostgREST ─────────────────────
alter table public.organizations        enable row level security;
alter table public.organization_members enable row level security;
revoke all on public.organizations, public.organization_members from anon, authenticated;

commit;

-- Verificação pós-aplicação:
-- select relname, relrowsecurity from pg_class
--  where relnamespace = 'public'::regnamespace
--    and relname in ('organizations','organization_members','live_sessions');
-- select o.name, m.role, count(*) from public.organization_members m
--   join public.organizations o on o.id = m.org_id group by 1, 2;
-- select count(*) from public.live_sessions where org_id = '';   -- esperado: 0
