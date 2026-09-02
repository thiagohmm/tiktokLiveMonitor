# Ponte Node — `bridge.js`, `gifts.js`, `follower.js`

> Diretório: `backend/internal/monitor/`

O monitor Go gerencia um **processo filho Node** que usa o pacote
`tiktok-live-connector` para se conectar ao Webcast do TikTok. A comunicação é
feita por **JSON Lines**:

- **Go → Node (stdin):** comandos `{ "action": "connect" | "disconnect" |
  "fetch-gifts" | "get-state", ... }`.
- **Node → Go (stdout):** eventos `{ "type": "...", "data": {...} }\n`.
- **stderr:** logs livres, capturados pelo Go.

O processo vive o quanto durar a conexão; o Go o reinicia sob demanda
(supervisor de reconexão).

---

## `bridge.js` — Processo principal da ponte

### Carregamento do conector

| Função | Descrição |
|---|---|
| `loadConnectionClass()` | Tenta carregar a classe de conexão em ordem: `TikTokLiveConnection` (v2), `WebcastPushConnection` de `tiktok-live-connector/legacy` e o fallback. Junta mensagens de erro de todas as tentativas. |
| Variáveis de topo | `ConnectionClass` (classe escolhida), `connectionClassError`, `connection` (instância ativa), `currentUsername`, `chatBuffer`, `processedPinnedMessages` (Set, máx. 200), `availableGiftsById` (Map id→nome). |

### Helpers de erro / I/O

| Função | Descrição |
|---|---|
| `errorMessage(err)` | Extrai a mensagem de erro (lida com `err.exception.message`). |
| `shouldIgnoreBridgeError(message)` | Erros conhecidos e inofensivos que não devem ser repassados ao Go: `"reading 'map'"`, `eulerstream.com`, `Business plan`, `fetchWebcastSignatureFromEulerRoute`. |
| `send(type, data)` | Serializa `{type, data}` + `\n` no stdout. Protege contra EPIPE/stream destruído (`stdoutBroken`). |
| listeners de stdin | `end`/`close` do stdin → `process.exit(0)` (o Go morreu/encerrou). |

### Extração de usuário e conteúdo

| Função | Descrição |
|---|---|
| `getUser(data)` | Extrai usuário de várias posições do payload (`data.user`, `member`, `sender`, `author`, `owner`) e campos `uniqueId`/`userId`/`displayId`/`id`. O **id numérico** é o último recurso (usuários anônimos). Resolve seguidor via `resolveIsFollower`. |
| `chatContent(data)` | Texto do chat em `content` (v2) ou `comment` (legado). |
| `asBool(value, fallback)` | Coerção booleana (números/strings). |
| `textFromDisplayText(displayText)` | Extrai texto de `displayText.defaultPattern`. |

### Comentários fixados (room pin)

| Função | Descrição |
|---|---|
| `getPinnedContent(data)` | Procura o texto do pin em dezenas de candidatos (`chatMessage`, `pinMessage`, `pinnedMessage`, `socialMessage`, `giftMessage`, `memberMessage`, `likeMessage`, campos soltos e `displayText`). |
| `getPinnedUser(data, content)` | Descobre o autor do pin (varre fontes; se nada achar, tenta `@mention` no texto; depois tenta casar com o `chatBuffer`). |
| `getPinnedMessageKey(data)` | Chave de deduplicação (`pinId`, `msgId`, etc.). |
| `handlePinnedMessage(data)` | Ignora `unpin`/`action===2`. Deduplica pela chave. Envia `pinned-comment` e `mark-user-red` (usuário). |

### Comandos (stdin)

| Função | Descrição |
|---|---|
| `handleCommand(cmd)` | Dispatch por `cmd.action`: `connect` → `doConnect(username)`, `disconnect` → `doDisconnect()`, `get-state` → `state`, `fetch-gifts` → `handleFetchGifts()`. |
| `doDisconnect()` | `connection.disconnect()` + limpa estado. |

### Catálogo de presentes

| Função | Descrição |
|---|---|
| `looksLikeGift(item)` | Heurística para reconhecer um objeto de presente (`giftName`, `describe`, `diamond_count`, `image`/`icon` + nome...). |
| `extractGiftArray(raw)` | Varre recursivamente (profundidade ≤ 4) arrays/objetos do payload de gifts (aceita `gifts`, `pages`, `data`) e devolve itens que parecem presentes. |
| `giftDisplayName(gift)` | Nome para exibição, traduzido via `translateGiftName` (de `gifts.js`). |
| `currentGiftNames()` | Lista única de nomes do catálogo. |
| `rememberAvailableGifts(raw)` | Popula `availableGiftsById` (id numérico ou nome como chave) e devolve os nomes. |
| `fetchGiftCatalogUnsigned()` | Busca o catálogo via `getJsonObjectFromWebcastApi('gift/list/', ..., false)` (sem assinatura Euler) com fallback para `connection.roomInfo`. |
| `publishAvailableGifts()` | Publica o catálogo: tenta `connection.availableGifts`, senão o fetch; envia `gifts-list`. Guarda contra reentrância (`giftsPublishInFlight`). |
| `handleFetchGifts()` | Responde `gifts-list` (vazio se desconectado). |

### Resolução de nome/tipo de presente

| Função | Descrição |
|---|---|
| `firstNonEmptyString(...values)` | Primeiro valor string não vazio (ou objeto com `giftName`/`name`/`describe`/`defaultPattern`). |
| `resolveGiftName(data)` | Nome do presente: payload direto/aninhado → tradução; senão consulta `availableGiftsById` por `giftId`; senão `"Presente <id>"` / `"Presente"`. |
| `resolveGiftType(data)` | `giftType` (int), de `giftDetails`/`gift`/direto. |

### Conexão (`doConnect`)

Passos:
1. Se `!ConnectionClass`, envia `connection-status` com `success:false`.
2. Se já conectado, desconecta.
3. Reseta buffers (`chatBuffer`, pins, gifts).
4. Cria `new ConnectionClass(username, { processInitialData:false, fetchRoomInfoOnConnect:true, enableExtendedGiftInfo:false })`.
5. Registra listeners (abaixo) e chama `await connection.connect()`.

Listeners de eventos do conector:

| Evento | Ação |
|---|---|
| `connected` | Envia `connection-status` `success:true` e publica gifts. |
| `disconnected` | `connection-status` `success:false`; `connection = null`. |
| `chat` | Envia `new-chat-message` (usuário + conteúdo). Também alimenta `chatBuffer` (até 500) para resolução de pins. |
| `gift` | Monta payload (`resolveGiftName/Type`, `repeatCount`, `repeatEnd`); envia `any-gift-received`. Registra presente desconhecido no catálogo (emite `gifts-list`). Se `repeatEnd`, envia também `new-gift-user`. |
| `like` | Envia `new-like-event` com `likeCount` (rajada do evento) e `total` (**acumulado oficial da sala**; o v3 expõe `count`/`total`, não `likeCount`/`totalLikeCount`). |
| `member` | `live-user-connected`. |
| `follow` | `new-follower` (`isFollower:true`). |
| `share` | `new-social-event`. |
| `giftPanelUpdate` | Atualiza catálogo e emite `gifts-list`. |
| `roomPin` | `handlePinnedMessage`. |
| `decodedData` | Se `WebcastRoomPinMessage`, trata pin. |
| `error` | Envia `error` (a menos que seja ignorável). |

### Global error handling

`uncaughtException` e `unhandledRejection` filtram erros ignoráveis e enviam o
restante ao Go via `send('error', ...)`.

---

## `gifts.js` — Tradução de nomes de presentes (PT-BR)

| Item | Descrição |
|---|---|
| `GIFT_TRANSLATIONS` | Mapa EN → PT-BR (~190 presentes). Ex.: `"rose": "Rosa"`, `"lion": "Leão"`, `"drama queen": "Rainha do Drama"`. |
| `translateGiftName(name)` | Trim + lower e busca no mapa; sem match devolve o nome original. |

> O mesmo dicionário existe no Go em `internal/controller/gifts.go`
> (`giftTranslations`) — é espelhado dos dois lados (o Node traduz na origem e
> o Go re-traduz para consultas/metas). Ao alterar um, manter o outro em
> sincronia.

---

## `follower.js` — Resolução de "é seguidor?"

O payload do TikTok não traz um booleano simples: seguidor/amigo mutuo/role
vêm em campos diferentes (e `false` pode ser só o default do protobuf).

| Função | Descrição |
|---|---|
| `toFollowFlag(value)` | Interpreta `true/1/2`/`"true"`/`"1"`/`"2"` como seguidor (2 = amigos/recíproco), `false/0/"false"/"0"` como não. |
| `identityFollowerFlag(identity)` | Lê `identity.isFollowerOfAnchor`/`isMutualFollowingWithAnchor`. |
| `resolveIsFollower(...sources)` | Regra: `true` explícito em qualquer fonte vence; caso contrário, se houve **algum** `false` explícito → `false`; senão `null` (desconhecido). Assim um `isFollower=false` "default de protobuf" não vira "não segue". |

---

## Diagramas relacionados
- `diagrams/00-arquitetura.puml` — posição da ponte no sistema.
- `diagrams/01-fluxo-eventos.puml` — caminho TikTok → bridge → monitor.
