# Backend — TikTok Live Monitor (documentação)

Documentação técnica do **backend** do projeto (Go + ponte Node), gerada a
partir da leitura do código em `backend/`. Escrita em PT-BR.

> O que é: um monitor de lives do TikTok que se conecta ao stream de um
> usuário via `tiktok-live-connector` (Node), converte os eventos em tempo real
> (mensagens, presentes, curtidas, shares, pins), persiste em PostgreSQL
> (Supabase) e entrega tudo para uma UI via **SSE** e **REST API**.

---

## Pilha e visão geral

| Camada | Pasta | Tecnologia |
|---|---|---|
| Entrypoint | `backend/main.go` | Go — composição das camadas |
| View (HTTP/SSE) | `backend/internal/view/` | Go `net/http` |
| Controller | `backend/internal/controller/` | Go |
| Monitor (estado da live) | `backend/internal/monitor/` | Go |
| Ponte Node (TikTok) | `backend/internal/monitor/*.js` | Node `tiktok-live-connector` |
| Ranking / Report | `backend/internal/ranking/`, `.../report/` | Go |
| Auth (Supabase) | `backend/internal/auth/` | Go + Supabase Auth |
| Model (contratos) | `backend/internal/model/` | Go (interfaces/entidades) |
| Database (Postgres) | `backend/internal/database/` | Go `pgx` |

Fluxo em uma frase: **TikTok → bridge.js (Node) → monitor → controller →
database (Postgres)**, e tudo que acontece também é **difundido por SSE** à UI
pela camada `view`. A UI (`frontend/`) é servida em separado e faz proxy para
`/api/*` e `/events`.

---

## Índice — documentação por pacote

| # | Arquivo | Conteúdo |
|---|---|---|
| — | [`README.md`](README.md) | Este índice + visão geral |
| 01 | [`01-model.md`](01-model.md) | Entidades, constantes de domínio e interfaces de repositório; tabela de valores (💎) de presentes |
| 02 | [`02-database.md`](02-database.md) | Conexão, schema (`migratePostgres`), tradução `?`→`$n`, todos os métodos do `DB`, cache write-behind de mensagens |
| 03 | [`03-monitor.md`](03-monitor.md) | Monitor Go: bridge, supervisor de reconexão, eventos, streaks de presente e correlação presente↔pergunta |
| 04 | [`04-bridge-node.md`](04-bridge-node.md) | Ponte Node: `bridge.js`, `gifts.js`, `follower.js` |
| 05 | [`05-controller.md`](05-controller.md) | `AppController`, handlers de eventos, presentes-alvo, tradução PT-BR e metas de presentes |
| 06 | [`06-ranking.md`](06-ranking.md) | Ranking de engajamento e modo TikTok (diamantes/tiers) |
| 07 | [`07-report.md`](07-report.md) | Relatório pós-live determinístico |
| 08 | [`08-auth.md`](08-auth.md) | JWT/middleware, lockout de login, clientes Supabase (auth + admin) |
| 09 | [`09-view-http-sse.md`](09-view-http-sse.md) | Servidor HTTP, rotas, fan-out SSE, auth handlers, CORS e hardening |
| 10 | [`10-entrypoint-ferramentas.md`](10-entrypoint-ferramentas.md) | `main.go` e a ferramenta de load test `cmd/sseload` |

---

## Diagramas (PlantUML)

Arquivos `.puml` (fonte) em [`diagrams/`](diagrams/). Renderize com PlantUML
(extensão de IDE, plantuml.com/plantuml ou `plantuml` CLI).

| Diagrama | O que mostra |
|---|---|
| [`00-arquitetura.puml`](diagrams/00-arquitetura.puml) | Visão de componentes: View → Controller → Monitor/Database/Ranking/Report, ponte Node e Supabase. |
| [`01-fluxo-eventos.puml`](diagrams/01-fluxo-eventos.puml) | Sequência de um evento da live (chat) até o SSE e o banco (com cache write-behind). |
| [`02-banco-er.puml`](diagrams/02-banco-er.puml) | Entidades do Postgres (tabelas e colunas) e relações conceituais por `live_name`/`uniqueId`. |
| [`03-autenticacao.puml`](diagrams/03-autenticacao.puml) | Sequência de login, cadastro, middleware JWT e `RequireAdmin`. |
| [`04-sse-fanout.puml`](diagrams/04-sse-fanout.puml) | Fan-out de SSE: canal por cliente, teto, ping e ejeção de clientes lentos/mortos. |
| [`05-gift-streak-correlacao.puml`](diagrams/05-gift-streak-correlacao.puml) | Streaks de presente (combos), liquidação por timeout e correlação presente↔pergunta. |
| [`06-reconexao-monitor.puml`](diagrams/06-reconexao-monitor.puml) | Supervisor de reconexão do bridge com backoff exponencial + jitter. |

---

## Como ler o backend (sugestão de ordem)

1. **`10-entrypoint-ferramentas.md`** — veja o `main()` e como as camadas são
   montadas (wiring).
2. **`00-arquitetura.puml`** + **`09-view-http-sse.md`** — entenda a "porta de
   entrada" (SSE + REST).
3. **`05-controller.md`** — o orquestrador que liga tudo.
4. **`01-model.md`** + **`02-database.md`** — os dados (entidades, contratos,
   schema Postgres).
5. **`03-monitor.md`** + **`04-bridge-node.md`** — como o stream do TikTok é
   recebido e interpretado.
6. **`06-ranking.md`** / **`07-report.md`** — cálculos derivados.
7. **`08-auth.md`** — segurança (login/JWT/admin).

## Notas gerais

- **Sem IA:** todo o processamento (perguntas frequentes, correlação
  presente↔pergunta, relatório) é **determinístico** (heurísticas/SQL) — não há
  chamadas de LLM no backend.
- **Comunicação Node↔Go:** JSON Lines por stdin/stdout de um processo filho;
  ver `03-monitor.md` e `04-bridge-node.md`.
- **Nomes de presentes:** traduzidos para PT-BR na origem (`monitor/gifts.js`)
  e no Go (`controller/gifts.go`); o valor em moedas vem de `model/giftvalues.go`.
- **Regra de unidades:** metas e relatórios usam **soma de `repeat_count`**
  (unidades), enquanto o ranking "modo TikTok" e perfis usam o **valor em 💎**
  (preço × repetição).
