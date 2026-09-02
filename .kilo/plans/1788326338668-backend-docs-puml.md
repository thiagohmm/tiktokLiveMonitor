# Plano — Documentação detalhada do backend + diagramas PlantUML

## Objetivo
Produzir documentação completa e navegável do backend (Go + ponte Node) em
**português (PT-BR)**: um arquivo `.md` por pacote (documentando cada arquivo e
cada função/tipo), um índice `README.md`, e diagramas **PlantUML** em arquivos
`.puml` separados. Nenhuma mudança em código-fonte; apenas arquivos de
documentação.

## Escopo e limites
- **Incluído:** todo o backend em `backend/` — código de produção (Go) e a ponte
  Node (`internal/monitor/*.js`).
- **Fora de escopo:** frontend (`frontend/`), arquivos de teste (`*_test.go`,
  `*.test.js`), `node_modules`, `dist/`. Testes serão citados apenas de forma
  resumida quando ajudarem a entender o comportamento.
- **Idioma:** PT-BR em todos os arquivos.
- **Formato:** um `.md` por pacote + diagramas `.puml` em subpasta própria.

## Localização dos artefatos
- Raiz da documentação: `docs/backend/`
- Diagramas: `docs/backend/diagrams/`
- Estrutura final:
  ```
  docs/backend/
  ├── README.md                  (índice + visão geral da arquitetura)
  ├── 01-model.md
  ├── 02-database.md
  ├── 03-monitor.md
  ├── 04-bridge-node.md
  ├── 05-controller.md
  ├── 06-ranking.md
  ├── 07-report.md
  ├── 08-auth.md
  ├── 09-view-http-sse.md
  ├── 10-entrypoint-ferramentas.md
  └── diagrams/
      ├── 00-arquitetura.puml
      ├── 01-fluxo-eventos.puml
      ├── 02-banco-er.puml
      ├── 03-autenticacao.puml
      ├── 04-sse-fanout.puml
      ├── 05-gift-streak-correlacao.puml
      └── 06-reconexao-monitor.puml
  ```

## Conteúdo de cada arquivo `.md`
Cada `.md` deve conter: breve descrição do pacote, o papel de cada arquivo, e
para cada arquivo a lista de **tipos/constantes/funções** com descrição curta,
assinatura e responsabilidade, referenciando `arquivo:linha` quando útil.

1. **`README.md`** — índice, pilha tecnológica, camadas (View → Controller →
   Model/Repository; Monitor + bridge Node; Auth; Database) e tabela dos
   diagramas com link.
2. **`01-model.md`** — `internal/model/`:
   - `entities.go`: structs `AnomalyLog`, `UserMessage`, `Gift`,
     `TargetGiftHistory`, `GoalMilestone`, `GiftGoal`, `PinnedComment`,
     `UserRank`, `LiveRanking`, `LiveReport`, `AnomalySummary`, `UserProfile`,
     `UserLiveSummary`, `Live`; constantes de status de meta, risco, tiers e
     modos de ranking.
   - `repository.go`: todas as interfaces de repositório, `LiveStat` e erros
     sentinela.
   - `giftvalues.go`: `giftValuesEnglish`, `giftValuesPortuguese`, `GiftValue()`,
     `normalizeGiftValueKey()`.
3. **`02-database.md`** — `internal/database/`:
   - `driver.go` (`rebindQuery`, `bind`, `exec`, `query`, `queryRow`,
     `insertID`, `upsertRoomLikeTotal`), `postgres.go` (`OpenPostgres`,
     `maxConns`, `OpenFromEnv`, `migratePostgres` + schema), `database.go`
     (todos os métodos do `DB` agrupados por repositório), `messagecache.go`
     (cache write-behind).
4. **`03-monitor.md`** — `internal/monitor/monitor.go` e `correlation.go`:
   eventos, `Monitor`, `New`, ciclo de vida (start/supervisor/reconnect),
   handlers de eventos, streaks de presente, correlação presente↔pergunta.
5. **`04-bridge-node.md`** — `bridge.js`, `gifts.js`, `follower.js`: conexão
   com `tiktok-live-connector`, comandos stdin, eventos stdout, resolução de
   seguidor e tradução de presentes.
6. **`05-controller.md`** — `internal/controller/`: `app.go` (orquestração,
   handlers de eventos, actions de ranking/report/profile, helpers), `gifts.go`
   (tabela de tradução), `goals.go` (metas, milestones, callback).
7. **`06-ranking.md`** — `internal/ranking/ranking.go`: pesos, `Compute`
   (engajamento) e `ComputeTikTok` (diamantes/tiers).
8. **`07-report.md`** — `internal/report/report.go`: geração determinística do
   relatório pós-live.
9. **`08-auth.md`** — `internal/auth/`: `auth.go` (JWT/middleware),
   `lockout.go` (anti brute-force + IP confiável), `supabase_auth.go`,
   `supabase_admin.go`, `theme.go`.
10. **`09-view-http-sse.md`** — `internal/view/`: `server.go` (rotas, SSE
    fan-out, hardening), `auth_handlers.go`, `cors.go`.
11. **`10-entrypoint-ferramentas.md`** — `backend/main.go` e
    `cmd/sseload/main.go`.

## Diagramas PlantUML (`.puml`)
Cada diagrama com `@startuml`/`@enduml`, título e legenda. Usar nomes reais dos
componentes/funções/tabelas.

1. **`00-arquitetura.puml`** — componente: Frontend (nginx) → View HTTP/SSE →
   Controller → (Monitor + bridge Node → TikTok) e (Repository → PostgreSQL
   Supabase); Auth/Supabase Auth como ator externo.
2. **`01-fluxo-eventos.puml`** — sequência: TikTok live → `bridge.js` (stdout) →
   `monitor.handleBridgeEvent` → `OnEvent` → `controller.Handle*Event` → `repo`
   → `broadcastSSE` → cliente SSE.
3. **`02-banco-er.puml`** — ER das tabelas criadas em `migratePostgres`
   (`anomaly_logs`, `gifts`, `shares`, `likes`, `room_like_totals`,
   `user_messages`, `target_gift_history`, `gift_goals`, `pinned_comments`,
   `settings`, `false_positives`) com chaves e colunas relevantes.
4. **`03-autenticacao.puml`** — sequência: login (lockout → Supabase token →
   cookie), signup (pendente), middleware JWT e endpoints `/api/admin/*`
   (`RequireAdmin`).
5. **`04-sse-fanout.puml`** — componente/sequência do `handleSSE` +
   `broadcastSSE` + `sseWriteLoop`: buffer por cliente, ejeção de lento/morto,
   ping, teto de clientes.
6. **`05-gift-streak-correlacao.puml`** — sequência: `any-gift-received` →
   streak pendente → liquidação (repeatEnd/timeout) → `new-gift-user` →
   presente-alvo + `correlateGiftWithQuestion`.
7. **`06-reconexao-monitor.puml`** — sequência/estado do `runSupervisor`:
   detecção de queda → backoff exponencial com jitter → restart do bridge →
   reenvio de `connect`.

## Ordem de execução
1. Criar `docs/backend/` e `docs/backend/diagrams/`.
2. Escrever `README.md` (índice/visão geral) por último, após os demais, para
   garantir que os links batem.
3. Escrever os `.md` de pacote na ordem 01→10.
4. Escrever os `.puml` (ordem acima).
5. Validar e finalizar.

## Validação
- Checar que todos os links do `README.md` apontam para arquivos existentes.
- Conferir que todos os símbolos documentados existem no código (nomes de
  funções/tipos batem com os arquivos lidos).
- Sintaxe PlantUML: garantir `@startuml`/`@enduml` pareados e sintaxe válida;
  se `plantuml` estiver disponível no host, renderizar para checar erro de
  sintaxe (sem gerar imagens em repositório, salvo se solicitado).
- Rodar `git status` ao final para confirmar que só arquivos de `docs/backend/`
  foram adicionados (nenhuma alteração em código).

## Riscos / observações
- Diagramas não têm FKs reais no banco (relações por `live_name`/`uniqueId`);
  documentar isso no `02-banco-er.puml` para não induzir a erro.
- Não há renderizador PlantUML embutido no ambiente; os `.puml` são entregues
  como fonte, renderizáveis no PlantUML server/IDE.
- O comentário no código mistura PT/EN; a documentação padroniza em PT-BR.
