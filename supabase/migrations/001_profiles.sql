-- Perfis de acesso do TikTok Live Monitor (Supabase Auth + Postgres)

create table if not exists public.profiles (
  id uuid primary key references auth.users (id) on delete cascade,
  email text not null,
  display_name text not null default '',
  role text not null default 'subscriber' check (role in ('admin', 'subscriber')),
  active boolean not null default true,
  notes text not null default '',
  subscription_expires_at timestamptz,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create index if not exists idx_profiles_role on public.profiles (role);
create index if not exists idx_profiles_active on public.profiles (active);

alter table public.profiles enable row level security;

create policy "Usuário lê o próprio perfil"
  on public.profiles
  for select
  using (auth.uid() = id);

create policy "Admin lê todos os perfis"
  on public.profiles
  for select
  using (
    exists (
      select 1
      from public.profiles p
      where p.id = auth.uid()
        and p.role = 'admin'
        and p.active = true
    )
  );

create policy "Admin gerencia perfis"
  on public.profiles
  for all
  using (
    exists (
      select 1
      from public.profiles p
      where p.id = auth.uid()
        and p.role = 'admin'
        and p.active = true
    )
  );

create or replace function public.handle_new_user()
returns trigger
language plpgsql
security definer
set search_path = public
as $$
begin
  insert into public.profiles (id, email, role, active)
  values (
    new.id,
    coalesce(new.email, ''),
    coalesce(new.raw_app_meta_data->>'role', 'subscriber'),
    coalesce((new.raw_app_meta_data->>'active')::boolean, true)
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
-- promova-o a admin:
-- update public.profiles set role = 'admin' where email = 'seu@email.com';
