# Plano — Verificação de Segurança (Backend + Frontend)

> Objetivo: produzir uma auditoria de segurança **reproduzível, com evidência** e um
> backlog priorizado de correções para o TikTok Live Monitor, cobrindo backend Go,
> frontend vanilla, banco/RLS, containers e borda (nginx).
>
> **Regra do projeto (AGENTS.md):** toda review de código/segurança/diff é delegada a
> subagente com o modelo `deepseek/deepseek-v4-pro` — ex.: `agent: "reviewer"`,
> `model: "deepseek/deepseek-v4-pro"`. Nenhuma trilha de review é executada no modelo
> da sessão.

---

## Escopo travado (aprovado pelo dono)

| Decisão | Escolha |
|---|---|
| Alcance | Repositório (estático) + **docker-compose local** (dinâmico) + **VPS read-only** |
| Profundidade | **Auditoria completa** — fases 0 a 6 |
| Entrega | `docs/security/RELATORIO.md` + `docs/security/achados.csv` + `docs/security/reteste.md` |
| Restrição inegociável | Nenhuma escrita, reinício ou exploração ativa contra a VPS. Somente leitura e requisições HTTP não autenticadas de observação. |
| Modelo das reviews | `deepseek/deepseek-v4-pro` (obrigatório, via `agent: "reviewer"`) |

**Progresso:**
- [x] Fase 0 — baseline e reconhecimento → `docs/security/00-baseline.md`
- [x] Fase 1 — varreduras automatizadas → `docs/security/raw/` (golangci-lint+gosec, govulncheck, gitleaks, semgrep), cada uma triada
- [x] Fase 2/3/4 — trilhas L1–L6 + consolidação L9 (deepseek-v4-pro) → `docs/security/raw/lanes-consolidado.*.md`
- [x] Fase 4b — verificação read-only da VPS (portas, privilégios, RLS, permissões, headers, containers residuais)
- [~] Fase 5 — verificação dinâmica: verificação empírica concluída (curl, psql read-only, node, `go version -m`); suíte `go test` e testes de carga pendentes de ambiente com Docker
- [x] Fase 6 — relatório, backlog e roteiro de reteste → `RELATORIO.md`, `achados.csv` (44 achados), `reteste.md`
- [ ] Correções e reteste dos achados Críticos/Altos (aguarda autorização de escrita em produção)

**Resultado:** 44 achados — 2 Críticos, 5 Altos, 22 Médios, 15 Baixos. Nenhum bypass de autenticação, IDOR, SQLi ou XSS explorável encontrado no código; os riscos materiais são segredos no repositório, configuração de infraestrutura e cabeçalhos de borda. A auditoria anterior (mesmo repositório) **nunca foi remediada**.

---

## 0. Escopo e superfície (levantado do repo)

| Camada | Artefato | Superfície relevante |
|---|---|---|
| Backend | `backend/` — 114 arquivos `.go`, ~30k LOC | API REST + SSE, um único `http.ServeMux` |
| Rotas | `backend/internal/view/server.go:101-158` (+ `registerTeamRoutes`) | ~60 rotas `/api/*`, `/events` |
| Middleware | `server.go:172-175` | cadeia `cors → auth.Middleware → tenantMiddleware → mux` |
| Auth | `backend/internal/auth/` (auth, local_store, local_session, lockout, persistent_lockout, identity, import, legacy_bridge, theme) | Argon2id, cookies HttpOnly, CSRF por sessão, lockout, recuperação de senha |
| Multi-tenant | `backend/internal/tenant/`, `backend/internal/view/tenant.go`, `internal/database/driver.go` (`orgSessions`) | isolamento por `org_id`, RLS default-deny |
| WhatsApp/WAHA | `backend/internal/whatsapp/`, `view/handlers_pix.go:453` | webhook HMAC (`X-Webhook-Hmac`), mídia em MinIO |
| Fila PIX | `view/handlers_pix.go` (581 linhas), `docs/fila-pix.md` | tickets, contatos, valores, mídia (`/api/pix/media/`) |
| Frontend | `frontend/*.js|.html` + cópia `frontend/dist/` | vanilla JS, 36 usos de `innerHTML`, `escapeHtml` presente |
| Dev server | `frontend/server.js` | servidor estático local com guarda de path traversal |
| Infra | `docker-compose{,.production,.raspberry}.yml`, `deploy/nginx`, submódulo `edge-nginx` | postgres, backend, waha, minio, nginx de borda (TLS) |
| Gates | — | **não há CI** de migração nem de segurança |

### Ativos a proteger
1. Sessões/credenciais de usuários e equipes (Argon2id hashes, tokens de sessão/CSRF).
2. Dados de monetização da Fila PIX (tickets, valores, comprovantes, contatos WhatsApp).
3. Isolamento entre organizações (multi-tenant) — vazamento cross-tenant é o pior caso.
4. Credenciais de infraestrutura (SMTP/Resend, WAHA, MinIO, Postgres, TikTok).
5. Disponibilidade do monitor (SSE + sessões de live) contra abuso/DoS.

### Fronteiras de confiança (trust boundaries)
- Internet → nginx de borda (TLS) → backend (`/api/*`, `/events`).
- Internet → **WAHA** (container próprio) → webhook interno `/api/webhooks/whatsapp`.
- Backend → Postgres (usuário `tlm`, **superusuário, ignora RLS**).
- Backend → MinIO (mídia PIX) e SMTP externo.
- Navegador → frontend vanilla (mesma origem via nginx) — sem framework, sem CSP conhecida.

---

## 1. Hipóteses iniciais a confirmar/refutar (do recon)

Priorizadas por impacto × probabilidade. Cada uma vira um item testável com evidência.

| # | Hipótese | Onde olhar |
|---|---|---|
| H1 | **RLS é decorativa**: o backend conecta como superusuário `tlm`, então o isolamento real depende de *toda* query filtrar `org_id`. Qualquer repo/query sem filtro = IDOR cross-tenant. | `internal/database/*.go`, todos os `WHERE org_id`, `view/tenant.go`, `docs/` |
| H2 | IDs de recursos (`/api/pix/tickets/{id}`, `/api/pix/media/{id}`, `/api/history/{id}`, `/api/admin/lives/session/delete`) resolvidos sem escopo de org. | `view/handlers_pix.go`, `view/handlers.go` |
| H3 | Rotas administrativas com verificação de papel incompleta/duplicada (`PlatformAdmin` vs `OrgRoleOwner`) e bypass via prefixo em `tenantExempt` (`view/tenant.go:75-80`). | `view/org_handlers.go`, `view/team_handlers.go`, `view/auth_handlers.go` |
| H4 | Webhook WAHA: `/api/webhooks/whatsapp` precisa ser isento de auth; validar HMAC em tempo constante, sobre o **raw body**, com fail-closed se o secret não estiver configurado. | `view/handlers_pix.go:437-460`, `whatsapp/` |
| H5 | `/api/pix/media/` serve objeto do MinIO: path/keys controláveis pelo usuário, sem checagem de org ⇒ exfiltração de comprovantes. | `view/handlers_pix.go`, `internal/media/` |
| H6 | XSS armazenado/refletido no frontend: comentários TikTok, nomes/uniqueIDs, categorias de infração, nomes de ticket e contatos entram em `innerHTML`. `escapeHtml` pode não cobrir todos os sinks (ou faltar em valores de atributo/URL). | `frontend/renderer.js`, `frontend/pix.js`, `frontend/admin.js`, `frontend/teams-ui.js` |
| H7 | Frontend `dist/` divergente do fonte (build manual): correção aplicada só em `frontend/*.js` pode não chegar em produção. | `frontend/dist` vs `frontend/` |
| H8 | SSE (`/events`) sem limite por usuário/org e sem verificação de origem ⇒ DoS de conexões e fuga de evento entre orgs. | `view/sse.go` (287 linhas), `publishSSE`, `sseWriteTimeout` |
| H9 | SSRF/abuso em endpoints que buscam dados externos (perfil TikTok, disponível-gifts, relatórios, importação de identidades via PDF). | `view/handlers.go:547`, `internal/report/`, `internal/receipt/`, `cmd/import-identities` |
| H10 | Segredos e artefatos locais versionados ou vazáveis (`.env.local`, `.env.admin-password`, `.env.supabase-password` existem em disco; `.env.*` está no `.gitignore` mas histórico pode conter). | `git log -p`, `gitleaks`, `.gitignore` |
| H11 | Hardening de containers: processo root, `--privileged`, portas expostas além do nginx, senhas em `environment:` do compose, volumes sem read-only, Postgres/MinIO/WAHA alcançáveis da internet. | `docker-compose*.yml`, `deploy/nginx`, `edge-nginx` |
| H12 | Headers de borda ausentes (HSTS, CSP, X-Content-Type-Options, Referrer-Policy, frame-ancestors) e TLS antigo. | `edge-nginx/nginx/conf.d/*.conf`, `deploy/nginx` |
| H13 | Logging/telemetria vazando dado sensível (e-mail, token, telefone, payload PIX) e ausência de auditoria de ações administrativas. | todos os `log.Printf` do backend |
| H14 | Dependências desatualizadas/vulneráveis (Go `pgx`, `golang.org/x/crypto`; vendored `jspdf`, `chart.js`). | `backend/go.mod`, `frontend/vendor/` |

---

## 2. Metodologia e trilhas

Execução em **lanes paralelas read-only** (nenhum lane escreve no repo), cada uma
delegada com `agent: "reviewer"` + `model: "deepseek/deepseek-v4-pro"`, contexto fresco
e entregável próprio. Síntese e priorização ficam no lane final de consolidação.

### Fase 0 — Preparação (evidência de base)
- Congelar baseline: `git rev-parse HEAD`, data, `git status --porcelain`.
- Registrar versões: Go toolchain, `go.mod`, imagens do compose (`docker compose config --images`).
- Inventário mecânico de rotas × middleware × exigência de auth/papel/org → tabela (base do teste de cobertura de authz).
- Saída: `docs/security/00-baseline.md` + `matriz-rotas.csv` (rota, método, auth, papel, escopo org, handler:linha).

**Gate:** matriz completa e revisada; nenhuma rota sem classificação.

### Fase 1 — Recon automatizado (barato, roda primeiro)
| Ferramenta | Alvo | Saída |
|---|---|---|
| `gosec` | backend Go | SARIF/JSON de padrões inseguros |
| `govulncheck` | deps Go + código alcançável | vulnerabilidades com caminho de chamada |
| `staticcheck`/`go vet` | backend | defeitos |
| `semgrep` (rulesets `p/golang`, `p/owasp-top-ten`, `p/xss`) | backend + frontend | achados por regra |
| `gitleaks` / `trufflehog` (histórico git completo) | repo | segredos vazados |
| `npm audit` / `osv-scanner` | vendor JS (jspdf, chart.js) | CVEs |
| `trivy`/`grype` (fs + images) | compose images | CVEs de base image + misconfig |
| `docker scout`/`hadolint` | Dockerfiles | hardening |
| `eslint` com `no-unsanitized` | frontend | sinks DOM inseguros |

**Disponibilidade local apurada:** `golangci-lint 2.11.4` (já cobre `gosec`/`govet`/`staticcheck`) e `uvx` estão presentes; `govulncheck` e `gitleaks` são executados via `go run` (autocontido, sem instalar nada). O **Docker daemon local está parado**, portanto `trivy`/`gitleaks`/`semgrep` por imagem ficam pendentes — rodar varredura de imagem na VPS exigiria baixar imagens no host de produção, o que **requer autorização explícita**. Não há `package.json`/lockfile no frontend, então `npm audit` é substituído por inspeção de versão dos arquivos vendor.

**Gate:** todo achado automatizado triado (verdadeiro positivo? alcançável? explorável?).

### Fase 2 — Review manual backend (por trilha)
- **B1 — Autenticação e sessão:** Argon2id (parâmetros, salt, timing), lifecycle de sessão, rotação/revogação, `SameSite=Lax` vs CSRF, `Secure`/`Domain` do cookie, `proxyTrust`/`X-Forwarded-For` (spoofing de IP no lockout — `auth.LoadProxyTrustFromEnv()`), enumeração de usuário, fluxo de recuperação de senha (token de uso único, expiração, binding, race), política de senha, signup/`AUTH_ENABLED=0` só em dev e recusa com HTTPS, `cmd/promote-admin`.
- **B2 — Autorização multi-tenant:** matriz da Fase 0 exercitada no código; `tenantExempt` e prefixos (`/api/admin/users`, `/api/admin/orgs`, `/api/admin/lives/assign`); distinção `PlatformAdmin` × `OrgRoleOwner` × `operator` (somente leitura) × vagas/limites (`organizations.max_lives`, `MAX_MONITORS`); caminho legado (`model.DefaultOrgID`) e assign de lives; "reiniciar nunca recria org de conta revogada".
- **B3 — IDOR / resolução de IDs:** todo handler que recebe ID por path/query; confirmar `org_id` no `WHERE` e checagem de existência antes de efeito; subárvores (`/api/history/`, `/api/pix/tickets/`, `/api/pix/media/`, `/api/pix/contacts/`).
- **B4 — Injeção e parsing:** SQL (interpolação de string vs `$1`), ordenação/limites (`limitParam`, `mode` de ranking), JSON malformado, `text/template`/HTML, PDF/upload (`internal/receipt`, `internal/report`, `ledongthuc/pdf`), ZIP/arquivos grandes.
- **B5 — Integrações:** HMAC do webhook WAHA (constante-time, raw body, segredo ausente = fail-closed, replay/idempotência), MinIO (política de bucket, URLs assinadas, TTL, key traversal), SMTP/Resend (injeção de header, HTML injection em e-mail), TikTok (rate limit, SSRF se houver URL de usuário).
- **B6 — SSE:** autorização no `handleSSE`, `publishSSE` filtrando por org, reconexão/limites, timeout de escrita, vazamento por canal compartilhado.
- **B7 — Disponibilidade e recursos:** rate limiting por rota/IP/usuário, tamanho de corpo (`MaxBytesReader`), timeouts de servidor (`ReadHeaderTimeout`, `IdleTimeout`), goroutine/`context` leaks, limites de monitor por org/global, custo de `/api/report`.
- **B8 — Erros, logs e privacidade:** mensagens que vazam detalhe interno, panic recovery, CORS (`view/cors.go` — sem `Allow-Credentials` hoje; confirmar que não há reflexão de `Origin`), logs com PII.
- **B9 — Migrações e RLS:** `database/postgres.go` (`migratePostgres`), `local_identity.go` — políticas default-deny reais? papéis do Postgres testados com um usuário **não** superusuário? grants/`BYPASSRLS`?

### Fase 3 — Review manual frontend
- **F1 — XSS:** enumerar todos os sinks (`innerHTML`, `insertAdjacentHTML`, `document.write`, `eval`, `Function`, `setAttribute` em `href`/`src`, `srcdoc`); classificar cada um (constante / escapado / controlado por servidor / controlado por atacante — ex.: comentário de terceiro na live, nome de pagador PIX, `uniqueID`). Validar `escapeHtml` (cobre `&<>"'`? usada em contexto de atributo e URL?).
- **F2 — CSP e headers:** ausência de CSP ⇒ impacto de XSS; verificar se o nginx pode impor `default-src 'self'` sem quebrar vendor/SSE/inline handlers (`onclick=` inline no HTML).
- **F3 — Sessão no cliente:** tokens em cookie HttpOnly? algo em `localStorage`/`sessionStorage`? envio de `X-CSRF-Token`? `frontend/config.js` (API base, flag `AUTH_ENABLED`) e o que vaza em `frontend/dist`.
- **F4 — Fluxos:** login/reset/invite (`frontend/auth.js`, `invite.js`, `reset-password.html`), redirecionamentos abertos (`?next=`), mensagens de erro por conta do servidor injetadas no DOM, auto-logout.
- **F5 — Terceiros e integridade:** `vendor/jspdf.umd.min.js`, `vendor/chart.umd.js` (versão, CVE, SRI ausente), qualquer CDN/fonte externa.
- **F6 — Deriva de build:** diff `frontend/` × `frontend/dist/` e definir checagem no lugar de cópia manual.
- **F7 — Dev server:** `frontend/server.js` (guarda de traversal já existe — validar com casos `%2e%2e`, symlink, `..%5c`) e confirmar que **não** vai para produção.

### Fase 4 — Infra, banco e borda
- Compose (prod/dev/raspberry): portas publicadas (Postgres 5432, MinIO 9000/9001, WAHA, backend 3001), senhas em `environment:` vs secrets, `user:`/`read_only`/`cap_drop`, healthchecks, restarts, rede interna isolada.
- Postgres: usuário de aplicação sem superuser/BYPASSRLS, senha forte, backups (`/backups/`), retenção, acesso de rede.
- MinIO/Waha: política de bucket, credenciais default, exposição via nginx/`$map` upstream.
- Borda: TLS (protocolos/cifras/HSTS/OCSP), limites (`client_max_body_size`, timeouts, `limit_req`), cabeçalhos de segurança, não expor `/api/*` sem TLS, proteção do admin (`/api/admin/*`).
- Observabilidade: alertas para falhas de login/lockout, erros 5xx, picos de conexão SSE.

### Fase 4b — Verificação da VPS em modo read-only
Executada com `ssh -p 22022 root@143.95.162.200` (chave, `BatchMode`). **Permitido:** `ss`, `docker ps/inspect/logs`, `ls -l`, `docker exec ... psql -c "SELECT ..."`, `curl -I`/`curl` em endpoint público sem credencial, `nginx -T`. **Proibido:** qualquer escrita, `docker restart`/`up`/`down`, alteração de `.env`, `psql` com DML/DDL, exploração ativa de vulnerabilidade, carga, tentativas de login.

Checklist:
1. Portas efetivamente publicadas no host × o que o compose declara.
2. Privilégios dos containers (root, `privileged`, `read_only`, capabilities).
3. Papéis do Postgres, `pg_hba`, presença real de políticas RLS e `force row level security`.
4. Permissões dos arquivos de segredo no host.
5. Cabeçalhos de segurança, TLS e limites na borda; dif do runtime contra o submódulo versionado.
6. Containers residuais/duplicados na rede da aplicação.
7. Respostas de endpoints públicos (esperado `401`/`403`) e vazamento de metadados.

> Resultado: ver `docs/security/00-baseline.md` §3–§4 (P1–P8).

### Fase 5 — Validação dinâmica (somente ambiente local)
Restrição: **nada destrutivo contra produção**. Alvo = `docker-compose.yml` local com dados sintéticos.
1. Testes de isolamento cross-tenant: dois usuários em duas orgs, exercitar toda a matriz da Fase 0 com IDs da outra org (esperado 403/404) — reaproveitar o harness de `backend/internal/view/integration_test.go` (1262 linhas) e `tenant_test.go`.
2. Casos negativos de authz: `operator` tentando rotas de dono/admin; usuário sem org; org desativada; sessão revogada; CSRF ausente/errado/roubado; cookie sem `Secure`; lockout bypass via `X-Forwarded-For` forjado.
3. XSS: payloads em campos que entram no DOM (comentário, nome de pagador, categoria de infração, nome de ticket) renderizados no frontend local.
4. Webhook: assinatura inválida, ausente, corpo alterado, replay; segredo não configurado.
5. Fuzzing/alvo: `go test -race`, fuzz dos parsers de query/limites e dos payloads de webhook.
6. Carga/abuso: muitas conexões SSE e muitas requisições a `/api/report` para medir DoS e limites.
7. Reexecução dos scanners da Fase 1 após correções.

**Gate:** toda hipótese H1–H14 classificada como confirmada, refutada ou não-testável (com motivo).

### Fase 6 — Triagem, relatório e reteste
- Classificação: severidade (Crítica/Alta/Média/Baixa) com justificativa de explorabilidade, CVSS quando fizer sentido, e **evidência obrigatória** (arquivo:linha, trecho, comando, saída ou passo-a-passo de repro).
- Cada achado: impacto real no negócio (cross-tenant? financeiro PIX? disponibilidade?), pré-condições, PoC mínima (redigida — sem payload pronto contra produção), correção recomendada, esforço, item de teste de regressão.
- Entregáveis: `docs/security/RELATORIO.md` (executivo + técnico), `docs/security/achados.csv` (backlog priorizado), `docs/security/reteste.md`.
- Reteste: aplicar correções de severidade Crítica/Alta e reexecutar apenas os casos correspondentes (evidência antes/depois).

### Fase 7 — Prevenção contínua (fora do escopo de auditoria, mas recomendado)
- CI mínimo: `go vet` + `go test ./... -race` + `gosec` + `govulncheck` + `gitleaks` + checagem de deriva `frontend/dist`.
- Regra de projeto mantida: toda review de segurança via `deepseek/deepseek-v4-pro`.

---

## 3. Como executar (forma de delegação)

Um único workflow com lanes paralelas read-only; cada lane devolve saída estruturada
(`arquivo:linha`, severidade, evidência, confiança, hipótese refutada/confirmada). Sem
escrita no repo durante as lanes; consolidação depois.

| Lane | Escopo | Entregável |
|---|---|---|
| L1 | Matriz de rotas × auth × papel × escopo de org | `matriz-rotas.csv` + rotas divergentes |
| L2 | B1 + B2 + B7 (authn, authz/tenant, DoS) | achados backend-authz |
| L3 | B3 + B4 + B9 (IDOR, injeção, RLS/migrações) | achados backend-dados |
| L4 | B5 + B6 + B8 (integrações, SSE, CORS/logs) | achados backend-integrações |
| L5 | F1–F7 (frontend: XSS, CSP, sessão, build drift) | achados frontend |
| L6 | Fase 4 (compose, Postgres, MinIO/WAHA, nginx, TLS) | achados infra |
| L7 | Fase 1 (scanners) — roda em paralelo e alimenta os demais | relatórios brutos + triagem |
| L8 | Fase 5 (validação dinâmica local) — depende de L1–L5 | casos + evidência de repro |
| L9 | Consolidação, severidade, relatório e backlog | `RELATORIO.md`, `achados.csv` |

Parâmetros fixos das lanes: `agent: "reviewer"`, `model: "deepseek/deepseek-v4-pro"`,
`context: "fresh"`, isolamento read-only, sem escritores concorrentes no repo.

---

## 4. Critérios de conclusão (definition of done)

1. Matriz de rotas cobrindo 100% das rotas de `server.go` + `registerTeamRoutes`, cada uma com auth/papel/escopo declarados.
2. H1–H14 classificadas, com evidência.
3. Todo achado com severidade + repro + correção + teste de regressão associado.
4. Achados Críticos/Altos retestados com evidência antes/depois.
5. Nenhuma conclusão baseada apenas em leitura superficial: cada afirmação de vulnerabilidade tem arquivo:linha e caminho de exploração, ou é marcada explicitamente como "não verificado".
6. Nada foi executado contra produção.
