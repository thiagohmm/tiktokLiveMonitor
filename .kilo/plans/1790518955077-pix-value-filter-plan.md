# Filtro de valores na Fila PIX (comprovante → OCR/valor → fila)

## Objetivo

Na Fila PIX o operador define uma lista de valores aceitos (ex.: R$ 10,00 e R$ 15,00) em textboxes na UI. Quando chega um comprovante (PDF ou JPG/JPEG), o backend extrai os valores monetários do arquivo e só deixa o atendimento **aparecer na fila quando a soma dos comprovantes do ticket atinge um dos valores aceitos**. Caso contrário o cliente recebe uma auto-resposta dizendo **quanto falta**.

## Contexto do código

- Fluxo atual: `POST /api/webhooks/whatsapp` (HMAC) → `PixQueueService.HandleInbound` (`backend/internal/controller/pixqueue.go:285`) → `handleReceipt` (linha 339) baixa a mídia do WAHA, valida magic bytes (`backend/internal/media/mime.go`), sobe pro MinIO, grava `pix_messages`, marca `has_receipt`, emite SSE `pix-ticket-update`/`pix-new-message`.
- Hoje **todo** JPG/PDF entra na fila. Não há extração de valor.
- Dockerfile do backend é multi-arch (amd64/arm64, Raspberry Pi) com `CGO_ENABLED=0`.
- Schema PIX criado no boot (`migratePostgres`, `backend/internal/database/postgres.go`) e espelhado em `supabase/migrations/005_pix_queue.sql` (RLS default deny; migração aplicada manualmente no SQL Editor do Supabase).
- Frontend da fila: `frontend/index.html` (seção Fila PIX ~linha 1555) + `frontend/pix.js`; testes em `frontend/pix-queue.test.mjs`.

## Decisões confirmadas com o usuário

1. **Sem match → auto-resposta** informando quanto falta (ex.: "falta R$ 2,00"), reaproveitando o envio via WAHA já existente (`sendAutoReply`).
2. **Somar parciais**: 8,00 + 2,00 = 10,00 → ticket entra na fila. Comprovante parcial fica armazenado (para acumular) porém oculto na fila até fechar o total.
3. **Nenhum valor configurado → filtro desligado** (comportamento atual, retrocompatível).
4. **Match exato**: a soma só abre a fila quando **igualar** um valor aceito. Soma intermediária (12,00 com aceitos 10/15) continua parcial, com aviso "falta R$ 3,00" para o próximo valor (15). Soma **acima do maior** aceito (ex.: 16,00) → ticket visível para o operador resolver (pagou a mais).

## Regras de borda (defaults definidos no plano)

- Gate ativo somente se o dono tiver ≥ 1 valor configurado.
- Visibilidade do ticket pendente (com gate ativo): tem ≥ 1 mensagem de **texto** inbound do cliente, **ou** soma dos comprovantes **igual** a um valor aceito, **ou** soma > maior valor aceito (cliente pagou a mais → operador decide). Histórico (`answered`) nunca é filtrado.
- Mensagens de texto do cliente continuam aparecendo normalmente (o filtro é só sobre comprovantes).
- Valor principal do comprovante = **maior** valor monetário extraído (comprovantes trazem "Valor: R$ X,XX" como linha principal).
- Valor ilegível (OCR não acha valor / PDF sem camada de texto) → comprovante **não** é armazenado, mídia deletada, auto-resposta "não foi possível identificar o valor no comprovante...", ticket vazio (sem mensagens) é apagado para não aparecer na fila.
- Erro de infraestrutura (binário tesseract ausente, erro de parse de PDF) → **fail-open**: aceita o comprovante como hoje + `log` de aviso (não bloquear pagamento válido por falha de OCR).
- Formato monetário pt-BR: aceita `R$ 15,00`, `15,00`, `15` (→ 1500 cents), `1.234,56`. Token só conta como moeda se tiver prefixo `R$` **ou** decimais com vírgula (evita confundir datas/IDs).
- Teto: máx. 20 valores por dono, cada um entre R$ 0,01 e R$ 999.999,99.
- Auto-resposta de parcial é enviada **a cada** comprovante parcial (não há dedupe por ticket, pois o ticket pode nem existir visível; aceitável — o cliente é sempre informado).
- **Concorrência**: dois comprovantes do mesmo ticket quase simultâneos podem ler a mesma soma e "fechar" duplicado. Serializar o processamento de inbound por ticket no service (mapa de `sync.Mutex` por ticket, mesmo padrão de `purgeLocks`), ou calcular a soma dentro da transação do insert. Preferir o mutex por ticket (simples e já existe precedente).
- **Latência do webhook**: OCR síncrono adiciona ~1–3 s por JPG. O webhook já é síncrono (download+MinIO) e o dedupe por `whatsapp_message_id` já protege contra reentrega do WAHA em timeout — manter síncrono.

## Tarefas

### 1. Bibliotecas e OCR (backend)

- Adicionar `github.com/ledongthuc/pdf` (Go puro, sem CGO) ao `backend/go.mod` para extrair texto de PDF com camada de texto.
- OCR de JPG: chamar binário `tesseract` via `exec.Command` (`tesseract <tmp>.jpg stdout -l por`), sem CGO. Novo pacote `backend/internal/receipt/`:
  - `ExtractTextPDF(data []byte) (string, error)`
  - `ExtractTextJPEG(ctx context.Context, data []byte) (string, error)` (arquivo temporário; binary path via env `TESSERACT_BIN`, default `tesseract`)
  - `ExtractValuesCents(text string) []int64` (regex pt-BR → cents)
  - `Decide(newSumCents int64, accepted []int64) (status: partial|complete|over, missingCents int64)`: `complete` se `newSum ∈ accepted`; `over` se `newSum > max(accepted)`; senão `partial` com `missing = menor(accepted) − newSum` tal que `missing > 0` (ex.: 12,00 → falta 3,00 p/ 15,00).
- Dockerfile (stage 2, `backend/Dockerfile`): `apt-get install -y --no-install-recommends tesseract-ocr tesseract-ocr-por` (amd64 e arm64 existem no Debian bookworm; +~120 MB na imagem). `CGO_ENABLED=0` inalterado.
- `.env.example`: documentar `TESSERACT_BIN` (opcional).

### 2. Schema (2 pontos: boot + Supabase)

- Nova tabela `pix_value_rules (id bigserial pk, owner_user_id text not null, value_cents bigint not null check (value_cents > 0), created_at timestamptz not null default current_timestamp, unique (owner_user_id, value_cents))`.
- Nova coluna em `pix_messages`: `media_value_cents bigint not null default -1` (−1 = sem extração; preenchida só em comprovantes legíveis).
- `migratePostgres()` (`backend/internal/database/postgres.go`): `CREATE TABLE IF NOT EXISTS pix_value_rules ...` + `ALTER TABLE pix_messages ADD COLUMN IF NOT EXISTS media_value_cents BIGINT NOT NULL DEFAULT -1`.
- Novo arquivo `supabase/migrations/006_pix_value_rules.sql` espelhando tudo com RLS default deny + `REVOKE ALL ... FROM anon, authenticated` (padrão do 005). **Lembrar o usuário de aplicar no SQL Editor.**

### 3. Repositório

- `backend/internal/model/repository.go`: nova interface `PixValueRuleRepository` (`ListPixValueRules(owner) ([]int64, error)` em cents; `ReplacePixValueRules(owner string, cents []int64) error` — delete+insert) e `DeleteEmptyPixTicket(owner string, ticketID int64) (bool, error)` no `PixTicketRepository` (só apaga ticket pending sem mensagens). Compor na `Repository`.
- `backend/internal/database/pixqueue.go`: implementar os métodos (usar `db.mu` lock como os demais).
- Ajustar query de `ListPixTickets` para expor por ticket: `paidTotalCents` (SUM de `media_value_cents` ≥ 0 das mensagens inbound image/document) e `hasInboundText` (EXISTS) — novos campos em `model.PixTicket` (`paidTotalCents int64 \`json:"paidTotalCents"\``, `hasInboundText bool \`json:"hasInboundText"\``).
- Atualizar fakes de repositório usados nos testes: `backend/cmd/sseload/main.go`, `backend/internal/controller/pixqueue_test.go`, `backend/internal/view/*_test.go`.

### 4. Controller (`PixQueueService`)

- Novo campo/config: carregar valores aceitos do dono a cada comprovante (`repo.ListPixValueRules`) — volume baixo, sem cache por enquanto.
- `handleReceipt`, após download + `DetectReceipt`, com gate ativo:
  1. Extrair texto (PDF via `receipt.ExtractTextPDF`, JPG via `ExtractTextJPEG`) e valores.
  2. **Ilegível** → não armazenar; `storeDelete`; auto-resposta de valor ilegível via WAHA (sem gravar mensagem no banco); `DeleteEmptyPixTicket` se o ticket não tem mensagens; return sem emit.
  3. **Parcial** (`newSum` não atinge nenhum aceito e `newSum` ≤ maior aceito) → armazenar mensagem com `media_value_cents` do comprovante; **não** emitir SSE; **não** marcar ticket visível; enviar auto-resposta "Recebemos R$ X,XX. Falta R$ Y,YY para completar R$ Z,ZZ. Envie um comprovante nesse valor." (gravada como mensagem outbound para constar quando o ticket abrir); return.
  4. **Completo/over** → fluxo atual (store + `MarkPixTicketReceipt` + emits), com `media_value_cents` preenchido.
- `ListTickets` (pending): com gate ativo, filtrar para fora tickets sem texto inbound e com `paidTotalCents` fora dos aceitos/over. Histórico sem filtro.
- Novos métodos `ListPixValues(owner)` / `ReplacePixValues(owner, cents)` para a view.
- Constantes de mensagem de auto-resposta em pt-BR junto de `AutoReplyUnsupported`.

### 5. API (view)

- `backend/internal/view/handlers_pix.go`:
  - `GET /api/pix/values` → `{"values":[1500,1000]}` (cents).
  - `PUT /api/pix/values` body `{"values":["15,00","10,00"]}` → parse pt-BR no servidor (mesma regra: `R$` opcional, vírgula decimal, ponto de milhar), validar teto/limites, dedup, replace-all.
- Registrar rotas no router (`backend/internal/view/server.go`, junto das demais em `/api/pix/*` ~linhas 99–106), autenticadas via `pixUser`.
- Expor `mediaValueCents` no JSON de `model.PixMessage` (`json:"mediaValueCents,omitempty"`).

### 6. Frontend

- `frontend/index.html` (card WhatsApp, antes de `pixTicketsWrap` ~linha 1578): bloco "Valores PIX aceitos" com linhas dinâmicas `<input type="text" inputmode="decimal" placeholder="Ex.: 15,00">`, botão remover por linha, "+ Adicionar valor" e "Salvar valores" (small-btn), hint "Comprovantes só entram na fila quando a soma atingir um destes valores."
- `frontend/pix.js`:
  - `parseMoneyBR(str) → cents|null` e `formatBRL(cents) → "R$ 15,00"` (funções puras exportadas p/ teste).
  - Carregar valores no init, salvar no PUT, re-render das linhas, validação client-side (mensagens de erro simples).
  - Na conversa, bolha de comprovante exibe "R$ X,XX" (campo `mediaValueCents` do `PixMessage` já exposto via JSON) para o operador conferir a soma.
- Testes em `frontend/pix-queue.test.mjs` (parse/format/render).

### 7. Testes e validação

- Go (novos):
  - `backend/internal/receipt/*_test.go`: extração pt-BR ("R$ 15,00", "1.234,56", "10", datas/telefones não contam), regras `Decide` (partial/complete/over), PDF de fixture com texto; OCR de JPG com skip se `tesseract` não estiver instalado (`exec.LookPath`).
  - `backend/internal/controller/pixqueue_test.go`: gate off (fluxo atual), parcial → sem SSE + auto-resposta com "falta", soma fecha → emit + visível, ilegível → reject + ticket vazio apagado, filtro de `ListTickets`.
- Rodar `go test ./internal/... -count=1` (o Dockerfile roda isso; os testes de banco exigem `TEST_DATABASE_URL` — localmente rodar ao menos os pacotes sem banco: `go test ./internal/controller/... ./internal/receipt/... ./internal/media/... ./internal/view/... -count=1`).
- Frontend: `npm test` em `frontend/` (roda `node --test *.test.mjs`).
- Manual (docker compose): `docker compose up -d --build postgres waha minio backend frontend`; configurar 10,00/15,00; do WhatsApp mandar comprovante de 8,00 (esperado: resposta "falta R$ 2,00" e nada na fila), depois de 2,00 (esperado: entra na fila com 📎); mandar comprovante de 6,00 (esperado: resposta "falta R$ 4,00" p/ 10,00); mandar comprovante de 16,00 (esperado: entra na fila, pagou a mais); limpar valores e confirmar que todo comprovante volta a entrar.

## Riscos

- OCR de screenshot ruim → falso negativo; mitigação: mensagem orienta reenviar em PDF (PDFs de banco têm camada de texto) e o cliente pode reenviar.
- PDF escaneado (sem texto) → tratado como ilegível (fail-closed com aviso ao cliente); não faremos rasterização/OCR de PDF neste escopo.
- Falso positivo de regex de moeda → mitigado por exigir `R$` ou decimais; ajustável nos testes.
- Imagem do backend cresce ~120 MB (impacto no Raspberry Pi 4).

## Fora de escopo

- Tela/painel para ver comprovantes parciais ocultos e aprovação manual.
- Acúmulo entre tickets diferentes (a soma é por ticket/contato).
- OCR de PNG/vídeo/áudio (continuam `unsupported` como hoje).
