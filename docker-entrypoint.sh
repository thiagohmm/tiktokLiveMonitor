#!/bin/sh
set -e

# Get the model filename from the node config
MODEL_FILENAME=$(node -e "console.log(require('./llm-model.js').GGUF_FILENAME)")
MODEL="/app/models/$MODEL_FILENAME"

ARCH="$(uname -m)"
case "$ARCH" in
    x86_64) SETUP_ARCH=x64 ;;
    aarch64 | arm64) SETUP_ARCH=arm64 ;;
    *)
        echo "[docker] AVISO: arquitetura $ARCH — usando linux x64 no setup."
        SETUP_ARCH=x64
        ;;
    esac

needs_setup=false
if [ ! -s "$MODEL" ]; then
    needs_setup=true
fi
# Volume só com GGUF e sem binário = precisa rodar setup de novo
if ! find /app/bin -type f \( -name llama-server -o -name llama-server.exe \) 2>/dev/null | grep -q .; then
    needs_setup=true
fi

if [ "$needs_setup" = true ]; then
    echo "[docker] Baixando GGUF + llama-server (Linux $SETUP_ARCH). Pode levar vários minutos."
    node scripts/setup-llm.js linux "$SETUP_ARCH" || {
        echo "[docker] Setup LLM falhou; HTTP sobe sem IA local (só regex)."
    }
fi

# Volume montado às vezes perde bit de execução
find /app/bin -type f -name llama-server -exec chmod +x {} \; 2>/dev/null || true

# Sobe o llama-server antes do Node aceitar tráfego → primeira conexão já usa LLM
echo "[docker] Pré-carregando llama-server…"
node -e "
require('./ai').probeLlamaReady().then(function (ok) {
  console.log(ok ? '[docker] IA local carregada e respondendo.' : '[docker] IA local indisponível (verifique logs acima).');
  process.exit(0);
}).catch(function (e) {
  console.warn('[docker] IA local não iniciada:', e && e.message ? e.message : e);
  process.exit(0);
});
"

exec "$@"
