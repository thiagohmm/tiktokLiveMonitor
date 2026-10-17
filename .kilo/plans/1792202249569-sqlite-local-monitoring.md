# monitor-cli standalone: monitoração local só em Java + SQLite

**Branch:** `feature/java-terminal-monitor` (local-only — não vai para o VPS; backend Go permanece intocado).

## Decisão do usuário (fechada)
- **"Usa só o java"**: o TUI (`monitor-cli`) se torna a aplicação completa. Sem backend Go, sem node/bridge, sem login.
- **"Só local, sem login"**: o modo remoto (login/API do VPS) é removido do CLI — nada de HTTP client/SSE para produzão.
- Sem copiar tudo da branch `lite-sem-ia` — enxuto, foco nos comandos do TUI.
- A queixa original (`:filter pinned` vazio) se resolve de vez: pins chegam direto do TikTok ao TUI e são gravados/consultados no SQLite local.

## Contexto técnico (verificado)
- CLI atual: Java 21 + Lanterna (Clean Architecture em `monitor-cli/src/main/java/com/tlm/cli/`): `ui/` (feed/filtros), `application/` (use cases + `MonitorHub`, `WatchEventsUseCase`, `SessionManager`/`LoginUseCase`), `domain/` (eventos/categories/ports), `infrastructure/api/` (AuthHttpApi, LiveHttpApi, SseClient — SSE contra a API Go).
- Boa notícia arquitetural: a UI/`FeedBuffer`/`CommandParser` são independentes de transporte — só `application`/`infrastructure` trocam.
- Biblioteca de ingestão escolhida: **TikTokLiveJava (jwdeveloper)** `com.github.jwdeveloper.TikTok-Live-Java:Client:1.10.0-Release` (JitPack) — porta Java do mesmo `tiktok-live-connector` 2.4.4 usado pelo backend hoje; traz chat/gift (com `GiftManager` = catálogo)/likes/follow/share/**RoomPin**/reconexão via Euler Stream (o conector Node já tolera falhas do Euler — mesmo risco aceito hoje). Fallback alternativo (prime: PirateTok/live-java, sem API key, porém Java 25 e novo demais — só usar se a jwdeveloper falhar no spike).

## Tarefas

### 1. Spike — validar a biblioteca (primeiro passo)
- Projeto de teste mínimo com JitPack: conectar numa live ativa e receber CHAT/GIFT/LIKE/FOLLOW/SHARE/**ROOM_PIN** + `getGiftManager()` + reconexão automática. Confirmar disponibilidade do evento de pin (`TikTokRoomPinMessageEvent`/`onRoomPin`); se ausente, verificar captura via `onWebsocketUnhandledMessage` (protobuf cru) antes de trocar para PirateTok.

### 2. Portas no domínio (substituem LiveApiPort/SseClient)
- `domain/port/LiveEventsPort`: callbacks `onChat/onGift/onLike/onShare/onFollow/onPinned/onStateChange(connected|disconnected|ended)` (+ catálogo de presentes pronto).
- `domain/port/LocalStorePort`: sessões, mensagens, presentes, compartilhamentos, likes (totais), fixados (+ pin_id dedupe por sessão), com leitura retroativa (`recentPinned(live, n)`).
- `domain/port/sqlite`: adapter **JDBC** (`org.xerial:sqlite-jdbc`) em `monitor-cli/data/tlm.db` (WAL). Schema mínimo espelhando as tabelas de eventos (mesma forma do backend, sem multi-tenant/auth/PIX/anomalia). Reset automático do feed ao `:connect`? Não: backfill só de fixados.

### 3. Adapter TikTok (ingestão)
- `infrastructure/tiktok/TikTokLiveAdapter`: implementa `LiveEventsPort` com a lib (client por username; heartbeats/reconnect da lib; timestamps em RFC3339; mapeamento para `DomainEvent`).
- Pins: dedupe por pinId (Set com teto 200, mesma regra do bridge.js) + captura de pin já presente ao conectar quando a lib expõe (messagem CRU se caso). Sem `unpin` (action=2).
- Emissão de `SystemNotice` para: conectado/desconectado ao vivo, live encerrada, catálogo carregado, falha de conexão.

### 4. Application/UI sem login
- Remover: `SessionManager`, `LoginUseCase`, `WatchEventsUseCase`, prompt de login do `Main.java`, `--url`/`TLM_URL`/config de credenciais (e-mail/senha);
- Novo `application/LocalMonitorUseCase`: `:connect user` cria/alimenta engine + grava sessão; `:quit`/Ctrl+C → desconecta best-effort e Stop de tudo; feed segue igual.
- `:pinned`/`:filter pinned`: filtra `Category.PINNED` como hoje E faz backfill do SQLite (`recentPinned(live, 20)`) injetando linhas `(histórico)` — resolve "nada aparece" mesmo em sessão nova/restart.
- `:pinned list`: últimos 20 do SQLite (mesma origem local). `:gift` usa catálogo do adapter. `:filter`, `:clear`, `:help`, `:disconnect` inalterados.
- Presentes-alvo (:gift seleção) continua filtrando o feed local; persistir seleção não é necessário (ephemeral).

### 5. Cleanup
- Deletar `infrastructure/api/` (todos), testes de login/SSE; reescrever testes: `LocalStoreTest` (CRUD + dedupe de pin_id + recentPinned), `TikTokLiveAdapterTest` (mapeamento evento→DomainEvent com fake do client), `FeedBufferTest` já existente deve passar.
- `.gitignore`: `monitor-cli/data/*.db*`.

### 6. Docs/escript
- `start.sh`/`start.bat`: `mvn -f monitor-cli/pom.xml package -DskipTests && java -jar monitor-cli/target/monitor-cli.jar` (já quase pronto — garantir `data/` criada).
- README: seção "Rodar local (standalone)" — requisitos só: Java 21+ e Maven; nota de primeira build (JitPack baixa a lib).
- AGENTS.md: atualizar a seção do monitor-cli (standalone SQLite, sem login/API/VPS).

## Fora de escopo
- Qualquer mudança no backend Go, node/bridge, compose, VPS; multi-org/auth/PIX/anomalias/frontend; histórico de chat/gifts além dos fixados; persistência da seleção de `:gift`.

## Riscos
- JitPack/dependência nova no build → fixar versão; se indisponível, avaliar mirror/local snapshot.
- Euler Stream (assinatura) pode limitar/oscilar — clássico já aceito no conector node; opcionalmente expor env de API key (EULER) se a lib suportar.
- Evento RoomPin indisponível na lib 1.10 → spike decide entre: protobuf cru via `onWebsocketUnhandledMessage` ou trocar para PirateTok.
- Live 18+/blocked device: fora do alcance (mesma limitação do backend atual).

## Validação
- `mvn -f monitor-cli/pom.xml test` (unit: store, adapter mapping, feed/filter).
- E2E manual (`./start.sh`): `:connect <user>` → chat/gifts/likes/follow fluem e persistem; host fixa comentário → aparece ao vivo (azul) e em `:pinned list`; `:filter pinned` mostra fixados ao vivo + `(histórico)` do DB; reiniciar o jar e repetir `:pinned list` (história do SQLite); `:disconnect`, `:clear`, `:quit` (Ctrl+C limpo); `:gift` catálogo + filtro de presentes.
- Review: seguir AGENTS.md — subagente `reviewer` com `deepseek/deepseek-v4-pro`.
