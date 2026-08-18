-- Schema Postgres (Supabase) — espelha o schema criado pelo migrate() do SQLite.
-- Aplique uma única vez no SQL Editor do projeto Supabase.
-- Sem RLS: as tabelas são acessadas pelo próprio backend via chave de serviço
-- (SUPABASE_DB_URL), nunca diretamente pelo navegador.

create table if not exists false_positives (
    id          bigint generated always as identity primary key,
    comment     text not null,
    category    text not null,
    expected    text not null default 'NAO',
    timestamp   timestamptz not null default now()
);

create table if not exists anomaly_logs (
    id          bigint generated always as identity primary key,
    live_name   text not null,
    day         date not null default (now() at time zone 'UTC')::date,
    timestamp   timestamptz not null default now(),
    uniqueId    text,
    comment     text not null,
    is_anomaly  integer not null default 0,
    category    text
);

create table if not exists gifts (
    id          bigint generated always as identity primary key,
    live_name   text not null,
    uniqueId    text not null,
    nickname    text not null,
    gift_name   text not null,
    repeat_count integer not null default 1,
    gift_type   integer not null default 0,
    timestamp   timestamptz not null default now()
);

create table if not exists user_messages (
    id          bigint generated always as identity primary key,
    uniqueId    text not null,
    username    text not null,
    message     text not null,
    timestamp   timestamptz not null default now()
);

create table if not exists target_gift_history (
    id            bigint generated always as identity primary key,
    live_name     text not null,
    uniqueId      text not null,
    nickname      text not null,
    gift_name     text not null,
    received_at   timestamptz not null default now(),
    answered_at   timestamptz,
    response_type text
);

create index if not exists idx_anomaly_logs_live_ts on anomaly_logs (live_name, timestamp desc);
create index if not exists idx_anomaly_logs_day on anomaly_logs (day);
create index if not exists idx_gifts_live_ts on gifts (live_name, timestamp desc);
create index if not exists idx_user_messages_uid on user_messages (uniqueId, timestamp desc);
create index if not exists idx_target_gift_history_live on target_gift_history (live_name, received_at desc);
