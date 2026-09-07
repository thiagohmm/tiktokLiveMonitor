# Plano — E-mail de boas-vindas/pagamento ao cadastrar

## Objetivo

Ao concluir o cadastro público (self-service) via `POST /api/auth/signup`, enviar
um e-mail transacional avisando o usuário que o cadastro foi recebido e que o
acesso é liberado após o pagamento de R$ 20,00.

## Decisões (já acordadas)

| Decisão | Escolha |
|---|---|
| Mecanismo de envio | SMTP via stdlib Go (`net/smtp`) — **sem dependências novas** |
| Dados de pagamento | Env vars (`PAYMENT_PIX_KEY` / `PAYMENT_LINK` / `PAYMENT_PRICE`) |
| Modo de envio | Assíncrono, best-effort (goroutine; falha só loga, não quebra o 201) |
| Remetente/assunto | `From` via env (`MAIL_FROM`); assunto fixo via env com default |
| Assunto (default) | `Liberação do acesso ao TikTok Live Monitor` |
| Escopo | Apenas cadastro público (`handleAuthSignup`). Criação por admin (`/api/admin/users`) **fora de escopo**. |

## Contexto do código (ponto de integração)

- Cadastro: `backend/internal/view/auth_handlers.go:144` (`handleAuthSignup`) →
  `s.admin.SignUpPending(body)` (`backend/internal/auth/supabase_admin.go:174`).
  Em sucesso retorna `201 {pending:true, message:...}`.
- Servidor/wiring: `backend/internal/view/server.go` (`HTTPServer` + `New`).
- Padrão de config por env a seguir: `backend/internal/auth/theme.go`
  (`LoadThemeFromEnv`).
- `go.mod` (`backend/`): apenas `jwt` e `pgx`; **não adicionar libs**.

## Env vars novas (adicionar ao `.env.example`)

```
# ── E-mail transacional (boas-vindas ao cadastrar) ─────────────────────────
SMTP_HOST=
SMTP_PORT=587
SMTP_USERNAME=
SMTP_PASSWORD=
# starttls | implicit | none   (default: starttls)
SMTP_TLS_MODE=starttls
# 1 = pular verificação TLS (apenas p/ SMTP self-signed em dev)
# SMTP_INSECURE_SKIP_VERIFY=0
MAIL_FROM=Equipe TikTok Live Monitor <no-reply@tiktoklivemonitor.com>
MAIL_SUBJECT=Liberação do acesso ao TikTok Live Monitor

# ── Pagamento (usado no corpo do e-mail) ────────────────────────────────────
PAYMENT_PIX_KEY=
PAYMENT_LINK=
PAYMENT_PRICE=20,00
```

Sem `SMTP_HOST`, o mailer fica desabilitado e nada é enviado (sem erro).

## Corpo do e-mail (fixo, com interpolação)

```
Assunto: Liberação do acesso ao TikTok Live Monitor

Olá! Tudo bem?

Identificamos que o seu cadastro no TikTok Live Monitor foi concluído com sucesso.

Para liberar o acesso à plataforma, é necessário realizar o pagamento de R$ 20,00.

Pagamento: <valor>

Após o pagamento, envie o comprovante respondendo a este e-mail. Assim que confirmarmos, seu acesso será liberado.

Caso tenha alguma dúvida, estou à disposição.

Atenciosamente,
Equipe TikTok Live Monitor
```

Regras da linha `Pagamento:` (prioridade):
1. `PAYMENT_LINK` não vazio → `Pagamento: <link>`.
2. senão `PAYMENT_PIX_KEY` não vazio → `Pagamento: Pix <chave>`.
3. senão → `Pagamento: [inserir chave Pix ou link de pagamento]` (placeholder) + log de warning.

`R$ 20,00` vem de `PAYMENT_PRICE` (default `20,00`).

## Tarefas (ordem de execução)

1. **Criar `backend/internal/mail/mail.go`** (package `mail`):
   - `Config`: `Host`, `Port`, `Username`, `Password`, `From`, `Subject`,
     `PixKey`, `PaymentLink`, `Price`, `TLSMode`, `InsecureSkipVerify`.
   - `LoadConfigFromEnv() Config` — lê os envs acima (defaults: `Port=587`,
     `TLSMode="starttls"`, `Price="20,00"`, `Subject="Liberação do acesso ao
     TikTok Live Monitor"`, `From` padrão).
   - `Mailer{cfg Config}` + `NewMailer(cfg)`.
   - `(m *Mailer) Enabled() bool` — `Host != "" && From != ""`.
   - `(m *Mailer) SendWelcome(to, displayName string) error` — monta assunto +
     corpo e chama o envio SMTP.
   - Funções puras (testáveis): `buildWelcomeBody(cfg, displayName) string` e
     `buildMessage(from, to, subject, body string) []byte`.
   - Envio SMTP: `net.Dial` → `smtp.NewClient` → (TLS conforme `TLSMode`) →
     `smtp.PlainAuth` (se `Username` não vazio) → `Mail`/`Rcpt`/`Data`.
     - `starttls`: `c.StartTLS(tls.Config{ServerName: host, InsecureSkipVerify})`.
     - `implicit`: `tls.Dial` direto (porta 465).
     - `none`: sem TLS.
   - `buildMessage`: headers `From`, `To`, `Subject` (UTF-8), `Date`,
     `MIME-Version: 1.0`, `Content-Type: text/plain; charset=UTF-8`,
     `Content-Transfer-Encoding: quoted-printable`, corpo com quebras `\r\n`.

2. **Criar `backend/internal/mail/mail_test.go`**:
   - Table-driven para `buildWelcomeBody`: prioridade link > pix > placeholder;
     interpolação de `Price`; `displayName` quando preenchido.
   - `buildMessage`: presença dos headers, assunto/corpo UTF-8 (acentos e `R$`)
     e finalização com `\r\n`.
   - SMTP real **não** é testado em unit (verificado manualmente).

3. **Editar `backend/internal/view/server.go`**:
   - Adicionar campo `mailer *mail.Mailer` em `HTTPServer`.
   - Em `New`, inicializar `mailer: mail.NewMailer(mail.LoadConfigFromEnv())`.
   - Importar `.../internal/mail`.

4. **Editar `backend/internal/view/auth_handlers.go`**:
   - Após o sucesso de `SignUpPending` (antes do `return` do 201), chamar
     `s.queueWelcomeEmail(body.Email, body.DisplayName)`.
   - Novo método `(s *HTTPServer) queueWelcomeEmail(email, displayName string)`:
     se `s.mailer == nil || !s.mailer.Enabled()` → return; senão
     `go func(){ if err := s.mailer.SendWelcome(...); err != nil { log.Printf("[View] welcome email: %v", err) } }()`.
     Normalizar `email` (`strings.TrimSpace(strings.ToLower(...))`).

5. **Editar `.env.example`**: adicionar o bloco de envs acima.

6. **Docs (opcional, baixa prioridade)**: nota em `docs/backend/08-auth.md` sobre
   o e-mail de boas-vindas e as novas envs; atualizar
   `docs/backend/diagrams/03-autenticacao.puml` se desejado.

## Falhas / modo de degradação

- **SMTP indisponível/erro**: goroutine loga; cadastro retorna `201` normalmente
  (best-effort). Usuário ainda recebe a instrução de pagamento no fluxo de login
  (mensagem "cadastro aguardando aprovação do administrador após o pagamento").
- **Sem `SMTP_HOST`/`MAIL_FROM`**: mailer desabilitado, nenhum envio, sem erro.
- **Sem `PAYMENT_PIX_KEY`/`PAYMENT_LINK`**: corpo usa o placeholder e loga warning.

## Validação

1. `cd backend && go build ./...` e `go test ./...` (inclui os novos testes).
2. Subir com SMTP real (ex.: Resend/Gmail SMTP) via envs, cadastrar um usuário e
   confirmar o recebimento do e-mail com o texto exato.
3. Confirmar que, com SMTP apontando para host inexistente, o cadastro **ainda**
   retorna `201` (best-effort) e o erro aparece no log.
4. Verificar que a linha `Pagamento:` reflete corretamente link > pix > placeholder.

## Fora de escopo

- E-mail para criação por admin (`/api/admin/users`).
- Aprovação automática de pagamento.
- HTML no corpo do e-mail (mantém `text/plain`).
- Novas dependências no `go.mod`.
