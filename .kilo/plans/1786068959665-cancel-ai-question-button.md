# Plano: Botão de cancelamento na "Pergunta para IA"

## Objetivo

Adicionar um botão **Cancelar** na seção "Pergunta para IA" da UI, que aborta uma pergunta em andamento (estado "Pensando...") e interrompe de fato a geração no llama-server, sem efeitos colaterais (sem reiniciar o worker nem reprocessar a pergunta cancelada).

## Escopo (confirmado com o usuário)

Somente o **stack Go**: `web/index.html` (frontend) + `internal/ai/ai.go` + `internal/server/server.go`.
Fora de escopo: a cópia Electron/Node (`index.html` raiz, `server.js`, `ai.js`) — lá `/api/ask-ai` nem existe.

## Contexto (estado atual)

- Frontend (`web/index.html`, linhas ~325-330 e ~487-514): botão `askAiBtn` faz `fetch('/api/ask-ai')` sem `AbortController`; enquanto espera, mostra "Pensando..." e desabilita o botão. Sem forma de cancelar.
- Backend (`internal/server/server.go` → `handleAskAI`, linha 475): já chama `s.aiMgr.Complete(r.Context(), req)`. Quando o cliente aborta o fetch, o Go cancela `r.Context()`, e `worker.complete` (que usa `http.NewRequestWithContext`) interrompe a chamada HTTP ao llama-server — que por sua vez para a geração ao detectar a desconexão.
- **Problema**: em `internal/ai/ai.go`, `processQueue` trata qualquer erro de `w.complete` (incluindo `context.Canceled`) como falha do worker: marca `ready=false`, recoloca a tarefa na fila (linha ~303) e isso dispara restart completo do llama-server + re-execução da pergunta cancelada.
- **Problema latente relacionado**: `processQueue(ctx)` executa a tarefa com o ctx de quem disparou o processamento, não com o ctx da própria tarefa.
- `aiMgr.Complete` só é usado por `handleAskAI` (ctx de request HTTP) e `WarmupLearning` (ctx longo/background). O monitor Go não chama o motor de moderação — seguro usar ctx por tarefa.

## Mudanças

### 1. Frontend — `web/index.html`

- Adicionar botão `<button id="cancelAiBtn" type="button">Cancelar</button>` ao lado de `askAiBtn` (linha ~328), com `display: none` e `background-color: #555` (mesmo padrão do botão Desconectar).
- No handler de clique de `askAiBtn`:
  - Criar `const controller = new AbortController()` e passar `signal: controller.signal` no `fetch('/api/ask-ai', ...)`.
  - Ao iniciar a espera: exibir `cancelAiBtn` (`style.display = 'inline-block'`).
  - No `catch`: se `e.name === 'AbortError'`, mostrar `answerDiv.textContent = 'Cancelado pelo usuário.'`; senão manter a mensagem de erro atual.
  - No `finally`: esconder `cancelAiBtn`, reabilitar `askAiBtn` (substituir o re-enable solto no fim do try).
- Adicionar listener de clique em `cancelAiBtn` que chama `controller.abort()` (manter referência ao controller numa variável do escopo do script; `null` quando ocioso).
- Não limpar o texto da pergunta no input ao cancelar. Enter continua enviando (comportamento atual).

### 2. Backend — `internal/ai/ai.go` (fila do Manager)

- Adicionar campo `ctx context.Context` em `queuedTask`; preencher em `Complete` com o ctx recebido.
- Em `processQueue`, após retirar a tarefa da fila:
  - Se `task.ctx != nil && task.ctx.Err() != nil` → descartar a tarefa (não executa; enviar `task.ctx.Err()` em `task.errCh` de forma não-bloqueante para não vazar) e seguir para a próxima da fila (loop de skip, já que hoje só uma tarefa é retirada por chamada).
  - Executar com o ctx da tarefa: `w.complete(taskCtx, task.req)`, onde `taskCtx = task.ctx` com fallback para o ctx recebido por parâmetro se `task.ctx == nil`. (`w.complete` já aplica o timeout de 120s por cima.)
- No tratamento de erro da goroutine de `processQueue`:
  - Se `errors.Is(err, context.Canceled) || (task.ctx != nil && task.ctx.Err() != nil)` → log informativo ("tarefa cancelada"), **não** marcar `worker.ready = false`, **não** re-enfileirar. O `defer` existente já libera `busy` e chama `processQueue` para a próxima tarefa.
  - Demais erros: comportamento atual inalterado (marca unready, re-enfileira).
- Não mexer em `spawnLocal`, `scheduleRetry`, `ProbeReady`, `RegisterWorker`, `Stop`.

### 3. Backend — `internal/server/server.go` (`handleAskAI`)

- Após `s.aiMgr.Complete` retornar erro: se `errors.Is(err, context.Canceled)` (ou `ctx.Err() != nil`), apenas retornar sem escrever resposta (a conexão já foi abortada pelo cliente; evita log/ruído de write em conexão morta). Demais erros: comportamento atual (500).
- Adicionar import de `errors` se necessário.

## Testes

- Criar `internal/ai/ai_test.go` (pacote `ai` não tem testes hoje):
  - `Complete` com ctx cancelado durante a espera → retorna `context.Canceled`.
  - Tarefa cancelada enquanto está na fila é descartada sem marcar worker unready e sem re-enfileirar (assert em `m.queue` e `m.worker.ready` usando um worker fake pronto, sem llama-server real).
- Manter `TestHandleAskAI` existente em `internal/server/integration_test.go` passando (400 sem pergunta, 405 método errado).

## Validação

1. `go build ./...`
2. `go test ./...`
3. Manual: rodar `./tiktok-live-monitor`, abrir `http://localhost:3000`, conectar numa live (ou não), enviar uma pergunta e clicar **Cancelar** durante "Pensando...":
   - UI mostra "Cancelado pelo usuário.", botão Enviar reabilita, botão Cancelar some.
   - Log do servidor: sem `[AI-Queue] Tentando (re)iniciar...` após o cancelamento; pergunta cancelada não reaparece no log como reprocessada.
   - Nova pergunta em seguida funciona normalmente (worker continua pronto).

## Riscos / observações

- Cancelamento em voo depende do llama-server abortar a geração ao fechar a conexão HTTP — comportamento padrão do llama-server (slot é liberado). O cancelamento é imediato na UI mesmo que o servidor leve um instante para liberar o slot.
- Corrida benigna: se a resposta chegar no exato momento do clique em Cancelar, o `abort()` pode rejeitar o fetch já resolvido → UI mostra "Cancelado"; sem impacto no backend.
- Tarefas de warmup usam ctx longo/background → nunca são descartadas pelo skip de canceladas.
