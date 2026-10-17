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
:gift               lista o catálogo de presentes da live
:gift 1,4 | :gift Rosa,Dino   filtra o feed pelos presentes escolhidos
:gift all           desfaz o filtro de presente
:pinned             fixados ao vivo; se o feed não tem nenhum, busca os
                    últimos 20 do banco local (marcados como histórico)
:pinned list        imprime os últimos 20 fixados do banco
:filter <all|chat|gift|like|follow|pinned|system>
:clear | :help | :quit (Ctrl+C também encerra, desconectando a live)
```

PageUp/PageDown rolam o feed; Esc limpa o campo de digitação. O rodapé conta
comentários, presentes, curtidas, seguidores e fixados da sessão.

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
