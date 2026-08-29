# Plano: Metas de presentes com recompensas

## Visão

O streamer define uma **meta de presentes** ao vivo em total de unidades (Σ `repeat_count`,
a mesma métrica do `GiftTotal` já usado em `internal/report` e `internal/ranking`).
Milestones com recompensa de texto (ex.: 500 → "música especial"). Progresso em tempo real
via SSE, desbloqueio automático de recompensas, conclusão ao atingir 100% e persistência
por live.

Decisões de produto:

- Métrica: **total de unidades** (`SUM(repeat_count)`) — sem mudança de schema em `gifts`.
- Determinístico em Go — sem depender do agente Python/LLM.
- Uma meta ativa por live; histórico de metas da live fica visível.

---

## Fase 1 — Model, schema e persistência

Passos:

1. `internal/model/entities.go` — adicionar entidades:
   - `GoalMilestone{ AtUnits int; Reward string; Unlocked bool; UnlockedAt *string }`
   - `GiftGoal{ ID, LiveName, Title, TargetUnits, Status, Milestones []GoalMilestone, CompletedAt *string, CreatedAt }`
   - Constantes: `GoalStatusActive/Completed/Cancelled`.
2. `internal/model/repository.go` — nova interface `GoalRepository`:
   - `AddGiftGoal(g GiftGoal) (int64, error)`
   - `GetGiftGoals(liveName string) ([]GiftGoal, error)`
   - `SaveGiftGoal(g GiftGoal) error` (regrava milestones/status)
   - `DeleteGiftGoals(liveName string) (int64, error)`
   - Agregar em `Repository`.
3. `internal/database/database.go` — migrar tabela:
   - `gift_goals (id PK, live_name, title, target_units, status, milestones JSON TEXT, created_at, completed_at)`
   - Implementar os 4 métodos (padrão de `AddTargetGiftHistory`/`scanTargetGiftHistory`).
   - Adicionar `gift_goals` na lista de tabelas do `DeleteLive`.
   - `GetGiftUnits(liveName) (units, count int)`: `SELECT COALESCE(SUM(repeat_count),0), COUNT(*) FROM gifts WHERE live_name = ?`.
4. `internal/database/database_test.go` — testes de CRUD, JSON round-trip de milestones e
   cascade do `DeleteLive`.

## Fase 2 — Controller

Passos:

1. Novo `internal/controller/goals.go` (padrão de `gifts.go`):
   - `CreateGoal(title string, targetUnits int, milestones []model.GoalMilestone) (model.GiftGoal, error)`
   - `UpdateGoal(goal)` / `CancelGoal()` / `CompleteGoal()` (atualizam status e persistem via repo)
   - `GetGoalsState()` — meta ativa + progresso atual + histórico da live.
2. `checkGoalProgress()` — chamada no fim do `HandleGiftEvent` (app.go):
   - calcular unidades da live via `repo.GetGiftUnits`;
   - marcar milestones cruzados (`Unlocked=true`, timestamp);
   - completar meta quando `units >= targetUnits` (status `completed`);
   - emitir callback com os eventos ocorridos.
3. `SetGoalCallback(fn(GoalUpdate))` — `GoalUpdate{ Progress, UnlockedMilestones, Completed }`
   para o view camada reenviar via SSE.

## Fase 3 — API (internal/view/server.go)

Passos:

1. Rotas:
   - `GET /api/goals` — meta ativa + progresso + histórico
   - `POST /api/goals` — criar/atualizar `{title, targetUnits, milestones:[{atUnits, reward}]}`
   - `POST /api/goals/cancel` e `POST /api/goals/complete`
2. No `Start()`: registrar `ctrl.SetGoalCallback(...)` que chama `broadcastSSE` com
   `goal-update`, `goal-unlocked` e `goal-completed` (mesmo padrão do `OnEvent`).
3. Testes:
   - `internal/controller/app_test.go` — criar meta, simular `HandleGiftEvent` → milestone
     desbloqueia, segunda onda → meta completa.
   - `internal/view/integration_test.go` — cobertura das 4 rotas.

## Fase 4 — Frontend (web/index.html + web/renderer.js)

Passos:

1. `web/index.html` — nova seção "Metas da live" ao lado dos presentes-alvo (~linha 1143):
   título, meta (unidades), 3 linhas de milestones (valor + recompensa), salvar/cancelar/
   concluir, **barra de progresso** com % e valor atual.
2. `web/renderer.js`:
   - fetch inicial `GET /api/goals` + salvar/cancelar/concluir;
   - handlers SSE `goal-update` / `goal-unlocked` / `goal-completed`;
   - toast/banner "🎉 50% — recompensa: música especial"; badges nos milestones desbloqueados.
   - Reaproveitar os helpers de fetch e registro de SSE já existentes no arquivo.

---

## Verificação

1. `go build ./...` e `go vet ./...`.
2. `go test ./...` (novos testes de database, controller e view).
3. Manual: subir o app, `POST /api/goals`, disparar `HandleGiftEvent` via teste de
   integração para ver progressão/desbloqueio; abrir a UI e conferir barra + toast.
