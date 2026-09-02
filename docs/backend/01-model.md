# Pacote `model` — Estruturas de dados e contratos de repositório

> Diretório: `backend/internal/model/`

O pacote `model` é a camada mais pura do backend: **define as estruturas de
dados (entidades/DTOs)** que trafegam entre as camadas e **os contratos
(interfaces) de acesso a dados** que a camada `database` implementa. Ele não
tem dependências de I/O, banco ou HTTP — apenas de `errors` e `time`.

Aqui também vive a **tabela de preços de presentes TikTok** (em moedas/💎),
usada para valorar presentes no ranking e nos relatórios.

---

## Arquivos

| Arquivo | Papel |
|---|---|
| `entities.go` | Entidades e constantes de domínio (JSON tags). |
| `repository.go` | Interfaces de repositório + erros sentinela + `LiveStat`. |
| `giftvalues.go` | Tabelas de valor (moedas) de presentes EN/PT-BR. |

---

## `entities.go` — Entidades

Todas as entidades são serializadas como JSON para a API (nomes seguem as
`json` tags). Timestamps são, em geral, strings.

### Moderação / anomalias

**`AnomalyLog`** — Um registro de moderação (decisão de sinalizar um comentário).
- `ID int64`, `LiveName string`, `Day string` (data `YYYY-MM-DD`),
  `Timestamp string`, `UniqueID string`, `Comment string`,
  `IsAnomaly bool`, `Category string`.

**`AnomalySummary`** — Agrupamento de logs de anomalia por categoria
(usado no relatório pós-live). Campos: `Category`, `Count`.

### Mensagens / interação

**`UserMessage`** — Uma mensagem de chat de um participante.
- `ID`, `LiveName`, `UniqueID`, `Username`, `Message`, `Timestamp`.

**`PinnedComment`** — Comentário fixado (pinado) pelo streamer durante a live.
- `ID`, `LiveName`, `UniqueID`, `Nickname`, `Comment`, `PinID`, `IsFollower *bool`,
  `Timestamp`.

### Presentes

**`Gift`** — Um presente recebido durante a live (já persistido com o nome
traduzido para PT-BR). Campos: `ID`, `LiveName`, `UniqueID`, `Nickname`,
`GiftName`, `RepeatCount`, `GiftType`, `Timestamp`.

Constantes de resposta de presente-alvo:
```go
TargetGiftResponseManual    = "manual"
TargetGiftResponseAutomatic = "automatic"
```

**`TargetGiftHistory`** — Histórico de quando um **presente-alvo** foi recebido
e respondido.
- `ID`, `LiveName`, `UniqueID`, `Nickname`, `GiftName`, `ReceivedAt`,
  `AnsweredAt *string`, `ResponseType *string` (nullable até ser respondido).

### Metas de presentes (gift goals)

Constantes de status:
```go
GoalStatusActive    = "active"
GoalStatusCompleted = "completed"
GoalStatusCancelled = "cancelled"
```

**`GoalMilestone`** — Um marco de progresso dentro de uma meta: quando a live
atinge `AtUnits` unidades, a recompensa `Reward` é concedida.
- `AtUnits int`, `Reward string`, `Unlocked bool`, `UnlockedAt *string`.

**`GiftGoal`** — Uma meta de presentes: alvo em unidades (SOMA de `repeat_count`).
Quando `GiftName` é vazio, conta **todos** os presentes da live; caso contrário,
apenas as unidades do presente indicado.
- `ID`, `LiveName`, `Title`, `GiftName`, `TargetUnits`, `Status`,
  `Milestones []GoalMilestone`, `CompletedAt *string`, `CreatedAt`.

### Ranking / perfis

Constantes de nível de risco:
```go
RiskLevelNone     = "none"
RiskLevelLow      = "low"
RiskLevelMedium   = "medium"
RiskLevelHigh     = "high"
RiskLevelCritical = "critical"
```

Constantes de tier visual (ranking de presenteador, espelha o TikTok):
```go
TierCrown    = "crown"    // 1º lugar
TierHeadband = "headband" // 2º lugar
TierMedal    = "medal"    // 3º lugar
```

Constantes de modo de ranking:
```go
ModeEngagement = "engagement" // pontuação ponderada padrão
ModeTikTok     = "tiktok"     // ranking da sala do TikTok: só valor de presente (💎)
```

**`UserRank`** — Posição de um participante no ranking de uma live.
- `UniqueID`, `Nickname`, `Score float64`, `GiftScore float64`,
  `Diamonds int` (valor total em moedas dos presentes — métrica do ranking
  da sala TikTok), `Tier` (crown/headband/medal só no top 3),
  `MessageCount`, `QuestionCount`, `GiftCount`, `ShareCount`, `LikeCount`,
  `AnomalyCount`, `RiskLevel`, `FirstSeen`, `LastSeen`.

**`LiveRanking`** — Ranking completo de uma live.
- `LiveName`, `UpdatedAt`, `TotalUsers`, `UserRanks []UserRank`, `Mode`,
  `TotalGiftValue int` (soma em 💎 da sala), `TotalLikes int64` (contador
  acumulado oficial da sala informado pelo stream).

### Relatório pós-live

**`LiveReport`** — Resumo pós-live (geração determinística, sem IA).
- `LiveName`, `StartedAt`, `EndedAt`, `DurationMinutes`,
  `MessageCount`, `ParticipantCount`, `GiftCount`, `GiftTotal`,
  `GiftValue` (valor em 💎), `TopSupporters []UserRank`,
  `FrequentQuestions []string`, `ModerationIssues []AnomalySummary`,
  `Summary string` (hoje não preenchido por IA).

### Perfil de usuário

**`UserProfile`** — Histórico agregado de um participante entre lives.
- `UniqueID`, `Nickname`, `Messages []UserMessage`, `Gifts []Gift`,
  `Alerts []AnomalyLog`, `RiskLevel`,
  `TotalMessages`, `TotalGifts`, `TotalGiftUnits` (soma de `repeat_count`),
  `TotalGiftValue` (valor em 💎), `TotalLikes`, `TotalShares`,
  `LastLives []UserLiveSummary`.

**`UserLiveSummary`** — Resumo de uma live em que o participante apareceu.
- `LiveName`, `Messages`, `Gifts`, `FirstSeen`, `LastSeen`.

### Lives derivadas

**`Live`** — Visão derivada de uma live em um dia (intervalo e contagem de
eventos). **Não existe tabela própria de lives/agendas**: é agregado a partir
das tabelas que carregam `live_name` (`user_messages`, `gifts`, `shares`,
`anomaly_logs`, `pinned_comments`, `target_gift_history`).
- `Name`, `Day`, `StartedAt`, `EndedAt`, `Events`.

---

## `repository.go` — Contratos de acesso a dados

Define **interfaces** consumidas por `controller`, `monitor`, `ranking` e
`report`. A implementação concreta é o pacote `database` (PostgreSQL/Supabase).
Centralizar os contratos aqui permite testar a lógica de negócio com repositórios
falsos (ex.: `cmd/sseload`).

### Erros sentinela
```go
ErrCommentRequired   // "comment is required"
ErrInvalidID         // "invalid id"
ErrUniqueIDRequired  // "uniqueId is required"
```

### Interfaces (e responsabilidade)

| Interface | Responsabilidade |
|---|---|
| `FeedbackRepository` | Consumo **somente-leitura** do feedback do usuário persistido pelo agente Python (`false_positives`): `GetFalsePositiveComments(limit)`. |
| `AnomalyRepository` | Persistência de logs de moderação (`LogAnomaly`, `GetRecentModerations`, `GetRecentAnomalyLogs`, `GetAnomalyLogsByLiveName`, `GetAnomalyLogsByUser`, `GetTodayAnomalyLogs`, `ClearHistory`, `DeleteModeration`, `CleanupOldAnomalies`). |
| `UserMessageRepository` | Persistência de mensagens de usuário (`AddUserMessageDedup`, `GetUserMessages`, `GetUserMessagesRecent`, `GetAllUserMessages`, `GetTodayUserMessages`). |
| `GiftRepository` | Persistência de presentes (`AddGift`, `GetRecentGifts`, `GetGiftsByUser`, `GetGiftSummary`, `GetGiftUnits`, `ClearGifts`). |
| `TargetGiftHistoryRepository` | Histórico de presente-alvo recebido/respondido (`AddTargetGiftHistory`, `MarkTargetGiftAnswered`, `GetRecentTargetGiftHistory`, `GetPendingTargetGiftHistory`). |
| `GoalRepository` | Metas de presentes da live (`AddGiftGoal`, `GetGiftGoals`, `SaveGiftGoal`, `DeleteGiftGoals`). |
| `PinnedCommentRepository` | Comentários fixados (`AddPinnedComment`, `GetRecentPinnedComments`). |
| `SessionRepository` | Reuso/purga de dados de sessão da live na (re)conexão (`GetLastSessionActivity`, `DeleteSessionData`). |
| `ShareRepository` | Compartilhamentos da live (`AddShare`, `GetUserShareCount`). |
| `LikeRepository` | Curtidas (`AddLike`, `GetUserLikeTotal`, `UpsertRoomLikeTotal`, `LikeTotals`). |
| `RankingRepository` | Estatísticas e análises de live/perfil (`LiveFirstSeen`, `LiveStatsByUser`, `RecentLivesForUser`, `TotalDistinctUsers`, `ListLives`, `DeleteLive`). |
| `SettingsRepository` | Configurações persistidas chave/valor (`GetSetting`, `SetSetting`) — ex.: presentes-alvo sobrevivem a restart. |
| `Repository` | Compõe todas as interfaces acima + `Close()`. |

### `LiveStat` — Dados agregados por usuário (usados para pontuar)

Estrutura de entrada do ranking (preenchida por `LiveStatsByUser`):
- `UniqueID`, `Nickname`, `MessageCount`, `QuestionCount`, `GiftCount`,
  `GiftTotal` (soma de unidades), `GiftValue` (valor em 💎 por presente×repetição),
  `ShareCount`, `LikeCount`, `FirstSeen`, `LastSeen`.

---

## `giftvalues.go` — Valor (moedas 💎) dos presentes

Tabela de preços pesquisada publicamente (ago/2026) de presentes da live do
TikTok. **`GiftValue(name)`** é a métrica usada pelo ranking "modo TikTok",
relatórios e perfis: número de moedas que o espectador gasta por unidade do
presente.

- `giftValuesEnglish` — mapa nome em inglês → moedas.
- `giftValuesPortuguese` — mapa com os nomes **traduzidos** (como ficam no
  banco) → moedas; chaves sem acento (veja `normalizeGiftValueKey`).
- `defaultGiftValue = 1` — valor aplicado a presentes fora da tabela
  (equivalente a uma Rosa, o presente mais barato).

### Funções

| Função | Descrição |
|---|---|
| `GiftValue(name string) int` | Retorna o valor em moedas de **uma unidade** do presente. Aceita nome EN ou PT-BR (sem acento). Desconhecidos → `1`. |
| `normalizeGiftValueKey(name string) string` | Minúsculas + trim + remove acentos/combinações unicode, para que nomes como "Galáxia"/"Fênix" casem com as chaves da tabela. |

---

## Relação com os diagramas
- Estruturas e contratos: ver `diagrams/00-arquitetura.puml` e
  `diagrams/02-banco-er.puml`.
