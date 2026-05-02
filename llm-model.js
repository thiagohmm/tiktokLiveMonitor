/**
 * GGUF padrão: Gemma 4 E2B IT (Q4_K_M ~3,2 GB). Menor variante Gemma 4; llama-server usa o template do GGUF.
 * Outras variantes: bartowski/google_gemma-4-E4B-it-GGUF, google_gemma-4-31B-it-GGUF, etc.
 */
module.exports = {
    GGUF_FILENAME: 'google_gemma-4-E2B-it-Q4_K_M.gguf',
    DOWNLOAD_URL:
        'https://huggingface.co/bartowski/google_gemma-4-E2B-it-GGUF/resolve/main/google_gemma-4-E2B-it-Q4_K_M.gguf'
};
