# Plano — Remoção da cobrança (produto sem remuneração)

Status: **executado** (ver §8 Execução).
Escopo: remover todo o dinheiro do produto — preço, mensalidade de vagas, vencimento de
assinatura, cobrança no cadastro e oferta de preço na landing — **sem descadastrar
nenhum cliente existente e sem apagar dados**.

Decisão central: **remoção suave em código, preservação total de dados**. Nenhuma tabela,
coluna, linha de `settings` ou log de auditoria é apagada nesta entrega. O código deixa de
ler e escrever a cobrança; a limpeza física (DROP) fica para uma segunda entrega, opcional,
depois de um período de observação.

---

## 1. Inventário do que é "cobrança" hoje

Existem **três blocos independentes** de cobrança no código. Só é seguro removê-los
sabendo que são gates de acesso diferentes.

### Bloco A — Mensalidade de vagas extras (organização)

Fonte de verdade: `organization_seat_subscriptions` + `organization_seat_payments` +
`settings['billing:seat_price_cents']`.

| Onde | O quê |
| --- | --- |
| `backend/internal/teams/store.go:38-51` | `Summary.PriceCents`, `Summary.ExpiresAt`, `Summary.Extra` |
| `backend/internal/teams/store.go:77-107` | `lockOrg` lê a assinatura vigente (`quantity`, `expires_at`) |
| `backend/internal/teams/store.go:109` | `expireInvites` expira convite quando não há período pago cobrindo a vaga |
| `backend/internal/teams/store.go:113-121` | `Store.Access` — **gate de acesso**: nega sessão/SSE de quem ocupa vaga extra vencida (`ErrSuspended`) |
| `backend/internal/teams/store.go:123-172` | `Summary` monta `extra`, `expiresAt`, `priceCents` e marca `seat.Suspended` |
| `backend/internal/teams/store.go:178-215` | `CreateInvitation` limita capacidade a `2 + extra` pago (`ErrCapacity`) |
| `backend/internal/teams/store.go:340-355` | `AcceptInvitation` recusa vaga `>2` sem período pago |
| `backend/internal/teams/administration.go:61-71` | `Allocate` recusa número de vaga `> 2+extra` |
| `backend/internal/teams/administration.go:143-170` | `SetPrice` (`billing:seat_price_cents`) |
| `backend/internal/teams/administration.go:172-190` | `AddCalendarMonth` (vencimento em São Paulo) |
| `backend/internal/teams/administration.go:191-277` | `RecordPayment` (ledger + período, dedupe por `reference`, overlap) |
| `backend/internal/teams/administration.go:278-300` | `Payments` (extrato) |
| `backend/internal/view/team_handlers.go:21,206-227` | rota `POST /api/admin/billing/price` + `handleSeatPrice` |
| `backend/internal/view/team_handlers.go:323-352` | ação `seat-subscription` em `/api/admin/orgs/{id}/...` |
| `backend/internal/view/team_handlers.go:26-33` | `writeTeamError` mapeia `ErrSuspended` (403) e `ErrCapacity` (409) |
| `backend/internal/view/tenant.go:143-146` | `teams.Access` no `tenantMiddleware` → 403 `seat_suspended` |
| `backend/internal/view/sse.go:179` | `teams.Access` no `authorize` do SSE |
| `backend/internal/view/auth_handlers.go:458-465` | sessão devolve `orgError: seat_suspended` / `seatStatus` |
| `backend/internal/view/auth_maintenance.go:23` | worker expira convites conforme assinatura |
| `backend/internal/database/local_identity.go:28-32,39-42,52` | tabelas `organization_seat_subscriptions` / `organization_seat_payments` |
| `frontend/teams-ui.js:2-3,16-18,57-73` | `money()`, `summaryText` com "R$/vaga/mês · vencimento", formulário de preço, formulário "Confirmar pagamento / liberar período", "Períodos registrados" |
| `frontend/admin.js:321` | botão "Equipe e mensalidade" |
| `frontend/auth.js:25` | mensagem `seat_suspended` |
| `backend/internal/teams/store_test.go:148-260` | teste de preço, pagamento, expiração e renovação |

### Bloco B — Validade de assinatura por usuário (legado pré-multi-tenant)

Fonte: coluna `users.subscription_expires_at`. É um gate **de login**, independente do
bloco A. Clientes antigos ainda têm data preenchida; **algumas dessas datas podem estar
no passado** e é isso que a remoção resolve.

| Onde | O quê |
| --- | --- |
| `backend/internal/database/local_identity.go:18` | coluna `subscription_expires_at` |
| `backend/internal/auth/local_store.go:113` | `ValidateToken` exige `subscription_expires_at IS NULL OR > now()` (sessão morre no vencimento) |
| `backend/internal/auth/local_store.go:127-140` | `SignInWithPassword` recusa login vencido |
| `backend/internal/auth/local_accounts.go:19,29,48,54` | campo em `SubscriberProfile`, `CreateSubscriberRequest`, `UpdateSubscriberRequest`, `profileColumns` |
| `backend/internal/auth/local_accounts.go:106-116,143-151,168-171` | gravação do campo + revogação de sessões |
| `backend/internal/auth/import.go:56` | importador grava a data |
| `backend/internal/view/auth_handlers.go:483-485` | sessão devolve `subscriptionExpiresAt` |
| `frontend/admin.js:148,233,255-270` | coluna "Validade", input de validade, "+30 dias" ao aprovar |
| `frontend/renderer.js:4285,4324` | idem no painel legado |
| `backend/internal/auth/local_test.go:133,147` | teste `TestAdminLoginIgnoresExpiredSubscription` |

### Bloco C — Cobrança na comunicação e na landing

| Onde | O quê |
| --- | --- |
| `backend/internal/mail/mail.go:35-37,47,68,90-92` | config `PaymentLink`, `PixKey`, `Price` (default `20,00`) |
| `backend/internal/mail/mail.go:114-125` | `SendWelcome` |
| `backend/internal/mail/mail.go:269-307` | `paymentLine` + `buildWelcomeBody` ("é necessário realizar o pagamento de R$ ...") |
| `backend/internal/view/auth_handlers.go:196-233` | mensagens de cadastro "após a confirmação do pagamento" |
| `.env.example:75-78`, `.env.production.example:27-29` | `PAYMENT_PIX_KEY`, `PAYMENT_LINK`, `PAYMENT_PRICE` |
| `frontend/landing.html:19,52` | banner de inauguração com "R$ 20" + card de preço + "liberação após confirmação do pagamento" |
| `frontend/landing-offer.js` | contador da oferta de preço |
| `frontend/admin.html:143-152,148` | painel "Fila de pagamento", input de validade, coluna "Validade" |
| `frontend/login.html:137`, `frontend/login-page.js:99` | "senha da sua assinatura" |
| `backend/internal/mail/mail_test.go` | testes com "R$ 25,00"/"R$ 20,00" |
| `docs/auth-local.md:63-88`, `AGENTS.md` (bullets de Equipes/vagas) | documentação |

### O que NÃO é cobrança (não tocar)

- **Fila PIX** (`pix_*`): fluxo de doações/transferências dos espectadores da live. É
  funcionalidade do produto, não receita da plataforma.
- `organizations.max_lives` e `MAX_MONITORS`: limites de capacidade técnica, não preço.
- Papéis `owner`/`operator`, convites, alocação de vagas, contas TikTok autorizadas:
  continuam existindo — perdem apenas o preço e o vencimento.
- Tabelas/colunas de cobrança no banco: **preservadas** (ver §3).

---

## 2. Decisões de produto

**Decidido em 2025 (dono do produto):** D1 = vagas ilimitadas. Demais itens seguem como
padrão proposto até revisão na execução.

| # | Tema | Decisão | Alternativa descartada |
| --- | --- | --- | --- |
| D1 | Vagas por organização | **DECIDIDO: ilimitadas** — remover `ErrCapacity` e o cálculo `2+extra`; o único teto volta a ser `max_lives`/`MAX_MONITORS` | Manter 2 vagas incluídas + extras liberadas manualmente pelo admin, sem preço |
| D2 | Gate de acesso por vaga | **Removido**: `Store.Access` deixa de existir; acesso = vínculo ativo em organização ativa (coerente com D1) | Manter `Access` só checando vínculo (mesmo efeito, menos código removido) |
| D3 | Aprovação de cadastro | **Mantida** (conta nasce `active=false`, admin aprova) — é o que garante atribuição de organização | Cadastro já ativo, organização escolhida depois |
| D4 | `subscription_expires_at` | Parar de ler/escrever; **coluna permanece** | Dropar a coluna (só depois de backup + decisão explícita) |
| D5 | Landing | Remover preço/oferta; manter CTA de contato/avaliação | Manter página como está |
| D6 | `team_rules_enabled` / "regularização" | Manter (organização só convida com dono principal definido), mas sem linguagem de "regularize vagas pagas" | Remover a exigência |
| D7 | Preço na UI | Remover toda exibição de R$ de mensalidade (Fila PIX continua com R$ do ticket) | — |

---

## 3. Estratégia de migração de dados (clientes existentes)

1. **Nada é apagado.** `organization_seat_payments`, `organization_seat_subscriptions`,
   `users.subscription_expires_at` e `settings['billing:seat_price_cents']` continuam no
   banco, apenas órfãos.
2. **Ids de migração preservados.** `migrateLocalIdentity()` continua criando as tabelas
   `IF NOT EXISTS` (código morto porém idempotente) — remover o `CREATE TABLE` não apaga
   nada, mas manter evita divergência entre bancos novos e o de produção. Recomendado:
   **manter os `CREATE TABLE`** e apenas anotar como legado.
3. **Ninguém fica bloqueado.** Toda conta hoje suspensa por vencimento (bloco B) ou por
   vaga extra vencida (bloco A) passa a acessar normalmente após o deploy — é o objetivo.
4. **Trilha de auditoria**: linhas `organization_audit_logs` com ações `billing.price` e
   `billing.recorded` permanecem como histórico; o código deixa de gravá-las.
5. **Registro do estado pré-deploy** (para fins de conferência/contábeis):
   ```sh
   # no VPS, antes do deploy, com backup recente do volume postgres-data
   docker compose -f docker-compose.production.yml exec -T postgres \
     psql -U tlm_app -d "$POSTGRES_DB" -c "\copy (SELECT org_id,quantity,starts_at,expires_at FROM organization_seat_subscriptions ORDER BY org_id,starts_at) TO STDOUT CSV HEADER" \
     > backups/seat-subscriptions-$(date +%F).csv
   docker compose -f docker-compose.production.yml exec -T postgres \
     psql -U tlm_app -d "$POSTGRES_DB" -c "\copy (SELECT id,email,active,subscription_expires_at FROM users ORDER BY email) TO STDOUT CSV HEADER" \
     > backups/users-expiry-$(date +%F).csv
   ```
6. **Limpeza física (entrega 2, opcional, ≥30 dias depois)**: PR separado com
   `ALTER TABLE users DROP COLUMN subscription_expires_at`, `DROP TABLE
   organization_seat_payments, organization_seat_subscriptions`, `DELETE FROM settings
   WHERE key='billing:seat_price_cents'`. Irreversível: exige backup verificado e decisão
   explícita do dono do produto.

---

## 4. Fases de implementação

### Fase 1 — Equipe sem cobrança (backend)

1. `backend/internal/teams/store.go`
   - Remover `PriceCents`, `ExpiresAt` e `Extra` de `Summary` (D1 = ilimitado); a contagem
     de vagas passa a ser derivada de `organization_seats`.
   - `lockOrg`: manter `primary_owner_user_id`, `team_rules_enabled`, `active`; remover a
     query de `organization_seat_subscriptions` e o cálculo de `Extra/Expires`.
   - `expireInvites`: manter só `expires_at<=now()` e a condição de dono/regularização.
   - **Remover `Store.Access`** e as constantes `ErrSuspended`/`ErrCapacity` (ajustar
     chamadores: `tenant.go`, `sse.go`, `auth_handlers.go`).
   - `Summary`: `Seat.Suspended` deixa de existir; `Seat.Included` pode ser mantido por
     compatibilidade visual ou removido junto com o frontend.
   - `CreateInvitation`: sem teto de capacidade (D1); cria a vaga nova automaticamente
     quando não houver `organization_seats` livre.
   - `AcceptInvitation`: remover o bloco `if seat > 2 && (...)`.
2. `backend/internal/teams/administration.go`
   - Remover `SetPrice`, `AddCalendarMonth`, `RecordPayment`, `Payments` e a struct
     `Payment` (esta em `store.go`).
   - `Allocate`: remover o uso de `o.Extra` na validação do número da vaga
     (`seat.Number < 1` + duplicidade continuam).
3. `backend/internal/view/team_handlers.go`
   - Remover `"/api/admin/billing/price"` do mux e `handleSeatPrice`.
   - Remover o `case "seat-subscription"` de `handleTeamAdministration`.
   - `writeTeamError`: remover os `case` de `ErrSuspended`/`ErrCapacity`.
4. `backend/internal/view/tenant.go`
   - Remover o bloco `if !tenantExempt(...) && !t.PlatformAdmin && s.teams != nil { Access }`.
   - `writeJSONErrorCode` fica sem uso → remover junto (única chamada é essa).
5. `backend/internal/view/sse.go:179` — `authorize` passa a depender só de
   `ValidateToken` + `tenants.resolve` (vínculo ativo).
6. `backend/internal/view/auth_handlers.go` — remover `orgError: seat_suspended` e
   `seatStatus` da resposta de sessão.
7. `backend/internal/view/auth_maintenance.go:23` — simplificar o `UPDATE ... expired`
   para apenas `expires_at<=now()`.
8. Testes: reescrever `teams/store_test.go:148-260` como
   `TestSeatAllocationExpiryAndRevocation` (sem preço/pagamento; cobre convite, alocação,
   vaga livre, revogação). Remover `/api/admin/billing/price` de
   `view/hardening_test.go:39`.

### Fase 2 — Fim da validade de assinatura por usuário

1. `backend/internal/auth/local_store.go` — remover a condição
   `(u.role='admin' OR u.subscription_expires_at IS NULL OR ...)` de `ValidateToken` e o
   cálculo `expired` de `SignInWithPassword` (mantém `!active`).
2. `backend/internal/auth/local_accounts.go` — remover `SubscriptionExpiresAt` das structs
   e do SQL (`profileColumns`, INSERT, UPDATE); `scanProfile` deixa de ler a coluna.
3. `backend/internal/auth/import.go:56` — remover a coluna do INSERT (JSON de importação
   continua aceito e ignorado, para não quebrar arquivos de migração antigos).
4. `backend/internal/view/auth_handlers.go:483-485` — remover `subscriptionExpiresAt`.
5. `backend/internal/auth/local_test.go:127-160` — substituir
   `TestAdminLoginIgnoresExpiredSubscription` por teste de suspensão por `active=false`
   (o comportamento que sobra).

### Fase 3 — Frontend

1. `frontend/teams-ui.js`
   - Remover `money()`; `summaryText` passa a "X de Y vagas ocupadas · N convites
     pendentes"; remover formulário de preço, formulário de pagamento/liberação e a lista
     "Períodos registrados"; título do painel "Equipe e mensalidade" → "Equipe".
   - Manter alocação, convites e contas TikTok autorizadas.
2. `frontend/admin.js`
   - `:321` rótulo do botão → "Equipe".
   - Remover coluna "Validade" (`:148`), input `subscriberExpires` (`:233`), o "+30 dias"
     de `toggleUser` (`:255-270`) e as mensagens "Aprovar pagamento"/"aguardando
     confirmação de pagamento" → "Aprovar"/"aguardando aprovação".
3. `frontend/admin.html` — remover `<input id="subscriberExpires">` (`:148`), o `<th>Validade</th>`,
   ajustar `colspan`, e renomear o painel "Fila de pagamento" → "Cadastros pendentes".
4. `frontend/renderer.js:4270-4330` — mesmo tratamento no painel legado de assinantes
   (coluna Validade, `body.subscriptionExpiresAt`).
5. `frontend/auth.js:25` — remover o mapeamento `seat_suspended`.
6. `frontend/login.html:137`, `frontend/login-page.js:99` — "senha da sua assinatura" →
   "senha da sua conta".
7. `frontend/landing.html:19,52` + `frontend/landing-offer.js` — remover banner de oferta e
   bloco de preço; CTA vira "Solicitar avaliação do meu canal". Remover
   `landing-offer.js` da lista de cópias do `frontend/build.mjs` se o arquivo for apagado.
8. Rebuild: `cd frontend && npm run build` (o `dist/` não é versionado; a imagem Docker
   builda em `frontend/Dockerfile`).

### Fase 4 — E-mail e configuração

1. `backend/internal/mail/mail.go` — remover `PixKey`, `PaymentLink`, `Price`,
   `defaultPrice`, `paymentLine` e reescrever `buildWelcomeBody` (boas-vindas sem
   pagamento: apenas confirmação de cadastro e aviso de que o administrador libera o
   acesso). Remover o `log` de placeholder em `SendWelcome`.
2. `backend/internal/mail/mail_test.go` — ajustar os casos de "R$ 25,00"/"R$ 20,00".
3. `.env.example:75-78` e `.env.production.example:27-29` — remover as três variáveis
   (documentar no `PRODUCAO.md`/`.env.example` que são ignoradas).

### Fase 5 — Documentação

1. `docs/auth-local.md` — apagar a seção "Mensalidades"; reescrever "Equipes e lives" sem
   vagas pagas/vencimento; ajustar o parágrafo inicial ("cobrança de vagas" sai).
2. `AGENTS.md` — reescrever o bullet "Equipes: dono principal + 2 vagas incluídas ...
   extras têm mensalidade manual e vencimento".
3. `docs/plano-revisao-seguranca.md:125` — atualizar a menção a "vagas/limites".
4. Este arquivo: preencher uma seção "Execução" com data, commit e resultado.

---

## 5. Ordem de deploy (produção com clientes)

1. `git switch -c feat/remover-cobranca` e commit por fase (backend, frontend, docs).
2. Backup do `postgres-data` + CSVs do §3.5.
3. `go build ./... && go vet ./...`; `go test -race ./...` com `TEST_DATABASE_URL`
   descartável; `cd frontend && npm test && npm run build`.
4. Deploy **backend + frontend juntos** (o frontend deixa de chamar
   `/api/admin/billing/price`; se o backend subir antes, nada quebra — a rota some e só a
   aba antiga falharia; se o frontend subir antes, idealmente também não, mas
   backend-first é a ordem segura).
5. Smoke pós-deploy (crítico, é o que valida os clientes atuais):
   - login de uma conta cujo `subscription_expires_at` está no passado → deve entrar;
   - login de um membro de organização com vaga extra vencida → deve entrar;
   - SSE conectando para esse membro (`/events`);
   - convite de um 4º membro em uma organização → deve concluir sem erro de vaga;
   - alocação de vagas (`POST /api/org/seats`) e reenvio/cancelamento de convite;
   - `/api/state`, Fila PIX e monitor de live seguem normais;
   - sessão (`/api/auth/session`) sem `seatStatus`/`subscriptionExpiresAt`.
6. Verificação de código morto:
   ```sh
   grep -rniE "billing|mensalidade|subscription|seat_suspended|PAYMENT_" \
     backend frontend docs .env.example .env.production.example \
     --include='*.go' --include='*.js' --include='*.html' --include='*.md' | grep -v dist/
   ```
   Resultado esperado: só `organization_seat_subscriptions` em migração legada (se mantida),
   histórico de auditoria e este documento.
7. Rollback: reverter a imagem anterior. Nada foi apagado no banco, então o código antigo
   volta a funcionar (períodos continuam lá; quem estava vencido volta a ficar suspenso —
   por isso o deploy deve ser feito já com a decisão de produto fechada).

---

## 6. Riscos e mitigação

| Risco | Mitigação |
| --- | --- |
| Cliente sai do ar por engano ao remover `Access` | Deploy backend-first com smoke imediato no login + SSE; rollback por imagem |
| Perda de histórico de pagamentos | Nada é dropado; CSVs exportados antes |
| Banco novo de dev divergir do de produção | Manter os `CREATE TABLE IF NOT EXISTS` das tabelas legadas |
| Frontend antigo em cache chamando rotas removidas | `admin.html`/`teams-ui.js` servidos com a nova build; chamadas removidas retornam 404 apenas em telas administrativas — sem efeito para clientes |
| Importação de identidades antigas quebrar | `import.go` ignora o campo do JSON em vez de rejeitar |
| Testes de integração acoplados ao preço | Fase 1 e 2 atualizam `store_test.go`, `local_test.go`, `hardening_test.go`, `mail_test.go` no mesmo commit |
| Esquecimento de mensagem "pagamento" em tela | Grep do §5.6 como critério de aceite |

## 7. Critérios de aceite

1. Nenhuma rota, função ou campo de preço/vencimento/assinatura remanescente (grep do §5.6).
2. `go build ./...`, `go vet ./...`, `go test -race ./...`, `npm test` e `npm run build` verdes.
3. Conta com `subscription_expires_at` no passado autentica e opera normalmente.
4. Organização com vaga extra vencida mantém todos os membros com acesso; convites novos
   funcionam independentemente de período pago.
5. Fila PIX, monitor de live, metas, ranking, relatórios e multi-tenant intactos.
6. Nenhum `DROP`/`DELETE` de dados de cobrança nesta entrega.

---

## 8. Execução

- **Data:** 2026-07-19 · **Branch:** `feat/remover-cobranca` (base `lite-sem-ia`)
- **Fases 1–5:** implementadas conforme §4.
  - Fase 1: `store.go`/`administration.go`/`team_handlers.go`/`tenant.go`/`sse.go`/`auth_handlers.go`/`auth_maintenance.go` sem `Access`, `ErrSuspended`, `ErrCapacity`, `SetPrice`, `RecordPayment`, `Payments`, `seat-subscription` e `billing/price`. `CreateInvitation` cria vaga nova quando não há vaga livre; `Summary` e `Seat` sem preço/expiração/`extra`/`suspended`.
  - Fase 2: `subscription_expires_at` deixado apenas como coluna legada; `ValidateToken`/`SignInWithPassword` sem o filtro; perfis/criação/edição sem o campo; importador ignora a data.
  - Fase 3: `teams-ui.js` sem dinheiro (apenas alocação/convites/contas TikTok); `admin.js`/`admin.html` sem "Fila de pagamento"/"Validade"/"+30 dias" (agora "Cadastros pendentes"/"Aprovar"); `renderer.js` sem coluna de validade; `auth.js` sem `seat_suspended`; `login.html`/`login-page.js` sem "assinatura"; `landing.html` sem banner/card de preço (CTA "Solicitar avaliação do meu canal"), `landing-offer.js` removido e `build.mjs` ajustado.
  - Fase 4: `mail.go` sem `PixKey`/`PaymentLink`/`Price` (boas-vindas sem pagamento); `mail_test.go` sem casos de R$; `.env.example`/`.env.production.example` sem `PAYMENT_*` (notado como ignorado).
  - Fase 5: `docs/auth-local.md` (sem "Mensalidades"), `AGENTS.md` (Equipes sem cobrança), `docs/plano-revisao-seguranca.md` (vagas/limites → limites de capacidade).
- **Verificação:** `go build`, `go vet`, `go test -race` (TEST_DATABASE_URL descartável) verdes; `npm test` no frontend com as mesmas 5 falhas pré-existentes de `reset-password.test.mjs` do base (nenhuma falha nova); `npm run build` OK e `dist/` sem `landing-offer.js`; grep de §5.6 retorna apenas os itens legados esperados (tabelas/coluna legadas + comentários/notas documentando a preservação).
- **Dados preservados:** nenhuma tabela, coluna, linha de `settings` ou log de auditoria removido do banco. Limpeza física segue como §3.6 (entrega 2, opcional).
- **Deploy:** pendente — seguir §5 (backup + CSVs antes, backend+frontend juntos, smoke pós-deploy).
