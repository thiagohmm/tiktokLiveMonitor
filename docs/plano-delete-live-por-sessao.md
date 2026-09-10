# Plano: exclusão de live por ID de sessão

**Objetivo:** ao clicar em "Deletar" numa linha da administração, apagar **somente
aquela live** — tudo o que aconteceu naquela conexão — e nunca o histórico inteiro
do streamer.

**Decisões do owner:**
1. Criar um **ID por live iniciada** (nova sessão a cada início de monitor); o delete
   apaga por esse ID.
2. O delete remove **tudo o que aconteceu naquela live**: eventos, metas e contador de
   curtidas daquela sessão.
3. Contrato da API: `live` **e** `day` obrigatórios; sem eles → `400`.

> **Status da revisão (DeepSeek `reviewer`):** v1 REJEITADA (3 críticos) → v2
> "APROVAR COM RESSALVAS" (3 críticos resolvidos, 3 P1 + 6 P2 abertos) → **v3 fecha
> todos**. Rastreabilidade completa em §12.

---

## 1. Diagnóstico (causa raiz comprovada)

`live_name` **não identifica uma live** — é o username do streamer, constante para
todas as sessões dele (`manager.go:69` injeta `payload["liveName"] = username`;
`controller.eventLiveName` lê esse campo).

| Camada | Evidência | Comportamento |
|---|---|---|
| Listagem | `database/lives.go:39` `ListLives` | agrupa por `(live_name, day)` → **uma linha por dia** |
| Front | `frontend/admin.js:205`, `renderer.js:3875` | envia só `?live=<name>` (sem `day`) |
| API | `view/handlers.go:369` `handleAdminLivesDelete` | lê só `live` |
| Repo | `database/lives.go:96` `DeleteLive` | `DELETE FROM <t> WHERE live_name = ?` em 9 tabelas, **sem filtro de dia** |

Como todas as linhas da tabela admin compartilham o mesmo `live_name`, deletar
qualquer uma apaga **todas** → o sintoma relatado.

### Bug correlato e mais grave (mesma raiz)
`monitor/session.go:40` → `database/sessions.go:82` `DeleteSessionData(liveName)`:
ao reconectar depois de >10h (`sessionReuseMaxAge`) ou em outro dia UTC, o monitor faz
`DELETE ... WHERE live_name = ?` e **apaga todo o histórico do streamer**, apesar do
comentário "only rows of that live". `TestRestoreOrPurgeDeletesStaleSession`
(`monitor_test.go:232`) documenta esse comportamento destrutivo como esperado.

O ID de sessão corrige os dois casos e elimina qualquer aritmética de data no delete.

---

## 2. Identidade da sessão (regra definitiva)

`live_sessions.id` = UUID v4 (`crypto/rand`, 16 bytes → hex; sem dependência nova no
`go.mod`) criado **a cada início de monitor**.

`BeginLiveSession(liveName, now)`:

1. Existe sessão com `ended_at IS NULL` para o `live_name`, **mesmo dia UTC** e
   `last_seen_at` < 10h (`sessionReusable`, `monitor/session.go:11`) e
   `last_seen_at` hoje? → **reusa** (retomada após crash/restart do backend no meio da
   live; nada é apagado).
2. Senão → **fecha todas as sessões abertas daquele `live_name`**
   (`ended_at = last_seen_at`) e **cria um ID novo**.

O passo 2 é o que fecha a live que terminou sem `StopMonitoring` (sessão "ociosa"): no
próximo início do mesmo streamer ela é encerrada com `ended_at` real. Nenhuma sessão
fica permanentemente aberta.

`EndLiveSession(id, at)` é chamado em **`Monitor.StopMonitoring` e `Monitor.Close`** —
cobre os dois modos de parada (`AppController.StopMonitoring` em single mode,
`Manager.StopMonitoring`→`Close` em manager mode, e o shutdown em `server.go:206`).
É **idempotente**: reexecutar (o shutdown single-mode passa pelos dois caminhos) apenas
reescreve `ended_at = COALESCE(ended_at, ?)` e não retorna erro.

Consequência direta: **parar e reiniciar a live do mesmo streamer no mesmo dia gera
dois IDs** — exatamente o caso que a v1 do plano fundia. Restart do backend **sem**
parada prévia permanece no mesmo ID, para não fragmentar uma live em duas linhas.

`TouchLiveSession(id, at)` atualiza `last_seen_at`, com throttle em memória (no máximo
1 escrita por minuto por sessão) para não amplificar I/O de eventos. O throttle é
seguro para as regras acima porque `last_seen_at` só precisa de granularidade de horas,
nunca de segundos.

---

## 3. Modelo de dados

### 3.1 `live_sessions`

```sql
CREATE TABLE IF NOT EXISTS live_sessions (
    id           TEXT PRIMARY KEY,                          -- UUID v4 (Go)
    live_name    TEXT NOT NULL,
    day          DATE NOT NULL,                             -- dia UTC da sessão
    started_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    ended_at     TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_live_sessions_name_day
    ON live_sessions(live_name, day DESC);
-- espelha o hardening de 002_rls_operational_tables.sql:36-48
ALTER TABLE live_sessions ENABLE ROW LEVEL SECURITY;
REVOKE ALL ON public.live_sessions FROM anon, authenticated;
```

### 3.2 Coluna `live_id` em **9** tabelas

`user_messages`, `gifts`, `shares`, `likes`, `pinned_comments`, `anomaly_logs`,
`target_gift_history`, `gift_goals`, `room_like_totals`.

Todas entram no escopo do delete (decisão do owner: "apagar tudo que aconteceu naquela
live"). Cada uma recebe `live_id TEXT`, índice e, após o backfill (§3.4),
`SET NOT NULL`.

- `gift_goals`: metas passam a pertencer à sessão → `live_id NOT NULL` + fiação
  completa no controller (§6). `AddGiftGoal` passa a inserir `live_id` e a validar
  `LiveID` não vazio (erro claro, em vez do erro opaco do NOT NULL); `GetGiftGoals`
  projeta `live_id` e `scanGiftGoal` (`database/goals.go:121`) o lê.
  `SaveGiftGoal` continua `UPDATE ... WHERE id = ?` **sem** tocar
  `live_name`/`live_id` — e não precisa: quem edita uma meta recebe o `LiveID` do
  banco (`handleGoals` → `GetGoalsState` → `findGoal`, `handlers.go:478,542`), não do
  corpo HTTP.
- `room_like_totals`: hoje `live_name TEXT PRIMARY KEY` (`postgres.go:104`) guarda o
  total acumulado da sala por **streamer**. Vira escopo de sessão com `live_id` como
  PK, mantendo `live_name` para leitura. As **leituras não mudam**
  (`engagement.go:59` `MAX(total) WHERE live_name = ?` continua devolvendo o máximo
  entre sessões → ranking/`TotalLikes` inalterados); só a escrita (`driver.go:77`)
  passa a usar `ON CONFLICT (live_id)`.
- `SET NOT NULL` nas 9 é a garantia anti-drift: qualquer caminho de escrita esquecido
  falha alto em teste, em vez de criar dado indeletável.

### 3.3 Migração — **ordem única e explícita**

Para não haver duas ordens divergentes, a divisão é:

| Onde | O que faz |
|---|---|
| `supabase/migrations/004_live_sessions.sql` | **só aditivo**: `live_sessions` + RLS/REVOKE; `ADD COLUMN IF NOT EXISTS live_id` nas 9; `CREATE INDEX IF NOT EXISTS` |
| `migratePostgres()` (mesmos statements, todos idempotentes) | o mesmo **+ backfill + `SET NOT NULL` + swap de PK** (§3.4) |

O `004` **não** faz backfill, `SET NOT NULL` nem troca de PK — assim a PK nunca é
recriada antes de a coluna estar preenchida, e a migração manual do SQL Editor não pode
derrubar o boot do backend.

### 3.4 Backfill — dentro de `migratePostgres()`, nesta ordem exata

```sql
-- 1) sessões legadas a partir das 9 tabelas
INSERT INTO live_sessions (id, live_name, day, started_at, last_seen_at)
SELECT 'legacy:' || md5(live_name || ':' || day), live_name, day, MIN(ts), MAX(ts)
FROM ( ... 9 fontes (live_name, day, ts) ... ) s
GROUP BY live_name, day
ON CONFLICT (id) DO NOTHING;

-- 2) carimba as linhas antigas
UPDATE gifts SET live_id = 'legacy:' || md5(live_name || ':' || (timestamp AT TIME ZONE 'UTC')::date)
WHERE live_id IS NULL;
-- idem para as outras 8

-- 3) agora sim
ALTER TABLE gifts ALTER COLUMN live_id SET NOT NULL;   -- idem para as outras 8
ALTER TABLE room_like_totals DROP CONSTRAINT IF EXISTS room_like_totals_pkey;
ALTER TABLE room_like_totals ADD PRIMARY KEY (live_id);
```

- `day`/`ts` por fonte — **não usar `timestamp` literal para todas**, cada tabela tem
  seu nome de coluna de tempo:

  | Tabela | Coluna do dia (`day`) | Coluna do instante (`ts`) |
  |---|---|---|
  | `user_messages`, `gifts`, `shares`, `likes`, `pinned_comments` | `(timestamp AT TIME ZONE 'UTC')::date` | `timestamp` |
  | `anomaly_logs` | `day` (já `DATE NOT NULL`) | `timestamp` |
  | `target_gift_history` | `(received_at AT TIME ZONE 'UTC')::date` | `received_at` |
  | `gift_goals` | `(COALESCE(created_at, CURRENT_TIMESTAMP) AT TIME ZONE 'UTC')::date` | `created_at` |
  | `room_like_totals` | `(COALESCE(updated_at, CURRENT_TIMESTAMP) AT TIME ZONE 'UTC')::date` | `updated_at` |

  Os dois `COALESCE` são **blindagem obrigatória**: `gift_goals.created_at`
  (`postgres.go:139`) e `room_like_totals.updated_at` (`postgres.go:107`) são nullable
  e um `NULL` deixaria `live_id` nulo e faria o `SET NOT NULL` falhar no boot.
- `id` determinístico ⇒ reexecução não duplica; os `UPDATE` são idempotentes.
- `gift_goals.created_at` é sempre preenchido pelo Go (`database/goals.go:29` —
  default `time.Now().UTC()` quando vazio), então metas legadas **são** backfilladas e
  não sobram metas órfãs (a contradição da v1).
- `AT TIME ZONE 'UTC'` explícito para casar com o dia que o Go grava (`sessions.go:18`).
  **Validar antes de aplicar:** `SHOW TimeZone` no Supabase — se a sessão não for UTC, o
  `DATE(timestamp)` atual de `ListLives` já rotula dias em outro fuso; alinhar ambos
  para UTC e registrar no PR (pode deslocar rótulos perto da meia-noite).
- Hardening opcional: `SET NOT NULL` também em `gift_goals.created_at` e
  `room_like_totals.updated_at` (hoje nullable).
- Verificação (deve ser 0 em todas as 9):
  `SELECT COUNT(*) FROM gifts WHERE live_id IS NULL;`

---

## 4. Fase 1 — Modelo e repositório

`internal/model`:
- novas entidades `LiveSession`, `LiveRef{ID, Name}`;
- `Live.ID` (`entities.go:226`) — é o que o front devolve no delete;
- `GiftGoal.LiveID`.

`internal/model/repository.go` — novos:
```go
BeginLiveSession(liveName string, now time.Time) (LiveSession, error) // regra §2
EndLiveSession(id string, at time.Time) error                         // idempotente
TouchLiveSession(id string, at time.Time) error
GetLiveSession(id string) (LiveSession, error)      // ErrInvalidID se vazio
LatestLiveSession(liveName string) (LiveSession, error) // lookup p/ fallback de eventos externos
DeleteLiveSession(id string) (int64, error)         // substitui DeleteLive
```

Removidos/substituídos: `DeleteLive(liveName)` (a causa do bug — sem alias),
`DeleteSessionData(liveName)` → `EndLiveSession` + escopo por sessão,
`GetLastSessionActivity(liveName)` → por sessão,
`GetTodayUserMessages`/`GetTodayAnomalyLogs` → `...BySession(liveID)`,
`GetGiftGoals`/`GetGiftUnits` → `(ref LiveRef)`.

Escritas passam a receber `LiveRef` no lugar de `liveName`: `LogAnomaly`,
`AddUserMessageDedup`, `BatchAddUserMessages`, `AddGift`, `AddShare`, `AddLike`,
`UpsertRoomLikeTotal`, `AddPinnedComment`, `AddTargetGiftHistory`.
`AddGiftGoal` já recebe a entidade → só o campo `LiveID`.

Também entra no escopo:
- `MessageCache.Add(liveName, …)` (`controller/app.go:507`, interface em `app.go:24`)
  → `Add(ref, …)`; `messageCacheEntry` (`messagecache.go:23`) ganha `liveID` e o
  `pending` passa a ser chaveado por `liveID+uniqueId`.
- **Dedup/prune de mensagens** (`messages.go:26,41` e `messages.go:107-119`): a chave
  de dedup hoje é global `(LOWER(uniqueId), LOWER(message))` e o prune mantém as 10
  mais recentes por usuário **sem recorte de live** — com sessões isso descarta
  mensagem legítima de outra sessão e evicta dados da sessão anterior. Passam a incluir
  `live_id`; o índice `idx_user_messages_dedup` (`postgres.go:163`) é recriado com
  `live_id` na frente. As leituras por usuário (`GetUserMessages`,
  `GetAllUserMessages`, `LiveStatsByUser`) **continuam por `live_name`/`uniqueId`** e
  não dependem do índice de dedup — sem inconsistência.

`DeleteLiveSession(id)` — transação única, lista explícita das 9 tabelas + a linha da
sessão, somando `RowsAffected`. Sem aritmética de data, logo sem ambiguidade de fuso.

**Risco de drift** (tabela nova com `live_id` esquecida no delete): mitigado pelo
`SET NOT NULL` + teste que lê `information_schema.columns` (§8).

---

## 5. Fase 2 — Monitor

- `Monitor` guarda `liveID` ao lado de `currentUsername` (`monitor.go:124`), com
  `CurrentLiveID()`.
- `StartMonitoring` (`monitor.go:229`): `restoreOrPurgeSessionData` (`monitor.go:242`)
  → **`beginOrResumeSession`**, que chama `repo.BeginLiveSession`. **Nada é apagado.**
  Reconexões internas do bridge passam por `bridge.go:213` e **não** reabrem sessão —
  o mesmo `liveID` sobrevive.
- `loadTodayData` → `loadSessionData`: filtra por `live_id` da sessão
  (`GetTodayUserMessages`→`...BySession`). O purge deixa de existir: dados de outra
  sessão simplesmente não entram no buffer.
- `Monitor.StopMonitoring` **e** `Monitor.Close` → `EndLiveSession` (idempotente).
- `Manager.OnEvent` (`manager.go:66`): `payload["liveId"] = monitor.CurrentLiveID()`.
- `TouchLiveSession` throttled a partir dos handlers de evento.

`controller.eventLiveName` → `eventLiveRef(data)`: usa `liveId` do payload; sem ele,
**resolve por lookup** (`LatestLiveSession(liveName)`) e, se não houver sessão, retorna
erro — **nunca cria** sessão. Isso evita sessão fantasma a partir de evento externo.
Único caso externo real é `ReportExternalFlag` (`app.go:179`, hoje sem rota HTTP, só
testes): `RecordTargetGift` e `RecordPinnedComment` são disparados pelos eventos
`EventGiftUser`/`EventPinnedComment` de `view/server.go:156,163`, originados de
`bridge.js`, portanto **já vêm com `liveId`**.

---

## 6. Fase 3 — Metas por sessão (fiação completa)

A v1 escopava `gift_goals` por `live_id` mas deixava o controller por `live_name` —
metas nasciam com `live_id` vazio e `GetGiftUnits` somava todos os dias. Fiação
completa, conforme a decisão do owner ("apagar tudo daquela live"):

- `monitor.State` (`monitor.go:85`) ganha `LiveID`; `GetState()` o expõe.
- `CreateGoal` (`controller/goals.go:62`): usa `ref := c.activeLiveRef()` e preenche
  `LiveID`.
- `activeGoalByID` (`:135`), `GetGoalsState` (`:151`) e `checkGoalProgress` (`:186`,
  hoje variádico de `liveName`, chamado de `HandleGiftEvent` em `app.go:549`) passam a
  operar por `LiveRef`.
- `CompleteGoal` (`:122`) e `checkSingleGoal` (`:217`, usa `active.LiveName`) →
  `GetGiftUnits(ref, …)`.
- `database/{goals,gifts}.go`: `GetGiftGoals(ref)` / `GetGiftUnits(ref)` filtram por
  `live_id`; `scanGiftGoal` lê a coluna. Únicos call sites são o controller de metas —
  mudança contida.
- Sem sessão ativa, `CreateGoal` mantém o erro atual ("no live is being monitored").

---

## 7. Fase 4 — Listagem, API e front

### 7.1 `ListLives` por sessão

```sql
SELECT s.id, s.live_name, s.day, s.started_at, s.ended_at, COALESCE(e.events, 0)
FROM live_sessions s
LEFT JOIN ( contagem de eventos agrupada por live_id ) e ON e.live_id = s.id
ORDER BY s.day DESC, s.started_at DESC
LIMIT ?
```

- **Sem filtro de sessão "em andamento"**: a v2 escondia sessões abertas com
  `last_seen_at` > 10h atrás, deixando-as invisíveis e **indeletáveis pela UI**. Toda
  sessão aparece; as ociosas são fechadas pelo passo 2 de `BeginLiveSession` (§2).
- Sessões legadas são **linhas reais** em `live_sessions` (criadas no backfill), então
  são exibidas e deletáveis como qualquer outra. **Sem `id` sintético no SELECT** — o
  `legacy:<name>:<day>` da v1 não existiria em `live_sessions` e daria `404` no delete.
- `Live.Events` conta **apenas tabelas de evento** (`user_messages`, `gifts`, `shares`,
  `likes`, `pinned_comments`, `anomaly_logs`, `target_gift_history`) — `gift_goals` e
  `room_like_totals` não são eventos e ficam fora da contagem. Ganho real: passa a
  incluir `likes`, hoje fora do UNION de `lives.go:50-60`; o valor da coluna "Eventos"
  muda e isso vai no comunicado do rollout.

### 7.2 API — **caminho novo** (fail-safe nos dois sentidos de rollback)

```
POST /api/admin/lives/session/delete?id=<live_id>&live=<name>&day=YYYY-MM-DD
POST /api/admin/lives/delete   →  410 Gone ("use /session/delete")
```

- `id`, `live` **e** `day` obrigatórios → `400`; `GetLiveSession(id)` inexistente →
  `404`; `session.LiveName != live` ou `session.Day != day` → `409`; OK →
  `DeleteLiveSession(id)` → `{"deleted": n, "id": "<live_id>"}`.
- `AppController.DeleteLive(id string)` muda de `liveName` para `id`.
- Por que path novo: mantendo o endpoint antigo no mesmo caminho, "front novo +
  backend antigo" faria o backend antigo ler só `live` e **apagar tudo**, com um modal
  dizendo "somente esta live". Com o path novo:

| Combinação | Resultado |
|---|---|
| front novo + backend novo | apaga só a sessão ✅ |
| front antigo + backend novo | `410 Gone` → não apaga nada ✅ |
| front novo + backend antigo | `404` no path novo → não apaga nada ✅ |

- Nenhum cliente funcional além do front: só `frontend/admin.js:205` e
  `frontend/renderer.js:3875` (atualizados em §7.3) e os testes
  (`integration_test.go:749,782,790`). Docs a atualizar no mesmo PR:
  `docs/backend/09-view-http-sse.md:75` e
  `docs/backend/diagrams/03-autenticacao.puml:60`.

### 7.3 Front

`frontend/admin.js` e `frontend/renderer.js` (dois pontos, hoje idênticos):
- `renderLives`/`renderAdminLives` passam o objeto `live` inteiro (já têm `live.id` e
  `live.day`) ao handler, em vez de só o nome.
- `deleteLive(live)` monta a URL nova com `id`, `live` e `day`.
- Confirmação explícita do escopo:
  `Deletar os dados da live "<name>" do dia <dd/mm/aaaa>? Essa ação não pode ser
  desfeita.` — a mensagem atual (`renderer.js:3851`, "Deletar TODOS os dados da live")
  descrevia exatamente o comportamento errado.
- `admin.js` usa `showMessage`/recarrega a lista (padrão do arquivo), não `alert`.

---

## 8. Fase 5 — Testes

`internal/database` (PostgreSQL descartável, `TEST_DATABASE_URL`):
- `TestBeginLiveSessionReusesOpenSession` / `...CreatesNewAfterEnd` /
  `...CreatesNewAfterDayChange` / `...ClosesIdleOpenSessions` — cobre a regra §2,
  inclusive "parar e reiniciar no mesmo dia = 2 IDs" (o caso que a v1 errava).
- `TestEndLiveSessionIsIdempotent` — duas chamadas não retornam erro e não alteram o
  `ended_at` original.
- `TestDeleteLiveSessionOnlyDeletesThatSession` — 2 sessões do mesmo streamer no mesmo
  dia + 1 de outro streamer; apaga um `id`; confirma que sobraram as outras (reproduz o
  bug relatado).
- `TestDeleteLiveSessionDeletesGoalsAndRoomLikeTotals` — metas e contador da sessão
  apagados; os das outras sessões intactos.
- `TestDeleteLiveSessionInvalidID` — `""`/inexistente.
- `TestListLivesIncludesIDAndEvents` — `id`/`day` coerentes; `events` conta `likes` e
  **não** conta metas/room totals; sessão ociosa aberta continua listada.
- `TestMessageDedupIsPerSession` / `TestMessagePruneIsPerSession` — mesma mensagem do
  mesmo usuário em duas sessões coexiste; prune de uma não evicta a outra.
- `TestBackfillStampsLiveID` — semeia linhas sem `live_id` nas 9 tabelas, **incluindo
  `gift_goals.created_at` NULL e `room_like_totals.updated_at` NULL**, roda o backfill,
  exige `COUNT(*) ... live_id IS NULL = 0`, colunas `NOT NULL` e
  `room_like_totals` com PK `live_id` (e a linha legada migrada, sem perda).
- `TestDeleteLiveSessionCoversEveryLiveIDTable` — **anti-drift**: lê
  `information_schema.columns` e falha se existir tabela com `live_id` fora da lista do
  delete.
- `TestMigratePostgresReentrant` — rodar `migratePostgres()` duas vezes seguidas não
  falha (cobre a reentrância do backfill e do swap de PK).
- Remover `TestDeleteLive`; ajustar `TestDeleteSessionData` e `TestGetGiftGoals`/
  `TestGetGiftUnits` para as assinaturas por sessão.

`internal/monitor`:
- `TestReconnectSameDayReusesSession`, `TestReconnectNewDayKeepsPreviousHistory` —
  substitui `TestRestoreOrPurgeDeletesStaleSession`, que codificava o bug.

**Helpers de teste precisam semear sessão.** `newTestController`
(`controller/goals_test.go:13`) e `setupTestServer` (`view/integration_test.go:23`)
usam `mon.SetCurrentLive("live1")`, que só seta `currentUsername`
(`monitor.go:213`) e **não** cria `live_sessions`. Sob a regra "`eventLiveRef` nunca
cria sessão" (§5), `ReportExternalFlag`, `HandleGiftEvent`, `RecordTargetGiftReceived`
e `RecordPinnedComment` em teste sem sessão passariam a retornar erro e a pular a
escrita — quebrando `app_test.go:226` e `integration_test.go:798`. Incluir nos
helpers: chamar `repo.BeginLiveSession(liveName, time.Now())` no setup e usar
`mon.SetCurrentLive`/`SetLiveID` com o id retornado.

`internal/view` (`integration_test.go:742`): `400` sem `id`/`live`/`day`; `404` id
inexistente; `409` divergência de `live` ou `day`; `410` no path antigo; `405` método
errado; `200` apagando só a sessão alvo.

Front: sem suíte JS no projeto → roteiro manual (apagar 1 sessão de um streamer com
vários dias e conferir que as outras permanecem; conferir a mensagem do modal).

---

## 9. Fase 6 — Rollout

1. `SHOW TimeZone` no Supabase e backup.
2. Aplicar `004` no SQL Editor — **só aditivo** (tabela, colunas, índices, RLS/REVOKE).
3. Subir backend: `migratePostgres()` roda backfill → `SET NOT NULL` → swap de PK de
   `room_like_totals`, nessa ordem.
4. Verificar `live_id IS NULL = 0` nas 9 tabelas e comparar a contagem de linhas de
   `/api/admin/lives` antes/depois (mesmo nº de linhas, agora com `id`).
5. Subir front. Ordem segura nos dois sentidos (§7.2).
6. Conferir em produção apagando uma sessão de um streamer com histórico e validando
   que as outras linhas continuam na tabela.

---

## 10. Riscos e mitigações

| Risco | Mitigação |
|---|---|
| Assinaturas de ~20 métodos do `Repository` mudam | troca mecânica; suíte de `database_test.go`/`monitor_test.go` cobre |
| Tabela nova com `live_id` esquecida no delete | `SET NOT NULL` + teste anti-drift via `information_schema` |
| Swap de PK de `room_like_totals` antes do backfill | ordem única §3.3/§3.4: só o `migratePostgres` faz backfill+NOT NULL+PK; teste de reentrância |
| Timestamp NULL em `gift_goals`/`room_like_totals` no backfill | `COALESCE(..., CURRENT_TIMESTAMP)` nas duas fontes; `SET NOT NULL` opcional nas colunas |
| Sessão aberta ociosa ficando invisível/indeletável | `ListLives` sem filtro de "em andamento" + fechamento no passo 2 de `BeginLiveSession` |
| Fuso horário no backfill (dias perto da meia-noite) | `AT TIME ZONE 'UTC'` explícito; validar `SHOW TimeZone` antes |
| Dedup/prune de mensagens global descartando dados de outra sessão | chave de dedup e prune com `live_id`; índice recriado; testes dedicados |
| Rollback com backend antigo apagando tudo | endpoint novo + `410` no path antigo (matriz §7.2) |
| Sessão fantasma criada por evento externo | `eventLiveRef` faz lookup (`LatestLiveSession`), nunca cria |
| `EndLiveSession` chamado 2× no shutdown | operação idempotente (`COALESCE(ended_at, ?)`) + teste |
| Metas/progresso somando outras sessões | fiação completa por sessão (§6) |
| "Eventos" na admin muda de valor | passa a contar `likes`; metas/room totals fora; comunicar no rollout |
| Docs defasados após o `410` | atualizar `docs/backend/09-view-http-sse.md:75` e `diagrams/03-autenticacao.puml:60` no mesmo PR |
| Relatório/ranking agregam por `live_name` (todos os dias) | **inalterado**, explicitamente fora de escopo; `LikeTotals` mantém `MAX(total) WHERE live_name` para não mexer no `TotalLikes`. Follow-up: relatório por sessão |

---

## 11. Ordem de execução e arquivos

| # | Passo | Arquivos |
|---|---|---|
| 1 | Migração 004 (aditivo) + `migratePostgres` (backfill, NOT NULL, PK) | `supabase/migrations/004_live_sessions.sql`, `backend/internal/database/postgres.go` |
| 2 | Entidades `LiveSession`/`LiveRef`/`Live.ID`/`GiftGoal.LiveID` | `backend/internal/model/{entities,repository}.go` |
| 3 | CRUD de sessão + `DeleteLiveSession` | `backend/internal/database/sessions.go`, `lives.go` |
| 4 | Escritas com `LiveRef` | `database/{messages,gifts,engagement,anomalies,pinned,targetgifts,goals,messagecache,driver}.go`, `controller/app.go` |
| 5 | Sessão no monitor | `monitor/{monitor,manager,session}.go` |
| 6 | Metas por sessão | `controller/goals.go`, `database/goals.go` |
| 7 | `ListLives` por sessão | `database/lives.go` |
| 8 | API + controller | `view/{handlers,server}.go`, `controller/app.go` |
| 9 | Front (2 telas) | `frontend/admin.js`, `frontend/renderer.js` |
| 10 | Docs | `docs/backend/09-view-http-sse.md`, `docs/backend/diagrams/03-autenticacao.puml` |
| 11 | Testes | `database/database_test.go`, `monitor/monitor_test.go`, `view/integration_test.go` |
| 12 | Rollout | Supabase SQL Editor → backend → front |

---

## 12. Rastreabilidade das revisões

| Origem | Achado | Tratamento |
|---|---|---|
| v1 **P1** | reuso <10h fundia duas lives no mesmo `live_id` | §2: novo ID por início de monitor; reuso só com `ended_at IS NULL`; testes |
| v1 **P1** | `gift_goals` a meio caminho | §6: fiação completa (entidade, `State.LiveID`, 6 funções do controller, repo) |
| v1 **P1** | contradição do backfill de metas | §3.4: union com 9 fontes, incluindo `gift_goals.created_at` |
| v1 **P2** | `legacy:` com dois formatos → 404 no delete | §7.1: sem ID sintético; sessões legadas são linhas reais |
| v1 **P2** | rollback "front novo + backend antigo" catastrófico | §7.2: endpoint novo + `410` |
| v1 **P2** | `ListLives.events` muda de semântica | §7.1/§10: comunicado |
| v1 factual | target gift/pin ditos externos ao bridge | §5: corrigido — vêm de `server.go:156,163` |
| v1 omisso | `MessageCache`/`messageCacheEntry` | §4: `LiveRef` no cache e na interface |
| v1 omisso | dedup/prune de mensagens global | §4: chave com `live_id` + índice + testes |
| v2 **P1** | ordem do swap de PK de `room_like_totals` | §3.3/§3.4: `004` só aditivo; ordem única no backend; teste de reentrância |
| v2 **P1** | `ListLives` escondia sessão aberta >10h (indeletável) | §7.1: sem filtro de "em andamento"; §2 passo 2 fecha ociosas |
| v2 **P1** | backfill com timestamp NULL quebrando o `SET NOT NULL` | §3.4: `COALESCE(..., CURRENT_TIMESTAMP)` nas 2 fontes |
| v2 **P2** | `410` deixa docs defasados | §7.2/§11: atualizar 2 docs no mesmo PR |
| v2 **P2** | `live_sessions` sem `REVOKE` | §3.1: espelha `002:36-48` |
| v2 **P2** | "Eventos" contando metas/room totals | §7.1: só tabelas de evento |
| v2 **P2** | `eventLiveRef` criando sessão fantasma | §5: lookup via `LatestLiveSession`, nunca cria |
| v2 **P2** | `scanGiftGoal`/`AddGiftGoal` sem `live_id` | §3.2: projeção, scan e validação explícitos |
| v2 **P2** | `EndLiveSession` 2× no shutdown | §2: idempotente + teste |
| v3 revisão | helpers de teste não criam sessão (`SetCurrentLive` não abre sessão) | §8: `BeginLiveSession` no setup de `newTestController`/`setupTestServer` |
| v3 revisão | `target_gift_history` usa `received_at`, não `timestamp` | §3.4: tabela de coluna do dia/instante por fonte |
| v1 sugestão | solução mínima (`live_name`+`day`) | Rejeitada pelo owner, que escolheu sessão real (resolve também o purge destrutivo) |

---

## 13. Implementação (o que divergiu do desenho)

Tudo do §1–§11 foi implementado e a suíte completa passa. Quatro ajustes surgidos
durante a implementação, com o motivo:

1. **`REVOKE` do `live_sessions` só no `004`, não no `migratePostgres`.** Os bancos de
   teste descartáveis (`CreateTestDatabase`) não têm os papéis `anon`/`authenticated`,
   então o `REVOKE` lá derrubaria o boot dos testes. É exatamente o padrão já usado no
   projeto: `002_rls_operational_tables.sql` tem o `REVOKE` e o `migratePostgres` só
   faz `ENABLE ROW LEVEL SECURITY`.
2. **`TestBackfillStampsLiveID` virou `TestLiveIDSchemaHardening`.** Depois do
   `SET NOT NULL` é impossível semear `live_id IS NULL` para exercitar o backfill. O
   teste agora cobra o que dá para cobrir de verdade: reexecução da migração inteira
   (`migratePostgres` 2×), `live_id NOT NULL` nas 9 tabelas e PK de
   `room_like_totals` em `live_id`.
3. **`liveId` injetado no `Monitor.emit` (novo).** No modo monitor único os eventos não
   passam pelo `Manager`, então não carregavam `liveId` — o `eventLiveRef` cairia no
   lookup `LatestLiveSession` **a cada mensagem de chat**. Agora todo evento emitido
   carrega o id da sessão (com cópia rasa do payload, sem mutar o mapa do chamador).
   Teste: `TestEmitInjectsLiveID`.
4. **Sessão reaberta quando a live em transmissão é apagada (novo).** Era a brecha
   prevista no §7.1 ("o monitor recria a sessão"): se o admin apagar a live que está
   no ar, `TouchLiveSession` agora retorna `ErrLiveSessionNotFound` (0 linhas
   afetadas) e o monitor abre uma sessão nova, em vez de gravar linhas órfãs
   invisíveis no admin. Teste: `TestTouchSessionReopensDeletedSession`.
5. **`activeLiveRef` cai para `LatestLiveSession`** quando o estado do monitor não tem
   `liveID` (start que não conseguiu abrir sessão, ou harness de teste), mantendo o
   contrato de nunca gravar sem sessão.

### Verificação executada

- `go build ./...`, `go vet ./...` e `gofmt` limpos nos arquivos alterados.
- Suíte completa contra PostgreSQL 16 descartável (`TEST_DATABASE_URL`): **todos os
  pacotes OK** — inclui `TestDeleteLiveSessionOnlyDeletesThatSession` (reproduz o bug
  relatado: 2 lives do mesmo streamer no mesmo dia, apaga uma só),
  `TestDeleteLiveSessionCoversEveryLiveIDTable` (anti-drift),
  `TestHandleAdminLivesSessionDelete` (400/404/409/405/200 pela API) e
  `TestHandleAdminLivesDeleteIsGone` (410 sem apagar nada).
- `004_live_sessions.sql` aplicado **duas vezes** num Postgres onde o backend já havia
  migrado: idempotente, com PK de `room_like_totals` = `live_id` e as 9 colunas
  `NOT NULL`.

### 14. Revisão da implementação (DeepSeek) e correções aplicadas

Veredito: **APTO COM RESSALVAS** — sem nada bloqueante. As 3 ressalvas recomendadas
foram aplicadas antes do push:

1. **SQL de rollback de schema** → `supabase/migrations/004_live_sessions_rollback.sql`.
   O `SET NOT NULL` inviabiliza o binário anterior (ele não cita `live_id` nos
   `INSERT`s), e a PK de `room_like_totals` mudou de `live_name` para `live_id` — o
   que quebraria o `ON CONFLICT (live_name)` do código antigo. O script libera o
   `NOT NULL` e devolve a PK (deduplicando por `live_name`), e foi testado aplicando-o
   sobre um banco migrado: `INSERT` sem `live_id` e `UPSERT ON CONFLICT (live_name)`
   do binário antigo voltam a funcionar. Documentado no `PRODUCAO.md`.
2. **Reconstrução de índice condicional** — o `DROP`/`CREATE` de
   `idx_user_messages_dedup` e `idx_pinned_comments_pin` só roda se a definição atual
   do índice ainda não contiver `live_id` (`pg_get_indexdef`). Antes, o `DROP`
   incondicional reconstruía e re-varria `user_messages` a cada boot.
3. **Lista única de tabelas** — `DeleteLiveSession` e o teste anti-drift passaram a
   consumir `liveIDTableNames()` (derivada de `liveIDColumns`). Antes o teste comparava
   `information_schema` com uma lista duplicada no próprio teste, então esquecer uma
   tabela no delete não reprovava nada.

Blindagem extra (também do review): `COALESCE(ts, CURRENT_TIMESTAMP)` no `MIN/MAX` do
backfill, para uma linha legada com timestamp NULL não quebrar o `INSERT` da sessão.

**Não adotado (risco residual aceito):** FK `live_id → live_sessions(id)`. Um evento
já despachado e gravado logo após o delete da live em transmissão pode gerar uma linha
órfã (invisível no admin). A janela é de milissegundos e o reopen de sessão cobre os
eventos seguintes; a FK trocaria isso por inserts falhando em corrida, o que não parece
melhor. Fica registrado como decisão consciente.
