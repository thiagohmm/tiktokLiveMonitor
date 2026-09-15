# Produção — TikTok Live Monitor

Ambiente migrado em 15/09/2026. Desenvolvimento local: [DEPLOYMENT.md](DEPLOYMENT.md).

## Endereços

| Uso | Endereço |
|---|---|
| Página comercial | https://livemonitortk.com.br/promo |
| Login da ferramenta | https://livemonitortk.com.br/login |
| Painel (exige sessão) | https://livemonitortk.com.br/ |
| Administração | https://livemonitortk.com.br/admin |
| Recuperação de senha | https://livemonitortk.com.br/reset-password.html |
| Readiness | https://livemonitortk.com.br/api/readiness |
| SSH | ssh -p 22022 root@143.95.162.200 |
| Diretório remoto | /opt/tiktok-live-monitor |
| Branch | lite-sem-ia |

/login.html, /admin.html e /index.html continuam válidos. A raiz abre o painel;
a apresentação comercial fica em /promo. O registro A aponta para 143.95.162.200;
www é CNAME do domínio principal.

## Arquitetura

```text
Navegador HTTPS → Nginx de borda :443 → frontend Nginx :80
  → backend Go + bridge Node :3001 (/api/* e /events)
    → PostgreSQL Docker :5432 (dados operacionais)
    → Supabase Auth + REST de profiles (usuários e assinaturas)
```

docker-compose.production.yml gerencia postgres, backend, frontend, nginx e o
serviço eventual certbot. Somente o Nginx publica portas 80/443. Ambos os proxies
devem manter buffering desativado e timeout longo para SSE.

**A migração do Supabase é parcial:** Auth, usuários, senhas, claims e
public.profiles permanecem em https://vcbvctmhwnurdnfssjfj.supabase.co.
A administração acessa profiles pelo backend. Não excluir o projeto Supabase.

O banco local contém 12 tabelas operacionais: anomaly_logs, false_positives,
gift_goals, gifts, likes, live_sessions, pinned_comments, room_like_totals,
settings, shares, target_gift_history e user_messages.

## Configuração e persistência

O .env remoto possui permissão 600 e não é versionado. O modelo é
.env.production.example; não copiá-lo sobre um ambiente existente.

- DATABASE_URL aponta para host postgres, banco tiktok_live_monitor, usuário tlm.
  Quando não definida, o Compose deriva a URL de POSTGRES_*. Se definida, a senha
  deve coincidir com POSTGRES_PASSWORD. Prefira senha hexadecimal para essa URL.
- AUTH_ENABLED=1; SUPABASE_URL, SUPABASE_ANON_KEY e SUPABASE_SERVICE_ROLE_KEY
  são necessários. Service role nunca vai para o navegador. JWT_SECRET vazio
  utiliza validação remota.
- DOMAIN=livemonitortk.com.br. O Compose define SITE_URL e CORS nessa origem HTTPS.
- E-mail/pagamento: RESEND_API_KEY, SMTP_*, MAIL_* e PAYMENT_*.
- TRUSTED_PROXIES deve corresponder à rede dos proxies Docker.

Volumes: tiktok-live-monitor_postgres-data, tiktok-live-monitor_letsencrypt e
tiktok-live-monitor_certbot-www. Não executar docker compose down -v em produção.

## HTTPS e renovação

Um certificado Let's Encrypt cobre domínio e www, incluindo /promo e /login.
O certificado inicial expira em 14/12/2026. O arquivo deploy/nginx/default.conf
**no VPS** contém HTTPS e redirecionamento HTTP.

O arquivo homônimo no Git é o bootstrap HTTP de primeira instalação.
**Não sobrescrever a configuração ativa durante deploys.** No servidor novo:

```sh
CERTBOT_EMAIL=seu-email@example.com ./deploy/enable-https.sh
```

O script emite pelo webroot compartilhado do Compose e ativa HTTPS. Cron do root:

```cron
17 3 * * * cd /opt/tiktok-live-monitor && ./deploy/renew-https.sh >> /var/log/tiktok-live-monitor-renew.log 2>&1
```

A renovação valida e recarrega Nginx sem TTY. Para testar:

```sh
docker compose -f docker-compose.production.yml --profile certificates run --rm certbot renew --dry-run
```

Simulação executada com sucesso em 15/09/2026 para o domínio e www.

## Backups

Arquivos iniciais com permissão 600 em /opt/tiktok-live-monitor/backups/:

- supabase-full-20260915T150313Z.dump: dump lógico completo acessível ao usuário
  Supabase, sem owners/ACLs; não inclui configuração externa, segredos da
  plataforma nem objetos de Storage.
- operational-20260915T150313Z.dump: public sem profiles, restaurado localmente.
  Houve aviso de schema public já existente. As 12 tabelas e contagens foram
  comparadas com a origem na ocasião.

Não há sincronização contínua entre os bancos. Gravações no ambiente antigo
após a cópia precisam de reconciliação antes de descarte. Os backups estão no
mesmo VPS; cópia externa e backups periódicos do banco ainda precisam ser implantados.

Backup antes de atualizar, no VPS:

```sh
cd /opt/tiktok-live-monitor
umask 077
mkdir -p backups
docker compose -f docker-compose.production.yml exec -T postgres \
  pg_dump -U tlm -d tiktok_live_monitor -Fc \
  > "backups/predeploy-$(date -u +%Y%m%dT%H%M%SZ).dump"
```

Restaurar primeiro em banco separado e validar. Não restaurar um dump antigo
sobre o banco ativo.

## Publicar uma versão

1. Validar frontend e Go, revisar com DeepSeek conforme AGENTS.md e criar commit.
   Testes de banco exigem TEST_DATABASE_URL em PostgreSQL descartável.
2. Fazer backup pelo procedimento acima.
3. Na raiz do checkout, enviar apenas os arquivos do commit, preservando .env,
   backups, certificados e a configuração HTTPS gerada no servidor:

```sh
git archive HEAD | ssh -p 22022 root@143.95.162.200 \
  'tar -xf - -C /opt/tiktok-live-monitor --exclude=deploy/nginx/default.conf'
```

A extração não remove arquivos remotos: exclusões precisam de tratamento
explícito. Na instalação inicial, transferir também o bootstrap HTTP e preencher
.env antes de subir os serviços.

4. No VPS:

```sh
cd /opt/tiktok-live-monitor
docker compose -f docker-compose.production.yml config --quiet
docker compose -f docker-compose.production.yml up -d --build --wait
docker compose -f docker-compose.production.yml exec -T frontend nginx -t
docker compose -f docker-compose.production.yml exec -T nginx nginx -t
docker compose -f docker-compose.production.yml exec -T nginx nginx -s reload
docker compose -f docker-compose.production.yml ps
```

O reload da borda atualiza a resolução do frontend se o container mudar de IP.
Mudanças só no frontend podem usar up -d --build --no-deps frontend, seguido
de validação e reload.

5. Verificar /promo, /login e /api/readiness com HTTPS válido. Para isolar
problemas externos de rede, executar **no VPS**:

```sh
curl --fail --resolve livemonitortk.com.br:443:127.0.0.1 https://livemonitortk.com.br/api/readiness
```

Isso valida TLS e aplicação localmente, não acesso pela internet. Verificar
também de uma rede externa. Em 15/09/2026, /promo respondeu externamente com
HTTP 200 e certificado válido. Houve timeouts anteriores durante a migração;
a causa não foi determinada.

## Recuperação de senha

SITE_URL=https://livemonitortk.com.br. A allowlist Redirect URLs no Supabase deve
incluir https://livemonitortk.com.br/reset-password.html. Conferir allowlist e
entrega do mailer antes do teste completo.

O backend gera o link via Supabase e envia por Resend/SMTP. A página remove o
token do hash após lê-lo. Falhas de rede/5xx do Auth retornam 502; link inválido
retorna 400. Testes com mocks não substituem entrega real e redefinição de senha.

## Página comercial

landing.html e landing.css são servidos em /promo. landing-offer.js mostra R$ 20
até 22/09/2026, horário de Brasília, e oculta a oferta a partir de 23/09 00:00.
O prazo controla a apresentação, não a cobrança: revisar PAYMENT_PRICE e os
e-mails ao encerrar a campanha.

O CTA leva a /login?tab=signup; as informações do canal ficam nas notas.
Avaliação e liberação após confirmação de pagamento são ações manuais da equipe.

## Ambientes anteriores

- A raiz de https://tiktok-live-monitor-two.vercel.app responde 308 para
  https://livemonitortk.com.br/login.html. Rewrites antigos de outras rotas
  continuam em frontend/vercel.json; não é uma desativação completa.
- https://backend-production-9986.up.railway.app é legado. A integração GitHub
  configurada anteriormente pode publicar automaticamente após push na branch
  lite-sem-ia; isso não publica no VPS.
- Os ambientes anteriores não foram removidos. Verificar gravações e
  dependências antes de desativá-los.

Para publicar alterações no redirect Vercel, executar **na raiz do repositório**,
pois o projeto já define Root Directory=frontend:

```sh
npx vercel --prod --yes --scope team_nr9JpG1qX8WCl8feRvjBdp3W
```

## Schema e rollback

O backend migra as tabelas operacionais ao iniciar. SQLs de Supabase citam
papéis específicos (anon/authenticated); não aplicá-los indiscriminadamente no
PostgreSQL local. Profiles continua no Supabase.

A migração 004 adicionou live_id e mudou constraints/PKs. Voltar ao backend
anterior pode exigir supabase/migrations/004_live_sessions_rollback.sql junto.
Planejar com backup e manutenção: rollback de imagem não reverte schema nem dados.
