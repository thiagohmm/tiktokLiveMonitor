# Pacote `view` — Camada HTTP (SSE + REST)

> Diretório: `backend/internal/view/`

O pacote `view` é a **camada de apresentação** do backend: um servidor HTTP puro
que expõe:

- **SSE** em `/events` (fan-out dos eventos da live em tempo real);
- **REST API** em `/api/*` (estado, settings, histórico, gifts, metas, ranking,
  relatório, perfil, admin, auth);
- **middlewares** de autenticação, CORS, limite de corpo e headers de segurança.

A UI estática vive em `frontend/` (servida por nginx/Vercel/dev server), que faz
proxy para `/api/*` e `/events`.

Arquivos:
| Arquivo | Papel |
|---|---|
| `server.go` | `HTTPServer`, `Config`, `New`, `Start` (rotas, middlewares, wiring de eventos do monitor, hardening, graceful shutdown). |
| `sse.go` | Mecanismo de fan-out SSE (constantes, `sseClient`, write loop, ejeção, `handleSSE`, `broadcastSSE`). |
| `handlers.go` | Handlers da REST API (`/api/*`), helpers de resposta e middlewares de hardening (`limitBody`, `securityHeaders`). |
| `auth_handlers.go` | Endpoints de auth (`/api/auth/*`) e admin de usuários (`/api/admin/users*`). |
| `cors.go` | CORS por origem permitida. |

---

## `server.go` — `HTTPServer`

### Estruturas

| Tipo | Descrição |
|---|---|
| `HTTPServer` | Campos: `httpServer`, `controller *controller.AppController`, mapa `sseClients`, `maxSSEClients`, `cfg`, `auth auth.Config`, `admin *auth.AdminClient`, `lockout *auth.LoginLockout`, `proxyTrust`, `theme`, `corsOrigins`. |
| `Config` | `Host`, `Port`. |

### Constantes SSE (`sse.go`)
```go
sseDefaultMaxClients = 10000
sseEventBuffer       = 256     // mensagens no canal de cada cliente
ssePingInterval      = 30s
sseWriteTimeout      = 5s      // deadline por escrita
```

### Construção e start

| Função | Descrição |
|---|---|
| `New(cfg Config, ctrl *controller.AppController) *HTTPServer` | Monta o servidor: carrega configs de auth/lockout/proxy/theme/CORS do ambiente. |
| `Start(ctx context.Context) error` | Registra rotas, monta middlewares (`auth.Middleware` → `cors` → `limitBody` → `securityHeaders`), **liga os handlers de evento do monitor** (persistência via controller), registra callback de metas e inicia o `http.Server` com hardening (timeouts anti-Slowloris). Trata SIGTERM/SIGINT/ctx com graceful shutdown. |

### Rotas registradas

| Rota | Métodos | Handler | Protegida |
|---|---|---|---|
| `/events` | GET | SSE | Sim (salvo auth off) |
| `/api/state` | GET | estado do monitor | Sim |
| `/api/settings` | GET/POST | ler/gravar settings | Sim |
| `/api/history` + `/api/history/` | GET/DELETE | histórico de moderação / delete por id | Sim |
| `/api/connect` | POST | inicia monitoramento | Sim |
| `/api/disconnect` | POST | para monitoramento | Sim |
| `/api/clear-history` | POST | limpa histórico | Sim |
| `/api/readiness` | GET | healthcheck | **Público** |
| `/api/gifts` | GET/DELETE | gifts da live / por `?user=` / limpar | Sim |
| `/api/available-gifts` | GET | catálogo | Sim |
| `/api/target-gift-history` | GET | recentes / `?pending=1` | Sim |
| `/api/target-gift-history/answer` | POST | responder presente-alvo | Sim |
| `/api/pinned-comments` | GET | fixados | Sim |
| `/api/ranking` | GET | ranking (`?live=`, `?mode=`) | Sim |
| `/api/report` | GET | relatório (`?live=`) | Sim |
| `/api/profile` | GET | perfil (`?uid=`) | Sim |
| `/api/goals` | GET/POST | metas (ler/criar/atualizar) | Sim |
| `/api/goals/cancel` | POST | cancelar meta (`?id=`) | Sim |
| `/api/goals/complete` | POST | completar meta (`?id=`) | Sim |
| `/api/admin/lives` | GET | listar lives | Sim + admin |
| `/api/admin/lives/delete` | POST | apagar live | Sim + admin |
| `/api/auth/config` | GET | config pública de auth | **Público** |
| `/api/auth/login` | GET/POST | estado/login | **Público** |
| `/api/auth/signup` | POST | cadastro | **Público** |
| `/api/auth/logout` | POST | logout | Sim |
| `/api/auth/me` | GET | usuário atual | Sim |
| `/api/admin/users` | GET/POST | listar/criar usuários | Sim + admin |
| `/api/admin/users/update` | PATCH/PUT | editar usuário | Sim + admin |
| `/api/admin/users/delete` | DELETE/POST | remover usuário | Sim + admin |
| `/` | GET | aviso (backend) | Público |

### Registro de eventos do monitor (dentro de `Start`)

```go
s.controller.GetMonitor().OnEvent(func(eventType, data) {
    // any-gift-received   -> go HandleGiftEvent (persiste + metas)
    // new-chat-message    -> go HandleChatMessageEvent (cache/db)
    // new-social-event    -> go HandleShareEvent
    // new-like-event      -> go HandleLikeEvent
    // new-gift-user       -> RecordTargetGiftReceived (presente-alvo)
    // pinned-comment      -> RecordPinnedComment
    // (sempre)            -> broadcastSSE(eventType, data)
})
s.controller.SetGoalCallback(func(update GoalUpdate) {
    // broadcastSSE goal-update / goal-unlocked / goal-completed
})
```

---

## `sse.go` — Mecanismo SSE (fan-out escalável)

**Problema resolvido:** um cliente lento "congelando" todos os outros, ou um
peer morto segurando o pipeline. Solução: canal com buffer por cliente +
goroutine de escrita dedicada + deadlines por escrita + ejeção.

| Função/Tipo | Descrição |
|---|---|
| `sseMaxClientsFromEnv()` | Teto via `SSE_MAX_CLIENTS` (padrão 10000). |
| `sseClient` | `w`, `flusher`, `ch chan []byte` (buffer 256), `done`, `error`, `doneOnce`. |
| `newSSEClient(w, flusher)` | Cria um cliente. |
| `finish()` | Fecha `done` e `error` **uma única vez**. O canal `ch` **nunca é fechado** (evita panic em sends não-bloqueantes do broadcast). |
| `write(msg) error` | `SetWriteDeadline(agora+5s)` + write + flush. |
| `sseWriteLoop(c, ctx)` | Drena o canal com `select`: mensagem, `ctx.Done`, `c.done` ou ping (30s). Erro de escrita → `dropSSEClient`. |
| `removeSSEClient(c)` | Remove do mapa + `finish` (desconexão normal, sem log). |
| `dropSSEClient(c, reason)` | Ejeta cliente problemático (escrita falhou/lenta) com log. |
| `handleSSE(w, r)` | Valida `Flusher`, cria cliente, aplica teto (503 quando cheio), limpa deadline de leitura, envia **estado inicial** (`server-state`), inicia write loop e **espera** a goroutine terminar antes de retornar (evita SIGSEGV com writer finalizado). |
| `broadcastSSE(eventType, data)` | Serializa e enfileira para **todos** os clientes em `select` não-bloqueante; buffer cheio → ejetar o cliente lento. |
| `writeSSEPayload(...)` | Escreve um evento SSE e faz flush (estado inicial). |

---

## `handlers.go` — API handlers

Todos seguem o padrão: validar método → decodificar/params → chamar o
controller → `writeJSON`.

| Handler | Descrição |
|---|---|
| `handleState` | GET: estado do monitor. |
| `handleSettings` | GET: settings. POST: decodifica `monitor.Settings`, aplica no controller, **broadcast `settings-update`**. |
| `handleHistory` | GET: 100 moderações recentes. DELETE `/api/history/{id}`: remove uma. |
| `handleConnect` / `handleDisconnect` | Inicia/para monitoramento (`StartMonitoring`/`StopMonitoring`). |
| `handleClearHistory` | Limpa histórico. |
| `handleReadiness` | `{ready, sseClients, goroutines}` (healthcheck). |
| `handleGifts` | GET: `?user=` → gifts do usuário; senão gifts da live atual (`limit`). DELETE: limpar gifts. |
| `handleAvailableGifts` | GET: catálogo. |
| `handleTargetGiftHistory` | GET: `?pending=1|true` → pendentes; senão recentes (`limit`). |
| `handleTargetGiftHistoryAnswer` | POST: valida `responseType` (manual/automatic) e marca respondido. |
| `handlePinnedComments` | GET: fixados (`limit`). |
| `handleRanking` | GET: `?live=` (padrão: live atual) + `?mode=tiktok`. |
| `handleReport` | GET: relatório da live. |
| `handleProfile` | GET: `?uid=` obrigatório. |
| `handleGoals` | GET: `GoalsState`. POST: `id > 0` → update (preserva status/milestones); senão cria. Valida `title` e `targetUnits ≥ 1`. |
| `handleGoalCancel` / `handleGoalComplete` | POST `?id=` → `CancelGoal`/`CompleteGoal`. |
| `handleAdminLives` / `handleAdminLivesDelete` | `RequireAdmin`; listar lives / apagar por `?live=`. |
| Helpers de goals | `goalIDParam(r)` (lê `?id=`), `findGoal(state, id)` (busca meta em ativas/histórico). |

---

### Helpers de resposta (`handlers.go`)

| Função | Descrição |
|---|---|
| `writeJSON(w, data)` | Resposta JSON 200. |
| `writeError(w, status, msg)` | JSON `{"error": msg}` com status. |
| `writeInternalError(w, r, err)` | Loga o erro real e responde **mensagem genérica** ("erro interno do servidor") para não vazar SQL/banco ao cliente. |

---

### Hardening (`handlers.go` e `server.go`)

| Função | Descrição |
|---|---|
| `maxRequestBodyBytes = 1 << 20` (1 MiB) | Limite de corpo. |
| `limitBody(next)` | Envolve `r.Body` em `MaxBytesReader`. |
| `securityHeaders(next)` | `X-Content-Type-Options: nosniff`; `Cache-Control: no-store` em `/api/*`. |
| Timeouts do `http.Server` (`server.go`) | `ReadHeaderTimeout 5s`, `ReadTimeout 15s`, `WriteTimeout 30s`, `IdleTimeout 60s`. Nota: sem limite interno de threads — a proteção contra writers presos é o deadline curto por escrita (`sseWriteTimeout`). |

---

## `auth_handlers.go` — Auth e admin de usuários

### Auth

| Handler | Descrição |
|---|---|
| `handleAuthConfig` | GET (público): `enabled`, `supabaseUrl`, `supabaseAnonKey`, limites de lockout e cores do tema. |
| `handleAuthLogin` | GET (público): resposta **genérica** de lockout (anti-enumeração). POST: aplica lockout, `SignInWithPassword`, `ValidateToken`, exige `Active` (403 se pendente), grava cookie HttpOnly `tlm_access_token` e devolve sessão. |
| `handleAuthSignup` | POST (público): rate-limit por IP (`SignupLockoutIdentity`), valida body (`auth.SignUpRequest`), `admin.SignUpPending` → 201 `{pending:true}`. Erros: 409 duplicado, 502 problema Supabase, 429 bloqueado. |
| `handleAuthLogout` | POST: `SignOutGlobal` + limpa cookie. |
| `handleAuthMe` | GET: retorna usuário do contexto + perfil (via `admin.GetProfileByID` quando disponível). Auth off → `{authenticated:false, authEnabled:false}`. |

### Admin de usuários (todos com `RequireAdmin`)

| Handler | Descrição |
|---|---|
| `handleAdminUsers` | GET: lista usuários + `pendingCount`. POST: cria assinante. |
| `handleAdminUsersUpdate` | PATCH/PUT: atualiza assinante. |
| `handleAdminUsersDelete` | DELETE/POST: remove por `?id=`. |

---

## `cors.go` — CORS

| Função | Descrição |
|---|---|
| `LoadCORSOriginsFromEnv()` | Lê `CORS_ALLOWED_ORIGINS` (CSV). Vazio = sem CORS (frontend/backend na mesma origem via proxy). |
| `cors(next)` | Para origens permitidas, adiciona `Access-Control-Allow-Origin`/`Methods`/`Headers`/`Max-Age` + `Vary: Origin`; responde preflight `OPTIONS` com 204. |

---

## Diagramas relacionados
- `diagrams/00-arquitetura.puml`.
- `diagrams/04-sse-fanout.puml`.
