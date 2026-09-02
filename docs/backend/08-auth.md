# Pacote `auth` — Autenticação e administração de usuários

> Diretório: `backend/internal/auth/`

O pacote `auth` implementa **autenticação baseada no Supabase Auth**:

- **Validação de JWT** local (HS256) dos access tokens emitidos pelo Supabase e
  middleware HTTP que protege `/api/*` e `/events` (`auth.go`);
- **Proteção contra brute-force** no login/cadastro com lockout em memória e
  honra de `X-Forwarded-For` apenas de proxies confiáveis (`lockout.go`);
- **Cliente do Supabase Auth** (login/logout com a anon key) (`supabase_auth.go`);
- **Cliente administrativo** (service role) para criar/aprovar/editar/remover
  assinantes e o fluxo de cadastro pendente (`supabase_admin.go`);
- **Cores do tema** expostas à tela de login (`theme.go`).

Arquivos:
| Arquivo | Papel |
|---|---|
| `auth.go` | Config, JWT, middleware, `RequireAdmin`, helpers de contexto. |
| `lockout.go` | `LoginLockout`, `LockoutConfig`, `ProxyTrust`, `ClientIP`. |
| `supabase_auth.go` | `SignInWithPassword`, `SignOutGlobal`, sessão. |
| `supabase_admin.go` | `AdminClient` (service role) + perfis. |
| `theme.go` | Cores de tema via env. |

---

## `auth.go` — Config, JWT e middleware

### Tipos/constantes

| Item | Descrição |
|---|---|
| `AccessTokenCookie = "tlm_access_token"` | Nome do cookie de sessão. |
| `User` | Identidade autenticada: `ID`, `Email`, `Role`, `Active`. |
| `Config` | `Enabled`, `JWTSecret`, `SupabaseURL`, `SupabaseAnon`, `ServiceRoleKey`, `JWTAudience`, `JWTIssuer`. |
| `userContextKey` | Chave de contexto (`authUser`). |

### Funções

| Função | Descrição |
|---|---|
| `LoadConfigFromEnv() Config` | Monta a config. **Auth desligada** quando `AUTH_ENABLED=0` ou `SUPABASE_JWT_SECRET` vazio. Defaults: `audience="authenticated"`, `issuer="<SUPABASE_URL>/auth/v1"`. |
| `PublicPath(path string) bool` | Caminhos **sem** token: `/api/auth/config`, `/api/auth/login`, `/api/auth/signup`, `/api/readiness`. Tudo em `/events` e `/api/*` exige autenticação. Demais (raiz) são públicos. |
| `TokenFromRequest(r) string` | Bearer do header `Authorization`; fallback para o cookie `tlm_access_token`. **Rejeita** tokens em query string (evita vazamento em logs/Referer). |
| `ValidateToken(tokenString) (*User, error)` | Parse/validação do JWT HS256 (iss/aud, exp). Lê claims: `role`/`active`/`subscription_expires_at` de `app_metadata`. Regras: sem claim de role → `subscriber`; sem `active` explícito → **inativo** (pendente); `subscriber` com assinatura expirada → erro. |
| `Middleware(next) http.Handler` | Auth **off**: injeta `User{Role:"admin", Active:true}`. Auth **on**: caminhos públicos passam; senão valida o token, exige `Active`, e guarda o `User` no contexto. |
| `UserFromContext(ctx) (*User, bool)` | Recupera o usuário do contexto. |
| `RequireAdmin(w, r, cfg) (*User, bool)` | Exige usuário ativo com `Role == "admin"`; escreve 401/403 e retorna `false` quando negado. |
| `writeAuthError(w, status, msg)` | Resposta JSON de erro. |

---

## `lockout.go` — Anti brute-force

### Configuração e estrutura

| Item | Descrição |
|---|---|
| `SignupLockoutIdentity = "*signup*"` | Chave fixa para rate-limit de cadastro por IP. |
| `LockoutConfig` | `MaxAttempts` + `Lockout` (duração). |
| `LoadLockoutConfigFromEnv()` | Envs `AUTH_MAX_LOGIN_ATTEMPTS` (padrão 5) e `AUTH_LOCKOUT_MINUTES` (padrão 15). |
| `lockoutEntry` | `failures`, `lockedUntil`, `lastSeen`. |
| `LoginLockout` | Mapa `key(email|ip) → entry` + mutex + parâmetros de poda (`reapInterval=30s`, `entryTTL=2×lockout` min 1min, `maxKeys=100k`). |
| `NewLoginLockout(cfg)` | Construtor. |
| `LockoutStatus` | `Locked`, `Remaining`, `RetryAfterSec`, `LockedUntil`, `MaxAttempts`. |

### Chave e IP confiável

| Função | Descrição |
|---|---|
| `lockoutKey(email, ip)` | `email|ip` minúsculo. |
| `ProxyTrust` | Conjunto de IPs/CIDRs de proxies confiáveis. |
| `ParseProxyTrust(raw)` | Constrói o conjunto a partir de CSV `"10.0.0.2,172.17.0.0/16,::1"`. |
| `LoadProxyTrustFromEnv()` | Lê `TRUSTED_PROXIES`. |
| `IsZero()` | Nenhum proxy confiável configurado. |
| `trustsIP(ip)` | IP pertence ao conjunto? |
| `remoteHost(remoteAddr)` | Extrai host de `RemoteAddr` (IPv6-safe). |
| `ClientIP(r, trust)` | IP do chamador. `X-Forwarded-For`/`X-Real-IP` **só** são honrados quando o peer direto é um proxy confiável (senão spoofável). |

### Métodos do `LoginLockout`

| Método | Descrição |
|---|---|
| `Config()` | Expõe a config (endpoint público). |
| `pruneLocked(now)` | Remove entradas desbloqueadas e velhas além do TTL (caller segura `mu`). |
| `reap(now)` | Podas no máximo a cada `reapInterval`. |
| `Status(email, ip)` | Estado de lockout (resposta **genérica** no GET de login para evitar enumeração). |
| `RecordFailure(email, ip)` | Incrementa falhas; ao atingir `MaxAttempts`, trava por `Lockout`. Aplica hard cap de `maxKeys` (remove entrada mais antiga). |
| `RecordSuccess(email, ip)` | Remove a entrada (sucesso). |
| `lockedStatus(entry)` | Monta `LockoutStatus` bloqueada com `retryAfterSec`. |

---

## `supabase_auth.go` — Login/logout no Supabase (anon key)

### Erros e tipos
```go
ErrInvalidCredentials // "email ou senha inválidos" (genérico — anti-enumeração)
ErrAuthUnavailable    // "serviço de autenticação indisponível, tente novamente"
LoginSession          // access_token, refresh_token, expires_in, token_type
```

### Funções

| Função | Descrição |
|---|---|
| `SignInWithPassword(email, password) (*LoginSession, error)` | POST `/auth/v1/token?grant_type=password` com a anon key. **Erros ≥300 viram `ErrInvalidCredentials`** de propósito (o `error_description` do Supabase distingue e-mail inexistente de senha errada — não vaza). Erros de rede/5xx viram `ErrAuthUnavailable`. |
| `SignOutGlobal(accessToken) error` | POST `/auth/v1/logout?scope=global` (revoga refresh tokens) com o token do usuário. |

---

## `supabase_admin.go` — Admin (service role)

### Tipos e request DTOs

| Tipo | Descrição |
|---|---|
| `SubscriberProfile` | Visão de assinante: `ID`, `Email`, `DisplayName`, `Role`, `Active`, `Notes`, `SubscriptionExpiresAt *time.Time`, `CreatedAt`, `UpdatedAt`. |
| `CreateSubscriberRequest` | `Email`, `Password`, `DisplayName`, `Notes`, `SubscriptionExpiresAt`. |
| `SignUpRequest` | Cadastro público: `Email`, `Password`, `DisplayName`, `Notes`. **Nunca aceita `role`/`active` do cliente** — todo cadastro nasce pendente. |
| `UpdateSubscriberRequest` | `ID`, `Password *string`, `DisplayName *string`, `Active *bool`, `Notes *string`, `SubscriptionExpiresAt *string`. |
| `AdminClient` | `cfg` + `http.Client{Timeout: 20s}`. |
| `ErrDuplicateSignup` | E-mail já cadastrado. |

### Funções

| Função | Descrição |
|---|---|
| `subscriberAppMetadata(active, expiresAt)` | Monta `app_metadata` (`role:"subscriber"`, `active`, `subscription_expires_at`). |
| `NewAdminClient(cfg)` | Construtor. |
| `enabled()` | Precisa de `Enabled && SupabaseURL && ServiceRoleKey`. |
| `request(method, path, body, out)` | Chamada HTTP genérica ao Supabase com service role (headers `apikey` + `Authorization`). Status ≥300 → erro com corpo. |
| `ListSubscribers() ([]SubscriberProfile, error)` | Lê `/rest/v1/profiles` (seleção + ordem `created_at.desc`) e ordena em memória (pendentes primeiro). |
| `SignUpPending(req)` | Cria assinante **pendente** (inativo) via `CreateSubscriber`; mapeia erro de duplicidade para `ErrDuplicateSignup`. |
| `CreateSubscriber(req)` | Valida e-mail/senha (≥8). Cria usuário em `/auth/v1/admin/users` (`email_confirm:true`, `app_metadata` inativo). Insere/atualiza a linha em `/rest/v1/profiles`. Em falha do perfil, **desfaz** o usuário criado. |
| `UpdateSubscriber(req)` | Atualiza `app_metadata` (active/expiração) via `/auth/v1/admin/users/<id>` e os campos do perfil via PATCH. Bloqueia edição de admin. |
| `DeleteSubscriber(id)` | Remove usuário via `/auth/v1/admin/users/<id>` (bloqueia remover admin). |
| `GetProfileByID(id)` | Perfil do usuário autenticado (usado por `/api/auth/me`). |

---

## `theme.go` — Cores do tema

| Função | Descrição |
|---|---|
| `ThemeColors` | `Pink`, `Cyan`, `BG` (hex). |
| `normalizeHexColor(raw, fallback)` | Valida `^#[0-9a-fA-F]{6}$`; inválido → fallback. |
| `LoadThemeFromEnv()` | Lê `AUTH_THEME_PINK`/`AUTH_THEME_CYAN`/`AUTH_THEME_BG`. Defaults: `#fe2c55`, `#25f4ee`, `#0b0d12`. |

---

## Fluxos (resumo)

**Login**: View → `ClientIP` → `lockout.Status` (429 se bloqueado) →
`SignInWithPassword` (falha → `RecordFailure`; erro 502 se indisponível) →
`ValidateToken` → `!Active` → 403 "aguardando aprovação" → sucesso →
`RecordSuccess` + cookie HttpOnly `tlm_access_token`.

**Requisições protegidas**: `auth.Middleware` valida o JWT localmente (HS256 +
iss/aud + exp + assinatura) e injeta o `User`. Endpoints `/api/admin/*` ainda
exigem `Role == "admin"` via `RequireAdmin`.

## Diagramas relacionados
- `diagrams/03-autenticacao.puml`.
