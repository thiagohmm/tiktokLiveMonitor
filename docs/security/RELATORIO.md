# Relatório de Auditoria de Segurança — TikTok Live Monitor

**Alvo:** `github.com/thiagohmm/tiktok-live-monitor` @ `6d6e3a0` (branch `lite-sem-ia`) + VPS de produção `143.95.162.200`
**Data:** 2026-10-05
**Escopo aprovado:** repositório (estático) + verificação read-only da VPS. Auditoria completa (fases 0–6 do plano).
**Método:** varreduras automatizadas (`golangci-lint`+`gosec`, `govulncheck`, `gitleaks`, `semgrep`), 6 trilhas de review manual delegadas a `deepseek/deepseek-v4-pro` (L1–L6), consolidação independente (L9), e **verificação empírica** dos achados de maior impacto (curl com Host forjado, `psql` read-only, `docker inspect`, `node` para semântica de URL, `go version -m` no binário de produção).
**Restrição respeitada:** nenhuma escrita, reinício ou exploração ativa contra a VPS.
**Backlog completo:** `docs/security/achados.csv` (44 achados).

---

## 1. Sumário executivo

**Nenhum comprometimento ativo foi encontrado na lógica da aplicação.** Não há bypass de autenticação, não há IDOR entre organizações, não há injeção SQL e não há XSS explorável nos caminhos atuais. O núcleo de autenticação é sólido e follow OWASP em vários pontos.

Os riscos materiais estão **fora do código de negócio**: segredos no repositório, configuração de infraestrutura e ausência de cabeçalhos de segurança na borda. Dois achados são críticos, e **ambos já estavam identificados numa auditoria anterior que nunca foi remediada**.

| # | Severidade | Achado | Status |
|---|---|---|---|
| S-01 | **Crítica** | Chave de API Blackbox (com integração GitHub e execução remota de código) exposta no histórico **e ainda em texto claro no HEAD** | Verificado |
| P1-CRIT | **Crítica** | RLS "default deny" documentado **não existe**: 29 tabelas com RLS ligado, **zero políticas**, aplicação conecta como superuser com `BYPASSRLS` | Verificado |
| L2-2 | Alta | `TRUSTED_PROXIES=172.16.0.0/12` confia em **todos** os containers do host → lockout de login forjável | Verificado |
| S-02 | Alta | `feedback.db` com dados pessoais de participantes em **19 commits** | Verificado |
| P3-ALT | Alta | HSTS **suprimido** em toda a UI (`/`, `/admin`, `/login`) por herança de `add_header`; sem CSP/X-Frame-Options/Referrer-Policy | Verificado empiricamente |
| P4-ALT | Alta | Nenhum `limit_req`/`limit_conn` no vhost, embora as zonas já existam no host (usadas pelo produto irmão) | Verificado |
| L2-1 | Alta | Enumeração de e-mail via `POST /api/auth/signup` (409 "e-mail já cadastrado") | Verificado |
| P2-ALT | Alta | `.env.admin-password`, `.env.local`, `.env.supabase-password` com permissão **644** no host de produção | Verificado |

> **A meta-observação mais importante:** `docs/../.kilo/plans/1788131684202-seguranca-remediacao.md` documenta uma auditoria anterior com 10 achados e um plano de remediação em 4 fases (incluindo revogação da chave e reescrita do histórico). Nada disso foi executado — e o próprio documento passou a ser uma nova fonte de exposição do segredo. **A falha de processo é o achado mais grave deste relatório.**

---

## 2. Veredito por área

| Área | Veredito | Base |
|---|---|---|
| Autenticação e sessão | **Sólido** | Argon2id m=64 MiB/t=3/p=4, comparação constant-time, equalização de timing para e-mail inexistente, sessões revogáveis, token de reset de uso único com `UPDATE ... RETURNING` atômico, cookie HttpOnly/Secure/SameSite=Lax, fail-closed de config |
| Autorização / multi-tenant | **Correto na aplicação, sem backstop no banco** | Todos os handlers com ID escopam por `org_id`; nenhum IDOR encontrado **em revisão estática**. Atenção: o harness de isolamento existente (`internal/database/org_isolation_test.go`, `internal/view/integration_test.go`) **não foi executado** — ver §6.1 |
| Entrada e injeção | **Correto** | Queries 100% parametrizadas; `fmt.Sprintf` em SQL só com listas constantes/`quoteIdent`; corpo limitado a 1 MiB por `MaxBytesReader` |
| Frontend | **Correto, com ressalvas** | `pix.js`/`admin.js`/`teams-ui.js` usam `textContent`; os 36 `innerHTML` de `renderer.js` são constantes ou escapados. Dois redirects abertos (login e borda) |
| Integrações (WAHA/MinIO/SMTP/TikTok) | **Bem implementado** | HMAC-SHA512 em tempo constante sobre corpo raw, falha fechada; proteção de SSRF no download de mídia; keys de objeto geradas pelo servidor; sem injeção de header em e-mail pela API |
| Borda e infraestrutura | **Fraco** | Sem CSP/HSTS efetivo/XFO/RP, sem rate limit, containers root, segredos em `environment:`, `pg_hba local trust`, backup só manual |
| Cadeia de suprimentos | **Bom, com risco de deriva** | Produção em Go 1.26.8 (sem CVEs conhecidas); mas `go.mod` em 1.26.2 e imagem de build com tag flutuante |
| Segredos e histórico git | **Crítico** | Ver S-01/S-02 |

### 2.1 Correção de hipóteses iniciais

Vale registrar o que **não** se confirmou, para evitar trabalho desnecessário (detalhamento em `00-baseline.md` §6):

- **H1 (RLS decorativa)** — confirmada, e pior do que o suposto: não há nenhuma política, e não apenas um filtro ausente.
- **H2/H3 (IDOR e bypass de papel)** — **refutadas**: nenhuma rota aceita ID de outra organização; todas as rotas admin sob prefixo de `tenantExempt` chamam `RequireAdmin` hoje.
- **H6 (XSS)** — **refutada**: `pix.js` **não usa** `innerHTML` (minha hipótese inicial estava errada); dados de atacante passam por `textContent`/`createElement`.
- **H7 (`dist/` divergente quebrando produção)** — **refutada**: `deploy/publish-frontend.sh` publica a **fonte**; `frontend/dist/` é artefato morto (gitignored, não lido por nenhum deploy).
- **H14 (dependências vulneráveis)** — **majoritariamente refutada**: as 21 CVEs de stdlib apontadas pelo `govulncheck` são da toolchain **local** (1.26.2); o binário em produção é **1.26.8** e não é afetado. `html/template` sequer é importado.
- **`gitleaks` deu verde com um segredo real presente no HEAD** — lacuna de ruleset documentada em `raw/gitleaks-summary.md`.

---

## 3. Achados críticos e altos (detalhamento com evidência)

### S-01 — Chave de API Blackbox exposta (Crítica)

Blob `57ee1372` de `.blackboxcli/settings.json` (commits `ec5a703` e `4991b3a`):

```json
{"mcpServers":{"remote-code":{"httpUrl":"https://cloud.blackbox.ai/api/mcp",
 "headers":{"Authorization":"Bearer sk-vT8X5HWOY1UfOoxYtsPkrA"}, "description":"...Remote execution platform...
  automates coding tasks on your GitHub repositories... GitHub Integration: Manage your GitHub token connections..."}}}
```

E **a mesma chave está no HEAD**, em texto claro, num arquivo rastreado:

```console
$ git show HEAD:.kilo/plans/1788131684202-seguranca-remediacao.md | grep -n 'sk-vT8'
14: ... contém `Authorization: Bearer sk-vT8X5HWOY1UfOoxYtsPkrA`.
36: 1. **Revogar a chave Blackbox** ... (`sk-vT8X5HWOY1UfOoxYtsPkrA`)
```

**Impacto:** o serviço credenciado é um MCP de execução remota de código com integração ao GitHub — nas mãos de terceiros permite executar código em sandbox, criar branches/commits/PRs e manipular tokens do GitHub. É comprometimento de **cadeia de desenvolvimento**, não um vazamento comum.

**Correção:** revogar a chave (única mitigação definitiva — clones/fork já existentes retêm o histórico), redigir as duas linhas do HEAD, e só então reescrever o histórico.

### P1-CRIT — RLS inoperante (Crítica)

```console
$ docker exec ... psql -U tlm -tAc "select rolname,rolsuper,rolbypassrls,rolcreatedb,rolcreaterole from pg_roles where rolname='tlm'"
tlm|t|t|t|t

$ ... psql -tAc "select c.relname||' rls='||c.relrowsecurity||' force='||c.relforcerowsecurity ..."
anomaly_logs rls=true force=false ... (29 tabelas, todas iguais)

$ ... psql -tAc "select count(*) from pg_policies"
0
```

`AGENTS.md` afirma que o backend cria "RLS default deny". Na prática: **nenhuma política existe**, `FORCE ROW LEVEL SECURITY` está desligado, e o usuário da aplicação é superuser com `BYPASSRLS`. O comentário no próprio código (`postgres.go:300-321`) reconhece a dependência.

**Impacto:** não há segunda camada. Qualquer falha futura de filtro `org_id`, ou qualquer SQLi, ou qualquer container que alcance `postgres:5432` com a `DATABASE_URL` lê/grava dados **de todas as organizações**. Com `rolcreatedb`/`rolcreaterole`, o comprometimento escala para o cluster.

**Correção:** criar role de aplicação sem `SUPERUSER`/`BYPASSRLS`/`CREATEDB`/`CREATEROLE` (papel elevado separado para as migrações de boot) e materializar o isolamento em políticas `USING (org_id = current_setting('app.org_id'))` com `FORCE ROW LEVEL SECURITY`. Alternativa mínima aceitável: remover `BYPASSRLS` e criar políticas; ou, se o modelo for mantido, **remover a alegação de "default deny" da documentação** — hoje ela dá falsa segurança a quem lê.

### L2-2 — `TRUSTED_PROXIES` confia em todos os containers do host (Alta)

```console
$ grep '^TRUSTED_PROXIES' /opt/tiktok-live-monitor/.env
TRUSTED_PROXIES=172.16.0.0/12

$ docker network inspect <cada rede>  →  subnets
bridge 172.17.0.0/16 · tiktok-live-monitor_default 172.18.0.0/16 · prontuario 172.19.0.0/16
tlm_minio 172.20.0.0/16 · vps-edge-nginx_default 172.21.0.0/16 · sigmenta_edge 172.30.0.0/24 · sigmenta_data 172.31.0.0/24
```

`172.16.0.0/12` cobre `172.16.0.0–172.31.255.255`: **todas** as redes do host. `ClientIP` (`lockout.go:172-205`) honra `X-Forwarded-For` de qualquer peer confiável, e o backend (`172.18.0.4`) é alcançável por container-to-container.

**Caminho de ataque:** qualquer container na rede da aplicação — incluindo o órfão `tlm-auth-local-candidate` (`172.18.0.7`) — envia `X-Forwarded-For` arbitrário e **rotaciona o IP a cada tentativa**, zerando a chave `email|ip` do lockout. Combinado com a ausência de teto global por conta (`L2-4`), viabiliza força bruta online contra uma conta conhecida.

**Correção:** `TRUSTED_PROXIES=172.18.0.5/32` — o IP do `vps-edge-nginx` na rede da aplicação, único hop legítimo. Uma linha.

### P3-ALT — HSTS suprimido na UI inteira (Alta)

Medido em produção, com caso-controle no mesmo servidor:

| Path | HSTS |
|---|---|
| `/`, `/admin`, `/login`, `/config.js` | ❌ ausente |
| `/api/readiness` | ✅ presente |

**Causa-raiz:** no nginx, `add_header` **não herda** do nível `server` quando o `location` declara o seu próprio. O HSTS está em `ssl-params.conf:3` (nível server), mas os locations de HTML/estáticos declaram `add_header Cache-Control` (`livemonitortk.conf:71,76,81,86,90,94`) e o perdem. O location `/api` não declara `add_header` → herda e funciona.

**Impacto:** exatamente `/login` e `/admin` — as páginas que mais precisam — são servidas sem HSTS (SSL-strip no primeiro acesso). Sem CSP, qualquer XSS futuro executa sem barreira; sem `frame-ancestors`/XFO, há UI-redress em `/login`.

**Correção:** o mesmo repositório já tem a solução — `sigmenta.conf:69-72` reaplica CSP/nosniff/Referrer-Policy/XFO dentro dos locations que usam `add_header`. Copiar o padrão.

### P4-ALT — Sem rate limit na borda (Alta)

`00-map.conf:10-12` **define** `sigmenta_api_auth` (1r/s) e `sigmenta_api_general` (20r/s), e `sigmenta.conf:40,53` as **aplica** — mas `livemonitortk.conf` não usa nenhuma. Combinado com `proxy_read_timeout 3600s` em `/api` e `/events` e a rota pública de webhook (`L4-1`), não há barreira de borda contra brute-force, flood de SSE ou abuso do webhook.

### S-02 — Dados pessoais no histórico git (Alta)

`feedback.db` (1.589.248 bytes, SQLite) com `uniqueId`, nicknames, mensagens e gifts de participantes reais, presente em **19 commits**. Hoje nenhum `.db` é rastreado no HEAD, mas o conteúdo permanece recuperável em qualquer clone — incluindo dados de pessoas que nunca consentiram com isso (LGPD).

### L2-1 — Enumeração de e-mail no signup (Alta)

`view/auth_handlers.go:186-188` devolve `409` com `err.Error()` (`"e-mail já cadastrado"`) apenas quando o e-mail existe — oráculo direto de existência, contrariando a postura anti-enumeração já adotada em `login` e `recover`. Correção trivial: resposta genérica 201/202.

### P2-ALT — Segredos com 644 no host (Alta)

```
-rw-------  .env                      ← correto
-rw-r--r--  .env.admin-password       ← 644
-rw-r--r--  .env.local                ← 644
-rw-r--r--  .env.supabase-password    ← 644
```

Legíveis por qualquer usuário/processo do host. São artefatos de produtos já desligados (Vercel/Supabase), mas contêm senha de admin e um `VERCEL_OIDC_TOKEN` — devem ser removidos **e** os segredos rotacionados.

---

## 4. Achados médios e baixos (resumo)

Os 37 restantes estão detalhados em `achados.csv`. Destaques:

**Endurecimento de autenticação:** lockout sem teto global por conta (`L2-4`); sucesso de signup/recover contado como falha, amplificando DoS (`L2-5`); CSRF **determinístico** derivado do token de sessão, sem rotação real, válido por 7 dias (`L2-6`); ponte legada devolvendo link de ativação no corpo + side-channel de timing de 12 s (`L2-7`); `/api/auth/reset-password` sem rate limit e sem checar `Enabled` (`L2-8`).

**Integrações:** webhook WAHA público sem rate limit, com custo de HMAC por requisição (`L4-1`); replay de comprovante que re-baixa e re-grava mídia **antes** de detectar a duplicata (`L4-2`); SSE sem teto por organização e `authorize()` indo ao banco a cada write/ping (`L4-3`); PII e conteúdo de chat gravados em log sem redação (`L4-4`).

**Borda:** open redirect por `Host` forjado, verificado — `curl -H 'Host: evil.example' http://143.95.162.200/` devolve `Location: https://evil.example/` porque o vhost é o `default_server` de :80 e usa `$host` no 301 (`V-01`); versão exata do nginx exposta só no HTTP (`V-02`); a UI é servida sob Host/SNI arbitrário (`V-03`).

**Frontend:** open redirect por `\` no parâmetro `next` do login — **verificado por execução** (`new URL('/\evil.com','https://livemonitortk.com.br')` → `https://evil.com/`, mesma semântica WHATWG dos browsers); dev server serve `.env.local` por não filtrar dotfiles (`L5-2`); `decodeURIComponent('%')` sem try/catch derruba o dev server — verificado (`L5-3`).

**Infraestrutura:** todos os containers como root, sem `read_only`/`cap_drop`/`no-new-privileges` (`P7-MED`); segredos por `environment:` em vez de `secrets:` (`L6-6`); `WAHA_API_KEY` com default vazio, permitindo WAHA sem autenticação (`L6-7`); `pg_hba local all all trust` (`L6-11`); backup apenas manual, no mesmo disco (`L6-12`); container órfão `tlm-auth-local-candidate` na rede da aplicação (`P5-MED`).

**Build:** `go.mod` em 1.26.2 enquanto a produção roda 1.26.8 — qualquer build local reintroduz 14 vulnerabilidades de stdlib alcançáveis (`G-01`); `FROM golang:1.26-bookworm` sem pin, build não reprodutível (`G-02`).

**Autorização (defesa em profundidade):** escritas PIX e operacionais dependem exclusivamente do whitelist do middleware `operatorReadPath`, sem checagem de papel no handler (`L3-3`); `tenantExempt` libera rotas admin por **prefixo**, não por rota exata (`L1-3`).

---

## 5. Refutações (testado e descartado — não gerar trabalho)

| Item levantado | Teste realizado | Veredito |
|---|---|---|
| `persistent_lockout` interpola `"15m0s"` em `::interval` (travaria todos os logins) | `psql: SELECT '15m0s'::interval` → `00:15:00` | **Falso alarme** |
| 21 CVEs de stdlib (`govulncheck`) | `go version -m` no binário de produção → **go1.26.8** | **Não se aplicam** (corrigidas até 1.26.6) |
| XSS de template | `grep html/template backend/` → vazio | Não se aplica |
| Cookie sem `Secure` (semgrep) | `SITE_URL=https://…` no `.env` real | Falso positivo |
| `InsecureSkipVerify` em SMTP (semgrep) | Env-gated, default off | Falso positivo |
| `math/rand` (semgrep) | Jitter de reconexão | Falso positivo |
| `frontend/dist/` desatualizado quebraria produção | `publish-frontend.sh:7` publica a fonte | Hipótese refutada — `dist/` é morto |
| IDOR cross-org / SQL injection | L3 revisou todo handler com ID e todas as queries | Nenhum encontrado |
| XSS explorável | L5 classificou os 36 `innerHTML` | Nenhum com dado de atacante |
| Bypass de HMAC do webhook · SSRF na mídia · traversal em `/api/pix/media/` e no dev server | L4 e L5 | Corretos |
| `DefaultOrgID` como bypass de tenant | L3 | Não explorável |
| `cmd/promote-admin` (citado no plano) | `ls` → diretório vazio | Ferramenta não existe |

---

## 6. Pendências de verificação (não confirmadas)

### 6.1 A suíte de testes passou, mas 146 testes foram PULADOS

```
cd backend && go test -v ./internal/... -count=1 -timeout 300s
→ PASS: 99   SKIP: 146   FAIL: 0
```

Motivo: 7 arquivos de teste exigem `TEST_DATABASE_URL` (PostgreSQL descartável) e chamam `t.Skip` quando a variável está ausente (`internal/database/database_test.go:17`, `tenant_migration_test.go:19`, `internal/auth/local_test.go`, `internal/view/integration_test.go`, `internal/monitor/monitor_test.go`, `internal/controller/goals_test.go`, `internal/teams/store_test.go`). O Docker daemon local está parado, então não havia banco disponível.

**Consequência para a leitura deste relatório:** a conclusão de que **não há IDOR** se apoia em **revisão estática** (L1 + L3), não em testes executados. Exatamente os testes que a corroborariam foram pulados:

- `TestOrgIsolationReads`, `TestOrgIsolationClearAndDelete`, `TestOrgIsolationTargetGiftMutations`, `TestOrgIsolationListLives`, `TestOrgSameLiveNameSeparateSessions` (`internal/database/org_isolation_test.go`)
- `TestOrganizationMembership`, `TestOrgRequired`, `TestOrganizationValidation`
- `TestAssignLegacyLivesByNameMovesSessionsAndEvents`, `TestAssignLegacyLivesRefusals`
- `internal/view/integration_test.go` (1.262 linhas de harness de integração)

Isso **reforça** a prioridade do reteste: rodar esses testes contra um Postgres descartável é o caminho mais curto para converter "não encontrei IDOR" em "está provado que não há". O `reteste.md` §2.1 já exige isso.

### 6.2 Demais pendências

1. **Regressão de RLS (P1-CRIT)** — exige role não-superuser contra Postgres descartável; não avaliável em modo read-only. Deve ser validada pelos mesmos `TestOrgIsolation*` após a troca de papel.
2. **`TRUSTED_PROXIES`** — valor real confirmado (`/12`); falta validar que `172.18.0.5/32` mantém o IP real dos clientes após a troca.
3. **Carga de DoS** (SSE, webhook, `/api/report`) — não medida; exigiria ambiente local com dados sintéticos.
4. **Varredura de CVE das imagens** (`trivy`) — bloqueada pelo Docker daemon local parado; exigiria baixar imagens no host de produção, o que requer autorização.

---

## 7. Ordem de correção recomendada

**Passo 0 — operacional imediato (sem código, ~10 min):**
1. **Revogar a chave Blackbox** no provedor (S-01) — antes de qualquer outra coisa.
2. `chmod 600` nos três `.env.*` e rotacionar os segredos que contêm (P2-ALT).
3. `docker rm -f tlm-auth-local-candidate` (P5-MED).

**Passo 1 — configuração de borda e auth (baixo esforço, alto impacto):**
4. `TRUSTED_PROXIES=172.18.0.5/32` + alerta de boot (L2-2).
5. Reaplicar HSTS/CSP/XFO/RP nos locations com `add_header`, copiando `sigmenta.conf` (P3-ALT).
6. Ligar `limit_req`/`limit_conn` no vhost reutilizando as zonas existentes (P4-ALT).
7. Resposta genérica no signup (L2-1).
8. Corrigir o open redirect do `$host` na borda (V-01) e o do `next` no login (L5-1).

**Passo 2 — higiene de segredos e histórico:**
9. Redigir a chave no HEAD e reescrever o histórico (`filter-repo`/BFG) purgando `.blackboxcli/` e `feedback.db` (S-01, S-02), com coordenação de force-push.
10. `secrets:` no Compose, guarda do `WAHA_API_KEY` (L6-6, L6-7).

**Passo 3 — defesa em profundidade (maior esforço):**
11. Role de aplicação sem privilégios + políticas RLS por `org_id` (P1-CRIT) — **com teste de regressão escrito antes** da troca de papel, para não derrubar o acesso.
12. Endurecimento de auth: teto por conta, rotação real de CSRF, desligar a ponte legada, rate limit no reset (L2-4 a L2-8).
13. Rate limit do webhook + checagem de duplicata antes de baixar mídia; teto de SSE por org (L4-1 a L4-3).
14. `user:`/`read_only`/`cap_drop`/`no-new-privileges`; `pg_hba local ... scram`; backup offsite (P7-MED, L6-11, L6-12).
15. Alinhar `go.mod` a 1.26.8 e pinar a imagem de build (G-01, G-02).

**Passo 4 — prevenção contínua:**
16. CI com `go vet` + `go test -race` + `gosec` + `govulncheck` + `gitleaks` (**com regras customizadas** — o ruleset padrão falhou) e checagem de deriva de dependências.
17. Manter a regra do projeto: toda review de segurança via `deepseek/deepseek-v4-pro`.

---

## 8. Anexos — evidência

| Arquivo | Conteúdo |
|---|---|
| `docs/security/achados.csv` | 44 achados com severidade, confiança, evidência, verificação e correção |
| `docs/security/00-baseline.md` | Baseline do commit, reconhecimento read-only da VPS, ledger de remediação da auditoria anterior, refutações |
| `docs/security/reteste.md` | Roteiro de reteste com evidência antes/depois por achado |
| `docs/security/raw/golangci-lint.{json,txt}` | Varredura estática Go (24 issues, triadas) |
| `docs/security/raw/govulncheck.json` + `govulncheck-summary.md` | CVEs com veredito por alcançabilidade |
| `docs/security/raw/gitleaks.json` + `gitleaks-summary.md` | Varredura de segredos + auditoria manual do histórico |
| `docs/security/raw/semgrep.json` + `semgrep-summary.md` | Rulesets OWASP/golang/xss/secrets |
| `docs/security/audit-workflow.js` | Script das 6 trilhas de review (reproduzível) |
| `docs/plano-revisao-seguranca.md` | Plano original com escopo travado |
