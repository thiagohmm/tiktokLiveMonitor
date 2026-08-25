"""Configuração do agente a partir de variáveis de ambiente."""

import os

_PROJECT_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def _env(name, default):
    value = os.environ.get(name)
    if value is None or value.strip() == "":
        return default
    return value.strip()


def _env_int(name, default):
    try:
        return int(_env(name, str(default)))
    except ValueError:
        return default


def _env_path(name, default):
    """Caminho via variável de ambiente; relativo é resolvido na raiz do projeto."""
    value = _env(name, default)
    return value if os.path.isabs(value) else os.path.join(_PROJECT_ROOT, value)


AGENT_HOST = _env("AGENT_HOST", "127.0.0.1")
AGENT_PORT = _env_int("AGENT_PORT", 9001)
MONITOR_URL = _env("MONITOR_URL", "http://127.0.0.1:3001").rstrip("/")
LLM_URL = _env("LLM_URL", "http://127.0.0.1:8080/v1").rstrip("/")
LLM_TIMEOUT = _env_int("LLM_TIMEOUT", 120)

# --- Worker LLM (llama-server, spawnado e dono do Python) ---
LLM_PORT = _env_int("LLM_PORT", 8080)
LLM_BIND = _env("LLAMA_SERVER_BIND", "")  # vazio: 127.0.0.1 (0.0.0.0 em container)
LLM_BIN_DIR = _env_path("LLAMA_BIN_DIR", "bin")
MODELS_DIR = _env_path("MODELS_DIR", "models")
MODEL_CONFIG = _env_path("MODEL_CONFIG", "model-config.json")
LLM_USE_MMAP = _env("LLAMA_USE_MMAP", "0") == "1"
LLM_CTX_SIZE = _env_int("LLAMA_CTX_SIZE", 16384)

# --- Feedback (feedback.db, dono do Python) ---
FEEDBACK_DB = _env_path("FEEDBACK_DB", "feedback.db")

# --- Embeddings (fastembed/ONNX) ---
FASTEMBED_MODEL = _env(
    "FASTEMBED_MODEL", "sentence-transformers/paraphrase-multilingual-MiniLM-L12-v2"
)
FASTEMBED_CACHE = _env_path("FASTEMBED_CACHE", "models/embeddings")

# --- RAG / moderação ---
RAG_TOP_K = _env_int("RAG_TOP_K", 8)
RAG_BACKFILL_LIMIT = _env_int("RAG_BACKFILL_LIMIT", 500)
RAG_CONCURRENCY = _env_int("RAG_CONCURRENCY", 1)
RAG_TIMEOUT = _env_int("RAG_TIMEOUT", 8)
RAG_CHAT_INDEX_ENABLED = _env("RAG_CHAT_INDEX_ENABLED", "1") == "1"
