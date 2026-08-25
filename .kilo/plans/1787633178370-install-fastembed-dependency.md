# Plano: Instalar dependência `fastembed` no Python nativo

## Problema

O monitor roda nativamente no macOS e o Go (`main.go:58` → `internal/agent/agent.go:82`)
spawna o agente Python com `python3 -m agent`, resolvendo para
`/usr/local/bin/python3` (Python 3.13 do python.org, universal2/arm64).

`fastembed` está no `requirements.txt`, mas esse requisito só é aplicado no build
Docker (`Dockerfile:63`). No ambiente nativo o pacote não está instalado, então cada
comentário dispara:

```
[agent] WARNING agent.moderate: embedding indisponível: No module named 'fastembed'
```

Consequência funcional: a moderação cai para o fallback de regras regex; o RAG + LLM
semântico fica silenciosamente desligado. Nenhuma mudança de código será feita (escopo
acordado: só instalar a dependência).

## Fatos do ambiente

- SO/arch: macOS arm64 (Apple Silicon).
- Python do agente: `/usr/local/bin/python3` → `.../Python.framework/Versions/3.13/bin/python3`
  (universal2, roda nativo arm64). Não é externamente gerenciado (sem PEP 668), então
  `pip install` funciona sem `--break-system-packages`.
- Não há venv no projeto (sem `pyvenv.cfg`).
- `models/embeddings/` ainda não existe (o modelo ainda não foi baixado) e está no
  `.gitignore`.

## Passos (ordem de execução)

1. **Confirmar o gap (opcional)**
   ```sh
   python3 -c "import fastembed"   # esperado: ModuleNotFoundError
   ```

2. **Instalar a dependência** no mesmo Python que o agente usa
   ```sh
   python3 -m pip install --upgrade "fastembed>=0.4,<0.6"
   ```
   Isso instala `fastembed` + dependências (`onnxruntime`, `numpy`, `onnx`, `tokenizers`,
   `huggingface-hub`, etc.). Alternativa equivalente e mais completa (reinstala o restante
   do `requirements.txt` de forma idempotente):
   ```sh
   python3 -m pip install -r requirements.txt
   ```

3. **Validar o import e o runtime**
   ```sh
   python3 -c "import fastembed, onnxruntime; print(fastembed.__version__, onnxruntime.__version__)"
   ```
   Deve imprimir versões sem erro. Se `onnxruntime` falhar ao importar, ver a seção Riscos.

4. **Pré-baixar o modelo de embedding (recomendado, requer acesso a HuggingFace)**
   ```sh
   python3 -c "from fastembed import TextEmbedding; m = TextEmbedding(model_name='sentence-transformers/paraphrase-multilingual-MiniLM-L12-v2', cache_dir='models/embeddings'); v = list(m.embed(['teste']))[0]; print('dims=', len(v))"
   ```
   Esperado: `dims= 384`. Sem esse passo, o download acontece no primeiro comentário
   (primeira moderação fica mais lenta).

5. **Reiniciar o monitor** para o agente re-spawnar e o backfill rodar limpo
   (o backfill de `feedback`/`anomaly`/`chat` só roda no startup do agente — `agent/api.py:93`).
   O agente vivo também passaria a importar o módulo na próxima chamada, mas o restart
   garante estado limpo e re-executa o backfill.

## Validação

- Log do agente deixa de mostrar `embedding indisponível: No module named 'fastembed'`.
- `curl -X POST http://127.0.0.1:9001/moderate -H 'Content-Type: application/json' -d '{"comment":"..."}'`
  passa a retornar categoria via RAG+LLM (não só regras).
- Log de startup mostra `backfill concluído: N itens processados` (prova que o embedding
  e o índice vetorial estão ativos).

## Riscos

- **Wheel do `onnxruntime` para arm64 + Python 3.13**: disponível a partir do
  onnxruntime 1.20. Se o pip baixar uma versão sem wheel arm64, fixar
  `pip install "onnxruntime>=1.20"`. Se persistir, considerar um venv dedicado
  (`python3 -m venv .venv`) e apontar o PATH do agente para ele.
- **Rede**: precisa de acesso a PyPI (instalação) e a HuggingFace (download do modelo,
  ~100–500 MB). Em ambiente offline, baixar o modelo noutra máquina e copiar para
  `models/embeddings/`.
- **Espaço em disco**: o modelo MiniLM-L12 ocupará algumas centenas de MB em
  `models/embeddings/`.

## Fora de escopo

- Mudanças em código (probe de disponibilidade no startup, rate-limit do warning).
- Docker / deploy no Raspberry Pi (o `Dockerfile` já instala `requirements.txt`).
