# Fila PIX (WhatsApp/WAHA + MinIO)

Fila de atendimento por WhatsApp: cada **organização** pareia o próprio número
via QR code (WAHA), recebe mensagens e comprovantes PIX (JPG/PDF) armazenados no
MinIO, responde somente texto e marca o atendimento como respondido. A fila, o
número pareado e os valores aceitos são compartilhados pelos membros da
organização e invisíveis para as demais (eventos SSE só vão para a org dona).

## Componentes

- **WAHA** (`devlikeapro/waha`): gateway WhatsApp (Web multi-device). Pairing por QR.
- **MinIO**: bucket `tlm-pix-media` com os comprovantes. Nunca é exposto ao navegador.
- **Backend Go**: cliente WAHA, webhook público com HMAC-SHA512, regras da fila e
  endpoint autenticado de mídia. Frontend nunca fala com WAHA/MinIO.

## Como funciona

1. O **dono da organização** clica em **Conectar WhatsApp** (conectar, QR e
   desconectar são exclusivos do dono — `403` para operador; operadores só
   atendem a fila: tickets, mensagens, valores e histórico) → backend cria a sessão `pix_<uuid da org>`
   no WAHA (sessões antigas, criadas por usuário, mantêm o nome gravado).
2. QR é exibido e o status é consultado a cada 3s até `connected`.
3. Cliente manda texto e comprovantes para o número pareado. O webhook do WAHA chega
   em `POST /api/webhooks/whatsapp` (assinado com `X-Webhook-Hmac`).
4. Texto entra na conversa. JPG/PDF é baixado do WAHA, validado por magic bytes
   (`FF D8 FF` / `%PDF-`), enviado ao MinIO e vinculado à mensagem.
5. Tipos não suportados (áudio, vídeo, sticker, localização, PNG) viram `unsupported`
   e recebem uma resposta automática única por atendimento.
6. O operador abre o atendimento pendente, responde **somente texto** e clica em
   **Marcar como Respondido**. O número passa para o histórico.

## Filtro de valores (comprovante → fila)

O operador configura **valores PIX aceitos** na UI (card WhatsApp → "Valores PIX
aceitos"; `GET|PUT /api/pix/values`, máx. 20 valores, R$ 0,01–R$ 9.999.999,99).
Com pelo menos um valor configurado, cada comprovante passa por um filtro:

1. O backend extrai texto do arquivo: PDF pela camada de texto
   (`github.com/ledongthuc/pdf`), JPG por OCR (`tesseract -l por`, chamado via
   `exec` — o binário Go continua `CGO_ENABLED=0`).
2. Os valores monetários em pt-BR (`R$ 15,00`, `1.234,56`) viram cents; o maior
   é tratado como valor principal do comprovante.
3. A soma dos comprovantes do ticket é comparada com os valores aceitos
   (**match exato**, `receipt.Decide`):
   - **igualou** um valor (ou passou do maior → pagou a mais) → o ticket
     aparece na fila;
   - **parcial** → o comprovante fica guardado (acumula) mas o ticket **não**
     aparece; o cliente recebe "Recebemos R$ X. Falta R$ Y para completar R$ Z."
     Ex.: aceito R$ 10,00, chegou R$ 8,00 → avisa falta R$ 2,00; ao chegar o
     comprovante de R$ 2,00, a soma fecha e o ticket entra na fila.
   - **valor ilegível** (screenshot ruim / PDF escaneado) → comprovante
     descartado, ticket vazio apagado, cliente é avisado para reenviar.
4. Sem valores configurados, o filtro fica **desligado** (todo comprovante entra,
   comportamento antigo). Falha de infraestrutura (tesseract ausente) é
   **fail-open**: o comprovante entra normalmente.
5. Tickets com mensagem de texto do cliente continuam visíveis; a soma
   acumulada fica em `pix_messages.media_value_cents` e aparece na conversa
   como etiqueta `R$ X,XX` no comprovante.

Schema: tabela `pix_value_rules` (valores em cents por organização) e coluna
`media_value_cents` em `pix_messages` — criadas por `migratePostgres()` no boot
.

## Regra de expurgo

Os comprovantes são apagados quando o usuário:

- desconecta a live (`POST /api/disconnect` / botão Desconectar), ou
- fecha a página (`pagehide` → `POST /api/monitoring/beacon-disconnect`).

O expurgo é **por organização**: acontece quando o último membro da organização
deixa de acompanhar lives. A saída de um membro enquanto outro ainda monitora não
apaga nada, e organizações diferentes nunca afetam as mídias umas das outras.
Recarregar a página não dispara o expurgo.

Depois do expurgo o arquivo some do MinIO, mas mensagens, metadados e histórico
permanecem no Postgres. A mídia passa a responder `410 Gone` e a UI mostra
"Comprovante removido ao desconectar a live".

## Configuração

Variáveis no `.env` (ver `.env.example`):

| Variável | Uso |
|---|---|
| `WAHA_ENABLED` | `0` desliga a Fila PIX sem derrubar o monitor de lives (default `0` em produção) |
| `WAHA_URL` | `http://waha:3000` dentro do compose |
| `WAHA_API_KEY` | chave global do WAHA (`X-Api-Key`) |
| `WAHA_WEBHOOK_SECRET` | segredo HMAC-SHA512 (`WHATSAPP_HOOK_HMAC_KEY` no WAHA) |
| `WAHA_IMAGE` | imagem WAHA conforme a arquitetura (arm/amd64); produção usa `gows-*` (sem Chromium) |
| `WAHA_ENGINE` | engine do WAHA em produção (default `GOWS`; `WEBJS` exige a imagem com Chromium) |
| `MINIO_ENDPOINT` | `minio:9000` (MinIO próprio) ou `host:9000` do MinIO externo |
| `MINIO_EXTERNAL_NETWORK` | rede Docker do MinIO externo (só com `deploy/compose.minio-externo.yml`) |
| `MINIO_ACCESS_KEY` / `MINIO_SECRET_KEY` | credenciais do bucket |
| `MINIO_BUCKET` | default `tlm-pix-media` |
| `MINIO_USE_SSL` | `0` na rede interna |
| `MINIO_IMAGE` | default `bitnamilegacy/minio:2025.7.23` (o repositório `minio/minio` saiu do Docker Hub) |
| `PIX_MEDIA_MAX_BYTES` | limite de download do comprovante (default 16 MB) |
| `TESSERACT_BIN` | binário de OCR dos JPGs (default `tesseract`, já na imagem) |

Sem essas variáveis o backend sobe normalmente e loga
`Fila PIX: desabilitada`; as rotas respondem `503`.

## Banco

As tabelas ficam no PostgreSQL do compose e são criadas no boot do backend
(`migratePostgres`), com RLS default deny nas 4 tabelas `pix_*`. Não há passo
manual de migração. `org_id` é `TEXT` (id de `organizations`). Bancos criados antes
do multi-tenant tinham `owner_user_id` (usuário): o boot renomeia a coluna e
converte cada valor para a organização do usuário. Todo usuário sem
organização ganha uma própria antes (1 usuário = 1 organização, ele como dono),
tudo numa transação; linhas sem dono vão para a organização de legado, que não
tem membros e só o admin da plataforma acessa —
ver `backend/internal/database/organizations.go` e `docs/auth-local.md`.

O filtro de valores adiciona a tabela `pix_value_rules` e a coluna
`pix_messages.media_value_cents` — também no boot.

Em produção WAHA e MinIO não publicam portas (só rede interna do compose). Os
volumes `waha_sessions` (pareamento) e `minio_data` (comprovantes) não entram no
`pg_dump` — ver backups em `PRODUCAO.md`.

## Endpoints

Autenticados:

- `POST /api/pix/whatsapp/connect` · `GET /api/pix/whatsapp/qr` ·
  `GET /api/pix/whatsapp/status` · `POST /api/pix/whatsapp/disconnect`
- `GET|PUT /api/pix/values` (valores PIX aceitos; strings pt-BR no PUT, cents no GET)
- `GET /api/pix/tickets?status=pending|answered&limit=`
- `GET|POST /api/pix/tickets/{id}/messages`
- `POST /api/pix/tickets/{id}/answer`
- `GET /api/pix/contacts/{id}/history`
- `GET /api/pix/media/{messageId}` (`200`, `404` ou `410`)
- `POST /api/monitoring/attach` · `POST /api/monitoring/beacon-disconnect`

Público (assinado):

- `POST /api/webhooks/whatsapp` (`401` sem HMAC válido)

## Teste local

```bash
cp .env.example .env
docker compose up -d --build postgres waha minio backend frontend
# UI: http://localhost:${FRONTEND_PORT:-8080}
# WAHA: http://127.0.0.1:${WAHA_PORT:-3210} · console MinIO: http://127.0.0.1:${MINIO_CONSOLE_PORT:-9001}
```

Em produção a Fila PIX é opcional: `docker-compose.production.yml` sobe sem
WAHA e MinIO, que ficam nos profiles `pix` e `pix-minio`; o MinIO também pode
ser externo (`deploy/compose.minio-externo.yml`). Ver "Fila PIX" em `PRODUCAO.md`.

Eventos em tempo real: o SSE já existente publica `pix-ticket-update` e
`pix-new-message` (apenas IDs; conteúdo é buscado na API autenticada).