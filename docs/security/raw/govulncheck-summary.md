# Fase 1 — govulncheck (triagem e veredito)

**Ferramenta:** `govulncheck v1.8.0` (via `go run`), banco de vulnerabilidades de **2026-10-01T20:24Z**
**Alvo:** `backend/...` com o toolchain local **go1.26.2 darwin/arm64**
**Resultado bruto:** 78 findings → 24 OSVs únicos → 42 pares OSV/pacote → **14 OSVs com função alcançável** no grafo de chamadas.
**Relatório bruto:** `docs/security/raw/govulncheck.json`

## 1. Veredito principal — a maior parte NÃO se aplica à produção

O `govulncheck` avalia a stdlib da **toolchain local**, não do binário implantado. O buildinfo autoritativo do binário em produção (`go version -m`, extraído read-only de `/app/tiktok-live-monitor` no container `tiktok-live-monitor-backend-1`) diz:

```
/tmp/tlm-backend-prod: go1.26.8
	path	github.com/thiagohmm/tiktok-live-monitor
	dep	golang.org/x/crypto	v0.55.0
	dep	golang.org/x/net	v0.58.0
	dep	github.com/jackc/pgx/v5	v5.10.0
	dep	github.com/ledongthuc/pdf	v0.0.0-20260907135840
	CGO_ENABLED=0  GOOS=linux  GOARCH=amd64
```

Como **todas** as 21 vulnerabilidades de stdlib encontradas são corrigidas no máximo em **1.26.6**, e a produção roda **1.26.8**, elas estão **REFRUTADAS para o ambiente implantado**. O container candidato `tlm-auth-local-candidate` também é `go1.26.8`.

Além disso, `html/template` **não é importado** em nenhum lugar do backend, o que elimina na raiz os OSVs de XSS em template (GO-2026-4980, GO-2026-4982) mesmo para builds antigos.

Módulos de terceiros: `pgx/v5 v5.10.0` e `ledongthuc/pdf` **sem OSV**; `x/net v0.58.0` acima de todos os "fixed in" relevantes.

## 2. O que resta de real

| ID | Sev | Achado | Situação |
|---|---|---|---|
| G-01 | **Média** | **Toolchain divergente entre dev e produção.** `backend/go.mod` declara `go 1.26.2` e o toolchain local é 1.26.2. Qualquer build fora do Dockerfile (dev, Raspberry, futuro CI) produz um binário com as **14 vulnerabilidades alcançáveis** listadas em §3. | Corrigir subindo `go.mod` para `go 1.26.8` (+ `toolchain go1.26.8`) e alinhando o toolchain local. |
| G-02 | **Média** | **Tag flutuante na imagem de build.** `backend/Dockerfile:17` usa `FROM golang:1.26-bookworm` sem pin de patch/digest. Hoje resolveu para 1.26.8 (por sorte, já corrigido), mas o mesmo Dockerfile pode futuramente resolver para uma versão vulnerável, e o build não é reprodutível. | Fixar `golang:1.26.8-bookworm` (ou por digest). |
| G-03 | **Baixa** | `golang.org/x/crypto v0.55.0` — GO-2026-6354 / GO-2026-6355: DoS em canais do `x/crypto/ssh` (corrigido em 0.56.0); GO-2026-5932: `openpgp` não mantido. | O `govulncheck` **não** encontrou alcance no grafo de chamadas (nem `ssh` nem `openpgp` são usados), então é risco residual de supply chain. Bump para `v0.56.0+` é trivial. |
| G-04 | **Informativo** | Base da imagem de runtime: `node:22-bookworm-slim` + `tesseract-ocr` + `tesseract-ocr-por` (o OCR é chamado via `exec`, mantendo `CGO_ENABLED=0`). | Superfície extra na imagem de runtime; varredura de CVE da imagem depende de `trivy` (bloqueado pelo Docker daemon local). |

## 3. As 14 vulnerabilidades alcançáveis que só afetam builds com toolchain < 1.26.6

Listadas como referência para o chamado G-01, todas com correção em `1.26.3`–`1.26.6`:

| OSV | Pacote | Funções alcançadas | Impacto |
|---|---|---|---|
| GO-2026-4918 | `net/http` (http2) | `Do`, `Get`, `RoundTrip`, `CloseIdleConnections` | Loop infinito com `SETTINGS_MAX_FRAME_SIZE` malicioso (DoS de saída) |
| GO-2026-5026 | `net/http` (idna) | idem | Não rejeita label Punycode ASCII-only |
| GO-2026-6089 | `net/http` | `ListenAndServe` | `ReadHeaderTimeout` não aplicado na checagem HTTP/2 sem TLS |
| GO-2026-5090 / GO-2026-6090 | `crypto/tls` | `Dial`, `HandshakeContext`, `Read`, `Write` | Handshake pós-handshake sem limite |
| GO-2026-5856 | `crypto/tls` | idem | Vazamento de privacidade em Encrypted Client Hello |
| GO-2026-5037 | `crypto/x509` | `Verify`, `VerifyHostname` | Parsing ineficiente de hostname (DoS) |
| GO-2026-5972 | `encoding/asn1` | `Unmarshal` | Sem limite de recursão |
| GO-2026-6088 | `encoding/xml` | `Decode`, `Unmarshal` | Sem guarda de recursão |
| GO-2026-6218 | `net/url` | `Parse` | Complexidade quadrática em `resolvePath` (DoS) |
| GO-2026-5038 | `mime` | `DecodeHeader` | Complexidade quadrática |
| GO-2026-5039 | `net/textproto` | `ReadMIMEHeader`, `ReadResponse` | Entrada arbitrária em erros sem escape (SMTP) |
| GO-2026-4977, GO-2026-4986 | `net/mail` | `ParseAddress` | Concatenação quadrática |
| GO-2026-4970 | `os` | — | Escape de raiz via symlink + barra final |
| GO-2026-4971 | `net` | `Dial`, `LookupHost` | Panic com byte NUL — **somente Windows**, irrelevante (Linux) |
