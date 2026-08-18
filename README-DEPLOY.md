# Deploy: Vercel + Supabase

Branch: `deploy/vercel-supabase`

Ajustes desta branch para o projeto rodar 100% gerenciado:

| Componente | Local/Docker (main) | Vercel (esta branch) |
|---|---|---|
| Banco | SQLite (`feedback.db`) | Supabase (Postgres) — `SUPABASE_DB_URL` |
| LLM | llama-server local (GGUF) | Remoto OpenAI-compatível — `LLM_ENDPOINT` |
| UI | servida pelo backend Go | mesma, mas com base da API configurável (`window.__API_BASE__`) e CORS liberado no backend |
| Build Go | `CGO_ENABLED=1` (go-sqlite3) | `CGO_ENABLED=0` (o driver `pgx` é puro Go) |

---

## 1. Supabase (banco de dados)

1. Crie um projeto em [supabase.com](https://supabase.com).
2. Abra **SQL Editor** e execute o conteúdo de [`supabase/schema.sql`](supabase/schema.sql)
   (cria `false_positives`, `anomaly_logs`, `gifts`, `user_messages`,
   `target_gift_history` — espelho do schema SQLite).
3. Em **Project Settings → Database**, copie a *connection string* (pública):
   ```
   postgres://postgres:SENHA@db.<projeto>.supabase.co:5432/postgres
   ```
   Use como valor de `SUPABASE_DB_URL` na Vercel.
   > Sem RLS: o acesso é feito pelo backend via pooler, nunca pelo navegador.
   > Se preferir, use a string *Session* (porta 5432) — é a que o código usa.

## 2. Backend Go na Vercel

Na Vercel: **Add New → Project** → importe o repositório, selecione a branch
`deploy/vercel-supabase` e configure:

- **Framework Preset**: *Other*
- **Root Directory**: (vazio — o backend está na raiz)
- **Build Command**: `docker build -f Dockerfile.vercel .` *(se escolhendo o build Docker)* —
  ou use o Dockerfile diretamente: em **Project Settings → General → Build & Development Settings**,
  marque que o projeto usa Docker e aponte para `Dockerfile.vercel` (Vercel detecta o Dockerfile na raiz).

Variáveis de ambiente (Project → Settings → Environment Variables):

| Variável | Obrigatória | Descrição |
|---|---|---|
| `SUPABASE_DB_URL` | Sim | Connection string do Supabase (passo 1) |
| `LLM_ENDPOINT` | Sim (sem LLM fica só regex) | URL do worker LLM remoto, ex.: `http://host:8080` |
| `CORS_ORIGIN` | Não | Origem da UI liberada (padrão `*`) |
| `PORT` | Não | A Vercel injeta automaticamente |

### Worker LLM remoto (obrigatório para a moderação com IA)

O local LLM (GGUF + llama.cpp) não roda em serverless. Opções:

- **Outro serviço na Vercel** (Flame) com o mesmo `Dockerfile` da branch
  `go-backend` + variáveis — ou uma **GPU** Vercel (Flame) rodando llama.cpp;
- **Render / Fly.io / Railway** com o `Dockerfile` original (multi-arch),
  expondo a porta `8080` do llama-server.

Aponte `LLM_ENDPOINT` para ele. O código detecta o endpoint remoto, não tenta
baixar/spawnar o GGUF local e trata `404` em `/health` como "servidor vivo"
(para endpoints OpenAI-compatíveis sem rota `/health`).

## 3. UI na Vercel (opcional, recomendação)

Dois modos:

**a) Mesmo serviço** — a UI já é servida pelo backend (`web/`). Deploy único,
zero configuração.

**b) Domínio separado** (UI na Vercel como projeto estático):
1. Crie um segundo projeto Vercel com **Root Directory: `web`**.
2. Em `web/index.html`, antes de `<script src="renderer.js">`:
   ```html
   <script>window.__API_BASE__ = 'https://minha-api.vercel.app';</script>
   ```
3. Na API, defina `CORS_ORIGIN=https://minha-ui.vercel.app`
   (o middleware CORS já está no `internal/view/server.go`).

## 4. Verificação local

```bash
export SUPABASE_DB_URL='postgres://...'
export LLM_ENDPOINT='http://localhost:8080'   # opcional
go run .
# abra http://localhost:3000
```

## Limitações da Vercel para este projeto

- **SSE (`/events`) e websocket-style long polling**: na Hobby a conexão
  precisa terminar em 300 s; o monitor usa reconnect com backoff e o
  `EventSource` do navegador reconecta sozinho, mas em plano Hobby pode haver
  queda periódica de ~5 min (a UI se recupera automaticamente).
- **Processo residente**: o bridge Node (`tiktok-live-connector`) só fica
  ativo enquanto houver live conectada — sem live, a função fria sobe
  normalmente (cold start com o Postgres é o maior custo).
- **Sem GPU na Hobby**: LLM local inviável; use `LLM_ENDPOINT` remoto.
