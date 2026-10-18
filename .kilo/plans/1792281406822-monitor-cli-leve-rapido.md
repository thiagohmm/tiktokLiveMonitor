# Plano: monitor-cli mais leve e mais rápido

## Contexto

Escopo: **somente o `monitor-cli/`** (TUI Java 21 + Lanterna, engine TikTokLiveJava, SQLite local) — o produto novo desta branch. Backend Go/frontend ficam de fora.

Prioridades do usuário (em ordem): **CPU/responsividade**, **tempo de início**, **memória RAM**. Tamanho do artefato (jar 22MB) ficou **fora de escopo**.

Estado atual do código e gargalos identificados:

1. **CPU**: `TerminalUi.run()` redesenha a tela inteira (`draw()` + `refresh(DELTA)`) a cada 30ms mesmo sem nada mudar (~33 quadros/s ociosos). `DosWindow.presentation()` copia listas (`feed.lines()` → `List.copyOf` → `subList` → `reverse`) **por quadro, por janela** — churn de GC constante. `DosWindow.matches()` faz `toLowerCase()` por linha por janela por evento.
2. **Início**: `start.sh` roda `mvn package -DskipTests` **sempre** (Maven + shade de 22MB). `JvmRelaunch` **sempre** relança um JVM filho duplo (`--sun-misc-unsafe-memory-access=allow`), dobrando o boot; e em runtime < 23 a flag é desconhecida — o filho morre e o app não sobe (README promete "Java 21+").
3. **SQLite (evento→tela)**: um `PreparedStatement` novo por evento + `SELECT id FROM lives` **por evento** + `synchronous=FULL` implícito com WAL (fsync por INSERT). Em rajada de likes isso atrasa o handler da lib e, por consequência, a UI.
4. **RAM**: heap padrão da JVM (25% da máquina) para um TUI; cópias transitórias de `presentation()` pressionam o GC.

## Decisões

- **Renderização por sujeira (dirty-flag)**: desenhar/refresh só quando houve mudança (evento, tecla, mouse, resize, segundo do uptime mudando). Poll de input a 30ms permanece (latência de teclado inalterada).
- **SQLite síncrono porém rápido**: `PRAGMA synchronous=NORMAL` (com WAL já ativo) + cache de `live_id` + statements reutilizáveis no caminho quente de escrita. Sem fila assíncrona, sem mudança de semântica de fechamento. Aceito: em queda de energia/OS os últimos eventos podem se perder (corrupção não ocorre; o app pode ter gravado tudo até o último checkpoint).
- **Início**: evitar o relançamento do JVM quando desnecessário e evitar rebuild quando o jar está atualizado.
- **Fora de escopo**: redução do jar/dependências, fila assíncrona de escrita, backend Go/frontend.

## Tarefas (ordem de execução)

### 1. Renderização dirty-flag — `monitor-cli/src/main/java/com/tlm/cli/ui/TerminalUi.java`

- Em `run()`: manter `drainPending()`, `pollInput()` e `doResizeIfNecessary()` a cada tick (30ms), mas executar `draw()` + `setCursorPosition(null)` + `refresh(DELTA)` **somente quando sujo**. Marca sujeira quando:
  - `drainPending()` executou ao menos uma tarefa (fazer `drainPending()` retornar `boolean`);
  - `pollInput()` devolveu tecla/mouse (todo `handleKey`/`handleMouse` conta);
  - `doResizeIfNecessary()` retornou não-nulo (houve resize);
  - o segundo do uptime mudou (guardar `lastDrawnSecond`; força repaint ~1Hz para o cabeçalho `no ar HH:MM:SS`).
- `requestQuit()`/comandos que mutam o feed pela própria thread de UI (`:clear`, `:help`, filtros etc.) já passam por `handleKey` → sujeira coberta. Tarefas vindas de `post(...)` cobertas pelo retorno de `drainPending()`.
- Teste novo viável: extrair a decisão "deve redesenhar?" num método puro (ex.: `FrameScheduler`) testável sem Lanterna; ou testar `drainPending` retornando true/false (existem `FeedBufferTest`/`WindowDeskTest` como padrão).

### 2. Eliminar cópias por quadro — `DosWindow.java` (+ `FeedBuffer.java` se preciso)

- Cachear `presentation()`: campo `List<FeedLine> presentationCache` + flag/versão invalidada em toda mutação (`offer`, `inject`, `replaceExtras`, `replaceSnapshot`, `replay`, `gifts`, `minCount`, `user`, `setLimit`, `commitLimitEdit`, `toggleOrder`).
- Corrigir `holdScroll(added)`: hoje chama `presentation().size()` (reconstrói a lista a cada evento). Calcular o tamanho aritmeticamente: `min(feedSize, limit > 0 ? limit : feedSize)` — sem materializar.
- `matches()`: evitar `toLowerCase()` repetido — comparar com `regionMatches(true, ...)` (ou cachear o lowercase do usuário/filtro). Mesma correção em `TerminalUi.applyUserFilter`/caminhos que comparam usuário.
- `FeedBuffer.add()` com buffer cheio faz `view.subList(0, overflow).clear()` (O(n) por evento): trocar `view` para `ArrayDeque` com `pollFirst()` (mantendo a API `window/lines`); `scrollOffset`/âncora continuam válidos (mesma semântica de remoção da cabeça). Atualizar `FeedBufferTest`.

### 3. SQLite rápido (síncrono) — `monitor-cli/src/main/java/com/tlm/cli/infrastructure/sqlite/SqliteStore.java`

- Após `journal_mode=WAL`: `PRAGMA synchronous=NORMAL`.
- Cache de live: `Map<String, Long> liveIds`; `liveId()` consulta o banco só no cache-miss (métodos já são `synchronized`).
- Statements reutilizáveis no caminho quente (criar no construtor, fechar em `close()`):
  - INSERT de `user_messages`, `gifts`, `shares`, `likes`, upsert de `room_like_totals`;
  - SELECT/INSERT de `lives` (usado pelo cache de liveId).
- `recordPinned`: trocar SELECT+INSERT por `INSERT OR IGNORE` único e retornar `executeUpdate() > 0` (o índice único parcial `idx_pinned_comments_pin` já garante o dedupe) — 1 ida ao banco em vez de 2. Manter INSERT normal quando `pin_id` for vazio.
- Consultas frias (`recentPinned`, `recentGifts`) continuam com `prepareStatement` por chamada (só em comandos).
- `SqliteStoreTest`: cobrir cache de liveId (segundo evento com o mesmo nome não perde o id), `recordPinned` devolvendo `false` no replay do mesmo `pin_id` e `true` no pin novo.

### 4. Início rápido — `JvmRelaunch.java`, `start.sh`

- `JvmRelaunch.handOffIfNeeded`/`command()`: **não relançar** quando a flag for desnecessária:
  - `Runtime.version().feature() < 23` → sem relançamento (a flag nem existe; corrige o boot quebrado em Java 21);
  - `allowsMemoryApi(vmArgs)` já cobre o caso da flag presente (manter).
  - Relançamento continua como fallback para `java -jar` direto em runtime 23+ sem a flag. Atualizar `JvmRelaunchTest`.
- `start.sh`:
  - **Build condicional**: só `mvn -f monitor-cli/pom.xml package -DskipTests` quando `monitor-cli/target/monitor-cli.jar` for mais antigo que qualquer arquivo em `monitor-cli/src` ou `monitor-cli/pom.xml` (ex.: `find monitor-cli/src monitor-cli/pom.xml -newer monitor-cli/target/monitor-cli.jar`); senão, pular direto para o `java -jar`.
  - **Flag direta na linha do java**: se `java --sun-misc-unsafe-memory-access=allow -version` aceitar (exit 0), invocar com a flag — zero relançamento; senão, `java -jar` puro (o `JvmRelaunch` resolve como hoje).
  - **Heap modesto**: `-Xmx256m` (TUI não precisa do heap default de 25% da RAM) + passagem de `TLM_JAVA_OPTS` para customização.
- Atualizar `monitor-cli/README.md` (seção "Rodar") e o texto de uso em `Main.java` se a invocação mudar.

## Riscos

- `synchronous=NORMAL`: em queda de energia/OS os últimos eventos não checkpointados se perdem — aceito para monitor local (WAL mantém consistência).
- Dirty-flag: esquecer um caminho de mutação sem marcar sujeira deixa a tela "congelada" até o próximo evento — mitigar com o repaint 1Hz do uptime e testes do passo 2 (cache invalidado corretamente).
- Guard de versão do `JvmRelaunch` muda comportamento em Java 21/22 (hoje quebra; passa a funcionar) — verificado pelo teste atualizado.

## Validação

1. `mvn -f monitor-cli/pom.xml test` — todos os testes (existentes + novos).
2. Manual: `./start.sh` com jar atualizado abre **sem** rebuild e sem processo JVM duplicado (`ps`/`jps` mostra 1 JVM); segunda execução com fonte alterada rebuilda.
3. Manual: `:connect <user>` em live real — feed fluido, janelas (`:comentarios`, `:gift`, `:pinned list`, `:gift all`) corretas; arrastar/fechar/rolar sem artefatos.
4. CPU ocioso: com a TUI parada e sem live, `top` deve mostrar CPU ~0% do java (hoje há consumo constante de redraw); em rajada de likes a UI não engasga.
5. `data/tlm-local.db`: eventos seguem sendo gravados (conferir contagens pós-live); `PRAGMA synchronous` retorna `1` (NORMAL).
