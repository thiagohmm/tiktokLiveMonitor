# Plano de unificação: Assistente de IA (Go) → Agente de IA (Python)

**Objetivo:** eliminar o assistente de IA escrito em Go e incorporar todas as suas funções no agente Python existente (`agent/`). O Go passa a ser apenas o monitor da live.

**Decisões confirmadas**
- O `llama-server` passa a ser **spawnado e dono pelo Python** (o Go não gerencia mais o processo do LLM).
- O `feedback.db` **migra para o Python** (persistência de feedback passa a viver no agente).

---

## Estado atual

### Assistente (Go) — será removido
| Componente | Responsabilidade |
|---|---|
| `internal/ai/` (`Manager`, `llm.go`) | Spawn/gerencia o worker `llama-server`, probe de saúde, registro de worker |
| `internal/service/ai.go` (`AskAI`) | Busca resumo de gifts + mensagens do usuário (`model.Repository`), monta contexto, chama o LLM, suporta histórico em cache |
| `internal/view/server.go` | Handlers `/api/ask-ai`, `/api/probe-llm`, `/api/worker/register`, AI em `/api/readiness` |
| `internal/database` + `internal/model` | `feedback.db` — feedback de falsos positivos da moderação (`/api/feedback`, `AddFeedback`) |

### Agente (Python, `agent/`) — será o único sistema de IA
| Arquivo | Responsabilidade |
|---|---|
| `api.py` | FastAPI: `/ask`, `/summarize`, `/health` |
| `router.py` | `Router` (intenção: LLM ou heurística), `ToolRegistry`, `Copilot` |
| `tools.py` | `MonitorClient` — chama a API Go (`/api/state`, `/api/ranking`, `/api/profile`, `/api/gifts`, `/api/history`, `/api/report`, `/api/pinned-comments`, `/api/target-gift-history`) |
| `buffer.py` / `context.py` | Buffer de eventos ao vivo + montagem de contexto |
| `sse.py` | Consome `/events` do Go e alimenta o buffer |
| `llm.py` | `LlamaServerChatModel` — cliente do `llama-server` |
| `summary.py` | `LiveSummarizer` |

---

## Fase 1 — Agente absorve as funções do assistente

1. **`agent/llm_worker.py` (novo)** — gestão do worker `llama-server` (migração de `internal/ai.Manager`):
   - Spawn do processo (`llama-server` com o modelo de `model-config.json`), monitor de saúde, restart e shutdown limpo no `lifespan`.
   - Endpoints no `api.py`: `GET /probe-llm` e `POST /worker/register` (mesmo contrato dos endpoints Go, para compatibilidade).
2. **`agent/history.py` (novo)** — absorção de `internal/service/ai.go`:
   - Busca de histórico de mensagens/gifts via `MonitorClient` (`/api/history`, `/api/gifts`) — a parte de dados ao vivo já está coberta por `buffer.py` + `context.py`.
   - Suporte a **conversa contínua** (mensagens em cache por sessão), como o `AskAI` faz hoje.
3. **`agent/feedback.py` (novo)** — migração do `feedback.db`:
   - SQLite `feedback.db` escrito pelo Python (mesmo schema do `internal/database` Go: feedback de falsos positivos da moderação).
   - Endpoint `POST /feedback` no `api.py` com o mesmo contrato do `/api/feedback` Go.
   - Usar o mesmo arquivo `feedback.db` atual (WAL) ou recriar com migração dos registros existentes.
4. **`agent/api.py`** — novos endpoints:
   - `POST /ask-ai` (contrato compatível com o assistente Go, incluindo histórico).
   - `GET /probe-llm`, `POST /worker/register`, `POST /feedback`.

## Fase 2 — Go deixa de ser assistente

5. Remover do Go:
   - `internal/ai/` (pasta inteira).
   - `internal/service/ai.go`.
   - Handlers `/api/ask-ai`, `/api/probe-llm`, `/api/worker/register`.
   - Persistência de feedback: `AddFeedback` e o `feedback.db` em `internal/database`/`internal/model` (o arquivo passa a ser dono do Python).
   - Trecho de AI em `handleReadiness` (mantém apenas a readiness do monitor).
6. **Camada de compatibilidade (transitória):** handler Go fino que apenas repassa `/api/ask-ai`, `/api/probe-llm`, `/api/feedback` para `agent:PORT/*` (o proxy `AgentProxy` em `/agent/` já existe e vira o caminho principal). Remover quando o frontend for atualizado (fora do escopo deste plano).

## Fase 3 — Validação

7. **`agent/test_agent.py`** — cobrir:
   - `/ask-ai` com e sem histórico de conversa.
   - `/probe-llm` com worker vivo e morto (fallback determinístico do `Router` usando o buffer).
   - `/feedback` (persistir e consultar).
   - Ciclo de vida do worker: spawn, health, shutdown no `lifespan`.
8. **Teste de fumaça:** subir Go + Python, conectar a uma live, perguntar via `/agent/ask-ai` e enviar feedback; comparar a qualidade da resposta com a do assistente antigo.

---

## Riscos / cuidados
- **Dono único do `llama-server`:** garantir que Go e Python nunca spawnem o processo ao mesmo tempo (remover o spawn do Go antes de ativar o do Python, ou portar a porta/config para o `model-config.json` lido pelo Python).
- **`feedback.db` compartilhado:** durante a migração, evitar escrita simultânea Go + Python no mesmo SQLite (cortar a escrita Go na Fase 2).
- **Fallback sem LLM:** o `Router` já responde com dados determinísticos do buffer — manter esse comportamento quando o worker estiver fora.

## Critério de aceite
- O Go não contém mais código de LLM, `ask-ai` nem escrita em `feedback.db`.
- Todas as funções do assistente (perguntas com contexto e histórico, probe do LLM, registro de worker, feedback) estão disponíveis no agente Python.
- Suítes de teste Go e Python passando.
