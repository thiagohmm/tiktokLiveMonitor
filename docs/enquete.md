# Enquete ao vivo

Enquete por **palavra-alvo no chat**: o operador define de 2 a 10 palavras e
cada comentário que for **exatamente** uma delas conta um voto. Palavra mais
votada ganha.

## Regras

- **Mínimo de 2 palavras** (máximo 10). Cada opção é **uma única palavra** de
  até 40 caracteres; opções repetidas (mesmo ignorando acento/caixa) são
  rejeitadas.
- **Voto travado no primeiro comentário**: se João comenta `não` e depois
  `sim`, o voto dele continua em `não`. Comentários seguintes do mesmo usuário
  são ignorados — não existe "mudar de ideia".
- **Casamento exato do comentário**: `não`, `Não!`, `nao 🔥` e `  NAO  `
  valem; `eu voto não`, `não sei` e `naosei` **não** valem. A normalização
  (`foldText` + remoção de pontuação/emoji + espaços colapsados) compara o
  comentário inteiro com a palavra escolhida.
- **Desempate**: com empate, ganha a primeira palavra cadastrada pelo operador
  e a resposta marca `tied: true` (o painel mostra "Empate").
- Limite de **5000 votantes** por enquete; votos acima disso são ignorados.
- Só há uma enquete aberta por live: para trocar as palavras é preciso
  **encerrar** (mantém o resultado) ou **limpar** (zera tudo). No painel, o campo
  de palavra e o `+` ficam bloqueados enquanto houver enquete aberta ou
  resultado na tela; reabrir uma enquete encerrada pede confirmação, pois os
  votos anteriores são descartados.

## Persistência

O estado (palavras, votos, votantes, vencedora) vive apenas **em memória**, no
`Monitor` da live — igual ao Sorteio. Reiniciar o backend ou reconectar a live
zera a enquete; não há histórico no Postgres.

O estado é isolado por `(org, live)`: o monitor é o da organização, e o SSE só
entrega o evento para a org dona (`publishSSE` + `orgId` injetado no `emit`).

## API

Todas as rotas aceitam `?live=` (quando omitido, usa a live atual da org) e
respondem `409` quando a organização não tem live conectada.

| Rota | Método | Efeito |
| --- | --- | --- |
| `/api/enquete` | GET | Snapshot: `{active, live, question, options[], total, winner, tied}` |
| `/api/enquete/start` | POST | Abre a votação. Body `{question, words: ["sim","não"]}` |
| `/api/enquete/stop` | POST | Encerra a votação mantendo os votos (apura vencedora) |
| `/api/enquete/reset` | POST | Limpa palavras, votos e resultado |

Validações de `start` respondem `400` com mensagem PT-BR (menos de 2 palavras,
opção com espaço, opção longa, duplicada, título acima de 80 caracteres). Abrir
outra enquete com uma já ativa responde `409`.

## Eventos SSE

| Evento | Quando | Payload |
| --- | --- | --- |
| `enquete-state` | start/stop/reset | Snapshot completo |
| `enquete-vote` | cada voto contabilizado | `{uniqueId, nickname, word, index, counts[], total, votedAt}` |
| `enquete-result` | ao encerrar | Snapshot com `winner`/`tied` |

O painel usa `counts[]` como fonte de verdade e completa a lista de votantes com
o `enquete-vote` (um evento por usuário, já que o voto é travado).

## Painel

Seção **🗳️ Enquete** no painel principal (depois do Sorteio): pergunta opcional,
chips das palavras-alvo (2 a 10), "Iniciar/Encerrar enquete", "Limpar",
resultado em barras com voto/percentual, marcação da vencedora e lista
expansível de quem votou.

## Arquivos

- `backend/internal/monitor/enquete.go` — regra, estado e coleta no chat
- `backend/internal/monitor/{monitor,events,manager}.go` — campos, gancho no
  `handleChatMessage` e fan-out por live
- `backend/internal/controller/app.go` — `EnqueteState/StartEnquete/StopEnquete/ResetEnquete`
- `backend/internal/view/handlers_enquete.go` — rotas REST
- `frontend/index.html` + `frontend/renderer.js` — painel e SSE
- Testes: `backend/internal/monitor/enquete_test.go`,
  `backend/internal/view/handlers_enquete_test.go`
