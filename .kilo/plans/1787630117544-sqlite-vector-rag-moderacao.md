# Plano: Vector DB no SQLite + RAG para melhorar a detecção (moderação)

## Objetivo

Melhorar a detecção de conteúdo impróprio na live com RAG sobre um índice vetorial
em SQLite. **Toda a IA fica no agente Python**; o Go mantém só integração (start do
agente, SSE, ingestão de flags, alerts/storage). As **regras regex saem do Go** e são
reescritas em Python como pré-filtro/fallback determinístico.

## Princípio de arquitetura (pedido: "manter a arquitetura existente")

Seguir os padrões já presentes no código, sem reescrever o que existe:

- **Python:** módulo por responsabilidade com injeção de dependência (DIP) e Protocol
  (ISP), como em `llm.py` (`ChatModel`), `feedback.py` (`FeedbackStore`),
  `buffer.py` (`EventSink`/`LiveBuffer`), `router.py`/`history.py` (serviços com deps
  injetadas). Os módulos novos (`embed.py`, `vectors.py`, `rules.py`, `moderate.py`)
  seguem esse mesmo padrão. Os módulos existentes são **estendidos**, não reescritos.
- **Go:** camadas `model` → `database` → `controller` → `view` preservadas. Só se
  **remove** o pacote `internal/moderation` (consequência direta de mover as regras) e
  se **adiciona** um endpoint de plumbing; nenhuma lógica de IA entra no Go.

## Decisões de design (acordadas)

1. **IA 100% no Python.** No Go ficam: start do agente, SSE `/events`, e o novo endpoint
   `POST /api/moderation/flag` (plumbing). Nada de IA/classificação no Go.
2. **Embeddings:** modelo dedicado via **`fastembed` + ONNX**
   (`paraphrase-multilingual-MiniLM-L12-v2`, 384 dims). Sem torch; CPU/ARM64 do Pi.
3. **Vector store:** tabela SQLite com embedding em `BLOB` float32; cosseno em
   **Python puro** (`struct` + `math`).
4. **Regras regex:** movidas para Python (`agent/rules.py`, port 1:1 de
   `classifyByRules`/`foldText`/allowlist). O `internal/moderation` do Go é removido.
5. **Corpus do RAG:** `feedback` (false_positives), `anomaly` (anomaly_logs flagados) e
   `chat` (user_messages via ingestão SSE).
6. **RAG no copiloto `/ask`:** busca semântica nas fontes `chat` + `feedback`/`anomaly`
   quando a pergunta não casa com nenhuma ferramenta (e como grounding da resposta).

## Arquitetura do fluxo (pós-implementação)

```
TikTok → bridge.js → Go monitor ──emit "new-chat-message"──┐
                                                          ├─► SSE /events ─► agente Python
                                                          │        ├─ buffer (permanece)
                                                          │        └─ RagModerator (novo):
                                                          │            1. embed (uma vez) → indexa 'chat'
                                                          │            2. regras (Python) → se flagar: envia flag + indexa 'classify'
                                                          │            3. senão: RAG(feedback+anomaly) → LLM few-shot
                                                          │               → se flagar: envia flag + indexa 'classify'
                                                          │        flag?
                                                          ▼
                                      POST {MONITOR_URL}/api/moderation/flag
                                                          │
                                        Go (plumbing): gate por settings, dedup leve,
                                        emit "flagged-message" → alerts + UI + anomaly_logs
```

A flag de repetição (`REPETICAO`, no `monitor.go`) **permanece no Go** — não é IA.

## Componentes novos (Python, seguindo os padrões atuais)

- `agent/embed.py` — Protocol `Embedder` (espelha `ChatModel`) + `FastembedEmbedder`
  (lazy-load, `asyncio.to_thread` para não travar o loop). `async embed(texts)`.
- `agent/vectors.py` — `VectorStore` (espelha `FeedbackStore`: sqlite3, WAL,
  `busy_timeout`, lock). Fontes: `feedback`/`anomaly`/`chat`/`classify`. Métodos
  `upsert(source, comment, category, embedding)` (dedup por `source+folded(comment)`),
  `search(vec, k, sources)`, `count`, `close`. `search` exclui `classify` por padrão.
- `agent/rules.py` — port 1:1 de `foldText`, `looksQuestion`, `passes*AiGate`,
  `classifyByRules` + allowlist em memória (carregado de `false_positives` e atualizado
  a cada `/feedback`). Retorna `{flagged, category, reason}`.
- `agent/moderate.py` — `RagModerator` (implementa `EventSink`; deps injetadas:
  Embedder, VectorStore, ChatModel, MonitorClient):
  - lê settings via `state` (inicial) + evento SSE `settings-update`;
  - `ingest("new-chat-message")` enfileira num worker (fila limitada + semáforo
    `RAG_CONCURRENCY`, drop sob pressão, dedup recente `(uniqueId, folded)`);
  - pipeline por mensagem: `embed`→indexa `chat` (se `RAG_CHAT_INDEX_ENABLED`) →
    `rules.classify`→(flag: envia flag + indexa `classify`, para) → senão
    `search`→few-shot→`llm.chat`→parse token→(flag: envia flag + indexa `classify`);
  - envia flag via `MonitorClient.flag()` (POST `/api/moderation/flag`).
- `agent/sse.py` — `SSEClient` passa a aceitar lista de sinks (fanout)
  `[MessageBuffer, RagModerator]`; contrato `EventSink` mantido (mudança aditiva).
- `agent/router.py` — `Copilot` ganha RAG opcional (Embedder + VectorStore): quando a
  rota é `NONE` (ou como grounding), faz `search` nas fontes `chat`/`feedback`/`anomaly`
  com o embedding da pergunta e injeta um bloco "CONTEXTO RECUPERADO" no prompt final.
  Falha de embedding ⇒ segue sem RAG.
- `agent/api.py` — lifespan monta e expõe `embedder`, `vector_store`, `rag_moderator`;
  fanout no SSE; endpoint `POST /moderate` (teste manual); `/feedback` passa a indexar o
  exemplo corrigido; task de backfill; `finally` fecha o store.
- `agent/config.py` — `FASTEMBED_MODEL`, `FASTEMBED_CACHE`, `RAG_TOP_K` (8),
  `RAG_BACKFILL_LIMIT` (500), `RAG_CONCURRENCY` (1), `RAG_TIMEOUT` (8s),
  `RAG_CHAT_INDEX_ENABLED` (1).
- `agent/tools.py` — adicionar `MonitorClient.flag(payload)` (aditivo).
- `requirements.txt` — adicionar `fastembed` (puxa `onnxruntime` + `numpy`).

## Mudanças no Go (plumbing, preservando as camadas)

- **Remover** `internal/moderation/` e referências:
  - `main.go`: não instancia `moderation.NewEngine`; `NewAppController` sem `modEngine`.
  - `controller/app.go`: remove `modEngine`, `handleModerationEvent` e seu registro,
    `GetStartupStatus`, `ClearModerationCache`, `WarmupModeration`. Mantém
    `handleAnomalyEvent` (alerta de `EventFlaggedMessage`).
  - `view/server.go`: remove warmup em `handleConnect`; simplifica `handleFeedback`
    (só forward ao agente); ajusta `handleReadiness` (UI não consome; retornar
    `{ready: true}`).
  - Atualizar `internal/view/integration_test.go` (construtor).
- **Adicionar** `POST /api/moderation/flag` (`handleModerationFlag`):
  - body `{comment, uniqueId, nickname, category, reason, source?}`;
  - valida `comment`/`category` (400); gate por `GetSettings().ModerationEnabled`
    (ignora se desabilitado, 200 no-op);
  - dedup leve por `(uniqueId, folded comment)` recente (set cap ~500);
  - emite `monitor.EventFlaggedMessage` e loga `repo.LogAnomaly(liveName, comment, true,
    category, uniqueId)` — mesmo caminho de alerta/UI de hoje.
- `GetFalsePositiveComments` fica morto no Go (o Python lê `false_positives` da própria
  conexão SQLite); remoção opcional da interface/implementação.

## Tarefas (ordem de execução)

1. **Embeddings** — `agent/embed.py` (Protocol `Embedder` + impl fastembed,
   `asyncio.to_thread`); teste com texto curto PT-BR (dimensão 384 + normalização).
2. **Vector store** — `agent/vectors.py` (schema, `upsert`, `search`, dedup, `close`);
   testes de pack/unpack BLOB, ordenação por similaridade, exclusão de `classify`.
3. **Regras em Python** — `agent/rules.py` portando `classifyByRules`/`foldText`/allowlist;
   portar os casos de `moderation_test.go` para `unittest` e comprovar equivalência.
4. **RagModerator** — `agent/moderate.py` (pipeline, worker com fila/semáforo/drop,
   ingest `new-chat-message` e `settings-update`, envio de flag) com fakes.
5. **Fanout SSE + lifespan** — `agent/sse.py` (lista de sinks) e `agent/api.py`
   (wiring, `/moderate`, `/feedback` indexa, backfill `feedback`+`anomaly`+`chat`).
6. **Copiloto com RAG** — `agent/router.py` (retrieval 3 fontes + grounding) com fakes.
7. **Go: remover engine** — apagar `internal/moderation`, limpar `main.go`,
   `controller/app.go`, `view/server.go` e testes afetados.
8. **Go: flag endpoint** — `handleModerationFlag` + rota + dedup + gate de settings;
   testes de integração (200 flag emitido, 400 sem comment, ignore quando desabilitado).
9. **Config/docs** — `agent/config.py` + `requirements.txt` + `.env.example`
   (`FASTEMBED_*`, `RAG_*`); nota no Dockerfile sobre cache do modelo fastembed
   (pré-download/volume para deploy offline no Pi).

## Validação

- **Python** (`python3 -m unittest agent.test_agent`): embedder, vector store, regras
  (equivalência com o Go), RagModerator (few-shot, parse de token, flag via monitor fake),
  copiloto com RAG, `/moderate` (200/503), backfill idempotente.
- **Go** (`go test ./...`): remoção do engine sem regressão; testes do flag endpoint.
- **Smoke manual**: subir servidor + llama-server; (1) fastembed embebe texto PT-BR;
  (2) um spam sutil (não pego por regex) vira `flagged-message` via SSE/alerts;
  (3) `curl -X POST /agent/moderate` retorna categoria coerente; (4) `/ask` responde
  pergunta aberta citando comentários recuperados; (5) feedback corrigido atualiza
  allowlist e índice; (6) frontend: flags e feedback seguem funcionando sem mudanças.

## Riscos e mitigações

- **Agente Python fora do ar ⇒ sem detecção** (regras saíram do Go). `agent.go` já
  reusa instância viva e re-spawna; documentar que a detecção depende do agente.
- **Equivalência das regras portadas** — mitigada portando os testes do Go para Python.
- **fastembed no Pi (onnxruntime ARM64)** — wheels aarch64 ok; pré-baixar modelo em
  volume/cache para deploy offline.
- **Latência** (embed + LLM por mensagem) — regras curto-circuitam o óbvio; fila limitada
  + semáforo + drop; dedup por `(uniqueId, folded)`.
- **Auto-treinamento (`classify`)** — excluído da recuperação por padrão; só
  `feedback`/`anomaly`/`chat` alimentam few-shots e o `/ask`.
- **Flags duplicados** — dedup leve no Go; regras→LLM já short-circuitam no Python.

## Fora de escopo

- RAG no endpoint `/ask-ai` (compat antiga) — apenas `/ask` (Copilot).
- Modelo de embedding além do fastembed escolhido.
- Autenticação da aba de administração e do flag endpoint.
