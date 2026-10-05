# Reteste — roteiro e evidência antes/depois

**Regra:** cada achado de severidade Crítica/Alta tem um comando de reteste **objetivo**, que deve passar depois da correção e falhar (ou reproduzir o problema) antes.
**Estado atual:** nenhuma correção foi aplicada ainda — as colunas "antes" registram o comportamento medido nesta auditoria.
**Ambiente para reteste:** VPS em modo read-only (`ssh -p 22022 root@143.95.162.200`) para borda/segredos/DB, e `docker-compose.yml` local com dados sintéticos para código.

---

## 1. Já verificado nesta auditoria (comportamento "antes" medido)

| # | Comando de reteste | Resultado observado (antes) | Esperado (depois) |
|---|---|---|---|
| S-01 | `git ls-files --error-unmatch .kilo/plans/1788131684202-seguranca-remediacao.md && git show HEAD:... \| grep -c sk-vT8` | 2 ocorrências no HEAD; chave também nos commits `ec5a703`/`4991b3a` | 0 ocorrências no HEAD; `git log --all -G 'sk-vT8X5HWOY1UfOoxYtsPkrA'` vazio após o rewrite; chave **revogada** no provedor |
| P1-CRIT | `docker exec tiktok-live-monitor-postgres-1 psql -U tlm -tAc "select count(*) from pg_policies"` | `0` | `> 0` (uma política por tabela multi-tenant) |
| P1-CRIT | `... psql -tAc "select rolsuper,rolbypassrls,rolcreatedb,rolcreaterole from pg_roles where rolname='tlm'"` | `t\|t\|t\|t` | `f\|f\|f\|f` na role da aplicação |
| P1-CRIT | Conectar com a role reduzida e `SELECT count(*) FROM live_sessions` sem filtro de org | (hoje retorna todas as linhas) | `0` linhas para org não autorizada; e a aplicação continua funcionando |
| L2-2 | `grep '^TRUSTED_PROXIES' /opt/tiktok-live-monitor/.env` | `TRUSTED_PROXIES=172.16.0.0/12` | `TRUSTED_PROXIES=172.18.0.5/32` |
| L2-2 | De dentro de outro container, `curl -H 'X-Forwarded-For: 9.9.9.9' http://backend:3001/api/auth/login` e observar o IP registrado no lockout | IP forjado aceito (peer 172.x ∈ /12) | IP ignorado; `ClientIP` devolve o peer real |
| S-02 | `git log --all --oneline -- feedback.db \| wc -l` | `19` | `0` |
| P3-ALT | `curl -sSI https://livemonitortk.com.br/login \| grep -i strict-transport` | vazio (ausente) | `max-age=31536000; includeSubDomains` |
| P3-ALT | `curl -sSI https://livemonitortk.com.br/admin \| grep -iE 'content-security-policy\|x-frame-options\|referrer-policy'` | vazio | CSP, `X-Frame-Options: DENY`, `Referrer-Policy` presentes |
| P4-ALT | `for i in $(seq 1 12); do curl -so /dev/null -w '%{http_code} ' -X POST https://livemonitortk.com.br/api/auth/login -d '{}'; done` | todos `400/401` (nenhuma barreira de borda) | `429` a partir do limite da zona |
| L2-1 | `POST /api/auth/signup` duas vezes com o mesmo e-mail | 2ª resposta `409 "e-mail já cadastrado"` (oráculo) | 2ª resposta **idêntica** à 1ª (`201/202` genérico) |
| P2-ALT | `ls -l /opt/tiktok-live-monitor/.env*` | `.env.admin-password`, `.env.local`, `.env.supabase-password` em `-rw-r--r--` | `-rw-------` ou arquivos removidos |
| V-01 | `curl -sSI -H 'Host: evil.example' http://143.95.162.200/` | `Location: https://evil.example/` | `Location: https://livemonitortk.com.br/` (ou `444`) |
| V-02 | `curl -sSI http://143.95.162.200/ \| grep -i '^server'` | `nginx/1.27.5` | `nginx` |
| L5-1 | `node -e "console.log(new URL('/\\\\evil.com','https://livemonitortk.com.br').origin)"` no contexto da validação de `login.html:325` | resolve para `https://evil.com` e a validação atual **aceita** | validação rejeita (`url.origin !== location.origin`) |
| L5-2 | Com `npm run dev` ativo: `curl -i localhost:3000/.env.local` | retorna o conteúdo com `VERCEL_OIDC_TOKEN` | `403/404` |
| L5-3 | `curl -i 'localhost:3000/%'` | `URIError` não tratada (processo em risco) | `400` |

---

## 2. Roteiro completo de reteste por área

### 2.1 Autorização e isolamento (exige Postgres com dados de duas orgs)
Usar o harness existente `backend/internal/view/integration_test.go` e `tenant_test.go`, que já criam duas organizações.

```bash
cd backend
go test ./internal/... -count=1 -race -timeout 300s
```

Casos a cobrir (esperado entre parênteses):
1. Ler/gravar `live_sessions`, `gifts`, `pix_tickets`, `pix_messages`, `settings`, `target_gift_history` com `org_id` da outra org → **0 linhas / 403-404**.
2. `DELETE /api/history/{id}` com id de outra org → 404.
3. `GET /api/pix/media/{id}` com id de outra org → 404.
4. `GET /api/admin/lives/session/delete` com sessão de outra org → 404.
5. `POST /api/goals` referenciando `live` de outra org → 400/404.
6. Operador tentando `DELETE /api/history/{id}`, `POST /api/pix/tickets/{id}/answer`, `PUT /api/pix/values`, `POST /api/settings` → **403** (após `L3-3`, o 403 deve vir do handler, não só do middleware).
7. Operador fazendo `GET /api/org` → decidir o comportamento esperado (hoje 403; ver `L1-5`).
8. Plataforma admin sem membership → opera `DefaultOrgID`; `UpsertOrgMember(DefaultOrgID)` → recusa.

### 2.2 Autenticação e sessão
```bash
go test ./internal/auth/... -count=1 -run 'Local|Lockout|CSRF|Recovery' -v
```
1. `TestSignupDoesNotEnumerateEmail` (**novo**): duas chamadas de signup com o mesmo e-mail → status e corpo idênticos.
2. `TestRotateCSRFChangesToken` (**novo**): `RotateCSRF` devolve valor diferente e o anterior passa a falhar em `CSRF`.
3. `TestPerAccountLockoutAcrossIPs` (**novo**): 5 falhas de IPs distintos bloqueiam a conta.
4. `TestSignupSuccessDoesNotCountAsFailure` (**novo**): 5 signups bem-sucedidos não disparam lockout.
5. `TestResetPasswordRejectedWhenAuthDisabled` (**novo**).
6. `TestSignOutGlobalInvalidTokenReturnsError` (**novo**).
7. CSRF: `POST` autenticado sem `X-CSRF-Token` → 403; com token de outra sessão → 403; `POST` com `Origin` diferente de `SITE_URL` → 403.
8. Cookie: `Set-Cookie` contém `HttpOnly`, `Secure`, `SameSite=Lax`, `Path=/`, e **não** contém `Domain`.
9. Recovery: usar o mesmo token duas vezes → a segunda falha (uso único); gerar novo link invalida o anterior.

### 2.3 Integrações
1. **Webhook**: assinatura ausente, inválida, corpo alterado após assinatura, segredo não configurado → todos rejeitados; replay do mesmo `message_id` → não deve chamar `DownloadMedia`/`storePut` (após `L4-2`).
2. **Mídia**: `GET /api/pix/media/{id}` com `id` não numérico, negativo, de outra org, e `id` inexistente → 404/400; confirmar `Content-Type` restrito a `image/jpeg`/`application/pdf` e presença de `Content-Security-Policy: sandbox`.
3. **SSE**: abrir N conexões de uma org e confirmar 503 por org antes do teto global (após `L4-3`); revogar a sessão de um cliente e confirmar a ejeção.
4. **Rate limit**: sequências de requisições a `/api/auth/login`, `/api/webhooks/whatsapp` e `/api/report` → `429` a partir do limite.

### 2.4 Borda e infraestrutura (após correções de config)
```bash
# headers em todos os tipos de recurso
for p in / /admin /login /config.js /vendor/chart.umd.js; do
  echo "== $p"; curl -sSI "https://livemonitortk.com.br$p" | grep -iE 'strict-transport|content-security|x-frame|referrer-policy|x-content-type'
done

# host forjado e host desconhecido
curl -sSI -H 'Host: evil.example' http://143.95.162.200/
curl -sSI -k -H 'Host: outro.example' https://143.95.162.200/

# TLS
openssl s_client -connect livemonitortk.com.br:443 -status </dev/null 2>/dev/null | grep -iE 'OCSP|Protocol|Cipher'

# hardening dos containers
for c in tiktok-live-monitor-backend-1 tiktok-live-monitor-postgres-1 vps-edge-nginx; do
  docker inspect --format '{{.Name}} user={{.Config.User}} roRootfs={{.HostConfig.ReadonlyRootfs}} caps={{.HostConfig.CapDrop}} sec={{.HostConfig.SecurityOpt}}' "$c"
done

# segredos não aparecem em docker inspect após migrar para secrets:
docker inspect tiktok-live-monitor-backend-1 --format '{{json .Config.Env}}' | grep -c -E 'DATABASE_URL|WAHA_API_KEY|MINIO_SECRET'
```

### 2.5 Frontend
1. Abrir o painel com dados sintéticos contendo payloads em campos de terceiros (comentário, `uniqueId`, nome de pagador PIX, nome de ticket, categoria de infração) e confirmar que aparecem como **texto**.
2. Testar `login.html?next=` com `/\evil.com`, `//evil.com`, `https://evil.com`, `/admin.html`, `\evil.com`, `%2f%2fevil.com` → apenas `/admin.html` deve redirecionar.
3. Confirmar ausência de token de sessão em `localStorage`/`sessionStorage` e ausência do token de convite persistido (após `L5-5`).
4. `npm run dev` + `curl localhost:3000/.env.local`, `/.git/config`, `/..%2f..%2fetc/passwd`, `/%`.

### 2.6 Build e dependências
```bash
cd backend && go test ./... -count=1 -race
govulncheck ./...                    # com go.mod alinhado a 1.26.8, deve ficar limpo
go version -m <binário em produção>  # confirmar toolchain e versões de módulos após o rebuild
```

---

## 3. Critério de conclusão do reteste

1. Todos os comandos da §1 com o resultado "depois" esperado.
2. Nenhuma regressão nos testes existentes (`go test ./... -race` verde).
3. Casos de isolamento cross-org da §2.1 retornando 0 linhas / 403-404, **inclusive com a role de banco reduzida** (é isso que prova que o P1-CRIT foi resolvido de verdade, e não apenas documentado).
4. `pg_policies > 0` e `rolbypassrls=f` — a prova de que existe a segunda camada.
5. Reexecução das quatro varreduras (`golangci-lint`, `govulncheck`, `gitleaks`, `semgrep`) sem novos achados de severidade Alta/Crítica.
