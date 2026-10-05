# Fase 0 — Baseline e Reconhecimento (evidência)

**Data da coleta:** 2026-10-05T00:13Z
**Alvo:** `/Users/thiagohmm/tiktokLiveMonitor` + VPS `143.95.162.200:22022` (produção, **somente leitura**)

## 1. Baseline do repositório

| Item | Valor |
|---|---|
| Commit | `6d6e3a095ae28ece16217c243319ad0504023256` (`6d6e3a0`) |
| Branch | `lite-sem-ia` |
| Working tree | limpo exceto artefatos desta auditoria (2 entradas não rastreadas) |
| Backend | 114 arquivos `.go`, ~29.956 LOC, Go 1.26.2 (toolchain local 1.26.2 darwin/arm64) |
| Frontend | HTML/JS vanilla; ~273 KB de JS em `frontend/*.js`, com cópia espelhada em `frontend/dist/` |
| Dependências Go | `pgx/v5`, `minio-go/v7`, `golang.org/x/crypto`, `ledongthuc/pdf` |
| Dependências JS | vendor: `jspdf.umd.min.js`, `chart.umd.js` (sem `package.json`/lockfile no repo) |
| Infra | `docker-compose{,.production,.raspberry}.yml`, `deploy/nginx`, submódulo `edge-nginx` |
| CI de segurança | **inexistente** |

## 2. Ferramental disponível

- Presentes: `golangci-lint 2.11.4` (com `gosec`, `govet`, `staticcheck`), `go 1.26.2`, `npm`, `uvx` (semgrep), `docker` CLI.
- Ausentes localmente: `gosec`, `govulncheck`, `semgrep`, `gitleaks`, `trufflehog`, `trivy`, `osv-scanner`, `staticcheck` — cobertos via `go run`/`uvx` (autocontidos) em vez de imagens.
- **Docker daemon local parado** ⇒ varreduras de imagem/container adiadas (exigem subir o daemon ou rodar na VPS, o que requer autorização explícita por ser host de produção).

## 3. Reconhecimento da VPS (read-only)

Comandos usados: `ss -tlnp`, `docker ps`, `docker inspect`, `docker exec ... psql -c "SELECT"`, `ls -l`, `curl -sSI`. Nenhuma escrita, nenhum reinício, nenhuma alteração de configuração.

### 3.1 Exposição de rede

```
LISTEN 0.0.0.0:22022  sshd
LISTEN 0.0.0.0:80     docker-proxy (vps-edge-nginx)
LISTEN 0.0.0.0:443    docker-proxy (vps-edge-nginx)
```

Portas publicadas nos containers: **apenas** `80`/`443` do `vps-edge-nginx`.
`backend:3001`, `postgres:5432`, `waha:3000` têm `Ports: {"..../tcp": null}` ⇒ **não publicados no host**. ✅ correto.

### 3.2 Hardening dos containers

| Container | Imagem | Privileged | ReadonlyRootfs | Usuário |
|---|---|---|---|---|
| `tiktok-live-monitor-backend-1` | `tiktok-live-monitor-backend` | false | false | vazio ⇒ **root** |
| `tiktok-live-monitor-postgres-1` | `postgres:17-alpine` | false | false | vazio ⇒ root |
| `tiktok-live-monitor-waha-1` | `devlikeapro/waha:gows-2026.9.1` | false | false | vazio ⇒ root |
| `vps-edge-nginx` | `nginx:1.27-alpine` | false | false | vazio ⇒ root |
| `sigmenta-v3-api`, `sigmenta-v2-minio-1..4`, `agenttk-integration-oauth-1` | (outros produtos no mesmo host) | false | false | vazio ⇒ root |
| `tlm-auth-local-candidate` | `tlm-backend:auth-local-20261003` | false | false | vazio ⇒ root |

### 3.3 PostgreSQL — estado de autorização (crítico)

```
tlm|rolsuper=t|rolbypassrls=t|rolcreatedb=t|rolcreaterole=t
current_user=tlm  session_user=tlm  current_database=tiktok_live_monitor
pg_hba: local all all trust ; host all all all scram-sha-256
```

- Todas as **29 tabelas** de `public` têm `relrowsecurity = true` e `relforcerowsecurity = false`.
- **`SELECT count(*) FROM pg_policies` ⇒ `0`.** Não existe **nenhuma** política RLS criada.

Consequência: o "RLS default deny" descrito em `AGENTS.md` **não existe em produção**. O usuário da aplicação é `tlm`, que é *superuser* **e** tem `BYPASSRLS`, portanto ignora RLS de qualquer forma. O isolamento entre organizações depende **exclusivamente** de cada consulta da aplicação incluir `org_id`.

### 3.4 Arquivos de ambiente no host

```
-rw------- 1 root root      2115 .env                     ← correto
-rw-r--r-- 1  501 games       33 .env.admin-password      ← 644
-rw-r--r-- 1  501 games     1325 .env.local               ← 644
-rw-r--r-- 1  501 games       49 .env.supabase-password   ← 644
```

`.env.admin-password`, `.env.local` e `.env.supabase-password` são legíveis por qualquer usuário/processo do host e não deveriam existir no servidor de produção.

### 3.5 Borda (nginx) — cabeçalhos e limites observados

```
HTTP/2 200
server: nginx
content-type: text/html
cache-control: no-store
```

Ausentes: `Strict-Transport-Security`, `Content-Security-Policy`, `X-Content-Type-Options`, `Referrer-Policy`, `X-Frame-Options`/`frame-ancestors`, `Permissions-Policy`.

Configuração (`/etc/nginx/conf.d/livemonitortk.conf`, **idêntica** ao submódulo `edge-nginx/`):

- `server_tokens off` ✅
- `location ~ ^/(api|events)` → `proxy_pass http://backend:3001`, `proxy_buffering off`, `proxy_read_timeout 3600s`, `proxy_send_timeout 3600s`
- **Sem** `limit_req`, **sem** `limit_conn`, **sem** `client_max_body_size`
- `root /var/www/livemonitortk` com `try_files $uri /index.html`
- Estáticos com `Cache-Control: public, max-age=3600` para `.js|.css|.png|.svg|.json`

### 3.6 Endpoints públicos verificados

| Endpoint (sem credencial) | Resultado |
|---|---|
| `/api/state` | `401` ✅ |
| `/api/admin/users` | `401` ✅ |
| `/api/pix/tickets` | `401` ✅ |
| `/api/readiness` | `200` → `{"goroutines":12,"ready":true,"sseClients":0}` — expõe métricas internas sem autenticação |
| `/api/auth/config` | `200` → `{"enabled":true,"lockoutMinutes":15,"maxLoginAttempts":5,"provider":"local",...}` |

## 4. Achados preliminares já confirmados (antes das trilhas de review)

| ID | Sev | Achado | Evidência |
|---|---|---|---|
| P1 | **Crítica** | RLS inoperante: 29 tabelas com RLS habilitado e **zero políticas**; `force=false`; usuário da aplicação é superuser com `BYPASSRLS`. Documentação (`AGENTS.md`) afirma existir "RLS default deny". | `pg_policies = 0`, `relforcerowsecurity=false`, `tlm|t|t|t|t` |
| P2 | **Alta** | Arquivos de segredo com permissão `644` em `/opt/tiktok-live-monitor` (`.env.admin-password`, `.env.local`, `.env.supabase-password`) legíveis por qualquer processo do host. | `ls -l` (3.4) |
| P3 | **Alta** | Borda sem nenhum cabeçalho de segurança: sem HSTS (SSL-strip no primeiro acesso), sem CSP (amplifica qualquer XSS do frontend), sem `frame-ancestors` (clickjacking do painel e do `/admin`). | `curl -sSI` (3.5) |
| P4 | **Alta** | Borda sem `limit_req`/`limit_conn`/`client_max_body_size` enquanto `/events` mantém `proxy_read_timeout 3600s`: DoS por conexões SSE e por corpo grande depende apenas do lockout do backend. | nginx conf (3.5) |
| P5 | **Média** | Container residual `tlm-auth-local-candidate` (`tlm-backend:auth-local-20261003`) rodando há 28 h na rede `tiktok-live-monitor_default` (IP `172.18.0.7`), com pool de conexões aberto ao banco, executando build antiga. | `docker ps` + `docker inspect` |
| P6 | **Baixa** | `/api/readiness` sem autenticação expõe contagem de goroutines e de clientes SSE. | `curl` (3.6) |
| P7 | **Média** | Todos os containers rodam como root, sem `read_only` rootfs e sem `cap_drop`, incluindo `postgres` e a API exposta. | `docker inspect` (3.2) |
| P8 | **Informativo** | Boa prática já correta: nenhuma porta interna publicada; `.env` com `600`; `/api/*` responde `401` sem sessão; `server_tokens off`. | (3.1–3.6) |

## 5. Ledger de remediação — auditoria anterior (`.kilo/plans/1788131684202-seguranca-remediacao.md`)

O repositório contém um plano de remediação de uma auditoria **anterior**. A verificação abaixo compara cada achado daquele plano com o código do HEAD atual (`6d6e3a0`). Serve para não repetir trabalho e para expor regressões.

| # | Achado original | Situação no HEAD | Evidência |
|---|---|---|---|
| 1 | Chave de API Blackbox vazada no histórico | **NÃO REMEDIADO — e ainda exposto** | `.blackboxcli/settings.json` continua no histórico (`ec5a703`, `4991b3a`). Pior: a chave `sk-vT8X5HWOY1UfOoxYtsPkrA` está **em texto claro no próprio HEAD**, em `.kilo/plans/1788131684202-seguranca-remediacao.md:14,36`. O rewrite de histórico planejado nunca foi executado. |
| 2 | `feedback.db` (SQLite, ~1,5 MB, dados reais de participantes) versionado | **PARCIAL** | Nenhum `.db` rastreado no HEAD ✅, mas o arquivo continua em **todo o histórico** (`6df6321`, `61265f6`, `e909f28`, ...). PII de terceiros (nicknames, uniqueIDs, comentários, gifts) permanece recuperável por qualquer clone. |
| 3 | Bypass de lockout via `X-Forwarded-For`/`X-Real-IP` | **CORRIGIDO** ✅ | `internal/auth/lockout.go:172` `ClientIP` só confia em headers se `RemoteAddr` estiver em `TRUSTED_PROXIES` (`ProxyTrust`), e percorre o XFF da direita para a esquerda ignorando proxies confiáveis. Implementação correta. |
| 4 | Servidor HTTP sem timeouts (Slowloris) | **CORRIGIDO** ✅ | `internal/view/server.go:230-238`: `ReadHeaderTimeout 5s`, `ReadTimeout 15s`, `WriteTimeout 30s`, `IdleTimeout 60s`. |
| 5 | Token de acesso em `?access_token=` | **CORRIGIDO** ✅ | `internal/auth/auth.go:63-73`: comentário explícito de rejeição; `TokenFromRequest` aceita apenas `Authorization: Bearer` ou o cookie de sessão. |
| 6 | Enumeração de usuários no `GET /api/auth/login` | **CORRIGIDO** ✅ | `view/auth_handlers.go:44-58`: resposta genérica (`locked: false`, limites configurados), sem consultar lockout de e-mail arbitrário. |
| 7 | Ausência de rate limiting global | **NÃO REMEDIADO** | O backend não tem limitador algum (nenhum `x/time/rate` ou middleware equivalente). Na borda, `edge-nginx/nginx/conf.d/00-map.conf:10-12` **define** as zonas `sigmenta_api_auth (1r/s)` e `sigmenta_api_general (20r/s)`, e `sigmenta.conf:40,53` as **aplica** — mas `livemonitortk.conf` **não usa nenhuma**. A mitigação já existe no host; só não está ligada neste vhost. |
| 8 | JWT sem validação de `aud`/`iss` | **OBSOLETO** | A autenticação migrou para local (Argon2id + sessões no Postgres); não há mais JWT do Supabase. |
| 9 | Erros internos de banco expostos via `writeError(..., err.Error())` | **PARCIAL** | 20 ocorrências nos handlers. Amostra: a maioria são erros de validação (`CreateGoal` em `handlers.go:622`, `ReplacePixValues` em `handlers_pix.go:418`) — baixo impacto; mas `auth_handlers.go:187` devolve `auth.ErrDuplicateSignup` ao cliente (enumeração de conta). |
| 10 | Bugs de corretude (`GetSetting`, `LiveFirstSeen`) | **OBSOLETO** | Código do banco SQLite legado removido; hoje há `internal/database/postgres.go`. |

### 5.1 Proteções adicionais identificadas (não estavam no plano anterior)

- `http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)` em `view/handlers.go:729` — limite de corpo **existe no backend** (a borda não define `client_max_body_size` para este vhost).
- Verificação de `Origin` contra `SITE_URL` em métodos não-GET, com exceção do webhook (`internal/auth/auth.go:86-90`), somada a `SameSite=Lax` e ao token CSRF por sessão.
- CSP explícita **apenas** na rota de mídia da fila PIX (`handlers_pix.go:366`), ausente em todas as demais rotas e na borda.

### 5.2 Ação imediata recomendada (independente do relatório final)

1. **Revogar a chave `sk-...KrA`** no provedor — o histórico antigo permanece recuperável por qualquer fork/clone, então revogação é a única mitigação definitiva.
2. **Redigir a chave** de `.kilo/plans/1788131684202-seguranca-remediacao.md` no HEAD.
3. **Permissões 644 → 600** nos `.env.*` da VPS e remoção dos arquivos obsoletos.
4. Ligar `limit_req` no vhost `livemonitortk` reutilizando as zonas já definidas.

## 6. Refutações e itens verificados como não-problema

Registrado para evitar trabalho desnecessário — cada item abaixo foi testado e **não** é vulnerabilidade.

| Item | Verificação | Veredito |
|---|---|---|
| `persistent_lockout.go` interpola `time.Duration.String()` (`"15m0s"`) em `$3::interval` (levantado como possível bug que travaria todos os logins) | `psql -tAc "SELECT '15m0s'::interval"` → `00:15:00` | **FALSO ALARME** — o parser de `interval` do Postgres aceita o formato compacto do Go. Lockout persistente funciona. |
| `cmd/promote-admin` citado como ferramenta de promoção | `ls -la backend/cmd/promote-admin/` | Diretório **vazio** (só `.`/`..`). Não há código a auditar; a promoção a `admin` hoje só ocorre por import ou SQL direto. Referências na documentação estão obsoletas. |
| `InsecureSkipVerify` em SMTP (`semgrep` G402) | `mail.go:94`: `os.Getenv("SMTP_INSECURE_SKIP_VERIFY") == "1"` | **FALSO POSITIVO** — desligado por padrão, exige config explícita. |
| `math/rand` em `monitor/bridge.go` | Uso é jitter de reconexão | **FALSO POSITIVO** — sem valor de segurança. |
| Cookie de sessão sem `Secure` (`semgrep`) | `auth_handlers.go:140` + `SITE_URL=https://...` no `.env` de produção | **FALSO POSITIVO** — `Secure=true` em produção. |
| 21 CVEs de stdlib apontadas pelo `govulncheck` | Buildinfo do binário de produção: **go1.26.8** | **NÃO SE APLICAM** — todas corrigidas até 1.26.6. `html/template` sequer é importado. |
| XSS de template (`html/template`) | `grep -rn "html/template" backend/` → vazio | **NÃO SE APLICA**. |
| `frontend/dist/` desatualizado quebraria produção | `deploy/publish-frontend.sh:7` publica `$ROOT/frontend` (a fonte); `build.mjs` gera `dist/`, que está no `.gitignore` e não é lido por nenhum deploy | **HIPÓTESE REFUTADA** — produção serve a fonte; `dist/` é artefato morto. |
| IDOR / injeção SQL no backend | L3 revisou todo handler com ID por path/query e todas as queries | **Nenhum encontrado** — filtro `org_id` consistente; queries parametrizadas. |
| XSS explorável no frontend | L5 classificou os 36 `innerHTML` (todos em `renderer.js`; `pix.js`/`admin.js`/`teams-ui.js` usam `textContent`) | **Nenhum sink com dado de atacante** — não explorável. |
| Bypass de HMAC no webhook WAHA | L4 revisou `whatsapp/verify.go` | **Correto** — `hmac.Equal`, corpo raw, segredo obrigatório, falha fechada. |
| SSRF no download de mídia do WAHA | L4 revisou `whatsapp/client.go:154,171-178` | **Correto** — compara scheme+host com `WAHA_URL` e recusa redirect para host estranho. |
| Path traversal em `/api/pix/media/` e no dev server | L4 e L5 | **Correto** — key gerada pelo servidor, `id` parseado como int64, escopo por org; `..` bloqueado no `server.js`. |
| Open redirect por `\` no `next` do login | `node -e "new URL('/\\evil.com','https://livemonitortk.com.br')"` → `https://evil.com/` | **CONFIRMADO** — o parser WHATWG dos browsers (que o Node implementa) resolve para o host do atacante, e a validação atual aceita. |
| `decodeURIComponent` sem try/catch no dev server | `node -e "decodeURIComponent('%')"` → `URIError: URI malformed` | **CONFIRMADO** — `/%` derruba o dev server (não tratado). |

## 7. Verificação dinâmica executada (Fase 5)

| Teste | Comando | Resultado |
|---|---|---|
| Suíte de testes do backend | `go test -v ./internal/... -count=1 -timeout 300s` | **PASS 99 · SKIP 146 · FAIL 0** — verde, porém 146 testes foram pulados por ausência de `TEST_DATABASE_URL` (7 arquivos exigem Postgres descartável). Os testes de isolamento multi-org (`org_isolation_test.go`, `integration_test.go`) estão **entre os pulados**: a conclusão "sem IDOR" apoia-se em revisão estática, não em testes executados. Ver `RELATORIO.md` §6.1. |
| Semântica de URL (open redirect) | `node -e` com `new URL('/\\evil.com', ...)` | `https://evil.com/` — confirmado (ver §6) |
| Tratamento de percent-encoding | `node -e` com `decodeURIComponent('%')` | `URIError` — confirmado (ver §6) |
| Cabeçalhos por tipo de recurso | `curl -sSI` em 5 paths | HSTS presente só em `/api/*` (ver §3.5 e P3-ALT) |
| Host header forjado | `curl -sSI -H 'Host: evil.example' http://143.95.162.200/` | `Location: https://evil.example/` (V-01) |
| Host/SNI arbitrário | `curl -sk -H 'Host: outro.example' https://143.95.162.200/` | `200` com o `index.html` completo (V-03) |
| Parse de `interval` do lockout persistente | `psql -tAc "SELECT '15m0s'::interval"` | `00:15:00` — falso alarme descartado |
| Buildinfo do binário de produção | `go version -m` sobre o binário extraído do container | `go1.26.8`, `x/crypto v0.55.0`, `x/net v0.58.0` |
| Caça a segredos no histórico | `git log -G`, `git rev-list --objects`, `git cat-file -p` | Chave Blackbox em 2 commits + HEAD; `feedback.db` em 19 commits |

**Limites desta fase:** os testes de carga (DoS de SSE/webhook/`/api/report`) e a varredura de CVE das imagens não foram executados — dependem do Docker daemon local (parado). Nenhuma exploração ativa foi conduzida contra a VPS.



