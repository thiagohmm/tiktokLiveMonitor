"""Perguntas à live com contexto e conversa contínua — absorção de ``internal/service/ai.go``.

- ``ConversationStore``: cache de mensagens por sessão (pergunta/resposta),
  equivalente ao suporte de histórico do ``AskAI``.
- ``AskAIService``: monta o contexto (buffer ao vivo + presentes e histórico
  persistentes via ``MonitorClient``) e gera a resposta com o LLM.
"""

import json
import logging
from collections import OrderedDict, deque

from . import llm
from .buffer import LiveBuffer
from .context import ContextBuilder
from .tools import MonitorClient

log = logging.getLogger("agent.history")

MAX_SESSION_MESSAGES = 20
MAX_SESSIONS = 100

_SYSTEM_TEMPLATE = (
    "Você é o assistente de uma live do TikTok. Responda a pergunta EXCLUSIVAMENTE com base nos dados abaixo.\n\n"
    "REGRAS:\n"
    "1. Vá direto ao ponto. Nunca cumprimente e nunca diga que é um assistente.\n"
    "2. Se a pergunta menciona um usuário, relate TUDO dele da seção de perfil completo: presença, mensagens e presentes.\n"
    "3. Se os dados não contêm a informação pedida, responda apenas: "
    '"Não encontrei dados sobre isso na live."\n\n'
    "DADOS DA LIVE:\n"
    "{context}\n\n"
    "Responda em português do Brasil, de forma direta e concisa."
)


def _json(data) -> str:
    return json.dumps(data, ensure_ascii=False, indent=2)


class ConversationStore:
    """Cache de mensagens de conversa por sessão (pergunta/resposta)."""

    def __init__(self, max_messages: int = MAX_SESSION_MESSAGES, max_sessions: int = MAX_SESSIONS):
        self._max_messages = max_messages
        self._max_sessions = max_sessions
        self._sessions: "OrderedDict[str, deque]" = OrderedDict()

    def messages(self, session: str = "default") -> list[dict]:
        return list(self._sessions.get(session, ()))

    def append(self, session: str, role: str, content: str) -> None:
        content = (content or "").strip()
        if not content:
            return
        entries = self._sessions.setdefault(session, deque(maxlen=self._max_messages))
        entries.append({"role": role, "content": content})
        self._sessions.move_to_end(session)
        while len(self._sessions) > self._max_sessions:
            self._sessions.popitem(last=False)

    def clear(self, session: str | None = None) -> None:
        if session is None:
            self._sessions.clear()
        else:
            self._sessions.pop(session, None)


class AskAIService:
    """Responde perguntas sobre a live com contexto e histórico de conversa."""

    def __init__(
        self,
        model: llm.ChatModel,
        monitor: MonitorClient | None = None,
        buffer: LiveBuffer | None = None,
        conversations: ConversationStore | None = None,
    ):
        self._model = model
        self._monitor = monitor or MonitorClient()
        self._buffer = buffer
        self._context = ContextBuilder(buffer) if buffer is not None else None
        self._conversations = conversations or ConversationStore()

    @property
    def conversations(self) -> ConversationStore:
        return self._conversations

    async def _live_context(self) -> str:
        """Contexto da live: buffer ao vivo + dados persistentes do monitor."""
        parts = []
        if self._context is not None:
            parts.append(self._context.build())
        try:
            gifts = await self._monitor.gifts()
            parts.append(f"PRESENTES (HISTÓRICO PERSISTENTE):\n{_json(gifts)}")
        except Exception as exc:  # noqa: BLE001
            log.warning("Falha ao buscar presentes: %s", exc)
        try:
            history = await self._monitor.history()
            parts.append(f"HISTÓRICO DE MODERAÇÃO:\n{_json(history)}")
        except Exception as exc:  # noqa: BLE001
            log.warning("Falha ao buscar histórico de moderação: %s", exc)
        return "\n\n".join(p for p in parts if p)

    async def ask(self, question: str, session: str = "default") -> str:
        question = (question or "").strip()
        context = await self._live_context()
        system = _SYSTEM_TEMPLATE.format(context=context or "(nenhum dado registrado na live)")

        messages = [{"role": "system", "content": system}]
        messages.extend(self._conversations.messages(session))
        messages.append({"role": "user", "content": question})

        answer = await self._model.chat(messages, max_tokens=512)
        self._conversations.append(session, "user", question)
        self._conversations.append(session, "assistant", answer)
        return answer
