# Entrypoint e ferramentas

> `backend/main.go` e `backend/cmd/sseload/main.go`

---

## `main.go` — Ponto de entrada do backend

Wiring (composição) de todas as camadas, na ordem:

```text
repo (PostgreSQL/Supabase)
   └─> monitor (bridge Node)
          └─> MessageCache (write-behind) → repo
                 └─> AppController (monitor + repo + cache + report + ranking)
                        └─> HTTPServer (view) — SSE + REST
```

### `main()`

| Etapa | O que faz |
|---|---|
| Log | Prefixo `[tiktok-live-monitor]` + flags de hora. |
| Banco | `database.OpenFromEnv()` (exige `DATABASE_URL`); `defer repo.Close()`. |
| Monitor | `monitor.New()` (bridge Node + estado em memória). |
| Cache de mensagens | `database.NewMessageCache(repo)` → `Start()`; `defer msgCache.Stop()` **registrado depois de `repo.Close`**, então o flush final roda antes de o banco fechar. |
| Controller | `controller.NewAppController(mon, repo)` + `SetMessageCache(msgCache)`. Restaura settings persistidas. |
| View | `view.New(view.Config{Host, Port}, ctrl)`; porta de `PORT` (padrão **3001**), host de `HOST` (padrão `0.0.0.0`). |
| Start | `srv.Start(ctx)` — bloqueia até o servidor encerrar (graceful). |

---

## `cmd/sseload/main.go` — Load test do fan-out SSE

Ferramenta de teste de carga do broadcast SSE, rodando como **processo
separado** do cliente de teste (macOS limita ~10240 arquivos abertos por
processo; 10k conexões loopback usariam 20k FDs num único processo).

### `fakeRepo`
Satisfaz `model.Repository` por **embedding de interface nula** +
implementação só dos métodos tocados pelo construtor do controller
(`GetSetting`/`SetSetting`). Qualquer outra chamada panica (esperado).

### `main()`

Flags:
- `-port` (padrão 19858), `-rate` (eventos/s, padrão 2000), `-duration`
  (padrão 60s).

Fluxo:
1. Cria `monitor.New()` e define live `"loadtest"`;
2. Cria controller com `fakeRepo` e o servidor `view`;
3. Sobe o servidor em goroutine;
4. Faz polling em `/api/readiness` até subir (timeout 10s) → imprime `READY`;
5. Dispara uma **tempestade** de eventos: ticker em `mon.Emit("load-test", ...)`
   na taxa pedida durante a duração (a View broadcasta a todos os SSE clients);
   ao fim imprime `STORM_DONE`;
6. `select {}` mantém o processo vivo (o cliente de teste o encerra com kill;
   o view trata SIGINT/SIGTERM com graceful shutdown).

> Uso: `go run ./cmd/sseload -port 19858 -rate 2000 -duration 60s`
