# Revisão do fluxo de troca de senha — correções do fluxo atual

## Veredito da revisão (fluxo de reset por link)

**O processo está funcional por design.** Cadeia fechada, confirmada contra o código-fonte do GoTrue:

1. `frontend/login.html` → "Esqueci minha senha?" → POST `/api/auth/recover`
   (`backend/internal/view/auth_handlers.go:221`): gera link via Admin API
   `POST /auth/v1/admin/generate_link` (type=recovery, redirect_to=`SITE_URL/reset-password.html`),
   envia e-mail (Resend/SMTP) em goroutine, anti-enumeração (erro só em log), rate-limit por IP.
2. Usuário clica no `action_link` → GoTrue `/auth/v1/verify` valida o token one-time e
   redireciona para `SITE_URL/reset-password.html#access_token=...&type=recovery`.
   **Confirmado no código do GoTrue** (`verifyGet` faz `q.Set("type", params.Type)` +
   `token.AsRedirectURL`), então o check do frontend está correto. Em erro, o fragmento
   traz `error_code=otp_expired` sem `access_token`.
3. `frontend/reset-password.html` extrai token/type do hash, valida (≥8 chars, confirmação),
   POST `/api/auth/reset-password` → `backend/internal/view/auth_handlers.go:305` →
   `auth.Config.UpdatePassword` (`backend/internal/auth/supabase_auth.go:135`) →
   `PUT /auth/v1/user` com Bearer token.

Cobertura de testes existente: `TestHandleAuthResetPassword*` em `backend/internal/view/auth_handlers_test.go`.

## Descobertas a corrigir (escopo acordado)

1. **Erro genérico demais no reset**: `handleAuthResetPassword` mapeia *qualquer* erro
   (Supabase fora do ar, senha rejeitada por policy) para 400 "link inválido ou expirado".
2. **Token fica na URL**: `reset-password.html` não limpa o fragmento após ler o
   `access_token` — o token de sessão (~1h) fica no histórico/barra de endereço.

## Fora de escopo (decisões registradas)

- "Trocar senha estando logado" — não existe hoje; usuário usa o fluxo de reset. Não implementar agora.
- Rate-limit em `/api/auth/reset-password` — token é JWT de sessão, brute-force inviável.
- Revogação de outras sessões após troca de senha (Supabase não revoga por padrão).

## Tarefas

1. **Backend — `handleAuthResetPassword`** (`backend/internal/view/auth_handlers.go:305`):
   - Distinguir `auth.ErrAuthUnavailable` (retornado por `UpdatePassword` em falha de rede/5xx):
     responder 502 com "serviço de autenticação indisponível, tente novamente".
   - Manter 400 "link inválido ou expirado" para os demais erros (token inválido/expirado/usado).
   - Ajustar testes em `backend/internal/view/auth_handlers_test.go`: caso de stub que responde
     5xx/erro de rede → expect 502; casos existentes de token inválido continuam 400.

2. **Frontend — `frontend/reset-password.html`**:
   - Após extrair `accessToken`/`linkType` do hash, limpar o fragmento:
     `history.replaceState(null, '', window.location.pathname + window.location.search)`
     (fazer no `init()` após a validação, antes de habilitar o form).

3. **Checklist operacional (validação sem código)** — pré-requisitos sem os quais o fluxo
   mora silenciosamente:
   - `SITE_URL` definido no Railway como `https://livemonitortk.com.br` (o código faz
     `TrimRight("/")` em `backend/internal/auth/auth.go:64`; vazio ⇒ 503 no recover).
   - `https://livemonitortk.com.br/reset-password.html` na allowlist **Redirect URLs** do
     Supabase (Authentication > URL Configuration) — caso contrário o verify redireciona com erro.
   - Mailer habilitado (`RESEND_API_KEY` ou `SMTP_HOST`): sem ele o link não é enviado,
     só loga (`backend/internal/view/auth_handlers.go:291`).
   - Conferir expiração do link (Supabase Mail OTP expiry) — link usado após expirar vira
     "link inválido ou expirado".

## Riscos

- Mudança mínima e localizada; nenhum dado/migração afetada.
- 502 novo no reset-password é aditivo (cliente anterior já trata `payload.error`).

## Validação

1. `go test ./...` em `backend/` (inclui os testes de reset/recover; não foi possível rodar
   em modo planejamento — bash restrito).
2. `npm test` e `npm run build` em `frontend/` (build copia `reset-password.html` para dist).
3. Smoke E2E manual: "esqueci minha senha" → recebe e-mail → clica no link → define nova
   senha → login com a nova senha → token some da URL após abrir a página.
4. Review final do diff delegada a subagente com modelo `deepseek/deepseek-v4-pro`
   (regra do AGENTS.md).
