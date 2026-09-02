# Pacote `controller` — Orquestração de serviços

> Diretório: `backend/internal/controller/`

O pacote `controller` é a **camada de orquestração**: ele conecta o `monitor`
(que produz eventos), o repositório (`model.Repository`, que persiste), o
`ranking`, o `report` e o cache de mensagens. A camada View (`internal/view`)
chama métodos do `AppController` e registra handlers de evento que disparam os
fluxos de persistência.

Arquivos:
| Arquivo | Papel |
|---|---|
| `app.go` | `AppController`, ações de monitor/settings/moderação, handlers de eventos (gift/chat/share/like), ranking/report/profile e helpers de extração de dados de `EventData`. |
| `gifts.go` | `giftTranslations` + `translateGiftName` (dicionário PT-BR espelhado do Node). |
| `goals.go` | Metas de presentes: progresso, milestones, conclusão e callback de update. |

---

## `app.go` — `AppController`

### Constantes e tipos
| Item | Descrição |
|---|---|
| `settingsKey = "app"` | Chave da tabela `settings` em que as configurações são persistidas (JSON). |
| `MessageCache` (interface) | Contrato mínimo do cache: `Add(...)` e `Snapshot()`. Implementado por `database.MessageCache`. |
| `AppController` | Struct principal com `monitor`, `repo`, `msgCache`, `reportGen`, `ranker`, `monCancel`, `flagSeen` (dedup de flags externos), e `goals goalState`. |

### Construtor e ciclo de vida

| Função | Descrição |
|---|---|
| `NewAppController(mon, repo) *AppController` | Injeta repo no monitor, cria `report.Generator` e `ranking.Ranker(DefaultWeights)`, e **restaura settings persistidas** (lê `settings["app"]` e faz `mon.SetSettings`). |
| `Stop()` | `monitor.Close()` (mata bridge — shutdown do app). |
| `SetMessageCache(mc MessageCache)` | Liga o cache write-behind. |

### Ações de monitoramento

| Função | Descrição |
|---|---|
| `StartMonitoring(ctx, username) error` | Cria um contexto cancelável próprio e delega a `monitor.StartMonitoring`. |
| `StopMonitoring()` | Cancela o contexto (`monCancel`) e chama `monitor.StopMonitoring()`. |
| `GetState() monitor.State` | Estado atual do monitor. |
| `GetSettings() monitor.Settings` | Config atual. |
| `SetSettings(settings)` | Aplica e **persiste** (JSON em `settings["app"]`). |
| `FetchAvailableGifts() ([]string, error)` | Catálogo de presentes (garante slice não-nil). |
| `GetMonitor() *monitor.Monitor` | Expõe o monitor (usado pela View para `OnEvent`). |

### Moderação (flags externos)

| Função | Descrição |
|---|---|
| `ReportExternalFlag(data)` | Injeta uma flag de moderação externa no pipeline: exige `ModerationEnabled`; extrai `comment`/`category`; **deduplica** por `uid|comentário` (`flagSeen`, máx. 500); emite `flagged-message` e grava `LogAnomaly`. |
| `foldComment(s) string` | Normaliza comentário (lower/trim/remove acentos) para a chave de dedup. |
| `GetRecentModerations(limit)` / `DeleteModeration(id)` / `ClearHistory()` | Delegam para o repo (histórico de moderação). |

### Presentes / presentes-alvo

| Função | Descrição |
|---|---|
| `GetRecentGifts(liveName, limit)` / `GetGiftsByUser(userID)` / `ClearGifts()` | Delegam ao repo. |
| `RecordTargetGiftReceived(data) (int64, error)` | Registra um presente-alvo recebido (resolve usuário/nome, converte timestamp ms→time). |
| `AnswerTargetGift(id, responseType) error` | Marca um presente-alvo como respondido (agora). |
| `GetRecentTargetGiftHistory(limit)` | Histórico recente da live atual. |
| `GetPendingTargetGiftHistory(limit)` | Pendentes (não respondidos) da live atual (vazio se sem live). |
| `RecordPinnedComment(data) (int64, error)` | Persiste comentário fixado (com panico recover). |
| `GetRecentPinnedComments(limit)` | Fixados recentes da live atual. |

### Lives / ranking / report / perfil

| Função | Descrição |
|---|---|
| `GetLives(limit)` | Lives derivadas (admin). |
| `DeleteLive(liveName)` | Apaga tudo de uma live. |
| `GetLiveRanking(liveName, mode)` | Monta `LiveRanking`. **Modo `tiktok`** → `ranker.BuildTikTokRanking` (só valor de presente, sem penalidade). **Padrão** → `BuildLiveRanking` com anomalias por usuário. Injeta `TotalLikes` = total oficial da sala (`LikeTotals`). |
| `GenerateReport(ctx, liveName)` | Delega ao `report.Generator`. |
| `GetUserProfile(uniqueID)` | Perfil histórico: mensagens recentes (10), gifts, últimas lives (10); soma unidades e valor; nickname best-effort; nível de risco a partir das anomalias (`riskForUser`). |
| `riskForUser(logs, uniqueID)` | Classifica risco: ≥4 anomalias → critical; ≥2 → medium; ≥1 → low; senão none. |

### Handlers de eventos (disparados pela View via `monitor.OnEvent`)

| Função | Descrição |
|---|---|
| `HandleGiftEvent(data)` | Persiste presente (`AddGift`) com nome **traduzido** e `repeatCount`/`giftType` sanitizados; ignora streak em andamento (`isGiftStreakInProgress`); no fim chama `checkGoalProgress()`. Com `panic recover`. |
| `HandleChatMessageEvent(data)` | Se `msgCache` estiver ligado, enfileira no cache (sem I/O); senão grava direto com dedup. |
| `HandleShareEvent(data)` | Persiste um share. |
| `HandleLikeEvent(data)` | Persiste curtida por evento; se o payload traz `total` (contador oficial da sala), chama `UpsertRoomLikeTotal` (monotônico). |

### Helpers de extração de `EventData`

| Função | Descrição |
|---|---|
| `toInt64(v) (int64, bool)` | Converte número/string para int64. |
| `isGiftStreakInProgress(data)` | `repeatEnd` presente e `false` → streak em andamento (não grava). |
| `eventInt(data, key, fallback)` / `eventString(data, keys...)` / `stringify(v)` / `eventBoolPtr(data, key)` | Extração segura tipada de campos. |
| `nestedString(v, keys...)` | Extrai string de um sub-objeto. |
| `resolveGiftName(data)` | Resolve nome do presente em múltiplos campos/aninhamentos; fallback `"Presente <id>"`; **traduz** via `translateGiftName`. |

---

## `gifts.go` — Tradução PT-BR

| Item | Descrição |
|---|---|
| `giftTranslations` | Mapa `en → PT-BR` (~190 entradas). Mesmo dicionário de `monitor/gifts.js`. |
| `translateGiftName(name string) string` | Busca por chave minúscula; sem match devolve o original. |

---

## `goals.go` — Metas de presentes (gift goals)

### Tipos de resposta

| Tipo | Descrição |
|---|---|
| `GoalProgress` | Uma meta + progresso atual: `Goal GiftGoal`, `Units int`, `Percent float64`. |
| `GoalUpdate` | Payload do callback: `Progress`, `UnlockedMilestones`, `NewlyUnlockedMilestones`, `Completed bool`. |
| `GoalsState` | Estado completo: `LiveName`, `Active *GoalProgress` (alias legado = primeira ativa), `Actives []GoalProgress`, `History []GiftGoal`. |
| `goalState` | Estado interno do controller p/ metas: `mu`, `callback func(GoalUpdate)`, `lastUnits map[int64]int` (evita emitir update repetido quando nada mudou além da barra). |

### Funções

| Função | Descrição |
|---|---|
| `SetGoalCallback(fn func(GoalUpdate))` | Registra callback de progresso (usado pela View para broadcasts SSE). |
| `CreateGoal(title, giftName, targetUnits, milestones)` | Cria meta **ativa** para a live atual. `giftName` vazio = conta todos os presentes. Persiste via `AddGiftGoal`. |
| `UpdateGoal(g)` | Persiste campos mutáveis de uma meta existente (valida id). |
| `CancelGoal(id)` | Marca a meta ativa da live como `cancelled`. |
| `CompleteGoal(id)` | Marca a meta ativa como `completed`, cruzando milestones já atingidos pelas unidades atuais. |
| `activeGoalByID(id)` | Busca a meta **ativa** com o id na live atual. |
| `GetGoalsState()` | Monta `GoalsState` com progresso percentual de cada ativa. |
| `checkGoalProgress()` | Chamado após `HandleGiftEvent`: para cada meta ativa da live, roda `checkSingleGoal`. Com `panic recover`. |
| `checkSingleGoal(active)` | Recalcula unidades (filtrando pelo gift da meta), cruza milestones, completa ao atingir o alvo, persiste mudanças e **emite update quando as unidades mudam** (mesmo sem milestone/conclusão). |
| `unitsChanged(goalID, units)` | True se unidades diferem da última emissão (tracker `lastUnits`; zera se > 128 entradas). |
| `crossMilestones(active, units)` | Desbloqueia milestones não desbloqueados com `AtUnits <= units`; retorna `(changed, newlyUnlocked)`. |
| `emitGoalUpdate(goal, units, completedNow, newlyUnlocked)` | Chama o callback (se registrado) com o `GoalUpdate`. |
| `goalGiftNames(giftName)` | Retorna o nome escolhido + a tradução PT-BR (gifts são gravados traduzidos). |
| `progressPercent(units, target)` | `units/target*100` clampado em `[0,100]`. |

> **Regra de ouro:** metas ativas contam as **unidades** (soma de
> `repeat_count`) — não o número de eventos nem o valor em moedas.

---

## Diagramas relacionados
- `diagrams/01-fluxo-eventos.puml` — persistência de cada evento.
- `diagrams/00-arquitetura.puml` — posição do controller.
