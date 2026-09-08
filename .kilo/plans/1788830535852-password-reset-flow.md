# Plano — Redefinição de senha ("Esqueci minha senha")

## Objetivo
Permitir que um usuário esqueceu a senha a redefina sozinho, sem depender do admin.

## Decisões (confirmadas com o usuário)
1. **Link gerado via `generate_link`** (service role) e enviado pelo mailer próprio (Resend/SMTP) — consistente com o e-mail de boas-vindas.
2. **Nova senha aplicada por endpoint no backend** (`PUT /auth/v1/user`), não via supabase-js.
3. UI "esqueci a senha" inline em `login.html`; página nova `reset-password.html` recebe o token e pede a nova senha.

## Fluxo (sequence)
```
login.html (Esqueci minha senha)
  -> POST /api/auth/recover {email}            (público, anti-enumeração, rate-limit por IP)
      backend: generate_link(type=recovery, redirect_to=SITE_URL/reset-password.html)
      backend: mailer.SendPasswordReset(email, action_link)   [sempre responde 200 genérico]
  -> usuário clica no link (Supabase /auth/v1/verify)
      Supabase valida o token e redireciona para
      https://<site>/reset-password.html#access_token=...&type=recovery
  -> reset-password.html lê access_token do hash
      POST /api/auth/reset-password {token, password}
      backend: PUT /auth/v1/user (apikey anon + Bearer token, body {password})
  -> sucesso -> redireciona para /login.html
```

---

## Fase 1 — Config e rotas públicas no backend

1.1. `backend/internal/auth/auth.go`
- Adicionar campo `SiteURL string` em `Config`.

1.2. `backend/internal/auth/auth.go`
- Em `LoadConfigFromEnv`, ler `SITE_URL` com `strings.TrimRight(..., "/")` e preencher `SiteURL`.

1.3. `backend/internal/auth/auth.go`
- Em `PublicPath`, incluir `/api/auth/recover` e `/api/auth/reset-password`.

## Fase 2 — Chamadas ao Supabase e lockout

2.1. `backend/internal/auth/supabase_admin.go`
- Novo método `GenerateRecoveryLink(email, redirectTo string) (string, error)`:
  POST `/auth/v1/admin/generate_link` via helper `request()`, body
  `{"type":"recovery","email":email,"options":{"redirect_to":redirectTo}}`,
  retornar o campo `action_link`. Erros (ex.: usuário inexistente) sobem para o handler.

2.2. `backend/internal/auth/supabase_auth.go`
- Novo método `UpdatePassword(accessToken, newPassword string) error`:
  PUT `{SupabaseURL}/auth/v1/user`, headers `apikey: anon` +
  `Authorization: Bearer <token>`, body `{"password": newPassword}`.
  2xx → nil; senão `errors.New("link inválido ou expirado")` (anti-enumeração).

2.3. `backend/internal/auth/lockout.go`
- Adicionar `RecoverLockoutIdentity = "*recover*"` (rate-limit por IP, separado do signup).

## Fase 3 — Mailer (e-mail de redefinição)

3.1. `backend/internal/mail/mail.go`
- Adicionar campo `ResetSubject string` em `Config`.

3.2. `backend/internal/mail/mail.go`
- Em `LoadConfigFromEnv`, ler `MAIL_RESET_SUBJECT` com default `"Redefinição de senha — TikTok Live Monitor"`.

3.3. `backend/internal/mail/mail.go`
- Novo `buildResetBody(link string) string` (texto simples com instrução e o link).

3.4. `backend/internal/mail/mail.go`
- Novo `SendPasswordReset(to, link string) error` (espelha `SendWelcome`; usa `sendResend`/`send`).

## Fase 4 — Handlers e rotas

4.1. `backend/internal/view/auth_handlers.go`
- `handleAuthRecover` (POST):
  - `!s.auth.Enabled` → 400; método != POST → 405.
  - IP via `auth.ClientIP`; lockout em `auth.RecoverLockoutIdentity`; locked → 429 com `retryAfterSec`.
  - Decodificar `{email}`; email vazio → 400.
  - `redirectTo := s.auth.SiteURL + "/reset-password.html"`; se `SiteURL` vazio → 503.
  - `link, err := s.admin.GenerateRecoveryLink(email, redirectTo)`; se sucesso, `go mailer.SendPasswordReset(...)` (best-effort, loga falha).
  - **Sempre** responder 200 genérico "Se este e-mail estiver cadastrado, enviaremos um link de redefinição."; registrar falha no lockout por tentativa.

4.2. `backend/internal/view/auth_handlers.go`
- `handleAuthResetPassword` (POST):
  - método != POST → 405; decodificar `{token, password}`.
  - `password` < 8 → 400 "senha deve ter pelo menos 8 caracteres".
  - `s.auth.UpdatePassword(token, password)`; erro → 400 "link inválido ou expirado"; sucesso → 200 `{success:true}`.

4.3. `backend/internal/view/server.go`
- Registrar `/api/auth/recover` e `/api/auth/reset-password`.

## Fase 5 — Testes do backend

5.1. `backend/internal/view/auth_handlers_test.go` (espelhar `signupTestServer`)
- recover: e-mail inexistente → sempre 200 (anti-enumeração); rate-limit por IP → 429 após o teto.

5.2. `backend/internal/view/auth_handlers_test.go`
- reset-password: senha curta → 400; sucesso → chama `PUT /auth/v1/user`.

5.3. `backend/internal/auth/supabase_admin_test.go`
- `GenerateRecoveryLink` monta o body correto e extrai `action_link`.

5.4. `backend/internal/mail/mail_test.go`
- `buildResetBody` contém o link.

## Fase 6 — Frontend

6.1. `frontend/auth.js`
- Adicionar `requestPasswordReset(email)` (POST `/api/auth/recover`, trata erro e `locked`/`retryAfterSec`) e `resetPassword(token, password)` (POST `/api/auth/reset-password`); expor ambos em `window.TLMAuth`.

6.2. `frontend/login.html`
- Link "Esqueci minha senha?" sob o campo de senha; alterna painel com input de e-mail + botão que chama `TLMAuth.requestPasswordReset` e mostra a mensagem genérica de sucesso.

6.3. `frontend/reset-password.html` (novo, usa `config.js`/`auth.js`)
- Ler `access_token` e `type` de `window.location.hash`; sem token ou `type !== "recovery"` → "link inválido ou expirado".
- Form "Nova senha" + "Confirmar" (min 8, conferência de igualdade).
- Submit → `TLMAuth.resetPassword(token, password)`; sucesso → mensagem e redirect para `/login.html`.

6.4. `frontend/build.mjs`
- Incluir `reset-password.html` na lista de cópia.

6.5. `frontend/Dockerfile`
- Incluir `reset-password.html` no `COPY`.

## Fase 7 — Config, documentação e rollout

7.1. `.env.example`
- Documentar `SITE_URL` (origem pública do frontend, usada no `redirect_to`) e `MAIL_RESET_SUBJECT`.

7.2. `docker-compose.yml` e `docker-compose.raspberry.yml`
- Repassar `SITE_URL` e `MAIL_RESET_SUBJECT` ao serviço `backend`.

7.3. `backend/.railway/railway.ts`
- Adicionar `SITE_URL: preserve()` e `MAIL_RESET_SUBJECT: preserve()`.

7.4. `DEPLOYMENT.md` / `PRODUCAO.md`
- Documentar a allowlist de Redirect URLs do Supabase e a env `SITE_URL`.

7.5. Rollout (ações manuais fora do repositório)
- Supabase: adicionar `https://tiktok-live-monitor-two.vercel.app/reset-password.html` (e origens de preview/localhost) em Authentication > URL Configuration > Redirect URLs.
- Railway: definir `SITE_URL=https://tiktok-live-monitor-two.vercel.app`.
- Publicar: push em `lite-sem-ia` (backend automático) e `cd frontend && npx vercel --prod`.

---

## Segurança / modos de falha
- **Anti-enumeração**: recover sempre 200 genérico; reset-password devolve erro genérico para token inválido/expirado.
- **Rate-limit**: `*recover*` por IP evita abuso de envio de e-mail.
- **Token**: nunca logado; trafega no corpo HTTPS. Token de recuperação é de uso único e expira (padrão Supabase).
- **SITE_URL ausente** → 503 explícito no recover (falha de configuração, não vaza dados de usuário).
- Senha com mínimo de 8 caracteres (consistente com `CreateSubscriber`).

## Validação
- `cd backend && go test ./...`
- `cd frontend && npm run build` (confirma que `reset-password.html` entra em `dist/`).
- Manual: recover para e-mail real → clicar link → definir senha → logar; repetir com e-mail inexistente (mensagem genérica).

## Fora de escopo
- Redefinição iniciada pelo admin (já existe via `UpdateSubscriber`).
- Templates HTML/e-mails ricos (mantém texto puro, como o de boas-vindas).
