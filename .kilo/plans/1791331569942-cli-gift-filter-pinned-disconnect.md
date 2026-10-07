# Plano — monitor-cli: filtro por presente, fixados e desconectar ao sair

Escopo: apenas `monitor-cli/` (Java 21, TUI Lanterna) + linha do `AGENTS.md`.
**Backend não muda**: os endpoints `/api/available-gifts`, `/api/pinned-comments`,
`/api/disconnect` e o evento SSE `pinned-comment` já existem e são multi-tenant
pela sessão.

## Superfície de comandos (definida com o usuário)

- `:gift` — busca o catálogo da live e mostra a **lista numerada** no feed.
- `:gift <nomes|números separados por vírgula>` — seleciona os presentes
  (ex.: `:gift 1,4` ou `:gift Rose,Dino`) e ativa a visão em que **somente os
  presentes escolhidos** aparecem quando alguém os envia.
- `:gift all` — limpa a seleção e volta a mostrar tudo (≡ `:filter all`).
- `:pinned` — ativa o acompanhamento ao vivo de fixados: a partir daí a tela
  mostra os comentários que forem fixados (categoria própria PINNED).
- `:pinned list` — imprime o **histórico dos últimos 20** fixados
  (`GET /api/pinned-comments?live=<atual>&limit=20`).
- `:filter <all|chat|gift|like|follow|system|pinned>` — filtro genérico por
  categoria, mantido como está (`:filter pinned` ≡ `:pinned`); **não** recebe
  seleção de presentes (isso é exclusivo do `:gift`).
- Aliases PT: `:presente(s)` para `:gift`; `:fixado(s)` para `:pinned`
  (`:fixados list` / `:pinned lista`).

## Decisões fechadas

1. **Filtro por presente**: seleção multipla via `:gift` (lista + seleção no
   mesmo comando). Com a seleção ativa, o feed mostra **somente** os presentes
   escolhidos. Seleção é **substitutiva** (cada `:gift <sel>` redefine o
   conjunto) e sobrevive a `:disconnect`/`:connect`; números referem-se sempre
   à última lista `:gift` exibida.
2. **Fixados**: `:pinned` (ao vivo, filtro PINNED) + `:pinned list` (últimos
   20, linhas INFO — visíveis sob qualquer filtro). O evento SSE
   `pinned-comment` deixa de ser decodificado como chat comum e vira a
   categoria `PINNED` (cor própria).
3. **Sair desconecta**: em `:quit`, Ctrl+C e shutdown hook, chamar
   `POST /api/disconnect` (monitor corrente, username vazio) **best-effort**:
   timeout curto (~3s), **sem** prompt de senha/`recoverAfter401()`, erros
   engolidos. Sempre desconecta, mesmo que a live tenha sido conectada por
   outro cliente (o backend retorna `{"success":true}` como no-op quando não
   há monitor — verificado em `handleDisconnect`).

## Fatos verificados no código

- `GET /api/available-gifts?live=<user>` → JSON `[]string` (nomes originais em
  inglês). `live` vazio ou live não monitorada → `[]` (handler engole o erro).
  Logo após `:connect` o catálogo pode vir vazio (backend busca assíncrono e
  cacheia ao receber `gifts-list`) → CLI deve orientar "tente de novo".
- `GET /api/pinned-comments?live=<user>&limit=20` → JSON
  `[{id, liveName, uniqueId, nickname, comment, pinId?, isFollower?, timestamp}]`.
  Limite aceito: 1–200. `live` vazio → `ResolveLive` usa a live corrente da org;
  se mesmo assim vazio, o SQL retorna os fixados de **todas** as lives da org.
- SSE: `server.go:206` publica **todo** evento do monitor via `publishSSE`
  (sem allowlist) → `pinned-comment` já chega ao CLI. Payload:
  `{uniqueId, nickname, comment, pinId, timestamp, isFollower}` — hoje cai no
  `case "new-chat-message", "pinned-comment"` do `EventDecoder`.
- Nomes de presente: catálogo (`gifts-list`) e `giftName` dos eventos saem do
  mesmo mapa `availableGiftsById` do `bridge.js`; o match exato
  case-insensitive funciona. Risco residual de divergência de nome → o filtro
  também aceita o nome digitado como aparece no feed.
- `LiveHttpApi.request` fixa timeout de 30s (longo demais para shutdown hook).
- `ApiHttp` só tem `parseObject` (Map); precisa de `parseArray` tolerante para
  as respostas em array (`available-gifts`, `pinned-comments`).
- **Bug de UX existente**: `TerminalUi.applyFilter` adiciona a confirmação
  "filtro: X" como INFO **depois** de `setFilter` — a própria confirmação (e
  qualquer erro SYSTEM) fica invisível sob o filtro ativo.

## Tarefas (ordem de implementação)

### 1. Infra do filtro por presente

- `ui/FeedLine.java`: novo componente `giftName` (`String`, `""` quando n/a).
  Manter construtor de compatibilidade `FeedLine(text, category)` delegando com
  `""` para não quebrar os call sites existentes.
- `ui/FeedBuffer.java`: trocar `Predicate<Category>` por `Predicate<FeedLine>`
  em `filter`, `setFilter`, `add`, `rebuildView`. **Mudança deliberada**:
  `setFilter` passa a envolver o predicado recebido admitindo sempre `INFO` e
  `SYSTEM` — corrige o bug de a confirmação "filtro: X" e os erros de API
  ficarem invisíveis sob filtro (ponto único, não em cada call site).
- `ui/Presenter.java`: o `case GiftEvent` passa a preencher `giftName` no
  `FeedLine` (valor original, sem normalizar).
- `infrastructure/api/ApiHttp.java`: novo `parseArray(String raw)` tolerante
  (Jackson `List<Object>`; branco/malformado/não-array → `List.of()`), no
  mesmo espírito do `parseObject`.
- `domain/port/LiveApiPort.java` + `infrastructure/api/LiveHttpApi.java`:
  `List<String> availableGifts(UserSession, String live)` →
  `GET /api/available-gifts?live=<urlencoded>`; parse via `parseArray`;
  deduplicar preservando a ordem (o cache do backend pode repetir nomes).

### 2. Comando `:gift`

- `ui/CommandParser.java`: novo record `Gift(String argument)` no selo
  `Command`; `dispatch`: `"gift" | "gifts" | "presente" | "presentes"` →
  `Gift(argument)`. Atualizar `helpText()`.
- `ui/TerminalUi.java`:
  - Estado: `List<String> giftCatalog` (última lista exibida) e
    `Set<String> selectedGifts` (lowercase).
  - `Gift("")` → `executor` + `runSafely`: `availableGifts` com
    `state.username()` como `live` (vazio se desconectado); guarda em
    `giftCatalog`; imprime lista numerada (INFO); lista vazia →
    "catálogo indisponível — conecte a uma live e tente de novo em alguns
    segundos". **Não** buscar automaticamente após `:connect` (catálogo do
    backend é assíncrono e viria vazio; opção considerada e rejeitada).
  - `Gift("all")` → limpa `selectedGifts` + aplica filtro all.
  - `Gift("<seleção>")` → resolve tokens (split em vírgula, trim; sem rede):
    token numérico = índice 1-based do `giftCatalog` (catálogo vazio →
    "rode :gift primeiro"; índice fora da faixa → linha de erro, não aplica);
    demais = nome literal. Ativa o predicado
    `l -> l.category() == GIFT && selected.contains(l.giftName().toLowerCase())`
    e confirma via INFO com os nomes escolhidos.
  - Extrair a resolução de tokens para um helper estático testável (ex.:
    `GiftSelection.resolve(String, List<String>) -> Set<String>` em `ui/`),
    pois `TerminalUi` depende de Lanterna e não é instanciável em teste.

### 3. Comentários fixados (`:pinned` / `:pinned list`)

- `domain/model/PinnedCommentEvent.java` (novo):
  `record PinnedCommentEvent(Instant at, String uniqueId, String nickname,
  String comment) implements DomainEvent`; adicionar ao `permits` de
  `DomainEvent`.
- `domain/model/PinnedCommentEntry.java` (novo):
  `record PinnedCommentEntry(String liveName, String uniqueId, String nickname,
  String comment, String timestamp)` — item do histórico REST (timestamp vem
  como string ISO; exibir como vem ou best-effort `HH:mm`, sem parsing
  obrigatório). `liveName` é exibido como prefixo `@live` **somente** quando a
  consulta foi feita sem live (desconectado → resultado org-wide).
- `domain/service/EventDecoder.java`: remover `"pinned-comment"` do case de
  chat; novo case → `PinnedCommentEvent`.
- `ui/Category.java`: + `PINNED`; aliases em `parseFilter`: `"pinned"`,
  `"pin"`, `"fixado"`, `"fixados"`.
- `ui/Presenter.java`: `case PinnedCommentEvent` →
  `stamp + "[fixado] @" + displayName + ": " + comment` com `Category.PINNED`.
- `ui/TerminalUi.java`: `colorOf(PINNED)` → `TextColor.ANSI.BLUE_BRIGHT`;
  `categoryOf(PinnedCommentEvent)` → `PINNED`; rodapé ganha contador
  `fixados %d`.
- `LiveApiPort`/`LiveHttpApi`:
  `List<PinnedCommentEntry> pinnedComments(UserSession, String live, int limit)`
  → `GET /api/pinned-comments?live=<...>&limit=20`.
- `ui/CommandParser.java`: novo record `Pinned(String argument)` no selo
  `Command`; `dispatch`: `"pinned" | "pin" | "fixado" | "fixados"` →
  `Pinned(argument)`.
- `ui/TerminalUi.java`:
  - `Pinned("")` → aplica o filtro PINNED (acompanhamento ao vivo).
  - `Pinned("list" | "lista")` → `executor` + `runSafely`: `pinnedComments`
    com `state.username()` (pode ir vazio → backend resolve a live corrente da
    org ou devolve org-wide) e `limit=20`; imprime cabeçalho + cada item como
    linha INFO (`[fixado] @nick: comentário`, com prefixo `@live` quando a
    consulta foi sem live); lista vazia → "nenhum comentário fixado".
  - Outro argumento → `Unknown` com uso.

### 4. Desconectar ao sair

- `LiveHttpApi`: extrair timeout por requisição (parâmetro `Duration`,
  default 30s); `LiveApiPort` ganha
  `void disconnect(UserSession, String username, Duration timeout)`
  (a assinatura antiga delega com 30s).
- `application/DisconnectLiveUseCase.java`: novo `executeBestEffort()` —
  `sessions.send(s -> { api.disconnect(s, "", Duration.ofSeconds(3)); ... })`,
  **sem** `recoverAfter401()` (sem prompt de senha durante encerramento),
  **sem** `hub.notice`/`hub.refreshState` (UI já está fechando), engolindo
  `ApiException`/`RuntimeException` silenciosamente. Executa na **thread
  chamadora** (não no `executor`, que pode já estar parado).
- `Main.java`: extrair sequência de encerramento idempotente
  (`AtomicBoolean`):
  `disconnect.executeBestEffort()` → `watch.stop()` → `ui.close()` →
  `executor.shutdown()`; usar a mesma sequência no shutdown hook e no
  `finally` após `ui.run()` (hoje os dois duplicam stop/close/shutdown sem
  desconectar). Ordem deliberada: o disconnect REST independe do SSE; fazer
  primeiro garante que a chamada saia mesmo se `ui.close()` travar. Custo
  máximo adicionado ao encerramento: ~3s (timeout do disconnect).
- Atualizar `Main.USAGE` (novos comandos + nota de que sair desconecta a
  live).

### 5. Testes

- `EventDecoderTest`: `pinned-comment` decodifica para `PinnedCommentEvent`.
- `PresenterTest`: linha de `PinnedCommentEvent` (`[fixado]`, categoria
  PINNED); `FeedLine` de `GiftEvent` carrega `giftName`.
- `CommandParserTest`: `:gift`/`:presentes` → `Gift("")`;
  `:gift 1,rosa` → `Gift("1,rosa")`; `:gift all` → `Gift("all")`;
  `:pinned` → `Pinned("")`; `:pinned list` / `:fixados lista` →
  `Pinned("list")`; `:filter pinned` → `Filter(PINNED)`; `:filter gift` segue
  → `Filter(GIFT)`.
- `FeedBufferTest`: ajustar predicados existentes para `Predicate<FeedLine>`;
  novo teste de filtro por `giftName`; teste de que INFO/SYSTEM passam por
  qualquer filtro.
- Novo teste do helper de seleção (números válidos/fora da faixa, catálogo
  vazio, nomes, misto, case-insensitive).
- `LiveHttpApiTest` (MockWebServer, padrão já existente): `availableGifts`
  (query `live` + parse), `pinnedComments` (query `limit=20` + parse),
  array malformado → lista vazia.

### 6. Docs

- `AGENTS.md`: atualizar a linha do `monitor-cli` (comandos `:gift`,
  `:pinned [list]`, seleção múltipla de presentes e desconexão automática da
  live ao sair).

## Fora de escopo

- Tradução PT-BR dos nomes de presentes no CLI (o dicionário
  `GIFT_TRANSLATIONS` é do frontend; o feed do CLI exibe o nome original, então
  o casamento é consistente). Matching: exato, case-insensitive.
- Edição/remoção individual da seleção (`:gift <sel>` redefine a seleção
  inteira a cada uso; `:gift all` limpa).

## Riscos / casos de borda

- Catálogo vazio logo após `:connect` (busca assíncrona no backend) →
  mensagem orientando tentar de novo; números de uma lista antiga continuam
  válidos até o próximo `:gift`.
- Sessão expirada na saída → `executeBestEffort` falha em silêncio e o
  monitor segue ativo no servidor (limitação aceita; sem prompt de senha
  durante encerramento).
- `kill -9`/queda de rede → sem desconexão (impossível garantir).
- Nomes com vírgula quebrariam o split — nomes de presentes TikTok não
  contêm vírgula na prática.
- Evento de presente sem nome (decoder cai no fallback `giftId`) não casa com
  a seleção por nome — comportamento residual aceito (o feed já exibe o
  fallback hoje).

## Validação

1. `mvn -f monitor-cli/pom.xml test` (todos verdes).
2. `mvn -f monitor-cli/pom.xml package`.
3. Manual contra produção: login → `:connect <user>` → `:gift` (lista
   numerada) → `:gift 1,2` (só os escolhidos aparecem quando enviados) →
   `:gift all` → `:pinned list` (últimos 20) → `:pinned` e fixar um
   comentário na live para vê-lo chegar em azul → `:quit` e confirmar no
   servidor que o monitor parou (`GET /api/state` → `connected:false`).
