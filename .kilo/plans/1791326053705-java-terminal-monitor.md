# Plano — Monitor de terminal em Java (TUI) consumindo a API de produção

## Objetivo
Criar um cliente de monitoração em **Java 21**, estilo **terminal Linux** (TUI com Lanterna), usando **Clean Architecture**, que consome a **API de produção** (`https://livemonitortk.com.br`) — sem criar/copiar API. Escopo: **monitorar eventos em tempo real (SSE) + conectar/desconectar lives**. Testes automatizados com **JUnit 5 + MockWebServer**.

## Decisões tomadas
| Decisão | Escolha |
|---|---|
| Escopo | Monitorar SSE + `connect`/`disconnect` de lives (POST com CSRF + Origin) |
| Stack | Java 21 (virtual threads p/ SSE), Maven, Lanterna 3 (TUI) |
| Localização | Novo módulo `monitor-cli/` na raiz do repo (não toca `backend/` nem `frontend/`) |
| Branch | `feature/java-terminal-monitor`, criada a partir de `main` |
| Credenciais | Prompt + `~/.config/tlm-cli/config.properties` (URL + e-mail; senha NUNCA salva); flags/env (`--url`, `TLM_EMAIL`, `TLM_PASSWORD`) sobrescrevem |
| Testes | JUnit 5 + MockWebServer (OkHttp, escopo test); `mvn test` sem tocar produção |

## Contrato da API (verificado no código `backend/`)
- `POST /api/auth/login` `{email,password}` → 200 `{authenticated, csrfToken}` + `Set-Cookie: tlm_session=<token>`. Erros: 401 (credencial), 403 (conta inativa/aguardando aprovação), 429 (lockout, corpo tem `retryAfterSec`).
- Autenticação nas demais chamadas: header `Authorization: Bearer <token>` (o valor do cookie) — aceito por `TokenFromRequest` (`internal/auth/auth.go:65`). Evita cookie jar.
- Chamadas **não-GET** exigem também: `Origin: <SITE_URL>` (comparado ao `SITE_URL` do servidor — usar a base URL configurada) e `X-CSRF-Token: <csrfToken>` (`auth.go:86-115`).
- `GET /events` → `text/event-stream`; frames `event: <tipo>\ndata: <json>\n\n`; keepalive `: ping`. 401 → sessão inválida; 503 → teto de clientes (retry com backoff).
- Tipos de evento (`internal/monitor/monitor.go:17-31`): `server-state`, `new-chat-message`, `any-gift-received`, `new-gift-user`, `new-like-event`, `new-follower`, `new-social-event`, `pinned-comment`, `flagged-message`, `keyword-mention`, `connection-status`, `live-user-connected`, `gifts-list`, `goal-update`, `settings-update`.
- REST: `GET /api/state`, `GET /api/lives`, `GET /api/settings`, `GET /api/history?limit=N`.
- Ações: `POST /api/connect` corpo `{"username":"<tiktok>"}`; `POST /api/disconnect?username=<tiktok>` (username vazio desconecta o monitor corrente).
- `GET /api/auth/me` devolve `csrfToken` atualizado (usar p/ recuperar de 403 por CSRF).

## Arquitetura (Clean Architecture + princípios clean code)
Pacotes por camada, regra de dependência apontando para o domínio:

```
monitor-cli/
  pom.xml
  src/main/java/com/tlm/cli/
    domain/            # sem dependência de framework
      model/           # ChatMessage, GiftEvent, LikeEvent, LiveState, ServerEvent…
      port/            # interfaces: AuthPort, LiveApiPort, EventStreamPort, ConfigPort
    application/       # casos de uso: LoginUseCase, WatchEventsUseCase,
                       # ConnectLiveUseCase, DisconnectLiveUseCase, ReconnectPolicy (backoff)
    infrastructure/
      api/             # HttpClient (java.net.http), SseParser, DTOs + mappers JSON (Jackson)
      config/          # ConfigResolver (flags > env > config file > prompt), ConfigStore
    ui/                # Lanterna: telas/painéis, Presenter (domínio → view model), CommandParser
    Main.java          # composition root: instancia adapters, injeta nas use cases
  src/test/java/com/tlm/cli/  # espelha pacotes
```

- **Virtual threads** (`Executors.newVirtualThreadPerTaskExecutor()`) para o loop de leitura SSE bloqueante (`BodyHandlers.ofInputStream()`).
- Reconexão SSE com **backoff exponencial** 1s → ×2 → teto 15s (espelha `frontend/auth.js`), resetando após conexão bem-sucedida; ao reconectar, re-sincroniza via `GET /api/state`.

## TUI (estilo terminal Linux)
- **Header**: título, status da conexão (● verde/vermelho), live atual, uptime.
- **Feed central**: eventos com timestamp `[HH:mm:ss]` e cor por tipo (chat=ciano, gift=amarelo, like=magenta, follow=verde, sistema/erro=vermelho).
- **Rodapé/stats**: contadores de comentários, presentes, likes, seguidores.
- **Linha de comando** (`:` estilo vi): `:connect <user>`, `:disconnect [user]`, `:filter <tipo|all>`, `:clear`, `:help`, `:quit`.
- Tratamento de erros na UI: 401 → re-login (prompt de senha) 1×; 429 → exibe `retryAfterSec`; 403 CSRF → renova token via `/api/auth/me` e tenta 1×.

## Maven (pom.xml)
- `maven.compiler.release=21`; deps: `com.googlecode.lanterna:lanterna:3.1.2`, `jackson-databind`; test: `junit-jupiter`, `com.squareup.okhttp3:mockwebserver`.
- `maven-shade-plugin` → jar executável `monitor-cli.jar` (`Main-Class`); rodar com `java -jar` ou `mvn compile exec:java`.

## Testes automatizados
- **Unitários**: `SseParser` (frames multi-linha, comentário `: ping`, evento sem nome, buffer parcial); mappers JSON→domínio; `ReconnectPolicy` (sequência de backoff e reset); `CommandParser` (`:connect`, args inválidos); formatação do feed/cores; `ConfigResolver` (precedência flags>env>arquivo>prompt).
- **Integração (MockWebServer)**: login devolve `Set-Cookie` + `csrfToken` → cliente envia `Authorization: Bearer` nas chamadas seguintes; `POST /api/connect` leva `Origin` + `X-CSRF-Token`; stream `/events` em chunks → eventos chegam ao caso de uso; 401 no SSE → fluxo de re-login; 503 no SSE → backoff.
- Nenhum teste chama a produção; smoke manual contra produção fica fora do `mvn test`.

## Lista de tarefas (ordem de execução)
1. `git checkout -b feature/java-terminal-monitor` a partir de `main`.
2. Scaffold `monitor-cli/` (pom.xml, estrutura de pacotes, `.gitignore` do módulo).
3. Camada `domain` (model + ports).
4. `infrastructure/config`: `ConfigStore` (lê/escreve `~/.config/tlm-cli/config.properties`, perm 600) + `ConfigResolver` com precedência.
5. `infrastructure/api`: cliente de login (extrai token do `Set-Cookie`), `LiveApiClient` (state/lives/connect/disconnect com CSRF+Origin), `SseClient` + `SseParser`.
6. `application`: use cases + `ReconnectPolicy`.
7. `ui` Lanterna: header/feed/stats/command line, `Presenter`, `CommandParser`.
8. `Main.java` (wiring, virtual threads, shutdown hook limpo: `:quit`/Ctrl+C faz `disconnect` lógico da sessão SSE e fecha).
9. Testes (unit + integração MockWebServer) até `mvn test` verde; `mvn package` gera o jar.
10. `--help`/mensagem de uso embutida no binário (sem criar docs extras).
11. Atualizar `AGENTS.md` (raiz) com uma linha sobre o módulo `monitor-cli/` (estrutura do projeto mudou).
12. **Review**: delegar a um subagente `reviewer` com modelo `deepseek/deepseek-v4-pro` (exigência do AGENTS.md) antes de concluir.

## Riscos / modos de falha
- `Origin` errado em POST → 403: o CLI deve enviar exatamente a base URL configurada (sem barra final).
- Token de sessão expira/revogado → 401 em qualquer endpoint: re-login via prompt de senha.
- Lockout (429) respeitado: não insistir antes de `retryAfterSec`.
- Produção é HTTPS: senha nunca em arquivo/log; token só em memória.
- Eventos SSE desconhecidos (novos tipos no futuro): renderizar como "sistema" com o payload bruto, nunca quebrar o parser.

## Fora de escopo
Fila PIX, alteração de settings, relatórios/ranking, empacotamento nativo (GraalVM), mudanças de CI/deploy, modificações no backend Go ou no frontend.

## Validação
- `mvn test` verde (unit + integração).
- `mvn package` gera jar executável.
- Smoke manual (pelo usuário, com credenciais reais): `java -jar monitor-cli/target/monitor-cli.jar` → login → feed de eventos → `:connect <live>` → `:disconnect` → `:quit`.
- Review do diff por subagente DeepSeek antes de merge/push.
