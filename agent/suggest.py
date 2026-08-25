"""Geração de respostas sugeridas para perguntas do chat ao vivo.

O Go continua detectando candidatos; este módulo só gera a resposta curta
via LLM (DIP: depende de ``ChatModel``).
"""

from __future__ import annotations

import logging

log = logging.getLogger("agent.suggest")

_SYSTEM = (
    "Você é um moderador de transmissões ao vivo (TikTok Live). "
    "Responda de forma curta, cordial e útil as perguntas do público. "
    "Se a mensagem não for uma pergunta real ou não valer resposta, "
    "responda exatamente NAO."
)

_REASON = "pergunta identificada como relevante"


def build_user_prompt(question: str, nickname: str | None = None) -> str:
    who = f" de {nickname}" if nickname else ""
    return (
        f"Pergunta recebida ao vivo{who}: {question!r}\n\n"
        "Dê uma resposta curta (até 2 frases), cordial e direta, em português (br)."
    )


class SuggestionService:
    """Gera uma sugestão de resposta para uma pergunta do chat."""

    def __init__(self, model):
        self._model = model

    async def suggest(self, question: str, nickname: str | None = None) -> dict | None:
        q = (question or "").strip()
        if not q:
            return None
        messages = [
            {"role": "system", "content": _SYSTEM},
            {"role": "user", "content": build_user_prompt(q, nickname)},
        ]
        try:
            raw = await self._model.chat(messages, max_tokens=120, temperature=0.3)
        except Exception as exc:  # noqa: BLE001
            log.warning("falha ao gerar sugestão: %s", exc)
            return None
        suggested = (raw or "").strip()
        if not suggested or suggested.upper() == "NAO":
            return None
        return {"suggested": suggested, "reason": _REASON}
