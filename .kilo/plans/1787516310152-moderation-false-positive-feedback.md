# Ensinar a IA a não alertar falsos positivos (proselitismo/spam/outros)

## Objetivo

Permitir que o usuário marque, na interface, um alerta de moderação como falso positivo ("não é X"). Isso deve (1) ensinar a IA via *few-shot* a generalizar para mensagens parecidas e (2) bloquear na hora mensagens idênticas já corrigidas, sem depender do LLM.

## Estado atual

- Backend já tem a base do feedback:
  - Tabela `false_positives` em `feedback.db` (`comment`, `category`, `expected`).
  - `DB.AddFeedback` (`internal/database/database.go:166`) valida e grava.
  - `buildPromptContext` (`internal/moderation/prompt.go:35`) injeta até 12 feedbacks como exemplos *few-shot* no prompt do LLM.
  - `GET/POST /api/feedback` (`internal/view/server.go:359`) grava e depois `ClearModerationCache()` + `WarmupModeration(force=true)`.
- **O que falta:**
  - Nenhum controle na UI para enviar feedback (`web/renderer.js` nunca chama `/api/feedback`).
  - Nenhum bloqueio determinístico: se o LLM estiver em cooldown ou errar de novo, a mesma mensagem continua sendo alertada.
  - Feedback pode ser duplicado indefinidamente (sem dedup).

## Decisões

- Abordagem escolhida: **botão na UI + aprendizado (few-shot) + bloqueio determinístico**.
- O "bloqueio" cobre **mensagens idênticas** (texto normalizado); a generalização para "um tipo de mensagem" fica por conta do *few-shot* do LLM.
- O botão só aparece para categorias de IA válidas: `PROSELITISMO`, `SPAM`, `GOLPE`, `ODIO`, `OUTRO`. **Não** para `REPETICAO`/`CORRELACAO` (vêm de outro mecanismo e são rejeitadas por `AddFeedback`).

## Tarefas

### 1. Interface do repositório — `internal/model/repository.go`
- Adicionar método à interface `FeedbackRepository`:
  ```go
  GetFalsePositiveComments(limit int) ([]string, error)
  ```
  Retorna comentários distintos com `expected = 'NAO'` (texto bruto; a normalização fica no Engine).

### 2. Persistência — `internal/database/database.go`
- Implementar `GetFalsePositiveComments(limit int)`:
  ```sql
  SELECT DISTINCT comment FROM false_positives
  WHERE expected = 'NAO' ORDER BY timestamp DESC LIMIT ?
  ```
  (clamp de `limit` entre 1 e 500, como nos demais métodos).
- Adicionar dedup em `AddFeedback`: antes do `INSERT`, checar se já existe linha com mesmo `comment` e `expected`; se existir, retornar idempotente (0, nil) sem inserir.

### 3. Motor de moderação — `internal/moderation/moderation.go`
- Adicionar campo `allowlist map[string]struct{}` em `Engine` e inicializar vazio em `NewEngine`.
- Adicionar método `refreshAllowlist()` que lê `e.repo.GetFalsePositiveComments(500)`, normaliza cada comentário com `foldText` e reconstrói o mapa (protegido por `e.mu`).
- Chamar `refreshAllowlist()` dentro de `WarmupLearning` (após montar o prompt, antes de marcar `Ready`) para que o allowlist seja carregado no connect e recarregado após cada feedback (que chama `WarmupModeration(force=true)`).
- Em `AnalyzeMessage`, logo após calcular `folded`, checar `e.allowlist` e, se presente, retornar `AnalysisResult{Flagged: false, Category: "OK"}` imediatamente (antes do cooldown/cache/chamada ao LLM). Logar algo como `[AI] ✅ liberado por allowlist`.

### 4. (Opcional, pequeno) Few-shot — `internal/moderation/prompt.go`
- Deduplicar exemplos por `(comment, expected)` no loop e aumentar o limite efetivo (ex.: de 12 para 24) mantendo o mesmo formato de saída. Não muda a assinatura de `buildPromptContext`.

### 5. Frontend — `web/renderer.js`
- Adicionar helper `sendFalsePositiveFeedback(comment, category)` que faz `POST /api/feedback` com `{comment, category, expected: "NAO"}`.
- Em `addFlaggedMessageToList` (linha ~1465), quando `category ∈ {PROSELITISMO, SPAM, GOLPE, ODIO, OUTRO}`, adicionar um botão "Não é {rótulo}" dentro da célula de Detalhe (`tdReason`). Ao clicar:
  1. chamar `sendFalsePositiveFeedback`;
  2. remover a linha (`tr.remove()`) e limpar seu `flaggedMessageTimers[timerKey]`;
  3. feedback visual (botão muda para "Corrigido" ou toast via `setStatus`).
- Não adicionar coluna nova na tabela (evita desalinhar as linhas de correlação que também usam `correlationMessagesTableBody`).

### 6. Estilo — `web/index.html`
- Adicionar uma classe CSS discreta para o botão de correção (reaproveitar o visual de `.small-btn` já existente), sem novas colunas de tabela.

## Validação

- `go test ./...` — garante que `AddFeedback` (com dedup), `GetFalsePositiveComments` e o fluxo de moderação seguem passando.
- Testes manuais no navegador:
  1. Conectar em uma live e provocar/aguardar um alerta de `PROSELITISMO` ou `SPAM`.
  2. Clicar "Não é ..." e confirmar que a linha some e o botão dá feedback.
  3. Reenviar a mesma mensagem: confirmar que **não** gera novo alerta (allowlist).
  4. Enviar uma variação leve da mensagem: confirmar que a IA tende a não marcar (few-shot).
  5. Verificar `feedback.db` (tabela `false_positives`) sem duplicatas.

## Fora de escopo

- Correção de falso positivo de `REPETICAO` (mecanismo de repetição, não categoria de IA).
- Tela de histórico de moderação com botão de correção (pode ser follow-up usando `/api/history`, que já existe mas não é usado pela UI).
- Rebalanceamento/pesagem sofisticada dos exemplos *few-shot* (só dedup + limite maior neste plano).

## Riscos

- Allowlist normalizado por `foldText` pode bloquear variações legítimas com o mesmo texto normalizado (ex.: só acentos/Ç). Aceitável e reversível (remover a linha em `feedback.db`).
- Aumentar o limite do *few-shot* pode alongar o prompt e custar mais tokens no LLM local. Manter em ~24 exemplos.
