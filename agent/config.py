"""Configuração do agente a partir de variáveis de ambiente."""

import os


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


AGENT_HOST = _env("AGENT_HOST", "127.0.0.1")
AGENT_PORT = _env_int("AGENT_PORT", 9001)
MONITOR_URL = _env("MONITOR_URL", "http://127.0.0.1:3001").rstrip("/")
LLM_URL = _env("LLM_URL", "http://127.0.0.1:8080/v1").rstrip("/")
LLM_TIMEOUT = _env_int("LLM_TIMEOUT", 120)
