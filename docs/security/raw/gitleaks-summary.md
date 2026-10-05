# Fase 1 — gitleaks (triagem) + auditoria manual do histórico

**Ferramenta:** `gitleaks v8.30.1` via `go run github.com/zricethezav/gitleaks/v8@latest`
**Relatório bruto (git mode):** `docs/security/raw/gitleaks.json`

## 1. Resultado do scanner: 0 vazamentos — e isso é um FALSO NEGATIVO

O `gitleaks git --log-opts="--all"` retornou `[]` (zero achados). **Isso está errado**, e a auditoria manual prova:

```console
$ git log --all -G 'sk-vT8X5HWOY1UfOoxYtsPkrA' --oneline --name-only
4f0ecd4 pi vscode rewind snapshot   .kilo/plans/1788131684202-seguranca-remediacao.md
0402ed1 pacelc em system design     .kilo/plans/1788131684202-seguranca-remediacao.md
4991b3a delecao de live             .blackboxcli/settings.json
ec5a703 erros gravacao de msg       .blackboxcli/settings.json

$ git ls-files --error-unmatch .kilo/plans/1788131684202-seguranca-remediacao.md
.kilo/plans/1788131684202-seguranca-remediacao.md      # rastreado no HEAD

$ git show HEAD:.kilo/plans/1788131684202-seguranca-remediacao.md | grep -n 'sk-vT8'
14: ... contém `Authorization: Bearer sk-vT8X5HWOY1UfOoxYtsPkrA`.
36: 1. **Revogar a chave Blackbox** ... (`sk-vT8X5HWOY1UfOoxYtsPkrA`)
```

**Motivo:** a chave não corresponde a nenhuma regra específica de provedor no ruleset padrão (Blackbox não está catalogado) e a regra genérica `generic-api-key` exige relação de entropia/palavra-chave que `Authorization: Bearer sk-...` dentro de texto corrido não satisfaz.

> **Conclusão metodológica:** varredura automática de segredos **não substitui** auditoria do histórico. Neste repositório o scanner padrão deu verde com um segredo real presente em 4 commits e no HEAD. Também é necessário excluir diretórios de artefatos da própria auditoria do escopo do scanner: o modo `dir` reportou 16 "achados" que eram URLs dentro do meu próprio `govulncheck.json` (ruído).

## 2. Segredos efetivamente confirmados no histórico (evidência manual)

### S-01 — [ALTA / potencialmente Crítica] Chave de API Blackbox exposta no histórico **e no HEAD**

Blob `57ee1372` (`.blackboxcli/settings.json`, commits `ec5a703` e `4991b3a`):

```json
{"mcpServers":{"remote-code":{"httpUrl":"https://cloud.blackbox.ai/api/mcp",
  "headers":{"Authorization":"Bearer sk-vT8X5HWOY1UfOoxYtsPkrA"},
  "description":"...Remote execution platform... automates coding tasks on your GitHub
   repositories... Choose from Claude Code, OpenAI Codex CLI, Blackbox CLI, or Gemini...
   GitHub Integration: Manage your GitHub token connections... Secure Execution: Runs code
   in isolated Vercel sandboxes..."}}}
```

Agravantes:
1. A chave **não foi revogada** desde a auditoria anterior (o plano em `.kilo/plans/` continua listando isso como tarefa pendente).
2. Está **no HEAD**, em texto claro, num arquivo rastreado — não basta reescrever o histórico, o arquivo atual também expõe.
3. O serviço credenciado é um **MCP de execução remota de código com integração ao GitHub**: nas mãos de terceiros, permite executar código em sandbox, criar branches/commits/PRs e manipular tokens do GitHub. O impacto real é **comprometimento da cadeia de desenvolvimento**, muito acima de um vazamento de credencial comum.

### S-02 — [ALTA] `feedback.db` (dados pessoais de participantes) em 19 commits

```
$ git log --all --oneline -- feedback.db | wc -l   → 19
$ git cat-file -s 3af653b9...                      → 1589248  (1,5 MB)
```

Banco SQLite com `uniqueId`, nicknames, mensagens e presentes de participantes reais, presente em **19 commits**. Hoje nenhum `.db` está rastreado no HEAD ✅, mas o conteúdo segue recuperável em qualquer clone — inclusive os nomes de exibição e IDs de usuários do TikTok de terceiros (LGPD: dados de pessoas que nunca consentiram com isso).

### S-03 — [OK] Nenhum arquivo `.env` real foi commitado

```
$ for f in .env .env.local .env.admin-password .env.supabase-password; do git log --all --oneline -- $f | wc -l; done
0 0 0 0
$ git rev-list --all --objects | grep -E '\.env($|\.)' | grep -v example
(vazio)
```

Somente `.env.example` e `.env.production.example` (placeholders) aparecem no histórico. ✅

### S-04 — [OK] Settings de agentes no histórico são inócuos

Blobs `.qwen/settings.json` (permissões de agente) e `mobile/.claude/settings.json` (plugin Expo) inspecionados manualmente: sem credenciais.

## 3. Recomendações

1. **Revogar `sk-vT8X5HWOY1UfOoxYtsPkrA` agora** — é a única mitigação definitiva; reescrever histórico não invalida clones/fork já existentes.
2. Redigir a chave no arquivo do HEAD (`.kilo/plans/1788131684202-seguranca-remediacao.md`) **antes** de qualquer novo commit.
3. Reescrever o histórico (`git filter-repo`/BFG) purgando `.blackboxcli/settings.json` e `feedback.db`, com coordenação de force-push — item que já estava planejado e nunca executado.
4. Cultura: nunca registrar o valor de um segredo dentro de um documento de remediação versionado.
5. CI: `gitleaks git` **mais** regras customizadas para provedores internos, **e** varredura de `git log -G` para padrões conhecidos — como demonstrado, o ruleset padrão falha.
