# Pacote `monitor` — Monitor de lives (parte Go)

> Diretório: `backend/internal/monitor/`

O pacote `monitor` é o **núcleo de integração com o TikTok**. Ele:

1. **Gerencia o processo filho Node** (`bridge.js`) que fala com o
   `tiktok-live-connector`;
2. **Traduz eventos brutos** do stream em eventos tipados (`EventData`);
3. **Mantém estado em memória** da live (chat recente, perguntas, usuários
   fixados, presentes-alvo, streaks de presente);
4. **Implementa reconexão** com backoff exponencial + jitter;
5. **Correlaciona presente com pergunta** do doador, de forma determinística
   (sem IA) — `correlation.go`.

A ponte Node fala com o Go por **JSON Lines**: o Go escreve comandos no stdin
do Node (`connect`, `disconnect`, `fetch-gifts`) e lê eventos do stdout
(`connection-status`, `new-chat-message`, `any-gift-received`, ...).

Arquivos:
| Arquivo | Papel |
|---|---|
| `monitor.go` | Constantes de evento, tipos, estrutura `Monitor`, ciclo de vida (start/stop/close) e acesso a estado. |
| `events.go` | Dispatch dos eventos do bridge (`handleBridgeEvent`), processamento de chat e de presente-alvo. |
| `bridge.go` | Processo Node (start/stop, stdin/stdout) e supervisor de reconexão (backoff + jitter). |
| `giftstreak.go` | Streaks (combos) de presente: liquidação por evento final ou timeout. |
| `session.go` | Reuso/purga de dados de sessão na (re)conexão à mesma live. |
| `availablegifts.go` | Catálogo de presentes disponíveis (cache + fetch no bridge). |
| `helpers.go` | Utilitários de parsing/normalização (texto, números, flag de seguidor, heurísticas PT-BR). |
| `correlation.go` | Correlação determinística presente↔pergunta (heurísticas + revisão adiante). |
| `bridge.js` / `gifts.js` / `follower.js` | Ponte Node — documentada em `04-bridge-node.md`. |

---

## `monitor.go` — Tipos, estrutura e ciclo de vida

### Constantes de eventos (string `type` enviadas aos handlers/SSE)
```go
EventChatMessage       = "new-chat-message"
EventGiftUser          = "new-gift-user"
EventAnyGift           = "any-gift-received"
EventPinnedComment     = "pinned-comment"
EventFlaggedMessage    = "flagged-message"
EventGiftQuestionCorr  = "gift-question-correlation"
EventKeywordMention    = "keyword-mention"
EventMarkUserRed       = "mark-user-red"
EventConnectionStatus  = "connection-status"
EventLiveUserConnected = "live-user-connected"
EventNewFollower       = "new-follower"
EventNewSocialEvent    = "new-social-event"
EventGiftsList         = "gifts-list"
EventNewLike           = "new-like-event"
```

### Tipos

| Tipo | Descrição |
|---|---|
| `EventData map[string]interface{}` | Payload de evento (chaves string, valores livres — vindos do JSON do Node). |
| `EventHandler func(eventType string, data EventData)` | Callback registrado via `OnEvent`. |
| `pendingEmit` | Evento aguardando emissão **fora do lock** (evita deadlock — ver `handleChatMessage`). |
| `UserInfo` | `UniqueID`, `Nickname`, `IsFollower *bool`. |
| `ChatMessage` | Mensagem do chat em memória (`UniqueID`, `Nickname`, `Comment`, `Timestamp` ms, `IsFollower`). |
| `QuestionEntry` | Candidata a pergunta (usada na correlação). |
| `GiftPayload` | Presente normalizado do stream (nome, usuário, `RepeatCount`, `RepeatEnd`, `GiftType`, `IsFollower`). |
| `Settings` | Config do monitor: `ModerationEnabled bool`, `LogLevel string`, `TargetGifts []string`. |
| `State` | Estado exposto à API: `Connected`, `Username`, `Settings`, `ReconnectAttempts`. |

### Constantes de buffer/tempo
```go
chatBufferMax             = 500
questionBufferMax         = 300
sessionReuseMaxAge        = 10 * time.Hour
questionCorrelationWindow = 3 * time.Minute
correlationForwardCount   = 2
correlationForwardDelay   = 4 * time.Second
pinnedMessagesMax         = 200
repeatWindowMs            = 60000   // 60s
repeatsRequired           = 3       // 3 repetições = "mensagem repetida"
```

Delays de reconexão ajustáveis (atomics lidos por goroutines de produção e
escritos por testes):
```go
reconnectBaseDelay = 1s; reconnectMaxDelay = 30s; reconnectJitterPct = 0.2
```

### Estrutura `Monitor`

Campos principais:
- `cmd *exec.Cmd`, `stdin`, `stdout` — processo bridge;
- `chatBuffer []ChatMessage`, `questionBuffer []QuestionEntry` — janela em
  memória;
- `pinnedUsers map[string]bool` (normalizados), `processedPins`,
  `repeatAlerted` — estado de moderação visual;
- `handlers []EventHandler` — assinantes de eventos;
- `settings Settings`, `connected bool`, `currentUsername string`;
- `repo model.Repository` — para restaurar/purgar dados de sessão;
- `giftsCh`, `availableGifts` — catálogo de presentes;
- canais do supervisor: `bridgeEnded`, `reconnectKick`,
  `reconnectAttempts`, `userStopped`, `supCancel/supDone/supStopCh`;
- `giftStreaks map[string]*giftStreak` — streaks de presente pendentes.

### Construção, settings e ciclo de vida

| Função | Descrição |
|---|---|
| `New() (*Monitor, error)` | Construtor com defaults (`ModerationEnabled=true`, `LogLevel="info"`). |
| `OnEvent(handler EventHandler)` | Registra um assinante de eventos. |
| `GetState() State` / `GetSettings() Settings` | Leituras seguras (mutex). |
| `SetSettings(s Settings)` | Atualiza settings (persistência é do controller). |
| `SetRepo(repo)` | Injeta repositório (para restore de sessão). |
| `SetCurrentLive(username)` | Define o usuário atual (útil em testes/load). |
| `StartMonitoring(ctx, username) error` | Zera estado, inicia bridge (se preciso), inicia supervisor, limpa buffers/pins/streaks, roda `restoreOrPurgeSessionData` e envia `connect`. |
| `StopMonitoring()` | Marca `userStopped`, para supervisor, envia `disconnect` e emite `connection-status` "Desconectado pelo usuário". |
| `Close()` | Para supervisor + mata bridge (shutdown do app — evita processo Node órfão). |
| `Emit(eventType, data)` | Emite evento a todos os handlers (exportado — usado por controller/view/load-test). |
| `emit(eventType, data)` | Implementação (copia handlers fora do lock para não segurar o mutex). |

### Acesso a estado / buffers

| Função | Descrição |
|---|---|
| `GetChatBuffer() []ChatMessage` | Cópia do buffer de chat. |
| `GetQuestionBuffer() []QuestionEntry` | Cópia do buffer de perguntas. |
| `PruneQuestions(now int64)` | Podas das perguntas mais antigas que a janela (`pruneQuestions`, em memória). |
| `IsPinnedUser(uid string) bool` | Se usuário está "marcado" (pinado/vermelho). |

---

## `events.go` — Handlers de eventos do bridge

### `handleBridgeEvent(eventType string, data EventData)` — dispatch

Stream de eventos recebidos:

- **`connection-status`**: atualiza `connected`, zera tentativas em sucesso;
  em caso de falha não solicitada, envia sinal para `reconnectKick`.
- **`new-chat-message`**: `handleChatMessage` (com lock) e emite os eventos
  pendentes + o próprio evento.
- **`any-gift-received`** → `handleGiftReceived` (streak, em `giftstreak.go`).
- **`new-gift-user`** → `handleSettledGiftUser`.
- **`new-like-event`** → reemite `EventNewLike`.
- **`pinned-comment`** → marca usuário como pinado + emite `mark-user-red`.
- **`live-user-connected`, `new-follower`, `new-social-event`, `mark-user-red`** → reemitem.
- **`error`** → loga `data["message"]`.
- **`gifts-list`** → `parseGiftNames` → `cacheAvailableGifts` → envia ao canal
  `giftsCh` e emite `EventGiftsList`.

### `handleChatMessage(data EventData) []pendingEmit`

**DEVE rodar com `m.mu` travado; NUNCA chama `m.emit` dentro do lock**
(`sync.Mutex` não é reentrante). Faz, em ordem:

1. valida o comentário (descarta vazio);
2. detecta **mensagem repetida** (mesmo usuário/comentário em 60s, 3+ vezes →
   evento pendente `flagged-message`, categoria `REPETICAO`, motivo
   "Mensagem repetida"; alerta não se repete enquanto a sequência segue e é
   esquecido ao cair abaixo do limiar);
3. adiciona ao `chatBuffer` (poda para `chatBufferMax`);
4. detecta pergunta (`looksLikeQuestion`) → `questionBuffer` + `pruneQuestions`;
5. detecta keyword (`detectKeyword` → presentes-alvo nos settings) → marca o
   usuário como pinado + eventos pendentes `keyword-mention` e `mark-user-red`.

Retorna a lista de eventos **pendentes** para o chamador emitir fora do lock.

### `handleTargetGift(data EventData)`

Se o presente for alvo (`isTargetGift`) e a liquidação estiver contando
(`isGiftCountingSettlement`), emite `new-gift-user` e dispara
`correlateGiftWithQuestion` em goroutine. Sempre marca `data["isRed"]`
(presente-alvo **e** usuário pinado/vermelho).

---

## `bridge.go` — Processo Node e supervisor de reconexão

### Processo bridge

| Função | Descrição |
|---|---|
| `resolveBridgePath() (string, error)` | Localiza `bridge.js` em múltiplos cenários: relativo ao executável (`./internal/monitor/bridge.js`, `./bridge.js`, diretório-pai), cwd e diretório-fonte (`go run`, via `runtime.Caller`). |
| `resolveNodeWorkDir(bridgePath)` | Acha o diretório que contém `node_modules/tiktok-live-connector` (cwd + até 5 níveis sobe a partir do bridge). |
| `startBridge() error` | Sobe `node bridge.js` com `NODE_PATH`, pipes de stdin/stdout/stderr (stderr → log), guarda o processo, recria `bridgeEnded` e inicia `readBridge`. |
| `stopBridge()` | Mata o processo Node (`Process.Kill` + `Wait`, best-effort). |
| `sendBridge(cmd map[string]interface{}) error` | Escreve um comando JSON + `\n` no stdin do Node. |
| `readBridge()` | Lê o stdout do Node linha a linha, faz `json.Unmarshal` em `bridgeMsg{Type, Data}` e chama `handleBridgeEvent`. Fecha `bridgeEnded` ao sair. |
| `bridgeMsg` | Estrutura de mensagem recebida do Node. |
| `dataToEvent(raw interface{}) EventData` | Converte o `data` do JSON do Node em `EventData` (string vira `{"uniqueId": ...}`). |

### Supervisor de reconexão

| Função | Descrição |
|---|---|
| `backoffDelay(attempt int) time.Duration` | `base × 2^(attempt-1)` (cap `max`, shift limitado a 30) + jitter aleatório até `jitterPct`. |
| `startSupervisor(ctx)` | Inicia a goroutine do supervisor (idempotente). |
| `stopSupervisor()` | Fecha `stopCh`, cancela o contexto e espera a goroutine encerrar. |
| `runSupervisor(ctx, stopCh, done)` | Loop: aguarda `bridgeEnded` (processo morreu) ou `reconnectKick` (conexão caiu). Emite `connection-status` com `success=false` (incluindo `retries`/`nextRetryInMs`), espera `backoffDelay`, reinicia o bridge e reenvia `connect`. Sai se `userStopped`, sem live ou ctx cancelado. |

---

## `giftstreak.go` — Streaks de presente (combos)

Quando o TikTok envia um presente em combos, o conector emite o 1º evento com
`repeatEnd=false`; o evento final (`repeatEnd=true`) pode **nunca chegar**
(amostragem do TikTok). O monitor então:

Constantes (atomics, ajustáveis por testes):
```go
giftStreakSettleTimeout     = 20s  // liquida streak órfão
giftStreakKeepAfterSettle   = 90s  // janela para suprimir finais atrasados
```

| Função | Descrição |
|---|---|
| `giftStreak` | Estado de um streak: `data`, `lastCount` (maior count visto), `settledEmitted`, `timer`. |
| `giftStreakKey(data)` | Chave do streak: `usuário|giftId` (fallback `usuário|giftName`), minúsculas. |
| `handleGiftReceived(data)` | `repeatEnd=true` → liquida direto (emite `any-gift-received`); se o streak já foi liquidado por timeout, **suprime** o final tardio. `repeatEnd=false` → registra/atualiza streak (maior `repeatCount`), reagenda o timer de `giftStreakSettleTimeout`. |
| `settleGiftStreak(key)` | Timeout: marca `settledEmitted`, emite `any-gift-received` com `repeatEnd=true` e o maior count, e roda `handleTargetGift`. Mantém a entrada no mapa por `giftStreakKeepAfterSettle` (90s) para suprimir o final atrasado. |
| `handleSettledGiftUser(data)` | Processa `new-gift-user` — suprimido se o streak já foi liquidado por timeout. |
| `isGiftCountingSettlement(data)` | Para `giftType != 1` sempre conta; para tipo 1 exige `repeatEnd=true` (ou ausente). |

---

## `session.go` — Reuso/purga de dados de sessão

Chamado por `StartMonitoring` quando há repositório injetado.

| Função | Descrição |
|---|---|
| `sessionReusable(last, now) bool` | Sessão reutilizável se a última atividade é no **mesmo dia UTC** e há menos de `sessionReuseMaxAge` (10h). |
| `restoreOrPurgeSessionData()` | Se a sessão de ontem/mais antiga for reutilizável, recarrega dados de hoje (`loadTodayData`); senão apaga (`DeleteSessionData`). |
| `loadTodayData()` | Recarrega mensagens de hoje (reconstrói `chatBuffer`/`questionBuffer`) e anomalias de hoje (marca `pinnedUsers`) ao reconectar na mesma live. |

---

## `availablegifts.go` — Catálogo de presentes

| Função | Descrição |
|---|---|
| `FetchAvailableGifts() ([]string, error)` | Retorna o catálogo cacheado; se vazio e o bridge está no ar, pede `fetch-gifts` (assíncrono) e retorna vazio (a resposta chega por `gifts-list`). |
| `requestAvailableGifts()` | Envia `fetch-gifts` ao bridge. |
| `CachedAvailableGifts() []string` | Cópia do último catálogo não vazio (nil se vazio). |
| `cacheAvailableGifts(names []string)` | Guarda uma cópia (ignora listas vazias). |
| `parseGiftNames(data) []string` | Extrai lista de nomes de `data["gifts"]` (suporta `[]string` e `[]interface{}`). |

---

## `helpers.go` — Utilitários de parsing/normalização

| Função | Descrição |
|---|---|
| `toInt(v) (int, bool)` | Conversão de número (float64/int/int64). |
| `extractFromData(data) UserInfo` | Monta `UserInfo` (nickname cai para uniqueId; flag de seguidor). |
| `asString(v) string` | Converte valores JSON para string (float/int → sem decimais; trim). |
| `parseFollowerFlag(v) (*bool, bool)` | Interpreta 1/2/true (e strings) como seguidor; 0/false como não. |
| `normalizeID(value)` | Trim + lower (identidade de usuário). |
| `foldText(s)` | Trim + lower + remove acentos/combinações unicode (comparação de texto). |
| `looksLikeQuestion(comment) bool` | Heurística PT-BR de pergunta: `?`/`¿` ou inícios/pistas (`pq`, `por que`, `como`, `qual`, `tem como`, `da pra`, ...). |
| `detectKeyword(comment) string` | Se o comentário contém algum `TargetGifts` (settings); retorna a keyword. |
| `isTargetGift(name) bool` | Presente-alvo por substring (com e sem acentos/caracteres não alfanuméricos). |
| `truthy(v) bool` | Coerção booleana (números, strings, bool). |
| `parseStoredTimestampMillis(raw, fallback)` | Timestamp do banco → ms (vários layouts). |
| `coalesce(values ...string) string` / `coalesceStr(val, fallback)` | Primeiro valor não vazio. |

---

## `correlation.go` — Correlação presente ↔ pergunta (determinística, sem IA)

Quando um usuário manda um **presente-alvo**, o monitor tenta correlacioná-lo
com uma pergunta/mensagem do doador na janela recente de chat.

### Tipos
| Tipo | Descrição |
|---|---|
| `correlationPick` | Escolha de correlação: `match QuestionEntry`, `method string`, `confidence string`. |

### Fluxo e funções

| Função | Descrição |
|---|---|
| `correlateGiftWithQuestion(gift GiftPayload)` | Ponto de entrada (goroutine). Poda perguntas, deduplica candidatas (perguntas + chat recente, janela de 3min, até 8), aplica heurística. **Caminho rápido**: 1 mensagem do doador → emite com confiança alta. **Fallback**: heurística escolhe a melhor mensagem (confiança baixa). Sem candidata → log `NO_CANDIDATES`/`NO_MATCH`. |
| `sameUserCandidates(gift, candidates)` | Filtra candidatas do próprio doador (por uid, fallback apelido). |
| `chooseQuestionHeuristic(gift, questions, recent) *correlationPick` | Ordem de preferência: pergunta do próprio doador (`same-user-question`, alta) → mensagem recente do doador (`same-user-recent-message`, alta) → mensagem recente com apelido (`same-nickname-recent-message`, média) → pergunta que menciona apelido (`nickname-mention`, média). |
| `scheduleForwardReview(correlationID, gift, base)` | Após `correlationForwardDelay` (4s), revisa mensagens **posteriores** à escolhida (até 2) e, se alguma pontuar melhor, emite um **substituto** (`replacement=true`, método `...-forward-2`). |
| `emitCorrelation(correlationID, gift, pick, method, confidence, replacement)` | Emite `EventGiftQuestionCorr` com `correlationId`, presente, pergunta, `method`, `confidence`. |
| `recentChatCandidatesLocked(chat, now)` | Converte chat recente (janela) em candidatas (≤40). |
| `getForwardMessages(base, chat, limit)` | Mensagens posteriores à base; prioriza as do mesmo autor. |
| `scoreCorrelationCandidate(candidate) float64` | Pontua texto: +3 pergunta, +1 `?`, +1 pista de pergunta, +0.5 tamanho entre 8 e 220. |
| `sameMessageIdentity(a, b)` | Mesma identidade (uid; fallback apelido). |
| `reversedEntries(in)` | Inverte a ordem (mais recentes primeiro). |
| `dedupeCandidates(questions, recent, now)` | União deduplicada cronológica (≤8). |
| `correlationIDFor(gift, now)` | Id único `corr-<user>-<now>-<now%100000>`. |
| `displayUser(uniqueID, nickname)` | Nome para log. |
| `logCorrelation(event, gift, pick)` | Log estruturado do resultado da correlação. |

---

## Notas de concorrência (importante)
- `sync.Mutex` não é reentrante: `handleChatMessage` **muta estado e retorna
  eventos pendentes** para que `m.emit` (que trava o mutex) nunca seja chamado
  de dentro do lock.
- Parâmetros de backoff/streak usam **atomics** porque goroutines de produção
  (supervisor, timers) leem em paralelo com testes escrevendo.

## Diagramas relacionados
- `diagrams/01-fluxo-eventos.puml` — caminho completo de um evento.
- `diagrams/05-gift-streak-correlacao.puml` — streaks + correlação.
- `diagrams/06-reconexao-monitor.puml` — supervisor de reconexão.