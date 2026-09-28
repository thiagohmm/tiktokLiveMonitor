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
    → WAHA :3000 (opcional: WhatsApp da Fila PIX; webhook → backend /api/webhooks/whatsapp)
    → MinIO :9000 (opcional: comprovantes PIX; próprio ou externo)
    → Supabase Auth + REST de profiles (usuários e assinaturas)
```

docker-compose.production.yml gerencia postgres, backend, frontend, nginx, o
serviço eventual certbot e, só com a [Fila PIX](#fila-pix) ativada, waha e
minio. Somente o Nginx publica portas 80/443; WAHA e MinIO ficam apenas na rede
interna. Ambos os proxies devem manter buffering desativado e timeout longo
para SSE.

**A migração do Supabase é parcial:** Auth, usuários, senhas, claims e
public.profiles permanecem em https://vcbvctmhwnurdnfssjfj.supabase.co.
A administração acessa profiles pelo backend. Não excluir o projeto Supabase.

O banco local contém 12 tabelas operacionais: anomaly_logs, false_positives,
gift_goals, gifts, likes, live_sessions, pinned_comments, room_like_totals,
settings, shares, target_gift_history e user_messages. A Fila PIX acrescenta
pix_whatsapp_sessions, pix_contacts, pix_tickets, pix_messages e pix_value_rules
(ver [docs/fila-pix.md](docs/fila-pix.md)). Todas são criadas pelo backend no boot.

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
- Fila PIX: opcional; sem WAHA_* e MINIO_* o backend sobe com a fila desligada
  (ver [Fila PIX](#fila-pix)).

Volumes: tiktok-live-monitor_postgres-data, tiktok-live-monitor_letsencrypt,
tiktok-live-monitor_certbot-www e, com a Fila PIX, tiktok-live-monitor_waha_sessions
(pareamento WhatsApp) e tiktok-live-monitor_minio_data (comprovantes, só com
MinIO próprio).
Não executar docker compose down -v em produção.

## HTTPS e renovação

Um certificado Let's Encrypt cobre domínio e www, incluindo /promo e /login.
O certificado inicial expira em 14/12/2026. O arquivo deploy/nginx/default.conf
**no VPS** contém HTTPS, redirecionamento HTTP e os blocos do Sigmenta e do
agenttk (ver [VPS compartilhado](#vps-compartilhado)).

O arquivo homônimo no Git espelha essa configuração ativa (25/09/2026). Ele exige
os certificados e os upstreams existentes. Não sobrescrever a configuração ativa
durante deploys.

```sh
CERTBOT_EMAIL=seu-email@example.com ./deploy/enable-https.sh
```

deploy/enable-https.sh (DOMAIN, padrão livemonitortk.com.br) só mexe nos blocos
`server {}` cujo server_name é o DOMAIN ou www.DOMAIN; os demais (Sigmenta,
map, upstreams) são preservados byte a byte:

- **Bloco 443 do DOMAIN presente e certificado no volume** (caso deste VPS): não
  altera a conf. Roda `certbot certonly --keep-until-expiring` (só renova perto
  do vencimento), `nginx -t` e reload.
- **Certificado presente, sem bloco 443**: troca o bloco :80 do DOMAIN pelo
  redirect com ACME e acrescenta um bloco 443 padrão (só `/` para o frontend).
- **Sem certificado** (instalação nova): troca temporariamente o bloco :80 do
  DOMAIN por um bootstrap HTTP com ACME e tira o bloco 443 do DOMAIN, se houver.
  Depois emite pelo webroot compartilhado e ativa o redirect e o HTTPS. O bloco
  443 que existia (com `/agenttk/`, `/media/` e os `.txt`) volta igual. Durante
  a emissão, essas rotas ficam fora do ar.

Antes de cada gravação, o script salva a conf em
`backups/nginx/default.conf.<timestamp>.pre-<fase>.bak` e grava no mesmo arquivo
(mesmo inode, compatível com o bind mount). Em seguida roda `nginx -t` na borda e,
se o teste falhar, restaura o backup sem reload. A borda nunca é recriada: com o
container rodando, usa só `exec nginx -s reload`. Se o container estiver parado
(servidor novo), o script o inicia com `up -d --no-recreate nginx`. Se a emissão
falhar, a conf fica no bootstrap HTTP e o script indica o backup para restaurar.
Num servidor novo sem Sigmenta/agenttk, remova antes esses blocos e a rede
`sigmenta_edge` do default.conf e do compose, ou crie a rede e os certificados
deles; senão o `nginx -t` falha e nada é aplicado.

Cron do root:

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

O pg_dump não cobre os volumes do WAHA (sessões pareadas) nem do MinIO
(comprovantes, que são temporários e expurgados ao desconectar a live).
Perder waha_sessions obriga cada usuário a parear o WhatsApp de novo. Se
necessário, copiar o volume com o serviço parado, por exemplo:

```sh
docker compose -f docker-compose.production.yml stop waha
docker run --rm -v tiktok-live-monitor_waha_sessions:/data -v "$PWD/backups":/out \
  alpine tar -czf "/out/waha-sessions-$(date -u +%Y%m%dT%H%M%SZ).tgz" -C /data .
docker compose -f docker-compose.production.yml start waha
```

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
docker compose -f docker-compose.production.yml up -d --build --wait postgres backend frontend
docker compose -f docker-compose.production.yml exec -T frontend nginx -t
docker compose -f docker-compose.production.yml exec -T nginx nginx -t
docker compose -f docker-compose.production.yml exec -T nginx nginx -s reload
docker compose -f docker-compose.production.yml ps
```

Com a Fila PIX ativa, acrescentar os serviços e arquivos do modo escolhido
(ver [Fila PIX](#fila-pix)); nomear `waha` ou `minio` no `up` ativa o profile
deles mesmo sem COMPOSE_PROFILES.

O reload da borda atualiza a resolução do frontend se o container mudar de IP.
Mudanças só no frontend podem usar up -d --build --no-deps frontend, seguido
de validação e reload.

5. Verificar /promo, /login e /api/readiness com HTTPS válido. Para isolar
problemas externos de rede, executar **no VPS** (e conferir também
sigmenta.com.br e /agenttk/, ver [VPS compartilhado](#vps-compartilhado)):

```sh
curl --fail --resolve livemonitortk.com.br:443:127.0.0.1 https://livemonitortk.com.br/api/readiness
```

Isso valida TLS e aplicação localmente, não acesso pela internet. Verificar
também de uma rede externa. Em 15/09/2026, /promo respondeu externamente com
HTTP 200 e certificado válido. Houve timeouts anteriores durante a migração;
a causa não foi determinada.

## VPS compartilhado

O Nginx de borda deste compose é o único processo em 80/443 no VPS e atende
também outros dois apps:

- **Sigmenta** (sigmenta.com.br e www): proxy para 172.30.0.10:8080 pela rede
  Docker externa `sigmenta_edge` (criada pelo projeto do Sigmenta). A borda
  precisa estar nela com o IP fixo 172.30.0.11, que o Sigmenta usa em
  `set_real_ip_from`. O certificado fica no mesmo volume letsencrypt.
- **agenttk** (`/agenttk/`, `/media/` e os `.txt` de verificação do TikTok):
  proxy para `agenttk-oauth:8090`. O compose do agenttk usa
  `tiktok-live-monitor_default` como rede externa, por isso o nome dessa rede é
  fixado no compose e não pode mudar.

Regras de deploy:

- Recriar só os serviços alterados, sem o nginx:
  `docker compose -f docker-compose.production.yml up -d --build <serviços>`.
  Não rodar `up` sem lista de serviços nem `--force-recreate` na borda: a
  recriação derruba os três sites e, se `agenttk-oauth` não resolver, o nginx
  não sobe.
- Nunca `docker compose down` (remove a borda e a rede default usada pelo
  agenttk); nunca `down -v`.
- Antes de qualquer reload: `exec -T nginx nginx -t`, e só então
  `exec -T nginx nginx -s reload`.
- Alterações no default.conf passam pelo Git; editar no servidor exige
  trazer a mudança de volta para o repositório.

## Fila PIX

A Fila PIX (WAHA + MinIO + OCR com tesseract, este já na imagem do backend) é
opcional. Detalhes funcionais em [docs/fila-pix.md](docs/fila-pix.md).

### Desligada (padrão)

Sem COMPOSE_PROFILES, WAHA_* e MINIO_* no .env, o compose sobe só postgres,
backend, frontend e nginx. O backend loga `Fila PIX: desabilitada` e as rotas
`/api/pix*` e o webhook respondem 503; o monitor de lives não é afetado.
WAHA_ENABLED tem default 0 em produção.

### Recursos do VPS

O VPS tem 1 vCPU e 1,7 GB de RAM compartilhados com o Sigmenta. Use o WAHA com
engine GOWS (imagem `devlikeapro/waha:gows-<versão>`, sem Chromium, limitada por
WAHA_MEM_LIMIT, default 384m). A imagem `:latest` usa WEBJS/Chromium
(300–600 MB) e não cabe junto dos dois apps. Conferir `free -m` e
`docker stats --no-stream` antes e depois de ativar.

### Modo A — MinIO próprio

No .env:

```sh
COMPOSE_PROFILES=pix,pix-minio
WAHA_ENABLED=1
WAHA_API_KEY=...            # chave longa
WAHA_WEBHOOK_SECRET=...     # segredo longo
WAHA_IMAGE=devlikeapro/waha:gows-2026.9.1
MINIO_ENDPOINT=minio:9000
MINIO_ACCESS_KEY=...        # vira o root do container minio
MINIO_SECRET_KEY=...
MINIO_BUCKET=tlm-pix-media
```

```sh
docker compose -f docker-compose.production.yml config --quiet
docker compose -f docker-compose.production.yml up -d --wait waha minio
docker compose -f docker-compose.production.yml up -d --build --wait backend
docker compose -f docker-compose.production.yml logs --tail 20 backend | grep 'Fila PIX'
```

O container minio se recusa a subir com MINIO_ACCESS_KEY/MINIO_SECRET_KEY vazios
(a imagem bitnami cairia em credenciais padrão). A RAM é limitada por
MINIO_MEM_LIMIT (default 256m).

### Modo B — MinIO externo (ex.: cluster do Sigmenta)

O backend entra numa rede Docker externa onde o MinIO responde, via o arquivo
extra deploy/compose.minio-externo.yml; o serviço minio deste compose não sobe.
Requisitos, preparados pelo responsável do outro app:

- uma rede Docker **dedicada** (ex.: `tlm_minio`, `internal`) contendo só os nós
  do MinIO. Não usar uma rede que também tenha o banco ou a API do outro app;
- um bucket dedicado (`tlm-pix-media`) e um usuário com policy restrita a esse
  bucket (List/Get/Put/Delete), sem admin nem acesso a outros buckets. O
  backend só consulta a existência do bucket; ele não o cria se já existir.

No .env:

```sh
COMPOSE_PROFILES=pix
WAHA_ENABLED=1
WAHA_API_KEY=...
WAHA_WEBHOOK_SECRET=...
WAHA_IMAGE=devlikeapro/waha:gows-2026.9.1
MINIO_EXTERNAL_NETWORK=tlm_minio
MINIO_ENDPOINT=<container-do-minio>:9000
MINIO_ACCESS_KEY=...        # usuário restrito, nunca o root do cluster
MINIO_SECRET_KEY=...
MINIO_BUCKET=tlm-pix-media
```

Todo comando que recria o backend precisa dos dois arquivos; sem o segundo, o
backend perde a rede do MinIO e a fila para de gravar comprovantes:

```sh
F="-f docker-compose.production.yml -f deploy/compose.minio-externo.yml"
docker compose $F config --quiet
docker compose $F up -d --wait waha
docker compose $F up -d --build --wait backend
docker compose $F exec -T backend sh -c 'curl -s -o /dev/null -w "%{http_code}\n" http://$MINIO_ENDPOINT/minio/health/live'
docker compose $F logs --tail 20 backend | grep 'Fila PIX'
```

Backup e expurgo dos comprovantes passam a depender do cluster externo; os
comprovantes são temporários (apagados ao desconectar a live).

### Desativar

Remover COMPOSE_PROFILES e as variáveis da fila do .env, recriar o backend
(`up -d --no-deps backend`) e parar `waha`/`minio` com `stop` (sem `down`).

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

Vercel e Railway foram desligados. O único deploy é o docker compose no VPS
(docker-compose.production.yml); nenhum push publica fora dele.

- Vercel: projetos tiktok-live-monitor (tiktok-live-monitor-two.vercel.app,
  tiktoklivemonitor.online) e frontend (frontend-six-silk-83.vercel.app).
- Railway: projeto tiktok-live-monitor, serviço backend
  (backend-production-9986.up.railway.app), antes publicado a cada push na
  branch lite-sem-ia.
- frontend/vercel.json e backend/.railway/railway.ts foram removidos do
  repositório. Links antigos para *.vercel.app não levam mais ao sistema; o
  endereço oficial é https://livemonitortk.com.br.

## Schema e rollback

O backend migra as tabelas operacionais e da Fila PIX ao iniciar
(migratePostgres): atualizar a imagem do backend já aplica o schema novo, sem
SQL Editor nem passo manual. Os SQLs em supabase/migrations/ são o histórico do
Supabase e citam auth.users e os papéis anon/authenticated, que não existem no
PostgreSQL do compose; não aplicá-los indiscriminadamente. 001_profiles.sql
pertence ao projeto Supabase, onde profiles continua. Para SQL manual no VPS:

```sh
docker compose -f docker-compose.production.yml exec -T postgres \
  psql -U tlm -d tiktok_live_monitor < arquivo.sql
```

A migração 004 adicionou live_id e mudou constraints/PKs. Voltar ao backend
anterior pode exigir supabase/migrations/004_live_sessions_rollback.sql junto.
Planejar com backup e manutenção: rollback de imagem não reverte schema nem dados.
