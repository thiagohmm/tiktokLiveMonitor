# monitor-cli — monitor de TikTok Live no terminal (local, só Java)

TUI standalone em Java 21 (Lanterna): conecta **direto** ao push de live do
TikTok (biblioteca [TikTokLiveJava](https://github.com/jwdeveloper/TikTokLiveJava),
mesmo ecossistema do conector Node usado pelo backend) e grava os eventos em um
**SQLite local**. Sem backend, sem login, sem Docker — este branch é para
monitoração local e **não vai para o VPS**.

## Rodar

```bash
./start.sh            # build + TUI (requer Java 21+ e Maven)
# ou:
mvn -f monitor-cli/pom.xml package && java -jar monitor-cli/target/monitor-cli.jar
```

Opções:

| Flag / env              | Efeito                                                    |
|-------------------------|-----------------------------------------------------------|
| `--db ARQUIVO` / `TLM_DB` | caminho do SQLite (padrão `./data/tlm-local.db`)        |
| `TLM_TIKTOK_API_KEY`    | chave opcional do Euler Stream (assinatura do WebSocket) quando o limite gratuito faltar |

A primeira build baixa a lib do JitPack (precisa de rede).

## Comandos da TUI

```
:connect <user>     conecta direto à live (@ opcional; uma live por vez)
:disconnect         desconecta a live atual
:comentarios [user] janela de comentários ao vivo (filtro opcional de usuário)
:gift               abre a janela de presentes e lista o catálogo
:gift 1,4 | :gift Rosa,Dino   filtra essa janela pelos presentes escolhidos
:qty <n>            quantidade mínima na janela de presentes (0 limpa)
:gift todos         tira o filtro de nome e de quantidade
:gift all           janela com o histórico de presentes da live (SQLite, até 200)
:pinned [list]      janela de fixados; sem pins ao vivo, busca os últimos 20
:mensagens [user]   janela com os eventos da live ao vivo
:curtidas :seguidores
:user <nome>        filtra a janela em foco por usuário (vazio limpa)
:close              fecha a janela em foco (Esc também, com o campo vazio; Ctrl+W idem)
:filter <all|chat|gift|like|follow|pinned|system>   filtra só o feed de fundo
:clear | :help | :quit (Ctrl+C também encerra, desconectando a live)
```

A barra no topo abre as mesmas janelas com o mouse. Cada janela é uma caixa
estilo DOS: arraste a barra de título para mover, clique em `[X]` para fechar,
use a roda ou as setas para rolar. Tab troca a janela em foco. O feed de fundo
continua com todos os eventos. O rodapé conta comentários, presentes, curtidas,
seguidores e fixados da sessão.

## Banco local

Tabelas em `data/tlm-local.db`: `lives`, `user_messages`, `gifts`, `shares`,
`likes`, `room_like_totals` (total da sala mantém o maior valor visto) e
`pinned_comments` (dedupe por `pin_id` dentro da live — pin e unpin do mesmo
messageId contam como um). Timestamps em ISO-8601 UTC.

## Limitações

- Pins **fixados antes do connect** não aparecem: o estado inicial da sala não
  é retransmitido (mesma limitação da bridge do backend). A partir da conexão,
  pins entram ao vivo e ficam no histórico.
- Salas 18+ podem exigir cookie de sessão (a lib não o usa por padrão).
- A assinatura do WebSocket depende do Euler Stream (free tier); configure
  `TLM_TIKTOK_API_KEY` se aparecer erro de sign/limite.
- Reconexão: a lib tenta sozinha (2s); o feed mostra "reconectando..." uma vez
  por ciclo para não spammar.

## Testes

```bash
mvn -f monitor-cli/pom.xml test
```
