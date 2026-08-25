# Plano: Aba de Administração (Lives e Horários)

## Objetivo

Criar uma nova aba de administração no front-end que liste as lives e os horários
registrados no banco de dados. O front é vanilla JS (`web/index.html` + `web/renderer.js`)
e o back é Go (`internal/controller`, `internal/view`, `internal/model`).

## Premissas e padrões existentes

- Rotas HTTP em `internal/view/server.go` (`mux.HandleFunc("/api/...")`).
- Lógica de negócio em `internal/controller/app.go`.
- Acesso ao banco em `internal/model/repository.go` (interface) e implementação em `internal/database`.
- Front: abas em `web/index.html`, renderização em `web/renderer.js`.

---

## Fase 1 — Levantamento e preparação

Tarefas pequenas e independentes:

- [x] **1.1** Inspecionar o schema do banco (tabelas de lives e horários) e anotar os
      campos exatos (nome da live, início, fim, status, user_id, etc.).
- [x] **1.2** Definir o formato JSON que o front vai consumir (ex.: `{ lives: [{ name,
startedAt, endedAt, status, ... }] }`).
- [x] **1.3** Criar branch de desenvolvimento → criada `feature/admin-lives` (a partir de `go-backend`).

### Resultado do levantamento (1.1)

- O arquivo raiz `data.db` está **vazio (0 bytes)** — não é o banco em uso.
- O banco real é `data/feedback.db` (WAL), criado em `internal/database/database.go`.
- **Não existe tabela dedicada de "lives" nem de "horários"/agendamentos.**
  As lives são implícitas: a coluna `live_name` presente nas tabelas abaixo.
- Tabelas existentes:
  - `anomaly_logs` (id, live_name, day DATE, timestamp, uniqueId, comment, is_anomaly, category)
  - `gifts` (id, live_name, uniqueId, nickname, gift_name, repeat_count, gift_type, timestamp)
  - `shares` (id, live_name, uniqueId, nickname, timestamp)
  - `user_messages` (id, live_name, uniqueId, username, message, timestamp)
  - `target_gift_history` (id, live_name, uniqueId, nickname, gift_name, received_at, answered_at, response_type)
  - `pinned_comments` (id, live_name, uniqueId, nickname, comment, pin_id, ...)
  - `false_positives` (id, comment, category, expected, timestamp)
- O banco atual está **sem registros** (tabelas vazias).
- Não há conceito de "schedule"/horário agendado no código (`internal/` não possui).

### Decisão de design

Como não há tabela de lives/horários, a aba de administração vai **derivar** os dados:

- **Lives**: `SELECT live_name, MIN(timestamp) AS started_at, MAX(timestamp) AS ended_at,
COUNT(*) AS events FROM (todas as tabelas com live_name) GROUP BY live_name`
  — ou, mais simples, agrupar por `live_name` + `day` em `anomaly_logs` (que tem `day DATE`).
- **Horários**: agrupamento por dia (`day` / `DATE(timestamp)`) mostrando o intervalo
  de atividade (primeiro e último evento do dia).

### Formato JSON definido (1.2)

`GET /api/admin/lives` →

```json
{
  "lives": [
    {
      "name": "nome_da_live",
      "day": "2026-08-24",
      "startedAt": "2026-08-24T19:00:00Z",
      "endedAt": "2026-08-24T21:30:00Z",
      "events": 1234
    }
  ]
}
```

## Fase 2 — Backend: camada de dados

- [x] **2.1** Criar struct `Live` em `internal/model/entities.go` (Name, Day, StartedAt, EndedAt, Events).
- [x] **2.2** Adicionar `ListLives(limit int) ([]Live, error)` à interface `RankingRepository` em `internal/model/repository.go`.
- [x] **2.3** Implementar `ListLives` em `internal/database/database.go` (UNION ALL das 6 tabelas com `live_name`, agrupado por `live_name` + `DATE(timestamp)`, ordenado por dia desc, com `LIMIT`).
- [x] **2.4** `ListLiveSchedules` — **mesclada em 2.3**: não há tabela de horários separada; o agrupamento por dia já cobre "horários".
- [x] **2.5** Testes de unidade em `database_test.go`: `TestListLives`, `TestListLivesEmpty`, `TestListLivesLimit` — todos passando.

## Fase 3 — Backend: API

- [x] **3.1** Criar handler `handleAdminLives` em `internal/view/server.go` (GET, query `limit` com default 100, responde `{ lives: [...] }`).
- [x] **3.2** Registrar a rota `mux.HandleFunc("/api/admin/lives", s.handleAdminLives)`.
- [x] **3.3** Adicionar método `GetLives(limit)` em `internal/controller/app.go` (delega para `repo.ListLives`).
- [x] **3.4** Erros: 405 para método inválido, 500 com mensagem em falha de banco, 200 com lista vazia quando não há lives.
- [x] **3.5** Testes de integração em `internal/view/integration_test.go`: `TestHandleAdminLives` (seed via `AddGift`, valida JSON) e `TestHandleAdminLivesMethodNotAllowed` — `go test ./...` passando.

## Fase 4 — Front-end: estrutura da aba

- [x] **4.1** Adicionar a seção "Administração — Lives e Horários" em `web/index.html` (mesmo padrão de seção do app: `section-title` + `table-container`; o front não usa abas de navegação, usa seções empilhadas).
- [x] **4.2** Seção única cobrindo lives e horários (os dados são derivados por live + dia, então uma tabela atende às duas).
- [x] **4.3** Tabela `<thead>` com colunas: Live, Dia, Início, Fim, Duração, Eventos + botão "Atualizar".
- [x] **4.4** Em `web/renderer.js`: `loadAdminLives()` (fetch `/api/admin/lives?limit=100`), chamada no `bootstrap()` e no botão "Atualizar". `node --check` OK.

## Fase 5 — Front-end: renderização

- [x] **5.1** `renderAdminLives(lives)` em `web/renderer.js` popula a tabela (nome, dia, início, fim, duração, eventos).
- [x] **5.2** `renderSchedules` — **coberta por 5.1**: a mesma tabela mostra os horários (dia + intervalo).
- [x] **5.3** `formatAdminTime` (fuso local pt-BR) e `formatAdminDuration` (ex.: `2h 30min`).
- [x] **5.4** Estado vazio ("Nenhuma live registrada") e erro de rede ("Não foi possível carregar as lives") sem quebrar a UI.
- [x] **5.5** Paginação simples: botão "Carregar mais" (aparece quando o resultado atinge o `limit`, incrementa em +100); "Atualizar" reseta o limit. `node --check` OK.

## Fase 6 — Testes e finalização

- [x] **6.1** Teste de integração do endpoint + teste com banco vazio (`TestHandleAdminLivesEmptyDB`).
- [x] **6.2** Smoke test: servidor real + `curl /api/admin/lives` retornou 14 lives reais do `data/feedback.db` (ex.: `7rosasdoluar` 2026-08-23 com 688 eventos).
- [x] **6.3** Revisão de estilos: a seção usa as classes existentes (`section-title`, `table-container`, `small-btn`), herdando o tema do app.
- [x] **6.4** `go test ./...` passando (controller, database, moderation, monitor, ranking, view) e `node --check web/renderer.js` OK.
- [x] **6.5** Commit final na branch `feature/admin-lives` e merge em `go-backend`.

---

## Critérios de aceite

- [x] Seção "Administração" visível no front (padrão de seções empilhadas do app).
- [x] Tabela de lives exibe os registros do banco com datas formatadas.
- [x] Horários exibidos por dia (início/fim/duração) na mesma tabela.
- [x] Estados vazio e de erro tratados sem quebrar a UI.
- [x] Testes de backend passando.

## Fase 7 — Exclusão de lives (acréscimo)

- [x] **7.1** `DeleteLive(liveName)` na interface `RankingRepository` + implementação em `internal/database` (DELETE nas 6 tabelas com `live_name`, retorna total de linhas removidas).
- [x] **7.2** `DeleteLive` no controller + rota `POST /api/admin/lives/delete?live=...` (400 sem `live`, 405 em método inválido, 500 em erro).
- [x] **7.3** Front: botão "Deletar" por linha (coluna "Ações"), com `confirm()` e recarga da lista após sucesso.
- [x] **7.4** Testes: `TestDeleteLive` (database) e `TestHandleAdminLivesDelete` (integração, incl. 400/405) — todos passando.

---

## Fora de escopo (por enquanto)

- Edição/criação de lives e horários (exclusão já implementada na Fase 7).
- Autenticação da aba de administração.
- Filtros avançados (busca por nome, intervalo de datas) — podem vir em fase futura.
