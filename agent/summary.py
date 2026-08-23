"""Geração de resumo da live a partir do buffer.

O modelo de linguagem e o buffer são injetados (DIP).
"""

from . import llm
from .buffer import LiveBuffer
from .context import ContextBuilder

_SYSTEM = (
    "Você é um assistente de live do TikTok. Resuma a live de forma direta e concisa, "
    "em português do Brasil, usando apenas os dados fornecidos."
)

_USER_TEMPLATE = (
    "DADOS DA LIVE:\n{context}\n\n"
    "Escreva um resumo estruturado com as seções: Visão geral, Repetições, Presentes e Moderação."
)


class LiveSummarizer:
    """Gera o resumo estruturado da live."""

    def __init__(self, model: llm.ChatModel, buffer: LiveBuffer):
        self._model = model
        self._context = ContextBuilder(buffer)

    async def summarize(self, max_tokens: int = 512) -> str:
        context = self._context.build()
        messages = [
            {"role": "system", "content": _SYSTEM},
            {"role": "user", "content": _USER_TEMPLATE.format(context=context)},
        ]
        return await self._model.chat(messages, max_tokens=max_tokens)
