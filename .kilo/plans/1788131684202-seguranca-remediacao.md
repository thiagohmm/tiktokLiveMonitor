# Plano de Remediação de Segurança — tiktokLiveMonitor

## Contexto

Auditoria de segurança no branch `lite-sem-ia` (Go + SQLite/Postgres + Supabase Auth + frontend vanilla). Foram encontrados vazamentos de segredos/dados no repositório e fragilidades de autenticação e hardening no servidor HTTP.

Escopo aprovado pelo usuário: **revogar a chave vazada + reescrever o histórico do git** para purgar chave e dados, além das correções de código.

---

## Achados (severidade)

### Crítico
1. **Chave de API Blackbox vazada no histórico** — `.blackboxcli/settings.json` (adicionado no commit `ec5a703`, removido no HEAD) contém `Authorization: Bearer sk-vT8X5HWOY1UfOoxYtsPkrA`.
2. **`feedback.db` versionado** — banco SQLite (~1.5 MB) com dados reais de participantes (mensagens, gifts, `uniqueId`, nicknames, comentários) commitado repetidamente em TODO o histórico, apesar de `*.db`/`feedback.db` estarem hoje no `.gitignore`.

### Alto
3. **Bypass de lockout de login** — `ClientIP` (`internal/auth/lockout.go:75`) confia em `X-Forwarded-For`/`X-Real-IP` sem restrição; o mapa `keys` cresce sem limite (memory DoS).
4. **Servidor HTTP sem timeouts** — `internal/view/server.go:208` não define `ReadHeaderTimeout`/`ReadTimeout`/`WriteTimeout`/`IdleTimeout` (Slowloris).

### Médio
5. **Token de acesso via query string** — `?access_token=` aceito em `internal/auth/auth.go:70` e usado pelo SSE em `web/auth.js:107` (vaza em logs/Referer/histórico).
6. **Enumeração de usuários** — `GET /api/auth/login` retorna status de lockout por email (`internal/view/auth_handlers.go:41`); `SignInWithPassword` repassa `error_description` do Supabase (`internal/auth/supabase_auth.go:66`).
7. **Sem rate limiting global** — só o login tem lockout; endpoints de escrita (`/api/connect`, `/api/goals`, etc.) não têm limite.
8. **JWT sem validação de `aud`/`iss`** — `ValidateToken` (`internal/auth/auth.go:88`) valida só HS256 + `exp`.

### Baixo
9. **Erros internos de banco expostos** — vários handlers chamam `writeError(w, ..., err.Error())` (ex.: `handleHistory`, `handleGifts` em `internal/view/server.go`).
10. **Bug (corretude)** — `GetSetting` com lógica invertida (`internal/database/database.go:1766`); `LiveFirstSeen` com 5 placeholders e 4 args (`internal/database/database.go:1402`).

---

## Tarefas ordenadas

### Fase 1 — Conter os vazamentos (faça ANTES de qualquer commit novo)
1. **Revogar a chave Blackbox** no painel do provedor (`sk-vT8X5HWOY1UfOoxYtsPkrA`). Confirmar revogação antes de prosseguir.
2. **Parar de versionar o banco**: `git rm --cached feedback.db` e confirmar que `*.db`, `*.db-shm`, `*.db-wal`, `feedback.db` permanecem no `.gitignore`. Avaliar `git rm --cached` também para quaisquer outros artefatos de dados (já ignorados mas porventura rastreados).
3. **Auditar o histórico em busca de outros segredos** (`.env`, chaves `sk-*`, `SUPABASE_*`, chaves privadas) antes do rewrite, para purgar tudo de uma vez.

### Fase 2 — Reescrever o histórico (destrutivo)
4. Instalar/usar `git filter-repo` (ou BFG) para:
   - Remover `feedback.db` (e `*.db`) de todos os commits.
   - Remover `.blackboxcli/settings.json` (ou substituir o token por `REDACTED`).
   - Remover qualquer outro segredo encontrado na tarefa 3.
5. **Coordenar o force-push**: avisar colaboradores, `git push --force --all` + `git push --force --tags` (todos os hashes mudarão; colaboradores devem re-clonar).
6. Alternativa/adicional: marcar a chave como comprometida e garantir que o remoto não tenha proteção que impeça o force-push (branch protection).

### Fase 3 — Corrigir o código (autenticação/hardening)
7. **`ClientIP`**: só confiar em `X-Forwarded-For`/`X-Real-IP` quando o request vier de um proxy confiável (ex.: verificar `RemoteAddr` contra allowlist de IPs do proxy, ou config explícita `TRUST_PROXY`). Sem isso, usar `RemoteAddr`. Adicionar limpeza periódica (TTL) ao mapa `keys` do lockout para evitar memory DoS.
8. **Timeouts do servidor** em `internal/view/server.go:208`: definir `ReadHeaderTimeout: 5s`, `ReadTimeout: 15s`, `WriteTimeout: 30s`, `IdleTimeout: 60s` (ajustar WriteTimeout para não cortar SSE — SSE usa `http.Flusher`; manter WriteTimeout generoso ou 0 apenas para o endpoint de SSE).
9. **Token na URL**: remover `?access_token=` do fluxo do SSE. Como `EventSource` não envia headers, migrar o SSE para usar fetch + `ReadableStream` com header `Authorization` (como já feito no `authFetch`), ou usar cookie `HttpOnly`/`Secure` de sessão. No backend, parar de aceitar token via query string em `TokenFromRequest` (`internal/auth/auth.go:70`).
10. **Enumeração**: no `GET /api/auth/login`, retornar resposta genérica (não revelar `locked`/`remainingAttempts` por email arbitrário sem autenticação); em `SignInWithPassword`, normalizar erros para uma mensagem genérica ("credenciais inválidas") em vez de repassar `error_description`.
11. **JWT**: validar `aud` e `iss` esperados (configuráveis via env, ex.: `SUPABASE_URL`) em `ValidateToken`.
12. **Erros de banco**: substituir `writeError(w, status, err.Error())` por mensagem genérica e logar o detalhe no servidor.
13. (Opcional) **Rate limiting global**: middleware simples por IP (token bucket) cobrindo todos os endpoints.
14. (Opcional) Corrigir os bugs de corretude (tarefa 10 da seção Achados) em conjunto.

### Fase 4 — Validação
15. `go test ./...` e `go vet ./...` após as mudanças.
16. Smoke test de login/logout/SSE e endpoints admin com `AUTH_ENABLED=1` (verificar que o SSE continua funcional após remover token da URL).
17. Confirmar com `git log --all --oneline -- feedback.db` e busca por `sk-` que o histórico não contém mais segredos/dados (após rewrite).

---

## Riscos / Observações

- **Rewrite de histórico é destrutivo**: muda todos os hashes; exige `--force` e re-clone por todos. Backup do repositório antes.
- **Remover `?access_token=` pode quebrar o SSE atual**: a migração do frontend (fetch/ReadableStream ou cookie) precisa ser feita junto com o backend para não deixar a UI sem eventos.
- **`AUTH_ENABLED=0`** (padrão quando `SUPABASE_JWT_SECRET` está vazio) deixa TODOS os endpoints, inclusive admin, abertos — por design, mas relevante se o app for exposto publicamente. Considerar exigir `AUTH_ENABLED=1` em produção.
- A chave vazada deve ser considerada **comprometida e irrevogável do histórico antigo** mesmo após o rewrite (qualquer fork/clone anterior ainda a contém); a revogação é a única mitigação definitiva.

## Fora de escopo (nesta iteração)

- Migração para HTTPS/TLS (assume reverse proxy).
- Proteção CORS/CSRF aprofundada.
- Migração do lockout para armazenamento persistente (Redis/DB).
