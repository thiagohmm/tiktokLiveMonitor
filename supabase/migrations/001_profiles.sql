-- Perfis de acesso do TikTok Live Monitor (Supabase Auth + Postgres)

create table if not exists public.profiles (
  id uuid primary key references auth.users (id) on delete cascade,
  email text not null,
  display_name text not null default '',
  role text not null default 'subscriber' check (role in ('admin', 'subscriber')),
  active boolean not null default false,
  notes text not null default '',
  subscription_expires_at timestamptz,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create index if not exists idx_profiles_role on public.profiles (role);
create index if not exists idx_profiles_active on public.profiles (active);

-- Também corrige projetos que já executaram uma versão anterior da migração.
alter table public.profiles alter column active set default false;

alter table public.profiles enable row level security;

drop policy if exists "Usuário lê o próprio perfil" on public.profiles;
create policy "Usuário lê o próprio perfil"
  on public.profiles
  for select
  using (auth.uid() = id);

-- Não há política de escrita para clientes. A administração usa a service
-- role exclusivamente no backend, que ignora RLS. Isso impede autoaprovação
-- e autopromoção para admin. Remova políticas antigas autorreferentes.
drop policy if exists "Admin lê todos os perfis" on public.profiles;
drop policy if exists "Admin gerencia perfis" on public.profiles;

create or replace function public.handle_new_user()
returns trigger
language plpgsql
security definer
set search_path = ''
as $$
begin
  insert into public.profiles (id, email, role, active)
  values (
    new.id,
    coalesce(new.email, ''),
    coalesce(new.raw_app_meta_data->>'role', 'subscriber'),
    coalesce((new.raw_app_meta_data->>'active')::boolean, false)
  )
  on conflict (id) do nothing;
  return new;
end;
$$;

drop trigger if exists on_auth_user_created on auth.users;
create trigger on_auth_user_created
  after insert on auth.users
  for each row execute function public.handle_new_user();

-- Após criar o primeiro usuário no painel Supabase (Authentication > Users),
-- promova-o a admin nas claims E no perfil (troque o e-mail):
-- update auth.users
-- set raw_app_meta_data = coalesce(raw_app_meta_data, '{}'::jsonb)
--   || '{"role":"admin","active":true}'::jsonb
-- where email = 'seu@email.com';
-- update public.profiles
-- set role = 'admin', active = true, updated_at = now()
-- where email = 'seu@email.com';
