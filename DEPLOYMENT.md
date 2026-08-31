# Implantação SaaS

## Teste com Docker Compose

1. Copie `.env.example` para `.env`.
2. Troque `POSTGRES_PASSWORD` e use a mesma senha em `DATABASE_URL`.
3. Para um smoke test sem login, use `AUTH_ENABLED=0`.
4. Execute `docker compose up --build -d`.
5. Verifique `http://IP_DO_SERVIDOR:3001/api/readiness`.

O Compose inicia a aplicação e um PostgreSQL persistente. O backend cria as
tabelas operacionais automaticamente.

## Ativar login e aprovação de clientes

1. Crie um projeto Supabase.
2. Execute `supabase/migrations/001_profiles.sql` no SQL Editor.
3. Crie o primeiro usuário em Authentication > Users.
4. Execute os dois `UPDATE` do final da migração, trocando o e-mail, para
   promover esse usuário nas claims e no perfil.
5. Preencha no `.env`:
   `SUPABASE_URL`, `SUPABASE_ANON_KEY`, `SUPABASE_JWT_SECRET` e
   `SUPABASE_SERVICE_ROLE_KEY`.
6. Defina `AUTH_ENABLED=1` e recrie apenas a aplicação:
   `docker compose up -d --force-recreate tiktok-live-monitor`.

Novos clientes cadastrados pela tela administrativa ficam em **Aguardando
aprovação**. Depois da confirmação do pagamento, o administrador usa
**Aprovar**. Suspensão e validade da assinatura também são controladas nessa
tela, disponível separadamente em `/admin.html`. O servidor só entrega essa
página para uma sessão ativa com papel `admin`; assinantes recebem acesso
negado.

Nunca envie `SUPABASE_SERVICE_ROLE_KEY` ao navegador, à Vercel como variável
pública ou ao repositório. Ela é usada somente pelo backend Go.

## Vercel

Este backend mantém conexão longa com o TikTok e SSE, portanto deve continuar
em um servidor/container persistente. A Vercel hospeda os arquivos web e usa
as regras de `vercel.json` como proxy para a URL pública HTTPS do backend.
Substitua `https://SEU_BACKEND` antes do deploy.

Na Vercel/Supabase, use a URL do pooler PostgreSQL em `DATABASE_URL`. Não use o
hostname `postgres`/`db`, que existe somente na rede do Docker Compose.
