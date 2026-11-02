# Sorteio ao Vivo — plano de implementação

Branch de trabalho: `lite-sem-ia` (atual, clean). Ao terminar: commit + push desta branch + deploy de produção (PRODUCAO.md).

## Contexto do código

- Backend Go, API pura: SSE em `/events` + REST `/api/*` (`backend/internal/view/server.go`). Eventos da live nascem em `internal/monitor` (Monitor por par (org, live) via `monitor.Manager`), passam pelo controller e saem por `publishSSE(orgID, ...)`.
- Coleta de chat: `handleChatMessage` em `backend/internal/monitor/events.go` (padrão `pendingEmit` — nunca emitir dentro do lock).
- Settings por org: JSON de `monitor.Settings` na tabela `settings` (chave `app:<org>`), validação em `handleSettings` (`backend/internal/view/handlers.go:46`), fluxo frontend em `frontend/renderer.js:3554` (fetch settings → merge → POST).
- Multi-tenant: todo handler usa `requestTenant(w, r)` (org + role); mesmas guardas dos endpoints existentes (405 por método, `writeError` em PT-BR, sem checagem extra de papel — igual `/api/settings`).
- Frontend vanilla: painéis em `frontend/index.html` (cards com `<h3>`), lógica em `frontend/renderer.js`, SSE assinado por `eventSource.addEventListener(...)` (renderer.js:2828+). `frontend/dist/` é gitignored e não é artefato de deploy — não reconstruir.
- Deploy: `git archive HEAD | ssh -p 22022 root@143.95.162.200 'tar -xf - -C /opt/tiktok-live-monitor'` → `docker compose -f docker-compose.production.yml up -d --build --wait` → `./deploy/publish-frontend.sh` → validar `/api/readiness`. Backup pg antes (`PRODUCAO.md`).

## Decisões (validadas com o usuário)

1. **Bag de participantes**: comentários que contêm a **palavra-chave** configurável no painel (case/acento-insensitive) **+ adições manuais** (operador digita @nome). Dedup por uniqueId normalizado; 1 bilhete por usuário.
2. **Sorteio por botão** no painel (on demand), não automático ao fim da live.
3. **N vencedores configurável** (input 1–50, default 1).
4. **Sem persistência** do sorteio no banco nesta versão (sem histórico). Estado vive em memória no Monitor da live; a palavra-chave persiste nas settings da org. Fora de escopo: exportar resultados, weighting, timer, multi-raffles simultâneos.
5. Pós-implementação: commit + push `lite-sem-ia` + deploy completo no VPS.

## Design

### Estado e ciclo de vida (por live)
- Novo estado no `Monitor` (`internal/monitor/sorteio.go`): `sorteioActive bool`, `sorteioParticipants map[string]SorteioParticipant` (chave = `normalizeID(uniqueId)`, manuais usam `lower:` como chave), `sorteioWinners []SorteioParticipant` (último sorteio).
- `SorteioParticipant{UniqueID, Nickname string; Manual bool; JoinedAt int64}` (JSON camelCase).
- Limite `sorteioParticipantsMax = 5000`; acima disso a coleta loga e ignora. Reset de estado em `StartMonitoring` (junto de chatBuffer etc., monitor.go:266).
- Monitorando via manager: fan-out por `liveKey{org, live}`. API receb `live` opcional, resolvida por `ResolveLive(orgID, live)` — igual `/api/ranking`. A UI opera na live resolvida (V1).

### Coleta por palavra-chave
- `Settings.SorteioKeyword string` (json `sorteioKeyword`) em `monitor.Settings`; validação: trim, máx 40 runes, vazio desabilita coleta.
- Em `handleChatMessage` (após a lógica de `detectKeyword`): se `sorteioActive` e `strings.Contains(foldText(comment), foldText(keyword))` → adiciona participante (dedup) + `pendingEmit{"sorteio-participant", ...}`.
- Eventos SSE novos: `sorteio-state` (start/stop/reset), `sorteio-participant` (participante adicionado), `sorteio-result` (winners + participantes restantes). Todos enriquecidos com liveId/orgId pelo manager (já automático).

### Sorteio
- `DrawSorteio(n int)`: embaralha cópia do pool com `crypto/rand` (Fisher–Yates), escolhe n distintos **e remove os vencedores do pool** (sorteio seguinte = suplentes; reentrada manual se necessário). Pool insuficiente → erro (`não há participantes suficientes`). Resultado fica visível (lastResult) até reset.

### API (`POST` body JSON; headers iguais aos POSTs existentes)
| Rota | Corpo | Efeito |
|---|---|---|
| `GET /api/sorteio` | — | `{active, keyword, live, participants[], winners[]}` |
| `POST /api/sorteio/start` | `{keyword?}` | ativa coleta; keyword≠0 atualiza settings da org (fetch→merge→SetSettings→publishSSE `settings-update`) |
| `POST /api/sorteio/stop` | — | encerra coleta (mantém participantes) |
| `POST /api/sorteio/draw` | `{count}` 1..50 | sorteia, remove vencedores, emite `sorteio-result` |
| `POST /api/sorteio/participants` | `{nickname}` máx 40 runes | adiciona manual |
| `POST /api/sorteio/participants/remove` | `{uniqueId}` | remove participante |
| `POST /api/sorteio/reset` | — | limpa participantes + resultado |

### Camadas por arquivo
1. `monitor/monitor.go`: campos/const `EventSorteio*` + limpeza no StartMonitoring; `Settings.SorteioKeyword`.
2. `monitor/sorteio.go` (novo): tipos + métodos do Monitor (locks respeitados; mutações só via métodos com `m.mu`, emits fora do lock via `pendingEmit`).
3. `monitor/manager.go`: métodos fan-out `SorteioStart/Stop/Reset/Draw/AddParticipant/RemoveParticipant/State(org, live)`; retorno `model.ErrOrgRequired`/`fmt.Errorf("live %q is not monitored")` quando ausente.
4. `controller/app.go`: delegantes com orgID + live resolvido (`ResolveLive`).
5. `view/handlers_sorteio.go` (novo) + rotas em `view/server.go` (~linha 133, junto de ranking): cada handler faz guardas (405, tenant, body) e após mutação publica via `s.publishSSE(t.OrgID, ev, payload)` (padrão `settings-update`).
6. `view/handlers.go` handleSettings: validação do novo campo `sorteioKeyword`.

### Frontend
- `index.html`: painel `🎁 Sorteio` (mesmo padrão visual dos cards): input palavra-chave + Salvar; botão Iniciar/Encerrar coleta; contador de participantes; lista com botão remover por item; input + botão "Adicionar manualmente"; input nº de vencedores (1–50, default 1); botão Sortear (desabilitado com pool vazio); área de vencedores em destaque (🥇/🥈/🥉 ordem) com botão Copiar nomes; botão Limpar.
- `renderer.js`: `loadSorteio()` no boot (e ao `connection-status` success / `server-state`); listeners dos 3 eventos; funções `sorteioApi*` reusando padrão fetch JSON existente; `renderSorteio()`; clipboard com fallback; status inline de erro.
- Painel não aparece em admin.html.

## Testes e validação

- `backend/internal/monitor/sorteio_test.go`: coleta com/sem acento por foldText, dedup deRepeated, inativo não coleta, manual add dedup, draw n distintos + remoção, pool insuficiente erro, reset limpa (seguir estilo `monitor_test.go`).
- `backend/internal/view/handlers_sorteio_test.go`: 405 por método, body inválido 400, `{count}` inválido/insuficiente 400, fluxo start/stop/draw com repo falso (seguir `handlers_pix_test.go`).
- Gate local: `cd backend && go vet ./... && go test ./...` e `cd frontend && npm test`.

## Execução (ordem)

1. Settings + monitor (estado, coleta, eventos, draw).
2. Manager + controller.
3. View: rotas, handlers, publishSSE; testes de view.
4. Testes de monitor + `go vet/test` verdes.
5. Frontend: painel + renderer + SSE.
6. `npm test` frontend.
7. Commit (mensagem PT-BR estilo repo: `feat(sorteio): ...`) na `lite-sem-ia`, push.
8. Deploy produção conforme PRODUCAO.md: backup pg dump → `git archive HEAD | ssh ...` → `docker compose -f docker-compose.production.yml config --quiet && up -d --build --wait` → `./deploy/publish-frontend.sh` → `docker compose ... ps` + validar `https://livemonitortk.com.br/api/readiness`, painel e coleta/sorteio em live real.

## Riscos / observações

- Estado em memória: `docker compose restart` no meio da live perde participantes (aceito — sem histórico nesta versão).
- Org com 2+ lives: sorteio opera na live resolvida (primeira da org / `?live=`). UI V1 não escolhe live — endereço documentado no painel quando relevante.
- Vencedores saem do pool pós-sorteio: suplentes são o resto; recolocar manualmente se o vencedor não aparecer.
- Deploy toca produção: seguir backup + passos exatos do PRODUCAO.md; nunca `down -v`.
