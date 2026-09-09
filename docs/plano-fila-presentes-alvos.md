# Plano: Fila de Presentes Alvos com "Fura Fila"

## Objetivo

- Tratar a lista de presentes alvos da view principal como uma **fila** (FIFO):
  o streamer responde de cima para baixo.
- Cada presente na fila pode ser marcado como **"Fura fila"** via checkbox.
- Ao ser marcado, o presente vai para o **topo da fila**, mas **abaixo de outros
  fura fila** (FIFO dentro da classe prioritária — quem fura primeiro fica na
  frente).
- Ao desmarcar, o presente volta para a posição normal da fila (por
  `received_at`).
- O flag é **persistido** no banco (sobrevive a reload/reconexão).

## Ordenação da fila (fonte única de verdade)

```sql
ORDER BY is_priority DESC,
         COALESCE(priority_at, received_at) ASC,
         received_at ASC,
         id ASC
```

- Fura fila primeiro, na ordem em que foram promovidos (`priority_at ASC`).
- Normais depois, na ordem em que foram recebidos.
- Tie-break por `id` (ordem de inserção) para promoções no mesmo milissegundo.
- "Próximo a responder" = primeira linha da fila.

Estado atual que se aproveita: a tabela principal (`userTableBody`) já é exibida
mais antiga no topo (FIFO), e `loadPendingTargetGifts` restaura os pendentes em
ordem. O timer de expiração (auto-responder após N minutos) permanece
independente da prioridade.

---

## Fase 1 — Schema, model e repository (backend)

1. `backend/internal/database/postgres.go` (`migratePostgres`):
   - `ALTER TABLE target_gift_history ADD COLUMN IF NOT EXISTS is_priority BOOLEAN NOT NULL DEFAULT FALSE;`
   - `ALTER TABLE target_gift_history ADD COLUMN IF NOT EXISTS priority_at TIMESTAMPTZ;`
   - Atualizar o `CREATE TABLE IF NOT EXISTS target_gift_history` com as duas
     colunas (para bancos novos).
2. `supabase/migrations/003_target_gift_priority.sql` — os mesmos `ALTER`s,
   aplicados **manualmente** no SQL Editor do Supabase (padrão do projeto, sem
   CI de migração).
3. `backend/internal/model/entities.go` — `TargetGiftHistory` ganha:
   - `IsPriority bool json:"isPriority"`
   - `PriorityAt *string json:"priorityAt,omitempty"`
4. `backend/internal/model/repository.go` — `TargetGiftHistoryRepository` ganha:
   - `SetTargetGiftPriority(id int64, priority bool, at time.Time) error`
5. `backend/internal/database/targetgifts.go`:
   - `SetTargetGiftPriority`:
     - `priority=true` → `UPDATE target_gift_history SET is_priority=TRUE, priority_at=? WHERE id=? AND answered_at IS NULL`
     - `priority=false` → `UPDATE target_gift_history SET is_priority=FALSE, priority_at=NULL WHERE id=?`
     - `RowsAffected == 0` → erro (não encontrado / já respondido).
   - `GetRecentTargetGiftHistory` e `GetPendingTargetGiftHistory`: incluir as
     colunas no `SELECT` e trocar o `ORDER BY` pela ordenação da fila acima.
   - `scanTargetGiftHistory`: ler as colunas novas.
6. Testes (`backend/internal/database/database_test.go`):
   - toggle on/off persiste;
   - ordenação: A(t1), B(t2) normais → promover A → [A,B]; promover B → [A,B]
     (B fica **abaixo** de A); despromover A → [B,A] (A volta por
     `received_at`);
   - linha já respondida não pode ser promovida;
   - flag sobrevive a re-query (cenário de reconexão).

## Fase 2 — Controller e API (backend)

1. `backend/internal/controller/app.go`:
   - `SetTargetGiftPriority(id int64, priority bool) error` → delega ao
     repository (mesmo padrão de `AnswerTargetGift`).
2. `backend/internal/view/handlers.go`:
   - `handleTargetGiftHistoryPriority`: `POST /api/target-gift-history/priority`
     com body `{"id": 123, "priority": true}`.
   - Validações: `id > 0`; `priority` como `*bool` (ausente → 400); erro do
     repo → 400/404 conforme o caso.
   - Resposta: `{"success": true}`.
3. `backend/internal/view/server.go`: registrar a rota
   `mux.HandleFunc("/api/target-gift-history/priority", ...)`.
4. (Opcional, fase 2.5) Broadcast SSE `target-gift-priority`
   (`{id, priority, priorityAt}`) para outros clientes abertos reordenarem em
   tempo real — mesmo padrão dos eventos `goal-update`.

## Fase 3 — Frontend (`frontend/index.html`, `frontend/renderer.js`)

1. Tabela principal:
   - Nova coluna **"Fila"** com um checkbox **"Fura fila"** por linha.
   - Badge visual `⚡ Fura fila` nas linhas prioritárias.
   - Destaque na primeira linha da fila (classe `queue-head`, ex.: borda
     colorida + chip "Próximo").
2. Ordenação no DOM — função única `reorderGiftQueue()`:
   - Ordena todos os `.user-row` por `(priority desc, priorityAt||receivedAt asc)`
     e re-aplica na tabela;
   - Chamada em: presente novo, toggle do checkbox, remoção ("Respondido") e
     expiração do timer;
   - Linhas guardam `dataset.priority` e `dataset.priorityAt` (além do
     `dataset.historyId` já existente).
3. `addUserToList`:
   - Presente novo entra na posição correta da fila (fim, se normal) em vez de
     `prepend` cego;
   - Caso de linha existente (mesmo user+gift): mantém o merge atual, só
     reordena.
4. `markTargetGiftPriority(historyId, priority)`:
   - Otimista: atualiza `dataset`, checkbox e `reorderGiftQueue()`;
   - `POST /api/target-gift-history/priority`; em falha, reverte checkbox e
     reordena (log de erro, mesmo padrão de `markTargetGiftAnswered`).
5. `loadPendingTargetGifts`:
   - A API já devolve em ordem de fila — renderizar na ordem recebida (remover
     o `.slice().reverse()` atual) e restaurar `dataset.priority`/`priorityAt`
     dos items.
6. Timer de expiração: sem mudança (fura fila que expira é auto-respondido e
   sai da fila como os demais).
7. Modal de histórico (baixo custo): exibir badge `⚡` nas linhas que foram
   fura fila.

## Fase 4 — Validação

- `cd backend && go test ./...`
- Smoke test manual (view principal com live ativa):
  1. 3 presentes normais → fila na ordem de recebimento;
  2. Marcar o 3º como fura fila → vai para o topo (único prioritário);
  3. Marcar o 1º → fica **abaixo** do 3º (FIFO dentro da classe prioritária);
  4. Desmarcar o 3º → volta para a posição normal por `received_at`;
  5. Reload/reconexão → fila e flags preservados;
  6. "Respondido" remove da fila; o próximo vira "Próximo".

## Decisões e pontos abertos

- **Cabeçalho da fila no topo** (mais antigo primeiro) — mantém o comportamento
  atual da tabela; o fura fila apenas sobe para o bloco prioritário.
- **Timer de expiração independente** da prioridade. Opção futura: estender o
  prazo de fura fila (fora do escopo).
- **Sync multi-cliente** via SSE é opcional nesta fase; sem ele, o cliente que
  marcou reordena localmente e os demais sincronizam na próxima reconexão.
- Admin (`admin.js`) não exibe presentes alvos — fora do escopo.
