# Plano — monitor-cli: comentários fixados não aparecem

**Branch:** `feature/java-terminal-monitor` · Escopo: apenas `monitor-cli/` (TUI Java 21 + Lanterna).
Backend Go / bridge Node / VPS **não mudam**.

## Sintoma (confirmado com o usuário)

Live conectada, chat/presentes/curtidas chegam normalmente, mas ao fixar um comentário
**nada** aparece: nem linha azul no feed, nem na janela `:pinned`, nem o contador `fixados`.
Sem alerta de erro e sem placeholder.

## Evidência coletada (verificada)

- Caminho do CLI até a UI está correto: `TikTokLiveEngine.onRoomPin` → `store.recordPinned`
  → `publish` → `TerminalUi.onDomainEvent` (`feed.add` + `desk.publish`); `WindowDesk.publish`
  oferece cada linha a todas as janelas, e a janela PINNED filtra por `Category.PINNED`.
- `SqliteStore.recordPinned` retorna `true` no primeiro pin (índice único parcial só deduplica
  replay) — não descarta pin novo.
- Lib `com.github.jwdeveloper.TikTok-Live-Java:Client:1.10.0-Release` = commit JitPack
  `d877d38db627439baac32dcae15c396cfe47c6a1`. Nesse commit:
  - `MessagesMapperFactory` **registra** `WebcastRoomPinMessage.class` → `commonHandler::handlePinMessage`.
  - `TikTokCommonEventHandler.handlePinMessage` faz **apenas**
    `pinMessage.getChatMessage()` → `new TikTokCommentEvent(chatMessage)` → `TikTokRoomPinEvent`.
  - `webcast.proto` de `WebcastRoomPinMessage` tem
    `oneof { chat_message=2, social_message=3, gift_message=4, member_message=5, like_message=6 }`,
    `method=30`, `pin_time=31`, `operator=32`, `action=33`, `display_duration=34`, `pin_id=35`.
  - `TikTokLiveMessageHandler`: se `mapper.isRegistered(method)` é falso, publica
    `TikTokWebsocketUnhandledMessageEvent` (o CLI **não** assina hoje → silêncio total).
    Se o mapeamento lança, publica `TikTokErrorEvent` (o CLI assina e alertaria — não ocorreu).
- `TikTokWebsocketMessageEvent` carrega o payload cru: `getMessage().getMethod()` e
  `getMessage().getPayload()` (`ByteString`).
- O bridge Node de produção (`backend/internal/monitor/bridge.js:113`) não confia em um único
  campo: varre `chatMessage`, `socialMessage`, `giftMessage`, `memberMessage`, `likeMessage` etc.
  e ignora `method === 'unpin'` / `action === 2`.

**Conclusão:** a libzinha só entrega o pin de chat e o CLI só escuta o evento tipado; qualquer
pin que chegue por outro `oneof`, por nome de método não mapeado, ou sem `chat_message` é
descartado em silêncio. A correção é capturar o pin pelo **payload cru** do `WebcastRoomPinMessage`,
espelhando o bridge Node, e diagnosticar o caso de "não chega nada".

## Diagnóstico que o código novo já embute

Ao assinar `TikTokWebsocketUnhandledMessageEvent` e logar o `method`, o primeiro teste contra
uma live distingue os dois cenários restantes:

- Se aparecer alerta `pin não mapeado: WebcastRoomPinMessage` → hipótese (b): nome de método/registro.
- Se não aparecer nada **nem** alerta de unhandled e **nem** pin na tela → hipótese (a): o TikTok
  não está entregando o pin (provável limite de assinatura/sessão; a lib documenta que "nem toda
  mensagem chega" sem `sessionId`). Aí liga a Tarefa 4.

## Tarefas (ordem de implementação)

### 1. Captura de pin pelo payload cru (núcleo)

- `infrastructure/tiktok/TikTokLiveEngine.java`:
  - Em `buildClient`, assinar:
    - `builder.onEvent(TikTokWebsocketMessageEvent.class, (c, e) -> onWebsocketMessage(e));`
    - `builder.onEvent(TikTokWebsocketUnhandledMessageEvent.class, (c, e) -> onUnhandled(e));`
  - **Remover** a assinatura de `TikTokRoomPinEvent` (evita dupla contagem; a extração passa a ser
    única, pelo payload cru).
  - `onWebsocketMessage(e)`: se `e.getMessage().getMethod()` for `"WebcastRoomPinMessage"`,
    chamar `handlePin(e.getMessage().getPayload().toByteArray())`.
  - `onUnhandled(e)`: se o `method` contiver "pin" (case-insensitive),
    `alert("pin não mapeado: " + method)` — diagnóstico permanente e barato.
  - `handlePin(byte[] payload)`:
    - `WebcastRoomPinMessage msg = WebcastRoomPinMessage.parseFrom(payload);` (try/catch →
      `alert("pin: " + e.getMessage())`).
    - Ignorar unpin: `msg.getAction() == 2` **ou** `"unpin".equalsIgnoreCase(msg.getMethod())`.
    - Chave de dedupe (substitui `pinKey`): `pin_id` (`msg.getPinId()`) → `common.msgId`
      (`msg.getCommon().getMsgId()`) → `pinTime + ":" + texto.hashCode()`.
    - `rememberPin(key)` como hoje (Set com teto `PIN_KEYS_MAX = 200`).
    - `handlePin` continua fazendo `store.recordPinned(...)` + `publish(...)` como o antigo
      `onRoomPin` (mesma semântica: só publica se `fresh`).
- `infrastructure/tiktok/TikTokEventMapper.java`:
  - Novo `PinnedCommentEvent pinned(WebcastRoomPinMessage msg, Instant at)` que escolhe o `oneof`
    setado (`hasChatMessage()`/`hasSocialMessage()`/… na ordem do `webcast.proto`) e monta
    `uniqueId`/`nickname` a partir do `User` do campo setado (`uniqueId(User)`/`nickname(User)` já
    existem; todos os cinco tipos expõem `getUser()`).
  - Texto por tipo: chat → `getContent()`; gift → `getGift().getName()`; member →
    `getActionDescription()`; social → `"compartilhou/seguiu a live"`; like → `"curtiu a live"`.
    Vazio → placeholder `"[sem texto identificado]"` (mantém o comportamento atual).
  - Manter a sobrecarga existente `pinned(TikTokCommentEvent, Instant)` (usada nos testes) ou
    realocá-la sobre a nova.
  - Timestamp: `msg.getPinTime() > 0 ? Instant.ofEpochMilli(getPinTime()) : Instant.now()`.

### 2. (Opcional, só se o diagnóstico indicar) suporte a `sessionId`

- `configure(...)`: se `TLM_TIKTOK_SESSION_ID` estiver definido e não vazio, chamar
  `settings.setSessionId(...)` (a lib documenta que sem sessão algumas mensagens não chegam).
  Não é obrigatório; entra apenas se a Tarefa 1 mostrar que o pin não é entregue.

### 3. Testes

- Novo `infrastructure/tiktok/PinExtractorTest` (JUnit 5): construir `WebcastRoomPinMessage` via
  builder da própria lib e cobrir:
  - pin de chat com texto → `uniqueId`/`nickname`/`comment` corretos;
  - pin de chat sem texto → placeholder;
  - pin cujo `oneof` é `social_message`/`gift_message`/`member_message`/`like_message` → usa o
    usuário e o texto do tipo certo;
  - `action == 2` / `method == "unpin"` → ignorado;
  - chave de dedupe: dois pins com mesmo `pin_id` → um evento; `pin_id` vazio não deduplica.
- `TikTokEventMapperTest`: manter os dois testes de `pinned(TikTokCommentEvent, ...)` (não quebrar).
- Se a extração sair num helper puro (recomendado: `PinExtractor.extract(WebcastRoomPinMessage)`),
  os testes ficam sem depender de `TerminalUi`.

### 4. Docs

- `monitor-cli/README.md` e `AGENTS.md`: ajustar a nota de limitações. Remover a promessa de que
  "pins entram ao vivo" sem qualificação; registrar que pin de chat/social/gift/member/like agora é
  capturado pelo payload cru, e que pin fixado **antes** do connect continua não aparecendo.

## Fora de escopo

- Backend Go, bridge Node, frontend, compose, VPS, multi-tenant.
- Backfill do pin fixado antes do connect (limitação da bridge; o estado inicial da sala não é
  retransmitido).
- Tradução de textos de pin social/gift (rótulos fixos em PT-BR bastam).

## Riscos / casos de borda

- **Pin não entregue** (limite Euler/sessão): Tarefa 1 não muda isso; só a Tarefa 2 resolve, e é
  best-effort. Fica registrado no README.
- `WebcastRoomPinMessage.parseFrom` de payload de formato novo (campo desconhecido): protobuf ignora
  campos desconhecidos, então não lança; o `oneof` pode vir vazio → placeholder (visível). Melhor que
  o silêncio atual.
- Dupla contagem se alguém reassinar o evento tipado: por isso removemos a assinatura de
  `TikTokRoomPinEvent`.
- `TikTokWebsocketMessageEvent` também é publicado para todo método mapeado (chat, gift…). O filtro
  por `method.equals("WebcastRoomPinMessage")` evita custo/ruído nos demais.
- Testes que constroem `WebcastRoomPinMessage` dependem da API gerada da lib 1.10.0 (já no classpath);
  se algum `hasXxx()`/`getXxx()` não existir, ajustar à API real do artifact (o `webcast.proto`
  confirma os campos, mas o nome do getter do `oneof` pode variar).

## Validação

1. `mvn -f monitor-cli/pom.xml test` (todos verdes, incluindo o novo `PinExtractorTest`).
2. `mvn -f monitor-cli/pom.xml package`.
3. Manual contra live ativa (`./start.sh`): `:connect <user>` → chat/presentes fluindo → o host fixa
   um comentário → a linha azul `[fixado] @nick: comentário` aparece no feed, na janela `:pinned`
   e o contador `fixados` incrementa; dois pins do mesmo `messageId` contam uma vez.
4. Diagnóstico: se nada aparecer, ler o alerta `pin não mapeado: …` (hipótese registro/método) ou,
   na ausência dele, concluir entrega parcial e habilitar `TLM_TIKTOK_SESSION_ID` (Tarefa 2).
5. Review conforme `AGENTS.md`: subagente `reviewer` com `deepseek/deepseek-v4-pro`.

## Decisão de dependência

Manter `TikTokLiveJava 1.10.0-Release` (commit `d877d38`): o `webcast.proto` dela já contém o
`WebcastRoomPinMessage` completo (`oneof` + `action` + `pin_id`), então **não** é preciso subir de
versão nem mexer nas importações do projeto.
