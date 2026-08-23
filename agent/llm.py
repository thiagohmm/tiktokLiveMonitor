"""Cliente do LLM (llama-server, endpoint OpenAI-compatível).

Define um contrato abstrato (``ChatModel``) para que o restante do pacote
dependa da abstração e não da implementação HTTP (DIP).
"""

import asyncio
import logging
from typing import Protocol, runtime_checkable

import httpx

from . import config

log = logging.getLogger("agent.llm")

_COMPLETIONS_PATH = "/chat/completions"


@runtime_checkable
class ChatModel(Protocol):
    """Contrato mínimo para um modelo de linguagem (ISP)."""

    async def chat(self, messages, max_tokens=512, temperature=0.1) -> str:
        """Envia uma conversa e retorna o texto da resposta."""


class LlamaServerChatModel:
    """Implementação de ``ChatModel`` baseada no llama-server."""

    def __init__(self, base_url=None, timeout=None):
        # DIP: os valores vêm por injeção, com default vindo de config.
        self._base_url = (base_url or config.LLM_URL).rstrip("/")
        self._timeout = timeout or config.LLM_TIMEOUT

    async def chat(self, messages, max_tokens=512, temperature=0.1) -> str:
        payload = {
            "messages": messages,
            "temperature": temperature,
            "max_tokens": max_tokens,
            "stream": False,
            "chat_template_kwargs": {"enable_thinking": False},
        }
        url = self._base_url + _COMPLETIONS_PATH

        last_err = None
        for attempt in range(3):
            try:
                async with httpx.AsyncClient(timeout=self._timeout) as client:
                    resp = await client.post(url, json=payload)
                    resp.raise_for_status()
                    data = resp.json()
                choices = data.get("choices") or []
                if not choices:
                    return ""
                content = choices[0].get("message", {}).get("content") or ""
                return content.strip()
            except Exception as exc:  # noqa: BLE001
                last_err = exc
                log.warning("LLM indisponível (tentativa %d/3): %s", attempt + 1, exc)
                await asyncio.sleep(2 * (attempt + 1))

        raise RuntimeError(f"LLM indisponível: {last_err}")


# Instância padrão para conveniência (ex.: uso fora do servidor).
default = LlamaServerChatModel()


async def chat(messages, max_tokens=512, temperature=0.1) -> str:
    """Função de conveniência usando a implementação padrão."""
    return await default.chat(messages, max_tokens=max_tokens, temperature=temperature)