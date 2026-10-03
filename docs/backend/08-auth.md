# Autenticação local

A identidade pertence ao PostgreSQL do VPS. `internal/auth` implementa login,
cadastro, administração, sessões e recuperação. Não há validação externa.

- `users`: IDs preservados na importação, e-mail único, papel de plataforma,
  situação, vencimento e hash Argon2id.
- `auth_sessions`: somente hashes de tokens; cookies HttpOnly; validade de 7 dias.
- `auth_action_tokens`: recuperação (1 hora) e ativação (7 dias), uso único.
- `auth_rate_limits`: bloqueio persistente de tentativas.
- CSRF e origem são exigidos em alterações autenticadas. `/api/auth/me` entrega
  um segredo CSRF estável por sessão, inclusive entre várias abas.
- Logout global, alteração de senha e desativação revogam sessões.
- Cadastro público aguarda aprovação; convite reserva uma vaga e vincula o
  convidado como ajudante de leitura.
- Vínculo e pagamento são verificados no PostgreSQL; SSE revalida acesso antes
  de entregar eventos. Donos não podem elevar o papel de plataforma.

A migração e a gestão comercial estão em [../auth-local.md](../auth-local.md).
