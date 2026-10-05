# Log de remediação — o que foi aplicado na produção

**Referência:** `docs/security/RELATORIO.md` (auditoria de 2026-10-05, commit base `6d6e3a0`).
**Regra:** cada onda é aplicada, verificada com evidência e só então passa para a seguinte.

---

## Onda 0 — VPS, sem deploy de código ✅

| Ação | Antes | Depois |
|---|---|---|
| Permissões dos segredos | `.env.admin-password`, `.env.local`, `.env.supabase-password` em `-rw-r--r--` (644) | `-rw-------` (600) |
| Container órfão | `tlm-auth-local-candidate` (imagem `tlm-backend:auth-local-20261003`) rodando há 28 h na rede da aplicação, 0 % CPU, sem conexões, ocioso desde o teste de reset-password de 03/10 | Removido. A imagem foi preservada para reexecução se necessário |
| Backend | `Up 28 hours (healthy)` | Inalterado — nenhuma interrupção |

Nada referenciava os arquivos de segredo (verificado por `grep` no repo e na VPS); o `.env` usado pelo Compose permaneceu intacto.

## Onda 1 — Bordas nginx ✅ (aplicada e verificada)

Commit do submódulo: `93d0e3e` (publicado em `thiagohmm/vps-edge-nginx`). Backup: `/root/edge-backup-20261005T003332Z/`. `nginx -t` antes de cada reload.

| Achado | Antes (medido) | Depois (medido) |
|---|---|---|
| HSTS suprimido (P3-ALT) | `/`, `/admin`, `/login`, `/config.js` **sem** HSTS; só `/api/*` tinha | Todos com `strict-transport-security: max-age=31536000; includeSubDomains` |
| CSP/XFO/RP (P3-ALT) | Ausentes | CSP, `X-Frame-Options: DENY`, `Referrer-Policy`, `nosniff`, `Permissions-Policy` em toda a UI |
| Open redirect por `$host` (V-01) | `curl -H 'Host: evil.example' http://143.95.162.200/` → `Location: https://evil.example/` | 444 (sem resposta). Com Host correto → `Location: https://livemonitortk.com.br/` |
| Host/SNI arbitrário serve a UI (V-03) | `curl -k -H 'Host: outro.example' https://143.95.162.200/` → 200 com o `index.html` (75 120 bytes) | Conexão encerrada (444) |
| Versão do nginx no HTTP (V-02) | `Server: nginx/1.27.5` | `server_tokens off` também no bloco `:80` |
| Sem rate limit (P4-ALT) | Nenhuma barreira de borda | 80 requisições paralelas → **32×400 + 48×429** na zona de auth e **71×200 + 9×429** na zona geral |
| IP forjável no lockout (L2-2) | `X-Forwarded-For` repassado do cliente | Sobrescrito com `$remote_addr` |
| Timeout de 1 h em toda a API | `proxy_read_timeout 3600s` em `/api` **e** `/events` | 1 h apenas em `/events`; 60–120 s nas demais |
| ACME | Funcional | Funcional (`/.well-known/acme-challenge/...` → 404, não 444) |
| Aplicação | `root=200 auth_config=200 state=401` | Inalterado |

Burst da zona de autenticação ajustado para 30 após o primeiro teste bloquear um humano na 4ª tentativa — o controle fino de brute force continua no lockout do backend.

## Onda 2 — Código ✅ (construído e implantado)

Deploy: backup `backups/predeploy-20261005T003926Z.dump` (1,2 MB), tag de rollback `tlm-backend:preaudit-20261005T003935Z`, `up -d --build --wait` → backend `healthy`.

| Achado | Correção | Verificação em produção |
|---|---|---|
| L2-1 enumeração no signup | Resposta 201 idêntica ao sucesso; conflito só no log | Revisão de código + teste de regressão |
| L1-3 rotas admin sem checagem central | `tenantMiddleware` exige role `admin` nos caminhos isentos | `TestTenantExemptAdminCoversOnlyPlatformAdminRoutes` |
| L2-10 logout falso-sucesso | `SignOutGlobal` devolve `ErrSessionNotFound` | `POST /api/auth/logout` com token inexistente → **401** (antes `200 {"success":true}`) |
| L2-8 reset sem limite e sem `Enabled` | Exige auth habilitada + lockout por IP | Código + revisão |
| L1-4 sem guarda de método | `/api/state` e `/api/readiness` só aceitam GET | `POST /api/readiness` com Origin válida → **405** |
| P6 readiness com métricas | Só `{"ready":true}` | `curl /api/readiness` → `{"ready":true}` |
| L2-11 Origin sem normalização | Compara origem com barra final normalizada | Matriz de Origin no teste de regressão |
| L5-1 open redirect no login | Exige mesma origem e recusa `\` | `node`: `/\evil.com` → `/index.html`; `/admin.html` → passa |
| L5-2 dev server servia `.env.local` | Denylist de dotfiles + remoção do arquivo | `GET /.env.local` → **404**; `/.git/config` → 404 |
| L5-3 `/%` derrubava o dev server | `try/catch` → 400 | `GET /%` → **400**, processo vivo |
| L5-4 `renderProfile` sem escape | 4 campos com `escapeHtml` | Código |
| L5-5 token de convite em `sessionStorage` | Apenas em memória | Código |
| G-01/G-02 toolchain e imagem | `go 1.26.8`, imagem fixada em `golang:1.26.8-bookworm` | Build local baixou 1.26.8 automaticamente |
| L2-2/L6-14 `TRUSTED_PROXIES` | `172.16.0.0/12` → **`172.18.0.5/32`** + documentado no compose | **Prova de hash**: a chave gravada em `auth_rate_limits` é `SHA256("email|186.220.37.106")` = meu IP real, não o IP do edge |
| L2-7 ponte legada do Supabase | `SUPABASE_URL`, `SUPABASE_ANON_KEY` e `SUPABASE_SERVICE_ROLE_KEY` desativados no `.env` | `SUPABASE_URL` ausente no container; 0 usuários sem senha no banco |

### Achado novo durante a Onda 2
O `.env` de produção continha `SUPABASE_SERVICE_ROLE_KEY` (`sb_secret_…`) em texto claro, **usado por nenhum código** — resíduo da era Supabase. Desativado junto com as outras chaves. **Se o projeto Supabase ainda existir, essa credencial deve ser revogada** (ela dá acesso privilegiado ao projeto).

---

## Pendente

### Onda 3 — auth e integrações
- L2-4: teto global de lockout por conta (hoje só `email|ip`).
- L4-1: rate limit do webhook — **coberto pela borda** nesta rodada; guarda de comprimento do segredo (o segredo atual tem 64 caracteres, então a validação entra sem quebrar nada).
- L4-2: checar duplicata **antes** de baixar/gravar mídia do comprovante.
- L4-3: teto de clientes SSE por organização + cache de `authorize()`.
- L4-4: redação de PII/conteúdo de chat nos logs.
- L3-3: checagem de papel dentro dos handlers de escrita (defesa em profundidade).

### Onda 4 — infraestrutura
- L6-6: Docker `secrets:` no lugar de `environment:`.
- L6-7: guarda do `WAHA_API_KEY` (avaliar `:?` sem quebrar instalações sem PIX).
- P7-MED: `user:`, `read_only`, `cap_drop`, `no-new-privileges`.
- L6-11: `pg_hba` `local all all scram-sha-256`.
- L6-12: backup agendado com destino externo.

### RLS (P1-CRIT)
Testar em banco descartável a remoção de `SUPERUSER`/`BYPASSRLS`/`CREATEDB`/`CREATEROLE` do `tlm` (mantendo a posse das tabelas, já que `force=false` faz o owner ignorar RLS) e validar que `migratePostgres` sobe. Criar políticas por `org_id` ou remover a alegação de "default deny" da documentação.

### Segredos no git (S-01/S-02) — depende do dono
- Redigir a chave no arquivo do HEAD.
- **Revogar a chave Blackbox no provedor** (única mitigação definitiva).
- Reescrita de histórico (`filter-repo`) com force-push — destrutiva, exige confirmação explícita.

### Avaliados e rejeitados conscientemente
| Achado | Decisão | Motivo |
|---|---|---|
| L2-5 signup/recover contam sucesso como falha | **Mantido** | É rate limit por IP contra criação de contas em massa, não contador de falhas. `RecordSuccess` no caminho feliz removeria o único freio |
| L2-6 CSRF determinístico derivado da sessão | **Mantido** | Rotacionar de verdade quebraria o CSRF entre abas e não agrega: quem tem o token de sessão já pode usá-lo como cookie |
| L2-9 prefixo `__Host-` no cookie | **Adiado** | Renomear o cookie desloga todos os usuários ativos em produção; ganho marginal frente ao custo |
