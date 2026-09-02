# Pacote `database` — Persistência PostgreSQL (Supabase)

> Diretório: `backend/internal/database/`

O pacote `database` **implementa a interface `model.Repository`** sobre o
PostgreSQL (Supabase), usando `database/sql` + driver `pgx` (stdlib). Além das
consultas, ele é responsável por:

- **Migrar/garantir o schema** na subida (`migratePostgres`);
- **Traduzir queries com placeholder `?`** para o formato `$n` do Postgres
  (`driver.go`);
- **Cache write-behind de mensagens** para reduzir escrita no banco
  (`messagecache.go`).

Convenções importantes do schema:
- A coluna de usuário é `"uniqueId"` (camel case, aspas duplas no Postgres);
  o helper `bind()` corrige isso automaticamente.
- Não há **foreign keys** reais: as relações são por `live_name`/`uniqueId`
  (convenção da aplicação). Veja o diagrama `diagrams/02-banco-er.puml`.
- Timestamps são `TIMESTAMPTZ` gravados/escritos em UTC; datas de dia
  (`day`) são `DATE`.

---

## Arquivos

| Arquivo | Papel |
|---|---|
| `postgres.go` | Conexão (`OpenPostgres`, `OpenFromEnv`), pool, migração do schema e helpers de teste (`CreateTestDatabase`). |
| `driver.go` | Wrappers de execução que traduzem `?` → `$n` e citam `"uniqueId"`. |
| `database.go` | Tipo `DB` (`*sql.DB` + `sync.Mutex`), `Close()`, `closeRows` e verificação de interface em tempo de compilação. |
| `messages.go` | `UserMessageRepository` + tipo de lote `UserMessageEntry`. |
| `gifts.go` | `GiftRepository`. |
| `engagement.go` | `LikeRepository` + `ShareRepository`. |
| `anomalies.go` | `FeedbackRepository` + `AnomalyRepository` (moderação). |
| `targetgifts.go` | `TargetGiftHistoryRepository`. |
| `goals.go` | `GoalRepository`. |
| `pinned.go` | `PinnedCommentRepository`. |
| `sessions.go` | `SessionRepository`, `ExecSQL` (crude, p/ testes) e `parseStoredTime`. |
| `lives.go` | `RankingRepository` (lives derivadas, agregação por usuário). |
| `settings.go` | `SettingsRepository`. |
| `messagecache.go` | `MessageCache`: buffer em memória com flush em lote para `user_messages`. |

---

## `postgres.go` — Conexão, pool e schema

### Funções

| Função | Descrição |
|---|---|
| `OpenPostgres(dsn string) (*DB, error)` | Abre o pool `pgx`, configura limites (`SetMaxOpenConns`, `SetMaxIdleConns`, `SetConnMaxLifetime(30m)`, `SetConnMaxIdleTime(5m)` — recicla conexões ociosas que provedores cloud derrubam) e roda `migratePostgres`. |
| `maxConns() int` | Tamanho do pool via env `DB_MAX_CONNS` (padrão **20**). |
| `OpenFromEnv() (*DB, error)` | Abre usando `DATABASE_URL` (obrigatória). Usado por `main.go`. |
| `migratePostgres() error` | Executa `CREATE TABLE IF NOT EXISTS` / índices para garantir o schema (ver tabelas abaixo). |
| `safeTestDBName(name string) bool` | Valida nome de banco de teste (minúsculas/dígitos/`_`, ≤63 chars). |
| `dsnForDatabase(baseDSN, name string) (string, error)` | Deriva um DSN apontando para outro database dentro da mesma URL `postgres://`. |
| `CreateTestDatabase(baseDSN string) (dsn string, cleanup func(), err error)` | Cria um banco descartável `tlm_test_<nano>` para testes e devolve uma função de limpeza (`DROP DATABASE ... WITH (FORCE)`). **Nunca aponte para produção.** |

### Schema criado por `migratePostgres`

| Tabela | Colunas principais | Uso |
|---|---|---|
| `false_positives` | `comment`, `category`, `expected` (padrão `'NAO'`), `timestamp` | Feedback de falso-positivo consumido pelo agente Python. |
| `anomaly_logs` | `live_name`, `day` (DATE), `"uniqueId"`, `comment`, `is_anomaly`, `category`, `timestamp` | Logs de moderação. |
| `gifts` | `live_name`, `"uniqueId"`, `nickname`, `gift_name`, `repeat_count`, `gift_type`, `timestamp` | Presentes recebidos. |
| `shares` | `live_name`, `"uniqueId"`, `nickname`, `timestamp` | Compartilhamentos. |
| `likes` | `live_name`, `"uniqueId"`, `nickname`, `like_count`, `timestamp` | Curtidas por evento (amostra do stream). |
| `room_like_totals` | `live_name` (PK), `total`, `updated_at` | Contador acumulado **oficial** de curtidas da sala (monotônico). |
| `user_messages` | `live_name` (default `''`), `"uniqueId"`, `username`, `message`, `timestamp` | Mensagens de chat (deduplicadas). |
| `target_gift_history` | `live_name`, `"uniqueId"`, `nickname`, `gift_name`, `received_at`, `answered_at`, `response_type` | Presentes-alvo recebidos/respondidos. |
| `gift_goals` | `live_name`, `title`, `gift_name` (default `''`), `target_units`, `status`, `milestones` (JSON TEXT default `'[]'`), `created_at`, `completed_at` | Metas de presentes. |
| `pinned_comments` | `live_name`, `"uniqueId"`, `nickname`, `comment`, `pin_id`, `is_follower` (INTEGER 0/1), `timestamp` | Comentários fixados. |
| `settings` | `key` (PK), `value` | Configurações chave/valor (ex.: presentes-alvo). |

Índices extras:
- `idx_pinned_comments_pin` — único parcial em `(live_name, pin_id)` onde
  `pin_id` não é vazio (evita duplicar pin).
- `idx_user_messages_dedup` — em `(LOWER("uniqueId"), LOWER(message))` para
  acelerar a deduplicação.

---

## `driver.go` — Adaptador de queries

As queries do projeto são escritas com placeholder `?` (estilo SQLite) e o
driver converte para `$n` do Postgres.

### Funções

| Função | Descrição |
|---|---|
| `rebindQuery(query string) string` | Converte cada `?` fora de literais `'...'` em `$1, $2, ...` (ignora `?` dentro de strings, tratando `''` como escape). |
| `bind(query string) string` | Aplica `rebindQuery` e cita a coluna `uniqueId` como `"uniqueId"`. |
| `exec(query, args...) (sql.Result, error)` | Executa uma instrução (INSERT/UPDATE/DELETE) com `bind`. |
| `query(query, args...) (*sql.Rows, error)` | Executa um SELECT com `bind`. |
| `queryRow(query, args...) *sql.Row` | Executa um SELECT de linha única com `bind`. |
| `insertID(query, args...) (int64, error)` | Executa um INSERT e retorna o `id` via `RETURNING id`. |
| `upsertRoomLikeTotal(liveName string, total int64) error` | UPSERT que mantém o **maior** total de curtidas da sala (`ON CONFLICT ... GREATEST`). |

> O `DB` (definido em `database.go`) expõe um `sync.Mutex` (`mu`) e todos os
> métodos públicos travam o mutex — o driver é seguro para uso concorrente.

---

## Métodos de `model.Repository` (um arquivo por domínio)

O `DB` (em `database.go`) encapsula `*sql.DB` + mutex e implementa cada
sub-interface no arquivo do seu domínio (ver tabela de arquivos acima).
Abaixo, os métodos agrupados pela interface que satisfazem.

> Convenção de limites: quase todas as leituras com `limit` clampeiam para um
> intervalo seguro (ex.: `1..500`), com um padrão por método.

### FeedbackRepository
| Método | Descrição |
|---|---|
| `GetFalsePositiveComments(limit int) ([]string, error)` | Comentários marcados como falso-positivo (`expected = 'NAO'`), distintos, mais recentes primeiro. |

### AnomalyRepository
| Método | Descrição |
|---|---|
| `LogAnomaly(liveName, comment string, isAnomaly bool, category, uniqueID string) error` | Insere um log de moderação (com `day` = data UTC de hoje). |
| `GetRecentModerations(limit int) ([]AnomalyLog, error)` | Últimos N registros, mais recentes primeiro. |
| `GetRecentAnomalyLogs(limit int)` | Alias de `GetRecentModerations`. |
| `GetAnomalyLogsByLiveName(liveName string)` | Logs de uma live específica. |
| `GetAnomalyLogsByUser(uniqueID string, limit int)` | Logs de um participante (case-insensitive), só `is_anomaly = TRUE`. |
| `GetTodayAnomalyLogs(liveName string)` | Logs de hoje (`day = date('now')`). Usado no restore de sessão. |
| `ClearHistory() (int64, error)` | Apaga **todos** os `anomaly_logs`; retorna linhas afetadas. |
| `DeleteModeration(id int64) (int64, error)` | Remove um log por id. |
| `CleanupOldAnomalies() (int64, error)` | Remove registros anteriores a hoje. |

### UserMessageRepository
| Método | Descrição |
|---|---|
| `AddUserMessageDedup(liveName, uniqueID, username, message string) error` | Insere mensagem **apenas se não existir** para o usuário (case-insensitive) e poda para as **10 mais recentes** do usuário. |
| `BatchAddUserMessages(entries []UserMessageEntry) error` | Insere várias mensagens numa **transação** (INSERT ... WHERE NOT EXISTS), podando cada usuário afetado para 10. Usado pelo `MessageCache`. |
| `GetUserMessages(uniqueID string)` | Todas as mensagens do usuário (recentes primeiro). |
| `GetUserMessagesRecent(uniqueID string, limit int)` | Últimas N mensagens (`limit <= 0` = todas). |
| `GetAllUserMessages() (map[string][]UserMessage, error)` | Todas as mensagens agrupadas por `uniqueId` (usado para perguntas frequentes do relatório). |
| `GetTodayUserMessages(liveName string)` | Mensagens de hoje da live (ordem crescente). Usado no restore de sessão. |

### GiftRepository
| Método | Descrição |
|---|---|
| `AddGift(...) (int64, error)` | Insere um presente e retorna o id. |
| `GetRecentGifts(liveName string, limit int)` | Últimos N presentes da live. |
| `GetGiftsByUser(uniqueID string)` | Presentes de um usuário. |
| `GetGiftSummary() (map[string]map[string]int, error)` | `uniqueId → gift_name → SUM(repeat_count)` (agregado global). |
| `GetGiftUnits(liveName string, giftNames ...string) (units, count int, err error)` | Soma de unidades (`SUM repeat_count`) e nº de eventos. Sem nomes = todos os presentes; senão filtra por `gift_name IN (...)`. Base das metas de presente. |
| `ClearGifts() (int64, error)` | Remove todos os presentes. |

### LikeRepository / ShareRepository
| Método | Descrição |
|---|---|
| `AddLike(liveName, uniqueID, nickname string, likeCount int) error` | Insere evento de curtida (rajada com `like_count`). |
| `UpsertRoomLikeTotal(liveName string, total int64) error` | Guarda o total acumulado da sala (apenas cresce — ver `driver.go`). |
| `LikeTotals(liveName string) (roomTotal, delivered int64, err error)` | Total oficial da sala (`MAX(total)`) e soma das curtidas entregues. |
| `GetUserLikeTotal(uniqueID string) (int64, error)` | Soma `like_count` do usuário. |
| `AddShare(...) error` | Insere um compartilhamento. |
| `GetUserShareCount(uniqueID string) (int, error)` | Total de shares do usuário. |

### TargetGiftHistoryRepository
| Método | Descrição |
|---|---|
| `AddTargetGiftHistory(...) (int64, error)` | Registra presente-alvo recebido (timestamp UTC). |
| `MarkTargetGiftAnswered(id int64, responseType string, answeredAt time.Time) error` | Marca como respondido (`answered_at`, `response_type`) — só se ainda não respondido. Valida `manual`/`automatic`. |
| `GetRecentTargetGiftHistory(liveName string, limit int)` | Histórico recente (filtra por live quando informada). |
| `GetPendingTargetGiftHistory(liveName string, limit int)` | Presentes-alvo **sem** resposta, recentes primeiro. |

### GoalRepository
| Método | Descrição |
|---|---|
| `AddGiftGoal(g GiftGoal) (int64, error)` | Cria meta (valida título/target/live; serializa `milestones` como JSON; `created_at` UTC). |
| `GetGiftGoals(liveName string) ([]GiftGoal, error)` | Metas da live, mais novas primeiro (desserializa milestones). |
| `SaveGiftGoal(g GiftGoal) error` | Atualiza campos mutáveis de uma meta existente. |
| `DeleteGiftGoals(liveName string) (int64, error)` | Remove todas as metas da live. |

### PinnedCommentRepository
| Método | Descrição |
|---|---|
| `AddPinnedComment(...) (int64, error)` | Insere comentário fixado. Com `pin_id` não vazio, evita duplicidade para a mesma live (retorna o id existente). |
| `GetRecentPinnedComments(liveName string, limit int)` | Fixados recentes (filtra por live quando informada). |

### SessionRepository
| Método | Descrição |
|---|---|
| `GetLastSessionActivity(liveName string) (time.Time, bool, error)` | Maior timestamp entre `gifts`, `anomaly_logs`, `user_messages`, `pinned_comments`, `target_gift_history` da live. |
| `DeleteSessionData(liveName string) error` | Transação que remove gifts, anomalias, mensagens, pins e histórico de presente-alvo da live. |

### RankingRepository
| Método | Descrição |
|---|---|
| `LiveFirstSeen(liveName string) (string, error)` | Menor timestamp entre todas as tabelas de evento da live (RFC3339 UTC). |
| `LiveStatsByUser(liveName string) ([]LiveStat, error)` | **Agregação central do ranking**: por usuário, conta mensagens (+ perguntas por heurística SQL), presentes (com `GiftValue` por grupo de presente), shares e likes, com `FirstSeen`/`LastSeen`/nickname mais recente. |
| `RecentLivesForUser(uniqueID string, limit int)` | Últimas N lives em que o usuário apareceu (mensagens/presentes/target gifts), com contagens e janela. |
| `TotalDistinctUsers() (int, error)` | Usuários distintos em `user_messages`. |
| `ListLives(limit int) ([]Live, error)` | Lives **derivadas**: agrupa por `(live_name, day)` com MIN/MAX de tempo e contagem de eventos, mais recentes primeiro. |
| `DeleteLive(liveName string) (int64, error)` | Remove a live de **todas** as tabelas com `live_name` (9 tabelas); retorna total de linhas. |

### SettingsRepository
| Método | Descrição |
|---|---|
| `GetSetting(key string) (string, error)` | Lê valor (vazio + nil quando a chave não existe). |
| `SetSetting(key, value string) error` | UPSERT chave/valor. |

### Perfis (engagement)
| Método | Descrição |
|---|---|
| `GetUserMessagesRecent(uniqueID string, limit int)` | Ver `UserMessageRepository`. |
| `GetUserShareCount(uniqueID string)` / `GetUserLikeTotal(uniqueID string)` | Totais do usuário (usados em `UserProfile`). |

### Infra
| Método | Descrição |
|---|---|
| `ExecSQL(query string, args ...any) error` | Executa statement cru (usado por testes para semear timestamps). |
| `Close() error` | Fecha o pool. |

Verificação em tempo de compilação: `var _ model.Repository = (*DB)(nil)`.

---

## `messagecache.go` — Cache write-behind de mensagens

**Motivação:** cada chat message viraria um INSERT. Sob tráfego alto de uma
live, isso gera muita escrita. O `MessageCache` agrega mensagens em memória e
as grava em lotes no banco.

### Constantes
```go
messageCacheMaxPerUser     = 10   // espelha a poda do banco (10 msgs/usuário)
messageCacheMaxPending     = 50   // dispara flush imediato ao atingir
defaultMessageCacheFlushPeriod = 2 * time.Second
```

### Tipo e funções

| Tipo/Método | Descrição |
|---|---|
| `MessageCache` | Buffer `pending map[uidLower]map[msgLower]messageCacheEntry` + flag `flushing` (impede flush sobreposto) + `stopOnce`/`wg` para shutdown. |
| `NewMessageCache(db *DB) *MessageCache` | Cria o cache apontando para o banco. |
| `SetFlushPeriod(d time.Duration)` | Sobrescreve o período (testes; ignora `d <= 0`). |
| `Add(liveName, uniqueID, username, message string)` | **Sem I/O**: normaliza, deduplica em memória, poda para 10 por usuário. Se o total atingir `messageCacheMaxPending`, dispara `Flush()` em goroutine. |
| `pruneMemory(uniqueID string)` | Mantém as 10 mensagens mais recentes do usuário no buffer (ordenadas por timestamp; caller segura `c.mu`). |
| `Flush()` | Guardado por `flushing`: move o buffer para uma lista de `UserMessageEntry`, limpa o estado e chama `BatchAddUserMessages` (preservando o timestamp de chegada). Em falha, registra perda no log. |
| `Snapshot() []model.UserMessage` | Cópia das mensagens ainda não gravadas (usado para o chat atual). |
| `Start()` | Inicia goroutine (com `wg`) e ticker que chama `Flush()` a cada `period`. |
| `Stop()` | Idempotente (`stopOnce`): fecha o canal `done`, espera a goroutine (`wg.Wait`) e faz um **flush final** (nada é perdido no shutdown). |
| `pendingLen() int` | Total de mensagens no buffer (testes). |

> Janela de perda: mensagens não gravadas se perdem num crash do processo
> (limitada ao `FlushPeriod`). O `Add` nunca faz I/O — segurança de escrita
> sob alto throughput.

---

## Orquestração no `main.go`
A ordem de shutdown é importante (comentários em `main.go`): o `MessageCache`
é registrado com `defer msgCache.Stop()` **após** `defer repo.Close()`, então o
flush final ocorre antes de o banco fechar.
