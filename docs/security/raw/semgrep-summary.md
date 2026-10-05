# Fase 1 — semgrep (triagem) + verificações empíricas na borda

**Ferramenta:** `semgrep` (via `uvx`, `--metrics=off`) com os rulesets `p/golang`, `p/owasp-top-ten`, `p/xss`, `p/secrets`
**Alvo:** `backend/` e `frontend/`
**Resultado:** 7 achados, **0 erros de análise**. Relatório bruto: `docs/security/raw/semgrep.json`

## 1. Triagem dos 7 achados do ruleset

| Regra | Local | Veredito |
|---|---|---|
| `cookie-missing-secure` | `view/auth_handlers.go:140` e `:368` | **FALSO POSITIVO.** O código é `Secure: strings.HasPrefix(s.auth.SiteURL, "https://") \|\| r.TLS != nil`. Em produção `docker-compose.production.yml:48` define `SITE_URL: https://${DOMAIN:-livemonitortk.com.br}` ⇒ **`Secure=true`**. O semgrep não avalia a expressão. |
| `missing-ssl-minversion` | `internal/mail/mail.go:208-211` e `:231-234` | **BAIXO / higiene.** O binário é Go 1.26.8; desde o Go 1.22 o mínimo padrão de `crypto/tls` já é TLS 1.2. Não há downgrade possível para TLS 1.0/1.1. Ainda assim, declarar `MinVersion: tls.VersionTLS12` explicitamente documenta a intenção. |
| `math-random-used` | `internal/monitor/bridge.go:11` | **FALSO POSITIVO.** Uso é jitter de reconexão (`rand.Float64()`), sem valor de segurança. |
| `missing-user-entrypoint` | `backend/Dockerfile:68` | **REAL.** Confere com o achado P7: nenhum `USER` declarado, container roda como root. |
| `request-host-used` | `frontend/nginx.conf:38` | **BAIXO.** Esse arquivo é usado apenas por `frontend/Dockerfile` (dev/compose local, `BACKEND_URL` via `envsubst`); **não** existe container de frontend em produção (a UI é estática servida pela borda). |

## 2. Achados novos descobertos durante a verificação do ruleset `$host` (confirmados na VPS)

O aviso de `$host` levou a inspecionar o `$host` da borda. Nenhum vhost declara `default_server`, portanto o **primeiro** bloco por porta vira o default — e `livemonitortk.conf` carrega antes de `sigmenta.conf`.

### V-01 (Média/Alta) — Open redirect por Host header forjado, verificado

`edge-nginx/nginx/conf.d/livemonitortk.conf:11` faz `return 301 https://$host$request_uri;` usando `$host`, que vem do cabeçalho controlado pelo cliente.

```console
$ curl -sSI -H "Host: evil.example" http://143.95.162.200/
HTTP/1.1 301 Moved Permanently
Server: nginx/1.27.5
Content-Length: 169
Location: https://evil.example/          ← host do atacante

$ curl -sSI -H "Host: livemonitortk.com.br" http://143.95.162.200/
Location: https://livemonitortk.com.br/  ← controle
```

Qualquer domínio apontado para o IP (ou o próprio IP) devolve um 301 para um destino arbitrário **escolhido pelo atacante**, aproveitando a reputação do VPS para phishing. Correção: trocar `$host` por `livemonitortk.com.br` literal, ou adicionar um `server { listen 80 default_server; return 444; }` explícito.

### V-02 (Baixa) — Vazamento de versão do nginx apenas no HTTP

O `server_tokens off` está **dentro** do bloco `listen 443`; o bloco da porta 80 não o tem:

```
http  → Server: nginx/1.27.5     ← versão exata exposta
https → server: nginx            ← correto
```

### V-03 (Baixa) — Host/SNI arbitrário serve a aplicação

`curl -k -H "Host: outro.example" https://143.95.162.200/` devolve **200** com o `index.html` completo da aplicação (75.120 bytes), porque o vhost do TikTok Monitor é o servidor default de :443. Não é escalada de privilégio, mas permite servir a UI sob domínio de terceiro (phishing/SEO) e revela que o certificado default do IP é o do produto.

## 3. Resumo

- **1 achado real** confirmado pelo ruleset (container root, já mapeado como P7).
- **3 falsos positivos** refutados com evidência (cookie `Secure`, `math/rand`, `$host` do dev server).
- **1 item de higiene** (TLS `MinVersion` explícito).
- **3 achados novos** verificados na borda (V-01 open redirect por Host, V-02 versão no HTTP, V-03 UI servida sob Host arbitrário).
- O ruleset de XSS (`p/xss`) **não** detectou nada no frontend vanilla — o que **não** significa ausência de XSS: `p/xss` é orientado a frameworks e não cobre sinks de `innerHTML` em JS puro. Essa análise depende da trilha manual L5.
